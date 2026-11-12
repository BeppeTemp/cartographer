package gitx

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// transientPushRefusals are the "[remote rejected]" reasons that are races or
// server faults: they heal on their own and stay retried.
var transientPushRefusals = []string{"failed to update ref", "cannot lock ref", "failed to lock", "internal error", "unpacker error"}

var permanentPushRules = []struct {
	remedy  string
	phrases []string
	// rejectedOnly: the phrase counts only on a line that reports an error or a
	// rejection (GitHub also prints size warnings on a push that succeeds).
	rejectedOnly bool
}{
	{"file over the remote's size limit: remove it from the history, or track it with Git LFS if the remote supports it",
		[]string{"gh001", "exceeds github's file size limit of 100", "file size limit of 100"}, true},
	{"the remote requires signed commits: Cartographer does not sign them, so lift that rule for this branch or push with an account exempt from it",
		[]string{"signed commits", "verified signature", "required signature", "signing required"}, false},
	{"the remote's rules refuse this push: Cartographer pushes straight to the branch, so lift the rule, or set `branch` to one it does not cover",
		[]string{"gh013", "rule violations", "repository rule"}, false},
	{"this credential cannot push to the remote: use a token or key with write access to the repository",
		[]string{"write access", "denied to", "key is read-only", "read-only deploy key"}, false},
	{"the remote's push rules declined the push and a retry will not change that",
		[]string{"hook declined"}, false},
}

// PermanentPushRejection reports whether the output of a failed push is a
// refusal that retrying only repeats, and the one-line remedy quoting the
// remote's own line: a credential without write access ("Permission to X
// denied", "write access", a read-only deploy key; a bare HTTP 403 is not enough,
// GitHub's secondary rate limit is one and heals), a repository rule or required
// signature (GitHub "GH013"), a file over the size limit ("GH001"), a server-side
// hook that declined. Only policy wording is permanent: a "[remote rejected]" for
// a lock, a ref update, an internal error or an unpacker error is a race or a
// server fault and is retried. Callers test the protected-branch and identity
// rejections first: their hook wording is also "hook declined". A
// non-fast-forward "[rejected]" is not matched either: it is retried through a
// rebase.
func PermanentPushRejection(output string) (remedy string, ok bool) {
	lower := strings.ToLower(output)
	for _, t := range transientPushRefusals {
		if strings.Contains(lower, t) {
			return "", false
		}
	}
	for _, r := range permanentPushRules {
		for _, p := range r.phrases {
			if lineWith(output, []string{p}, r.rejectedOnly) != "" {
				return r.remedy + quoteLine(output, r.phrases, r.rejectedOnly), true
			}
		}
	}
	// "Permission to X denied" and a 403 that says why. A 403 alone, or one that
	// mentions a rate limit, is not a verdict on the credential.
	if strings.Contains(lower, "permission to") && strings.Contains(lower, "denied") {
		return permanentPushRules[3].remedy + quoteLine(output, []string{"permission to"}, false), true
	}
	return "", false
}

// lineWith is the first line of output holding one of phrases (case-folded); with
// rejectedOnly the line must also carry "error" or "rejected".
func lineWith(output string, phrases []string, rejectedOnly bool) string {
	for _, line := range strings.Split(output, "\n") {
		l := strings.ToLower(line)
		if rejectedOnly && !strings.Contains(l, "error") && !strings.Contains(l, "rejected") {
			continue
		}
		for _, p := range phrases {
			if strings.Contains(l, p) {
				return line
			}
		}
	}
	return ""
}

// quoteLine is ` (the remote said: "<line>")` for the first line of output that
// holds one of phrases, with git's "remote:" prefix dropped, or "".
func quoteLine(output string, phrases []string, rejectedOnly bool) string {
	line := lineWith(output, phrases, rejectedOnly)
	if line == "" {
		return ""
	}
	line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "remote:"))
	if len(line) > 200 {
		line = line[:200] + "..."
	}
	return fmt.Sprintf(" (the remote said: %q)", line)
}

// probeRefName is the branch the push probe aims at; a dry run never creates it.
const probeRefName = "refs/heads/cartographer-push-probe"

// ProbePushAccess asks remote whether this machine's credentials may push:
// "git push --dry-run" of a throw-away commit to a branch that does not exist,
// from a temporary repository. A dry run contacts the receiving side and writes
// nothing, so it catches a read-only credential (HTTP 403, a read-only deploy
// key) at once. A rule the remote evaluates only on a real push (required
// signatures, repository rules) is not seen here; SyncOut reports those on the
// first push. Failures come back with the permanent-rejection remedy, or
// remoteRemedy's, or ErrProbeTimeout.
func ProbePushAccess(ctx context.Context, remote string, env ...string) error {
	if err := ValidateRemote(remote); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp("", "cartographer-pushprobe-")
	if err != nil {
		return fmt.Errorf("probe %s: %w", RedactRemoteURL(remote), err)
	}
	defer os.RemoveAll(tmp)
	ident := []string{"GIT_AUTHOR_NAME=cartographer", "GIT_AUTHOR_EMAIL=probe@example.invalid",
		"GIT_COMMITTER_NAME=cartographer", "GIT_COMMITTER_EMAIL=probe@example.invalid"}
	if out, err := runGit(tmp, "init", "--quiet"); err != nil {
		return fmt.Errorf("git init: %w: %s", err, out)
	}
	tree, err := runGitEnv(tmp, ident, "hash-object", "-t", "tree", "-w", "--stdin")
	if err != nil {
		return fmt.Errorf("git hash-object: %w: %s", err, tree)
	}
	sha, err := runGitEnv(tmp, ident, "commit-tree", "-m", "push probe", strings.TrimSpace(tree))
	if err != nil {
		return fmt.Errorf("git commit-tree: %w: %s", err, sha)
	}
	cmd := netCommand(ctx, cloneEnv(remote, append(ident, env...)), "-C", tmp,
		"push", "--dry-run", "--quiet", "--", remote, strings.TrimSpace(sha)+":"+probeRefName)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	shown := RedactRemoteURL(remote)
	if ctx.Err() != nil {
		return fmt.Errorf("%w for %s: %v", ErrProbeTimeout, shown, ctx.Err())
	}
	output := RedactRemoteText(strings.TrimSpace(string(out)), remote)
	remedy := sshRemedy(ctx, remote, output)
	if remedy == "" {
		remedy, _ = PermanentPushRejection(output)
	}
	if remedy == "" {
		remedy = remoteRemedy(output)
	}
	if remedy != "" {
		return fmt.Errorf("git push --dry-run %s: %s\n%s", shown, remedy, output)
	}
	return fmt.Errorf("git push --dry-run %s: %w: %s", shown, redactedErr(err, remote), output)
}
