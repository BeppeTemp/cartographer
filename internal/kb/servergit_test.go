package kb

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/gitx"
)

type fakeForge struct{}

func (fakeForge) FindOpenPR(context.Context, string, string, string, string) ([]PullRequest, error) {
	return nil, nil
}

type scriptedForge struct {
	find   func() ([]PullRequest, error)
	create func() (PullRequest, error)
	get    func() (PullRequest, error)
	ready  func() (bool, error)
	merge  func() (MergeResult, error)
}

func (f scriptedForge) FindOpenPR(context.Context, string, string, string, string) ([]PullRequest, error) {
	return f.find()
}
func (f scriptedForge) CreatePR(context.Context, string, string, string, string, string, string) (PullRequest, error) {
	return f.create()
}
func (f scriptedForge) GetPR(context.Context, string, string, int) (PullRequest, error) {
	return f.get()
}
func (f scriptedForge) PRReady(context.Context, string, string, int) (bool, error) { return f.ready() }
func (f scriptedForge) MergeSquash(context.Context, string, string, int, string) (MergeResult, error) {
	return f.merge()
}
func (fakeForge) CreatePR(context.Context, string, string, string, string, string, string) (PullRequest, error) {
	return PullRequest{Number: 1, URL: "http://forge/pr/1", State: "open"}, nil
}
func (fakeForge) GetPR(context.Context, string, string, int) (PullRequest, error) {
	return PullRequest{Number: 1, State: "open"}, nil
}
func (fakeForge) PRReady(context.Context, string, string, int) (bool, error) { return true, nil }
func (fakeForge) MergeSquash(context.Context, string, string, int, string) (MergeResult, error) {
	return MergeResult{Merged: true, SHA: "merged"}, nil
}

func serverGitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

func branchForTest(dir string) (string, error) {
	out, err := exec.Command("git", "-C", dir, "branch", "--show-current").Output()
	return strings.TrimSpace(string(out)), err
}

// serverGitRemote makes transport identity look like GitHub while keeping all
// fixture traffic local. fetchurl is deliberately separate from the push URL.
func serverGitRemote(t *testing.T, k *KB, bare string) {
	t.Helper()
	serverGitRun(t, k.Root, "branch", "-M", "main")
	serverGitRun(t, k.Root, "push", bare, "main:main")
	serverGitRun(t, bare, "update-server-info")
	// Dumb HTTPS serves a real local Git repository under the exact owner/repo
	// path. This exercises the production HTTPS identity check without network.
	server := httptest.NewTLSServer(http.FileServer(http.Dir(filepath.Dir(filepath.Dir(bare)))))
	t.Cleanup(server.Close)
	serverGitRun(t, k.Root, "remote", "add", "origin", server.URL+"/example/wiki.git")
	serverGitRun(t, k.Root, "remote", "set-url", "--push", "origin", bare)
	k.GitEnv = append(k.GitEnv, "GIT_SSL_NO_VERIFY=true")
}

func TestConfigureServerGitCreatesDedicatedBranch(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not in PATH")
	}
	root := t.TempDir()
	k, err := Init(filepath.Join(root, "kb"))
	if err != nil {
		t.Fatal(err)
	}
	bare := filepath.Join(root, "remote", "example", "wiki.git")
	if err := os.MkdirAll(filepath.Dir(bare), 0o755); err != nil {
		t.Fatal(err)
	}
	serverGitRun(t, filepath.Dir(bare), "init", "--bare", bare)
	serverGitRemote(t, k, bare)
	if err := k.ConfigureServerGit(ServerGitConfig{BaseBranch: "main", WorkingBranch: "cartographer/kb", Owner: "example", Repository: "wiki", Forge: fakeForge{}}); err != nil {
		t.Fatalf("ConfigureServerGit: %v", err)
	}
	if branch, _ := branchForTest(k.Root); branch != "cartographer/kb" {
		t.Fatalf("branch = %q, want cartographer/kb", branch)
	}
	state := k.ServerGitStatus()
	if state.Profile != "server" || state.BaseBranch != "main" || state.WorkingBranch != "cartographer/kb" {
		t.Fatalf("state = %#v", state)
	}
}

