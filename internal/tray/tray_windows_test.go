//go:build windows

package tray

import (
	"runtime"
	"testing"
	"unsafe"
)

// A wrong NOTIFYICONDATAW layout silently shows no icon. The documented
// Unicode layout is 976 bytes on 64-bit Windows (amd64 and arm64).
func TestNotifyIconDataLayout(t *testing.T) {
	if unsafe.Sizeof(uintptr(0)) != 8 {
		t.Skip("layout pinned for 64-bit Windows")
	}
	var d notifyIconData
	if got := unsafe.Sizeof(d); got != 976 {
		t.Errorf("sizeof(notifyIconData) = %d, want 976", got)
	}
	for name, c := range map[string][2]uintptr{
		"hWnd":         {unsafe.Offsetof(d.hWnd), 8},
		"uID":          {unsafe.Offsetof(d.uID), 16},
		"hIcon":        {unsafe.Offsetof(d.hIcon), 32},
		"szTip":        {unsafe.Offsetof(d.szTip), 40},
		"dwState":      {unsafe.Offsetof(d.dwState), 296},
		"szInfo":       {unsafe.Offsetof(d.szInfo), 304},
		"uVersion":     {unsafe.Offsetof(d.uVersion), 816},
		"szInfoTitle":  {unsafe.Offsetof(d.szInfoTitle), 820},
		"guidItem":     {unsafe.Offsetof(d.guidItem), 952},
		"hBalloonIcon": {unsafe.Offsetof(d.hBalloonIcon), 968},
	} {
		if c[0] != c[1] {
			t.Errorf("offset of %s = %d, want %d", name, c[0], c[1])
		}
	}
}

func TestBGRA(t *testing.T) {
	got := bgra([]byte{1, 2, 3, 4, 5, 6, 7, 8})
	want := []byte{3, 2, 1, 4, 7, 6, 5, 8}
	if string(got) != string(want) {
		t.Errorf("bgra = %v, want %v", got, want)
	}
}

// The message loop needs a desktop session; over a service or an SSH channel
// Shell_NotifyIconW fails and the test skips.
func TestWindowSmoke(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	u := &winUI{onAction: func(Action) {}, menu: DefaultMenu()}
	if err := u.init(); err != nil {
		t.Skipf("no window station: %v", err)
	}
	if !u.added {
		call(procDestroyWindow, u.hwnd)
		t.Skip("Shell_NotifyIconW failed: no notification area in this session")
	}
	call(procPostMessage, u.hwnd, wmClose, 0, 0)
	u.loop() // returns on WM_QUIT, posted by WM_DESTROY after the icon is deleted
	if u.hicon != 0 {
		t.Error("the icon handle was not released")
	}
}

// Closing updates must end the message loop: feed posts WM_CLOSE (needs no shell).
func TestFeedClosePostsQuit(t *testing.T) {
	u := &winUI{done: make(chan struct{})}
	updates := make(chan Snapshot, 1)
	posted := make(chan uintptr, 4)
	finished := make(chan struct{})
	go func() { u.feed(updates, func(m uintptr) { posted <- m }); close(finished) }()

	updates <- Snapshot{State: StateRunning}
	if m := <-posted; m != wmUpdate {
		t.Fatalf("snapshot posted %#x, want wmUpdate", m)
	}
	close(updates)
	if m := <-posted; m != wmClose {
		t.Fatalf("close posted %#x, want wmClose", m)
	}
	<-finished
}
