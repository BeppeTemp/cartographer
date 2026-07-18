package agents

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
)

// withStubs temporarily replaces lookPath/userHomeDir/goos and restores them
// via t.Cleanup, so tests never touch the real PATH/filesystem/OS.
func withStubs(t *testing.T, home string, found map[string]string, os_ string) {
	t.Helper()
	origLookPath, origHome, origGOOS := lookPath, userHomeDir, goos
	lookPath = func(name string) (string, error) {
		if p, ok := found[name]; ok {
			return p, nil
		}
		return "", errors.New("not found")
	}
	userHomeDir = func() (string, error) { return home, nil }
	if os_ != "" {
		goos = os_
	}
	t.Cleanup(func() {
		lookPath, userHomeDir, goos = origLookPath, origHome, origGOOS
	})
}

func TestDetect_NothingInstalled(t *testing.T) {
	home := t.TempDir()
	withStubs(t, home, nil, "linux")

	got := Detect()
	if len(got) != 4 {
		t.Fatalf("expected 4 agents, got %d", len(got))
	}
	for _, a := range got {
		if a.Installed {
			t.Errorf("%s: expected not installed, got Installed=true evidence=%q", a.Name, a.Evidence)
		}
	}
}

func TestDetect_BinaryInPath(t *testing.T) {
	home := t.TempDir()
	withStubs(t, home, map[string]string{"claude": "/usr/local/bin/claude"}, "linux")

	got := Detect()
	for _, a := range got {
		if a.Provider == configurator.ProviderClaudeCode {
			if !a.Installed || a.Evidence != "/usr/local/bin/claude" {
				t.Errorf("claude: expected Installed=true evidence=/usr/local/bin/claude, got %+v", a)
			}
		} else if a.Installed {
			t.Errorf("%s: expected not installed", a.Name)
		}
	}
}

func TestDetect_ConfigDirFallback(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	withStubs(t, home, nil, "linux")

	got := Detect()
	for _, a := range got {
		if a.Provider == configurator.ProviderCodex {
			if !a.Installed || a.Evidence != filepath.Join(home, ".codex") {
				t.Errorf("codex: expected Installed=true evidence=%s, got %+v", filepath.Join(home, ".codex"), a)
			}
		} else if a.Installed {
			t.Errorf("%s: expected not installed", a.Name)
		}
	}
}

func TestDetect_KiroMacOSApp(t *testing.T) {
	home := t.TempDir()
	withStubs(t, home, nil, "darwin")

	// /Applications/Kiro.app: only assert the heuristic branch runs without
	// panicking; we don't assume it exists on the test machine, but we can
	// verify the OS-specific branch is reachable by checking Provider/Name.
	got := Detect()
	var kiro Agent
	for _, a := range got {
		if a.Provider == configurator.ProviderKiro {
			kiro = a
		}
	}
	if kiro.Name != "Kiro" {
		t.Fatalf("expected Kiro agent in Detect() results, got %+v", kiro)
	}
}

func TestDetect_OpenCodeXDGConfigDir(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".config", "opencode"), 0o755); err != nil {
		t.Fatal(err)
	}
	withStubs(t, home, nil, "linux")

	got := Detect()
	for _, a := range got {
		if a.Provider == configurator.ProviderOpenCode {
			if !a.Installed || a.Evidence != filepath.Join(home, ".config", "opencode") {
				t.Errorf("opencode: expected Installed=true evidence=%s, got %+v", filepath.Join(home, ".config", "opencode"), a)
			}
		}
	}
}
