package main

import "testing"

// A console shared with the shell the command was typed in must never be
// released: only the one Windows created for the task's process alone is.
func TestOwnsConsoleAlone(t *testing.T) {
	for n, want := range map[uint32]bool{0: false, 1: true, 2: false, 3: false} {
		if got := ownsConsoleAlone(n); got != want {
			t.Errorf("ownsConsoleAlone(%d) = %v, want %v", n, got, want)
		}
	}
}
