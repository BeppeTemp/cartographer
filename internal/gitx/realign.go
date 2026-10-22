package gitx

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// ErrNotLinear means the commits above the upstream hold a merge or a root
// commit, which RealignCommitters leaves alone: the caller may log why the
// forge will still see the old committers.
var ErrNotLinear = errors.New("gitx: the local commits are not a linear range")

// RealignCommitters rewrites the local commits above upstream (upstream..HEAD,
// never a published one) so that each one's committer is its author (D950).
// A rebase replays local commits with the committer of its environment, and a
// forge with a membership push rule checks committers too, so a commit whose
// writer is known must keep that writer as its committer. A commit authored by
// exactly the server identity (serverName <cartographer@any host>: no writer
// was known when it was made, possibly by another instance) is re-authored, and re-committed, as defName/defEmail when
// both are set, so a KB whose writer is registered later can still publish it.
// A commit authored "<token id> <serverEmail>" names the token that wrote it,
// is not that identity, and keeps its author.
//
// It only rewrites a linear range (a merge or a root commit in it makes it
// return ErrNotLinear and change nothing), keeps every commit's message, tree
// and dates, and moves the checked-out branch with a compare-and-swap
// update-ref, so the working tree and the index are not touched. It returns how
// many commits it rewrote; 0 with a nil error when there is nothing to do.
func RealignCommitters(dir, upstream, serverName, serverEmail, defName, defEmail string, env ...string) (int, error) {
	// HEAD is read once, first: the range, the rewrite and the compare-and-swap
	// all refer to this commit, so a commit another process adds meanwhile
	// makes the swap fail instead of being dropped from the branch.
	oldHead, err := HeadSHA(dir)
	if err != nil {
		return 0, err
	}
	if out, err := runGit(dir, "rev-list", "--merges", upstream+".."+oldHead); err != nil {
		return 0, fmt.Errorf("git rev-list --merges: %w: %s", err, out)
	} else if strings.TrimSpace(out) != "" {
		return 0, ErrNotLinear
	}
	out, err := gitStdout(dir, "log", "--reverse", "--date=raw",
		"--format=%H%x00%P%x00%an%x00%ae%x00%ad%x00%cn%x00%ce%x00%cd", upstream+".."+oldHead)
	if err != nil {
		return 0, fmt.Errorf("git log %s..HEAD: %w", upstream, err)
	}
	rewritten := map[string]string{}
	newHead, n := "", 0
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		f := strings.Split(line, "\x00")
		if len(f) != 8 {
			continue
		}
		sha, parent := f[0], f[1]
		if parent == "" {
			// A root commit above upstream: nothing this rewrite may touch.
			return 0, ErrNotLinear
		}
		an, ae, ad, cn, ce, cd := f[2], f[3], f[4], f[5], f[6], f[7]
		if isServerIdentity(an, ae, serverName, serverEmail) && defName != "" && defEmail != "" && defEmail != ae {
			an, ae = defName, defEmail
		}
		newParent, parentMoved := rewritten[parent]
		if !parentMoved {
			newParent = parent
		}
		if !parentMoved && an == f[2] && ae == f[3] && cn == an && ce == ae {
			newHead = sha
			continue
		}
		body, err := gitStdout(dir, "cat-file", "commit", sha)
		if err != nil {
			return 0, fmt.Errorf("git cat-file commit %s: %w", sha, err)
		}
		_, msg, ok := strings.Cut(body, "\n\n")
		if !ok {
			return 0, fmt.Errorf("commit %s: no message", sha)
		}
		tree, err := gitStdout(dir, "rev-parse", sha+"^{tree}")
		if err != nil {
			return 0, fmt.Errorf("git rev-parse %s^{tree}: %w", sha, err)
		}
		cmd := exec.Command("git", "-C", dir, "commit-tree", strings.TrimSpace(tree), "-p", newParent, "-F", "-")
		cmd.Env = gitEnv(append(append([]string{}, env...),
			"GIT_AUTHOR_NAME="+an, "GIT_AUTHOR_EMAIL="+ae, "GIT_AUTHOR_DATE="+ad,
			"GIT_COMMITTER_NAME="+an, "GIT_COMMITTER_EMAIL="+ae, "GIT_COMMITTER_DATE="+cd))
		cmd.Stdin = strings.NewReader(msg)
		created, err := cmd.Output()
		if err != nil {
			return 0, fmt.Errorf("git commit-tree for %s: %w", sha, err)
		}
		newSHA := strings.TrimSpace(string(created))
		rewritten[sha] = newSHA
		newHead = newSHA
		n++
	}
	if n == 0 {
		return 0, nil
	}
	if out, err := runGitEnv(dir, env, "update-ref", "-m", "cartographer: committer is the writer (D950)", "HEAD", newHead, oldHead); err != nil {
		return 0, fmt.Errorf("git update-ref HEAD: %w: %s", err, out)
	}
	return n, nil
}

// gitStdout runs git in dir and returns its stdout alone: a warning git
// prints on stderr must not end up in a commit message or a parsed field.
func gitStdout(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = gitEnv(nil)
	out, err := cmd.Output()
	return string(out), err
}

// isServerIdentity reports whether name <email> is the identity a Cartographer
// instance commits as when no writer is known: the server's name at
// cartographer@<some host>. The host is not compared with this machine's: the
// commit may come from another instance, or from a pod that has since been
// replaced (D950). A token-id author has another name and is never matched.
func isServerIdentity(name, email, serverName, serverEmail string) bool {
	if serverName == "" || name != serverName {
		return false
	}
	local, _, ok := strings.Cut(serverEmail, "@")
	if !ok || local == "" {
		return false
	}
	return strings.HasPrefix(email, local+"@")
}
