package gitx

import (
	"context"
	"errors"
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
	// The forges' own sentences for a credential that may read and not write:
	// GitHub's deploy key ("The key you are authenticating with has been marked
	// as read only."), GitLab ("You are not allowed to push code to this
	// project."; "...to protected branches..." is another refusal and does not
	// match) and Gitea ("User permission denied for writing."). No retry heals one.
	{"this credential cannot push to the remote: use a token or key with write access to the repository",
		[]string{"write access", "denied to", "marked as read only", "not allowed to push code to this project",
			"permission denied for writing", "key is read-only", "read-only deploy key"}, false},
	{"the remote's push rules declined the push and a retry will not change that",
		[]string{"hook declined"}, false},
	// A credential the remote rejected, or that git had none to offer, does not
	// heal between retries. HTTP 401 only: a bare 403 can be a rate limit.
	{"the credentials were rejected: check the token or credential helper for this forge",
		[]string{"authentication failed", "returned error: 401"}, false},
	{"git had no credentials for this forge and could not ask: configure a token or credential helper, or use an ssh remote",
		[]string{"could not read username"}, false},
}

// PermanentPushRejection reports whether the output of a failed push is a
// refusal that retrying only repeats, and the one-line remedy quoting the
// remote's own line: a credential without write access ("Permission to X
// denied", "write access", a read-only deploy key, GitLab's "not allowed to push
// code to this project", Gitea's "permission denied for writing"; a bare HTTP 403 is not enough,
// GitHub's secondary rate limit is one and heals), a credential the remote
// rejected ("Authentication failed", HTTP 401) or that git had none of to offer
// ("could not read Username": its own remedy), a repository rule or required
// signature (GitHub "GH013"), a file over the size limit ("GH001"), a server-side
// hook that declined. Only policy wording is permanent: a "[remote rejected]" for
// a lock, a ref update, an internal error or an unpacker error is a race or a
// server fault and is retried. Callers test the protected-branch and identity
// rejections first: their hook wording is also "hook declined". A
// non-fast-forward "[rejected]" is not matched either: it is retried through a
// rebase.
func PermanentPushRejection(output string) (remedy string, ok bool) {
	fixed, quote, ok := permanentPushRule(output)
	if !ok {
		return "", false
	}
	return fixed + quote, true
}

// PermanentPushRemedy is PermanentPushRejection for a surface that must not
// carry the remote's words (/health, status, doctor): only the rule's fixed
// sentence, "" when output is not a permanent refusal. It never returns any of
// output.
func PermanentPushRemedy(output string) string {
	fixed, _, _ := permanentPushRule(output)
	return fixed
}

// permanentPushRule classifies output: the matching rule's fixed sentence and the
// quote of the remote's line that triggered it.
func permanentPushRule(output string) (fixed, quote string, ok bool) {
	lower := strings.ToLower(output)
	for _, t := range transientPushRefusals {
		if strings.Contains(lower, t) {
			return "", "", false
		}
	}
	for _, r := range permanentPushRules {
		for _, p := range r.phrases {
			if lineWith(output, []string{p}, r.rejectedOnly) != "" {
				return r.remedy, quoteLine(output, r.phrases, r.rejectedOnly), true
			}
		}
	}
	// "Permission to X denied" and a 403 that says why. A 403 alone, or one that
	// mentions a rate limit, is not a verdict on the credential.
	if strings.Contains(lower, "permission to") && strings.Contains(lower, "denied") {
		return permanentPushRules[3].remedy, quoteLine(output, []string{"permission to"}, false), true
	}
	return "", "", false
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

// unreachablePhrases are the lines git and ssh print when the remote could not
// be reached at all: no DNS answer, no route, a refused or silent connection.
// An authentication or authorization failure is not among them: the remote
// answered, and its answer is a verdict on the credential.
var unreachablePhrases = []string{
	"could not resolve host", "could not resolve hostname", "temporary failure in name resolution",
	"name or service not known", "network is unreachable", "no route to host",
	"connection timed out", "operation timed out", "connection refused",
	"failed to connect to", "couldn't connect to server", "could not connect to server",
	// A connection that dies before any verdict: a reset, an ssh banner exchange
	// cut short, a dropped link.
	"network is down", "connection reset", "connection closed by remote host",
	"kex_exchange_identification",
}

// IsRemoteUnreachable reports whether err, from ProbePushAccess, says the
// remote did not answer: the probe's deadline passed, or git's own output names
// a network failure. A caller that probes for a verdict on a credential (doctor)
// skips the check on this and does not call the credential refused: the machine
// may simply be offline.
func IsRemoteUnreachable(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, ErrProbeTimeout) {
		return true
	}
	lower := strings.ToLower(err.Error())
	for _, p := range unreachablePhrases {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}

// credentialVerdictPhrases are the lines (besides PermanentPushRejection's) with
// which a remote that ANSWERED says the credential is not accepted for a push:
// a login it refused, no login at all (git could not ask for one), a key it
// rejected. A bare HTTP 403 is not among them: PermanentPushRejection leaves it
// out because a rate limit is one too.
var credentialVerdictPhrases = []string{
	"authentication failed", "invalid username or password", "returned error: 401",
	"could not read username", "could not read password", "terminal prompts disabled",
	"permission denied (publickey",
}

// IsPushCredentialVerdict reports whether err, from ProbePushAccess, is the
// remote's verdict on the credential: a permanent rejection
// (PermanentPushRejection: no write access, a read-only deploy key, a rule), or
// a login refused or missing. Anything else that is not IsRemoteUnreachable (a
// TLS or host-key failure, an HTTP 429 or 5xx, a connection cut mid-way) says
// nothing about the credential: the caller reports that the check could not be
// made, never that the push is refused. No credential at all ("could not read
// Username", terminal prompts disabled) counts as a verdict: the server would
// not push either.
func IsPushCredentialVerdict(err error) bool {
	if err == nil || errors.Is(err, ErrProbeTimeout) || errors.Is(err, context.Canceled) {
		return false
	}
	text := err.Error()
	if _, ok := PermanentPushRejection(text); ok {
		return true
	}
	lower := strings.ToLower(text)
	for _, p := range credentialVerdictPhrases {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
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
// remoteRemedy's, or ErrProbeTimeout (the deadline), or context.Canceled (the
// caller cancelled).
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
	if errors.Is(ctx.Err(), context.Canceled) {
		// The caller gave up (Ctrl-C): not a deadline, and no verdict of any kind.
		return fmt.Errorf("push probe of %s interrupted: %w", shown, context.Canceled)
	}
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
