// Package wsl detects when the process runs inside WSL.
//
// It also reports which Windows-side facts are reachable from the distro.
//
// It is client-side and standard-library only. Every probe goes through [Env],
// so tests inject the /proc contents and the environment and need no WSL.
package wsl

import (
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
)

// Info is what [Detect] knows about the running environment.
type Info struct {
	// Is is true inside a WSL distro. A container that merely shares a WSL
	// kernel is not one: see DetectWith.
	Is bool
	// Distro is WSL_DISTRO_NAME, empty when unset.
	Distro string
	// Version is 2 for WSL2, 1 for WSL1, 0 when the kernel string does not say.
	Version int
	// Interop is true when powershell.exe is reachable from Linux.
	Interop bool
	// PowerShell is the resolved path of powershell.exe when Interop is true.
	PowerShell string
}

// Env holds the seams of detection; tests fill every field, [RealEnv] fills
// them with the process's own.
type Env struct {
	GOOS     string
	ReadFile func(name string) ([]byte, error)
	Getenv   func(key string) string
	LookPath func(file string) (string, error)
	// Exists reports whether a path exists.
	Exists func(name string) bool
}

// fixedPowerShell is where Windows PowerShell lives when the Windows PATH is
// not appended to the distro's (appendWindowsPath=false).
const fixedPowerShell = "/mnt/c/Windows/System32/WindowsPowerShell/v1.0/powershell.exe"

// RealEnv is the environment of this process.
func RealEnv() Env {
	return Env{
		GOOS:     runtime.GOOS,
		ReadFile: os.ReadFile,
		Getenv:   os.Getenv,
		LookPath: exec.LookPath,
		Exists:   exists,
	}
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

// Detect reports the WSL facts of this process.
func Detect() Info { return DetectWith(RealEnv()) }

// DetectWith is Detect over an injected environment.
func DetectWith(e Env) Info {
	if e.GOOS != "linux" {
		return Info{}
	}
	lk := strings.ToLower(kernelString(e))
	distro := e.Getenv("WSL_DISTRO_NAME")
	kernelSaysWSL := strings.Contains(lk, "microsoft") || strings.Contains(lk, "wsl")
	// The kernel decides: WSL_DISTRO_NAME alone (inherited by a container started
	// from a WSL shell) only supplies the name.
	if !kernelSaysWSL {
		return Info{}
	}
	// A Docker container on a WSL host shares the kernel string but has none of
	// the distro's markers: it is not a distro.
	if distro == "" && e.Getenv("WSL_INTEROP") == "" && !e.Exists("/run/WSL") {
		return Info{}
	}
	info := Info{Is: true, Distro: distro}
	switch {
	case strings.Contains(lk, "wsl2") || strings.Contains(lk, "microsoft-standard"):
		info.Version = 2
	case strings.Contains(lk, "microsoft"):
		info.Version = 1
	}
	if p, err := e.LookPath("powershell.exe"); err == nil && p != "" {
		info.Interop, info.PowerShell = true, p
	} else if e.Exists(fixedPowerShell) {
		info.Interop, info.PowerShell = true, fixedPowerShell
	}
	return info
}

// kernelString is the kernel release, from osrelease then /proc/version; an
// unreadable file counts as empty (no error: detection never fails).
func kernelString(e Env) string {
	for _, f := range []string{"/proc/sys/kernel/osrelease", "/proc/version"} {
		if b, err := e.ReadFile(f); err == nil && strings.TrimSpace(string(b)) != "" {
			return string(b)
		}
	}
	return ""
}

// IsDrvfs reports whether p lives on a Windows drive mounted into the distro
// (drvfs/9p): no POSIX modes, no reliable locks, slow git. A path that does not
// exist is judged by its first existing parent. Outside Linux it is false.
func IsDrvfs(p string) (bool, error) {
	if runtime.GOOS != "linux" {
		return false, nil
	}
	b, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return false, err
	}
	p, err = existingParent(p, exists)
	if err != nil {
		return false, err
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		p = r
	}
	return isDrvfs(string(b), p), nil
}

// isDrvfs is the pure part: p is absolute and symlink-free (slash form),
// mountinfo the content of /proc/self/mountinfo.
func isDrvfs(mountinfo, p string) bool {
	p = path.Clean(p)
	best, drv := "", false
	for _, line := range strings.Split(mountinfo, "\n") {
		// "ID parent maj:min root mountpoint opts [optional...] - fstype source super"
		pre, post, ok := strings.Cut(line, " - ")
		if !ok {
			continue
		}
		f, g := strings.Fields(pre), strings.Fields(post)
		if len(f) < 5 || len(g) < 1 {
			continue
		}
		mp := unescapeMount(f[4])
		if !under(p, mp) || len(mp) < len(best) {
			continue
		}
		best = mp
		drv = g[0] == "drvfs" || g[0] == "fuse.drvfs" ||
			(g[0] == "9p" && strings.Contains(post, "aname=drvfs"))
	}
	if best == "" || best == "/" {
		// No mount beyond / covers p: the mountinfo of a distro that lists no
		// drive still names /mnt/<letter> a Windows drive. A native disk mounted
		// there is in mountinfo and never reaches this fallback.
		if rest, ok := strings.CutPrefix(p, "/mnt/"); ok {
			seg, _, _ := strings.Cut(rest, "/")
			return len(seg) == 1
		}
	}
	return drv
}

// under reports whether p is mp or below it, on a path-element boundary.
func under(p, mp string) bool {
	mp = path.Clean(mp)
	return mp == "/" || p == mp || strings.HasPrefix(p, mp+"/")
}

// unescapeMount decodes the octal escapes of mountinfo (\040 is a space).
func unescapeMount(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+4 <= len(s) {
			if v, ok := octal(s[i+1 : i+4]); ok {
				b.WriteByte(v)
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func octal(s string) (byte, bool) {
	var v int
	for _, c := range []byte(s) {
		if c < '0' || c > '7' {
			return 0, false
		}
		v = v*8 + int(c-'0')
	}
	return byte(v), v < 256
}

// existingParent makes p absolute and walks up to the first path that exists.
func existingParent(p string, ex func(string) bool) (string, error) {
	p, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	for !ex(p) && filepath.Dir(p) != p {
		p = filepath.Dir(p)
	}
	return p, nil
}
