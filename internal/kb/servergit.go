package kb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BeppeTemp/cartographer/internal/gitx"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

const serverGitStateFilename = "server-git.json"

// ServerGitConfig is the resolved, non-secret configuration for the server
// profile. Token material belongs only in Forge and is never persisted.
type ServerGitConfig struct {
	BaseBranch    string
	WorkingBranch string
	Owner         string
	Repository    string
	Forge         Forge
}

// ServerGitState is local operational state, deliberately outside history.
type ServerGitState struct {
	Profile        string `json:"profile"`
	BaseBranch     string `json:"base_branch"`
	WorkingBranch  string `json:"working_branch"`
	PRNumber       int    `json:"pr_number,omitempty"`
	PRURL          string `json:"pr_url,omitempty"`
	PRHeadSHA      string `json:"pr_head_sha,omitempty"`
	PRBaseSHA      string `json:"pr_base_sha,omitempty"`
	LastForgeError string `json:"last_forge_error,omitempty"`
	Phase          string `json:"phase,omitempty"` // open | merge_uncertain
	MergeSHA       string `json:"merge_sha,omitempty"`
}

func (k *KB) serverGitStatePath() string {
	return filepath.Join(k.cartographerDirPath(), serverGitStateFilename)
}

func (k *KB) loadServerGitState() (ServerGitState, error) {
	var state ServerGitState
	data, err := os.ReadFile(k.serverGitStatePath())
	if errors.Is(err, os.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return state, fmt.Errorf("server git state: read: %w", err)
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return state, fmt.Errorf("server git state: decode: %w", err)
	}
	return state, nil
}

func (k *KB) saveServerGitState(state ServerGitState) error {
	if err := k.ensureCartographerDir(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("server git state: encode: %w", err)
	}
	return writeFileAtomic(k.serverGitStatePath(), data)
}

// ConfigureServerGit validates and mounts a dedicated working branch. It is
// intentionally fail-fast: ambiguous local state is never repaired by reset.
func (k *KB) ConfigureServerGit(cfg ServerGitConfig) error {
	if cfg.BaseBranch == "" || cfg.WorkingBranch == "" || cfg.Owner == "" || cfg.Repository == "" || cfg.Forge == nil {
		return fmt.Errorf("server git profile requires base_branch, working_branch, github owner/repository, and forge")
	}
	if cfg.BaseBranch == cfg.WorkingBranch {
		return fmt.Errorf("server git profile base_branch and working_branch must differ")
	}
	if !gitx.IsRepo(k.Root) {
		return fmt.Errorf("server git profile requires a git repository")
	}
	if status, err := gitx.Status(k.Root); err != nil {
		return err
	} else if strings.TrimSpace(status) != "" {
		return fmt.Errorf("server git profile refuses dirty startup state")
	}
	if _, ok := k.hasRemote(); !ok {
		return fmt.Errorf("server git profile requires origin")
	}
	// Keep the transport and review boundaries tied to the same repository.
	// This deliberately returns no URL: remotes may embed credentials and config
	// errors must never turn those into logs or persisted state.
	remoteURL, err := gitx.RemoteURL(k.Root, "origin")
	if err != nil || !githubRemoteMatches(remoteURL, cfg.Owner, cfg.Repository) {
		return fmt.Errorf("server git profile origin does not match configured GitHub repository")
	}
	if branch, _ := gitx.Branch(k.Root); branch == "" {
		return fmt.Errorf("server git profile refuses detached HEAD")
	}
	if err := gitx.Fetch(k.Root, "origin", k.GitEnv...); err != nil {
		return fmt.Errorf("server git profile fetch: %w", err)
	}
	if !gitx.RemoteBranchExists(k.Root, "origin", cfg.BaseBranch) {
		return fmt.Errorf("server git profile base branch origin/%s is missing", cfg.BaseBranch)
	}
	state, err := k.loadServerGitState()
	if err != nil {
		return err
	}
	if gitx.RemoteBranchExists(k.Root, "origin", cfg.WorkingBranch) {
		// Validate the published branch itself, not merely a possibly stale local
		// checkout. Otherwise an unrelated remote rewrite can be hidden by an old
		// local working branch and later be pushed/merged by mistake.
		remoteOK, remoteErr := gitx.IsAncestor(k.Root, "origin/"+cfg.BaseBranch, "origin/"+cfg.WorkingBranch)
		if (remoteErr != nil || !remoteOK) && state.Phase != "merge_uncertain" {
			return fmt.Errorf("server git profile remote working branch %q does not descend from origin/%s; operator action required", cfg.WorkingBranch, cfg.BaseBranch)
		}
		if !gitx.BranchExists(k.Root, cfg.WorkingBranch) {
			if err := gitx.CheckoutNewBranch(k.Root, cfg.WorkingBranch, "origin/"+cfg.WorkingBranch, k.GitEnv...); err != nil {
				return err
			}
		} else if err := gitx.Checkout(k.Root, cfg.WorkingBranch); err != nil {
			return err
		}
		ok, err := gitx.IsAncestor(k.Root, "origin/"+cfg.BaseBranch, "HEAD")
		if (err != nil || !ok) && state.Phase != "merge_uncertain" {
			return fmt.Errorf("server git profile working branch %q does not descend from origin/%s; operator action required", cfg.WorkingBranch, cfg.BaseBranch)
		}
	} else {
		if gitx.BranchExists(k.Root, cfg.WorkingBranch) {
			return fmt.Errorf("server git profile local working branch %q has no remote counterpart; operator action required", cfg.WorkingBranch)
		}
		if err := gitx.CheckoutNewBranch(k.Root, cfg.WorkingBranch, "origin/"+cfg.BaseBranch, k.GitEnv...); err != nil {
			return err
		}
	}
	k.ServerGit = &cfg
	state.Profile, state.BaseBranch, state.WorkingBranch = "server", cfg.BaseBranch, cfg.WorkingBranch
	if err := k.saveServerGitState(state); err != nil {
		return err
	}
	if state.Phase == "merge_uncertain" {
		ctx, cancel := serverGitContext()
		defer cancel()
		_ = k.reconcileUncertainMerge(ctx, state)
	}
	return nil
}

