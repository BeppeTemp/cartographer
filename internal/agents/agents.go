// Package agents detects which LLM agent CLIs/apps are installed on the local
// machine (Claude Code, OpenCode, Codex CLI, Kiro, Antigravity, Crush, Copilot CLI, Pi, Cline), so `cartographer agents`
// and `cartographer connect all` know which providers to target.
package agents

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/procattr"
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

// runVersion runs `<path> --version` and returns its stdout. Indirected so
// tests never execute a binary.
//
// Pi is the only provider whose detection spawns a binary: the first `pi` on
// PATH is executed with --version, because the name alone is not evidence
// (other tools ship a `pi` shim).
var runVersion = func(path string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := procattr.Hide(exec.CommandContext(ctx, path, "--version"))
	// A shim can leave a grandchild holding stdout; without WaitDelay Output()
	// would block past the timeout and hang `agents` and `connect`.
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	return string(out), err
}

// bareVersion matches the whole first line of `pi --version`: MAJOR.MINOR.PATCH
// with an optional pre-release suffix.
var bareVersion = regexp.MustCompile(`^\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$`)

// binaryProbes confirm that a binary found on PATH is really the provider's.
// Trap: other tools ship a `pi` on PATH (a terminal-multiplexer shim was seen
// forwarding to the real Pi, which passes; an unrelated `pi` does not), and a
// name match alone would report Pi as installed. A timeout, an error or any
// other output means "not this provider's binary"; detection then continues
// with the config directories.
var binaryProbes = map[configurator.Provider]func(path string) bool{
	configurator.ProviderPi: func(path string) bool {
		out, err := runVersion(path)
		if err != nil {
			return false
		}
		first, _, _ := strings.Cut(strings.TrimSpace(out), "\n")
		return bareVersion.MatchString(strings.TrimSpace(first))
	},
}

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
// then its application directory for this GOOS if the registry knows one. (A
// provider's own root directory is no longer a probe: D898 removed it.) Which one matched is
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
			if probe := binaryProbes[d.Provider]; probe != nil && !probe(path) {
				continue
			}
			return a.found(HeuristicBinary, path)
		}
	}
	for _, segments := range d.ConfigDirs {
		if home == "" {
			// filepath.Join("", ".claude") is relative to the working
			// directory: a project's own .claude/ would make the client look
			// installed.
			break
		}
		dir := filepath.Join(append([]string{home}, segments...)...)
		if dirExists(dir) {
			return a.found(HeuristicConfigDir, dir)
		}
	}
	// A glob of directories: an editor extension's directory carries a version.
	// A bad pattern or no match is "not found", never an error.
	for _, segments := range d.ConfigDirGlobs {
		if home == "" {
			break
		}
		// ReadDir on the fixed parent and Match on the last segment only: a home
		// path holding '[', '*' or '?' must not be read as part of a pattern.
		last := len(segments) - 1
		parent := filepath.Join(append([]string{home}, segments[:last]...)...)
		entries, err := os.ReadDir(parent)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if ok, _ := filepath.Match(segments[last], e.Name()); ok && dirExists(filepath.Join(parent, e.Name())) {
				return a.found(HeuristicConfigDir, filepath.Join(parent, e.Name()))
			}
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
	// Per-GOOS rather than a darwin-only field: the descriptor should be able to
	// name an application directory for any platform, and a GOOS the registry
	// says nothing about simply has no such probe. Nothing that was detected on
	// darwin stops being detected — the darwin entries are the same paths.
	if dir := d.AppDirs[goos]; dir != "" && dirExists(dir) {
		return a.found(HeuristicAppDir, dir)
	}
	return a
}
