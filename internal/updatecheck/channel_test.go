package updatecheck

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDetectChannel(t *testing.T) {
	links := map[string]string{
		"/opt/homebrew/bin/cartographer": "/opt/homebrew/Caskroom/cartographer/0.16.1/cartographer",
		"/usr/local/bin/cartographer":    "/usr/local/Caskroom/cartographer/0.16.1/cartographer",
	}
	evalWith := func(m map[string]string) func(string) (string, error) {
		return func(p string) (string, error) {
			if r, ok := m[p]; ok {
				return r, nil
			}
			return p, nil
		}
	}
	cases := []struct {
		name   string
		goos   string
		exe    string
		env    map[string]string
		links  map[string]string
		docker bool
		want   Channel
	}{
		{name: "cask via /opt/homebrew/bin", goos: "darwin", exe: "/opt/homebrew/bin/cartographer", links: links, want: ChannelHomebrew},
		{name: "cask via /usr/local/bin (intel)", goos: "darwin", exe: "/usr/local/bin/cartographer", links: links, want: ChannelHomebrew},
		{name: "caskroom path directly", goos: "darwin", exe: "/opt/homebrew/Caskroom/cartographer/0.16.1/cartographer", want: ChannelHomebrew},
		{name: "linuxbrew", goos: "linux", exe: "/home/linuxbrew/.linuxbrew/bin/cartographer", want: ChannelHomebrew},
		{name: "install.sh default", goos: "darwin", exe: "/usr/local/bin/cartographer", want: ChannelInstallSh},
		{name: "install.sh home fallback", goos: "linux", exe: "/home/u/.local/bin/cartographer", want: ChannelInstallSh},
		{name: "install.sh custom dir", goos: "linux", exe: "/srv/tools/cartographer", env: map[string]string{"CARTOGRAPHER_INSTALL_DIR": "/srv/tools"}, want: ChannelInstallSh},
		{name: "go install GOBIN", goos: "linux", exe: "/opt/gobin/cartographer", env: map[string]string{"GOBIN": "/opt/gobin"}, want: ChannelGoInstall},
		{name: "go install GOPATH", goos: "darwin", exe: "/work/go/bin/cartographer", env: map[string]string{"GOPATH": "/work/go:/other"}, want: ChannelGoInstall},
		{name: "go install default GOPATH", goos: "linux", exe: "/home/u/go/bin/cartographer", want: ChannelGoInstall},
		{name: "container", goos: "linux", exe: "/usr/local/bin/cartographer", docker: true, want: ChannelContainer},
		{name: "unknown", goos: "linux", exe: "/tmp/build/cartographer", want: ChannelUnknown},
		{name: "empty", goos: "linux", exe: "", want: ChannelUnknown},
		{name: "windows install.ps1", goos: "windows", exe: `C:\Users\u\AppData\Local\Cartographer\bin\cartographer.exe`, env: map[string]string{"LOCALAPPDATA": `C:\Users\u\AppData\Local`}, want: ChannelInstallPS1},
		{name: "windows install.ps1 case-insensitive", goos: "windows", exe: `c:\users\u\appdata\local\cartographer\BIN\cartographer.exe`, env: map[string]string{"LOCALAPPDATA": `C:\Users\u\AppData\Local`}, want: ChannelInstallPS1},
		{name: "windows custom install dir", goos: "windows", exe: `D:\tools\cartographer.exe`, env: map[string]string{"CARTOGRAPHER_INSTALL_DIR": `D:\tools`}, want: ChannelInstallPS1},
		{name: "windows go install", goos: "windows", exe: `C:\Users\u\go\bin\cartographer.exe`, env: map[string]string{"GOPATH": `C:\Users\u\go`}, want: ChannelGoInstall},
		{name: "windows unknown", goos: "windows", exe: `C:\Downloads\cartographer.exe`, want: ChannelUnknown},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			home := "/home/u"
			if c.goos == "windows" {
				home = `C:\Users\u`
			}
			env := Env{
				GOOS:         c.goos,
				Getenv:       func(k string) string { return c.env[k] },
				HomeDir:      home,
				EvalSymlinks: evalWith(c.links),
				Exists:       func(p string) bool { return c.docker && p == "/.dockerenv" },
			}
			if got := DetectChannelIn(c.exe, env); got != c.want {
				t.Errorf("DetectChannelIn(%q) = %s, want %s", c.exe, got, c.want)
			}
		})
	}
}