// githubRemoteMatches recognizes GitHub's HTTPS and SSH remote spellings.
// It intentionally accepts an enterprise hostname too: the configured GitHub
// API endpoint controls the forge, while owner/repository is the invariant
// shared by API and transport. Userinfo is ignored so a token can never affect
// matching or appear in an error.
func githubRemoteMatches(remote, owner, repository string) bool {
	remote = strings.TrimSpace(remote)
	if remote == "" || owner == "" || repository == "" {
		return false
	}
	remote = strings.TrimSuffix(strings.TrimRight(remote, "/"), ".git")
	path := ""
	switch {
	case strings.Contains(remote, "://"):
		// URL parsing is intentionally avoided here because scp-like SSH URLs
		// are not URLs; for URL forms the final two path components are enough.
		if slash := strings.Index(remote, "://"); slash >= 0 {
			scheme := remote[:slash]
			if scheme != "https" && scheme != "ssh" {
				return false
			}
			rest := remote[slash+3:]
			if i := strings.IndexByte(rest, '/'); i >= 0 {
				path = rest[i+1:]
			}
		}
	case strings.Contains(remote, ":"):
		// git@host:owner/repository (the host/user portion cannot carry a
		// repository path, so only the right hand side is considered).
		path = remote[strings.LastIndex(remote, ":")+1:]
	}
	path = strings.Trim(path, "/")
	return path == owner+"/"+repository
}

// ServerGitStatus returns the persisted PR metadata alongside the current
// profile. It is safe for local-profile KBs too.
func (k *KB) ServerGitStatus() ServerGitState {
	state, _ := k.loadServerGitState()
	if k.ServerGit != nil {
		state.Profile, state.BaseBranch, state.WorkingBranch = "server", k.ServerGit.BaseBranch, k.ServerGit.WorkingBranch
	} else if state.Profile == "" {
		state.Profile = "local"
	}
	return state
}

