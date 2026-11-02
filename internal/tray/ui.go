package tray

import (
	"fmt"
	"strings"
	"unicode/utf16"
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

// MenuItem is one row of the popup menu, in display order.
type MenuItem struct {
	Action    Action
	Text      string
	Enabled   bool
	Separator bool
	// Status marks the first, disabled line (it carries no action).
	Status bool
}

// MenuItems is the menu a snapshot calls for: the status line, then the four
// actions. Atlas needs a URL, Restart needs an installed service that is not
// already restarting, Open log needs a path.
func MenuItems(m Menu, s Snapshot) []MenuItem {
	return []MenuItem{
		{Text: StatusLine(s), Status: true},
		{Separator: true},
		{Action: ActionOpenAtlas, Text: m.OpenAtlas, Enabled: s.UIURL != ""},
		{Action: ActionRestart, Text: m.Restart, Enabled: s.Installed && !s.Restarting},
		{Action: ActionOpenLog, Text: m.OpenLog, Enabled: s.LogPath != ""},
		{Separator: true},
		{Action: ActionQuit, Text: m.Quit, Enabled: true},
	}
}

// truncateUTF16 cuts s to at most max UTF-16 code units without splitting a
// surrogate pair (the Windows tooltip holds 127 units).
func truncateUTF16(s string, max int) string {
	n := 0
	for i, r := range s {
		w := utf16.RuneLen(r)
		if w < 0 {
			w = 1 // an invalid rune encodes as U+FFFD, one unit
		}
		if n+w > max {
			return s[:i]
		}
		n += w
	}
	return s
}

// menuCommandBase offsets action ids so they are never 0 (a dismissed menu).
const menuCommandBase = 100

// CommandID is the id a popup menu reports for the row: 0 for the status line
// and separators (they are not selectable and must not alias Action 0).
func (it MenuItem) CommandID() int {
	if it.Status || it.Separator {
		return 0
	}
	return menuCommandBase + int(it.Action)
}
