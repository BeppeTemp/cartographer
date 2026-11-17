//go:build !windows

package main

func isSharingViolation(error) bool { return false }
