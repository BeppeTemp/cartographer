//go:build windows

package main

import (
	"errors"
	"syscall"
)

// errorSharingViolation is ERROR_SHARING_VIOLATION: another process holds the
// file open without sharing.
const errorSharingViolation syscall.Errno = 32

func isSharingViolation(err error) bool { return errors.Is(err, errorSharingViolation) }
