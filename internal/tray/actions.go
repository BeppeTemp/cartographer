package tray

import (
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"runtime"
	"sync"
	"sync/atomic"

	"github.com/BeppeTemp/cartographer/internal/service"
)

// openFn hands a URL or a file path to the operating system's "open". Tests
// replace it. Windows uses rundll32's FileProtocolHandler, never `cmd /c
// start`, which re-parses `&` in a URL; macOS uses `open`.
var openFn = osOpen

func osOpen(target string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", target)
	case "darwin":
		cmd = exec.Command("open", target)
	default:
		return fmt.Errorf("tray: cannot open %q on %s", target, runtime.GOOS)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// OpenURL opens the Atlas URL. Only the http URL Status.UIURL builds is
// accepted (host:port and /ui/); nothing read from /health reaches here.
func OpenURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || u.Host == "" || u.User != nil ||
		u.Path != "/ui/" || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("tray: refusing to open %q", raw)
	}
	return openFn(raw)
}

// OpenPath opens a file (the server log) with its associated program.
func OpenPath(path string) error {
	if path == "" {
		return errors.New("tray: no path to open")
	}
	return openFn(path)
}

// Actions runs what the menu asks for. Quit is the UI's own business: it
// stops the tray and leaves the server alone.
type Actions struct {
	restart  func() error
	logf     func(format string, args ...any)
	inflight atomic.Bool
	wg       sync.WaitGroup

	mu   sync.Mutex
	last Snapshot
}

// NewActions restarts through m, the same path as `cartographer service
// restart`. logf receives the errors (the tray log); it may be nil.
func NewActions(m *service.Manager, logf func(format string, args ...any)) *Actions {
	return newActions(m.Restart, logf)
}

func newActions(restart func() error, logf func(format string, args ...any)) *Actions {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &Actions{restart: restart, logf: logf}
}

// Observe records the newest snapshot, which OpenAtlas and OpenLog read.
func (a *Actions) Observe(s Snapshot) {
	a.mu.Lock()
	a.last = s
	a.mu.Unlock()
}

// Busy reports whether a restart is in flight (the menu item is disabled and
// the status line says "restarting…" meanwhile).
func (a *Actions) Busy() bool { return a.inflight.Load() }

// Wait blocks until an in-flight restart finishes.
func (a *Actions) Wait() { a.wg.Wait() }

// Handle performs one action. It never blocks on the restart: on Windows the
// restart waits up to ~30 s for the old process, far too long for a UI thread.
func (a *Actions) Handle(act Action) {
	a.mu.Lock()
	snap := a.last
	a.mu.Unlock()
	switch act {
	case ActionOpenAtlas:
		if snap.UIURL == "" {
			return // greyed in the menu; nothing to open
		}
		if err := OpenURL(snap.UIURL); err != nil {
			a.logf("tray: open atlas: %v", err)
		}
	case ActionOpenLog:
		if err := OpenPath(snap.LogPath); err != nil {
			a.logf("tray: open log: %v", err)
		}
	case ActionRestart:
		if !snap.Installed {
			return // greyed in the menu; there is no service to restart
		}
		a.startRestart()
	}
}

func (a *Actions) startRestart() {
	if !a.inflight.CompareAndSwap(false, true) {
		return
	}
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		defer a.inflight.Store(false)
		if err := a.restart(); err != nil {
			a.logf("tray: restart service: %v", err)
		}
	}()
}
