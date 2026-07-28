package main

import (
	"strings"
	"testing"
)

func TestServiceHelpUsesStdoutAndSucceeds(t *testing.T) {
	out := withStdout(t, func() {
		if code := cmdService([]string{"--help"}); code != 0 {
			t.Errorf("exit = %d, want 0", code)
		}
	})
	if !strings.Contains(out, "Usage: cartographer service") {
		t.Fatalf("help = %q", out)
	}
}

func TestServiceWithoutActionFails(t *testing.T) {
	if code := cmdService(nil); code != exitStatusError {
		t.Errorf("exit = %d, want %d", code, exitStatusError)
	}
}
