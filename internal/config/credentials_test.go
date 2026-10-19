package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCredentialsPath(t *testing.T) {
	c := &Config{Data: filepath.Join("srv", "data")}
	if got, want := c.CredentialsPath(), filepath.Join("srv", "data", "credentials"); got != want {
		t.Errorf("default = %q, want %q", got, want)
	}
	c.CredentialsDir = filepath.Join("etc", "cartographer", "credentials")
	if got := c.CredentialsPath(); got != c.CredentialsDir {
		t.Errorf("explicit = %q, want %q", got, c.CredentialsDir)
	}
	// With no data directory, the one a service install uses.
	if got, want := (&Config{}).CredentialsPath(), filepath.Join(DefaultDataDir(), "credentials"); got != want {
		t.Errorf("no data = %q, want %q", got, want)
	}
}

func TestWriteCredentialIsPrivateAndNeverReplaces(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "credentials")
	created, err := WriteCredential(dir, "kb-a.token", []byte("first"))
	if err != nil || !created {
		t.Fatalf("first write: created=%v err=%v", created, err)
	}
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(filepath.Join(dir, "kb-a.token"))
		if err != nil || fi.Mode().Perm() != 0o600 {
			t.Errorf("file mode = %v err=%v, want 0600", fi.Mode().Perm(), err)
		}
		di, err := os.Stat(dir)
		if err != nil || di.Mode().Perm() != 0o700 {
			t.Errorf("dir mode = %v err=%v, want 0700", di.Mode().Perm(), err)
		}
	}
	created, err = WriteCredential(dir, "kb-a.token", []byte("second"))
	if err != nil || created {
		t.Fatalf("second write: created=%v err=%v, want a no-op", created, err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "kb-a.token")); string(b) != "first" {
		t.Errorf("an existing file was replaced: %q", b)
	}
}

func TestLoosePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no POSIX modes")
	}
	p := filepath.Join(t.TempDir(), "k")
	if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if msg, loose := LoosePermissions(p); loose {
		t.Errorf("0600 reported loose: %s", msg)
	}
	for _, mode := range []os.FileMode{0o640, 0o604, 0o644} {
		if err := os.Chmod(p, mode); err != nil {
			t.Fatal(err)
		}
		if _, loose := LoosePermissions(p); !loose {
			t.Errorf("mode %04o not reported loose", mode)
		}
	}
}

func TestIsReservedDataName(t *testing.T) {
	if !IsReservedDataName("credentials") || IsReservedDataName("kb-a") {
		t.Error("only the server's own directory names are reserved")
	}
}
