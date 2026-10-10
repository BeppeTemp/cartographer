package clientconfig

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestWriteTokenModeAndReplace(t *testing.T) {
	dir := t.TempDir()
	if err := WriteToken(dir, "first"); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		fi, _ := os.Stat(TokenPath(dir))
		if fi.Mode().Perm() != 0o600 {
			t.Fatalf("mode = %o, want 600", fi.Mode().Perm())
		}
	}
	if err := WriteToken(dir, "second"); err != nil {
		t.Fatal(err)
	}
	got, err := ReadToken(dir)
	if err != nil || got != "second" {
		t.Fatalf("ReadToken = %q, %v", got, err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("leftover files: %v", entries)
	}
}

func TestReadTokenMissing(t *testing.T) {
	if got, err := ReadToken(t.TempDir()); got != "" || err != nil {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestTokenSymlinkRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	dir := t.TempDir()
	target := filepath.Join(t.TempDir(), "elsewhere")
	if err := os.WriteFile(target, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, TokenPath(dir)); err != nil {
		t.Fatal(err)
	}
	if err := WriteToken(dir, "tok"); err == nil {
		t.Fatal("WriteToken followed a symlink")
	}
	if b, _ := os.ReadFile(target); string(b) != "x" {
		t.Fatalf("symlink target modified: %q", b)
	}
	if _, err := ReadToken(dir); err == nil {
		t.Fatal("ReadToken followed a symlink")
	}
}

func TestReadTokenUnsafeModeRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no POSIX mode bits")
	}
	dir := t.TempDir()
	if err := os.WriteFile(TokenPath(dir), []byte("tok\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := ReadToken(dir)
	if err == nil || !strings.Contains(err.Error(), "chmod 600") {
		t.Fatalf("err = %v, want chmod 600 hint", err)
	}
}

func TestRemoveToken(t *testing.T) {
	dir := t.TempDir()
	if err := RemoveToken(dir); err != nil {
		t.Fatal(err)
	}
	_ = WriteToken(dir, "tok")
	if err := RemoveToken(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(TokenPath(dir)); !os.IsNotExist(err) {
		t.Fatal("token still there")
	}
}

func TestTokenPathIsNamespaced(t *testing.T) {
	// The dir is the user's HOME: a generic name would collide there.
	if p := TokenPath("/home/user"); !strings.HasSuffix(p, ".cartographer-token") {
		t.Fatalf("TokenPath = %q", p)
	}
}