func TestConfigureServerGitRefusesDirtyState(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not in PATH")
	}
	root := t.TempDir()
	k, err := Init(filepath.Join(root, "kb"))
	if err != nil {
		t.Fatal(err)
	}
	bare := filepath.Join(root, "remote", "example", "wiki.git")
	if err := os.MkdirAll(filepath.Dir(bare), 0o755); err != nil {
		t.Fatal(err)
	}
	serverGitRun(t, filepath.Dir(bare), "init", "--bare", bare)
	serverGitRemote(t, k, bare)
	if err := k.WriteFileAtomic("data/dirty.md", []byte("dirty")); err != nil {
		t.Fatal(err)
	}
	if err := k.ConfigureServerGit(ServerGitConfig{BaseBranch: "main", WorkingBranch: "cartographer/kb", Owner: "example", Repository: "wiki", Forge: fakeForge{}}); err == nil {
		t.Fatal("expected dirty startup refusal")
	}
}

func TestGitHubRemoteMatches(t *testing.T) {
	for _, tc := range []struct {
		name, remote string
		want         bool
	}{
		{"https", "https://github.com/acme/wiki.git", true},
		{"https slash", "https://github.com/acme/wiki/", true},
		{"https credentials", "https://token@github.com/acme/wiki.git", true},
		{"ssh scp", "git@github.com:acme/wiki.git", true},
		{"ssh url", "ssh://git@github.com/acme/wiki.git", true},
		{"mismatch", "https://github.com/acme/other.git", false},
		{"not github transport", "file:///tmp/acme/wiki.git", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := githubRemoteMatches(tc.remote, "acme", "wiki"); got != tc.want {
				t.Fatalf("githubRemoteMatches(%q) = %v, want %v", tc.remote, got, tc.want)
			}
		})
	}
}

func TestServerPRLifecyclePersistsOnlyNonSecretState(t *testing.T) {
	root := t.TempDir()
	created := 0
	f := &scriptedForge{
		find: func() ([]PullRequest, error) { return nil, nil },
		create: func() (PullRequest, error) {
			created++
			return PullRequest{Number: 12, URL: "https://forge/pr/12", HeadSHA: "head", State: "open"}, nil
		},
		get:   func() (PullRequest, error) { return PullRequest{}, errors.New("unused") },
		ready: func() (bool, error) { return false, errors.New("unused") },
		merge: func() (MergeResult, error) { return MergeResult{}, errors.New("unused") },
	}
	k := &KB{Root: root, ServerGit: &ServerGitConfig{BaseBranch: "main", WorkingBranch: "cartographer/kb", Owner: "acme", Repository: "wiki", Forge: f}}
	if err := k.ReconcileServerPR(); err != nil {
		t.Fatal(err)
	}
	if created != 1 {
		t.Fatalf("CreatePR calls = %d, want 1", created)
	}
	state := k.ServerGitStatus()
	if state.PRNumber != 12 || state.PRHeadSHA != "head" || state.Phase != "open" {
		t.Fatalf("state = %#v", state)
	}
	data, err := os.ReadFile(k.serverGitStatePath())
	if err != nil || strings.Contains(string(data), "secret") {
		t.Fatalf("persisted state leaks secret or unreadable: %q, %v", data, err)
	}
	// A restart reloads the persisted PR and an existing open PR is reused.
	f.find = func() ([]PullRequest, error) {
		return []PullRequest{{Number: 12, URL: "https://forge/pr/12", HeadSHA: "next", State: "open"}}, nil
	}
	if err := k.ReconcileServerPR(); err != nil {
		t.Fatal(err)
	}
	if created != 1 || k.ServerGitStatus().PRHeadSHA != "next" {
		t.Fatalf("existing PR was not reused: created=%d state=%#v", created, k.ServerGitStatus())
	}
}

