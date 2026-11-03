package kb

// Reserved-file conflicts (D311). log.md, index.md, _map.md and _archive.md are
// not concepts: GitPathToConceptID maps none of them, so a rebase conflict that
// touches only these files used to register nothing and freeze the KB — no
// conflicts_list entry, nothing git_conflict_resolve could act on. The server
// now reconciles them itself; the conflict registry is the fallback when it
// cannot, never a git command handed to the agent.

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"github.com/BeppeTemp/cartographer/internal/gitx"
)

// gitAttributesFile is the committed attributes file at the KB root, and
// logUnionAttr the line that gives data/log.md git's built-in union merge: two
// clones that each prepended an entry merge into both entries instead of a
// conflict. Committed, not .git/info/attributes, so every clone inherits it.
const (
	gitAttributesFile = ".gitattributes"
	logUnionAttr      = "data/log.md merge=union"
)

// ConflictKindReserved marks a registry entry whose ConceptID is the git path
// of a reserved file rather than a concept ID.
const ConflictKindReserved = "reserved"

// ConflictKindArtifact marks a registry entry for a conflicting file that is
// neither a concept nor a reserved one (a skill, a template, an expanded
// concept's asset, instructions.md, settings.yaml, paths.yaml): ConceptID holds
// its git path, and the strategies are ours|theirs (D1563).
const ConflictKindArtifact = "artifact"

// IsPathConflict reports whether the registry entry's ConceptID is a git path
// (any kind but a concept).
func (c Conflict) IsPathConflict() bool {
	return c.Kind == ConflictKindReserved || c.Kind == ConflictKindArtifact
}

// IsReservedFile reports whether base (a file name, not a path) is one of the
// KB's reserved files, which are never concepts.
func IsReservedFile(base string) bool {
	switch base {
	case "index.md", "log.md", "_map.md", "_archive.md":
		return true
	}
	return false
}

// IsReservedPath reports whether a git-relative path names a reserved file
// under data/: log.md, _map.md, _archive.md, or the index.md of a map or of the
// root. An expanded concept's body ("data/<map>/<concept>/index.md") is NOT
// reserved: it is a concept (GitPathToExpandedConceptID), and resolving its
// conflict as a structural file dropped a concurrent edit to its body (D1566).
func IsReservedPath(p string) bool {
	p = filepath.ToSlash(p)
	if !strings.HasPrefix(p, "data/") || !IsReservedFile(path.Base(p)) {
		return false
	}
	_, expanded := GitPathToExpandedConceptID(p)
	return !expanded
}

// GitPathToExpandedConceptID maps "data/<map>/<concept>/index.md", the body of
// an expanded concept (D77), to its concept ID "<map>/<concept>". A map's own
// "data/<map>/index.md" and the root "data/index.md" are not concepts.
func GitPathToExpandedConceptID(p string) (string, bool) {
	p = filepath.ToSlash(p)
	rest, ok := strings.CutPrefix(p, "data/")
	if !ok || path.Base(rest) != "index.md" {
		return "", false
	}
	dir := path.Dir(rest)
	if strings.Count(dir, "/") != 1 {
		return "", false
	}
	return dir, true
}

// isCuratedIndexPath reports whether p is an index.md that people and
// index_patch/index_entry edit (D301, D714): the root's, or the map's unless
// its contract says `index: generated`. Only a generated index is rewritten by
// every write, so only that one can safely take the remote side.
func (k *KB) isCuratedIndexPath(p string) bool {
	p = filepath.ToSlash(p)
	if path.Base(p) != "index.md" {
		return false
	}
	name := strings.TrimSuffix(strings.TrimPrefix(p, "data/"), "index.md")
	name = strings.TrimSuffix(name, "/")
	if name == "" || strings.Contains(name, "/") {
		return true
	}
	contract, err := k.ReadMapContract(name)
	return err != nil || contract.Index != IndexGenerated
}

// isLogPath reports whether p is a log.md, the one append-only reserved file:
// it is reconciled by union, every other reserved file by taking the remote.
func isLogPath(p string) bool { return path.Base(filepath.ToSlash(p)) == "log.md" }

