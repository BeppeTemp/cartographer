package kb

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/gitx"
)

func finalState(t *testing.T, k *KB, number int, head string) {
	t.Helper()
	if err := k.saveServerGitState(ServerGitState{Profile: "server", BaseBranch: "main", WorkingBranch: "cartographer/kb", PRNumber: number, PRHeadSHA: head, Phase: "open"}); err != nil {
		t.Fatal(err)
	}
}

func readyForge(head string, ready bool) *scriptedForge {
	return &scriptedForge{
		find:   func() ([]PullRequest, error) { return nil, errors.New("unused") },
		create: func() (PullRequest, error) { return PullRequest{}, errors.New("unused") },
		get:    func() (PullRequest, error) { return PullRequest{Number: 9, State: "open", HeadSHA: head}, nil },
		ready:  func() (bool, error) { return ready, nil },
		merge:  func() (MergeResult, error) { return MergeResult{}, errors.New("must not merge") },
	}
}

func TestFinalizeServerPRRejectsStaleCallerAndForgeHead(t *testing.T) {
	k, _, head := serverGitFinalizeFixture(t)
	base, _ := gitx.HeadSHAAt(k.Root, "origin/main")
	f := readyForge(head, true)
	k.ServerGit.Forge = f
	finalState(t, k, 9, head)
	if err := k.FinalizeServerPR(context.Background(), "stale"); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("stale caller error = %v", err)
	}
	f.get = func() (PullRequest, error) { return PullRequest{Number: 9, State: "open", HeadSHA: "new-head"}, nil }
	if err := k.FinalizeServerPR(context.Background(), head); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("stale forge head error = %v", err)
	}
	if got, _ := gitx.HeadSHAAt(k.Root, "origin/main"); got != base {
		t.Fatalf("base changed on stale finalization: %s -> %s", base, got)
	}
}

