//go:build !windows

package service

import (
	"fmt"
	"os"
	"os/exec"
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
// may never reap it).
func unixAlive(pid int) bool {
	if err := syscall.Kill(pid, 0); err != nil && err != syscall.EPERM {
		return false
	}
	if data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid)); err == nil {
		if i := strings.LastIndexByte(string(data), ')'); i >= 0 && strings.HasPrefix(strings.TrimSpace(string(data)[i+1:]), "Z") {
			return false
		}
	}
	return true
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
