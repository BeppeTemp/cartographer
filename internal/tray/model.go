// Package tray is the core of the status icon: the health model of
// the local server or, on a machine with no local service, of the server the
// client is connected to, the poll loop that keeps it current and the generated
// monochrome icons. It draws nothing itself: the
// notification-area and menu-bar implementations of [UI] live in
// platform files, so everything here runs and is tested on any OS.
//
// The tray never writes server or KB state. It reads /health through the MCP
// client, and the actions it offers go only through service.Manager and the
// operating system's "open"; in client mode it offers no service action.
package tray

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/service"
)

// State is what the icon shows. The states are carried by shape, never by
// colour: a macOS template image is tinted by the system (D1482).
type State int

const (
	// StateStopped: nothing answers /health.
	StateStopped State = iota
	// StateRunning: the server answers and is ready.
	StateRunning
	// StateStarting: the server answers but is still bootstrapping its KBs.
	StateStarting
	// StateNotInstalled: no service definition is installed and nothing
	// answers (local mode only; client mode has no such state).
	StateNotInstalled
)

func (s State) String() string {
	switch s {
	case StateStopped:
		return "stopped"
	case StateRunning:
		return "running"
	case StateStarting:
		return "starting"
	case StateNotInstalled:
		return "not installed"
	}
	return "unknown"
}

// Mode says which server a snapshot describes.
type Mode int

const (
	// ModeLocal: the local server of the native service (the zero value).
	ModeLocal Mode = iota
	// ModeClient: the server of the client config, read-only: no service to
	// restart and no local log.
	ModeClient
)

// Snapshot is everything the UI shows. It is comparable on purpose: the poll
// loop pushes a snapshot only when it differs from the previous one.
type Snapshot struct {
	State State
	// Checked is true once a probe produced this snapshot; the zero Snapshot,
	// shown before the first probe, reads "checking…" rather than "stopped".
	Checked bool
	// NotReady: the server answers /health with ready:false yet is not
	// bootstrapping (State is StateRunning), e.g. no KB mounted.
	NotReady bool
	Version  string
	// KBCount is the number of KBs the server reported, -1 when it did not
	// send the list (an older or single-KB server).
	KBCount int
	// KBNames are the KBs the server reported, newline-separated in its order
	// (a string keeps Snapshot comparable).
	KBNames string
	// PushLagKBs is how many KBs the server reports with a failed push or
	// commits unpushed past a few minutes; 0 when none or unknown.
	PushLagKBs int
	// LatestVersion is a newer release the server knows of (D254). It only
	// changes the status line, never the icon.
	LatestVersion string
	// UIURL is where the Atlas UI answers; empty when there is none.
	UIURL string
	// LogPath is the server log the menu opens.
	LogPath string
	// Installed is whether a service definition exists, so a UI can grey out
	// "Restart service" when there is nothing to restart.
	Installed bool
	// Restarting is true while a restart requested from the menu is in flight.
	Restarting bool
	// Agents is this machine's connected agent clients and whether their
	// materialized files match the server, refreshed less often than /health.
	Agents Agents
	// Mode is which server this snapshot describes.
	Mode Mode
	// Server is the host (host:port) of the client's server, in ModeClient.
	Server string
	// TokenMissing (ModeClient): the client config asks for a token and none
	// resolved, so the probe was anonymous and the KB list is absent.
	TokenMissing bool
	// Rejected (ModeClient): the server answered 401 or 403 to the client
	// token, so it is up but this client may not read it.
	Rejected bool
}

// KBs splits KBNames.
func (s Snapshot) KBs() []string {
	if s.KBNames == "" {
		return nil
	}
	return strings.Split(s.KBNames, "\n")
}

// MaxAgents bounds the agent rows (an array keeps Snapshot comparable).
const MaxAgents = 8

// AgentRow is one connected agent client.
type AgentRow struct {
	Name string // display name, e.g. "Claude Code"
	Sync Sync
}

// Sync is whether an agent client's materialized files match the server.
type Sync int

const (
	SyncUnknown Sync = iota // not checked: the server or the lockfile could not be read
	SyncOK
	SyncDrift
)

// Agents is the client side of `cartographer status`, kept comparable.
type Agents struct {
	Known bool // read at least once
	N     int  // how many of Rows are set
	Rows  [MaxAgents]AgentRow
}

