package main

import (
	"errors"
	"strings"
	"testing"
)

// fakeWslSide stands in for wsl.exe and the marker.
type fakeWslSide struct {
	marker    string
	running   []string
	native    bool
	noBinary  bool
	ver       string
	exit      int
	delegated [][]string
}

func withFakeWsl(t *testing.T, goos string, f *fakeWslSide) {
	t.Helper()
	o := []any{serviceGOOS, readWslMarkerFn, writeWslMarkerFn, removeWslMarkerFn, wslMarkerPathFn, wslDelegateFn, wslVersionFn, wslListFn, serveTaskInstalledFn}
	t.Cleanup(func() {
		serviceGOOS = o[0].(string)
		readWslMarkerFn = o[1].(func() (string, error))
		writeWslMarkerFn = o[2].(func(string) error)
		removeWslMarkerFn = o[3].(func() error)
		wslMarkerPathFn = o[4].(func() (string, error))
		wslDelegateFn = o[5].(func(string, []string) (int, error))
		wslVersionFn = o[6].(func(string) (string, error))
		wslListFn = o[7].(func(bool) ([]string, error))
		serveTaskInstalledFn = o[8].(func() bool)
	})
	if f.ver == "" {
		f.ver = version
	}
	serviceGOOS = goos
	readWslMarkerFn = func() (string, error) { return f.marker, nil }
	writeWslMarkerFn = func(d string) error { f.marker = d; return nil }
	removeWslMarkerFn = func() error { f.marker = ""; return nil }
	wslMarkerPathFn = func() (string, error) { return `C:\marker.json`, nil }
	wslDelegateFn = func(d string, a []string) (int, error) {
		f.delegated = append(f.delegated, append([]string{d}, a...))
		return f.exit, nil
	}
	wslVersionFn = func(string) (string, error) {
		if f.noBinary {
			return "", errors.New("command not found")
		}
		return f.ver, nil
	}
	wslListFn = func(bool) ([]string, error) { return f.running, nil }
	serveTaskInstalledFn = func() bool { return f.native }
}

func TestServiceDelegatesToWslWithAMarker(t *testing.T) {
	f := &fakeWslSide{marker: "Ubuntu", exit: 3}
	withFakeWsl(t, "windows", f)
	var code int
	withStdout(t, func() { code = cmdService([]string{"status", "--json"}) })
	if code != 3 {
		t.Errorf("exit = %d, want the delegated 3", code)
	}
	if len(f.delegated) != 1 || strings.Join(f.delegated[0], " ") != "Ubuntu status --json" {
		t.Errorf("delegated = %v", f.delegated)
	}
}

func TestServiceNeverDelegatesWithoutMarkerOrOffWindows(t *testing.T) {
	for _, goos := range []string{"windows", "linux"} {
		f := &fakeWslSide{}
		if goos == "linux" {
			f.marker = "Ubuntu"
		}
		withFakeWsl(t, goos, f)
		if _, done := maybeDelegateToWsl("status", nil); done {
			t.Errorf("%s: delegated", goos)
		}
	}
	f := &fakeWslSide{marker: "Ubuntu"}
	withFakeWsl(t, "windows", f)
	for _, verb := range []string{"sync-timer", "tray", "ensure"} {
		if _, done := maybeDelegateToWsl(verb, []string{"status"}); done {
			t.Errorf("%s was delegated", verb)
		}
	}
}

func TestServiceInstallWsl(t *testing.T) {
	f := &fakeWslSide{running: []string{"Ubuntu"}}
	withFakeWsl(t, "windows", f)
	code, done := maybeDelegateToWsl("install", []string{"--wsl", "--no-tray"})
	if !done || code != 0 || f.marker != "Ubuntu" {
		t.Fatalf("code %d done %v marker %q", code, done, f.marker)
	}
	if strings.Join(f.delegated[0], " ") != "Ubuntu install --no-tray" {
		t.Errorf("--wsl was forwarded: %v", f.delegated)
	}
	if _, _ = maybeDelegateToWsl("install", []string{"--wsl=Debian"}); f.delegated[1][0] != "Debian" || f.marker != "Debian" {
		t.Errorf("--wsl=Debian: %v, marker %q", f.delegated, f.marker)
	}
	if code, _ := maybeDelegateToWsl("uninstall", nil); code != 0 || f.marker != "" {
		t.Errorf("uninstall kept the marker: %d %q", code, f.marker)
	}
}

func TestServiceInstallWsl_Refusals(t *testing.T) {
	for name, c := range map[string]struct {
		f    fakeWslSide
		args []string
		want string
	}{
		"two running":    {fakeWslSide{running: []string{"Ubuntu", "Debian"}}, []string{"--wsl"}, "Ubuntu, Debian"},
		"none running":   {fakeWslSide{}, []string{"--wsl"}, "no WSL distro is running"},
		"windows path":   {fakeWslSide{}, []string{"--wsl", "Ubuntu", "--config", `C:\cfg\server.yaml`}, "paths inside WSL"},
		"windows data":   {fakeWslSide{}, []string{"--wsl", "Ubuntu", `--data=\\srv\share`}, "paths inside WSL"},
		"native present": {fakeWslSide{native: true}, []string{"--wsl", "Ubuntu"}, "two servers"},
		"no binary":      {fakeWslSide{noBinary: true}, []string{"--wsl", "Ubuntu"}, "install.sh"},
	} {
		f := c.f
		withFakeWsl(t, "windows", &f)
		var code int
		errOut := withStderr(t, func() { code, _ = maybeDelegateToWsl("install", c.args) })
		if code != exitStatusError || !strings.Contains(errOut, c.want) {
			t.Errorf("%s: code %d, stderr %q", name, code, errOut)
		}
		if len(f.delegated) != 0 || f.marker != "" {
			t.Errorf("%s: delegated %v, marker %q", name, f.delegated, f.marker)
		}
	}
}

func TestServiceDelegateWarnsOnVersionSkew(t *testing.T) {
	f := &fakeWslSide{marker: "Ubuntu", ver: "0.0.1-other"}
	withFakeWsl(t, "windows", f)
	errOut := withStderr(t, func() { maybeDelegateToWsl("restart", nil) })
	if !strings.Contains(errOut, "0.0.1-other") || len(f.delegated) != 1 {
		t.Errorf("stderr %q, delegated %v", errOut, f.delegated)
	}
}
