package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/service"
	"github.com/BeppeTemp/cartographer/internal/wsl"
)

func withServerRuns(t *testing.T, owner string) {
	t.Helper()
	orig := wslServerRunsFn
	wslServerRunsFn = func(d string) error {
		if d == owner {
			return nil
		}
		return errors.New("exit status 1")
	}
	t.Cleanup(func() { wslServerRunsFn = orig })
}

func TestRecordWslServerOwner(t *testing.T) {
	f := &fakeWslSide{running: []string{"Debian", "Ubuntu"}}
	withFakeWsl(t, "windows", f)
	withServerRuns(t, "Ubuntu")
	var out, errw bytes.Buffer
	recordWslServerOwner(&out, &errw)
	if f.marker != "Ubuntu" || !strings.Contains(out.String(), "server: WSL distro Ubuntu") || errw.Len() != 0 {
		t.Errorf("marker %q, out %q, err %q", f.marker, out.String(), errw.String())
	}

	// Every exit that records nothing says why on stderr (#2093).
	for name, c := range map[string]struct {
		f    *fakeWslSide
		want string
	}{
		"no owner":    {&fakeWslSide{running: []string{"Debian"}}, "Debian: service status failed (exit status 1)"},
		"native task": {&fakeWslSide{running: []string{"Ubuntu"}, native: true}, "native Serve task registered"},
	} {
		withFakeWsl(t, "windows", c.f)
		withServerRuns(t, "Ubuntu")
		var out, errw bytes.Buffer
		recordWslServerOwner(&out, &errw)
		wantNote := "note: WSL server owner not recorded: " + c.want + "\n"
		if c.f.marker != "" || out.Len() != 0 || errw.String() != wantNote {
			t.Errorf("%s: marker %q, out %q, err %q", name, c.f.marker, out.String(), errw.String())
		}
	}

	// Silent: no running distro, a marker already set, no WSL at all.
	for name, f := range map[string]*fakeWslSide{
		"no running": {},
		"marker set": {running: []string{"Ubuntu"}, marker: "Debian"},
	} {
		withFakeWsl(t, "windows", f)
		withServerRuns(t, "Ubuntu")
		var out, errw bytes.Buffer
		recordWslServerOwner(&out, &errw)
		if out.Len() != 0 || errw.Len() != 0 || f.marker == "Ubuntu" {
			t.Errorf("%s: out %q, err %q, marker %q", name, out.String(), errw.String(), f.marker)
		}
	}
	withFakeWsl(t, "windows", &fakeWslSide{})
	wslListFn = func(bool) ([]string, error) { return nil, service.ErrNoWsl }
	out.Reset()
	recordWslServerOwner(&out, &errw)
	if out.Len() != 0 || errw.Len() != 0 {
		t.Errorf("no wsl.exe: out %q, err %q", out.String(), errw.String())
	}

	// A marker write failure and a listing failure print a note.
	f = &fakeWslSide{running: []string{"Ubuntu"}}
	withFakeWsl(t, "windows", f)
	withServerRuns(t, "Ubuntu")
	writeWslMarkerFn = func(string) error { return errors.New("access denied") }
	out.Reset()
	errw.Reset()
	recordWslServerOwner(&out, &errw)
	if out.Len() != 0 || errw.String() != "note: WSL server owner not recorded: marker not written (access denied)\n" {
		t.Errorf("write failure: out %q, err %q", out.String(), errw.String())
	}
	wslListFn = func(bool) ([]string, error) { return nil, errors.New("wsl.exe exploded") }
	errw.Reset()
	recordWslServerOwner(&out, &errw)
	if errw.String() != "note: WSL server owner not recorded: wsl listing failed (wsl.exe exploded)\n" {
		t.Errorf("listing failure: err %q", errw.String())
	}

	f = &fakeWslSide{running: []string{"Ubuntu"}}
	withFakeWsl(t, "linux", f)
	out.Reset()
	errw.Reset()
	recordWslServerOwner(&out, &errw)
	if f.marker != "" || out.Len() != 0 || errw.Len() != 0 {
		t.Errorf("off Windows: marker %q, out %q, err %q", f.marker, out.String(), errw.String())
	}
}

func TestRefuseIPv6LoopbackForWsl(t *testing.T) {
	f := &fakeWslSide{marker: "Ubuntu"}
	withFakeWsl(t, "windows", f)
	if err := refuseIPv6LoopbackForWsl("http://[::1]:39273/mcp"); err == nil || !strings.Contains(err.Error(), "127.0.0.1") {
		t.Errorf("[::1] with a WSL server = %v", err)
	}
	if err := refuseIPv6LoopbackForWsl("http://127.0.0.1:39273/mcp"); err != nil {
		t.Errorf("127.0.0.1 = %v", err)
	}
	f.marker = ""
	if err := refuseIPv6LoopbackForWsl("http://[::1]:39273/mcp"); err != nil {
		t.Errorf("[::1] without a WSL server = %v", err)
	}
}

func TestPrintWslServerHint(t *testing.T) {
	f := &fakeWslSide{running: []string{"Ubuntu"}}
	withFakeWsl(t, "windows", f)
	orig := wslListFn
	wslListFn = func(bool) ([]string, error) { return []string{"Ubuntu"}, nil }
	t.Cleanup(func() { wslListFn = orig })
	var out bytes.Buffer
	if !printWslServerHint(&out) || !strings.Contains(out.String(), "cartographer service install --wsl Ubuntu") {
		t.Errorf("hint %q", out.String())
	}
	// No distro has cartographer: the native offer stays.
	f.noBinary = true
	out.Reset()
	if printWslServerHint(&out) || out.Len() != 0 {
		t.Errorf("no cartographer in WSL: %q", out.String())
	}
	// A marker names the distro directly.
	f.marker = "Debian"
	if !printWslServerHint(&out) || !strings.Contains(out.String(), "Debian") {
		t.Errorf("marker hint %q", out.String())
	}
	withFakeWsl(t, "darwin", &fakeWslSide{marker: "Ubuntu"})
	if printWslServerHint(&out) {
		t.Error("hint off Windows")
	}
}

func TestPrintWslWindowsHint(t *testing.T) {
	orig := wslDetectFn
	t.Cleanup(func() { wslDetectFn = orig })
	for _, interop := range []bool{true, false} {
		wslDetectFn = func() wsl.Info { return wsl.Info{Is: true, Version: 2, Interop: interop} }
		var out bytes.Buffer
		printWslWindowsHint(&out)
		if got := strings.Contains(out.String(), "PowerShell"); got != interop {
			t.Errorf("interop %v: %q", interop, out.String())
		}
	}
}
