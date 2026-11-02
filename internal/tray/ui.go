package tray

import (
	"fmt"
	"strings"
)

// Action is a menu entry the user chose.
type Action int

const (
	ActionOpenAtlas Action = iota
	ActionRestart
	ActionOpenLog
	ActionQuit
)

// Menu carries the text of the fixed menu entries, so both platforms say the
// same words.
type Menu struct {
	OpenAtlas string
	Restart   string
	OpenLog   string
	Quit      string
}

// DefaultMenu is the English menu.
func DefaultMenu() Menu {
	return Menu{OpenAtlas: "Open Atlas", Restart: "Restart service", OpenLog: "Open log", Quit: "Quit"}
}

// UI is one platform's notification-area or menu-bar implementation. Run blocks
// until the user quits, the UI fails, or updates is closed (the process was
// asked to stop: Run must then tear its icon down and return), applies each snapshot received on
// updates and calls onAction for each chosen entry. Every call into the OS
// toolkit must stay on the thread that owns it: updates cross over through the
// channel, never by calling the toolkit from the poll goroutine. onAction is
// called on the UI thread and must return promptly; long work (a restart) runs
// in its own goroutine.
type UI interface {
	Run(menu Menu, updates <-chan Snapshot, onAction func(Action)) error
}

// StatusLine is the first, disabled line of the menu and the tooltip.
func StatusLine(s Snapshot) string {
	if s.Restarting {
		return "restarting…"
	}
	switch s.State {
	case StateStopped:
		return "stopped"
	case StateStarting:
		return "starting…"
	case StateNotInstalled:
		return "not installed"
	}
	parts := []string{"Cartographer"}
	if s.Version != "" {
		parts[0] += " " + s.Version
	}
	if s.KBCount >= 0 {
		noun := "KBs"
		if s.KBCount == 1 {
			noun = "KB"
		}
		parts = append(parts, fmt.Sprintf("%d %s", s.KBCount, noun))
	}
	if s.LatestVersion != "" {
		v := s.LatestVersion
		if !strings.HasPrefix(v, "v") {
			v = "v" + v
		}
		parts = append(parts, "update available: "+v)
	}
	return strings.Join(parts, " — ")
}
