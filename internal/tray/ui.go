package tray

import (
	"fmt"
	"net/url"
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

// ActionOpenKB is the first of the actions that open one KB in the Atlas:
// ActionOpenKB+i opens the i-th KB of the snapshot the menu was built from.
const ActionOpenKB Action = 100

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
	return Menu{OpenAtlas: "Open Atlas", Restart: "Restart Service", OpenLog: "Open Log", Quit: "Quit"}
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
	// Status marks an information row: no action. Header and Section say
	// which kind; neither is a plain line.
	Status  bool
	Header  bool // the server's state: Text, then Detail on a second line
	Section bool // the title of a group of rows
	// Detail is the header's second line.
	Detail string
	// Badge is a row's status, shown at its right edge in its Tone.
	Badge string
	// Children is a submenu: the row opens it instead of acting.
	Children []MenuItem
	// Tone is the colour of the row's status dot on a platform that draws
	// one. A platform that cannot ignores it: the text says everything.
	Tone Tone
}

// Tone is the state a row's Badge shows, which a platform maps to a colour.
type Tone int

const (
	ToneNone   Tone = iota
	ToneOK          // running, in sync
	ToneBusy        // starting, restarting, out of sync
	ToneDown        // stopped
	ToneIdle        // not installed, not checked
	ToneUpdate      // a newer release exists
)

func withV(v string) string {
	if v == "" || v == "dev" || strings.HasPrefix(v, "v") {
		return v
	}
	return "v" + v
}

// InfoItems are the rows above the actions: the server's state with its
// version, KBs and address, a newer release, and the connected agent clients.
// The tooltip keeps the one-line StatusLine.
func InfoItems(s Snapshot) []MenuItem {
	head := MenuItem{Status: true, Header: true, Text: "Cartographer", Badge: "Running", Tone: ToneOK}
	switch {
	case s.Restarting:
		head.Badge, head.Tone = "Restarting…", ToneBusy
	case s.State == StateStarting:
		head.Badge, head.Tone = "Starting…", ToneBusy
	case s.State == StateStopped:
		head.Badge, head.Tone = "Stopped", ToneDown
	case s.State == StateNotInstalled:
		head.Badge, head.Tone = "Not installed", ToneIdle
	}
	var detail []string
	if s.Version != "" {
		detail = append(detail, withV(s.Version))
	}
	if u, err := url.Parse(s.UIURL); err == nil && u.Host != "" && s.State != StateNotInstalled {
		detail = append(detail, u.Host)
	}
	head.Detail = strings.Join(detail, " · ")
	items := []MenuItem{head}
	if s.LatestVersion != "" {
		items = append(items, MenuItem{Status: true, Text: "Update available", Badge: withV(s.LatestVersion), Tone: ToneUpdate})
	}
	if s.Agents.Known {
		items = append(items, MenuItem{Separator: true}, MenuItem{Status: true, Section: true, Text: "Agents"})
		if s.Agents.N == 0 {
			items = append(items, MenuItem{Status: true, Text: "None connected"})
		}
		for _, r := range s.Agents.Rows[:min(s.Agents.N, MaxAgents)] {
			it := MenuItem{Status: true, Text: r.Name, Badge: "In sync", Tone: ToneOK}
			switch {
			case s.Agents.Unchecked:
				it.Badge, it.Tone = "Not checked", ToneIdle
			case r.Drifted:
				it.Badge, it.Tone = "Out of sync", ToneBusy
			}
			items = append(items, it)
		}
	}
	if kbs := s.KBs(); len(kbs) > 0 {
		sub := make([]MenuItem, len(kbs))
		for i, name := range kbs {
			sub[i] = MenuItem{Action: ActionOpenKB + Action(i), Text: name, Enabled: s.UIURL != ""}
		}
		items = append(items, MenuItem{Separator: true},
			MenuItem{Text: fmt.Sprintf("Knowledge Bases (%d)", len(kbs)), Enabled: true, Children: sub})
	}
	return items
}

// PlainLines is how a platform that draws text only shows an information
// row: the badge after a dash, the header's detail on a line of its own.
func PlainLines(it MenuItem) []string {
	line := it.Text
	if it.Badge != "" {
		line += " — " + it.Badge
	}
	if it.Detail != "" {
		return []string{line, it.Detail}
	}
	return []string{line}
}

// MenuItems is the menu a snapshot calls for: the information rows, then the
// actions. Atlas needs a URL, Restart needs an installed service that is not
// already restarting, Open Log needs a path.
func MenuItems(m Menu, s Snapshot) []MenuItem {
	return append(InfoItems(s),
		MenuItem{Separator: true},
		MenuItem{Action: ActionOpenAtlas, Text: m.OpenAtlas, Enabled: s.UIURL != ""},
		MenuItem{Action: ActionRestart, Text: m.Restart, Enabled: s.Installed && !s.Restarting},
		MenuItem{Action: ActionOpenLog, Text: m.OpenLog, Enabled: s.LogPath != ""},
		MenuItem{Separator: true},
		MenuItem{Action: ActionQuit, Text: m.Quit, Enabled: true},
	)
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

// CommandID is the id a popup menu reports for the row: 0 for information
// rows, separators and submenu parents (they act on nothing and must not alias
// Action 0).
func (it MenuItem) CommandID() int {
	if it.Status || it.Separator || it.Children != nil {
		return 0
	}
	return menuCommandBase + int(it.Action)
}
