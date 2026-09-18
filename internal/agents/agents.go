// Package agents detects which LLM agent CLIs/apps are installed on the local
// machine (Claude Code, OpenCode, Codex CLI, Kiro, Hermes, Antigravity), so `cartographer agents`
// and `cartographer connect all` know which providers to target.
package agents

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/BeppeTemp/cartographer/internal/configurator"
)

// Heuristic names the probe that matched, so a caller can weigh the evidence
// instead of only reading it: a binary on PATH is a live client, while a
// directory is compatible with a client that was removed and left its
// configuration behind (#305). Empty when nothing matched.
type Heuristic string

const (
	HeuristicBinary       Heuristic = "binary"         // one of the provider's binaries, found on PATH
	HeuristicConfigDir    Heuristic = "config-dir"     // a config directory under the home directory
	HeuristicEnvConfigDir Heuristic = "env-config-dir" // a config directory anchored at an environment variable
	HeuristicProviderRoot Heuristic = "provider-root"  // the provider's own root directory (D141)
	HeuristicAppDir       Heuristic = "app-dir"        // an application-installation directory for this GOOS
)

// Agent describes the detection result for one provider.
type Agent struct {
	Provider   configurator.Provider
	Name       string // human-readable name, e.g. "Claude Code"
	Installed  bool
	Evidence   string    // what triggered detection (binary path or config dir), empty if not installed
	DetectedBy Heuristic // which probe produced Evidence, empty if not installed
}

// found records a positive detection: the three fields move together, and this
// is the only place that sets them.
func (a Agent) found(h Heuristic, evidence string) Agent {
	a.Installed, a.Evidence, a.DetectedBy = true, evidence, h
	return a
}

// lookPath and userHomeDir are indirected so tests can stub them out without
// touching the real PATH/filesystem.
var (
	lookPath    = exec.LookPath
	userHomeDir = os.UserHomeDir
	getenv      = os.Getenv
	goos        = runtime.GOOS
)

// dirExists reports whether path exists and is a directory.
func dirExists(path string) bool {
	if path == "" {
		return false
	}
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

// Detect probes the local machine for every supported provider, in the
// registry's detection order (D137: identity and detection evidence live in
// internal/configurator's descriptors, not in per-provider functions here).
// An agent is Installed if at least one heuristic matches: any of its binaries in PATH,
// then its config directories in descriptor order, then any directory it anchors at an
// environment variable (%APPDATA% and friends, which no home-relative path can name),
// then its own root directory if it declares one (D141), then its application
// directory for this GOOS if the registry knows one. Which one matched is
// recorded in DetectedBy (D224): the heuristics do not carry the same weight,
// and only the caller knows whether that matters.
func Detect() []Agent {
	home, _ := userHomeDir()
	out := make([]Agent, 0, len(configurator.DetectionOrder()))
	for _, d := range configurator.DetectionOrder() {
		out = append(out, detect(d, home))
	}
	return out
}

func detect(d configurator.Descriptor, home string) Agent {
	a := Agent{Provider: d.Provider, Name: d.DisplayName}
	for _, binary := range d.Binaries {
		if path, err := lookPath(binary); err == nil {
			return a.found(HeuristicBinary, path)
		}
	}
	for _, segments := range d.ConfigDirs {
		dir := filepath.Join(append([]string{home}, segments...)...)
		if dirExists(dir) {
			return a.found(HeuristicConfigDir, dir)
		}
	}
	// An env-anchored config dir is skipped when the variable is unset, which is
	// what makes declaring a Windows location free on every other platform.
	for _, ed := range d.EnvConfigDirs {
		base := getenv(ed.Env)
		if base == "" {
			continue
		}
		dir := filepath.Join(append([]string{base}, ed.Segments...)...)
		if dirExists(dir) {
			return a.found(HeuristicEnvConfigDir, dir)
		}
	}
	// A provider with its own root directory (D141: $HERMES_HOME) is installed
	// if that root exists — it is the same evidence `connect` needs anyway.
	if d.BaseDirEnv != "" {
		if root := getenv(d.BaseDirEnv); dirExists(root) {
			return a.found(HeuristicProviderRoot, root)
		}
	}
	// Per-GOOS rather than a darwin-only field: the descriptor should be able to
	// name an application directory for any platform, and a GOOS the registry
	// says nothing about simply has no such probe. Nothing that was detected on
	// darwin stops being detected — the darwin entries are the same paths.
	if dir := d.AppDirs[goos]; dir != "" && dirExists(dir) {
		return a.found(HeuristicAppDir, dir)
	}
	return a
}
