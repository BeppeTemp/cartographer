package clientconfig_test

import (
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
)

func TestAllowSymlinks_RoundTripKeepsItAndIsNotABinding(t *testing.T) {
	dir := t.TempDir()
	cfg := clientconfig.Default()
	cfg.SetAllowSymlink("opencode", "$HOME/.config/opencode")
	cfg.SetAllowSymlink("opencode", "$HOME/.config/opencode") // idempotent
	if err := clientconfig.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(clientconfig.Path(dir))
	if strings.Contains(string(raw), "\n    kbs:") {
		t.Errorf("a symlink-only entry gained kbs:\n%s", raw)
	}
	got, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if e := got.AllowedSymlinks("opencode"); len(e) != 1 || e[0] != "$HOME/.config/opencode" {
		t.Errorf("entries = %v", e)
	}
	if got.Clients["opencode"].Explicit() {
		t.Error("allow_symlinks made the binding explicit")
	}
	got.ResetBinding("opencode")
	if len(got.AllowedSymlinks("opencode")) != 1 {
		t.Error("ResetBinding dropped allow_symlinks")
	}
	got.UnsetAllowSymlink("opencode", "$HOME/.config/opencode")
	if len(got.Clients) != 0 {
		t.Errorf("empty entry kept: %v", got.Clients)
	}
}

func TestAllowSymlinks_InvalidEntryIsRejected(t *testing.T) {
	for _, bad := range []string{"relative/path", "", ".config/x", "~", "$HOME", "$HOME/", "/", "~/..", "/a/../b", "$HOME/x/../..", "/tmp/a\x1b[2Jb"} {
		dir := t.TempDir()
		yaml := "server:\n  url: http://localhost:1/mcp\nproviders:\n  opencode:\n    allow_symlinks: [" + strconv.Quote(bad) + "]\n"
		if err := os.WriteFile(clientconfig.Path(dir), []byte(yaml), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := clientconfig.Load(dir); err == nil || !strings.Contains(err.Error(), "allow_symlinks") {
			t.Errorf("entry %q: err = %v", bad, err)
		}
	}
}

// A file that still holds the flat clients: map is read with its allow_symlinks,
// and the realignment moves the whole entry to providers: with the key.
func TestAllowSymlinks_FlatFileIsReadAndRealignedWithTheKey(t *testing.T) {
	dir := t.TempDir()
	flat := "server_url: http://localhost:1/mcp\nclients:\n  opencode:\n    allow_symlinks: [$HOME/.config/opencode]\n"
	if err := os.WriteFile(clientconfig.Path(dir), []byte(flat), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if e := cfg.AllowedSymlinks("opencode"); len(e) != 1 || e[0] != "$HOME/.config/opencode" {
		t.Errorf("flat entries = %v", e)
	}
	if _, _, err := clientconfig.RealignFile(dir, "0.30.0"); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(clientconfig.Path(dir))
	if !strings.Contains(string(raw), "providers:") || !strings.Contains(string(raw), "allow_symlinks: [$HOME/.config/opencode]") || strings.Contains(string(raw), "clients:") {
		t.Errorf("the entry was not moved with its key:\n%s", raw)
	}
}

func TestAllowSymlinks_HomeItselfIsRejectedAndRemoveComparesCleaned(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if clientconfig.ValidSymlinkEntry(home, home) {
		t.Error("the home directory itself was accepted")
	}
	cfg := clientconfig.Default()
	cfg.SetAllowSymlink("opencode", "$HOME/.config/opencode")
	cfg.SetAllowSymlink("opencode", home+"/.config//opencode/")
	if e := cfg.AllowedSymlinks("opencode"); len(e) != 1 {
		t.Errorf("same link under two spellings kept twice: %v", e)
	}
	if !cfg.UnsetAllowSymlink("opencode", "~/.config/opencode/") {
		t.Error("remove by another spelling found nothing")
	}
	if cfg.UnsetAllowSymlink("opencode", "~/.config/opencode") {
		t.Error("removing an absent entry reported found")
	}
}
