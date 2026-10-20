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

// A Kubernetes Secret mounted for a server that does not run as root is
// root-owned and 0440 with the pod's fsGroup, whatever defaultMode says: the
// group bit is how the server reads it, so it is not a warning. Others reading
// it still is.
func TestLoosePermissionsOfAnotherUsersFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no POSIX modes")
	}
	p := filepath.Join(t.TempDir(), "k")
	if err := os.WriteFile(p, []byte("x"), 0o440); err != nil {
		t.Fatal(err)
	}
	if _, loose := LoosePermissions(p); !loose {
		t.Fatal("the server's own 0440 file is not reported loose")
	}
	uid := processUID()
	processUID = func() int { return uid + 1 }
	t.Cleanup(func() { processUID = os.Getuid })

	if msg, loose := LoosePermissions(p); loose {
		t.Errorf("another user's 0440 file reported loose: %s", msg)
	}
	if err := os.Chmod(p, 0o444); err != nil {
		t.Fatal(err)
	}
	if _, loose := LoosePermissions(p); !loose {
		t.Error("another user's 0444 file, readable by others, not reported loose")
	}
}

func TestIsReservedDataName(t *testing.T) {
	if !IsReservedDataName("credentials") || IsReservedDataName("kb-a") {
		t.Error("only the server's own directory names are reserved")
	}
}

// A session on --kb folders without data has no default credentials directory
// and no default audit log: nothing is kept in the home directory.
func TestFolderSessionHasNoDefaultDirectories(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	cfg := Default()
	cfg.KBs = []KBSpec{{Path: "/work/kb-a"}}
	if !cfg.FolderSession() || cfg.CredentialsPath() != "" || cfg.AuditLogPath() != "" {
		t.Errorf("folder session: creds %q, audit %q", cfg.CredentialsPath(), cfg.AuditLogPath())
	}
	cfg.CredentialsDir, cfg.Audit.Log = "/c", "/a.log"
	if cfg.CredentialsPath() != "/c" || cfg.AuditLogPath() != "/a.log" {
		t.Errorf("explicit settings ignored: %q, %q", cfg.CredentialsPath(), cfg.AuditLogPath())
	}

	// Data, a remote KB, or no KB at all keep the server paths.
	for name, mod := range map[string]func(*Config){
		"data":   func(c *Config) { c.Data = "/srv/data" },
		"remote": func(c *Config) { c.KBs = []KBSpec{{Remote: "https://example.com/kb-a.git"}} },
		"mixed":  func(c *Config) { c.KBs = append(c.KBs, KBSpec{Remote: "https://example.com/kb-b.git"}) },
		"none":   func(c *Config) { c.KBs = nil },
	} {
		c := Default()
		c.KBs = []KBSpec{{Path: "/work/kb-a"}}
		mod(c)
		if c.FolderSession() || c.CredentialsPath() == "" || c.AuditLogPath() == "" {
			t.Errorf("%s: creds %q, audit %q; want the server paths", name, c.CredentialsPath(), c.AuditLogPath())
		}
	}
}
