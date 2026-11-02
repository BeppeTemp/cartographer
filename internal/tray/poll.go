package tray

import (
	"context"
	"errors"
	"net"
	"net/http"
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
	// AgentsEvery is how many polls apart the agent clients are re-read: their
	// state reads files and asks the server, and changes only on a sync.
	AgentsEvery = 12
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
	// Agents reads the agent clients (`cartographer status`); optional. Called
	// on the first poll, every AgentsEvery polls and when the server returns.
	Agents func() Agents
}

// NewDeps wires the production implementation: statusFn is
// Manager.Status on the effective config path, the probe is
// client.MCPClient.Health.
func NewDeps(statusFn func() (service.Status, error), logPath string) Deps {
	return Deps{
		Status:  statusFn,
		Probe:   probeHealth,
		LogPath: logPath,
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
	var agents Agents
	polls := 0
	poll := func() {
		snap := d.snapshot(&good)
		if d.Agents != nil {
			back := have && last.State != StateRunning && snap.State == StateRunning
			if polls%AgentsEvery == 0 || back {
				agents = d.Agents()
			}
			polls++
			snap.Agents = agents
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
func (d Deps) snapshot(good **service.Status) Snapshot {
	st, err := d.Status()
	var health *client.Health
	if err != nil {
		d.logf("tray: service status: %v", err)
		if *good != nil {
			st, err = **good, nil
		} else {
			st = service.Status{Installed: true} // see above: not "not installed"
		}
	} else {
		cp := st
		*good = &cp
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
