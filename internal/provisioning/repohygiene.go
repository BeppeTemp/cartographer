package provisioning

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// repohygiene.go (D193 WP5) — generated files must not dirty the repository.
//
// A workspace projection writes into a directory the user version-controls. Two
// rules follow, and neither is negotiable:
//
//  1. Cartographer excludes **only its own untracked paths**, and it does so in
//     `info/exclude` of the git directory — the per-clone, unversioned ignore file. It never
//     edits `.gitignore`: that file is the repository's, shared with everyone
//     who clones it, and a tool that writes there commits an opinion on behalf
//     of a team.
//  2. If a path Cartographer would own is already **tracked**, the sync
//     **refuses** with an attributed error. Overwriting a tracked file destroys
//     work that is under version control, and there is no safe silent answer.

// excludeMarkerBegin/End delimit Cartographer's block in .git/info/exclude (a
// linked worktree's block has its own, keyed markers: blockMarkers), so
// the block can be rewritten and removed without touching anything else in a
// file the user also writes by hand.
const (
	excludeMarkerBegin = "# >>> cartographer (managed) >>>"
	excludeMarkerEnd   = "# <<< cartographer (managed) <<<"
)

// gitTrackedFn is indirected for tests: the hygiene rules must be testable in a
// temp dir without constructing a real repository for every case.
var gitTrackedFn = gitTrackedPaths

// gitTrackedPaths returns the set of repository-relative paths git tracks under
// dir. A directory that is not a git repository returns an empty set and no
// error: binding a plain directory is legal, it simply has no hygiene to keep.
func gitTrackedPaths(dir string) (map[string]bool, error) {
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		return map[string]bool{}, nil
	}
	out, err := exec.Command("git", "-C", dir, "ls-files", "-z").Output()
	if err != nil {
		return nil, fmt.Errorf("provisioning: list tracked files in %s: %w", dir, err)
	}
	tracked := map[string]bool{}
	for _, p := range strings.Split(string(out), "\x00") {
		if p != "" {
			tracked[filepath.ToSlash(p)] = true
		}
	}
	return tracked, nil
}

// TrackedCollisionError is the refusal of rule 2: a path the projection owns is
// already under version control.
type TrackedCollisionError struct {
	Workspace string
	Provider  string
	Paths     []string
}

func (e *TrackedCollisionError) Error() string {
	return fmt.Sprintf("workspace %s: %s would write to %s, which git already tracks — "+
		"Cartographer never overwrites a versioned file. Remove or relocate it, or bind this provider to a different workspace",
		e.Workspace, e.Provider, strings.Join(e.Paths, ", "))
}

// CheckWorkspaceHygiene reports whether any path the provider's projection owns
// in workspaceDir is already tracked by git. It is called **before** anything is
// written, so a refusal costs nothing.
//
// A shared-file destination (CLAUDE.md, AGENTS.md, opencode.json) that is
// tracked is deliberately NOT a refusal: those are user-owned files that a
// repository legitimately has, and Cartographer writes a marker-delimited block
// inside them rather than owning the file — the same D57 mechanism it uses for
// a user's settings.json. Only a path Cartographer would own **entirely** — its
// own directories, and .mcp.json, which it writes whole — can collide.
func CheckWorkspaceHygiene(provider, workspaceDir string, ownedPaths, blockPaths []string) error {
	tracked, err := gitTrackedFn(workspaceDir)
	if err != nil {
		return err
	}
	if len(tracked) == 0 {
		return nil
	}
	shared := map[string]bool{}
	for _, p := range blockPaths {
		shared[filepath.ToSlash(p)] = true
	}

	var hits []string
	for _, owned := range ownedPaths {
		slash := filepath.ToSlash(owned)
		if shared[slash] {
			continue
		}
		if tracked[slash] {
			hits = append(hits, slash)
			continue
		}
		// A directory Cartographer owns: any tracked file beneath it is a
		// collision, because pruning the directory would delete it.
		prefix := slash + "/"
		for t := range tracked {
			if strings.HasPrefix(t, prefix) {
				hits = append(hits, t)
			}
		}
	}
	if len(hits) == 0 {
		return nil
	}
	sort.Strings(hits)
	return &TrackedCollisionError{Workspace: workspaceDir, Provider: provider, Paths: dedupe(hits)}
}

