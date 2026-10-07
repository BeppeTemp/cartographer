package provisioning_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

func TestEnsurePeerHooks_RegistersStartAndStop(t *testing.T) {
	cases := []struct {
		provider configurator.Provider
		settings string
		events   []string
	}{
		{configurator.ProviderClaudeCode, ".claude/settings.json", []string{`"SessionStart"`, `"Stop"`}},
		{configurator.ProviderCodex, ".codex/hooks.json", []string{`"SessionStart"`, `"Stop"`}},
		{configurator.ProviderKiro, ".kiro/hooks/cartographer.json", []string{`"SessionStart"`, `"Stop"`}},
	}
	for _, tc := range cases {
		t.Run(string(tc.provider), func(t *testing.T) {
			baseDir := t.TempDir()
			lock, err := provisioning.EnsurePeerHooks(baseDir, tc.provider, provisioning.Lock{})
			if err != nil {
				t.Fatal(err)
			}
			// Idempotent: a second run registers nothing twice.
			lock, err = provisioning.EnsurePeerHooks(baseDir, tc.provider, lock)
			if err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(filepath.Join(baseDir, tc.settings))
			if err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{provisioning.PeerStartHookName, provisioning.PeerStopHookName} {
				if n := strings.Count(string(data), "/"+name+"/"); n != 1 {
					t.Errorf("%s registered %d times in %s:\n%s", name, n, tc.settings, data)
				}
			}
			for _, ev := range tc.events {
				if !strings.Contains(string(data), ev) {
					t.Errorf("%s not registered in %s:\n%s", ev, tc.settings, data)
				}
			}
			for _, mf := range lock.Managed {
				if !strings.Contains(mf.Path, "cartographer-peers-") {
					t.Errorf("unexpected managed file %+v", mf)
				}
			}

			// A sync with a manifest that never names them keeps them.
			if d := provisioning.ComputeDiff(provisioning.Manifest{}, lock); len(d.Removed) != 0 {
				t.Fatalf("peer hooks reported as removed by a sync: %+v", d.Removed)
			}

			lock, err = provisioning.RemovePeerHooks(baseDir, lock)
			if err != nil {
				t.Fatal(err)
			}
			if len(lock.Managed) != 0 {
				t.Fatalf("lock still holds %+v", lock.Managed)
			}
			after, _ := os.ReadFile(filepath.Join(baseDir, tc.settings))
			if strings.Contains(string(after), "cartographer-peers-") {
				t.Fatalf("registration left behind in %s:\n%s", tc.settings, after)
			}
		})
	}
}

func TestEnsurePeerHooks_ScriptNamesTheProvider(t *testing.T) {
	baseDir := t.TempDir()
	if _, err := provisioning.EnsurePeerHooks(baseDir, configurator.ProviderCodex, provisioning.Lock{}); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(baseDir, ".codex", "hooks", provisioning.PeerStopHookName)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var script []byte
	for _, e := range entries {
		if e.Name() != "hook.json" {
			script, _ = os.ReadFile(filepath.Join(dir, e.Name()))
		}
	}
	if !strings.Contains(string(script), "peer hook codex Stop") {
		t.Fatalf("stop script does not hand off to `peer hook codex Stop`:\n%s", script)
	}
}

func TestEnsurePeerHooks_OpenCodeIsLeftToTheRelay(t *testing.T) {
	baseDir := t.TempDir()
	lock, err := provisioning.EnsurePeerHooks(baseDir, configurator.ProviderOpenCode, provisioning.Lock{})
	if err != nil || len(lock.Managed) != 0 {
		t.Fatalf("opencode got peer hooks: %+v, %v", lock.Managed, err)
	}
}