func TestServerPRLifecycleFailsClosedAndRetriesOnStatus(t *testing.T) {
	root := t.TempDir()
	boom := errors.New("forge unavailable")
	f := &scriptedForge{
		find:   func() ([]PullRequest, error) { return nil, boom },
		create: func() (PullRequest, error) { return PullRequest{}, boom },
		get:    func() (PullRequest, error) { return PullRequest{}, boom },
		ready:  func() (bool, error) { return false, boom },
		merge:  func() (MergeResult, error) { return MergeResult{}, boom },
	}
	k := &KB{Root: root, ServerGit: &ServerGitConfig{BaseBranch: "main", WorkingBranch: "work", Owner: "a", Repository: "b", Forge: f}}
	if err := k.ReconcileServerPR(); !errors.Is(err, boom) || k.ServerGitStatus().LastForgeError == "" {
		t.Fatalf("forge failure was not persisted: %v / %#v", err, k.ServerGitStatus())
	}
	f.find = func() ([]PullRequest, error) { return []PullRequest{{Number: 1, HeadSHA: "h", State: "open"}}, nil }
	if err := k.ReconcileServerPR(); err != nil || k.ServerGitStatus().LastForgeError != "" {
		t.Fatalf("status retry did not recover: %v / %#v", err, k.ServerGitStatus())
	}
	f.find = func() ([]PullRequest, error) { return []PullRequest{{Number: 1}, {Number: 2}}, nil }
	if err := k.ReconcileServerPR(); err == nil {
		t.Fatal("expected exact one-open-PR invariant failure")
	}
}

func TestServerWritesBlockedDuringMergeUncertainty(t *testing.T) {
	k := &KB{Root: t.TempDir(), ServerGit: &ServerGitConfig{BaseBranch: "main", WorkingBranch: "work", Owner: "a", Repository: "b", Forge: fakeForge{}}}
	if err := k.saveServerGitState(ServerGitState{Profile: "server", Phase: "merge_uncertain", PRNumber: 1}); err != nil {
		t.Fatal(err)
	}
	if err := k.ServerWritesBlocked(); err == nil {
		t.Fatal("expected merge_uncertain to block writes")
	}
	if _, err := k.SyncIn(); err == nil {
		t.Fatal("SyncIn must block writes before touching refs during uncertainty")
	}
}

func TestMergeUncertaintyBlocksPendingServerPush(t *testing.T) {
	k, _, _ := serverGitFinalizeFixture(t)
	k.GitSync = true
	if err := k.saveServerGitState(ServerGitState{Profile: "server", BaseBranch: "main", WorkingBranch: "cartographer/kb", Phase: "merge_uncertain", PRNumber: 1}); err != nil {
		t.Fatal(err)
	}
	if err := k.SyncOut(); err == nil {
		t.Fatal("pending server push must be blocked during merge uncertainty")
	}
}

func TestRecordServerRebaseConflictRetainsNonConceptPaths(t *testing.T) {
	k := &KB{Root: t.TempDir(), ServerGit: &ServerGitConfig{BaseBranch: "main", WorkingBranch: "work", Owner: "a", Repository: "b", Forge: fakeForge{}}}
	k.RecordServerRebaseConflict(&gitx.RebaseConflictError{Files: []string{"README.md", "data/note.md"}, LocalSHA: "local", RemoteSHA: "remote", Remote: "origin", Branch: "main"})
	conflicts, err := k.ListConflicts()
	if err != nil || len(conflicts) != 2 {
		t.Fatalf("conflicts = %#v, %v", conflicts, err)
	}
	seen := map[string]bool{}
	for _, c := range conflicts {
		seen[c.ConceptID] = true
	}
	if !seen["git-path:README.md"] || !seen["note"] {
		t.Fatalf("non-concept conflict was not retained: %#v", conflicts)
	}
}

// serverGitInitBare creates the fake remote and makes it advertise the branch
// the KB is actually on. Without the symbolic-ref a clone on a host configured
// for init.defaultBranch=master checks out nothing, and every fixture built on
// top of it sees an empty worktree.
func serverGitInitBare(t *testing.T, bare string) {
	t.Helper()
	serverGitRun(t, filepath.Dir(bare), "init", "--bare", bare)
	serverGitRun(t, bare, "symbolic-ref", "HEAD", "refs/heads/"+gitx.DefaultBranch)
}