func (k *KB) syncServerPR(ctx context.Context) error {
	if k.ServerGit == nil {
		return nil
	}
	cfg := k.ServerGit
	state := k.ServerGitStatus()
	if state.Phase == "merge_uncertain" {
		return k.reconcileUncertainMerge(ctx, state)
	}
	prs, err := cfg.Forge.FindOpenPR(ctx, cfg.Owner, cfg.Repository, cfg.WorkingBranch, cfg.BaseBranch)
	if err != nil {
		return k.recordForgeError(err)
	}
	if len(prs) > 1 {
		return k.recordForgeError(fmt.Errorf("server git profile found %d open PRs for %s -> %s", len(prs), cfg.WorkingBranch, cfg.BaseBranch))
	}
	var pr PullRequest
	if len(prs) == 1 {
		pr = prs[0]
	} else {
		pr, err = cfg.Forge.CreatePR(ctx, cfg.Owner, cfg.Repository, cfg.WorkingBranch, cfg.BaseBranch, "Cartographer: "+filepath.Base(k.Root), "Automated KB changes from Cartographer. Review and squash-merge through this PR.")
		if err != nil {
			return k.recordForgeError(err)
		}
	}
	state.PRNumber, state.PRURL, state.PRHeadSHA, state.PRBaseSHA, state.LastForgeError = pr.Number, pr.URL, pr.HeadSHA, pr.BaseSHA, ""
	state.Phase, state.MergeSHA = "open", ""
	return k.saveServerGitState(state)
}

// ServerWritesBlocked prevents a second PR cycle while the result of a prior
// merge request is unknown. Only status reconciliation may clear it.
func (k *KB) ServerWritesBlocked() error {
	if k.ServerGit == nil {
		return nil
	}
	state := k.ServerGitStatus()
	if state.Phase == "merge_uncertain" {
		return fmt.Errorf("server git profile is degraded: merge outcome is uncertain; reconcile with pr_status before new writes")
	}
	return nil
}

// RecordServerRebaseConflict persists server-specific metadata before the
// caller returns the structured git conflict. Non-concept files remain a
// fail-closed error because the agent-facing registry cannot safely edit them.
func (k *KB) RecordServerRebaseConflict(err error) {
	var rce *gitx.RebaseConflictError
	if !errors.As(err, &rce) || k.ServerGit == nil {
		return
	}
	state := k.ServerGitStatus()
	for _, file := range rce.Files {
		id, concept := GitPathToConceptID(file)
		if !concept {
			// A conflict outside data/ cannot be exposed as a concept, but it
			// must still stop server finalization. Retain a stable path-keyed
			// registry entry so it can be explicitly resolved; silently skipping
			// it would make a later PR look safe while Git remains conflicted.
			id = "git-path:" + filepath.ToSlash(file)
		}
		c := Conflict{ConceptID: id, Path: file, LocalSHA: rce.LocalSHA, RemoteSHA: rce.RemoteSHA, Branch: rce.Branch, BaseBranch: k.ServerGit.BaseBranch, WorkingBranch: k.ServerGit.WorkingBranch, PRNumber: state.PRNumber, PRURL: state.PRURL, Files: rce.Files}
		_ = k.RegisterConflict(c)
		if concept {
			_ = k.MarkDegraded(id)
		}
	}
}

// ReconcileServerPR retries a previously failed PR lookup/create from a
// status request. It does not change Git refs and is a no-op for Local Core.
func (k *KB) ReconcileServerPR() error {
	if k.ServerGit == nil {
		return nil
	}
	return k.WithGitLock(func() error {
		ctx, cancel := serverGitContext()
		defer cancel()
		return k.syncServerPR(ctx)
	})
}

func (k *KB) recordForgeError(err error) error {
	state := k.ServerGitStatus()
	state.LastForgeError = err.Error()
	_ = k.saveServerGitState(state)
	k.SetGitStatus("degraded", err)
	return err
}

func (k *KB) markMergeUncertain(state ServerGitState, sha string, err error) error {
	state.Phase, state.MergeSHA, state.LastForgeError = "merge_uncertain", sha, err.Error()
	_ = k.saveServerGitState(state)
	k.SetGitStatus("degraded", err)
	return err
}

