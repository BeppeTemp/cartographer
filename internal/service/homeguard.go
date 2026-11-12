package service

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
)

// The per-user scheduler (launchd's gui/<uid> domain, systemd --user, the
// Task Scheduler) belongs to the account, not to $HOME. A run with HOME
// pointed at a sandbox -- the e2e suite, a test, a throwaway setup -- writes
// its unit files under that HOME, but registering them would replace the
// account's own jobs, which carry the same fixed labels (#683): the e2e suite
// once booted out a user's real sync timer and left one pointing into a
// deleted temp directory. So the scheduler is touched only when $HOME is the
// account's home.

// errSandboxHome is returned instead of running a scheduler command.
var errSandboxHome = errors.New("HOME is not this account's home directory")

// ErrSandboxHome is errSandboxHome for callers outside the package, which tell
// this deliberate refusal from a real failure.
var ErrSandboxHome = errSandboxHome

// schedulerCommands are the commands that act on the account-wide scheduler,
// by base name: inside WSL the interop powershell.exe is run by its full path
// (/mnt/c/...), and wsl.exe reaches the distro's own backend.
var schedulerCommands = map[string]bool{
	"launchctl":      true,
	"systemctl":      true,
	"powershell.exe": true,
	"crontab":        true,
	"wsl.exe":        true,
}

// accountHome and envHome are indirected for tests.
var (
	accountHome = func() (string, error) {
		u, err := user.Current()
		if err != nil {
			return "", err
		}
		return u.HomeDir, nil
	}
	envHome = os.UserHomeDir
)

// sandboxedHome reports whether $HOME differs from the account's home, with
// both for the message. An undeterminable account home is not a sandbox: the
// guard must not take away the service from a machine it cannot read.
func sandboxedHome() (bool, string, string) {
	acct, err := accountHome()
	if err != nil || acct == "" {
		return false, "", ""
	}
	env, err := envHome()
	if err != nil || env == "" {
		return false, "", ""
	}
	return !samePath(acct, env), env, acct
}

// homeSandboxed is sandboxedHome over the home the paths of this package
// resolve (userHomeDir), which is what the state and definition paths use.
func homeSandboxed() bool {
	acct, err := accountHome()
	if err != nil || acct == "" {
		return false
	}
	h, err := userHomeDir()
	if err != nil || h == "" {
		return false
	}
	return !samePath(acct, h)
}

// cronGuard refuses a cron operation that mutates state (pid, marker, lock,
// crontab) under a sandboxed HOME, before anything is touched.
func cronGuard() error {
	if homeSandboxed() {
		return fmt.Errorf("%w: the cron backend was not touched", errSandboxHome)
	}
	return nil
}

func samePath(a, b string) bool {
	clean := func(p string) string {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			p = r
		}
		return filepath.Clean(p)
	}
	return clean(a) == clean(b)
}

// guardScheduler refuses a scheduler command under a sandboxed HOME.
func guardScheduler(name string) error {
	if !schedulerCommands[filepath.Base(name)] {
		return nil
	}
	if sandboxed, env, acct := sandboxedHome(); sandboxed {
		return fmt.Errorf("%w (HOME=%s, account home %s): the unit files are written, but nothing is registered with the per-user scheduler", errSandboxHome, env, acct)
	}
	return nil
}
