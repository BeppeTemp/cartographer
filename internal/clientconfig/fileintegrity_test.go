package clientconfig

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
}

// A dotfile manager keeps .cartographer.yaml as a symlink: a save replaces the
// target and the link survives, so the dotfiles repository sees the change.
func TestSaveFollowsASymlinkedConfig(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	home, repo := t.TempDir(), t.TempDir()
	target := filepath.Join(repo, "cartographer.yaml")
	writeFile(t, target, "server_url: http://127.0.0.1:39273/mcp\n", 0o644)
	if err := os.Symlink(target, Path(home)); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(home)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ServerName = "renamed"
	if err := Save(home, cfg); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(Path(home)); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the link was replaced by a regular file (err %v)", err)
	}
	if got, _ := os.ReadFile(target); !strings.Contains(string(got), "  name: renamed") {
		t.Errorf("the target was not updated:\n%s", got)
	}
}

// A file the operator closed to 0600 stays 0600 even without a literal token.
func TestSaveKeepsTheModeOfAnExistingConfig(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mode bits mean nothing on Windows")
	}
	home := t.TempDir()
	writeFile(t, Path(home), "server_url: http://127.0.0.1:39273/mcp\n", 0o600)
	cfg, err := Load(home)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ServerName = "renamed"
	if err := Save(home, cfg); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(Path(home)); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", fi.Mode().Perm())
	}
}

// A corrupt state file leaves a .bak when it is replaced, and the message
// names what only the user can redo.
func TestCorruptStateKeepsABackupAndSaysWhatToRedo(t *testing.T) {
	home := t.TempDir()
	writeFile(t, Path(home), "server_url: http://127.0.0.1:39273/mcp\n", 0o644)
	writeFile(t, StatePath(home), "{not json", 0o644)

	r, w, _ := os.Pipe()
	old := os.Stderr
	os.Stderr = w
	cfg, err := Load(home)
	w.Close()
	os.Stderr = old
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	if msg := string(buf[:n]); !strings.Contains(msg, "cartographer connect") || strings.Contains(msg, "next sync rebuilds it") {
		t.Errorf("message = %q", msg)
	}
	cfg.KnownKBs = []string{"kb-a"}
	if err := Save(home, cfg); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(StatePath(home) + ".bak"); string(got) != "{not json" {
		t.Errorf(".bak = %q", got)
	}
}

// The schema's defaults are what Load applies for a key the file lacks.
func TestLoadAppliesSchemaDefaultsForAbsentKeys(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CARTOGRAPHER_SERVER_URL", "")
	writeFile(t, Path(home), "auth: false\n", 0o644)
	cfg, err := Load(home)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ServerName != "cartographer" || cfg.Token != EnvRefToken(DefaultTokenEnv) || cfg.ServerURL == "" {
		t.Errorf("defaults not applied: name=%q token=%q url=%q", cfg.ServerName, cfg.Token, cfg.ServerURL)
	}
	writeFile(t, Path(home), "server_name: \"\"\n", 0o644)
	if cfg, _ = Load(home); cfg.ServerName != "" {
		t.Errorf("an explicit empty server_name must stay, got %q", cfg.ServerName)
	}
}

