// Package agents detects which LLM agent CLIs/apps are installed on the local
// machine (Claude Code, OpenCode, Codex CLI, Kiro, Hermes), so `cartographer agents`
// and `cartographer connect all` know which providers to target.
package agents

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/BeppeTemp/cartographer/internal/configurator"
)

// Agent describes the detection result for one provider.
type Agent struct {
	Provider  configurator.Provider
	Name      string // human-readable name, e.g. "Claude Code"
	Installed bool
	Evidence  string // what triggered detection (binary path or config dir), empty if not installed
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
// An agent is Installed if at least one heuristic matches: its binary in PATH,
// then its config directories in descriptor order, then its own root directory
// if it declares one (D141), then — on darwin only — its application bundle.
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
	if d.Binary != "" {
		if path, err := lookPath(d.Binary); err == nil {
			a.Installed, a.Evidence = true, path
			return a
		}
	}
	for _, segments := range d.ConfigDirs {
		dir := filepath.Join(append([]string{home}, segments...)...)
		if dirExists(dir) {
			a.Installed, a.Evidence = true, dir
			return a
		}
	}
	// A provider with its own root directory (D141: $HERMES_HOME) is installed
	// if that root exists — it is the same evidence `connect` needs anyway.
	if d.BaseDirEnv != "" {
		if root := getenv(d.BaseDirEnv); dirExists(root) {
			a.Installed, a.Evidence = true, root
			return a
		}
	}
	if d.DarwinAppDir != "" && goos == "darwin" && dirExists(d.DarwinAppDir) {
		a.Installed, a.Evidence = true, d.DarwinAppDir
	}
	return a
}