func (k *KB) reconcileUncertainMerge(ctx context.Context, state ServerGitState) error {
	if state.PRNumber == 0 {
		return k.recordForgeError(fmt.Errorf("merge outcome uncertain without PR identity"))
	}
	pr, err := k.ServerGit.Forge.GetPR(ctx, k.ServerGit.Owner, k.ServerGit.Repository, state.PRNumber)
	if err != nil {
		return k.recordForgeError(err)
	}
	if strings.EqualFold(pr.State, "open") {
		return k.recordForgeError(fmt.Errorf("merge outcome remains uncertain for open PR #%d", pr.Number))
	}
	if !pr.Merged {
		return k.recordForgeError(fmt.Errorf("merge outcome is unmerged %q for PR #%d", pr.State, pr.Number))
	}
	if err := gitx.Fetch(k.Root, "origin", k.GitEnv...); err != nil {
		return k.recordForgeError(fmt.Errorf("merged PR reconciliation fetch: %w", err))
	}
	baseHead, err := gitx.HeadSHAAt(k.Root, "origin/"+k.ServerGit.BaseBranch)
	if err != nil {
		return k.recordForgeError(err)
	}
	mergeSHA := pr.MergeSHA
	if mergeSHA == "" {
		mergeSHA = state.MergeSHA
	}
	if mergeSHA == "" {
		mergeSHA = baseHead
	}
	ok, err := gitx.IsAncestor(k.Root, mergeSHA, "origin/"+k.ServerGit.BaseBranch)
	if err != nil || !ok {
		return k.recordForgeError(fmt.Errorf("merged PR result %s is not present on origin/%s", mergeSHA, k.ServerGit.BaseBranch))
	}
	if status, _ := gitx.Status(k.Root); strings.TrimSpace(status) != "" {
		return k.recordForgeError(fmt.Errorf("merged PR reconciliation requires a clean working tree"))
	}
	oldHead, _ := gitx.HeadSHAAt(k.Root, "origin/"+k.ServerGit.WorkingBranch)
	if err := gitx.ResetHardTo(k.Root, baseHead, k.GitEnv...); err != nil {
		return k.recordForgeError(err)
	}
	if err := gitx.PushForceWithLease(k.Root, "origin", k.ServerGit.WorkingBranch, oldHead, k.GitEnv...); err != nil {
		return k.recordForgeError(err)
	}
	state.PRNumber, state.PRURL, state.PRHeadSHA, state.PRBaseSHA, state.LastForgeError, state.Phase, state.MergeSHA = 0, "", "", "", "", "", ""
	return k.saveServerGitState(state)
}

