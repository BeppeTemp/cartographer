package tray

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFirstStartHereOncePerPath(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "bin", "cartographer.exe")
	if !FirstStartHere(dir, a) {
		t.Fatal("first start from a path must announce")
	}
	if FirstStartHere(dir, a) {
		t.Fatal("second start from the same path must stay quiet")
	}
	if !FirstStartHere(dir, filepath.Join(dir, "other", "cartographer.exe")) {
		t.Fatal("a binary at another path must announce again")
	}
}

func TestFirstStartHereFailsQuiet(t *testing.T) {
	if FirstStartHere("", "x") || FirstStartHere(t.TempDir(), "") {
		t.Fatal("no state dir or no path must not announce")
	}
	// A state dir that is a file cannot hold the marker: quiet, not repeated.
	f := filepath.Join(t.TempDir(), "file")
	if err := writeFile(f); err != nil {
		t.Fatal(err)
	}
	if FirstStartHere(f, "x") {
		t.Fatal("unwritable state dir must not announce")
	}
}

func writeFile(p string) error { return os.WriteFile(p, []byte("x"), 0o644) }
