package tray

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/service"
)

const (
	// DefaultInterval is how often the server is probed.
	DefaultInterval = 5 * time.Second
	// MinInterval is the smallest interval `--interval` accepts.
	MinInterval  = time.Second
	probeTimeout = 2 * time.Second
	// AgentsInterval is how often the agent clients are re-read: that is the
	// `cartographer status` path, which reads files and asks the server, and
	// its answer changes only on a sync.
	AgentsInterval = 5 * time.Minute
)

// Deps is what Run needs from the outside; tests replace every field.
type Deps struct {
	// Status resolves the service definition and its HTTP address. It is
	// called on every tick: the service may be reinstalled with another
	// --http while the tray runs.
	Status func() (service.Status, error)
	// Probe fetches /health from the HTTP address (host:port).
	Probe func(addr string) (*client.Health, error)
	// NewTicker returns the tick channel and its stop function.
	NewTicker func(d time.Duration) (<-chan time.Time, func())
	// Busy reports whether a menu-requested restart is in flight; optional.
	Busy func() bool
	// LogPath is copied into every snapshot.
	LogPath string
	// Logf receives probe-side problems; optional.
	Logf func(format string, args ...any)
	// Agents reads the agent clients (`cartographer status`); optional. It
	// runs off the poll loop, at start, every AgentsInterval worth of polls
	// and when the server comes back; a poll shows the newest result.
	Agents func() Agents
	// Client resolves the client config's server, on every tick; optional
	// (nil = local mode only). It is consulted only when no local service is
	// installed and ForceLocal is off. Present false (no client config file)
	// keeps the local path.
	Client func() (ClientTarget, error)
	// ProbeClient fetches /health of the client's server, with the client's
	// token and TLS rules; NewDeps sets the production probe.
	ProbeClient func(ClientTarget) (*client.Health, error)
	// ForceLocal keeps the local mode whatever the client config says (an
	// explicit --config).
	ForceLocal bool
}

// NewDeps wires the production implementation: statusFn is
// Manager.TrayView on the effective config path (no scheduler command), the probe is
// client.MCPClient.Health.
func NewDeps(statusFn func() (service.Status, error), logPath string) Deps {
	return Deps{
		Status:      statusFn,
		Probe:       probeHealth,
		ProbeClient: probeClient,
		LogPath:     logPath,
		NewTicker: func(d time.Duration) (<-chan time.Time, func()) {
			t := time.NewTicker(d)
			return t.C, t.Stop
		},
	}
}

// probeHealth reads /health of the server at addr. The tray carries no token:
// /health is exempt from authentication.
func probeHealth(addr string) (*client.Health, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1" // a wildcard bind is reached on loopback
	}
	c := &client.MCPClient{
		ServerURL: "http://" + net.JoinHostPort(host, port) + "/mcp",
		// A server bound to one of this machine's own addresses is still
		// this machine; only /health is read.
		AllowInsecureHTTP: true,
		HTTP:              &http.Client{},
	}
	return c.Health(probeTimeout)
}

// probeClient reads /health of the client's server_url with that URL's own
// scheme and the client's rules (client.CheckServerURL inside Health): no
// forced http://, no loopback rewrite. The token travels in the Authorization
// header of this one request.
func probeClient(t ClientTarget) (*client.Health, error) {
	return client.New(t.ServerURL, t.Token).WithInsecureHTTP(t.AllowInsecureHTTP).Health(probeTimeout)
}

// Run probes every interval and calls emit with the first snapshot and then
// with each one that differs from the previous. It returns when ctx is done;
// a probe that fails is a Stopped (or NotInstalled) snapshot, never a panic or
// an exit.
func Run(ctx context.Context, d Deps, interval time.Duration, emit func(Snapshot)) {
	if interval < MinInterval {
		interval = MinInterval
	}
	ticks, stop := d.NewTicker(interval)
	defer stop()

	var last Snapshot
	have := false
	var good *service.Status // last Status() that succeeded
	var lastErr string       // the Status() error already logged; empty after a success
	cm := &clientMemo{}
	var mu sync.Mutex
	var agents Agents
	refresh := make(chan struct{}, 1)
	if d.Agents != nil {
		go func() { // never blocks the icon, nor Quit: Run does not wait for it
			for {
				select {
				case <-ctx.Done():
					return
				case <-refresh:
				}
				a := d.Agents()
				mu.Lock()
				agents = a
				mu.Unlock()
			}
		}()
	}
	every := max(1, int(AgentsInterval/interval))
	polls := 0
	poll := func() {
		snap := d.snapshot(&good, &lastErr, cm)
		if d.Agents != nil {
			back := have && last.State != StateRunning && snap.State == StateRunning
			if polls%every == 0 || back {
				select {
				case refresh <- struct{}{}:
				default: // a read is already queued
				}
			}
			polls++
			mu.Lock()
			snap.Agents = agents
			mu.Unlock()
		}
		if have && snap == last {
			return
		}
		last, have = snap, true
		emit(snap)
	}
	poll()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticks:
			poll()
		}
	}
}

