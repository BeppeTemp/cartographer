package gitx

import (
	"errors"
	"fmt"
	"os/exec"
	"strconv"
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
	// Deliberate: a commit authored "<token id> <serverEmail>" keeps its author
	// here, so a token-authored commit a membership rule refuses is not repaired
	// by this automatic path. `cartographer kb reauthor` is the governed way.
	done, err := rewriteLinear(dir, upstream, "cartographer: committer is the writer (D950)", env,
		func(an, ae, _, _ string) (string, string, string, string) {
			if isServerIdentity(an, ae, serverName, serverEmail) && defName != "" && defEmail != "" && defEmail != ae {
				an, ae = defName, defEmail
			}
			return an, ae, an, ae
		})
	return len(done), err
}

// Rewritten is one commit a history rewrite replaced: its id before and after.
type Rewritten struct{ Old, New string }

// ReauthorCommits rewrites the local commits above upstream (upstream..HEAD,
// never a published one) so that name <email> is both the author and the
// committer of every one that is not already (D1063). Unlike RealignCommitters
// it replaces a token-authored commit's author too: it is the operator's
// explicit repair, not a sync step. It keeps messages (and so the
// Cartographer-Author, -Token and -Host trailers, the record of who really
// wrote), trees and both dates, and moves the checked-out branch with the same
// compare-and-swap update-ref, so a commit another process adds meanwhile
// makes it fail and nothing is lost. A merge or a root commit in the range
// returns ErrNotLinear and changes nothing. It returns the old → new ids of
// the commits it replaced; none, with a nil error, when there is nothing to do.
func ReauthorCommits(dir, upstream, name, email string, env ...string) ([]Rewritten, error) {
	if name == "" || email == "" {
		return nil, errors.New("gitx: reauthor needs a name and an email")
	}
	return rewriteLinear(dir, upstream, "cartographer: kb reauthor (D1063)", env,
		func(_, _, _, _ string) (string, string, string, string) { return name, email, name, email })
}

// rewriteLinear is the commit-by-commit loop both rewrites share: decide gives
// the author and committer (name, email) a commit should have from the ones it
// has; a commit that already has them, with an unmoved parent, is kept as is.
// HEAD is read once, first, and the final update-ref swaps from it.
func rewriteLinear(dir, upstream, reflogMsg string, env []string,
	decide func(an, ae, cn, ce string) (nan, nae, ncn, nce string)) ([]Rewritten, error) {
	oldHead, err := HeadSHA(dir)
	if err != nil {
		return nil, err
	}
	if out, err := runGit(dir, "rev-list", "--merges", upstream+".."+oldHead); err != nil {
		return nil, fmt.Errorf("git rev-list --merges: %w: %s", err, out)
	} else if strings.TrimSpace(out) != "" {
		return nil, ErrNotLinear
	}
	out, err := gitStdout(dir, "log", "--reverse", "--date=raw",
		"--format=%H%x00%P%x00%an%x00%ae%x00%ad%x00%cn%x00%ce%x00%cd", upstream+".."+oldHead)
	if err != nil {
		return nil, fmt.Errorf("git log %s..HEAD: %w", upstream, err)
	}
	rewritten := map[string]string{}
	var done []Rewritten
	newHead := ""
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		f := strings.Split(line, "\x00")
		if len(f) != 8 {
			continue
		}
		sha, parent := f[0], f[1]
		if parent == "" {
			// A root commit above upstream: nothing this rewrite may touch.
			return nil, ErrNotLinear
		}
		ad, cd := f[4], f[7]
		an, ae, cn, ce := decide(f[2], f[3], f[5], f[6])
		newParent, parentMoved := rewritten[parent]
		if !parentMoved {
			newParent = parent
		}
		if !parentMoved && an == f[2] && ae == f[3] && cn == f[5] && ce == f[6] {
			newHead = sha
			continue
		}
		body, err := gitStdout(dir, "cat-file", "commit", sha)
		if err != nil {
			return nil, fmt.Errorf("git cat-file commit %s: %w", sha, err)
		}
		_, msg, ok := strings.Cut(body, "\n\n")
		if !ok {
			return nil, fmt.Errorf("commit %s: no message", sha)
		}
		tree, err := gitStdout(dir, "rev-parse", sha+"^{tree}")
		if err != nil {
			return nil, fmt.Errorf("git rev-parse %s^{tree}: %w", sha, err)
		}
		cmd := exec.Command("git", "-C", dir, "commit-tree", strings.TrimSpace(tree), "-p", newParent, "-F", "-")
		cmd.Env = gitEnv(append(append([]string{}, env...),
			"GIT_AUTHOR_NAME="+an, "GIT_AUTHOR_EMAIL="+ae, "GIT_AUTHOR_DATE="+ad,
			"GIT_COMMITTER_NAME="+cn, "GIT_COMMITTER_EMAIL="+ce, "GIT_COMMITTER_DATE="+cd))
		cmd.Stdin = strings.NewReader(msg)
		created, err := cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("git commit-tree for %s: %w", sha, err)
		}
		newSHA := strings.TrimSpace(string(created))
		rewritten[sha] = newSHA
		newHead = newSHA
		done = append(done, Rewritten{Old: sha, New: newSHA})
	}
	if len(done) == 0 {
		return nil, nil
	}
	if out, err := runGitEnv(dir, env, "update-ref", "-m", reflogMsg, "HEAD", newHead, oldHead); err != nil {
		return nil, fmt.Errorf("git update-ref HEAD: %w: %s", err, out)
	}
	return done, nil
}

