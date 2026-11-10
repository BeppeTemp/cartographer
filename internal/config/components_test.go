package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadComponents(t *testing.T, body string) (*Config, error) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "c.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return Load(p)
}

func TestComponents_Load(t *testing.T) {
	cfg, err := loadComponents(t, "components:\n  - kind: surveyor\n    name: nightly\n    kbs: [kb-b, kb-a]\n  - kind: surveyor\n    name: second\n    kbs: [kb-a]\n    min_replicas: 3\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Components) != 2 || cfg.Components[0].MinReplicas != 1 || cfg.Components[1].MinReplicas != 3 || cfg.Components[0].KBs[0] != "kb-a" {
		t.Fatalf("%+v", cfg.Components)
	}
	if cfg, err = loadComponents(t, "web:\n  enabled: true\n"); err != nil || len(cfg.Components) != 0 {
		t.Fatalf("absent key: %v %v", cfg, err)
	}
}

func TestComponents_LoadErrors(t *testing.T) {
	for name, tc := range map[string]struct{ body, want string }{
		"kind":      {"components:\n  - kind: client\n    name: a\n    kbs: [kb-a]\n", "components[0].kind"},
		"no kind":   {"components:\n  - name: a\n    kbs: [kb-a]\n", "components[0].kind"},
		"name":      {"components:\n  - kind: surveyor\n    name: Bad_Name\n    kbs: [kb-a]\n", "components[0].name"},
		"dup":       {"components:\n  - {kind: surveyor, name: a, kbs: [kb-a]}\n  - {kind: surveyor, name: a, kbs: [kb-a]}\n", "declared twice"},
		"no kbs":    {"components:\n  - kind: surveyor\n    name: a\n", "kbs must name"},
		"min zero":  {"components:\n  - {kind: surveyor, name: a, kbs: [kb-a], min_replicas: 0}\n", "min_replicas 0"},
		"min big":   {"components:\n  - {kind: surveyor, name: a, kbs: [kb-a], min_replicas: 17}\n", "min_replicas 17"},
		"unknown":   {"components:\n  - {kind: surveyor, name: a, kbs: [kb-a], min_replica: 2}\n", `unknown key "min_replica" under components[0]`},
		"top level": {"component:\n  - {kind: surveyor, name: a, kbs: [kb-a]}\n", ""},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := loadComponents(t, tc.body)
			if tc.want == "" {
				return // a misspelled top-level key is the lenient rest of the file
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestUnmountedComponentKBs(t *testing.T) {
	specs := []ComponentSpec{{Kind: "surveyor", Name: "a", KBs: []string{"kb-a", "kb-typo"}, MinReplicas: 1}}
	got := UnmountedComponentKBs(specs, []string{"kb-a", "kb-b"})
	if len(got) != 1 || !strings.Contains(got[0], `"kb-typo"`) {
		t.Fatalf("got %v", got)
	}
	if got := UnmountedComponentKBs(specs[:0], nil); len(got) != 0 {
		t.Fatal(got)
	}
}