func TestUpgradeCommandsAndApplyArgv(t *testing.T) {
	if got := ChannelHomebrew.UpgradeCommand("v1"); got != "brew upgrade --cask beppetemp/tap/cartographer" {
		t.Errorf("homebrew: %q", got)
	}
	if got := ChannelInstallSh.UpgradeCommand(""); got != "curl -fsSL https://raw.githubusercontent.com/BeppeTemp/cartographer/main/install.sh | sh -s -- update" {
		t.Errorf("install.sh: %q", got)
	}
	if got := ChannelInstallPS1.UpgradeCommand(""); got != "& ([scriptblock]::Create((irm https://raw.githubusercontent.com/BeppeTemp/cartographer/main/install.ps1))) update" {
		t.Errorf("install.ps1: %q", got)
	}
	if got := ChannelGoInstall.UpgradeCommand("v0.17.0"); got != "go install github.com/BeppeTemp/cartographer/cmd/cartographer@v0.17.0" {
		t.Errorf("go-install: %q", got)
	}
	for _, c := range []Channel{ChannelContainer, ChannelUnknown} {
		if c.UpgradeCommand("v1") != "" || c.ApplyArgv() != nil {
			t.Errorf("%s must name no command", c)
		}
	}
	if ChannelGoInstall.ApplyArgv() != nil {
		t.Error("go-install is never patched automatically")
	}
	if got := strings.Join(ChannelHomebrew.ApplyArgv(), " "); got != "brew upgrade --cask beppetemp/tap/cartographer" {
		t.Errorf("homebrew argv: %q", got)
	}
	sh := ChannelInstallSh.ApplyArgv()
	if len(sh) != 3 || sh[0] != "sh" || sh[1] != "-c" || !strings.HasSuffix(sh[2], "| sh -s -- update") {
		t.Errorf("install.sh argv: %q", sh)
	}
	ps := ChannelInstallPS1.ApplyArgv()
	if len(ps) != 4 || ps[0] != "powershell" || ps[1] != "-NoProfile" || ps[2] != "-Command" || !strings.HasSuffix(ps[3], "))) update") {
		t.Errorf("install.ps1 argv: %q", ps)
	}
}

func TestParsePolicy(t *testing.T) {
	for in, want := range map[string]string{"": PolicyNotify, "notify": PolicyNotify, " Auto-Patch ": PolicyAutoPatch} {
		if got, err := ParsePolicy(in); err != nil || got != want {
			t.Errorf("ParsePolicy(%q) = %q, %v", in, got, err)
		}
	}
	_, err := ParsePolicy("auto")
	if err == nil || !strings.Contains(err.Error(), "notify") || !strings.Contains(err.Error(), "auto-patch") {
		t.Errorf("unknown policy error must name the valid values: %v", err)
	}
}

func TestShouldAutoApply(t *testing.T) {
	channels := []Channel{ChannelHomebrew, ChannelInstallSh, ChannelInstallPS1, ChannelGoInstall, ChannelContainer, ChannelUnknown}
	for _, policy := range []string{PolicyNotify, PolicyAutoPatch} {
		for _, kind := range []string{KindPatch, KindMinor, KindMajor} {
			for _, ch := range channels {
				res := Result{Available: true, Kind: kind}
				want := policy == PolicyAutoPatch && kind == KindPatch &&
					(ch == ChannelHomebrew || ch == ChannelInstallSh || ch == ChannelInstallPS1)
				if got := ShouldAutoApply(policy, res, ch); got != want {
					t.Errorf("ShouldAutoApply(%s, %s, %s) = %v, want %v", policy, kind, ch, got, want)
				}
			}
		}
	}
	if ShouldAutoApply(PolicyAutoPatch, Result{Available: false, Kind: KindPatch}, ChannelHomebrew) {
		t.Error("nothing available, nothing to apply")
	}
}

func TestApplyLockSecondStarterIsNoop(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	release, err := AcquireApplyLock(dir, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AcquireApplyLock(dir, now); !errors.Is(err, ErrApplyInProgress) {
		t.Fatalf("second starter: %v", err)
	}
	release()
	release2, err := AcquireApplyLock(dir, now)
	if err != nil {
		t.Fatalf("after release: %v", err)
	}
	release2()

	// A lock left by an apply that died is taken over after an hour.
	if err := os.WriteFile(filepath.Join(dir, applyLockName), []byte("1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := now.Add(-2 * time.Hour)
	os.Chtimes(filepath.Join(dir, applyLockName), old, old)
	if _, err := AcquireApplyLock(dir, now); err != nil {
		t.Fatalf("stale lock not taken over: %v", err)
	}
}

func TestMarkers(t *testing.T) {
	dir := t.TempDir()
	if _, ok := ReadMarker(dir, true); ok {
		t.Fatal("no marker yet")
	}
	if err := WriteMarker(dir, false, Marker{Version: "v0.16.2", Log: "/x/update.log"}); err != nil {
		t.Fatal(err)
	}
	if m, ok := ReadMarker(dir, false); !ok || m.Log != "/x/update.log" {
		t.Fatalf("failed marker: %+v %v", m, ok)
	}
	if err := WriteMarker(dir, true, Marker{Version: "v0.16.2"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := ReadMarker(dir, false); ok {
		t.Error("a success clears the failure")
	}
	if m, ok := ReadMarker(dir, true); !ok || m.Version != "v0.16.2" {
		t.Fatalf("applied marker: %+v", m)
	}
	ClearMarker(dir)
	if _, ok := ReadMarker(dir, true); ok {
		t.Error("ClearMarker left it")
	}
}
