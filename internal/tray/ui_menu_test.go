package tray

import (
	"reflect"
	"testing"
)

func TestMenuItems(t *testing.T) {
	m := DefaultMenu()
	type row struct {
		text    string
		enabled bool
	}
	rows := func(s Snapshot) []row {
		var out []row
		for _, it := range MenuItems(m, s) {
			if it.Separator {
				out = append(out, row{"-", false})
				continue
			}
			out = append(out, row{it.Text, it.Enabled})
		}
		return out
	}
	full := Snapshot{State: StateRunning, Version: "1.0", KBCount: 2, UIURL: "http://127.0.0.1:1/ui/", LogPath: "l", Installed: true}
	if got, want := rows(full), []row{
		{"Cartographer 1.0 — 2 KBs", false}, {"-", false}, {"Open Atlas", true}, {"Restart service", true},
		{"Open log", true}, {"-", false}, {"Quit", true},
	}; !reflect.DeepEqual(got, want) {
		t.Errorf("full menu = %v, want %v", got, want)
	}
	bare := Snapshot{State: StateStopped, KBCount: -1}
	if got, want := rows(bare), []row{
		{"stopped", false}, {"-", false}, {"Open Atlas", false}, {"Restart service", false},
		{"Open log", false}, {"-", false}, {"Quit", true},
	}; !reflect.DeepEqual(got, want) {
		t.Errorf("bare menu = %v, want %v", got, want)
	}
	busy := full
	busy.Restarting = true
	if rows(busy)[3].enabled {
		t.Error("Restart must be disabled while a restart is in flight")
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
