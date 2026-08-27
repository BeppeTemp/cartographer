package agents

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
)

// withStubs temporarily replaces lookPath/userHomeDir/getenv/goos and restores
// them via t.Cleanup, so tests never touch the real PATH/filesystem/OS/env.
// The environment starts empty: a provider detected through its own root
// directory (D141) is invisible unless a test sets it via withEnv.
func withStubs(t *testing.T, home string, found map[string]string, os_ string) {
	t.Helper()
	origLookPath, origHome, origGetenv, origGOOS := lookPath, userHomeDir, getenv, goos
	lookPath = func(name string) (string, error) {
		if p, ok := found[name]; ok {
			return p, nil
		}
		return "", errors.New("not found")
	}
	userHomeDir = func() (string, error) { return home, nil }
	getenv = func(string) string { return "" }
	if os_ != "" {
		goos = os_
	}
	t.Cleanup(func() {
		lookPath, userHomeDir, getenv, goos = origLookPath, origHome, origGetenv, origGOOS
	})
}

// withEnv stubs the environment lookup with a fixed map, on top of withStubs.
func withEnv(t *testing.T, env map[string]string) {
	t.Helper()
	orig := getenv
	getenv = func(name string) string { return env[name] }
	t.Cleanup(func() { getenv = orig })
}

func TestDetect_NothingInstalled(t *testing.T) {
	home := t.TempDir()
	withStubs(t, home, nil, "linux")

	got := Detect()
	if want := len(configurator.DetectionOrder()); len(got) != want {
		t.Fatalf("expected %d agents, got %d", want, len(got))
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

// A provider whose evidence is its own root directory (D141, hermes) is
// detected from $HERMES_HOME when the binary is absent — and only when that
// directory actually exists.
func TestDetect_ProviderRootDir(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(t.TempDir(), "hermes")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}

	withStubs(t, home, nil, "linux")
	withEnv(t, map[string]string{"HERMES_HOME": root})
	for _, a := range Detect() {
		if a.Provider != configurator.ProviderHermes {
			continue
		}
		if !a.Installed || a.Evidence != root {
			t.Fatalf("hermes: expected Installed=true evidence=%q, got %+v", root, a)
		}
	}

	// Pointing at a directory that does not exist is not evidence.
	withEnv(t, map[string]string{"HERMES_HOME": filepath.Join(root, "nope")})
	for _, a := range Detect() {
		if a.Provider == configurator.ProviderHermes && a.Installed {
			t.Fatalf("hermes: expected not installed, got %+v", a)
		}
	}
}

// A provider whose surfaces ship under different executable names is detected
// from any of them: Kiro's IDE installs `kiro`, its standalone CLI installs
// `kiro-cli`, and either is the same provider.
func TestDetect_AlternateBinaryName(t *testing.T) {
	home := t.TempDir()
	withStubs(t, home, map[string]string{"kiro-cli": "/opt/homebrew/bin/kiro-cli"}, "linux")

	for _, a := range Detect() {
		switch a.Provider {
		case configurator.ProviderKiro:
			if !a.Installed || a.Evidence != "/opt/homebrew/bin/kiro-cli" {
				t.Errorf("kiro: expected detection from kiro-cli, got %+v", a)
			}
		default:
			if a.Installed {
				t.Errorf("%s: expected not installed, got %+v", a.Name, a)
			}
		}
	}
}
