package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/kb"
)

func TestLocalReindexTargets(t *testing.T) {
	base := t.TempDir()
	explicit := filepath.Join(base, "explicit")
	data := filepath.Join(base, "data")
	if _, err := kb.Init(explicit); err != nil {
		t.Fatal(err)
	}
	if _, err := kb.Init(filepath.Join(data, "discovered")); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(base, "server.yaml")
	configBody := "data: " + data + "\nkbs:\n  - name: explicit-name\n    path: " + explicit + "\n"
	if err := os.WriteFile(configPath, []byte(configBody), 0o644); err != nil {
		t.Fatal(err)
	}
	origConfigPath := reindexConfigPath
	reindexConfigPath = func() (string, error) { return configPath, nil }
	defer func() { reindexConfigPath = origConfigPath }()

	targets, err := localReindexTargets("")
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 2 || targets[0].Name != "discovered" || targets[1].Name != "explicit-name" {
		t.Fatalf("targets = %+v", targets)
	}
	targets, err = localReindexTargets("explicit-name")
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0].Path != explicit {
		t.Fatalf("selected target = %+v", targets)
	}
	if _, err := localReindexTargets("missing"); err == nil {
		t.Fatal("missing configured KB should fail")
	}
}
