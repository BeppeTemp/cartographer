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
	// LatestVersion is a newer release the server knows of (D254). It only
	// changes the status line, never the icon.
	LatestVersion string
	// UIURL is where the Atlas UI answers; empty when there is none.
	UIURL string
	// LogPath is the server log the menu opens.
	LogPath string
	// Restarting is true while a restart requested from the menu is in flight.
	Restarting bool
}

// Classify turns one probe result into a snapshot. health and err are the
// outcome of client.MCPClient.Health; status is the service definition's view.
//
// Order matters. A server that answers but is bootstrapping is Starting even
// when nothing is installed (someone runs it by hand). With no service
// installed and nothing answering, the honest state is NotInstalled, not
// Stopped: there is nothing the Restart item could start. Any other failure
// (refused connection, a non-200 answer, a 401 that /health cannot produce)
// is Stopped.
func Classify(health *client.Health, err error, status service.Status) Snapshot {
	snap := Snapshot{KBCount: -1, UIURL: status.UIURL}
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
	case !status.Installed:
		snap.State = StateNotInstalled
	default:
		snap.State = StateRunning
	}
	if err == nil && health != nil {
		snap.Version = health.Version
		snap.LatestVersion = health.LatestVersion
		if health.KBs != nil {
			snap.KBCount = len(*health.KBs)
		}
	}
	return snap
}
