package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/service"
	"github.com/BeppeTemp/cartographer/internal/tray"
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

// rangeUI stands in for a platform UI that follows the tray.UI contract: Run
// returns when updates is closed.
type rangeUI struct{ seen chan struct{} }

func (u rangeUI) Run(_ tray.Menu, updates <-chan tray.Snapshot, _ func(tray.Action)) error {
	for range updates {
		select {
		case u.seen <- struct{}{}:
		default:
		}
	}
	return nil
}

// A signal cancels the context; the UI must then return, not block forever.
func TestRunTrayReturnsWhenContextIsCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	deps := tray.Deps{
		Status:    func() (service.Status, error) { return service.Status{}, nil },
		Probe:     func(string) (*client.Health, error) { return nil, errors.New("down") },
		NewTicker: func(time.Duration) (<-chan time.Time, func()) { return make(chan time.Time), func() {} },
	}
	ui := rangeUI{seen: make(chan struct{}, 1)}
	done := make(chan int, 1)
	go func() { done <- runTray(ctx, ui, deps, time.Second, tray.NewActions(service.NewManager(), nil)) }()
	select {
	case <-ui.seen: // the first snapshot arrived
	case <-time.After(5 * time.Second):
		t.Fatal("no snapshot reached the UI")
	}
	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Errorf("runTray = %d, want 0", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runTray did not return after the context was cancelled")
	}
}
