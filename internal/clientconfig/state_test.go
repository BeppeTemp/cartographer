package clientconfig_test

import (
	"os"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
)

// AC-cad-18: a state file written before kb_change loads and is not rewritten
// for it; one with it round-trips.
func TestStateKBChangeCompatible(t *testing.T) {
	dir := t.TempDir()
	writeConfigFile(t, dir, "providers:\n  claude:\n    kbs: all\n")
	old := "{\n  \"known_kbs\": [\n    \"kb-a\"\n  ],\n  \"agents\": [\n    \"claude\"\n  ]\n}\n"
	if err := os.WriteFile(clientconfig.StatePath(dir), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.KBChange != nil || strings.Join(cfg.KnownKBs, ",") != "kb-a" {
		t.Fatalf("loaded %+v %v", cfg.KBChange, cfg.KnownKBs)
	}
	if err := clientconfig.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, clientconfig.StatePath(dir)); got != old {
		t.Errorf("a state without kb_change was rewritten:\n%s\nwas:\n%s", got, old)
	}

	cfg.KBChange = &clientconfig.KBChange{At: "2026-11-24T10:00:00Z", Added: []string{"kb-b"}, Removed: []string{"kb-c"}}
	if err := clientconfig.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	back, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if c := back.KBChange; c == nil || c.At != "2026-11-24T10:00:00Z" || strings.Join(c.Added, ",") != "kb-b" || strings.Join(c.Removed, ",") != "kb-c" {
		t.Errorf("round trip %+v", c)
	}
}
