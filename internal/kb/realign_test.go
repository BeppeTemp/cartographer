package kb

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/gitx"
	"github.com/BeppeTemp/cartographer/internal/settingschema"
)

// realignSchema stands in for config.KBSettingsSchema, which kb cannot import.
var realignSchema = settingschema.Schema{Keys: []settingschema.Key{
	{Path: "write_gate", Scope: settingschema.ScopeKB, Type: "string", Default: "off", Description: "Gate."},
	{Path: "branch", Scope: settingschema.ScopeKB, Type: "string", Default: `""`, Description: "Branch."},
}}

func realignBytes(data []byte) ([]byte, settingschema.Report, error) {
	return realignSchema.Realign(data, SettingsRetired)
}

func writeSettings(t *testing.T, k *KB, body string) {
	t.Helper()
	path := filepath.Join(k.Root, SettingsFile)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	gitHere(t, k.Root, "add", SettingsFile)
	gitHere(t, k.Root, "-c", "user.name=user", "-c", "user.email=user@example.com", "commit", "-q", "-m", "settings")
}

// D903: the realignment of a KB's settings.yaml is one commit of the server's,
// through the normal commit path, so reverting it restores the file.
func TestRealignSettingsIsOneRevertibleCommit(t *testing.T) {
	t.Parallel()
	k, _ := initGitKB(t)
	old := "write_gate: error\ngit_profile: local\ngit_branch: team/kb-data\n"
	writeSettings(t, k, old)
	before := gitHere(t, k.Root, "rev-list", "--count", "HEAD")
	headBefore := gitHere(t, k.Root, "rev-parse", "HEAD")

	rep, sha, err := k.RealignSettings(realignBytes, "test run")
	if err != nil {
		t.Fatal(err)
	}
	if rep.Empty() || sha == "" {
		t.Fatalf("nothing realigned: report %v sha %q", rep.Lines(), sha)
	}
	if got := gitHere(t, k.Root, "rev-list", "--count", "HEAD"); got == before {
		t.Fatal("no commit was made")
	}
	if n := gitHere(t, k.Root, "rev-list", "--count", headBefore+"..HEAD"); n != "1" {
		t.Errorf("the realignment is %s commits, want one", n)
	}
	data, _ := os.ReadFile(filepath.Join(k.Root, SettingsFile))
	text := string(data)
	if strings.Contains(text, "git_profile:") || strings.Contains(text, "git_branch:") || !strings.Contains(text, "write_gate: error") {
		t.Errorf("settings.yaml after the realignment:\n%s", text)
	}
	if st := gitHere(t, k.Root, "status", "--short"); st != "" {
		t.Errorf("the realignment left the tree dirty:\n%s", st)
	}
	if subject := gitHere(t, k.Root, "log", "-1", "--format=%s"); subject != "settings: realign settings.yaml" {
		t.Errorf("subject = %q", subject)
	}
	if body := gitHere(t, k.Root, "log", "-1", "--format=%B"); !strings.Contains(body, "Reason: test run") {
		t.Errorf("no Reason trailer:\n%s", body)
	}
	// Reverting it restores the file as it was.
	gitHere(t, k.Root, "-c", "user.name=user", "-c", "user.email=user@example.com", "revert", "--no-edit", "HEAD")
	if restored, _ := os.ReadFile(filepath.Join(k.Root, SettingsFile)); string(restored) != old {
		t.Errorf("revert did not restore the file:\n%s", restored)
	}
}

// A second run changes nothing and commits nothing.
func TestRealignSettingsSecondRunIsANoOp(t *testing.T) {
	t.Parallel()
	k, _ := initGitKB(t)
	writeSettings(t, k, "git_profile: local\n")
	if _, _, err := k.RealignSettings(realignBytes, "first"); err != nil {
		t.Fatal(err)
	}
	head := gitHere(t, k.Root, "rev-parse", "HEAD")
	rep, sha, err := k.RealignSettings(realignBytes, "second")
	if err != nil || !rep.Empty() || sha != "" {
		t.Fatalf("second run: report %v sha %q err %v", rep.Lines(), sha, err)
	}
	if gitHere(t, k.Root, "rev-parse", "HEAD") != head {
		t.Error("the second run committed")
	}
}