// Classify turns one probe result into a snapshot. health and err are the
// outcome of client.MCPClient.Health; status is the service definition's view.
//
// Order matters. A server that answers is Running (or Starting while it
// bootstraps) whether or not a service is installed: someone may run it by
// hand, and hiding it would be a lie; Snapshot.Installed tells the UI whether
// Restart has anything to act on. NotInstalled is only "nothing installed and
// nothing answering". Any other failure (refused connection, a non-200 answer,
// a 401 that /health cannot produce) is Stopped.
func Classify(health *client.Health, err error, status service.Status) Snapshot {
	snap := Snapshot{Checked: true, KBCount: -1, UIURL: status.UIURL, Installed: status.Installed}
	var remote *client.RemoteError
	starting := errors.As(err, &remote) && remote.Code == client.CodeStarting
	switch {
	case starting:
		snap.State = StateStarting
	case err != nil || health == nil:
		snap.State = StateStopped
		if !status.Installed {
			snap.State = StateNotInstalled
		}
	default:
		// A bootstrapping server is the CodeStarting error above; ready:false
		// without it is a running server that is not ready (#1491).
		snap.State = StateRunning
		snap.NotReady = health.Ready != nil && !*health.Ready
	}
	snap.fillHealth(health, err)
	return snap
}

// fillHealth copies what an answering server reported: the one place the
// version and KB mapping is written, for both modes.
func (snap *Snapshot) fillHealth(health *client.Health, err error) {
	if err != nil || health == nil {
		return
	}
	snap.Version = health.Version
	snap.LatestVersion = health.LatestVersion
	if health.KBs != nil {
		snap.KBCount = len(*health.KBs)
		names := make([]string, 0, len(*health.KBs))
		for _, kb := range *health.KBs {
			names = append(names, kb.Name)
		}
		snap.KBNames = strings.Join(names, "\n")
		for _, kb := range *health.KBs {
			if kb.Push != nil {
				snap.PushLagKBs++
			}
		}
	}
}

// ClassifyClient turns one probe of the client's server into a snapshot. Only
// a CodeStarting answer is Starting; every other failure (unreachable, a
// refused server_url, 5xx, TLS) is Stopped, shown as unreachable. A running
// server is described like Classify does, but nothing is installed locally.
func ClassifyClient(health *client.Health, err error, target ClientTarget) Snapshot {
	snap := Snapshot{Checked: true, KBCount: -1, Mode: ModeClient}
	if u, uerr := client.ServerBase(target.ServerURL); uerr == nil {
		snap.Server = u.Host
	}
	if ui, uerr := client.AtlasURL(target.ServerURL); uerr == nil {
		snap.UIURL = ui
	}
	var remote *client.RemoteError
	if err == nil && healthProblem(health) != "" {
		health = nil // an answer that would forge menu text: shown as unreachable
	}
	switch {
	case errors.As(err, &remote) && remote.Code == client.CodeStarting:
		snap.State = StateStarting
	case errors.As(err, &remote) && (remote.Code == client.CodeUnauthorized || remote.HTTPStatus == 401 || remote.HTTPStatus == 403):
		snap.State = StateStopped
		snap.Rejected = true
	case err != nil || health == nil:
		snap.State = StateStopped
	default:
		snap.State = StateRunning
		snap.NotReady = health.Ready != nil && !*health.Ready
		snap.TokenMissing = target.Auth && target.Token == ""
	}
	snap.fillHealth(health, err)
	return snap
}

// healthProblem says why a remote /health answer is refused, "" when it is
// fine. The version, the latest version and every KB name end up in menu rows
// and status text, and the KB names are joined with "\n" in Snapshot.KBNames:
// a control rune (a newline above all) in any of them could forge a row or
// reach an NSMenuItem or Win32 title, so such an answer is not shown at all.
// Because of this check no name in a client-mode snapshot contains "\n", and
// KBs() cannot split one name into two rows (Snapshot stays comparable, which
// a slice would break).
func healthProblem(h *client.Health) string {
	if h == nil {
		return ""
	}
	if client.HasControl(h.Version) || client.HasControl(h.LatestVersion) {
		return "the server advertised a version with control characters"
	}
	if h.KBs != nil {
		for _, kb := range *h.KBs {
			if client.HasControl(kb.Name) {
				return "the server advertised a KB name with control characters"
			}
		}
	}
	return ""
}

// ClientTarget is the server a client-mode tray reads: the client config,
// resolved on every tick. Present is false when the client config file does
// not exist (then the tray stays in local mode).
//
// TRAP: Token is the client's bearer token. It goes in the Authorization
// header of the one /health request and nowhere else: never in a Snapshot, a
// menu row, a log line or an error. String and GoString redact it so a stray
// %v or %+v cannot leak it; TestClientTargetNeverPrintsTheToken checks it.
type ClientTarget struct {
	Present           bool
	ServerURL         string
	Token             string
	AllowInsecureHTTP bool
	Auth              bool
}

// String redacts the token.
func (t ClientTarget) String() string {
	tok := "none"
	if t.Token != "" {
		tok = "<redacted>"
	}
	return fmt.Sprintf("ClientTarget{present: %t, server: %s, token: %s}", t.Present, redactURL(t.ServerURL), tok)
}

// GoString redacts the token for %#v.
func (t ClientTarget) GoString() string { return t.String() }

// redactURL hides the userinfo and the query of a URL for a log line.
func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "<unparsable>"
	}
	u.RawQuery, u.Fragment = "", ""
	return u.Redacted()
}
