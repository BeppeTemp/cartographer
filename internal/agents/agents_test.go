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
		if a.DetectedBy != "" {
			t.Errorf("%s: expected no heuristic recorded, got %q", a.Name, a.DetectedBy)
		}
	}
}

func TestDetect_BinaryInPath(t *testing.T) {
	home := t.TempDir()
	withStubs(t, home, map[string]string{"claude": "/usr/local/bin/claude"}, "linux")

	got := Detect()
	for _, a := range got {
		if a.Provider == configurator.ProviderClaudeCode {
			if !a.Installed || a.Evidence != "/usr/local/bin/claude" || a.DetectedBy != HeuristicBinary {
				t.Errorf("claude: expected Installed=true evidence=/usr/local/bin/claude detected-by=%s, got %+v", HeuristicBinary, a)
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
			if !a.Installed || a.Evidence != filepath.Join(home, ".codex") || a.DetectedBy != HeuristicConfigDir {
				t.Errorf("codex: expected Installed=true evidence=%s detected-by=%s, got %+v", filepath.Join(home, ".codex"), HeuristicConfigDir, a)
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
			if !a.Installed || a.Evidence != filepath.Join(home, ".config", "opencode") || a.DetectedBy != HeuristicConfigDir {
				t.Errorf("opencode: expected Installed=true evidence=%s detected-by=%s, got %+v", filepath.Join(home, ".config", "opencode"), HeuristicConfigDir, a)
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
		if !a.Installed || a.Evidence != root || a.DetectedBy != HeuristicProviderRoot {
			t.Fatalf("hermes: expected Installed=true evidence=%q detected-by=%s, got %+v", root, HeuristicProviderRoot, a)
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

func TestDetect_AntigravityBinary(t *testing.T) {
	home := t.TempDir()
	binPath := "/usr/local/bin/agy"
	withStubs(t, home, map[string]string{"agy": binPath}, "linux")

	for _, a := range Detect() {
		if a.Provider == configurator.ProviderAntigravity {
			if !a.Installed || a.Evidence != binPath {
				t.Errorf("antigravity: expected Installed=true evidence=%s, got %+v", binPath, a)
			}
		} else if a.Installed {
			t.Errorf("%s: expected not installed", a.Name)
		}
	}
}

func TestDetect_LegacyGeminiBinaryIsNotAntigravity(t *testing.T) {
	home := t.TempDir()
	withStubs(t, home, map[string]string{"gemini": "/usr/local/bin/gemini"}, "linux")
	for _, a := range Detect() {
		if a.Provider == configurator.ProviderAntigravity && a.Installed {
			t.Fatalf("legacy gemini binary must not detect Antigravity: %+v", a)
		}
	}
}

func TestDetect_AntigravityConfigDirFallback(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".gemini", "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	withStubs(t, home, nil, "linux")

	got := Detect()
	for _, a := range got {
		if a.Provider == configurator.ProviderAntigravity {
			if !a.Installed || a.Evidence != filepath.Join(home, ".gemini", "config") {
				t.Errorf("antigravity: expected Installed=true evidence=%s, got %+v", filepath.Join(home, ".gemini", "config"), a)
			}
		} else if a.Installed {
			t.Errorf("%s: expected not installed", a.Name)
		}
	}
}

// D216 WP8. %APPDATA%\opencode is where a Windows install keeps OpenCode's
// configuration, and neither home-relative entry (.config/opencode, .opencode)
// can name it: ConfigDirs are joined against the home directory, full stop. The
// env-anchored candidate is the only way to express it.
func TestDetect_EnvAnchoredConfigDirOnWindows(t *testing.T) {
	home := t.TempDir()
	appData := t.TempDir()
	dir := filepath.Join(appData, "opencode")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	withStubs(t, home, nil, "windows")
	withEnv(t, map[string]string{"APPDATA": appData})

	for _, a := range Detect() {
		if a.Provider != configurator.ProviderOpenCode {
			continue
		}
		if !a.Installed || a.Evidence != dir || a.DetectedBy != HeuristicEnvConfigDir {
			t.Fatalf("opencode: expected Installed=true evidence=%q detected-by=%s, got %+v", dir, HeuristicEnvConfigDir, a)
		}
		return
	}
	t.Fatal("opencode missing from Detect() results")
}

// An env-anchored candidate whose variable is unset is skipped: that is what
// makes declaring a Windows location free everywhere else, and it is what keeps
// this change purely additive.
func TestDetect_EnvAnchoredConfigDirIsSkippedWhenUnset(t *testing.T) {
	home := t.TempDir()
	withStubs(t, home, nil, "linux")
	for _, a := range Detect() {
		if a.Provider == configurator.ProviderOpenCode && a.Installed {
			t.Fatalf("opencode: expected not installed with APPDATA unset, got %+v", a)
		}
	}
}

// The application directory is now per-GOOS instead of darwin-only. The
// invariant is that it stays a *bonus*: a GOOS the registry says nothing about
// simply has no such probe, and none of the darwin answers changed.
func TestDetect_AppDirIsPerGOOS(t *testing.T) {
	home := t.TempDir()
	appDir := t.TempDir()

	// A descriptor that declares an app dir only for one GOOS is detected there
	// and not elsewhere. Driven through detect() with a synthetic descriptor, so
	// the assertion does not depend on /Applications existing on the test host.
	d := configurator.Descriptor{
		Provider:    configurator.ProviderKiro,
		DisplayName: "Kiro",
		AppDirs:     map[string]string{"darwin": appDir},
	}

	withStubs(t, home, nil, "darwin")
	if a := detect(d, home); !a.Installed || a.Evidence != appDir || a.DetectedBy != HeuristicAppDir {
		t.Errorf("darwin: expected detection from the app dir %q via %s, got %+v", appDir, HeuristicAppDir, a)
	}

	withStubs(t, home, nil, "windows")
	if a := detect(d, home); a.Installed {
		t.Errorf("windows: a darwin-only app dir must not be evidence, got %+v", a)
	}
}

// The additive invariant, stated as a test rather than as a promise: every
// descriptor that ships a darwin application directory must still ship exactly
// the same path, and no descriptor may have lost a binary or a config dir.
// Detection may only gain true positives (D216 WP8).
func TestDescriptorsKeepTheirUnixDetection(t *testing.T) {
	wantAppDirs := map[configurator.Provider]string{
		configurator.ProviderKiro:        "/Applications/Kiro.app",
		configurator.ProviderAntigravity: "/Applications/Antigravity.app",
	}
	wantConfigDirs := map[configurator.Provider][][]string{
		configurator.ProviderClaudeCode:  {{".claude"}},
		configurator.ProviderCodex:       {{".codex"}},
		configurator.ProviderKiro:        {{".kiro"}},
		configurator.ProviderOpenCode:    {{".config", "opencode"}, {".opencode"}},
		configurator.ProviderAntigravity: {{".gemini", "config"}, {".gemini", "antigravity"}, {".gemini", "antigravity-cli"}},
	}
	wantBinaries := map[configurator.Provider][]string{
		configurator.ProviderClaudeCode:  {"claude"},
		configurator.ProviderCodex:       {"codex"},
		configurator.ProviderKiro:        {"kiro", "kiro-cli"},
		configurator.ProviderHermes:      {"hermes"},
		configurator.ProviderOpenCode:    {"opencode"},
		configurator.ProviderAntigravity: {"agy"},
	}

	for _, d := range configurator.Providers() {
		if want, ok := wantAppDirs[d.Provider]; ok {
			if got := d.AppDirs["darwin"]; got != want {
				t.Errorf("%s: AppDirs[darwin] = %q, want %q", d.Provider, got, want)
			}
		} else if len(d.AppDirs) != 0 {
			t.Errorf("%s: gained AppDirs %v — an unconfirmed location is a false positive", d.Provider, d.AppDirs)
		}
		if want, ok := wantConfigDirs[d.Provider]; ok {
			if !sameSegments(d.ConfigDirs, want) {
				t.Errorf("%s: ConfigDirs = %v, want %v", d.Provider, d.ConfigDirs, want)
			}
		} else if len(d.ConfigDirs) != 0 {
			t.Errorf("%s: unexpected ConfigDirs %v", d.Provider, d.ConfigDirs)
		}
		if want := wantBinaries[d.Provider]; !sameStrings(d.Binaries, want) {
			t.Errorf("%s: Binaries = %v, want %v", d.Provider, d.Binaries, want)
		}
	}
}

func sameStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func sameSegments(got, want [][]string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if !sameStrings(got[i], want[i]) {
			return false
		}
	}
	return true
}

// The point of DetectedBy (#305, D224) is that a live client and a directory
// left behind by a removed one stop looking alike: same Installed, different
// heuristic. This is the Windows report that opened the issue, minus Windows —
// no `claude` binary anywhere, ~/.claude still on disk.
func TestDetect_LeftoverConfigDirIsNotReportedAsABinary(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	withStubs(t, home, nil, "linux")

	for _, a := range Detect() {
		if a.Provider != configurator.ProviderClaudeCode {
			continue
		}
		if !a.Installed || a.DetectedBy != HeuristicConfigDir {
			t.Fatalf("claude: expected Installed=true detected-by=%s, got %+v", HeuristicConfigDir, a)
		}
		return
	}
	t.Fatal("claude missing from Detect() results")
}

// Every heuristic reports itself under its own name: two probes sharing a value
// would be worse than no value at all, since the field exists to tell them
// apart.
func TestHeuristicsAreDistinct(t *testing.T) {
	seen := map[Heuristic]bool{}
	for _, h := range []Heuristic{HeuristicBinary, HeuristicConfigDir, HeuristicEnvConfigDir, HeuristicProviderRoot, HeuristicAppDir} {
		if h == "" {
			t.Error("a heuristic must not be empty: empty means \"not detected\"")
		}
		if seen[h] {
			t.Errorf("duplicate heuristic value %q", h)
		}
		seen[h] = true
	}
}
