package kb

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// commitBranchSetting commits a settings.yaml that sets `branch: value` on
// branch of the bare remote (created from main when it does not exist), the way
// a teammate would: from a clone of their own.
func commitBranchSetting(t *testing.T, bare, onBranch, value string) {
	t.Helper()
	work := filepath.Join(t.TempDir(), "teammate")
	gitIn(t, filepath.Dir(work), "clone", "-q", bare, work)
	if onBranch != "main" {
		gitIn(t, work, "checkout", "-q", "-b", onBranch, "origin/main")
	}
	if err := os.WriteFile(filepath.Join(work, SettingsFile), []byte("branch: "+value+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, work, "add", SettingsFile)
	gitIn(t, work, "-c", "user.name=user", "-c", "user.email=user@example.com", "commit", "-q", "-m", "settings: branch")
	gitIn(t, work, "push", "-q", "origin", onBranch)
}

// D335, D903: a configured branch missing on a non-empty remote is created by
// the first write; the remote's default branch is left untouched.
func TestConfiguredBranch_FirstWriteCreatesIt(t *testing.T) {
	t.Parallel()
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k, bare := remoteWithDefault(t)
	commitBranchSetting(t, bare, "main", "kb-data")
	mainBefore := gitHere(t, bare, "rev-parse", "main")
	gitHere(t, k.Root, "checkout", "-q", "-b", "kb-data")

	if _, err := k.SyncIn(); err != nil {
		t.Fatalf("SyncIn: %v", err)
	}
	if got := k.ConfiguredBranch(); got != "kb-data" {
		t.Fatalf("ConfiguredBranch = %q, want kb-data", got)
	}
	if err := k.WriteFileAtomic("data/a.md", []byte("a\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := k.CommitOp("write a"); err != nil {
		t.Fatalf("CommitOp: %v", err)
	}
	if err := k.SyncOut(); err != nil {
		t.Fatalf("SyncOut: %v", err)
	}
	if got := gitHere(t, bare, "rev-parse", "kb-data"); got != gitHere(t, k.Root, "rev-parse", "HEAD") {
		t.Fatalf("remote kb-data %s is not the KB HEAD", got)
	}
	if got := gitHere(t, bare, "rev-parse", "main"); got != mainBefore {
		t.Fatal("the remote default branch moved")
	}
	if up := gitHere(t, k.Root, "rev-parse", "--abbrev-ref", "kb-data@{upstream}"); up != "origin/kb-data" {
		t.Fatalf("upstream = %q, want origin/kb-data", up)
	}
	s := k.GitStatusSnapshot()
	if s.State != "clean" || s.RemoteDefaultBranch != "kb-data" || s.BranchSource != "settings" {
		t.Fatalf("status = %+v", s)
	}

	// Once on the remote, the branch is pulled and pushed like any other.
	if _, err := k.SyncIn(); err != nil {
		t.Fatalf("SyncIn after creation: %v", err)
	}
}

// D903: `branch` is read from the settings.yaml committed on the remote's
// default branch. One committed on any other branch changes nothing, however the
// working copy sits.
func TestConfiguredBranch_OnlyTheDefaultBranchCounts(t *testing.T) {
	t.Parallel()
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k, bare := remoteWithDefault(t)
	commitBranchSetting(t, bare, "side", "elsewhere")

	if _, err := k.SyncIn(); err != nil {
		t.Fatalf("SyncIn: %v", err)
	}
	if got := k.ConfiguredBranch(); got != "" {
		t.Fatalf("a setting committed on another branch took effect: ConfiguredBranch = %q", got)
	}
	if err := k.WriteFileAtomic("data/a.md", []byte("a\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := k.CommitOp("write a"); err != nil {
		t.Fatalf("CommitOp: %v", err)
	}
	if err := k.SyncOut(); err != nil {
		t.Fatalf("SyncOut: %v", err)
	}
	if got := gitHere(t, bare, "rev-parse", "main"); got != gitHere(t, k.Root, "rev-parse", "HEAD") {
		t.Fatal("the write did not land on the default branch")
	}
	if heads := remoteHeads(t, bare); strings.Contains(heads, "elsewhere") {
		t.Fatalf("a branch named by a non-default settings.yaml was created: %q", heads)
	}

	// The same setting, once it reaches the default branch, is the one that counts.
	commitBranchSetting(t, bare, "main", "elsewhere")
	_, err := k.SyncIn()
	if !errors.Is(err, ErrBranchDiverged) {
		t.Fatalf("SyncIn err = %v, want ErrBranchDiverged: the KB sits on main and the default branch now says elsewhere", err)
	}
	if got := k.ConfiguredBranch(); got != "elsewhere" {
		t.Fatalf("ConfiguredBranch = %q, want elsewhere", got)
	}
}

// D335, D903: a clone on another branch is refused, the message names the
// configured branch and the setting, and nothing reaches the remote.
func TestConfiguredBranch_OtherBranchRefused(t *testing.T) {
	t.Parallel()
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k, bare := remoteWithDefault(t)
	commitBranchSetting(t, bare, "main", "kb-data")

	_, err := k.SyncIn()
	if !errors.Is(err, ErrBranchDiverged) {
		t.Fatalf("SyncIn err = %v, want ErrBranchDiverged", err)
	}
	for _, want := range []string{`"main"`, `"kb-data"`, "`branch`", "settings.yaml", k.Root} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not name %s", err, want)
		}
	}
	if strings.Contains(err.Error(), "Merge") {
		t.Fatalf("error %q suggests a merge on the remote", err)
	}
	if err := k.WriteFileAtomic("data/b.md", []byte("b\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := k.CommitOp("write b"); err != nil {
		t.Fatalf("CommitOp: %v", err)
	}
	if err := k.SyncOut(); !errors.Is(err, ErrBranchDiverged) {
		t.Fatalf("SyncOut err = %v, want ErrBranchDiverged", err)
	}
	if heads := remoteHeads(t, bare); heads != "refs/heads/main" {
		t.Fatalf("remote heads = %q, want main only", heads)
	}
	if s := k.GitStatusSnapshot(); s.State != "degraded" {
		t.Fatalf("status = %+v", s)
	}
}

// Without the key the status says the branch comes from the remote (D264).
func TestConfiguredBranch_UnsetReportsRemoteSource(t *testing.T) {
	t.Parallel()
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k, _ := remoteWithDefault(t)
	if _, err := k.SyncIn(); err != nil {
		t.Fatalf("SyncIn: %v", err)
	}
	if s := k.GitStatusSnapshot(); s.BranchSource != "remote" || s.RemoteDefaultBranch != "main" {
		t.Fatalf("status = %+v", s)
	}
}

// A branch name git would refuse is a finding in the file, never a branch.
func TestConfiguredBranch_InvalidNameIsIgnored(t *testing.T) {
	t.Parallel()
	for _, v := range []string{"has space", "-lead", "a..b", "x.lock", "HEAD"} {
		_, bad, err := ParseSettings([]byte("branch: \"" + v + "\"\n"))
		if err != nil || len(bad) != 1 || bad[0].Key != "branch" {
			t.Errorf("branch %q: bad = %v, err = %v, want one finding on branch", v, bad, err)
		}
	}
	if _, bad, _ := ParseSettings([]byte("branch: kb-data\n")); len(bad) != 0 {
		t.Errorf("branch kb-data was refused: %v", bad)
	}
}

// A `branch` committed on the default branch that is no valid name must not be
// read as "no setting": that would send writes straight to the default branch.
func TestConfiguredBranch_InvalidNameOnTheRemoteRefusesTheSync(t *testing.T) {
	t.Parallel()
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k, bare := remoteWithDefault(t)
	commitBranchSetting(t, bare, "main", `"has space"`)
	if _, err := k.SyncIn(); err == nil || !strings.Contains(err.Error(), "branch") {
		t.Fatalf("SyncIn error = %v, want a refusal naming `branch`", err)
	}
	if got := k.ConfiguredBranch(); got != "" {
		t.Fatalf("ConfiguredBranch = %q, want empty", got)
	}
}
