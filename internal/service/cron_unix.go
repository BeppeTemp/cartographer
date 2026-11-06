//go:build !windows

package service

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/BeppeTemp/cartographer/internal/procattr"
)

func realProcOps() *procOps {
	return &procOps{start: unixStart, alive: unixAlive, cmdline: procCmdline, signal: func(pid int, sig syscall.Signal) error { return syscall.Kill(pid, sig) }}
}

// unixStart runs bin detached: its own session, stdin/stdout/stderr on
// /dev/null (the server logs to --log-file), exactly env.
func unixStart(bin string, args, env []string) (int, error) {
	cmd := procattr.Hide(exec.Command(bin, args...))
	cmd.Env = env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	// Reaped in this process when it lives on (tests); a short-lived `ensure`
	// exits and init adopts the server.
	go func() { _ = cmd.Wait() }()
	return cmd.Process.Pid, nil
}

// unixAlive is kill(pid, 0), a zombie counting as gone (a container's pid 1
// may never reap it). On Linux an exiting process goes through state Z, then
// X, then loses its /proc entry (ENOENT, or ESRCH on a read already open)
// while kill(pid, 0) still succeeds, the parent being reaping it: all three
// are gone, none alive (#1705: the test reported "still alive" at random).
// ENOENT/ESRCH is trusted only when /proc is this process's own namespace
// view (procIsOurs): a /proc of another pid namespace (unshare --pid without
// --mount-proc, a host /proc bind-mounted in a container) lacks live pids, and
// reading one as dead would let ownPID drop server.pid of a running server.
// Without /proc (macOS) or with a foreign one, kill alone answers; EPERM means
// another user's process, which /proc may hide (hidepid), so it stays alive.
func unixAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	if err != nil && err != syscall.EPERM {
		return false
	}
	data, rerr := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if rerr != nil {
		if (errors.Is(rerr, fs.ErrNotExist) || errors.Is(rerr, syscall.ESRCH)) && err == nil {
			if procIsOurs() {
				return false
			}
		}
		return true
	}
	if i := strings.LastIndexByte(string(data), ')'); i >= 0 {
		// Z zombie; X/x dead, the instant between the reap and the entry going.
		if st := strings.TrimSpace(string(data)[i+1:]); st != "" && strings.ContainsRune("ZXx", rune(st[0])) {
			return false
		}
	}
	return true
}

// procIsOurs reports whether /proc is the view of this pid namespace: the
// /proc/self link names our own pid there, and another namespace's /proc names
// a different one (or none).
func procIsOurs() bool {
	l, err := os.Readlink("/proc/self")
	return err == nil && l == strconv.Itoa(os.Getpid())
}

func procCmdline(pid int) (string, bool) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil {
		return "", false
	}
	return strings.ReplaceAll(string(data), "\x00", " "), true
}

// lockExclusive takes an exclusive flock on path, retrying without blocking
// for up to wait.
func lockExclusive(path string, wait time.Duration) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(wait)
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() { syscall.Flock(int(f.Fd()), syscall.LOCK_UN); f.Close() }, nil
		}
		if err != syscall.EWOULDBLOCK && err != syscall.EAGAIN {
			f.Close()
			return nil, err
		}
		if time.Now().After(deadline) {
			f.Close()
			return nil, errLockHeld
		}
		time.Sleep(100 * time.Millisecond)
	}
}
