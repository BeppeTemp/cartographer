// Package kb — conflict registry and degraded-marker support (Step 3).
// State is persisted in <root>/.cartographer/conflicts.json, which is local-only
// (gitignored) so it never enters the versioned history.
package kb

import (
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

const (
	cartographerSubdir = ".cartographer"
	conflictsFilename  = "conflicts.json"
)

// Conflict describes a rebase conflict detected on a specific concept.
type Conflict struct {
	ConceptID  string   `json:"concept_id"`
	Path       string   `json:"path"`       // git-relative file path (e.g. "data/shared/notes/c.md")
	LocalSHA   string   `json:"local_sha"`  // HEAD SHA before the failed rebase
	RemoteSHA  string   `json:"remote_sha"` // SHA of <remote>/<branch> after fetch
	Branch     string   `json:"branch"`
	Files      []string `json:"files"`       // all conflicting git paths in the same rebase
	DetectedAt string   `json:"detected_at"` // RFC3339 UTC

	// Step 4 — recorded resolution (empty until the agent calls git_conflict_resolve).
	ResolutionStrategy string `json:"resolution_strategy,omitempty"` // "ours" | "theirs" | "edit"
	ResolutionBody     string `json:"resolution_body,omitempty"`     // full reconciled file content, used when strategy="edit"
}

// cartographerDirPath returns the path to the .cartographer directory inside the KB root.
func (k *KB) cartographerDirPath() string {
	return filepath.Join(k.Root, cartographerSubdir)
}

// conflictsFilePath returns the path of the conflicts.json file.
func (k *KB) conflictsFilePath() string {
	return filepath.Join(k.cartographerDirPath(), conflictsFilename)
}

// ensureCartographerDir creates the .cartographer directory and ensures it is
// excluded from git via .git/info/exclude (ensureInfoExclude, D62) — never a
// versioned .gitignore.
func (k *KB) ensureCartographerDir() error {
	if err := os.MkdirAll(k.cartographerDirPath(), 0o755); err != nil {
		return fmt.Errorf("ensureCartographerDir: %w", err)
	}
	return ensureInfoExclude(k.Root, ".cartographer/")
}

// loadConflicts reads the current conflict registry. Returns nil, nil if not present.
func (k *KB) loadConflicts() ([]Conflict, error) {
	data, err := os.ReadFile(k.conflictsFilePath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("loadConflicts: %w", err)
	}
	var conflicts []Conflict
	if err := json.Unmarshal(data, &conflicts); err != nil {
		return nil, fmt.Errorf("loadConflicts: unmarshal: %w", err)
	}
	return conflicts, nil
}

// saveConflicts persists the conflict registry atomically.
func (k *KB) saveConflicts(conflicts []Conflict) error {
	if err := k.ensureCartographerDir(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(conflicts, "", "  ")
	if err != nil {
		return fmt.Errorf("saveConflicts: marshal: %w", err)
	}
	return writeFileAtomic(k.conflictsFilePath(), data)
}

// RegisterConflict adds or updates the conflict entry for c.ConceptID.
// Idempotent: if an entry for the same ConceptID already exists it is replaced.
func (k *KB) RegisterConflict(c Conflict) error {
	if c.DetectedAt == "" {
		c.DetectedAt = time.Now().UTC().Format(time.RFC3339)
	}
	conflicts, err := k.loadConflicts()
	if err != nil {
		return err
	}
	for i, existing := range conflicts {
		if existing.ConceptID == c.ConceptID {
			// Preserve a previously recorded resolution if the incoming conflict
			// (e.g. a re-detection during a later SyncIn) carries none.
			if c.ResolutionStrategy == "" && existing.ResolutionStrategy != "" {
				c.ResolutionStrategy = existing.ResolutionStrategy
				c.ResolutionBody = existing.ResolutionBody
			}
			conflicts[i] = c
			return k.saveConflicts(conflicts)
		}
	}
	conflicts = append(conflicts, c)
	return k.saveConflicts(conflicts)
}

// ListConflicts returns all open conflicts (never nil on success — may be empty slice).
func (k *KB) ListConflicts() ([]Conflict, error) {
	cs, err := k.loadConflicts()
	if cs == nil && err == nil {
		return []Conflict{}, nil
	}
	return cs, err
}

// ClearConflict removes the conflict entry for conceptID. No-op if not present.
func (k *KB) ClearConflict(conceptID string) error {
	conflicts, err := k.loadConflicts()
	if err != nil {
		return err
	}
	filtered := conflicts[:0]
	for _, c := range conflicts {
		if c.ConceptID != conceptID {
			filtered = append(filtered, c)
		}
	}
	return k.saveConflicts(filtered)
}

// MarkDegraded sets status: degraded on the given concept's frontmatter.
// Best-effort: if the concept does not exist or its frontmatter cannot be parsed,
// an error is returned and the caller decides whether to log and continue.
func (k *KB) MarkDegraded(conceptID string) error {
	data, err := k.ReadConcept(okf.ConceptID(conceptID))
	if err != nil {
		return fmt.Errorf("MarkDegraded: read %q: %w", conceptID, err)
	}
	fm, err := okf.ParseFrontmatter(data.FrontmatterRaw)
	if err != nil {
		return fmt.Errorf("MarkDegraded: parse frontmatter %q: %w", conceptID, err)
	}
	fm.Set("status", "degraded")
	if _, err := k.WriteConcept(okf.ConceptID(conceptID), fm, data.Body, ""); err != nil {
		return fmt.Errorf("MarkDegraded: write %q: %w", conceptID, err)
	}
	return nil
}

// RecordResolution stores the agent's chosen resolution for a registered conflict.
// strategy must be "ours", "theirs", or "edit"; for "edit", body is the full reconciled
// file content (frontmatter + body). Returns an error if no conflict is registered for
// conceptID. The git transaction is deferred to FinalizeConflicts.
func (k *KB) RecordResolution(conceptID, strategy, body string) error {
	conflicts, err := k.loadConflicts()
	if err != nil {
		return err
	}
	for i := range conflicts {
		if conflicts[i].ConceptID == conceptID {
			conflicts[i].ResolutionStrategy = strategy
			conflicts[i].ResolutionBody = body
			return k.saveConflicts(conflicts)
		}
	}
	return fmt.Errorf("RecordResolution: no open conflict for %q", conceptID)
}

// PendingConflictCount returns how many open conflicts still lack a recorded resolution.
func (k *KB) PendingConflictCount() (int, error) {
	conflicts, err := k.loadConflicts()
	if err != nil {
		return 0, err
	}
	n := 0
	for _, c := range conflicts {
		if c.ResolutionStrategy == "" {
			n++
		}
	}
	return n, nil
}

// resolvedContent computes the final file content for a conflict from its recorded
// resolution. "ours"/"theirs" are materialised by content from the local/remote SHA
// (git show), which sidesteps the inverted --ours/--theirs semantics of a live rebase.
func (k *KB) resolvedContent(c Conflict) (string, error) {
	switch c.ResolutionStrategy {
	case "ours":
		return gitx.ShowFile(k.Root, c.LocalSHA, c.Path)
	case "theirs":
		return gitx.ShowFile(k.Root, c.RemoteSHA, c.Path)
	case "edit":
		if c.ResolutionBody == "" {
			return "", fmt.Errorf("resolvedContent: empty body for edit resolution of %q", c.ConceptID)
		}
		return c.ResolutionBody, nil
	default:
		return "", fmt.Errorf("resolvedContent: unknown strategy %q for %q", c.ResolutionStrategy, c.ConceptID)
	}
}

// FinalizeConflicts converges the diverged history in a single git merge: it merges the
// remote conflict SHA into the current branch, overwrites every conflicting file with the
// content chosen by its recorded resolution, commits the merge, pushes (best-effort,
// respecting GitSync/remote), and clears the registry and degraded markers.
//
// It must only be called when every open conflict has a recorded resolution (see
// PendingConflictCount). Returns the resolved concept IDs. On any git failure the merge is
// aborted and the pre-merge working tree (including the degraded markers) is restored.
func (k *KB) FinalizeConflicts() ([]string, error) {
	conflicts, err := k.loadConflicts()
	if err != nil {
		return nil, err
	}
	if len(conflicts) == 0 {
		return nil, nil
	}
	for _, c := range conflicts {
		if c.ResolutionStrategy == "" {
			return nil, fmt.Errorf("FinalizeConflicts: conflict on %q has no recorded resolution", c.ConceptID)
		}
	}
	remoteSHA := conflicts[0].RemoteSHA
	if remoteSHA == "" {
		return nil, fmt.Errorf("FinalizeConflicts: missing remote SHA")
	}

	// Clean the working tree: the degraded markers are uncommitted and would block the
	// merge. Stash them away (never popped on success — the resolution supersedes them).
	stashed := false
	if status, _ := gitx.Status(k.Root); strings.TrimSpace(status) != "" {
		if err := gitx.StashPush(k.Root); err != nil {
			return nil, fmt.Errorf("FinalizeConflicts: stash: %w", err)
		}
		stashed = true
	}
	restore := func() {
		if stashed {
			_ = gitx.StashPop(k.Root)
		}
	}

	if _, err := gitx.MergeNoCommitNoFF(k.Root, remoteSHA); err != nil {
		restore()
		return nil, fmt.Errorf("FinalizeConflicts: merge: %w", err)
	}

	// Overwrite each conflicting file with its resolved content and stage it.
	resolvedPaths := make(map[string]bool, len(conflicts))
	for _, c := range conflicts {
		content, cerr := k.resolvedContent(c)
		if cerr != nil {
			_ = gitx.MergeAbort(k.Root)
			restore()
			return nil, cerr
		}
		abs := filepath.Join(k.Root, filepath.FromSlash(c.Path))
		if werr := writeFileAtomic(abs, []byte(content)); werr != nil {
			_ = gitx.MergeAbort(k.Root)
			restore()
			return nil, fmt.Errorf("FinalizeConflicts: write %s: %w", c.Path, werr)
		}
		if aerr := gitx.AddPath(k.Root, c.Path); aerr != nil {
			_ = gitx.MergeAbort(k.Root)
			restore()
			return nil, fmt.Errorf("FinalizeConflicts: add %s: %w", c.Path, aerr)
		}
		resolvedPaths[c.Path] = true
	}

	// Refuse to finalize if the merge left conflicting files the registry does not cover.
	if unmerged, uerr := gitx.UnmergedFiles(k.Root); uerr == nil {
		var foreign []string
		for _, f := range unmerged {
			if !resolvedPaths[f] {
				foreign = append(foreign, f)
			}
		}
		if len(foreign) > 0 {
			_ = gitx.MergeAbort(k.Root)
			restore()
			return nil, fmt.Errorf("FinalizeConflicts: %d unexpected conflicting file(s) not in registry: %s",
				len(foreign), strings.Join(foreign, ", "))
		}
	}

	ids := make([]string, 0, len(conflicts))
	for _, c := range conflicts {
		ids = append(ids, c.ConceptID)
	}
	msg := "resolve git conflict: " + strings.Join(ids, ", ")
	authorName, authorEmail := k.gitAuthor()
	if err := gitx.Commit(k.Root, msg, authorName, authorEmail, k.GitEnv...); err != nil && !errors.Is(err, gitx.ErrNothingToCommit) {
		_ = gitx.MergeAbort(k.Root)
		restore()
		return nil, fmt.Errorf("FinalizeConflicts: commit: %w", err)
	}

	// Resolution committed: discard the stashed degraded markers.
	if stashed {
		_ = gitx.StashDrop(k.Root)
	}

	// Push the converged history (best-effort). A failure here leaves the resolution
	// committed locally; the next write re-attempts the push.
	if perr := k.SyncOut(); perr != nil {
		fmt.Fprintf(os.Stderr, "cartographer: FinalizeConflicts push: %v\n", perr)
	}

	// Clear the registry.
	for _, c := range conflicts {
		_ = k.ClearConflict(c.ConceptID)
	}
	return ids, nil
}

// GitPathToConceptID converts a git-relative file path to a ConceptID.
// Only paths of the form "data/<...>.md" are converted; all others return ("", false).
// Reserved files (index.md, log.md, _map.md, _archive.md) are excluded — note
// this means an expanded concept's "index.md" (D77 WP2) is not reported here;
// callers needing that mapping should go through WalkConcepts instead.
func GitPathToConceptID(path string) (string, bool) {
	// Normalize to forward slashes regardless of OS.
	path = filepath.ToSlash(path)
	const prefix = "data/"
	if !strings.HasPrefix(path, prefix) {
		return "", false
	}
	if !strings.HasSuffix(path, ".md") {
		return "", false
	}
	id := strings.TrimPrefix(path, prefix)
	id = strings.TrimSuffix(id, ".md")
	if id == "" {
		return "", false
	}
	// Exclude reserved filenames at any depth.
	base := filepath.Base(id + ".md")
	if base == "index.md" || base == "log.md" || base == "_map.md" || base == "_archive.md" {
		return "", false
	}
	return id, true
}