// snapshot probes once. good holds the last Status that was read successfully:
// when Status() fails (a transient error, say a failing PowerShell call), that
// one is reused, so an installed service does not flip to "not installed" for a
// tick. With no earlier good read the answer is Stopped, never NotInstalled:
// an unreadable definition proves nothing about installation.
func (d Deps) snapshot(good **service.Status, lastErr *string, cm *clientMemo) Snapshot {
	st, err := d.Status()
	var health *client.Health
	if err != nil {
		// A permanent error would write a line every tick into a log nobody
		// rotates: report it once, again when the message changes or after a
		// success.
		if msg := err.Error(); msg != *lastErr {
			*lastErr = msg
			d.logf("tray: service status: %v", err)
		}
		if *good != nil {
			st, err = **good, nil
		} else {
			st = service.Status{Installed: true} // see above: not "not installed"
		}
	} else {
		cp := st
		*good = &cp
		*lastErr = ""
	}
	if !d.ForceLocal && d.Client != nil && !st.Installed {
		if snap, ok := d.clientSnapshot(cm); ok {
			return snap
		}
	}
	if err != nil {
		// no earlier good read: nothing to probe, classify as Stopped
	} else if st.HTTPAddr == "" {
		// Stdio transport or no readable config: there is nothing to probe.
		err = errors.New("no http address")
	} else {
		health, err = d.Probe(st.HTTPAddr)
	}
	if err == nil && health == nil {
		err = errors.New("empty health response")
	}
	snap := Classify(health, err, st)
	snap.LogPath = d.LogPath
	snap.Restarting = d.Busy != nil && d.Busy()
	return snap
}

// clientMemo is what the client mode remembers between ticks.
type clientMemo struct {
	good     *ClientTarget // last target that resolved
	loadErr  string        // the Client() error already logged
	probeErr string        // the refused-server_url error already logged
}

// clientSnapshot probes the client's server. ok is false when there is no
// client config file: the caller then takes the local path unchanged. A target
// that cannot be read keeps the last good one, or shows Stopped.
func (d Deps) clientSnapshot(cm *clientMemo) (Snapshot, bool) {
	t, err := d.Client()
	switch {
	case err != nil:
		if msg := err.Error(); msg != cm.loadErr {
			cm.loadErr = msg
			d.logf("tray: client config: %v", err)
		}
		if cm.good == nil {
			return Snapshot{Checked: true, KBCount: -1, Mode: ModeClient, State: StateStopped}, true
		}
		t = *cm.good
	case !t.Present:
		cm.good, cm.loadErr = nil, ""
		return Snapshot{}, false
	default:
		cp := t
		cm.good, cm.loadErr = &cp, ""
	}
	var health *client.Health
	if d.ProbeClient == nil {
		err = errors.New("no client probe")
	} else {
		health, err = d.ProbeClient(t)
	}
	var remote *client.RemoteError
	switch {
	case err != nil && !errors.As(err, &remote):
		// A server_url the client's rules refuse (remote plain http://, a bad
		// scheme, an unparsable URL). err is never logged: CheckServerURL
		// formats the raw URL into it, userinfo and query included. The line
		// is the redacted URL and a fixed reason.
		d.logProbeOnce(cm, fmt.Sprintf("tray: server_url %s: refused by the client's URL rules (remote plain http:// needs allow_insecure_http; https:// or loopback http:// only)", redactURL(t.ServerURL)))
	case err == nil && healthProblem(health) != "":
		d.logProbeOnce(cm, fmt.Sprintf("tray: server_url %s: %s: refused", redactURL(t.ServerURL), healthProblem(health)))
	default:
		cm.probeErr = ""
	}
	if err == nil && health == nil {
		err = errors.New("empty health response")
	}
	return ClassifyClient(health, err, t), true
}

// logProbeOnce writes a probe-side line once until the message changes or a
// probe passes.
func (d Deps) logProbeOnce(cm *clientMemo, msg string) {
	if msg != cm.probeErr {
		cm.probeErr = msg
		d.logf("%s", msg)
	}
}

func (d Deps) logf(format string, args ...any) {
	if d.Logf != nil {
		d.Logf(format, args...)
	}
}

// Latest returns a one-slot channel and an emit function for Run: a UI that is
// slow to apply a snapshot only ever sees the newest one. Single producer; the
// returned close function ends the stream and may only be called once emit
// will not be called again.
func Latest() (<-chan Snapshot, func(Snapshot), func()) {
	ch := make(chan Snapshot, 1)
	return ch, func(s Snapshot) {
		for {
			select {
			case ch <- s:
				return
			default:
			}
			select {
			case <-ch:
			default:
			}
		}
	}, func() { close(ch) }
}
