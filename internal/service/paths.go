package service

import (
	"os"
	"path/filepath"
	"runtime"
)

// userHomeDir, goos and getenv are indirected so tests can stub them out
// without touching the real home directory/OS/environment (mirrors
// internal/agents).
var (
	userHomeDir = os.UserHomeDir
	goos        = runtime.GOOS
	getenv      = os.Getenv
)

// stableBinSymlinks lists the Homebrew-managed symlinks that stay stable
// across `brew upgrade` (unlike the versioned Caskroom path the binary is
// actually invoked from). Var so tests can point it at a fake layout.
var stableBinSymlinks = []string{
	"/opt/homebrew/bin/cartographer",
	"/usr/local/bin/cartographer",
}

// resolveStableBinPath returns the path to record in the generated service
// definition for the given as-invoked binary path. It never resolves symlinks
// on binPath itself (a resolved Homebrew Caskroom path is version-pinned and
// breaks on every `brew upgrade`). If one of stableBinSymlinks exists and
// resolves to the same file as binPath, that stable symlink is preferred;
// otherwise binPath is returned unchanged.
//
// On Windows binPath is already stable: install.ps1 replaces
// %LOCALAPPDATA%\Cartographer\bin\cartographer.exe in place on every upgrade
// (D245), so there is no versioned payload to step around and nothing to
// resolve.
func resolveStableBinPath(binPath string) string {
	if goos == "windows" {
		return binPath
	}
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

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// appDataDir is %APPDATA% (roaming, per-user configuration that follows the
// user) and localAppDataDir is %LOCALAPPDATA% (machine-local state: logs, the
// task definitions). Both fall back to their canonical location under the home
// directory when the variable is unset, so a task or a test running without the
// usual environment still resolves a path instead of failing.
func appDataDir() (string, error) {
	return windowsUserDir("APPDATA", "Roaming")
}

func localAppDataDir() (string, error) {
	return windowsUserDir("LOCALAPPDATA", "Local")
}

func windowsUserDir(env, fallbackLeaf string) (string, error) {
	if dir := getenv(env); dir != "" {
		return dir, nil
	}
	home, err := userHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "AppData", fallbackLeaf), nil
}

// ConfigPath returns the standard path of the server YAML config generated
// and consumed by `cartographer service`: ~/.config/cartographer/server.yaml
// on unix, %APPDATA%\cartographer\server.yaml on Windows.
//
// The branch belongs here and nowhere else: ConfigPath has callers well outside
// this package (cmd/cartographer's kb, reindex, kbrename and service commands),
// and each of them asking the platform itself is how the answer starts to
// differ per caller.
//
// On Windows there is one read fallback: if the %APPDATA% file does not exist
// and <home>\.config\cartographer\server.yaml does, the latter is returned, so a
// machine that ran a pre-Windows build — or that has a hand-written config in the
// unix location — keeps working, with that file still governing the service.
// Nothing is moved or copied: `service install` on such a machine installs a task
// pointing at the config that already exists, and a *new* config is generated at
// the %APPDATA% location. The fallback is one-directional and only applies when
// the roaming file is absent.
func ConfigPath() (string, error) {
	home, err := userHomeDir()
	if err != nil {
		return "", err
	}
	unixPath := filepath.Join(home, ".config", "cartographer", "server.yaml")
	if goos != "windows" {
		return unixPath, nil
	}
	dir, err := appDataDir()
	if err != nil {
		return "", err
	}
	windowsPath := filepath.Join(dir, "cartographer", "server.yaml")
	if !fileExists(windowsPath) && fileExists(unixPath) {
		return unixPath, nil
	}
	return windowsPath, nil
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

// WindowsTaskPath returns the path of the server's Scheduled Task definition:
// %LOCALAPPDATA%\cartographer\tasks\serve.xml.
//
// The file on disk — not a scheduler query — is what "installed" means on every
// platform (see Status and EffectiveConfigPath), which is why the definition is
// written where it can be read back rather than only handed to the scheduler.
func WindowsTaskPath() (string, error) {
	return windowsTaskFile("serve.xml")
}

// WindowsLogPath returns the log file the server task appends to:
// %LOCALAPPDATA%\cartographer\Logs\server.log. Windows has no journald and a
// task action has no equivalent of the plist's StandardOutPath, so the path is
// passed to `serve --log-file` (D217).
func WindowsLogPath() (string, error) {
	return windowsLogFile("server.log")
}

func windowsTaskFile(name string) (string, error) {
	dir, err := localAppDataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "cartographer", "tasks", name), nil
}

func windowsLogFile(name string) (string, error) {
	dir, err := localAppDataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "cartographer", "Logs", name), nil
}
