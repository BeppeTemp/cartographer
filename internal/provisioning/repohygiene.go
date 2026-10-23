package provisioning

import (
	"bufio"
	"errors"
	"fmt"
	"io"
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

// excludeMarkerBegin/End delimit Cartographer's block in .git/info/exclude, so
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
	baseDir, path, isRepo, err := excludeFile(workspaceDir)
	if err != nil {
		return err
	}
	if !isRepo {
		return nil
	}
	infoDir := filepath.Dir(path)

	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("provisioning: read %s: %w", path, err)
	}
	kept := stripExcludeBlock(string(existing))

	var lines []string
	if len(ownedPaths) > 0 {
		lines = make([]string, 0, len(ownedPaths))
		for _, p := range ownedPaths {
			lines = append(lines, "/"+strings.TrimPrefix(filepath.ToSlash(p), "/"))
		}
		sort.Strings(lines)
		lines = dedupe(lines)
	}
	out := renderExclude(kept, lines)
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

// ErrGitDirUnresolvable is the refusal for a `.git` file that does not lead to a
// git directory (no `gitdir:` line, a target that is missing or not a
// directory). The exclude block is a convenience, so the callers warn and go on.
var ErrGitDirUnresolvable = errors.New("provisioning: the .git file does not lead to a git directory")

// ExcludeWarning reports whether err from EnsureWorkspaceExcluded or
// RemoveWorkspaceExclusions is one the sync survives with a warning: the exclude
// file sits behind a link (D148) or cannot be resolved.
func ExcludeWarning(err error) bool {
	return errors.Is(err, ErrSymlinkDestination) || errors.Is(err, ErrGitDirUnresolvable)
}

// excludeFile resolves the exclude file of the repository at workspaceDir the way
// git does, without running it. baseDir anchors the no-follow walk of the write:
// the workspace for a `.git` directory (so a linked `.git` is refused), the
// resolved git directory for a `.git` file.
//
// A `.git` file (a linked worktree or a submodule) holds `gitdir: <path>`,
// relative to the workspace. That directory is the exclude's home, unless it has
// a `commondir` file (a linked worktree): then it is the directory that file
// names, relative to the git directory, because `info/exclude` is shared by all
// the worktrees of a repository.
func excludeFile(workspaceDir string) (baseDir, path string, isRepo bool, err error) {
	dotGit := filepath.Join(workspaceDir, ".git")
	info, lerr := os.Lstat(dotGit)
	if lerr != nil {
		return "", "", false, nil
	}
	if !info.Mode().IsRegular() {
		// A directory, or a link or something else the writers below refuse
		// when they would write: that stays their decision.
		if _, serr := os.Stat(dotGit); serr != nil {
			return "", "", false, nil
		}
		return workspaceDir, filepath.Join(dotGit, "info", "exclude"), true, nil
	}
	gitDir, err := readGitDirFile(dotGit, workspaceDir)
	if err != nil {
		return "", "", false, err
	}
	if rel, rerr := readSmallFile(filepath.Join(gitDir, "commondir")); rerr == nil {
		if common := strings.TrimSpace(rel); common != "" {
			gitDir = resolveFrom(gitDir, common)
		}
	}
	if st, serr := os.Stat(gitDir); serr != nil || !st.IsDir() {
		return "", "", false, fmt.Errorf("%w: %s", ErrGitDirUnresolvable, gitDir)
	}
	return gitDir, filepath.Join(gitDir, "info", "exclude"), true, nil
}

// readGitDirFile returns the directory a `.git` file points at.
func readGitDirFile(dotGit, workspaceDir string) (string, error) {
	content, err := readSmallFile(dotGit)
	if err != nil {
		return "", fmt.Errorf("%w: %s: %v", ErrGitDirUnresolvable, dotGit, err)
	}
	first, _, _ := strings.Cut(content, "\n")
	target, ok := strings.CutPrefix(strings.TrimSpace(first), "gitdir:")
	if target = strings.TrimSpace(target); !ok || target == "" {
		return "", fmt.Errorf("%w: %s has no gitdir line", ErrGitDirUnresolvable, dotGit)
	}
	return resolveFrom(workspaceDir, target), nil
}

// resolveFrom joins a path git wrote (slashes, absolute or relative to base).
func resolveFrom(base, p string) string {
	p = filepath.FromSlash(p)
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Join(base, p)
}

// maxGitPointerSize bounds the pointer files read (`.git`, `commondir`): a real
// one is a line, so a larger file is not one.
const maxGitPointerSize = 64 * 1024

func readSmallFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxGitPointerSize))
	return string(b), err
}

// RemoveWorkspaceExclusions drops Cartographer's block entirely, for an unbind
// or a disconnect. The rest of the file is preserved verbatim.
func RemoveWorkspaceExclusions(workspaceDir string) error {
	return EnsureWorkspaceExcluded(workspaceDir, nil)
}

func renderExclude(kept string, lines []string) string {
	var b strings.Builder
	b.WriteString(kept)
	if len(lines) > 0 {
		if kept != "" && !strings.HasSuffix(kept, "\n") {
			b.WriteString("\n")
		}
		b.WriteString(excludeMarkerBegin + "\n")
		b.WriteString("# Written by `cartographer sync`. Paths this workspace's projection owns.\n")
		b.WriteString("# Remove the block, not the file: `cartographer workspace unbind` does it for you.\n")
		for _, l := range lines {
			b.WriteString(l + "\n")
		}
		b.WriteString(excludeMarkerEnd + "\n")
	}
	return b.String()
}

// stripExcludeBlock returns content with Cartographer's block removed, and
// everything else byte-identical.
func stripExcludeBlock(content string) string {
	if !strings.Contains(content, excludeMarkerBegin) {
		return content
	}
	var out []string
	inBlock := false
	sc := bufio.NewScanner(strings.NewReader(content))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.TrimSpace(line) == excludeMarkerBegin:
			inBlock = true
		case strings.TrimSpace(line) == excludeMarkerEnd:
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
