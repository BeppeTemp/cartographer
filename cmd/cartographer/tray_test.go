package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRunDispatchesTray(t *testing.T) {
	old := trayFn
	defer func() { trayFn = old }()
	var got []string
	trayFn = func(args []string) int { got = args; return 7 }
	if code := run([]string{"tray", "--interval", "2s"}); code != 7 {
		t.Fatalf("run returned %d, want trayFn's 7", code)
	}
	if len(got) != 2 || got[0] != "--interval" {
		t.Errorf("trayFn args = %v", got)
	}
}

// On a platform with no tray, cmdTray fails before it opens the log file.
func TestCmdTrayUnsupportedPlatformTouchesNothing(t *testing.T) {
	old := trayGOOS
	defer func() { trayGOOS = old }()
	trayGOOS = "linux"
	logPath := filepath.Join(t.TempDir(), "sub", "tray.log")
	if code := cmdTray([]string{"--log-file", logPath}); code != exitStatusError {
		t.Fatalf("cmdTray = %d, want %d", code, exitStatusError)
	}
	if _, err := os.Stat(filepath.Dir(logPath)); !os.IsNotExist(err) {
		t.Errorf("the log directory was created on an unsupported platform: %v", err)
	}
}

func TestCmdTrayRejectsTooShortInterval(t *testing.T) {
	old := trayGOOS
	defer func() { trayGOOS = old }()
	trayGOOS = "darwin"
	logPath := filepath.Join(t.TempDir(), "sub", "tray.log")
	if code := cmdTray([]string{"--interval", "100ms", "--log-file", logPath}); code != exitStatusError {
		t.Fatalf("cmdTray = %d, want %d", code, exitStatusError)
	}
	if _, err := os.Stat(filepath.Dir(logPath)); !os.IsNotExist(err) {
		t.Errorf("the log directory was created before the flags were validated")
	}
}
