//go:build windows

package service

import (
	"errors"
	"syscall"
	"time"
)

// The cron backend is Linux-only (Backend never selects it on Windows); these
// stubs only let the package compile there.
var errCronUnsupported = errors.New("service: the cron backend is not available on Windows")

func realProcOps() *procOps {
	return &procOps{
		start:   func(string, []string, []string) (int, error) { return 0, errCronUnsupported },
		alive:   func(int) bool { return false },
		cmdline: func(int) (string, bool) { return "", false },
		signal:  func(int, syscall.Signal) error { return errCronUnsupported },
	}
}

func lockExclusive(string, time.Duration) (func(), error) { return nil, errCronUnsupported }