// writeGitAttributes makes root/.gitattributes carry logUnionAttr, appending
// it to an existing file. It reports whether the file changed. The path is
// Lstat'ed first: writing through a symlink would modify its target (D148).
func writeGitAttributes(root string) (bool, error) {
	p := filepath.Join(root, gitAttributesFile)
	var existing string
	fi, err := os.Lstat(p)
	switch {
	case err == nil && !fi.Mode().IsRegular():
		return false, fmt.Errorf("%s is not a regular file", gitAttributesFile)
	case err == nil:
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return false, rerr
		}
		existing = string(b)
		for _, line := range strings.Split(existing, "\n") {
			if strings.TrimSpace(line) == logUnionAttr {
				return false, nil
			}
		}
	case !errors.Is(err, os.ErrNotExist):
		return false, err
	}
	if existing != "" && !strings.HasSuffix(existing, "\n") {
		existing += "\n"
	}
	return true, writeFileAtomic(p, []byte(existing+logUnionAttr+"\n"))
}

// ensureGitAttributes brings a KB created before D311 to the scaffold's
// .gitattributes, in a commit of its own. It runs once per process after a
// successful SyncIn (so the commit lands on a refreshed base) and costs one
// file read afterwards. Best-effort: a failure is logged and retried on the
// next process start. Callers hold the git lock.
func (k *KB) ensureGitAttributes() {
	if k.gitattrsChecked {
		return
	}
	k.gitattrsChecked = true
	changed, err := writeGitAttributes(k.Root)
	if err == nil && changed {
		err = gitx.CommitPaths(k.Root, []string{gitAttributesFile}, "chore: set merge=union for data/log.md", "", "", k.GitCommandEnv()...)
		if errors.Is(err, gitx.ErrNothingToCommit) {
			err = nil
		}
		// CommitPaths commits through a temporary index: stage the file in
		// the real one too, or status shows it modified against HEAD.
		if err == nil {
			err = gitx.AddPath(k.Root, gitAttributesFile)
		}
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "cartographer: %s: %v\n", gitAttributesFile, err)
	}
}

// reservedResolution is the content a reserved file takes when localSHA and
// remoteSHA conflict on it: "union" of both sides for log.md and for a curated
// index.md (hand-edited and patched by index_entry, so each side may hold a
// line the other lacks, D1566), the remote's version ("theirs") for the rest —
// a generated index.md is regenerated by the next write (RegenerateIndexes),
// _map.md and _archive.md are curated scaffolding whose canonical copy is the
// remote's.
func (k *KB) reservedResolution(p, localSHA, remoteSHA string) (string, error) {
	if isLogPath(p) || k.isCuratedIndexPath(p) {
		return k.unionContent(p, localSHA, remoteSHA)
	}
	return gitx.ShowFile(k.Root, remoteSHA, p)
}

// unionContent is the union merge of p between localSHA and remoteSHA against
// their merge base. A side where the file is missing counts as empty.
func (k *KB) unionContent(p, localSHA, remoteSHA string) (string, error) {
	show := func(ref string) string {
		if ref == "" {
			return ""
		}
		s, err := gitx.ShowFile(k.Root, ref, p)
		if err != nil {
			return ""
		}
		return s
	}
	base, _ := gitx.MergeBase(k.Root, localSHA, remoteSHA)
	return gitx.MergeFileUnion(show(base), show(localSHA), show(remoteSHA))
}

// pullRebase is the local-profile rebase onto the remote branch the caller has
// just fetched (gitx.RebaseOntoAutostash: no second network round-trip, which
// would run outside the fetch deadline while the git lock is held) with the
// D311 fallback: a conflict whose files are all reserved is reconciled by
// autoResolveReserved and the pull reports success. A conflict that touches a
// concept, or one the auto-resolution could not settle, comes back as the
// original *gitx.RebaseConflictError for the registry.
func (k *KB) pullRebase(remote, branch string) error {
	err := gitx.RebaseOntoAutostash(k.Root, remote, branch, k.GitCommandEnv()...)
	var rce *gitx.RebaseConflictError
	if err == nil {
		k.realignCommitters(remote, branch)
		return nil
	}
	if !errors.As(err, &rce) || !allReserved(rce.Files) {
		return err
	}
	if aerr := k.autoResolveReserved(rce); aerr != nil {
		fmt.Fprintf(os.Stderr, "cartographer: auto-resolve reserved file conflict (%s): %v\n", strings.Join(rce.Files, ", "), aerr)
		return err
	}
	fmt.Fprintf(os.Stderr, "cartographer: auto-resolved reserved file conflict: %s\n", strings.Join(rce.Files, ", "))
	k.realignCommitters(remote, branch)
	return nil
}