func serverGitFinalizeFixture(t *testing.T) (*KB, string, string) {
	t.Helper()
	root := t.TempDir()
	k, err := Init(filepath.Join(root, "kb"))
	if err != nil {
		t.Fatal(err)
	}
	bare := filepath.Join(root, "remote", "example", "wiki.git")
	if err := os.MkdirAll(filepath.Dir(bare), 0o755); err != nil {
		t.Fatal(err)
	}
	serverGitInitBare(t, bare)
	serverGitRemote(t, k, bare)
	f := fakeForge{}
	if err := k.ConfigureServerGit(ServerGitConfig{BaseBranch: "main", WorkingBranch: "cartographer/kb", Owner: "example", Repository: "wiki", Forge: f}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(k.Root, "data", "change.md"), []byte("---\ntype: Note\ntitle: Change\n---\nchange\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	serverGitRun(t, k.Root, "add", "data/change.md")
	serverGitRun(t, k.Root, "commit", "-m", "working change")
	serverGitRun(t, k.Root, "push", "origin", "cartographer/kb")
	serverGitRun(t, bare, "update-server-info")
	if err := gitx.Fetch(k.Root, "origin", k.GitEnv...); err != nil {
		t.Fatal(err)
	}
	head, err := gitx.HeadSHA(k.Root)
	if err != nil {
		t.Fatal(err)
	}
	return k, bare, head
}

func TestFinalizeServerPRRequiresApprovalWithoutChangingBase(t *testing.T) {
	k, _, head := serverGitFinalizeFixture(t)
	baseBefore, err := gitx.HeadSHAAt(k.Root, "origin/main")
	if err != nil {
		t.Fatal(err)
	}
	f := &scriptedForge{
		find:   func() ([]PullRequest, error) { return nil, fmt.Errorf("unused") },
		create: func() (PullRequest, error) { return PullRequest{}, fmt.Errorf("unused") },
		get:    func() (PullRequest, error) { return PullRequest{Number: 4, State: "open", HeadSHA: head}, nil },
		ready:  func() (bool, error) { return false, nil },
		merge:  func() (MergeResult, error) { return MergeResult{}, fmt.Errorf("must not merge") },
	}
	k.ServerGit.Forge = f
	if err := k.saveServerGitState(ServerGitState{Profile: "server", BaseBranch: "main", WorkingBranch: "cartographer/kb", PRNumber: 4, PRHeadSHA: head, Phase: "open"}); err != nil {
		t.Fatal(err)
	}
	if err := k.FinalizeServerPR(context.Background(), head); err == nil {
		t.Fatal("expected approval/check gate failure")
	}
	baseAfter, _ := gitx.HeadSHAAt(k.Root, "origin/main")
	if baseAfter != baseBefore {
		t.Fatalf("protected base changed without merge: %s -> %s", baseBefore, baseAfter)
	}
}

func TestFinalizeServerPRSuccessfulSquashStartsNewCycle(t *testing.T) {
	k, bare, head := serverGitFinalizeFixture(t)
	merged := false
	f := &scriptedForge{
		find:   func() ([]PullRequest, error) { return nil, fmt.Errorf("unused") },
		create: func() (PullRequest, error) { return PullRequest{}, fmt.Errorf("unused") },
		get: func() (PullRequest, error) {
			if merged {
				return PullRequest{Number: 5, State: "closed", Merged: true, MergeSHA: head}, nil
			}
			return PullRequest{Number: 5, State: "open", HeadSHA: head}, nil
		},
		ready: func() (bool, error) { return true, nil },
		merge: func() (MergeResult, error) {
			mergeHead, err := gitx.HeadSHA(k.Root)
			if err != nil {
				return MergeResult{}, err
			}
			// This is the explicit fake-forge merge: production code never pushes
			// the protected base itself.
			serverGitRun(t, k.Root, "push", bare, "HEAD:main")
			serverGitRun(t, bare, "update-server-info")
			merged = true
			return MergeResult{Merged: true, SHA: mergeHead}, nil
		},
	}
	k.ServerGit.Forge = f
	if err := k.saveServerGitState(ServerGitState{Profile: "server", BaseBranch: "main", WorkingBranch: "cartographer/kb", PRNumber: 5, PRHeadSHA: head, Phase: "open"}); err != nil {
		t.Fatal(err)
	}
	if err := k.FinalizeServerPR(context.Background(), head); err != nil {
		t.Fatal(err)
	}
	state := k.ServerGitStatus()
	if state.PRNumber != 0 || state.Phase != "" {
		t.Fatalf("new cycle state = %#v", state)
	}
	base, _ := gitx.HeadSHAAt(k.Root, "origin/main")
	work, _ := gitx.HeadSHA(k.Root)
	if base != work {
		t.Fatalf("working branch was not reset to merged base: work=%s base=%s", work, base)
	}
}