// CheckLinear returns ErrNotLinear when upstream..HEAD holds a merge or a root
// commit, nil otherwise: the check a rewrite makes before it starts, for a
// caller that wants to refuse it up front.
func CheckLinear(dir, upstream string) error {
	for _, args := range [][]string{{"--merges"}, {"--max-parents=0"}} {
		out, err := runGit(dir, append(append([]string{"rev-list"}, args...), upstream+"..HEAD")...)
		if err != nil {
			return fmt.Errorf("git rev-list %s: %w: %s", args[0], err, out)
		}
		if strings.TrimSpace(out) != "" {
			return ErrNotLinear
		}
	}
	return nil
}

// LocalCommit is one commit above the upstream, with who it is by.
type LocalCommit struct {
	SHA            string
	Author         string // name
	AuthorEmail    string
	Committer      string
	CommitterEmail string
	Subject        string
}

// LocalCommits lists upstream..HEAD, oldest first.
func LocalCommits(dir, upstream string) ([]LocalCommit, error) {
	out, err := gitStdout(dir, "log", "--reverse", "--format=%H%x00%an%x00%ae%x00%cn%x00%ce%x00%s", upstream+"..HEAD")
	if err != nil {
		return nil, fmt.Errorf("git log %s..HEAD: %w", upstream, err)
	}
	var cs []LocalCommit
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		f := strings.Split(line, "\x00")
		if len(f) != 6 {
			continue
		}
		cs = append(cs, LocalCommit{SHA: f[0], Author: f[1], AuthorEmail: f[2], Committer: f[3], CommitterEmail: f[4], Subject: f[5]})
	}
	return cs, nil
}

// UnpublishedCommitCount counts the commits in upstream..HEAD, and how many of
// them are not reachable from any remote-tracking ref: the two are equal when
// the range holds only commits that exist nowhere else.
func UnpublishedCommitCount(dir, upstream string) (inRange, unpublished int, err error) {
	count := func(args ...string) (int, error) {
		out, err := gitStdout(dir, append([]string{"rev-list", "--count"}, args...)...)
		if err != nil {
			return 0, fmt.Errorf("git rev-list: %w", err)
		}
		return strconv.Atoi(strings.TrimSpace(out))
	}
	if inRange, err = count(upstream + "..HEAD"); err != nil {
		return 0, 0, err
	}
	if unpublished, err = count(upstream+"..HEAD", "--not", "--remotes"); err != nil {
		return 0, 0, err
	}
	return inRange, unpublished, nil
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
