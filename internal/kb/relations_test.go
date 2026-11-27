package kb

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

const relSettings = "depends_on:\n  - {name: core-kb, remote: git.example.com/org/core}\n"

func commitSettingsHere(t *testing.T, k *KB, text string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(k.Root, SettingsFile), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	gitHere(t, k.Root, "add", SettingsFile)
	gitHere(t, k.Root, "-c", "user.name=user", "-c", "user.email=user@example.com", "commit", "-q", "-m", "deps")
}

// AC-kbrel-3
func TestRelationsAtDefaultBranchOnly(t *testing.T) {
	t.Parallel()
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k, bare := remoteWithDefault(t)
	commitRawSettings(t, bare, "main", relSettings)
	commitRawSettings(t, bare, "work", "depends_on:\n  - {name: other-kb, remote: git.example.com/org/other}\n")
	if _, err := k.SyncIn(); err != nil {
		t.Fatalf("SyncIn: %v", err)
	}
	// An uncommitted working copy value changes nothing either.
	if err := os.WriteFile(filepath.Join(k.Root, SettingsFile), []byte("depends_on:\n  - {name: local-kb, remote: git.example.com/org/local}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	want := []Relation{{Name: "core-kb", Remote: "git.example.com/org/core"}}
	got, err := k.RelationsAt("refs/remotes/origin/main")
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("RelationsAt(origin/main) = %v, %v", got, err)
	}
	got, err = k.Relations()
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("Relations = %v, %v (the default branch is origin/%s)", got, err, k.RemoteDefaultBranch())
	}
}

func TestRelationsNoRemoteReadsHEAD(t *testing.T) {
	t.Parallel()
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k := newTestKB(t)
	if got, err := k.Relations(); err != nil || len(got) != 0 {
		t.Fatalf("no settings.yaml: %v, %v", got, err)
	}
	commitSettingsHere(t, k, relSettings)
	k.forgetRelationsValidation()
	got, err := k.Relations()
	if err != nil || len(got) != 1 || got[0].Name != "core-kb" {
		t.Fatalf("Relations = %v, %v", got, err)
	}
}

// AC-kbrel-4
func TestRelationsAtGitFailureIsAnError(t *testing.T) {
	t.Parallel()
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k := newTestKB(t)
	if got, err := k.RelationsAt("no-such-ref"); err == nil {
		t.Fatalf("an unknown ref was read as none: %v", got)
	}
	if err := os.Rename(filepath.Join(k.Root, ".git"), filepath.Join(k.Root, ".git-away")); err != nil {
		t.Fatal(err)
	}
	if got, err := k.Relations(); err == nil {
		t.Fatalf("a broken repository was read as none: %v", got)
	}
}

// The tests below set the package variable relationsMaxAge, so they do not
// run in parallel.
func TestRelationsMemoFollowsTheRef(t *testing.T) {
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	old := relationsMaxAge
	relationsMaxAge = 0 // revalidate on every call
	t.Cleanup(func() { relationsMaxAge = old })
	k := newTestKB(t)
	if got, _ := k.Relations(); len(got) != 0 {
		t.Fatalf("Relations = %v", got)
	}
	commitSettingsHere(t, k, relSettings)
	if got, err := k.Relations(); err != nil || len(got) != 1 {
		t.Fatalf("after the ref moved: %v, %v", got, err)
	}
}

