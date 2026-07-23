package service

import (
	"os"
	"path/filepath"
	"runtime"
)

// userHomeDir and goos are indirected so tests can stub them out without
// touching the real home directory/OS (mirrors internal/agents).
var (
	userHomeDir = os.UserHomeDir
	goos        = runtime.GOOS
)

// stableBinSymlinks lists the Homebrew-managed symlinks that stay stable
// across `brew upgrade` (unlike the versioned Caskroom path the binary is
// actually invoked from). Var so tests can point it at a fake layout.
var stableBinSymlinks = []string{
	"/opt/homebrew/bin/cartographer",
	"/usr/local/bin/cartographer",
}

// resolveStableBinPath returns the path to record in the generated
// plist/unit for the given as-invoked binary path. It never resolves
// symlinks on binPath itself (a resolved Homebrew Caskroom path is
// version-pinned and breaks on every `brew upgrade`). If one of
// stableBinSymlinks exists and resolves to the same file as binPath, that
// stable symlink is preferred; otherwise binPath is returned unchanged.
func resolveStableBinPath(binPath string) string {
	target, err := filepath.EvalSymlinks(binPath)
	if err != nil {
		target = binPath
	}
	for _, candidate := range stableBinSymlinks {
		resolved, err := filepath.EvalSymlinks(candidate)
		if err != nil {
			continue
		}
		if resolved == target {
			return candidate
		}
	}
	return binPath
}

// ConfigPath returns the standard path of the server YAML config generated
// and consumed by `cartographer service`: ~/.config/cartographer/server.yaml.
func ConfigPath() (string, error) {
	home, err := userHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "cartographer", "server.yaml"), nil
}

// LaunchdPlistPath returns the path of the launchd agent plist on macOS:
// ~/Library/LaunchAgents/com.cartographer.serve.plist.
func LaunchdPlistPath() (string, error) {
	home, err := userHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", "com.cartographer.serve.plist"), nil
}

// LaunchdLogPath returns the log file launchd redirects stdout/stderr to on
// macOS: ~/Library/Logs/cartographer/server.log.
func LaunchdLogPath() (string, error) {
	home, err := userHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "Logs", "cartographer", "server.log"), nil
}

// SystemdUnitPath returns the path of the systemd user unit on Linux:
// ~/.config/systemd/user/cartographer.service.
func SystemdUnitPath() (string, error) {
	home, err := userHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "systemd", "user", "cartographer.service"), nil
}
