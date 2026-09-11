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
//     `.git/info/exclude` — the per-clone, unversioned ignore file. It never
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

// EnsureWorkspaceExcluded writes Cartographer's owned paths into
// `<workspaceDir>/.git/info/exclude`, inside its own marker block.
//
// It is idempotent, it never touches a line outside the block, and it never
// touches `.gitignore`. A workspaceDir that is not a git repository is a no-op:
// there is nothing to keep clean.
//
// Only paths Cartographer owns entirely are excluded. A block it writes inside
// a user-owned file (CLAUDE.md, AGENTS.md) must NOT be excluded — that file is
// the user's, and telling git to ignore it would hide their own edits.
func EnsureWorkspaceExcluded(workspaceDir string, ownedPaths []string) error {
	infoDir := filepath.Join(workspaceDir, ".git", "info")
	if _, err := os.Stat(filepath.Join(workspaceDir, ".git")); err != nil {
		return nil
	}
	if err := os.MkdirAll(infoDir, 0o755); err != nil {
		return fmt.Errorf("provisioning: mkdir %s: %w", infoDir, err)
	}
	path := filepath.Join(infoDir, "exclude")

	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("provisioning: read %s: %w", path, err)
	}
	kept := stripExcludeBlock(string(existing))

	if len(ownedPaths) == 0 {
		return writeExclude(path, kept, nil)
	}
	lines := make([]string, 0, len(ownedPaths))
	for _, p := range ownedPaths {
		lines = append(lines, "/"+strings.TrimPrefix(filepath.ToSlash(p), "/"))
	}
	sort.Strings(lines)
	return writeExclude(path, kept, dedupe(lines))
}

// RemoveWorkspaceExclusions drops Cartographer's block entirely, for an unbind
// or a disconnect. The rest of the file is preserved verbatim.
func RemoveWorkspaceExclusions(workspaceDir string) error {
	return EnsureWorkspaceExcluded(workspaceDir, nil)
}

func writeExclude(path, kept string, lines []string) error {
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
	out := b.String()
	if strings.TrimSpace(out) == "" {
		// Nothing of ours and nothing of theirs: leave the file as it was
		// rather than creating an empty one.
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return nil
		}
	}
	return writeFileNoFollow(path, []byte(out), 0o644)
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
