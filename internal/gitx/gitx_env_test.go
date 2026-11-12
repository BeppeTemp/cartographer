package gitx

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func envValue(env []string, key string) (string, bool) {
	v, ok := "", false
	for _, kv := range env {
		if strings.HasPrefix(kv, key+"=") {
			v, ok = strings.TrimPrefix(kv, key+"="), true
		}
	}
	return v, ok
}

// The 401 test cannot tell the fix from its absence under go test (no tty),
// so the environment is checked directly (#1004).
func TestGitEnv_NeverPrompts(t *testing.T) {
	if v, _ := envValue(gitEnv(nil), "GIT_TERMINAL_PROMPT"); v != "0" {
		t.Errorf("GIT_TERMINAL_PROMPT = %q, want 0", v)
	}
}

// A sync run from a git hook inherits the variables that pin a repository; Env
// is the environment of a command that names its repository with -C.
func TestEnv_DropsTheRepositoryPins(t *testing.T) {
	for _, v := range repoPinVars {
		t.Setenv(v, "/elsewhere")
	}
	env := Env()
	for _, v := range repoPinVars {
		if got, ok := envValue(env, v); ok {
			t.Errorf("%s = %q survives in Env()", v, got)
		}
	}
	if v, _ := envValue(env, "LC_ALL"); v != "C" {
		t.Errorf("LC_ALL = %q, want C", v)
	}
}

func TestNetEnv_SSHBatchMode(t *testing.T) {
	dir := t.TempDir()
	runIn(t, dir, "init", "-q")
	t.Setenv("GIT_SSH_COMMAND", "")
	os.Unsetenv("GIT_SSH_COMMAND")
	os.Unsetenv("GIT_SSH")
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")

	if v, _ := envValue(netEnv(dir, nil), "GIT_SSH_COMMAND"); !strings.Contains(v, "BatchMode=yes") {
		t.Errorf("nothing set: GIT_SSH_COMMAND = %q, want BatchMode=yes", v)
	}
	if v, ok := envValue(netEnv(dir, []string{"GIT_SSH_COMMAND=ssh -i k"}), "GIT_SSH_COMMAND"); !ok || v != "ssh -i k" {
		t.Errorf("caller's GIT_SSH_COMMAND replaced: %q", v)
	}
	if _, ok := envValue(netEnv(dir, []string{"GIT_SSH=/opt/wrapper"}), "GIT_SSH_COMMAND"); ok {
		t.Error("GIT_SSH set: GIT_SSH_COMMAND injected over the wrapper")
	}
	runIn(t, dir, "config", "core.sshCommand", "ssh -J jump")
	if _, ok := envValue(netEnv(dir, nil), "GIT_SSH_COMMAND"); ok {
		t.Error("core.sshCommand set: GIT_SSH_COMMAND injected over it")
	}
}

func TestCheckRepo_TellsNotARepoFromARefusal(t *testing.T) {
	t.Parallel()
	if !hasGit() {
		t.Skip("git not in PATH")
	}
	if ok, err := CheckRepo(t.TempDir()); ok || err != nil {
		t.Fatalf("plain directory: ok=%v err=%v, want false, nil", ok, err)
	}
	if ok, err := CheckRepo(filepath.Join(t.TempDir(), "missing")); ok || err == nil {
		t.Fatalf("missing directory: ok=%v err=%v, want an error", ok, err)
	}
}

// TrackedPathsFold answers for the path as asked, whatever its capitalisation in
// the index, and does not count a file below a directory that folds to it.
func TestTrackedPathsFold(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH")
	}
	dir := t.TempDir()
	mustGit(t, dir, "init", "-q")
	for p, body := range map[string]string{"agents.md": "x", "Dir/File.md": "y", "star/*.md": "z"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, p)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, p), []byte(body), 0o644); err != nil {
			t.Skipf("write %s: %v", p, err) // a filesystem that cannot name a '*' file
		}
		mustGit(t, dir, "add", "-f", p)
	}
	got, err := TrackedPathsFold(dir, "AGENTS.md", "dir/file.md", "DIR", "star/*.md", "STAR/other.md", "missing.md")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"AGENTS.md": true, "dir/file.md": true, "star/*.md": true}
	if len(got) != len(want) {
		t.Fatalf("TrackedPathsFold = %v, want %v", got, want)
	}
	for p := range want {
		if !got[p] {
			t.Errorf("%s must be tracked: %v", p, got)
		}
	}
	if got["DIR"] || got["STAR/other.md"] || got["missing.md"] {
		t.Errorf("a directory, a glob that is no file and a missing path are not tracked: %v", got)
	}
	if none, err := TrackedPathsFold(dir); err != nil || len(none) != 0 {
		t.Errorf("no paths = %v, %v", none, err)
	}
}

// An inherited GIT_LITERAL_PATHSPECS=1 (and its siblings) must not turn
// `:(literal,icase)X` into a file name: the answer would be "untracked".
func TestTrackedPathsFold_IgnoresPathspecEnvironment(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH")
	}
	dir := t.TempDir()
	mustGit(t, dir, "init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "agents.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, dir, "add", "agents.md")
	t.Setenv("GIT_LITERAL_PATHSPECS", "1")
	t.Setenv("GIT_GLOB_PATHSPECS", "1")
	t.Setenv("GIT_NOGLOB_PATHSPECS", "1")
	t.Setenv("GIT_ICASE_PATHSPECS", "0")
	got, err := TrackedPathsFold(dir, "AGENTS.md")
	if err != nil {
		t.Fatal(err)
	}
	if !got["AGENTS.md"] {
		t.Errorf("with GIT_*_PATHSPECS inherited, a tracked file reads as untracked: %v", got)
	}
}

// git may list a name composed (core.precomposeunicode) that was asked
// decomposed: the file is still the file asked about.
func TestTrackedPathsFold_UnicodeNormalisation(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH")
	}
	dir := t.TempDir()
	mustGit(t, dir, "init", "-q")
	nfc := "d\u00e9j\u00e0/\u00e9.md"
	nfd := "de\u0301ja\u0300/e\u0301.md"
	if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(nfc)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, nfc), []byte("x"), 0o644); err != nil {
		t.Skipf("write: %v", err)
	}
	mustGit(t, dir, "add", "-f", "--", nfc)
	probe := exec.Command("git", "-C", dir, "ls-files", "--", nfd)
	probe.Env = gitEnv(nil)
	if b, err := probe.Output(); err != nil || len(b) == 0 {
		t.Skip("this filesystem/git does not precompose Unicode names")
	}
	got, err := TrackedPathsFold(dir, nfd)
	if err != nil {
		t.Fatal(err)
	}
	if !got[nfd] {
		t.Errorf("a decomposed request for a tracked file reads as untracked: %v", got)
	}
}
