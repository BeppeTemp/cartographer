package main

import (
	"os"
	"os/exec"
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

// A value valid for git but another YAML value unquoted is written as a string.
func TestNewKBSettingsFileQuotesTheBranch(t *testing.T) {
	for _, b := range []string{"true", "123", "&a", "!t", "'x", "null"} {
		s, _, err := kb.ParseSettings(newKBSettingsFile(b))
		if err != nil {
			t.Fatalf("%q: %v", b, err)
		}
		if got, _ := s.String("branch"); got != b {
			t.Errorf("branch %q read back as %q", b, got)
		}
	}
}

// D1093: a fresh clone is checked out on the server's override, not on the
// branch the KB's settings.yaml names (which is not read, and not created).
func TestEnsureClonedKBOnOverride(t *testing.T) {
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

	dest, err := ensureClonedKBOn(fileURL(bare), "kb-o", filepath.Join(tmp, "data"), "mine")
	if err != nil {
		t.Fatal(err)
	}
	if got := gitOut(t, dest, "symbolic-ref", "--short", "HEAD"); got != "mine" {
		t.Fatalf("clone on %q, want mine", got)
	}
	if gitOut(t, dest, "branch", "--list", "team-kb") != "" {
		t.Error("the team branch was created locally")
	}
	k, err := kb.Open(dest)
	if err != nil {
		t.Fatal(err)
	}
	k.SetBranchOverride("mine")
	if _, err := k.SyncIn(); err != nil {
		t.Fatalf("SyncIn: %v", err)
	}
	if s := k.GitStatusSnapshot(); s.State == "degraded" || s.BranchSource != "server" {
		t.Fatalf("status = %+v", s)
	}
}

// D1412: `kb create --branch X` on a repository with history and no X puts the
// KB on an orphan branch X: only X is pushed and kbs[].branch is recorded.
func TestCmdKBCreateOrphanBranch(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH")
	}
	history := func(t *testing.T, tmp string) string {
		return seedForgeRemote(t, tmp, "main", []string{"README.md", "app.txt"}, 2)
	}
	t.Run("orphan branch", func(t *testing.T) {
		tmp := t.TempDir()
		bare := history(t, tmp)
		mainSHA := gitOut(t, bare, "rev-parse", "main")
		cfgPath := filepath.Join(tmp, "config.yaml")
		if err := os.WriteFile(cfgPath, []byte("data: /srv\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		dataDir := filepath.Join(tmp, "data")
		var code int
		out := withStdout(t, func() {
			withNoGuidance(t, func() {
				code = cmdKBCreate([]string{"alpha", "--data", dataDir, "--config", cfgPath, "--remote", fileURL(bare), "--branch", "kb", "--readme", "keep"})
			})
		})
		if code != 0 {
			t.Fatalf("cmdKBCreate = %d", code)
		}
		if !strings.Contains(out, "orphan") {
			t.Errorf("stdout does not say the branch is an orphan:\n%s", out)
		}
		if got := gitOut(t, bare, "rev-parse", "main"); got != mainSHA {
			t.Errorf("main moved: %s -> %s", mainSHA, got)
		}
		if err := exec.Command("git", "-C", bare, "merge-base", "main", "kb").Run(); err == nil {
			t.Error("kb shares history with main")
		}
		if got := gitOut(t, bare, "show", "kb:settings.yaml"); !strings.Contains(got, "\nbranch: kb\n") {
			t.Errorf("settings.yaml on kb lacks the branch:\n%s", got)
		}
		path := filepath.Join(dataDir, "alpha")
		if got := gitOut(t, path, "symbolic-ref", "--short", "HEAD"); got != "kb" {
			t.Errorf("clone on %q, want kb", got)
		}
		cfg, err := config.Load(cfgPath)
		if err != nil {
			t.Fatal(err)
		}
		if len(cfg.KBs) != 1 || cfg.KBs[0].BranchOverride != "kb" || cfg.KBs[0].Remote != fileURL(bare) {
			t.Errorf("kbs = %+v", cfg.KBs)
		}
	})
	t.Run("no server config: refused before any push", func(t *testing.T) {
		tmp := t.TempDir()
		bare := history(t, tmp)
		before := gitOut(t, bare, "for-each-ref")
		dataDir := filepath.Join(tmp, "data")
		var code int
		withStderr(t, func() {
			withNoGuidance(t, func() {
				code = cmdKBCreate([]string{"alpha", "--data", dataDir, "--config", filepath.Join(tmp, "missing.yaml"), "--remote", fileURL(bare), "--branch", "kb"})
			})
		})
		if code == 0 {
			t.Fatal("expected a refusal")
		}
		if _, err := os.Stat(filepath.Join(dataDir, "alpha")); err == nil {
			t.Error("the local KB was kept")
		}
		if got := gitOut(t, bare, "for-each-ref"); got != before {
			t.Errorf("the remote changed:\n%s", got)
		}
	})
	t.Run("branch exists or is the default", func(t *testing.T) {
		for _, x := range []string{"main", "other"} {
			tmp := t.TempDir()
			bare := history(t, tmp)
			mustRunGit(t, bare, "branch", "other", "main")
			before := gitOut(t, bare, "for-each-ref")
			cfgPath := filepath.Join(tmp, "config.yaml")
			if err := os.WriteFile(cfgPath, []byte("data: /srv\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			var code int
			errOut := withStderr(t, func() {
				withNoGuidance(t, func() {
					code = cmdKBCreate([]string{"alpha", "--data", filepath.Join(tmp, "data"), "--config", cfgPath, "--remote", fileURL(bare), "--branch", x})
				})
			})
			if code == 0 || !strings.Contains(errOut, "branch "+x) {
				t.Errorf("%s: code %d, stderr:\n%s", x, code, errOut)
			}
			if got := gitOut(t, bare, "for-each-ref"); got != before {
				t.Errorf("%s: the remote changed", x)
			}
		}
	})
	t.Run("no branch: refusal names --branch", func(t *testing.T) {
		tmp := t.TempDir()
		bare := history(t, tmp)
		var code int
		errOut := withStderr(t, func() {
			withNoGuidance(t, func() {
				code = cmdKBCreate([]string{"alpha", "--data", filepath.Join(tmp, "data"), "--remote", fileURL(bare)})
			})
		})
		if code == 0 || !strings.Contains(errOut, "--branch <name>") {
			t.Errorf("code %d, stderr:\n%s", code, errOut)
		}
	})
	t.Run("forge initial commit keeps D1093", func(t *testing.T) {
		tmp := t.TempDir()
		bare := seedForgeRemote(t, tmp, "trunk", []string{"README.md"}, 1)
		cfgPath := filepath.Join(tmp, "config.yaml")
		if err := os.WriteFile(cfgPath, []byte("data: /srv\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		var code int
		withNoGuidance(t, func() {
			code = cmdKBCreate([]string{"alpha", "--data", filepath.Join(tmp, "data"), "--config", cfgPath, "--remote", fileURL(bare), "--branch", "kb"})
		})
		if code != 0 {
			t.Fatalf("cmdKBCreate = %d", code)
		}
		if got := gitOut(t, bare, "show", "trunk:settings.yaml"); !strings.Contains(got, "\nbranch: kb\n") {
			t.Errorf("default branch lacks settings.yaml:\n%s", got)
		}
		if cfg, _ := config.Load(cfgPath); len(cfg.KBs) != 0 {
			t.Errorf("kbs = %+v, want none", cfg.KBs)
		}
	})
}

// bareKBOnBranch is a bare remote whose default branch `main` holds plain files
// and whose branch `kb` is an orphan KB (D1412).
func bareKBOnBranch(t *testing.T, tmp string) string {
	t.Helper()
	bare := seedForgeRemote(t, tmp, "main", []string{"README.md", "app.txt"}, 2)
	src := kbtest.New(t).Root
	mustRunGit(t, src, "branch", "-M", "kb")
	mustRunGit(t, src, "push", bare, "kb:kb")
	return bare
}

// D1412: `kb clone --branch X` mounts a KB on a branch of a repository whose
// default branch holds none; the layout is checked after the checkout.
func TestCmdKBCloneOrphanBranch(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH")
	}
	newCfg := func(t *testing.T, tmp string) string {
		p := filepath.Join(tmp, "config.yaml")
		if err := os.WriteFile(p, []byte("data: /srv\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	t.Run("branch given", func(t *testing.T) {
		tmp := t.TempDir()
		bare := bareKBOnBranch(t, tmp)
		cfgPath := newCfg(t, tmp)
		data := filepath.Join(tmp, "data")
		var code int
		withStdout(t, func() {
			withNoGuidance(t, func() {
				code = cmdKBClone([]string{fileURL(bare), "kb-x", "--data", data, "--config", cfgPath, "--branch", "kb"})
			})
		})
		if code != 0 {
			t.Fatalf("cmdKBClone = %d", code)
		}
		if got := gitOut(t, filepath.Join(data, "kb-x"), "symbolic-ref", "--short", "HEAD"); got != "kb" {
			t.Errorf("clone on %q, want kb", got)
		}
		if cfg, _ := config.Load(cfgPath); len(cfg.KBs) != 1 || cfg.KBs[0].BranchOverride != "kb" {
			t.Errorf("kbs = %+v", cfg.KBs)
		}
	})
	t.Run("no branch: hint names the KB branch", func(t *testing.T) {
		tmp := t.TempDir()
		bare := bareKBOnBranch(t, tmp)
		data := filepath.Join(tmp, "data")
		var code int
		errOut := withStderr(t, func() {
			withNoGuidance(t, func() {
				code = cmdKBClone([]string{fileURL(bare), "kb-x", "--data", data})
			})
		})
		if code != 1 || !strings.Contains(errOut, "--branch kb") {
			t.Errorf("code %d, stderr:\n%s", code, errOut)
		}
		if _, err := os.Stat(filepath.Join(data, "kb-x")); err == nil {
			t.Error("the clone was kept")
		}
	})
	t.Run("absent branch, default holds no KB", func(t *testing.T) {
		tmp := t.TempDir()
		bare := bareKBOnBranch(t, tmp)
		var code int
		errOut := withStderr(t, func() {
			withNoGuidance(t, func() {
				code = cmdKBClone([]string{fileURL(bare), "kb-x", "--data", filepath.Join(tmp, "data"), "--config", newCfg(t, tmp), "--branch", "absent"})
			})
		})
		if code != 1 || !strings.Contains(errOut, "default branch holds no KB") {
			t.Errorf("code %d, stderr:\n%s", code, errOut)
		}
	})
	t.Run("default is a KB, own branch", func(t *testing.T) {
		tmp := t.TempDir()
		bare, _ := bareWithKB(t, tmp)
		data := filepath.Join(tmp, "data")
		var code int
		withStdout(t, func() {
			withNoGuidance(t, func() {
				code = cmdKBClone([]string{fileURL(bare), "kb-x", "--data", data, "--config", newCfg(t, tmp), "--branch", "own"})
			})
		})
		if code != 0 {
			t.Fatalf("cmdKBClone = %d", code)
		}
		if got := gitOut(t, filepath.Join(data, "kb-x"), "symbolic-ref", "--short", "HEAD"); got != "own" {
			t.Errorf("clone on %q, want own", got)
		}
	})
}

// D1412 trap: a server whose default branch holds no KB (and no settings.yaml)
// follows the kbs[].branch override, and a sync round trip never touches the
// default branch.
func TestOrphanBranchServerSync(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH")
	}
	tmp := t.TempDir()
	bare := bareKBOnBranch(t, tmp)
	mainSHA := gitOut(t, bare, "rev-parse", "main")
	dest, err := ensureClonedKBOn(fileURL(bare), "kb-o", filepath.Join(tmp, "data"), "kb")
	if err != nil {
		t.Fatal(err)
	}
	if got := gitOut(t, dest, "symbolic-ref", "--short", "HEAD"); got != "kb" {
		t.Fatalf("clone on %q, want kb", got)
	}
	k, err := kb.Open(dest)
	if err != nil {
		t.Fatal(err)
	}
	k.SetBranchOverride("kb")
	if _, err := k.SyncIn(); err != nil {
		t.Fatalf("SyncIn: %v", err)
	}
	if err := k.SyncOut(); err != nil {
		t.Fatalf("SyncOut: %v", err)
	}
	if s := k.GitStatusSnapshot(); s.State == "degraded" || s.BranchSource != "server" {
		t.Fatalf("status = %+v", s)
	}
	if got := gitOut(t, dest, "symbolic-ref", "--short", "HEAD"); got != "kb" {
		t.Errorf("clone left kb for %q", got)
	}
	if got := gitOut(t, bare, "rev-parse", "main"); got != mainSHA {
		t.Errorf("main moved")
	}
}
