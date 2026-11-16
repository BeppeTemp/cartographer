package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func writeFakeBinary(t *testing.T, dir string) string {
	t.Helper()
	name := "cartographer"
	if os.PathSeparator == '\\' {
		name += ".exe"
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// With two binaries on PATH the launcher names the one that writes it, not the
// first on PATH (#2152); a path that does not outlive the run falls back to PATH.
func TestBridgeCommandNamesTheWritingBinary(t *testing.T) {
	root := t.TempDir()
	writer := writeFakeBinary(t, filepath.Join(root, "rc", "bin"))
	other := writeFakeBinary(t, filepath.Join(root, "stable", "bin"))
	cellar := writeFakeBinary(t, filepath.Join(root, "Cellar", "cartographer", "0.1", "bin"))
	caskroom := writeFakeBinary(t, filepath.Join(root, "Caskroom", "cartographer", "0.1"))
	scratch := writeFakeBinary(t, filepath.Join(root, "scratch"))

	oldExe, oldTmp := executablePath, tempRoots
	t.Cleanup(func() { executablePath, tempRoots = oldExe, oldTmp })
	tempRoots = func() []string { return []string{filepath.Join(root, "scratch")} }
	// The other binary comes first on PATH; the writer is second.
	t.Setenv("PATH", filepath.Dir(other)+string(os.PathListSeparator)+filepath.Dir(writer))

	for _, c := range []struct{ name, exe, want string }{
		{"the writing binary", writer, writer},
		{"a Cellar path", cellar, other},
		{"a Caskroom path", caskroom, other},
		{"a temporary directory", scratch, other},
		{"a directory anybody can write to", open777(t, root), other},
	} {
		if c.exe == "" {
			continue
		}
		executablePath = func() (string, error) { return c.exe, nil }
		if got, err := bridgeCommand(); err != nil || got != c.want {
			t.Errorf("%s: bridgeCommand = %q, %v; want %q", c.name, got, err, c.want)
		}
		// The bridge entry and the launcher name the same command.
		spec, err := bridgeServerSpec("claude", false, "")
		if err != nil || spec.Command != c.want {
			t.Errorf("%s: bridge entry = %q, %v; want %q", c.name, spec.Command, err, c.want)
		}
	}
}

// open777 is a binary in a directory that anybody can write to, without the
// sticky bit; "" where the platform has no such mode.
func open777(t *testing.T, root string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		return ""
	}
	dir := filepath.Join(root, "open")
	p := writeFakeBinary(t, dir)
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	return p
}
