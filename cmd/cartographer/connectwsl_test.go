package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/wsl"
)

func withServerRuns(t *testing.T, owner string) {
	t.Helper()
	orig := wslServerRunsFn
	wslServerRunsFn = func(d string) bool { return d == owner }
	t.Cleanup(func() { wslServerRunsFn = orig })
}

func TestRecordWslServerOwner(t *testing.T) {
	f := &fakeWslSide{running: []string{"Debian", "Ubuntu"}}
	withFakeWsl(t, "windows", f)
	withServerRuns(t, "Ubuntu")
	var out bytes.Buffer
	recordWslServerOwner(&out)
	if f.marker != "Ubuntu" || !strings.Contains(out.String(), "server: WSL distro Ubuntu") {
		t.Errorf("marker %q, out %q", f.marker, out.String())
	}

	for name, f := range map[string]*fakeWslSide{
		"no owner":    {running: []string{"Debian"}},
		"native task": {running: []string{"Ubuntu"}, native: true},
		"no running":  {},
	} {
		withFakeWsl(t, "windows", f)
		var out bytes.Buffer
		recordWslServerOwner(&out)
		if f.marker != "" || out.Len() != 0 {
			t.Errorf("%s: marker %q, out %q", name, f.marker, out.String())
		}
	}
	f = &fakeWslSide{running: []string{"Ubuntu"}}
	withFakeWsl(t, "linux", f)
	recordWslServerOwner(&out)
	if f.marker != "" {
		t.Error("recorded off Windows")
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