// A KB without a settings.yaml, or that is not a git repository, is left alone.
func TestRealignSettingsLeavesWhatItCannotVouchFor(t *testing.T) {
	t.Parallel()
	k, _ := initGitKB(t)
	head := gitHere(t, k.Root, "rev-parse", "HEAD")
	if rep, _, err := k.RealignSettings(realignBytes, "x"); err != nil || !rep.Empty() {
		t.Errorf("no settings.yaml: %v %v", rep.Lines(), err)
	}
	if gitHere(t, k.Root, "rev-parse", "HEAD") != head {
		t.Error("committed with no settings.yaml")
	}

	plain := &KB{Root: t.TempDir()}
	if err := os.WriteFile(filepath.Join(plain.Root, SettingsFile), []byte("git_profile: local\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if rep, _, err := plain.RealignSettings(realignBytes, "x"); err != nil || !rep.Empty() {
		t.Errorf("not a repository: %v %v", rep.Lines(), err)
	}
	if b, _ := os.ReadFile(filepath.Join(plain.Root, SettingsFile)); string(b) != "git_profile: local\n" {
		t.Errorf("a non-repository file was rewritten: %q", b)
	}
}

// A person's uncommitted edit to settings.yaml is not taken along in the
// server's commit: the file is left as it is.
func TestRealignSettingsSkipsAFileWithLocalEdits(t *testing.T) {
	t.Parallel()
	k, _ := initGitKB(t)
	writeSettings(t, k, "write_gate: error\n")
	edited := "write_gate: warning\ngit_profile: local\n"
	if err := os.WriteFile(filepath.Join(k.Root, SettingsFile), []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	head := gitHere(t, k.Root, "rev-parse", "HEAD")
	_, _, err := k.RealignSettings(realignBytes, "x")
	if err == nil || !strings.Contains(err.Error(), "uncommitted") {
		t.Fatalf("err = %v, want the uncommitted edit named", err)
	}
	if b, _ := os.ReadFile(filepath.Join(k.Root, SettingsFile)); string(b) != edited {
		t.Errorf("the edited file changed: %q", b)
	}
	if gitHere(t, k.Root, "rev-parse", "HEAD") != head {
		t.Error("committed over a local edit")
	}
}

// Unrelated dirty work in the tree stays out of the commit.
func TestRealignSettingsCommitsOnlyTheSettingsFile(t *testing.T) {
	t.Parallel()
	k, _ := initGitKB(t)
	writeSettings(t, k, "git_profile: local\n")
	other := filepath.Join(k.Root, "data", "wip.md")
	if err := os.WriteFile(other, []byte("---\ntype: Note\ntitle: wip\n---\nwip\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := k.RealignSettings(realignBytes, "x"); err != nil {
		t.Fatal(err)
	}
	if files := gitHere(t, k.Root, "show", "--name-only", "--format=", "HEAD"); strings.TrimSpace(files) != SettingsFile {
		t.Errorf("the commit holds %q", files)
	}
	if st := gitHere(t, k.Root, "status", "--short"); !strings.Contains(st, "wip.md") {
		t.Errorf("the unrelated work vanished from the tree: %q", st)
	}
}

// With a remote, the realignment is pushed like any other write.
func TestRealignSettingsIsPushed(t *testing.T) {
	t.Parallel()
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k, bare := setupKBWithRemote(t)
	if err := k.SyncOut(); err != nil {
		t.Fatal(err)
	}
	writeSettings(t, k, "git_profile: local\n")
	if err := k.SyncOut(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := k.RealignSettings(realignBytes, "x"); err != nil {
		t.Fatal(err)
	}
	branch, _ := gitx.Branch(k.Root)
	if gitHere(t, bare, "rev-parse", branch) != gitHere(t, k.Root, "rev-parse", "HEAD") {
		t.Error("the realignment commit did not reach the remote")
	}
}

// Right after a restart the canonical branch is not resolved yet: the
// realignment must not push to the checked-out branch when the default
// branch's settings.yaml names another one.
func TestRealignSettingsDoesNotPushToTheDefaultBranchBeforeResolvingIt(t *testing.T) {
	t.Parallel()
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k, bare := setupKBWithRemote(t)
	k.SyncOutDebounce = 0
	if err := k.SyncOut(); err != nil {
		t.Fatal(err)
	}
	writeSettings(t, k, "git_profile: local\nbranch: dev\n")
	if err := k.SyncOut(); err != nil {
		t.Fatal(err)
	}
	branch, _ := gitx.Branch(k.Root)
	pushed := gitHere(t, bare, "rev-parse", branch)
	k.setCanonical("", false) // as after a restart, before the first sync
	if _, _, err := k.RealignSettings(realignBytes, "x"); err != nil {
		t.Fatal(err)
	}
	if got := gitHere(t, bare, "rev-parse", branch); got != pushed {
		t.Errorf("the realignment commit reached %s, but settings.yaml sends writes to dev", branch)
	}
}
