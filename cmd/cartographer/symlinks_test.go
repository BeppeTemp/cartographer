package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/safewrite"
)

// The allow set is filled from the home directory's client file only: a
// .cartographer.yaml in the working directory (a cloned repository) never
// decides which links are followed.
func TestRegisterAllowedSymlinks_ReadsOnlyTheHomeFile(t *testing.T) {
	t.Cleanup(safewrite.ResetAllowedLinks)
	home := writeClientCfg(t, []string{"opencode"}, nil, map[string]clientconfig.ClientBinding{
		"opencode": {AllowSymlinks: []string{"$HOME/.config/opencode"}},
	})
	repo := t.TempDir()
	yaml := "server:\n  url: http://localhost:1/mcp\nproviders:\n  claude:\n    allow_symlinks: [\"/etc\"]\n"
	if err := os.WriteFile(filepath.Join(repo, clientconfig.FileName), []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(repo)

	registerAllowedSymlinks()
	got := safewrite.AllowedLinks()
	if len(got) != 1 || got[0] != filepath.Join(home, ".config", "opencode") {
		t.Errorf("allowed = %v, want only the home entry", got)
	}
}

func TestRegisterAllowedSymlinks_NoConfigRegistersNothing(t *testing.T) {
	t.Cleanup(safewrite.ResetAllowedLinks)
	setHome(t, t.TempDir())
	registerAllowedSymlinks()
	if got := safewrite.AllowedLinks(); len(got) != 0 {
		t.Errorf("allowed = %v", got)
	}
}

func TestClientSymlinks_AddListRemove(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	home := writeClientCfg(t, []string{"opencode"}, nil, nil)
	if err := os.Symlink(t.TempDir(), filepath.Join(home, "oc")); err != nil {
		t.Fatal(err)
	}
	withStdout(t, func() {
		if code := cmdClient([]string{"symlinks", "opencode", "--add", "$HOME/oc", "--add", "$HOME/oc"}); code != 0 {
			t.Errorf("add = %d", code)
		}
	})
	if e := loadClientCfg(t, home).AllowedSymlinks("opencode"); len(e) != 1 || e[0] != "$HOME/oc" {
		t.Errorf("entries = %v", e)
	}
	out := withStdout(t, func() { cmdClient([]string{"symlinks", "opencode"}) })
	if !strings.Contains(out, `"$HOME/oc" -> `) {
		t.Errorf("list output:\n%s", out)
	}
	if code := cmdClient([]string{"symlinks", "opencode", "--add", "relative"}); code != 2 {
		t.Errorf("relative add = %d, want 2", code)
	}
	if code := cmdClient([]string{"symlinks", "nope", "--add", "$HOME/x"}); code != 2 {
		t.Errorf("unknown provider = %d, want 2", code)
	}
	if code := cmdClient([]string{"symlinks", "opencode", "--remove", "$HOME/other"}); code != 1 {
		t.Errorf("remove of an unlisted entry = %d, want 1", code)
	}
	if code := cmdClient([]string{"symlinks", "opencode", "--add", "$HOME"}); code != 2 {
		t.Errorf("add of the home dir = %d, want 2", code)
	}
	withStdout(t, func() { cmdClient([]string{"symlinks", "opencode", "--remove", "~/oc/"}) })
	if e := loadClientCfg(t, home).AllowedSymlinks("opencode"); len(e) != 0 {
		t.Errorf("entries after remove = %v", e)
	}
}

// The set the plugin registration used is replaced, not merged, by the full
// load: with no config behind it nothing stays followed.
func TestRegisterAllowedSymlinks_ResetsTheLightSet(t *testing.T) {
	t.Cleanup(safewrite.ResetAllowedLinks)
	home := t.TempDir()
	setHome(t, home)
	safewrite.SetAllowedLinks([]string{filepath.Join(home, "left-by-registration")})
	registerAllowedSymlinks()
	if got := safewrite.AllowedLinks(); len(got) != 0 {
		t.Errorf("allowed = %v", got)
	}
}
