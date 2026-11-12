package wsl

import (
	"errors"
	"path/filepath"
	"testing"
)

func fakeEnv(goos string, files, env, onPath map[string]string, exists ...string) Env {
	ex := map[string]bool{}
	for _, e := range exists {
		ex[e] = true
	}
	return Env{
		GOOS: goos,
		ReadFile: func(n string) ([]byte, error) {
			if v, ok := files[n]; ok {
				return []byte(v), nil
			}
			return nil, errors.New("absent")
		},
		Getenv: func(k string) string { return env[k] },
		LookPath: func(f string) (string, error) {
			if v, ok := onPath[f]; ok {
				return v, nil
			}
			return "", errors.New("not found")
		},
		Exists: func(n string) bool { return ex[n] },
	}
}

func TestDetect(t *testing.T) {
	const osr = "/proc/sys/kernel/osrelease"
	wsl2 := "5.15.167.4-microsoft-standard-WSL2\n"
	distro := map[string]string{"WSL_DISTRO_NAME": "Ubuntu"}
	tests := []struct {
		name string
		env  Env
		want Info
	}{
		{"not linux", fakeEnv("windows", map[string]string{osr: wsl2}, distro, nil), Info{}},
		{"plain linux", fakeEnv("linux", map[string]string{osr: "6.8.0-generic\n"}, nil, nil), Info{}},
		{"wsl2 with distro env", fakeEnv("linux", map[string]string{osr: wsl2}, distro, nil),
			Info{Is: true, Distro: "Ubuntu", Version: 2}},
		{"wsl1", fakeEnv("linux", map[string]string{osr: "4.4.0-19041-Microsoft\n"}, distro, nil),
			Info{Is: true, Distro: "Ubuntu", Version: 1}},
		{"old wsl2 kernel without the WSL2 token", fakeEnv("linux", map[string]string{osr: "4.19.128-microsoft-standard\n"}, map[string]string{"WSL_INTEROP": "/run/WSL/1_interop"}, nil),
			Info{Is: true, Version: 2}},
		{"container on a wsl kernel", fakeEnv("linux", map[string]string{osr: wsl2}, nil, nil), Info{}},
		{"marker dir only", fakeEnv("linux", map[string]string{osr: wsl2}, nil, nil, "/run/WSL"), Info{Is: true, Version: 2}},
		{"osrelease unreadable, version file used", fakeEnv("linux", map[string]string{"/proc/version": "Linux version 5.15.0-microsoft-standard-WSL2"}, distro, nil),
			Info{Is: true, Distro: "Ubuntu", Version: 2}},
		{"env alone, no proc", fakeEnv("linux", nil, distro, nil), Info{}},
		{"env on a non-wsl kernel", fakeEnv("linux", map[string]string{osr: "6.8.0-generic\n"}, distro, nil, "/run/WSL"), Info{}},
		{"interop on PATH", fakeEnv("linux", map[string]string{osr: wsl2}, distro, map[string]string{"powershell.exe": "/x/powershell.exe"}),
			Info{Is: true, Distro: "Ubuntu", Version: 2, Interop: true, PowerShell: "/x/powershell.exe"}},
		{"interop at the fixed path", fakeEnv("linux", map[string]string{osr: wsl2}, distro, nil, fixedPowerShell),
			Info{Is: true, Distro: "Ubuntu", Version: 2, Interop: true, PowerShell: fixedPowerShell}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := DetectWith(tc.env); got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

const mountinfo = `22 1 8:16 / / rw,relatime - ext4 /dev/sdc rw,discard
30 22 0:27 / /mnt/wsl rw,relatime - tmpfs tmpfs rw
45 22 0:50 / /mnt/c rw,noatime - 9p C:\134 rw,dirsync,aname=drvfs;path=C:\;uid=1000;gid=1000
46 22 0:51 / /mnt/d rw,noatime - drvfs D:\ rw,uid=1000
47 22 0:52 / /srv/share\040x rw - fuse.drvfs E:\ rw
48 22 8:32 / /mnt/e rw - ext4 /dev/sdd rw
`

func TestIsDrvfs(t *testing.T) {
	for p, want := range map[string]bool{
		"/home/user/.local/share": false,
		"/mnt/wsl/x":              false,
		"/mnt/c/Users/user":       true,
		"/mnt/z/not/in/mountinfo": true,  // fallback: no mount beyond / covers it
		"/mnt/e/data":             false, // a native disk in mountinfo beats the letter rule
		"/mnt/d":                  true,
		"/srv/share x/kb":         true,
		"/srv/share":              false,
		"/mnt/cc/x":               false,
	} {
		if got := isDrvfs(mountinfo, p); got != want {
			t.Errorf("isDrvfs(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestIsDrvfsLongestMountDecides(t *testing.T) {
	mi := "1 0 0:1 / / rw - ext4 /dev/a rw\n" +
		"2 1 0:2 / /data rw - 9p C:\\134 rw,aname=drvfs\n" +
		"3 2 0:3 / /data/native rw - ext4 /dev/b rw\n"
	if !isDrvfs(mi, "/data/x") || isDrvfs(mi, "/data/native/x") {
		t.Fatal("the longest mount point must decide")
	}
}

func TestExistingParentIsAbsolute(t *testing.T) {
	got, err := existingParent(filepath.Join("rel", "missing", "x"), func(p string) bool { return filepath.Base(p) == "rel" })
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(got) || filepath.Base(got) != "rel" {
		t.Fatalf("got %q, want the absolute path of rel", got)
	}
}
