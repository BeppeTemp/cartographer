package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/config"
	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/kbtest"
)

// D1093: `kb create --branch X` sets the KB's own `branch`, committed on the
// remote's default branch before X is created from it, and leaves the clone on X.
func TestCmdKBCreateBranch(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH")
	}
	t.Run("empty remote", func(t *testing.T) {
		tmp := t.TempDir()
		bare := filepath.Join(tmp, "alpha.git")
		mustRunGit(t, "", "init", "--bare", bare)
		dataDir := filepath.Join(tmp, "data")
		var code int
		out := withStdout(t, func() {
			withNoGuidance(t, func() {
				code = cmdKBCreate([]string{"alpha", "--data", dataDir, "--remote", fileURL(bare), "--branch", "team-kb"})
			})
		})
		if code != 0 {
			t.Fatalf("cmdKBCreate = %d", code)
		}
		if !strings.Contains(out, "branch: team-kb") {
			t.Errorf("stdout lacks the branch line:\n%s", out)
		}
		path := filepath.Join(dataDir, "alpha")
		if got := gitOut(t, path, "symbolic-ref", "--short", "HEAD"); got != "team-kb" {
			t.Errorf("clone on %q, want team-kb", got)
		}
		heads := gitOut(t, bare, "for-each-ref", "--format=%(refname)", "refs/heads/")
		if !strings.Contains(heads, "refs/heads/main") || !strings.Contains(heads, "refs/heads/team-kb") {
			t.Errorf("remote heads = %q, want main and team-kb", heads)
		}
		// The setting is read from the default branch, so it is committed there.
		if got := gitOut(t, bare, "show", "main:settings.yaml"); !strings.Contains(got, "\nbranch: team-kb\n") {
			t.Errorf("settings.yaml on main does not set the branch:\n%s", got)
		}
		if got := gitOut(t, path, "rev-parse", "@{upstream}"); got != gitOut(t, bare, "rev-parse", "team-kb") {
			t.Errorf("team-kb does not track the remote's team-kb")
		}
	})
	t.Run("forge initial commit", func(t *testing.T) {
		tmp := t.TempDir()
		bare := seedForgeRemote(t, tmp, "trunk", []string{"README.md", "LICENSE"}, 1)
		dataDir := filepath.Join(tmp, "data")
		var code int
		withNoGuidance(t, func() {
			code = cmdKBCreate([]string{"alpha", "--data", dataDir, "--remote", fileURL(bare), "--branch", "team-kb"})
		})
		if code != 0 {
			t.Fatalf("cmdKBCreate = %d", code)
		}
		if got := gitOut(t, bare, "show", "trunk:settings.yaml"); !strings.Contains(got, "\nbranch: team-kb\n") {
			t.Errorf("settings.yaml on the remote's default branch does not set the branch:\n%s", got)
		}
		if heads := gitOut(t, bare, "for-each-ref", "--format=%(refname)", "refs/heads/"); !strings.Contains(heads, "refs/heads/team-kb") || strings.Contains(heads, "refs/heads/main") {
			t.Errorf("remote heads = %q", heads)
		}
	})
	t.Run("the default branch itself writes no key", func(t *testing.T) {
		tmp := t.TempDir()
		bare := filepath.Join(tmp, "alpha.git")
		mustRunGit(t, "", "init", "--bare", bare)
		dataDir := filepath.Join(tmp, "data")
		var code int
		withNoGuidance(t, func() {
			code = cmdKBCreate([]string{"alpha", "--data", dataDir, "--remote", fileURL(bare), "--branch", "main"})
		})
		if code != 0 {
			t.Fatalf("cmdKBCreate = %d", code)
		}
		if got := gitOut(t, bare, "show", "main:settings.yaml"); strings.Contains(got, "\nbranch: main") {
			t.Errorf("a branch key was written for the default branch:\n%s", got)
		}
	})
}

func TestCmdKBCreateBranchUsageErrors(t *testing.T) {
	dataDir := t.TempDir()
	for name, args := range map[string][]string{
		"no remote":  {"alpha", "--data", dataDir, "--no-remote", "--branch", "x"},
		"invalid":    {"alpha", "--data", dataDir, "--remote", "https://git.example.com/user/a.git", "--branch", "a..b"},
		"whitespace": {"alpha", "--data", dataDir, "--remote", "https://git.example.com/user/a.git", "--branch", "  "},
	} {
		t.Run(name, func(t *testing.T) {
			var code int
			withNoGuidance(t, func() { code = cmdKBCreate(args) })
			if code != 2 {
				t.Errorf("exit = %d, want 2", code)
			}
			if _, err := os.Stat(filepath.Join(dataDir, "alpha")); err == nil {
				t.Error("a scaffold was left behind")
			}
		})
	}
}

// bareWithKB is a bare remote holding a KB on its default branch.
func bareWithKB(t *testing.T, tmp string) (bare, branch string) {
	t.Helper()
	src := kbtest.New(t).Root
	head, err := os.ReadFile(filepath.Join(src, ".git", "HEAD"))
	if err != nil {
		t.Fatal(err)
	}
	branch = strings.TrimSpace(strings.TrimPrefix(string(head), "ref: refs/heads/"))
	bare = filepath.Join(tmp, "wiki-kb.git")
	mustRunGit(t, "", "init", "--bare", bare)
	mustRunGit(t, src, "remote", "add", "origin", bare)
	mustRunGit(t, src, "push", "origin", branch+":"+branch)
	mustRunGit(t, bare, "symbolic-ref", "HEAD", "refs/heads/"+branch)
	return bare, branch
}

