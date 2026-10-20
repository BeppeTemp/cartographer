package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
)

// seedPushedKB creates a data dir holding one KB called name whose commits are
// all pushed to a local bare origin.
func seedPushedKB(t *testing.T, name string) (dataDir, origin string) {
	t.Helper()
	dataDir = seedRenameKB(t, name)
	dir := filepath.Join(dataDir, name)
	origin = filepath.Join(t.TempDir(), name+".git")
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q", "--bare", origin)
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		git("-C", dir, "init", "-q")
	}
	git("-C", dir, "add", "-A")
	git("-C", dir, "commit", "-q", "--allow-empty", "-m", "seed")
	git("-C", dir, "remote", "add", "origin", origin)
	git("-C", dir, "push", "-q", "-u", "origin", "HEAD")
	return dataDir, origin
}

func runUnmount(t *testing.T, args ...string) (int, string) {
	t.Helper()
	var code int
	orig := printPostUnmountGuidanceFn
	printPostUnmountGuidanceFn = func(string, bool) {}
	defer func() { printPostUnmountGuidanceFn = orig }()
	out := withStdout(t, func() { code = cmdKBUnmount(args) })
	return code, out
}

// TestCmdKBUnmount_HappyPath: the entry goes (comments kept, backup written),
// the bindings go, and the clone leaves the data dir so discovery cannot
// remount it (D761).
func TestCmdKBUnmount_HappyPath(t *testing.T) {
	if !hasGitBinary() {
		t.Skip("git not available")
	}
	home := writeClientBindings(t, "http://127.0.0.1:39273")
	dataDir, origin := seedPushedKB(t, "wiki")
	cfgPath := writeServerConfig(t, "# operator note\ndata: "+dataDir+"\nkbs:\n  - remote: https://git.example.com/user/wiki.git # the wiki\n    name: wiki\n  - remote: https://git.example.com/user/other.git\n    name: other\n")

	code, out := runUnmount(t, "wiki", "--data", dataDir, "--config", cfgPath)
	if code != 0 {
		t.Fatalf("exit = %d:\n%s", code, out)
	}
	cfg, _ := os.ReadFile(cfgPath)
	if strings.Contains(string(cfg), "name: wiki") || !strings.Contains(string(cfg), "name: other") || !strings.Contains(string(cfg), "# operator note") {
		t.Errorf("server config not edited in place:\n%s", cfg)
	}
	backups, _ := filepath.Glob(cfgPath + ".bak-*")
	if len(backups) != 1 {
		t.Errorf("backups = %v, want one", backups)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "wiki")); !os.IsNotExist(err) {
		t.Error("the clone is still in the data dir: discovery would remount it")
	}
	if _, err := os.Stat(filepath.Join(dataDir, unmountedDir, "wiki", "data", "index.md")); err != nil {
		t.Errorf("clone not parked: %v", err)
	}
	c, err := clientconfig.Load(home)
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Clients["claude"].KBs; !slices.Equal(got, []string{"kb-b"}) {
		t.Errorf("clients.claude.kbs = %v, want [kb-b]", got)
	}
	if got := c.Workspaces["kiro"][0].KBs; len(got) != 0 {
		t.Errorf("workspaces.kiro[0].kbs = %v, want empty", got)
	}
	if !strings.Contains(out, "kb clone "+origin+" wiki") {
		t.Errorf("no way back printed:\n%s", out)
	}
}

func TestCmdKBUnmount_DeleteCloneOfDiscoveredKB(t *testing.T) {
	if !hasGitBinary() {
		t.Skip("git not available")
	}
	writeClientBindings(t, "http://127.0.0.1:39273")
	dataDir, _ := seedPushedKB(t, "wiki")
	cfgPath := writeServerConfig(t, "data: "+dataDir+"\n")

	code, out := runUnmount(t, "wiki", "--data", dataDir, "--config", cfgPath, "--delete-clone")
	if code != 0 {
		t.Fatalf("exit = %d:\n%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "wiki")); !os.IsNotExist(err) {
		t.Error("clone not deleted")
	}
	if backups, _ := filepath.Glob(cfgPath + ".bak-*"); len(backups) != 0 {
		t.Errorf("no entry to remove, yet a backup was written: %v", backups)
	}
}

// TestCmdKBUnmount_Refusals: nothing changes when the clone cannot be proven
// pushed, unless --force.
func TestCmdKBUnmount_Refusals(t *testing.T) {
	if !hasGitBinary() {
		t.Skip("git not available")
	}
	writeClientBindings(t, "http://127.0.0.1:39273")

	t.Run("dirty tree, then --force", func(t *testing.T) {
		dataDir, _ := seedPushedKB(t, "wiki")
		cfgPath := writeServerConfig(t, "data: "+dataDir+"\n")
		if err := os.WriteFile(filepath.Join(dataDir, "wiki", "data", "new.md"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if code, out := runUnmount(t, "wiki", "--data", dataDir, "--config", cfgPath); code == 0 {
			t.Fatalf("dirty tree accepted:\n%s", out)
		}
		if _, err := os.Stat(filepath.Join(dataDir, "wiki")); err != nil {
			t.Fatal("clone touched on refusal")
		}
		if code, out := runUnmount(t, "wiki", "--data", dataDir, "--config", cfgPath, "--force"); code != 0 {
			t.Fatalf("--force refused:\n%s", out)
		}
	})
	t.Run("no origin", func(t *testing.T) {
		dataDir := seedRenameKB(t, "wiki")
		cfgPath := writeServerConfig(t, "data: "+dataDir+"\n")
		if code, out := runUnmount(t, "wiki", "--data", dataDir, "--config", cfgPath); code == 0 {
			t.Fatalf("a clone with no origin was accepted:\n%s", out)
		}
	})
	t.Run("parked copy exists", func(t *testing.T) {
		dataDir, _ := seedPushedKB(t, "wiki")
		cfgPath := writeServerConfig(t, "data: "+dataDir+"\n")
		if err := os.MkdirAll(filepath.Join(dataDir, unmountedDir, "wiki"), 0o755); err != nil {
			t.Fatal(err)
		}
		if code, out := runUnmount(t, "wiki", "--data", dataDir, "--config", cfgPath); code == 0 {
			t.Fatalf("existing parked copy accepted:\n%s", out)
		}
	})
	t.Run("dry run", func(t *testing.T) {
		dataDir, _ := seedPushedKB(t, "wiki")
		cfgPath := writeServerConfig(t, "data: "+dataDir+"\n")
		if code, out := runUnmount(t, "wiki", "--data", dataDir, "--config", cfgPath, "--dry-run"); code != 0 {
			t.Fatalf("exit = %d:\n%s", code, out)
		}
		if _, err := os.Stat(filepath.Join(dataDir, "wiki")); err != nil {
			t.Fatal("dry run changed the clone")
		}
	})
}