// The defaults of absent keys are load-time values: a Load+Save of a file that
// never carried them does not write them back, and the bytes stay as they were.
func TestLoadSaveDoesNotPersistTheDefaultsOfAbsentKeys(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CARTOGRAPHER_SERVER_URL", "")
	in := "auth: false\n"
	writeFile(t, Path(home), in, 0o644)
	cfg, err := Load(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := Save(home, cfg); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(Path(home))
	for _, key := range []string{"token:", "server_name:", "server_url:"} {
		// A commented example of the key is the schema documentation, not a value.
		if strings.HasPrefix(string(got), key) || strings.Contains(string(got), "\n"+key) {
			t.Errorf("Save wrote back %s the file never carried:\n%s", key, got)
		}
	}
}

// A key a command assigned is written even when it equals the default; the
// untouched ones are still left out.
func TestAssignedDefaultsArePersisted(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CARTOGRAPHER_SERVER_URL", "")
	writeFile(t, Path(home), "auth: false\n", 0o644)
	cfg, err := Load(home)
	if err != nil {
		t.Fatal(err)
	}
	url := cfg.ServerURL
	cfg.ServerURL = url // chosen on purpose, equal to the default
	cfg.Assigned("server.url")
	if err := Save(home, cfg); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(Path(home))
	if !strings.Contains(string(got), "\n  url: "+url) {
		t.Errorf("explicit server_url not persisted:\n%s", got)
	}
	if strings.Contains(string(got), "\n  name:") || strings.Contains(string(got), "\n  token:") {
		t.Errorf("untouched defaults were written:\n%s", got)
	}
	// Changed and changed back, without Assigned: the value moved, so it is kept.
	home2 := t.TempDir()
	writeFile(t, Path(home2), "auth: false\n", 0o644)
	cfg, _ = Load(home2)
	cfg.ServerName = "other"
	if err := Save(home2, cfg); err != nil {
		t.Fatal(err)
	}
	cfg, _ = Load(home2)
	cfg.ServerName = "cartographer"
	cfg.Assigned("server.name")
	if err := Save(home2, cfg); err != nil {
		t.Fatal(err)
	}
	got, _ = os.ReadFile(Path(home2))
	if !strings.Contains(string(got), "\n  name: cartographer") {
		t.Errorf("change and change-back not persisted:\n%s", got)
	}
}

// The generated client state below the user config directory is never written
// through a link (#2365): a symlinked cartographer directory or leaf is
// refused and the victim stays untouched; a symlinked ~/.config keeps working.
func TestSaveDerivedRefusesSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	state := DerivedState{Bindings: map[string][]DerivedBinding{"claude": {{Path: "/x"}}}}
	empty := func(t *testing.T, dir string) {
		t.Helper()
		if es, _ := os.ReadDir(dir); len(es) != 0 {
			t.Errorf("%s was written to: %v", dir, es)
		}
	}
	t.Run("directory", func(t *testing.T) {
		home, victim := t.TempDir(), t.TempDir()
		t.Setenv("HOME", home)
		if err := os.MkdirAll(filepath.Join(home, ".config"), 0o755); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(home, ".config", "cartographer")
		if err := os.Symlink(victim, link); err != nil {
			t.Fatal(err)
		}
		err := SaveDerived("/cfg", state)
		if err == nil || !strings.Contains(err.Error(), "symlink") {
			t.Fatalf("err = %v, want a symlink refusal", err)
		}
		empty(t, victim)
		if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("the link was replaced (err %v)", err)
		}
	})
	t.Run("leaf", func(t *testing.T) {
		home, victim := t.TempDir(), t.TempDir()
		t.Setenv("HOME", home)
		cart := filepath.Join(home, ".config", "cartographer")
		if err := os.MkdirAll(cart, 0o755); err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(victim, "t.json")
		writeFile(t, target, "keep", 0o644)
		if err := os.Symlink(target, filepath.Join(cart, DerivedFileName)); err != nil {
			t.Fatal(err)
		}
		if err := SaveDerived("/cfg", state); err == nil {
			t.Fatal("a symlinked leaf was written")
		}
		if got, _ := os.ReadFile(target); string(got) != "keep" {
			t.Errorf("target changed: %q", got)
		}
		if es, _ := os.ReadDir(cart); len(es) != 1 {
			t.Errorf("temp file left behind: %v", es)
		}
	})
	t.Run("config root link", func(t *testing.T) {
		home, real := t.TempDir(), t.TempDir()
		t.Setenv("HOME", home)
		if err := os.Symlink(real, filepath.Join(home, ".config")); err != nil {
			t.Fatal(err)
		}
		if err := SaveDerived("/cfg", state); err != nil {
			t.Fatalf("a symlinked ~/.config must keep working: %v", err)
		}
		if _, err := os.Stat(filepath.Join(real, "cartographer", DerivedFileName)); err != nil {
			t.Fatal(err)
		}
	})
}

func TestWriteTokenRefusesASymlinkedLeaf(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	dir, victim := t.TempDir(), t.TempDir()
	target := filepath.Join(victim, "t")
	writeFile(t, target, "keep", 0o600)
	if err := os.Symlink(target, TokenPath(dir)); err != nil {
		t.Fatal(err)
	}
	if err := WriteToken(dir, "tok"); err == nil {
		t.Fatal("a symlinked token file was written")
	}
	if got, _ := os.ReadFile(target); string(got) != "keep" {
		t.Errorf("target changed: %q", got)
	}
	if es, _ := os.ReadDir(dir); len(es) != 1 {
		t.Errorf("temp file left behind: %v", es)
	}
}

// A symlinked config directory (a dotfile manager's) still takes the token.
func TestWriteTokenThroughASymlinkedConfigDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	real := t.TempDir()
	dir := filepath.Join(t.TempDir(), "cfg")
	if err := os.Symlink(real, dir); err != nil {
		t.Fatal(err)
	}
	if err := WriteToken(dir, "tok"); err != nil {
		t.Fatal(err)
	}
	if got, _ := ReadToken(dir); got != "tok" {
		t.Errorf("token = %q", got)
	}
}
