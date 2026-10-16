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

func TestLoadKBAliases(t *testing.T) {
	cfg, err := loadYAML(t, "kb_aliases:\n  - from: kb-old\n    to: kb-a\n  - from: kb-older\n    to: kb-a\n")
	if err != nil {
		t.Fatal(err)
	}
	want := []KBAlias{{From: "kb-old", To: "kb-a"}, {From: "kb-older", To: "kb-a"}}
	if len(cfg.KBAliases) != len(want) {
		t.Fatalf("KBAliases = %v, want %v", cfg.KBAliases, want)
	}
	for i := range want {
		if cfg.KBAliases[i] != want[i] {
			t.Errorf("KBAliases[%d] = %v, want %v", i, cfg.KBAliases[i], want[i])
		}
	}
	if got := cfg.OriginOf("kb_aliases"); got != OriginFile {
		t.Errorf("origin of kb_aliases = %v, want %v", got, OriginFile)
	}
}

func TestLoadKBAliasesAbsentIsEmpty(t *testing.T) {
	cfg, err := loadYAML(t, "http: \":9090\"\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.KBAliases) != 0 {
		t.Errorf("KBAliases = %v, want none", cfg.KBAliases)
	}
}

func TestLoadKBAliasesRejectsBadItems(t *testing.T) {
	cases := []struct {
		name, yaml, want string
	}{
		{"empty from", "kb_aliases:\n  - from: \"\"\n    to: kb-a\n", "kb_aliases[0].from"},
		{"empty to", "kb_aliases:\n  - from: kb-old\n", "kb_aliases[0].to"},
		{"invalid name", "kb_aliases:\n  - from: kb/old\n    to: kb-a\n", "invalid KB name"},
		{"self alias", "kb_aliases:\n  - from: kb-a\n    to: kb-a\n", "aliased to itself"},
		{"duplicate from", "kb_aliases:\n  - from: kb-old\n    to: kb-a\n  - from: kb-old\n    to: kb-b\n", "duplicate from"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadYAML(t, tc.yaml)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Load error = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}
