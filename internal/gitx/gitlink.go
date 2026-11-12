package gitx

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/procattr"
)

// A KB repository holds files, never other repositories. A directory that
// carries a `.git` of its own is a nested repository (a gitlink, index mode
// 160000, once staged): the server neither records one nor lets a git command
// of its own look inside one. Three layers:
//
//   - serverConfig, in gitEnv, makes every git command of the server ignore
//     submodules and run no fsmonitor of a nested repository;
//   - stageAll never stages a change to a gitlink, and the commit refuses to
//     record one (ErrGitlink);
//   - a gitlink the history already holds is left out of "git add -A", which
//     is the one command that inspects it whatever the configuration says.

// ErrGitlink is the error of a commit that would record a gitlink (index mode
// 160000, a nested git repository). Nothing is committed and the index holds
// no gitlink change any more.
var ErrGitlink = errors.New("refusing to record a nested git repository")

// GitlinkError names the paths a refused commit would have recorded as
// gitlinks. It satisfies errors.Is(err, ErrGitlink).
type GitlinkError struct {
	Paths []string
}

func (e *GitlinkError) Error() string {
	return fmt.Sprintf("%s (gitlink): %s: remove the .git directory inside it", ErrGitlink.Error(), strings.Join(e.Paths, ", "))
}

// Unwrap makes errors.Is(err, ErrGitlink) true.
func (e *GitlinkError) Unwrap() error { return ErrGitlink }

// serverConfig is the configuration every git command of the server runs with,
// ahead of any repository's own (command-line configuration wins over every
// file, and a child git process inherits it):
//
//   - diff.ignoreSubmodules=all: status, diff, stash and the like do not look
//     inside a nested repository to tell whether it is dirty;
//   - submodule.recurse and fetch.recurseSubmodules off: pull, fetch, checkout
//     and reset do not recurse into one, whatever the host's git config says;
//   - core.fsmonitor=false: no fsmonitor hook runs, not even in a git process
//     that does look inside a nested repository;
//   - commit.gpgsign=false, push.gpgSign=false and an empty core.hooksPath: the server's commits
//     do not depend on the user's commit-time configuration. A background
//     service has no terminal for a pinentry prompt and must not fail every
//     write on a hook written for the user's own commits. Signing is not
//     supported.
var serverConfig = []string{
	"diff.ignoreSubmodules=all",
	"submodule.recurse=false",
	"fetch.recurseSubmodules=false",
	"core.fsmonitor=false",
	"commit.gpgsign=false",
	"push.gpgSign=false",
	"core.hooksPath=",
}

// withServerConfig appends serverConfig to the GIT_CONFIG_PARAMETERS of env
// (the variable "git -c" sets, understood by every git version, unlike
// GIT_CONFIG_COUNT which needs 2.31), after the entries already there so that
// ours win.
func withServerConfig(env []string) []string {
	const key = "GIT_CONFIG_PARAMETERS="
	prior := ""
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, key); ok {
			prior = v
		}
	}
	var b strings.Builder
	b.WriteString(prior)
	for _, kv := range serverConfig {
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString("'" + kv + "'")
	}
	return append(env, key+b.String())
}

// gitlinkMode is the index mode of a gitlink.
const gitlinkMode = "160000"

// runGitOut runs git and returns its stdout alone: the NUL-separated listings
// below must not carry a warning git prints on stderr.
func runGitOut(dir string, env []string, args ...string) (string, error) {
	cmd := procattr.Hide(exec.Command("git", append([]string{"-C", dir}, args...)...))
	cmd.Env = gitEnv(env)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return string(out), nil
}

// indexGitlinks lists the paths the index records as gitlinks.
func indexGitlinks(dir string, env []string) ([]string, error) {
	out, err := runGitOut(dir, env, "ls-files", "--stage", "-z")
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, rec := range strings.Split(out, "\x00") {
		// "<mode> <object> <stage>\t<path>"
		meta, path, ok := strings.Cut(rec, "\t")
		if ok && strings.HasPrefix(meta, gitlinkMode+" ") {
			paths = append(paths, path)
		}
	}
	return paths, nil
}

