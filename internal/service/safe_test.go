package service

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// A definition is written into the user's home, so a symlink planted at the
// destination or in a parent directory is refused with an error naming the
// path, and what it points at is left alone (D148). The error quotes the
// path (%q), so the tests look for strconv.Quote of it: on Windows the
// backslashes are doubled.

func symlinkOrSkip(t *testing.T, target, link string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
}

func victimFile(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(p, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func assertKept(t *testing.T, victim string) {
	t.Helper()
	if b, _ := os.ReadFile(victim); string(b) != "keep" {
		t.Fatalf("the link target was overwritten: %q", b)
	}
}

func TestInstallDarwin_RefusesASymlinkedPlist(t *testing.T) {
	home := withTestHome(t, "darwin")
	m, _ := newTestManager()
	plist, _ := LaunchdPlistPath()
	victim := victimFile(t)
	symlinkOrSkip(t, victim, plist)
	err := m.installDarwin("/opt/bin/cartographer", filepath.Join(home, "server.yaml"))
	if err == nil || !strings.Contains(err.Error(), strconv.Quote(plist)) {
		t.Fatalf("installDarwin error = %v, want one naming %s", err, plist)
	}
	assertKept(t, victim)
}

func TestInstallLinux_RefusesASymlinkedParent(t *testing.T) {
	home := withTestHome(t, "linux")
	m, _ := newTestManager()
	unit, _ := SystemdUnitPath()
	realDir := t.TempDir()
	symlinkOrSkip(t, realDir, filepath.Dir(unit))
	err := m.installLinux("/opt/bin/cartographer", filepath.Join(home, "server.yaml"))
	if err == nil || !strings.Contains(err.Error(), strconv.Quote(filepath.Dir(unit))) {
		t.Fatalf("installLinux error = %v, want one naming %s", err, filepath.Dir(unit))
	}
	if ents, _ := os.ReadDir(realDir); len(ents) != 0 {
		t.Fatalf("the unit was written through the linked directory: %v", ents)
	}
}
