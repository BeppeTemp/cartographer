package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/service"
	"github.com/BeppeTemp/cartographer/internal/wsl"
)

type fakeDoctorWsl struct {
	info      wsl.Info
	installed bool
	keepAlive bool
	wslconfig string
	cfgErr    error
	dataDir   string
	drvfs     bool
	windows   int // calls that reached Windows
}

func withFakeDoctorWsl(t *testing.T, f *fakeDoctorWsl) {
	t.Helper()
	oDetect, oKA, oCfg, oDrv, oData, oStatus := wslDetectFn, wslKeepAliveFn, wslConfigFn, wslDrvfsFn, wslDataDirFn, statusServiceFn
	t.Cleanup(func() {
		wslDetectFn, wslKeepAliveFn, wslConfigFn, wslDrvfsFn, wslDataDirFn, statusServiceFn = oDetect, oKA, oCfg, oDrv, oData, oStatus
	})
	wslDetectFn = func() wsl.Info { return f.info }
	wslKeepAliveFn = func(wsl.Info) (bool, error) { f.windows++; return f.keepAlive, nil }
	wslConfigFn = func(wsl.Info) (string, bool, error) { f.windows++; return f.wslconfig, f.wslconfig != "", f.cfgErr }
	wslDrvfsFn = func(string) (bool, error) { return f.drvfs, nil }
	wslDataDirFn = func() string { return f.dataDir }
	statusServiceFn = func() (service.Status, error) { return service.Status{Installed: f.installed}, nil }
}

func wsl2() wsl.Info {
	return wsl.Info{Is: true, Distro: "Ubuntu", Version: 2, Interop: true, PowerShell: "/mnt/c/powershell.exe"}
}

func findingWith(fs []doctorFinding, sev, sub string) bool {
	for _, f := range fs {
		if f.Check == "wsl" && f.Severity == sev && strings.Contains(f.Message+" "+f.Fix, sub) {
			return true
		}
	}
	return false
}

func TestCheckWSL(t *testing.T) {
	for name, c := range map[string]struct {
		f       fakeDoctorWsl
		sev     string
		want    string
		without string
	}{
		"wsl1":            {fakeDoctorWsl{info: wsl.Info{Is: true, Distro: "Ubuntu", Version: 1}}, doctorWarning, "wsl --set-version Ubuntu 2", ""},
		"data on drvfs":   {fakeDoctorWsl{info: wsl2(), dataDir: "/mnt/c/data", drvfs: true}, doctorError, "data dir /mnt/c/data is on a Windows drive", ""},
		"keep-alive gone": {fakeDoctorWsl{info: wsl2(), installed: true}, doctorWarning, "instanceIdleTimeout=-1", ""},
		"idle off":        {fakeDoctorWsl{info: wsl2(), installed: true, wslconfig: "[general]\ninstanceIdleTimeout = -1\n"}, "", "", "keep-alive"},
		"keep-alive ok":   {fakeDoctorWsl{info: wsl2(), installed: true, keepAlive: true}, "", "", "keep-alive"},
		"not installed":   {fakeDoctorWsl{info: wsl2()}, "", "", "keep-alive"},
		"mirrored":        {fakeDoctorWsl{info: wsl2(), wslconfig: "; comment\n[wsl2]\nnetworkingMode=Mirrored\n"}, doctorInfo, "mirrored networking was not verified", ""},
		"no interop":      {fakeDoctorWsl{info: wsl.Info{Is: true, Version: 2}, installed: true}, doctorInfo, "interop is disabled", "keep-alive task is missing"},
		"unreadable cfg":  {fakeDoctorWsl{info: wsl2(), installed: true, cfgErr: errors.New("boom")}, doctorInfo, "cannot read .wslconfig", ""},
	} {
		f := c.f
		withFakeDoctorWsl(t, &f)
		got := checkWSL()
		if c.want != "" && !findingWith(got, c.sev, c.want) {
			t.Errorf("%s: want a %s finding with %q, got %+v", name, c.sev, c.want, got)
		}
		if c.without != "" && findingWith(got, doctorWarning, c.without) {
			t.Errorf("%s: unexpected %q finding: %+v", name, c.without, got)
		}
	}
}

func TestCheckWSL_NothingOutsideWSL(t *testing.T) {
	f := &fakeDoctorWsl{installed: true, dataDir: "/mnt/c/data", drvfs: true}
	withFakeDoctorWsl(t, f)
	if got := checkWSL(); len(got) != 0 || f.windows != 0 {
		t.Errorf("outside WSL: findings %+v, Windows calls %d", got, f.windows)
	}
}

func TestIniValue(t *testing.T) {
	c := "\ufeff[wsl2]\r\nmemory=4GB\r\n[General]\r\n# x\r\ninstanceIdleTimeout=-1 # never\r\n"
	if v := iniValue(c, "general", "instanceidletimeout"); v != "-1" {
		t.Errorf("value = %q", v)
	}
	if v := iniValue(c, "wsl2", "instanceIdleTimeout"); v != "" {
		t.Errorf("a key of another section = %q", v)
	}
}

// Windows with the WSL marker: an unreachable server names the distro, and a
// marker counts as a local service.
func TestCheckServer_WindowsWithWslMarker(t *testing.T) {
	withFakeWsl(t, "windows", &fakeWslSide{marker: "Ubuntu"})
	orig := statusServiceFn
	statusServiceFn = func() (service.Status, error) { return service.Status{}, nil }
	t.Cleanup(func() { statusServiceFn = orig })
	cfg := &clientconfig.Config{ServerURL: "http://127.0.0.1:39273/mcp"}
	got := checkServer(t.TempDir(), cfg, nil, serverFacts{}, errors.New("connection refused"))
	if len(got) != 1 || !strings.Contains(got[0].Message, "WSL distro Ubuntu") || !strings.Contains(got[0].Fix, "wsl -d Ubuntu -- cartographer service status") {
		t.Errorf("finding = %+v", got)
	}
	if noLocalServiceFor(cfg.ServerURL) {
		t.Error("the WSL marker does not count as a local service")
	}
}