// stagedGitlinks lists the paths the index records as a gitlink that base
// (a tree-ish) does not record the same way: new ones, and changed ones.
func stagedGitlinks(dir string, env []string, base string) ([]string, error) {
	out, err := runGitOut(dir, env, "diff-index", "--cached", "--raw", "-z", "--no-renames", "--ignore-submodules=none", base)
	if err != nil {
		return nil, err
	}
	var paths []string
	fields := strings.Split(out, "\x00")
	for i := 0; i+1 < len(fields); i += 2 {
		// ":<old mode> <new mode> <old object> <new object> <status>", then the path.
		meta := strings.Fields(fields[i])
		if len(meta) >= 2 && meta[1] == gitlinkMode {
			paths = append(paths, fields[i+1])
		}
	}
	return paths, nil
}

// newGitlinks lists the gitlinks the index would record that no parent of the
// commit-to-be records. The parent is HEAD, and in the middle of a merge also
// MERGE_HEAD: a gitlink the other side brought is not one the server wrote.
func newGitlinks(dir string, env []string) ([]string, error) {
	paths, err := stagedGitlinks(dir, env, "HEAD")
	if err != nil {
		if HeadUnborn(dir) {
			return indexGitlinks(dir, env) // no HEAD yet: every gitlink is new
		}
		return nil, err
	}
	if len(paths) == 0 {
		return nil, nil
	}
	if _, mergeErr := runGitOut(dir, env, "rev-parse", "--verify", "--quiet", "MERGE_HEAD"); mergeErr != nil {
		return paths, nil
	}
	notTheirs, err := stagedGitlinks(dir, env, "MERGE_HEAD")
	if err != nil {
		return nil, err
	}
	differs := make(map[string]bool, len(notTheirs))
	for _, p := range notTheirs {
		differs[p] = true
	}
	var fresh []string
	for _, p := range paths {
		if differs[p] {
			fresh = append(fresh, p)
		}
	}
	return fresh, nil
}

// refuseGitlinks returns a *GitlinkError when the index holds a gitlink the
// commit-to-be would record for the first time.
func refuseGitlinks(dir string, env []string) error {
	paths, err := newGitlinks(dir, env)
	if err != nil {
		return err
	}
	if len(paths) > 0 {
		return &GitlinkError{Paths: paths}
	}
	return nil
}

// stageAll stages every change of the working tree ("git add -A") except a
// change to a gitlink, and refuses a nested repository.
//
// A gitlink that is already in the index and whose directory still holds a
// .git stays out of the add: "git add -A" is the one command that looks inside
// a gitlink (to tell whether it is dirty) whatever diff.ignoreSubmodules says.
// One that no longer holds a .git is nothing to inspect, and git treats it as
// before (the removal of the folder is staged). A nested repository that is new
// to the index is staged by the add, found by newGitlinks and unstaged again,
// so the index never holds a gitlink the history does not.
func stageAll(dir string, env []string) error {
	known, err := indexGitlinks(dir, env)
	if err != nil {
		return err
	}
	args := []string{"add", "-A"}
	var inspected []string
	for _, p := range known {
		if _, statErr := os.Lstat(filepath.Join(dir, filepath.FromSlash(p), ".git")); statErr == nil {
			inspected = append(inspected, ":(exclude,literal)"+p)
		}
	}
	if len(inspected) > 0 {
		args = append(append(args, "--", "."), inspected...)
	}
	if out, err := runGitEnv(dir, env, args...); err != nil {
		return fmt.Errorf("git add -A: %w: %s", err, out)
	}
	paths, err := newGitlinks(dir, env)
	if err != nil {
		return err
	}
	if len(paths) == 0 {
		return nil
	}
	reset := append([]string{"--literal-pathspecs", "reset", "-q", "--"}, paths...)
	if out, err := runGitEnv(dir, env, reset...); err != nil {
		return fmt.Errorf("git reset (unstage %s): %w: %s", strings.Join(paths, ", "), err, out)
	}
	return &GitlinkError{Paths: paths}
}