func TestRelationsGitReadsBounded(t *testing.T) {
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	old := relationsMaxAge
	relationsMaxAge = time.Hour
	t.Cleanup(func() { relationsMaxAge = old })
	k := newTestKB(t)
	if _, err := k.Relations(); err != nil {
		t.Fatal(err)
	}
	k.NormalizedOrigin()
	base := relationsGitReads.Load()
	for i := 0; i < 1000; i++ {
		if _, err := k.Relations(); err != nil {
			t.Fatal(err)
		}
		k.NormalizedOrigin()
	}
	if n := relationsGitReads.Load() - base; n != 0 {
		t.Fatalf("%d git reads within relationsMaxAge, want 0", n)
	}
	// A write through the server forgets the validation.
	if err := k.WithGitLock(func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := k.Relations(); err != nil {
		t.Fatal(err)
	}
	if relationsGitReads.Load() == base {
		t.Fatal("a write did not make the next read look at git")
	}
}

func TestNormalizedOrigin(t *testing.T) {
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k := newTestKB(t)
	if o, ok := k.NormalizedOrigin(); ok {
		t.Fatalf("a KB with no remote has origin %q", o)
	}
	gitHere(t, k.Root, "remote", "add", "origin", "https://user:secret@Git.Example.com/org/core.git")
	k.forgetRelationsValidation()
	if o, ok := k.NormalizedOrigin(); !ok || o != "git.example.com/org/core" {
		t.Fatalf("origin = %q, %v", o, ok)
	}
}

// The default branch is the clone's origin/HEAD, not the branch writes go to:
// a configured working branch must not widen a binding (decision 3).
func TestRelationsIgnoreAConfiguredWorkingBranch(t *testing.T) {
	t.Parallel()
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k, bare := remoteWithDefault(t)
	commitRawSettings(t, bare, "main", relSettings)
	commitRawSettings(t, bare, "work", "depends_on:\n  - {name: other-kb, remote: git.example.com/org/other}\n")
	if _, err := k.SyncIn(); err != nil {
		t.Fatalf("SyncIn: %v", err)
	}
	// What `branch: work` (or kbs[].branch) does to the branch writes go to.
	k.setCanonical("work", BranchSourceSettings)
	if got := k.RemoteDefaultBranch(); got != "work" {
		t.Fatalf("setup: RemoteDefaultBranch = %q", got)
	}
	got, err := k.Relations()
	if err != nil || len(got) != 1 || got[0].Name != "core-kb" {
		t.Fatalf("Relations = %v, %v: the working branch widened the binding", got, err)
	}
}

// A KB with a remote and no known default branch has no relations to read yet:
// an error, never HEAD, which can be a working branch.
func TestRelationsRemoteWithoutKnownDefaultIsAnError(t *testing.T) {
	t.Parallel()
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k, _ := setupKBWithRemote(t) // a remote added by hand: no origin/HEAD yet
	commitSettingsHere(t, k, relSettings)
	if got, err := k.Relations(); err == nil {
		t.Fatalf("Relations = %v with no known default branch, want an error", got)
	}
}

func TestNormalizedOriginErrIsNotNoRemote(t *testing.T) {
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	old := relationsMaxAge
	relationsMaxAge = time.Hour
	t.Cleanup(func() { relationsMaxAge = old })
	k := newTestKB(t)
	gitHere(t, k.Root, "remote", "add", "origin", "https://git.example.com/org/core.git")
	away := filepath.Join(k.Root, ".git-away")
	if err := os.Rename(filepath.Join(k.Root, ".git"), away); err != nil {
		t.Fatal(err)
	}
	if o, ok, err := k.NormalizedOriginErr(); err == nil {
		t.Fatalf("a failing git read as no remote: %q, %v", o, ok)
	}
	if o, ok := k.NormalizedOrigin(); ok || o != "" {
		t.Fatalf("NormalizedOrigin = %q, %v on a failure", o, ok)
	}
	// The failure was not kept: once git answers, so does the origin.
	if err := os.Rename(away, filepath.Join(k.Root, ".git")); err != nil {
		t.Fatal(err)
	}
	if o, ok, err := k.NormalizedOriginErr(); err != nil || !ok || o != "git.example.com/org/core" {
		t.Fatalf("after recovery: %q, %v, %v", o, ok, err)
	}
}

// A tag or a local branch named origin/<default> wins over refs/remotes/ in a
// short name: Relations must read the remote-tracking ref, not it.
func TestRelationsAmbiguousTagDoesNotWin(t *testing.T) {
	t.Parallel()
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k, bare := remoteWithDefault(t)
	commitRawSettings(t, bare, "main", relSettings)
	if _, err := k.SyncIn(); err != nil {
		t.Fatalf("SyncIn: %v", err)
	}
	// A commit with another depends_on, tagged origin/main.
	other := "depends_on:\n  - {name: other-kb, remote: git.example.com/org/other}\n"
	if err := os.WriteFile(filepath.Join(k.Root, SettingsFile), []byte(other), 0o644); err != nil {
		t.Fatal(err)
	}
	gitHere(t, k.Root, "add", SettingsFile)
	gitHere(t, k.Root, "-c", "user.name=user", "-c", "user.email=user@example.com", "commit", "-q", "-m", "other")
	gitHere(t, k.Root, "tag", "origin/main")
	got, err := k.Relations()
	if err != nil || len(got) != 1 || got[0].Name != "core-kb" {
		t.Fatalf("Relations = %v, %v: the tag origin/main was read", got, err)
	}
}

// A hand-added remote has no origin/HEAD until the first SyncIn, which fetches
// before it asks the remote for its default branch and records it.
func TestRelationsKnownAfterTheFirstSyncIn(t *testing.T) {
	t.Parallel()
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k, bare := remoteWithDefault(t)
	commitRawSettings(t, bare, "main", relSettings)
	gitHere(t, k.Root, "remote", "set-head", "origin", "--delete")
	if _, err := k.Relations(); err == nil {
		t.Fatal("origin/HEAD was deleted: Relations should not know the default branch yet")
	}
	k.forgetRelationsValidation()
	if _, err := k.SyncIn(); err != nil {
		t.Fatalf("SyncIn: %v", err)
	}
	got, err := k.Relations()
	if err != nil || len(got) != 1 || got[0].Name != "core-kb" {
		t.Fatalf("after the first SyncIn: %v, %v", got, err)
	}
}

// A remote that is a git URL but has no owner and name says so, apart from a
// text that is no git remote at all (#2446).
func TestNormalizeDeclaredRemoteErrorWording(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"one segment":  "git://host:9418/kb.git",
		"host only":    "https://example.com/kb",
		"not a remote": "",
	}
	for name, raw := range cases {
		_, err := NormalizeDeclaredRemote(raw)
		if err == nil {
			t.Errorf("%s: no error", name)
			continue
		}
		wantOwner := name != "not a remote"
		if got := strings.Contains(err.Error(), "needs an owner and a name"); got != wantOwner {
			t.Errorf("%s: %q, owner-and-name wording = %v, want %v", name, err, got, wantOwner)
		}
	}
}