// realignNotLinear records the KBs whose "not a linear range" notice was
// printed, so a sync that finds the same merge again stays quiet.
var realignNotLinear sync.Map

// realignCommitters gives the local commits the pull just replayed their
// author back as committer, and a commit no writer was known for the KB's
// configured identity (gitx.RealignCommitters, D950). Best-effort: a failure is
// logged and leaves the pulled state, which the next push reports if the
// forge refuses it.
func (k *KB) realignCommitters(remote, branch string) {
	if remote == "" {
		remote = "origin"
	}
	defName, defEmail, _ := k.ConfiguredIdentity()
	serverName, serverEmail := ServerIdentity()
	if n, err := gitx.RealignCommitters(k.Root, remote+"/"+branch, serverName, serverEmail, defName, defEmail, k.GitEnv...); err != nil {
		// A range with a merge stays so until it is published: say it once.
		if _, again := realignNotLinear.LoadOrStore(k.Root, true); errors.Is(err, gitx.ErrNotLinear) && again {
			return
		}
		fmt.Fprintf(os.Stderr, "cartographer: realign committers: %v\n", err)
	} else if n > 0 {
		fmt.Fprintf(os.Stderr, "cartographer: %d local commit(s) re-committed by their writer (D950)\n", n)
	}
}

// allReserved reports whether files is non-empty and none of them maps to a
// concept.
func allReserved(files []string) bool {
	if len(files) == 0 {
		return false
	}
	for _, f := range files {
		if !IsReservedPath(f) {
			return false
		}
	}
	return true
}

// autoResolveReserved converges an aborted rebase whose conflicts are all on
// reserved files with one merge commit of the remote SHA, the files set to
// reservedResolution — the same merge FinalizeConflicts makes, so the next
// push is a fast-forward. Re-running the rebase instead would conflict again:
// it replays the local commits one by one, and the commit that touched the
// file still meets the remote's version. On any failure the merge is aborted
// and the tree is left as the aborted rebase left it.
func (k *KB) autoResolveReserved(rce *gitx.RebaseConflictError) error {
	if rce.RemoteSHA == "" || rce.LocalSHA == "" {
		return fmt.Errorf("missing local or remote SHA")
	}
	if status, _ := gitx.Status(k.Root); strings.TrimSpace(status) != "" {
		return fmt.Errorf("working tree is not clean")
	}
	contents := make(map[string]string, len(rce.Files))
	for _, f := range rce.Files {
		c, err := k.reservedResolution(f, rce.LocalSHA, rce.RemoteSHA)
		if err != nil {
			return err
		}
		contents[f] = c
	}
	if _, err := gitx.MergeNoCommitNoFFEnv(k.Root, rce.RemoteSHA, ServerGitEnv()...); err != nil {
		return err
	}
	fail := func(err error) error {
		_ = gitx.MergeAbort(k.Root)
		return err
	}
	for f, c := range contents {
		if err := writeFileAtomic(filepath.Join(k.Root, filepath.FromSlash(f)), []byte(c)); err != nil {
			return fail(err)
		}
		if err := gitx.AddPath(k.Root, f); err != nil {
			return fail(err)
		}
	}
	unmerged, err := gitx.UnmergedFiles(k.Root)
	if err != nil {
		return fail(err)
	}
	if len(unmerged) > 0 {
		return fail(fmt.Errorf("merge left unexpected conflicting file(s): %s", strings.Join(unmerged, ", ")))
	}
	msg := "auto-resolve reserved file conflict: " + strings.Join(rce.Files, ", ")
	if err := gitx.Commit(k.Root, msg, "", "", k.GitCommandEnv()...); err != nil && !errors.Is(err, gitx.ErrNothingToCommit) {
		return fail(err)
	}
	return nil
}
