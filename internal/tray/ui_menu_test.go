package tray

import (
	"reflect"
	"strings"
	"testing"
)

func TestMenuItems(t *testing.T) {
	m := DefaultMenu()
	rows := func(items []MenuItem) []string {
		var out []string
		for _, it := range items {
			switch {
			case it.Separator:
				out = append(out, "-")
			case it.Status:
				out = append(out, strings.Join(PlainLines(it), " | "))
			default:
				r := it.Text
				if !it.Enabled {
					r += " (off)"
				}
				if it.Children != nil {
					r += " >"
				}
				out = append(out, r)
			}
		}
		return out
	}
	agents := Agents{Known: true, N: 2}
	agents.Rows[0] = AgentRow{Name: "Claude Code", Sync: SyncOK}
	agents.Rows[1] = AgentRow{Name: "Codex CLI", Sync: SyncDrift}
	full := Snapshot{State: StateRunning, Version: "1.0", KBCount: 2, KBNames: "kb-a\nwork-kb", LatestVersion: "1.1",
		UIURL: "http://127.0.0.1:1/ui/", LogPath: "l", Installed: true, Agents: agents}
	if got, want := rows(MenuItems(m, full)), []string{
		"Cartographer — Running | v1.0 · 127.0.0.1:1", "Update available — v1.1", "-",
		"Agents", "Claude Code — In sync", "Codex CLI — Out of sync", "-",
		"Knowledge Bases (2) >", "-",
		"Open Atlas", "Restart Service", "Open Log", "-", "Quit",
	}; !reflect.DeepEqual(got, want) {
		t.Errorf("full menu =\n%q\nwant\n%q", got, want)
	}
	kb := MenuItems(m, full)[7]
	if got := rows(kb.Children); !reflect.DeepEqual(got, []string{"kb-a", "work-kb"}) {
		t.Errorf("KB submenu = %q", got)
	}
	if kb.Children[1].Action != ActionOpenKB+1 {
		t.Errorf("second KB action = %d, want %d", kb.Children[1].Action, ActionOpenKB+1)
	}

	bare := Snapshot{State: StateStopped, KBCount: -1}
	if got, want := rows(MenuItems(m, bare)), []string{
		"Cartographer — Stopped", "-",
		"Open Atlas (off)", "Restart Service (off)", "Open Log (off)", "-", "Quit",
	}; !reflect.DeepEqual(got, want) {
		t.Errorf("bare menu = %q, want %q", got, want)
	}

	unchecked := full
	unchecked.Agents.Rows[0].Sync, unchecked.Agents.Rows[1].Sync = SyncUnknown, SyncUnknown
	unchecked.Restarting = true
	got := rows(MenuItems(m, unchecked))
	if got[0] != "Cartographer — Restarting… | v1.0 · 127.0.0.1:1" || got[4] != "Claude Code — Not checked" || got[5] != "Codex CLI — Not checked" {
		t.Errorf("restarting, unchecked = %q", got)
	}
	if got[10] != "Restart Service (off)" {
		t.Error("Restart must be disabled while a restart is in flight")
	}
	none := Snapshot{State: StateNotInstalled, KBCount: -1, Agents: Agents{Known: true}}
	if got := rows(MenuItems(m, none)); got[0] != "Cartographer — Not installed" || got[3] != "None connected" {
		t.Errorf("not installed, no agents = %q", got)
	}
}

func TestTruncateUTF16(t *testing.T) {
	for _, c := range []struct {
		in   string
		max  int
		want string
	}{
		{"abc", 5, "abc"},
		{"abcdef", 3, "abc"},
		{"ab😀", 3, "ab"}, // the emoji is two units: it must not be split
		{"ab😀", 4, "ab😀"},
		{"", 3, ""},
	} {
		if got := truncateUTF16(c.in, c.max); got != c.want {
			t.Errorf("truncateUTF16(%q, %d) = %q, want %q", c.in, c.max, got, c.want)
		}
	}
}

// The status row has Action 0 like Open Atlas: only a CommandID of 0 keeps
// their menu command ids apart.
func TestCommandIDsAreDistinct(t *testing.T) {
	seen := map[int]bool{}
	for _, it := range MenuItems(DefaultMenu(), Snapshot{}) {
		id := it.CommandID()
		if it.Status || it.Separator {
			if id != 0 {
				t.Errorf("%+v has command id %d, want 0", it, id)
			}
			continue
		}
		if id == 0 || seen[id] {
			t.Errorf("%+v: command id %d is zero or duplicated", it, id)
		}
		seen[id] = true
	}
}
