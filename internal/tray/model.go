// Package tray is the core of the local server's status icon (D1482): the health model of
// the local server, the poll loop that keeps it current and the generated
// monochrome icons. It draws nothing itself: the
// notification-area and menu-bar implementations of [UI] live in
// platform files, so everything here runs and is tested on any OS.
//
// The tray never writes server or KB state. It reads /health through the MCP
// client, and the actions it offers (a later change) go only through
// service.Manager and the operating system's "open".
package tray

import (
	"errors"
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
	// StateNotInstalled: no service definition is installed.
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

// Snapshot is everything the UI shows. It is comparable on purpose: the poll
// loop pushes a snapshot only when it differs from the previous one.
type Snapshot struct {
	State   State
	Version string
	// KBCount is the number of KBs the server reported, -1 when it did not
	// send the list (an older or single-KB server).
	KBCount int
	// KBNames are the KBs the server reported, newline-separated in its order
	// (a string keeps Snapshot comparable).
	KBNames string
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
	snap := Snapshot{KBCount: -1, UIURL: status.UIURL, Installed: status.Installed}
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
	case health.Ready != nil && !*health.Ready:
		snap.State = StateStarting
	default:
		snap.State = StateRunning
	}
	if err == nil && health != nil {
		snap.Version = health.Version
		snap.LatestVersion = health.LatestVersion
		if health.KBs != nil {
			snap.KBCount = len(*health.KBs)
			names := make([]string, 0, len(*health.KBs))
			for _, kb := range *health.KBs {
				names = append(names, kb.Name)
			}
			snap.KBNames = strings.Join(names, "\n")
		}
	}
	return snap
}