func TestFinalizeServerPRValidationLintAndGateFailuresDoNotPush(t *testing.T) {
	for _, tc := range []struct {
		name    string
		want    string
		prepare func(t *testing.T, k *KB)
	}{
		{"validation", "validation failed", func(t *testing.T, k *KB) {
			if err := os.WriteFile(filepath.Join(k.Root, "data", "invalid.md"), []byte("not markdown frontmatter\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{"lint", "lint failed", func(t *testing.T, k *KB) { k.ServerMergeLint = func() error { return errors.New("lint failed") } }},
		{"commit gate", "commit gate", func(t *testing.T, k *KB) {
			if err := os.WriteFile(filepath.Join(k.Root, "data", "blocker.md"), []byte("---\ntype: Contradiction\nresolution_status: open\ninvolves: [change]\n---\nblocked\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k, _, head := serverGitFinalizeFixture(t)
			before, _ := gitx.HeadSHAAt(k.Root, "origin/cartographer/kb")
			f := readyForge(head, true)
			k.ServerGit.Forge = f
			finalState(t, k, 9, head)
			tc.prepare(t, k)
			if err := k.FinalizeServerPR(context.Background(), head); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected %q failure, got %v", tc.want, err)
			}
			after, _ := gitx.HeadSHAAt(k.Root, "origin/cartographer/kb")
			if after != before {
				t.Fatalf("working remote changed on %s failure: %s -> %s", tc.name, before, after)
			}
		})
	}
}

func TestFinalizeServerPRLeaseFailureDoesNotCallForgeMerge(t *testing.T) {
	k, bare, head := serverGitFinalizeFixture(t)
	mergeCalls := 0
	f := readyForge(head, true)
	f.merge = func() (MergeResult, error) { mergeCalls++; return MergeResult{}, nil }
	k.ServerGit.Forge = f
	finalState(t, k, 9, head)
	// The injected lint runs after fetch/head verification and replaces the
	// remote working ref, exercising the actual force-with-lease failure.
	k.ServerMergeLint = func() error {
		other := filepath.Join(t.TempDir(), "other")
		serverGitRun(t, filepath.Dir(other), "clone", bare, other)
		serverGitRun(t, other, "checkout", "cartographer/kb")
		if err := os.WriteFile(filepath.Join(other, "lease.txt"), []byte("race\n"), 0o644); err != nil {
			return err
		}
		serverGitRun(t, other, "add", "lease.txt")
		serverGitRun(t, other, "-c", "user.name=test", "-c", "user.email=test@example.test", "commit", "-m", "lease race")
		serverGitRun(t, other, "push", "origin", "cartographer/kb")
		serverGitRun(t, bare, "update-server-info")
		return nil
	}
	if err := k.FinalizeServerPR(context.Background(), head); err == nil || !strings.Contains(err.Error(), "force-with-lease") {
		t.Fatalf("lease failure = %v", err)
	}
	if mergeCalls != 0 {
		t.Fatalf("MergeSquash called after lease failure: %d", mergeCalls)
	}
}

func TestFinalizeServerPRRejectsRemoteHeadRaceBeforeRebase(t *testing.T) {
	k, bare, head := serverGitFinalizeFixture(t)
	f := readyForge(head, true)
	f.ready = func() (bool, error) {
		other := filepath.Join(t.TempDir(), "other")
		serverGitRun(t, filepath.Dir(other), "clone", bare, other)
		serverGitRun(t, other, "checkout", "cartographer/kb")
		if err := os.WriteFile(filepath.Join(other, "head-race.txt"), []byte("race\n"), 0o644); err != nil {
			return false, err
		}
		serverGitRun(t, other, "add", "head-race.txt")
		serverGitRun(t, other, "-c", "user.name=test", "-c", "user.email=test@example.test", "commit", "-m", "head race")
		serverGitRun(t, other, "push", "origin", "cartographer/kb")
		serverGitRun(t, bare, "update-server-info")
		return true, nil
	}
	k.ServerGit.Forge = f
	finalState(t, k, 9, head)
	if err := k.FinalizeServerPR(context.Background(), head); err == nil || !strings.Contains(err.Error(), "stale PR head after fetch") {
		t.Fatalf("remote head race = %v", err)
	}
}

func TestFinalizeServerPRRejectsBaseRaceBeforeMerge(t *testing.T) {
	k, bare, head := serverGitFinalizeFixture(t)
	base, err := gitx.HeadSHAAt(k.Root, "origin/main")
	if err != nil {
		t.Fatal(err)
	}
	var gets, mergeCalls int
	advancedBase := ""
	f := readyForge(head, true)
	f.get = func() (PullRequest, error) {
		gets++
		currentBase := base
		if gets > 1 {
			currentBase = advancedBase
		}
		return PullRequest{Number: 9, State: "open", HeadSHA: head, BaseSHA: currentBase}, nil
	}
	f.merge = func() (MergeResult, error) { mergeCalls++; return MergeResult{}, nil }
	k.ServerGit.Forge = f
	finalState(t, k, 9, head)
	k.ServerMergeLint = func() error {
		other := filepath.Join(t.TempDir(), "other")
		serverGitRun(t, filepath.Dir(other), "clone", bare, other)
		if err := os.WriteFile(filepath.Join(other, "data", "base-race.md"), []byte("---\ntype: Note\n---\nrace\n"), 0o644); err != nil {
			return err
		}
		serverGitRun(t, other, "add", "data/base-race.md")
		serverGitRun(t, other, "-c", "user.name=test", "-c", "user.email=test@example.test", "commit", "-m", "base race")
		serverGitRun(t, other, "push", "origin", "main")
		serverGitRun(t, bare, "update-server-info")
		var err error
		advancedBase, err = gitx.HeadSHA(k.Root) // overwritten below from the other clone ref
		if err != nil {
			return err
		}
		advancedBase, err = gitx.HeadSHAAt(other, "HEAD")
		return err
	}
	if err := k.FinalizeServerPR(context.Background(), head); err == nil || !strings.Contains(err.Error(), "stale PR base before merge") {
		t.Fatalf("base race error = %v", err)
	}
	if mergeCalls != 0 {
		t.Fatalf("MergeSquash called after base race: %d", mergeCalls)
	}
}

func TestMergeUncertainRestartReconcilesOnlyConfirmedMerge(t *testing.T) {
	k, bare, head := serverGitFinalizeFixture(t)
	merged := false
	f := readyForge(head, true)
	f.get = func() (PullRequest, error) {
		if merged {
			return PullRequest{Number: 9, State: "closed", Merged: true, MergeSHA: head}, nil
		}
		return PullRequest{Number: 9, State: "open", HeadSHA: head}, nil
	}
	f.merge = func() (MergeResult, error) {
		serverGitRun(t, k.Root, "push", bare, "HEAD:main") // fake forge did merge
		serverGitRun(t, bare, "update-server-info")
		merged = true
		return MergeResult{}, errors.New("timeout after merge")
	}
	k.ServerGit.Forge = f
	finalState(t, k, 9, head)
	if err := k.FinalizeServerPR(context.Background(), head); err == nil {
		t.Fatal("expected uncertain result after fake timeout")
	}
	if k.ServerGitStatus().Phase != "merge_uncertain" {
		t.Fatalf("state = %#v", k.ServerGitStatus())
	}
	// New object simulates restart; recovery consumes only persisted non-secret
	// data and the forge-confirmed base result.
	restarted, err := Open(k.Root)
	if err != nil {
		t.Fatal(err)
	}
	restarted.GitEnv = append(restarted.GitEnv, "GIT_SSL_NO_VERIFY=true")
	restarted.ServerGit = &ServerGitConfig{BaseBranch: "main", WorkingBranch: "cartographer/kb", Owner: "example", Repository: "wiki", Forge: f}
	if err := restarted.ReconcileServerPR(); err != nil {
		t.Fatal(err)
	}
	if state := restarted.ServerGitStatus(); state.Phase != "" || state.PRNumber != 0 {
		t.Fatalf("recovery did not start new cycle: %#v", state)
	}
}

func TestMergeUncertainOpenUnmergedAndUnknownRemainBlocked(t *testing.T) {
	for _, tc := range []struct {
		name string
		pr   PullRequest
		err  error
	}{
		{"open", PullRequest{Number: 9, State: "open"}, nil},
		{"unmerged", PullRequest{Number: 9, State: "closed"}, nil},
		{"unknown", PullRequest{}, errors.New("forge unavailable")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k := &KB{Root: t.TempDir()}
			f := &scriptedForge{
				find: func() ([]PullRequest, error) { return nil, fmt.Errorf("unused") }, create: func() (PullRequest, error) { return PullRequest{}, fmt.Errorf("unused") },
				get: func() (PullRequest, error) { return tc.pr, tc.err }, ready: func() (bool, error) { return false, nil }, merge: func() (MergeResult, error) { return MergeResult{}, nil },
			}
			k.ServerGit = &ServerGitConfig{BaseBranch: "main", WorkingBranch: "work", Owner: "a", Repository: "b", Forge: f}
			if err := k.saveServerGitState(ServerGitState{Profile: "server", BaseBranch: "main", WorkingBranch: "work", PRNumber: 9, Phase: "merge_uncertain"}); err != nil {
				t.Fatal(err)
			}
			if err := k.ReconcileServerPR(); err == nil {
				t.Fatal("expected reconciliation to remain blocked")
			}
			if err := k.ServerWritesBlocked(); err == nil {
				t.Fatal("writes became unblocked")
			}
		})
	}
}

func registerResolvedServerConflict(t *testing.T, k *KB) {
	t.Helper()
	head, err := gitx.HeadSHA(k.Root)
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(k.Root, "data", "change.md"))
	if err != nil {
		t.Fatal(err)
	}
	if err := k.RegisterConflict(Conflict{ConceptID: "change", Path: "data/change.md", LocalSHA: head, RemoteSHA: head, Branch: "main", BaseBranch: "main", WorkingBranch: "cartographer/kb", ResolutionStrategy: "edit", ResolutionBody: string(content)}); err != nil {
		t.Fatal(err)
	}
}

func TestFinalizeServerConflictsKeepsRegistryOnRebasePushAndForgeFailure(t *testing.T) {
	t.Run("rebase", func(t *testing.T) {
		k, bare, _ := serverGitFinalizeFixture(t)
		registerResolvedServerConflict(t, k)
		other := filepath.Join(t.TempDir(), "other")
		serverGitRun(t, filepath.Dir(other), "clone", bare, other)
		if err := os.WriteFile(filepath.Join(other, "data", "change.md"), []byte("---\ntype: Note\n---\nbase race\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		serverGitRun(t, other, "add", "data/change.md")
		serverGitRun(t, other, "-c", "user.name=test", "-c", "user.email=test@example.test", "commit", "-m", "base race")
		serverGitRun(t, other, "push", "origin", "main")
		serverGitRun(t, bare, "update-server-info")
		if _, err := k.finalizeServerConflicts(); err == nil {
			t.Fatal("expected rebase failure")
		}
		if cs, _ := k.ListConflicts(); len(cs) == 0 {
			t.Fatal("registry cleared after rebase failure")
		}
	})
	t.Run("push", func(t *testing.T) {
		k, _, _ := serverGitFinalizeFixture(t)
		registerResolvedServerConflict(t, k)
		k.GitSync = true
		serverGitRun(t, k.Root, "remote", "set-url", "--push", "origin", filepath.Join(t.TempDir(), "missing.git"))
		if _, err := k.finalizeServerConflicts(); err == nil {
			t.Fatal("expected push failure")
		}
		if cs, _ := k.ListConflicts(); len(cs) == 0 {
			t.Fatal("registry cleared after push failure")
		}
	})
	t.Run("forge", func(t *testing.T) {
		k, _, _ := serverGitFinalizeFixture(t)
		registerResolvedServerConflict(t, k)
		k.GitSync = true
		boom := errors.New("forge down")
		k.ServerGit.Forge = &scriptedForge{
			find: func() ([]PullRequest, error) { return nil, boom }, create: func() (PullRequest, error) { return PullRequest{}, boom },
			get: func() (PullRequest, error) { return PullRequest{}, boom }, ready: func() (bool, error) { return false, boom }, merge: func() (MergeResult, error) { return MergeResult{}, boom },
		}
		if _, err := k.finalizeServerConflicts(); err == nil {
			t.Fatal("expected forge failure")
		}
		if cs, _ := k.ListConflicts(); len(cs) == 0 {
			t.Fatal("registry cleared after forge failure")
		}
	})
}

func TestFinalizeServerConflictsResolvesSyntheticUnrelatedPathID(t *testing.T) {
	k, _, _ := serverGitFinalizeFixture(t)
	head, err := gitx.HeadSHA(k.Root)
	if err != nil {
		t.Fatal(err)
	}
	const id = "git-path:README.md"
	if err := k.RegisterConflict(Conflict{ConceptID: id, Path: "README.md", LocalSHA: head, RemoteSHA: head, Branch: "main", BaseBranch: "main", WorkingBranch: "cartographer/kb"}); err != nil {
		t.Fatal(err)
	}
	// This is the same agent-facing API used for a concept conflict: the
	// synthetic ID makes unrelated paths explicit rather than silently skipped.
	if err := k.RecordResolution(id, "edit", "resolved by operator\n"); err != nil {
		t.Fatal(err)
	}
	ids, err := k.finalizeServerConflicts()
	if err != nil || len(ids) != 1 || ids[0] != id {
		t.Fatalf("finalize ids=%#v err=%v", ids, err)
	}
	if data, err := os.ReadFile(filepath.Join(k.Root, "README.md")); err != nil || string(data) != "resolved by operator\n" {
		t.Fatalf("resolved unrelated path = %q, %v", data, err)
	}
	if cs, err := k.ListConflicts(); err != nil || len(cs) != 0 {
		t.Fatalf("registry = %#v, %v", cs, err)
	}
}
