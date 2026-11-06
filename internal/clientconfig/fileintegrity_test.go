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