// FinalizeServerPR enforces review, a caller-supplied PR head, rebase and a
// force-with-lease update of only the dedicated working branch before asking
// the forge for a squash merge.
func (k *KB) FinalizeServerPR(ctx context.Context, expectedHead string) error {
	if k.ServerGit == nil {
		return fmt.Errorf("server git profile is not enabled")
	}
	if expectedHead == "" {
		return fmt.Errorf("head_sha is required")
	}
	if cs, err := k.ListConflicts(); err != nil {
		return err
	} else if len(cs) != 0 {
		return fmt.Errorf("cannot finalize PR with %d unresolved conflict(s)", len(cs))
	}
	return k.WithGitLock(func() error {
		state := k.ServerGitStatus()
		if state.Phase == "merge_uncertain" {
			return fmt.Errorf("cannot finalize while merge outcome is uncertain")
		}
		if state.PRNumber == 0 {
			return fmt.Errorf("no open server-profile PR")
		}
		pr, err := k.ServerGit.Forge.GetPR(ctx, k.ServerGit.Owner, k.ServerGit.Repository, state.PRNumber)
		if err != nil {
			return k.recordForgeError(err)
		}
		if !strings.EqualFold(pr.State, "open") || pr.HeadSHA != expectedHead {
			return fmt.Errorf("stale or closed PR: expected open head %s, current state=%s head=%s", expectedHead, pr.State, pr.HeadSHA)
		}
		if state.PRHeadSHA != expectedHead {
			return fmt.Errorf("stale PR head: expected %s, current %s", expectedHead, state.PRHeadSHA)
		}
		if state.PRBaseSHA != "" && pr.BaseSHA != "" && state.PRBaseSHA != pr.BaseSHA {
			return fmt.Errorf("stale PR base: expected %s, current %s", state.PRBaseSHA, pr.BaseSHA)
		}
		if state.PRBaseSHA == "" && pr.BaseSHA != "" {
			state.PRBaseSHA = pr.BaseSHA
			if err := k.saveServerGitState(state); err != nil {
				return err
			}
		}
		ready, err := k.ServerGit.Forge.PRReady(ctx, k.ServerGit.Owner, k.ServerGit.Repository, state.PRNumber)
		if err != nil {
			return k.recordForgeError(err)
		}
		if !ready {
			return fmt.Errorf("PR reviews or checks are not satisfied")
		}
		if err := gitx.Fetch(k.Root, "origin", k.GitEnv...); err != nil {
			return err
		}
		remoteHead, err := gitx.HeadSHAAt(k.Root, "origin/"+k.ServerGit.WorkingBranch)
		if err != nil {
			return err
		}
		if remoteHead != expectedHead {
			return fmt.Errorf("stale PR head after fetch: expected %s, current %s", expectedHead, remoteHead)
		}
		if err := gitx.RebaseOntoAutostash(k.Root, "origin", k.ServerGit.BaseBranch, k.GitEnv...); err != nil {
			k.RecordServerRebaseConflict(err)
			return err
		}
		if errs, err := k.Validate(""); err != nil {
			return err
		} else if len(errs) != 0 {
			return fmt.Errorf("finalize PR validation failed: %d error(s)", len(errs))
		}
		changes, err := gitx.DiffNameStatus(k.Root, "origin/"+k.ServerGit.BaseBranch, "HEAD")
		if err != nil {
			return fmt.Errorf("finalize PR changed concepts: %w", err)
		}
		var ids []string
		for _, change := range changes {
			if id, ok := GitPathToConceptID(change.Path); ok {
				ids = append(ids, id)
			}
		}
		if len(ids) != 0 {
			conceptIDs := make([]okf.ConceptID, len(ids))
			for i, id := range ids {
				conceptIDs[i] = okf.ConceptID(id)
			}
			gate, err := k.CommitGate(conceptIDs)
			if err != nil {
				return err
			}
			if !gate.Pass {
				return fmt.Errorf("finalize PR commit gate failed")
			}
		}
		if k.ServerMergeLint != nil {
			if err := k.ServerMergeLint(); err != nil {
				return fmt.Errorf("finalize PR lint: %w", err)
			}
		}
		if err := gitx.PushForceWithLease(k.Root, "origin", k.ServerGit.WorkingBranch, expectedHead, k.GitEnv...); err != nil {
			return err
		}
		newHead, err := gitx.HeadSHA(k.Root)
		if err != nil {
			return err
		}
		// Re-read the forge immediately before merge. A base advance can race
		// rebase/gates; GitHub's PR is the merge authority for both head and base.
		latest, err := k.ServerGit.Forge.GetPR(ctx, k.ServerGit.Owner, k.ServerGit.Repository, state.PRNumber)
		if err != nil {
			return k.recordForgeError(err)
		}
		if !strings.EqualFold(latest.State, "open") || latest.HeadSHA != newHead {
			return fmt.Errorf("stale PR before merge: expected open head %s, current state=%s head=%s", newHead, latest.State, latest.HeadSHA)
		}
		if pr.BaseSHA != "" && latest.BaseSHA != "" && latest.BaseSHA != pr.BaseSHA {
			return fmt.Errorf("stale PR base before merge: expected %s, current %s", pr.BaseSHA, latest.BaseSHA)
		}
		merged, mergeErr := k.ServerGit.Forge.MergeSquash(ctx, k.ServerGit.Owner, k.ServerGit.Repository, state.PRNumber, newHead)
		if mergeErr != nil {
			return k.markMergeUncertain(state, newHead, fmt.Errorf("squash merge outcome uncertain: %w", mergeErr))
		}
		if !merged.Merged {
			return k.markMergeUncertain(state, newHead, fmt.Errorf("squash merge outcome uncertain"))
		}
		state.Phase, state.MergeSHA = "merge_uncertain", merged.SHA
		if err := k.saveServerGitState(state); err != nil {
			return err
		}
		return k.reconcileUncertainMerge(ctx, state)
	})
}

// serverGitContext bounds forge calls while preserving the operation lock.
func serverGitContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 15*time.Second)
}
