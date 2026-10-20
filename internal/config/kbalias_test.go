package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadYAML(t *testing.T, body string) (*Config, error) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return Load(p)
}

// D906: kb_aliases is no longer a setting. A file that still carries it keeps
// resolving until the realignment has moved the list, and the key is named in a
// warning.
func TestLegacyKBAliasesStillResolveWithAWarning(t *testing.T) {
	cfg, err := loadYAML(t, "kb_aliases:\n  - from: kb-old\n    to: kb-a\n  - from: kb-older\n    to: kb-a\n")
	if err != nil {
		t.Fatal(err)
	}
	got, err := cfg.KBAliases()
	if err != nil {
		t.Fatal(err)
	}
	want := []KBAlias{{From: "kb-old", To: "kb-a"}, {From: "kb-older", To: "kb-a"}}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("KBAliases = %v, want %v", got, want)
	}
	found := false
	for _, w := range cfg.Warnings {
		found = found || strings.Contains(w, "kb_aliases")
	}
	if !found {
		t.Errorf("no warning names kb_aliases: %v", cfg.Warnings)
	}
}

func TestKBAliasesAbsentIsEmpty(t *testing.T) {
	cfg, err := loadYAML(t, "http: \"127.0.0.1:1\"\n")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := cfg.KBAliases(); err != nil || len(got) != 0 {
		t.Errorf("KBAliases = %v, %v; want none", got, err)
	}
}

// A bad legacy list is dropped with a warning: a removed key never stops a start.
func TestLegacyKBAliasesBadItemsAreIgnored(t *testing.T) {
	for name, body := range map[string]string{
		"empty from":     "kb_aliases:\n  - from: \"\"\n    to: kb-a\n",
		"empty to":       "kb_aliases:\n  - from: kb-old\n",
		"invalid name":   "kb_aliases:\n  - from: kb/old\n    to: kb-a\n",
		"self alias":     "kb_aliases:\n  - from: kb-a\n    to: kb-a\n",
		"duplicate from": "kb_aliases:\n  - from: kb-old\n    to: kb-a\n  - from: kb-old\n    to: kb-b\n",
	} {
		t.Run(name, func(t *testing.T) {
			cfg, err := loadYAML(t, body)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if got, _ := cfg.KBAliases(); len(got) != 0 {
				t.Errorf("KBAliases = %v, want none", got)
			}
		})
	}
}

func TestValidateKBAliasesRejectsBadItems(t *testing.T) {
	for name, tc := range map[string]struct {
		in   []KBAlias
		want string
	}{
		"empty from":     {[]KBAlias{{From: "", To: "kb-a"}}, "kb_aliases[0].from"},
		"empty to":       {[]KBAlias{{From: "kb-old"}}, "kb_aliases[0].to"},
		"invalid name":   {[]KBAlias{{From: "kb/old", To: "kb-a"}}, "invalid KB name"},
		"self alias":     {[]KBAlias{{From: "kb-a", To: "kb-a"}}, "aliased to itself"},
		"duplicate from": {[]KBAlias{{From: "kb-old", To: "kb-a"}, {From: "kb-old", To: "kb-b"}}, "duplicate from"},
	} {
		t.Run(name, func(t *testing.T) {
			err := ValidateKBAliases(tc.in)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

func TestServerStateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if st, err := ReadServerState(dir); err != nil || len(st.KBAliases) != 0 {
		t.Fatalf("a missing state file must read as empty, got %v, %v", st, err)
	}
	want := ServerState{KBAliases: []KBAlias{{From: "kb-old", To: "kb-a"}}}
	if err := WriteServerState(dir, want); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".cartographer", "state.json")); err != nil {
		t.Fatalf("the state file is not under <data>/.cartographer/: %v", err)
	}
	got, err := ReadServerState(dir)
	if err != nil || len(got.KBAliases) != 1 || got.KBAliases[0] != want.KBAliases[0] {
		t.Fatalf("read back %v, %v; want %v", got, err, want)
	}
	// What the server resolves comes from the file, with no YAML involved.
	cfg := Default()
	cfg.Data = dir
	if a, err := cfg.KBAliases(); err != nil || len(a) != 1 {
		t.Errorf("KBAliases = %v, %v", a, err)
	}
}

func TestRecordAndPruneKBAliases(t *testing.T) {
	// a → b, then b → c: single hop, a now reaches c.
	l := RecordKBRename(nil, "a", "b")
	l = RecordKBRename(l, "b", "c")
	if len(l) != 2 || l[0] != (KBAlias{From: "a", To: "c"}) || l[1] != (KBAlias{From: "b", To: "c"}) {
		t.Fatalf("list = %v", l)
	}
	// Renaming back restores the name: the item starting at it goes.
	l = RecordKBRename(l, "c", "a")
	for _, x := range l {
		if x.From == "a" {
			t.Errorf("an alias starting at a mounted name was kept: %v", l)
		}
	}
	if p := PruneKBAliases(l, "a"); len(p) != 0 {
		// every item now points at a or starts at it
		t.Errorf("PruneKBAliases = %v, want none", p)
	}
}

// The realignment's half: a list the YAML still holds is moved to the state
// file, an item the state already has for the same old name is kept as it is.
func TestMigrateKBAliasesMovesTheListToTheStateFile(t *testing.T) {
	data := t.TempDir()
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	body := "# note\ndata: " + data + "\nkb_aliases:\n  - from: kb-old\n    to: kb-a\n  - from: kb-older\n    to: kb-b\n"
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteServerState(data, ServerState{KBAliases: []KBAlias{{From: "kb-old", To: "kb-z"}}}); err != nil {
		t.Fatal(err)
	}
	n, err := MigrateKBAliases(cfgPath, data)
	if err != nil || n != 1 {
		t.Fatalf("MigrateKBAliases = %d, %v; want 1 moved", n, err)
	}
	st, _ := ReadServerState(data)
	if len(st.KBAliases) != 2 || st.KBAliases[0].To != "kb-z" || st.KBAliases[1] != (KBAlias{From: "kb-older", To: "kb-b"}) {
		t.Errorf("state = %v", st.KBAliases)
	}
	if _, err := MigrateKBAliases(cfgPath, ""); err == nil {
		t.Error("with no data directory the list has nowhere to go and must say so")
	}
	if err := DropKBAliasesKey(cfgPath); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(cfgPath)
	if strings.Contains(string(out), "kb_aliases") || !strings.Contains(string(out), "# note") {
		t.Errorf("the key must go and the rest stay:\n%s", out)
	}
}
