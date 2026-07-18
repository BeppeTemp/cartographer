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
