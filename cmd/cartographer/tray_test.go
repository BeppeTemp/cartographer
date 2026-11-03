package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
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

func TestTrayAgentRow(t *testing.T) {
	for state, want := range map[string]tray.Sync{
		"in_sync": tray.SyncOK, "drift": tray.SyncDrift, "unknown": tray.SyncUnknown, "error": tray.SyncUnknown,
	} {
		if got := trayAgentRow(providerStatus{Name: "claude", State: state}); got.Sync != want || got.Name != "Claude Code" {
			t.Errorf("%s: got %+v, want sync %v as Claude Code", state, got, want)
		}
	}
}

// The tray's tick reads two files and probes /health once: no launchctl,
// systemctl or powershell.exe start, whatever the platform. The scheduler
// commands are replaced by scripts that record any call.
func TestTrayStatusFnRunsNoSchedulerCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell stubs for the scheduler commands")
	}
	bin := t.TempDir()
	calls := filepath.Join(t.TempDir(), "calls")
	for _, name := range []string{"launchctl", "systemctl", "powershell.exe", "powershell", "schtasks"} {
		script := "#!/bin/sh\necho " + name + " >> " + calls + "\nexit 1\n"
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	var health atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		health.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","ready":true}`))
	}))
	defer srv.Close()
	cfg := filepath.Join(t.TempDir(), "server.yaml")
	if err := os.WriteFile(cfg, []byte("http: "+strconv.Quote(strings.TrimPrefix(srv.URL, "http://"))+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var statusCalls atomic.Int32
	inner := trayStatusFn(service.NewManager(), cfg)
	deps := tray.NewDeps(func() (service.Status, error) {
		statusCalls.Add(1)
		return inner()
	}, "")
	ticks := make(chan time.Time)
	deps.NewTicker = func(time.Duration) (<-chan time.Time, func()) { return ticks, func() {} }

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		tray.Run(ctx, deps, time.Second, func(tray.Snapshot) {})
	}()
	for range 4 {
		ticks <- time.Now()
	}
	cancel()
	<-done

	if _, err := os.Stat(calls); err == nil {
		b, _ := os.ReadFile(calls)
		t.Errorf("the tick ran scheduler commands:\n%s", b)
	}
	if n, h := statusCalls.Load(), health.Load(); n < 4 || h != n {
		t.Errorf("status reads = %d, /health requests = %d, want equal and at least 4", n, h)
	}
}
