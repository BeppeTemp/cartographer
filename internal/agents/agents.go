// Package agents detects which LLM agent CLIs/apps are installed on the local
// machine (Claude Code, OpenCode, Codex CLI, Kiro), so `cartographer agents`
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

// Detect probes the local machine for the four supported agent providers.
// An agent is Installed if at least one heuristic matches (binary in PATH or
// a well-known config directory present).
func Detect() []Agent {
	home, _ := userHomeDir()
	return []Agent{
		detectClaude(home),
		detectOpenCode(home),
		detectCodex(home),
		detectKiro(home),
	}
}

func detectClaude(home string) Agent {
	a := Agent{Provider: configurator.ProviderClaudeCode, Name: "Claude Code"}
	if path, err := lookPath("claude"); err == nil {
		a.Installed, a.Evidence = true, path
		return a
	}
	if dir := filepath.Join(home, ".claude"); dirExists(dir) {
		a.Installed, a.Evidence = true, dir
	}
	return a
}

func detectOpenCode(home string) Agent {
	a := Agent{Provider: configurator.ProviderOpenCode, Name: "OpenCode"}
	if path, err := lookPath("opencode"); err == nil {
		a.Installed, a.Evidence = true, path
		return a
	}
	if dir := filepath.Join(home, ".config", "opencode"); dirExists(dir) {
		a.Installed, a.Evidence = true, dir
		return a
	}
	if dir := filepath.Join(home, ".opencode"); dirExists(dir) {
		a.Installed, a.Evidence = true, dir
	}
	return a
}

func detectCodex(home string) Agent {
	a := Agent{Provider: configurator.ProviderCodex, Name: "Codex CLI"}
	if path, err := lookPath("codex"); err == nil {
		a.Installed, a.Evidence = true, path
		return a
	}
	if dir := filepath.Join(home, ".codex"); dirExists(dir) {
		a.Installed, a.Evidence = true, dir
	}
	return a
}

func detectKiro(home string) Agent {
	a := Agent{Provider: configurator.ProviderKiro, Name: "Kiro"}
	if path, err := lookPath("kiro"); err == nil {
		a.Installed, a.Evidence = true, path
		return a
	}
	if dir := filepath.Join(home, ".kiro"); dirExists(dir) {
		a.Installed, a.Evidence = true, dir
		return a
	}
	if goos == "darwin" && dirExists("/Applications/Kiro.app") {
		a.Installed, a.Evidence = true, "/Applications/Kiro.app"
	}
	return a
}