// EnsureWorkspaceExcluded writes Cartographer's owned paths into the repository's
// `info/exclude` (excludeFile says where that is), inside its own marker block.
//
// It is idempotent, it never touches a line outside the block, and it never
// touches `.gitignore`. A workspaceDir that is not a git repository is a no-op:
// there is nothing to keep clean. A `.git` file the exclude file cannot be
// resolved from is ErrGitDirUnresolvable, which the callers report as a warning.
//
// Only paths Cartographer owns entirely are excluded. A block it writes inside
// a user-owned file (CLAUDE.md, AGENTS.md) must NOT be excluded — that file is
// the user's, and telling git to ignore it would hide their own edits.
func EnsureWorkspaceExcluded(workspaceDir string, ownedPaths []string) error {
	target, isRepo, err := excludeFile(workspaceDir)
	if err != nil {
		return err
	}
	if !isRepo {
		return nil
	}
	baseDir, path, key := target.baseDir, target.path, target.key
	infoDir := filepath.Dir(path)

	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("provisioning: read %s: %w", path, err)
	}
	kept := stripStaleBlocks(stripExcludeBlock(string(existing), key), target.commonDir)

	var lines []string
	if len(ownedPaths) > 0 {
		lines = make([]string, 0, len(ownedPaths))
		for _, p := range ownedPaths {
			// An empty or "." path is the workspace root: "/" is no pattern and
			// would only look like the root being excluded (#1199).
			if p = strings.Trim(filepath.ToSlash(p), "/"); p == "" || p == "." {
				continue
			}
			lines = append(lines, "/"+p)
		}
		sort.Strings(lines)
		lines = dedupe(lines)
	}
	out := renderExclude(kept, lines, key)
	if err == nil && out == string(existing) {
		// Nothing to change: no write, so a linked .git/info never refuses it.
		return nil
	}
	if strings.TrimSpace(out) == "" && os.IsNotExist(err) {
		// Nothing of ours and nothing of theirs: leave the file as it was
		// rather than creating an empty one.
		return nil
	}
	// The write goes through every directory below baseDir (for a plain
	// repository .git and .git/info, for a linked one info) and the file itself:
	// a symlink in that chain is refused (D148).
	if err := mkdirAllNoFollow(baseDir, infoDir, 0o755); err != nil {
		return fmt.Errorf("provisioning: mkdir %s: %w", infoDir, err)
	}
	return writeFileNoFollow(path, []byte(out), 0o644)
}

// RemoveWorkspaceExclusions drops Cartographer's block entirely, for an unbind
// or a disconnect. The rest of the file is preserved verbatim.
func RemoveWorkspaceExclusions(workspaceDir string) error {
	return EnsureWorkspaceExcluded(workspaceDir, nil)
}

// blockMarkers returns the lines delimiting the block of key. The main checkout
// and a submodule, whose git directory is their own, have no key and keep the
// plain markers; a linked worktree shares info/exclude with the main checkout and
// every other worktree, so its block carries the worktree's name and nobody
// else's block is ever read, rewritten or removed by it.
func blockMarkers(key string) (begin, end string) {
	if key == "" {
		return excludeMarkerBegin, excludeMarkerEnd
	}
	return "# >>> cartographer (managed: " + key + ") >>>", "# <<< cartographer (managed: " + key + ") <<<"
}

func renderExclude(kept string, lines []string, key string) string {
	begin, end := blockMarkers(key)
	var b strings.Builder
	b.WriteString(kept)
	if len(lines) > 0 {
		if kept != "" && !strings.HasSuffix(kept, "\n") {
			b.WriteString("\n")
		}
		b.WriteString(begin + "\n")
		b.WriteString("# Written by `cartographer sync`. Paths this workspace's projection owns.\n")
		b.WriteString("# Remove the block, not the file: `cartographer workspace unbind` does it for you.\n")
		for _, l := range lines {
			b.WriteString(l + "\n")
		}
		b.WriteString(end + "\n")
	}
	return b.String()
}

// stripExcludeBlock returns content with Cartographer's block of key removed, and
// everything else byte-identical, the blocks of other keys included.
func stripExcludeBlock(content, key string) string {
	begin, end := blockMarkers(key)
	if !strings.Contains(content, begin) {
		return content
	}
	var out []string
	inBlock := false
	sc := bufio.NewScanner(strings.NewReader(content))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.TrimSpace(line) == begin:
			inBlock = true
		case strings.TrimSpace(line) == end:
			inBlock = false
		case !inBlock:
			out = append(out, line)
		}
	}
	joined := strings.Join(out, "\n")
	joined = strings.TrimRight(joined, "\n")
	if joined == "" {
		return ""
	}
	return joined + "\n"
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, v := range in {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

// TrackedPaths exposes the tracked-file set to the client, which needs it to
// tell a shared file the repository owns from one Cartographer created itself:
// the first must not be excluded, the second must.
func TrackedPaths(dir string) (map[string]bool, error) { return gitTrackedFn(dir) }

// keyedBlockKey returns the worktree name a keyed block's begin line carries.
func keyedBlockKey(line string) (string, bool) {
	const prefix, suffix = "# >>> cartographer (managed: ", ") >>>"
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, prefix) || !strings.HasSuffix(line, suffix) || len(line) <= len(prefix)+len(suffix) {
		return "", false
	}
	return line[len(prefix) : len(line)-len(suffix)], true
}

// stripStaleBlocks drops the keyed blocks of worktrees that no longer exist, so
// `git worktree remove` does not leave its paths excluded for good in the file it
// shared: a block is stale when <commonDir>/worktrees/<name> is gone. Every
// Ensure and Remove does it, from the main checkout or any worktree. Everything
// else stays byte-identical, and so does the whole content when nothing is stale.
func stripStaleBlocks(content, commonDir string) string {
	if commonDir == "" || !strings.Contains(content, "# >>> cartographer (managed: ") {
		return content
	}
	var out []string
	dropEnd := ""
	dropped := false
	for _, line := range strings.Split(strings.TrimSuffix(content, "\n"), "\n") {
		switch {
		case dropEnd != "":
			if strings.TrimSpace(line) == dropEnd {
				dropEnd = ""
			}
			continue
		default:
			if key, ok := keyedBlockKey(line); ok {
				if _, err := os.Lstat(filepath.Join(commonDir, "worktrees", key)); os.IsNotExist(err) {
					_, dropEnd = blockMarkers(key)
					dropped = true
					continue
				}
			}
		}
		out = append(out, line)
	}
	if !dropped {
		return content
	}
	if len(out) == 0 {
		return ""
	}
	return strings.Join(out, "\n") + "\n"
}
