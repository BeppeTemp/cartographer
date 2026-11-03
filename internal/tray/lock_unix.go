//go:build !windows

package tray

import (
	"os"
	"path/filepath"
	"syscall"
)

// acquireInstanceLock takes an exclusive, non-blocking flock on path (created,
// with its directory, when missing): the macOS counterpart of the Windows
// mutex, so a hand-typed `cartographer tray` next to the LaunchAgent does not
// show a second icon. held is false when another process owns the lock; the
// kernel drops it when this process exits, so a crash leaves nothing stale.
// release is never nil.
func acquireInstanceLock(path string) (held bool, release func(), err error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, func() {}, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return false, func() {}, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if err == syscall.EWOULDBLOCK {
			return false, func() {}, nil
		}
		return false, func() {}, err
	}
	return true, func() { f.Close() }, nil
}

// trayLockPath is the lock file of the macOS tray, under the Application
// Support directory the rest of the client state uses.
func trayLockPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "Application Support", "cartographer", "tray.lock"), nil
}

// singleInstanceAt takes the lock at path. second is true when another tray
// holds it. When the lock cannot be had at all the tray runs unguarded, and
// warn says so once.
func singleInstanceAt(path string, warn func(string)) (release func(), second bool) {
	held, release, err := acquireInstanceLock(path)
	if err != nil {
		warn("tray: single-instance lock unavailable, running unguarded: " + err.Error())
		return func() {}, false
	}
	return release, !held
}