// D1093: `kb clone --branch X` checks X out and records kbs[].branch in the
// server file, keeping its comments and entries.
func TestCmdKBCloneBranch(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH")
	}
	tmp := t.TempDir()
	bare, _ := bareWithKB(t, tmp)
	data := filepath.Join(tmp, "data")
	cfgPath := filepath.Join(tmp, "config.yaml")
	original := "# my server\nhttp: \":9090\" # kept\nkbs:\n  - remote: https://git.example.com/user/kb-a.git # first\n"
	if err := os.WriteFile(cfgPath, []byte(original), 0o640); err != nil {
		t.Fatal(err)
	}
	var code int
	out := withStdout(t, func() {
		withNoGuidance(t, func() {
			code = cmdKBClone([]string{fileURL(bare), "--data", data, "--config", cfgPath, "--branch", "mine"})
		})
	})
	if code != 0 {
		t.Fatalf("cmdKBClone = %d", code)
	}
	mounted := filepath.Join(data, "wiki-kb")
	if got := gitOut(t, mounted, "symbolic-ref", "--short", "HEAD"); got != "mine" {
		t.Errorf("clone on %q, want mine", got)
	}
	if !strings.Contains(out, "kbs[].branch") {
		t.Errorf("stdout does not say where the branch was recorded:\n%s", out)
	}
	text, _ := os.ReadFile(cfgPath)
	for _, kept := range []string{"# my server", `http: ":9090" # kept`, "# first", "remote: https://git.example.com/user/kb-a.git"} {
		if !strings.Contains(string(text), kept) {
			t.Errorf("%q was lost:\n%s", kept, text)
		}
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("the edited file does not load: %v", err)
	}
	if len(cfg.KBs) != 2 || cfg.KBs[1].Remote != fileURL(bare) || cfg.KBs[1].BranchOverride != "mine" || cfg.KBs[1].Name != "" {
		t.Errorf("kbs = %+v, want the new entry {remote, branch} after the existing one", cfg.KBs)
	}
}

// A different name from the remote's is kept in the entry, and a KB whose own
// setting names another branch is told the override wins.
func TestCmdKBCloneBranchNameAndTeamSetting(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH")
	}
	tmp := t.TempDir()
	bare, branch := bareWithKB(t, tmp)
	work := filepath.Join(tmp, "work")
	mustRunGit(t, "", "clone", "-q", bare, work)
	if err := os.WriteFile(filepath.Join(work, kb.SettingsFile), []byte("branch: team-kb\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRunGit(t, work, "add", kb.SettingsFile)
	mustRunGit(t, work, "-c", "user.name=user", "-c", "user.email=user@example.com", "commit", "-q", "-m", "settings")
	mustRunGit(t, work, "push", "-q", "origin", branch)

	cfgPath := filepath.Join(tmp, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("data: /srv\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var code int
	out := withStdout(t, func() {
		withNoGuidance(t, func() {
			code = cmdKBClone([]string{fileURL(bare), "kb-b", "--data", filepath.Join(tmp, "data"), "--config", cfgPath, "--branch", "mine"})
		})
	})
	if code != 0 {
		t.Fatalf("cmdKBClone = %d", code)
	}
	if !strings.Contains(out, "server override wins") {
		t.Errorf("stdout lacks the override note:\n%s", out)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil || len(cfg.KBs) != 1 || cfg.KBs[0].Name != "kb-b" || cfg.KBs[0].BranchOverride != "mine" {
		t.Errorf("cfg = %+v, err = %v", cfg, err)
	}
}

// Nothing is cloned when the server file cannot take the entry.
func TestCmdKBCloneBranchRefusals(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH")
	}
	tmp := t.TempDir()
	bare, _ := bareWithKB(t, tmp)
	data := filepath.Join(tmp, "data")
	t.Run("no config file", func(t *testing.T) {
		var code int
		withNoGuidance(t, func() {
			code = cmdKBClone([]string{fileURL(bare), "--data", data, "--config", filepath.Join(tmp, "absent.yaml"), "--branch", "mine"})
		})
		if code != 1 {
			t.Errorf("exit = %d, want 1", code)
		}
		if _, err := os.Stat(filepath.Join(data, "wiki-kb")); err == nil {
			t.Error("the KB was cloned")
		}
	})
	t.Run("entry exists", func(t *testing.T) {
		cfgPath := filepath.Join(tmp, "config.yaml")
		if err := os.WriteFile(cfgPath, []byte("kbs:\n  - remote: "+fileURL(bare)+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		var code int
		withNoGuidance(t, func() {
			code = cmdKBClone([]string{fileURL(bare), "--data", data, "--config", cfgPath, "--branch", "mine"})
		})
		if code != 1 {
			t.Errorf("exit = %d, want 1", code)
		}
		if _, err := os.Stat(filepath.Join(data, "wiki-kb")); err == nil {
			t.Error("the KB was cloned")
		}
	})
	t.Run("invalid name", func(t *testing.T) {
		var code int
		withNoGuidance(t, func() {
			code = cmdKBClone([]string{fileURL(bare), "--data", data, "--branch", "-x"})
		})
		if code != 2 {
			t.Errorf("exit = %d, want 2", code)
		}
	})
}
