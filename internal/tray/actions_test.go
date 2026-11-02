package tray

import (
	"errors"
	"sync"
	"testing"
)

func stubOpen(t *testing.T) *[]string {
	t.Helper()
	var opened []string
	old := openFn
	openFn = func(s string) error { opened = append(opened, s); return nil }
	t.Cleanup(func() { openFn = old })
	return &opened
}

func TestOpenURLAcceptsOnlyTheAtlasURL(t *testing.T) {
	opened := stubOpen(t)
	if err := OpenURL("http://127.0.0.1:39273/ui/"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{
		"", "https://127.0.0.1:39273/ui/", "http://127.0.0.1:39273/other", "file:///etc/passwd",
		"http://u:p@127.0.0.1:1/ui/", "http://127.0.0.1:1/ui/?x=1&calc", "calc.exe", "http:///ui/",
		"http://[::1", "http://bad host:80/ui/", // unparsable: refused, never a panic
	} {
		if err := OpenURL(bad); err == nil {
			t.Errorf("OpenURL(%q) accepted", bad)
		}
	}
	if len(*opened) != 1 || (*opened)[0] != "http://127.0.0.1:39273/ui/" {
		t.Errorf("opened %v", *opened)
	}
}

func TestActionsOpen(t *testing.T) {
	opened := stubOpen(t)
	a := newActions(func() error { return nil }, nil)

	a.Handle(ActionOpenAtlas) // no snapshot yet: UIURL empty, no-op
	a.Observe(Snapshot{State: StateRunning, UIURL: "", LogPath: "/logs/server.log"})
	a.Handle(ActionOpenAtlas)
	if len(*opened) != 0 {
		t.Fatalf("OpenAtlas with an empty URL opened %v", *opened)
	}
	a.Observe(Snapshot{State: StateRunning, UIURL: "http://127.0.0.1:39273/ui/", LogPath: "/logs/server.log"})
	a.Handle(ActionOpenAtlas)
	a.Handle(ActionOpenLog)
	a.Handle(ActionQuit) // the UI's business
	want := []string{"http://127.0.0.1:39273/ui/", "/logs/server.log"}
	if len(*opened) != 2 || (*opened)[0] != want[0] || (*opened)[1] != want[1] {
		t.Errorf("opened %v, want %v", *opened, want)
	}
}

func TestRestartRunsOnceOffTheCallerAndLogsErrors(t *testing.T) {
	release := make(chan struct{})
	calls := 0
	var logged []string
	var mu sync.Mutex
	a := newActions(func() error {
		mu.Lock()
		calls++
		mu.Unlock()
		<-release
		return errors.New("kickstart failed")
	}, func(format string, args ...any) { mu.Lock(); logged = append(logged, format); mu.Unlock() })

	a.Observe(Snapshot{State: StateStopped, Installed: false})
	a.Handle(ActionRestart) // not installed: a no-op
	if a.Busy() {
		t.Fatal("restart started with no service installed")
	}
	a.Observe(Snapshot{State: StateStopped, Installed: true})
	a.Handle(ActionRestart) // returns at once although the restart blocks
	if !a.Busy() {
		t.Fatal("not busy while a restart is in flight")
	}
	a.Handle(ActionRestart) // ignored while in flight
	close(release)
	a.Wait()
	if a.Busy() {
		t.Error("still busy after the restart ended")
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 || len(logged) != 1 {
		t.Errorf("calls=%d logged=%v", calls, logged)
	}
}

func TestOpenKB(t *testing.T) {
	opened := stubOpen(t)
	a := newActions(func() error { return nil }, nil)
	a.Observe(Snapshot{UIURL: "http://127.0.0.1:1/ui/", KBNames: "kb-a\nwork kb&x"})
	a.Handle(ActionOpenKB + 1)
	a.Handle(ActionOpenKB + 2) // out of range: the list changed under the menu
	if want := "http://127.0.0.1:1/ui/?kb=work+kb%26x"; len(*opened) != 1 || (*opened)[0] != want {
		t.Errorf("opened %v, want [%s]", *opened, want)
	}
	if err := OpenURL("http://127.0.0.1:1/ui/?kb=a&kb=b"); err == nil {
		t.Error("two kb values accepted")
	}
	if err := OpenURL("http://127.0.0.1:1/ui/?panel=x"); err == nil {
		t.Error("a query other than kb accepted")
	}
}
