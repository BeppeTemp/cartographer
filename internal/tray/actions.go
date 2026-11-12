package tray

import (
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/BeppeTemp/cartographer/internal/service"
	"github.com/BeppeTemp/cartographer/internal/webui"
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

// OpenURL opens the Atlas URL. Only a URL the tray built is accepted: http or
// https, a path ending in /ui/ with no "." or ".." segment (a reverse proxy may
// add a prefix), no userinfo or fragment, and at most the one kb query webui.KBURL adds.
func OpenURL(raw string) error {
	refuse := fmt.Errorf("tray: refusing to open %q", raw)
	u, err := url.Parse(raw)
	if err != nil {
		return refuse
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil ||
		!strings.HasSuffix(u.Path, "/ui/") || hasDotSegment(u.Path) || u.Fragment != "" || (u.RawQuery != "" && (len(q) != 1 || len(q["kb"]) != 1)) {
		return refuse
	}
	return openFn(raw)
}

func hasDotSegment(p string) bool {
	for _, seg := range strings.Split(p, "/") {
		if seg == "." || seg == ".." {
			return true
		}
	}
	return false
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

// Handle performs one action. It is called on the UI thread and returns
// promptly: the restart runs in its own goroutine, because on Windows it waits
// up to ~30 s for the old process, far too long for a UI thread.
func (a *Actions) Handle(act Action) {
	a.mu.Lock()
	snap := a.last
	a.mu.Unlock()
	if snap.Mode == ModeClient && (act == ActionRestart || act == ActionOpenLog) {
		return // not shown in client mode: no local service, no local log
	}
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
	default:
		// The index is into the newest snapshot (D1495): a KB list that
		// changed while the menu was open may shift it; out of range is ignored.
		kbs := snap.KBs()
		i := int(act - ActionOpenKB)
		if act < ActionOpenKB || i >= len(kbs) || snap.UIURL == "" {
			return
		}
		if err := OpenURL(webui.KBURL(snap.UIURL, kbs[i])); err != nil {
			a.logf("tray: open %s: %v", kbs[i], err)
		}
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
