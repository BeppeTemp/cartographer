// Package gitx provides a git wrapper via os/exec for the Agentic Wiki.
// No external git library: only CLI invocation.
package gitx

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/BeppeTemp/cartographer/internal/procattr"
)

// ErrNothingToCommit signals that there was nothing to commit.
var ErrNothingToCommit = errors.New("nothing to commit")

// gitEnv is the environment of every git command gitx runs: the process
// environment, then extra, then LC_ALL=C. gitx recognises outcomes by git's
// English text ("nothing to commit", "CONFLICT", …), and a git built with
// gettext — Homebrew's, most Linux distributions' — prints them in the user's
// locale, where those checks silently stopped matching. Appended last, so it
// wins over both the process environment and extra.
//
// GIT_TERMINAL_PROMPT=0 sits before extra, so it holds for every command and
// still yields to a caller that says otherwise: a server started from a
// terminal must never leave git blocked on /dev/tty for a username or password
// nobody is there to type (#1004). The ssh counterpart is netEnv.
//
// The environment also carries serverConfig (gitlink.go): no command of the
// server looks inside a nested repository.
//
// The variables that pin a repository (repoPinVars) are dropped from the
// process environment: gitx always names the repository with -C, and an
// inherited GIT_DIR would send the command to another one. extra may set them
// again (CommitPaths sets GIT_INDEX_FILE).
func gitEnv(extra []string) []string {
	env := append(withoutRepoPins(os.Environ()), "GIT_TERMINAL_PROMPT=0")
	return append(withServerConfig(append(env, extra...)), "LC_ALL=C")
}

// Env is the environment a git command of the client runs with when it names
// the repository itself (-C): gitEnv without extras, so the variables that pin a
// repository (GIT_DIR, GIT_INDEX_FILE, GIT_WORK_TREE, ...) are dropped. A sync
// run from a git hook inherits them, and the command would answer for the
// repository running the hook instead of the one asked about.
func Env() []string { return gitEnv(nil) }

// repoPinVars are the environment variables that select a repository, its
// index or its object store instead of the one -C names.
var repoPinVars = []string{
	"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_OBJECT_DIRECTORY",
	"GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_COMMON_DIR", "GIT_NAMESPACE", "GIT_PREFIX",
}

// withoutRepoPins returns a copy of env without the repoPinVars.
func withoutRepoPins(env []string) []string {
	out := make([]string, 0, len(env))
next:
	for _, kv := range env {
		for _, v := range repoPinVars {
			if strings.HasPrefix(kv, v+"=") {
				continue next
			}
		}
		out = append(out, kv)
	}
	return out
}

// sshBatchCommand is the ssh invocation that never asks: no password or
// passphrase prompt, and a bounded connect.
const sshBatchCommand = "ssh -o BatchMode=yes -o ConnectTimeout=10"

// netEnv is gitEnv for a command that talks to the remote. On top of it, ssh
// runs in batch mode — unless GIT_SSH_COMMAND or GIT_SSH is already set by the caller or
// the process, or the repository configures core.sshCommand: GIT_SSH_COMMAND
// would override that, and an operator's proxy command or identity file is not
// ours to replace (the rule cloneEnv follows).
func netEnv(dir string, extra []string) []string {
	env := gitEnv(extra)
	if hasEnv(env, "GIT_SSH_COMMAND") || hasEnv(env, "GIT_SSH") {
		return env
	}
	// Bounded: this runs before every fetch and push, and must not become the
	// thing that hangs.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cfg := procattr.Hide(exec.CommandContext(ctx, "git", "-C", dir, "config", "--get", "core.sshCommand"))
	cfg.Env = env
	// Like netCommand: kill the group and bound the pipes, so a surviving child
	// (a Windows shim) cannot hold Output() past the 5s deadline.
	killGroupOnCancel(cfg)
	cfg.WaitDelay = time.Second
	if out, err := cfg.Output(); err == nil && strings.TrimSpace(string(out)) != "" {
		return env
	}
	return append(env, "GIT_SSH_COMMAND="+sshBatchCommand)
}

// netCommand builds every git command that talks to a remote. It is the one
// constructor on purpose: a deadline kills only `git` unless the process group
// goes with it (killGroupOnCancel), and without WaitDelay a surviving
// git-remote-http or ssh keeps the output pipe open so Wait blocks until the
// remote closes the connection. Hand-building these per function let Clone,
// lsRemoteProbe and ProbeRemoteContent forget both. args are the full git
// arguments (including any -C); env is the complete environment.
func netCommand(ctx context.Context, env []string, args ...string) *exec.Cmd {
	cmd := procattr.Hide(exec.CommandContext(ctx, "git", args...))
	cmd.Env = env
	killGroupOnCancel(cmd)
	cmd.WaitDelay = time.Second
	return cmd
}

// runGitNet runs a git command that talks to the remote, bounded by timeout
// with a process-group kill (the way Fetch is), under netEnv. A deadline hit
// comes back as a RemoteTimeoutError.
func runGitNet(dir, remote string, timeout time.Duration, env []string, args ...string) (string, error) {
	// netEnv first: its own bounded config probe is not network time and must
	// not eat the budget that bounds the command (#1870).
	env = netEnv(dir, env)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := netCommand(ctx, env, append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return string(out), &RemoteTimeoutError{Remote: RedactRemoteURL(remote), After: timeout}
	}
	if err != nil {
		// remote may be a name; git echoes its URL, and the credentials in it.
		return scrubRepoOutput(dir, string(out)), err
	}
	return string(out), err
}

// IsRepo checks whether the directory is a valid git repository.
func IsRepo(dir string) bool {
	cmd := procattr.Hide(exec.Command("git", "-C", dir, "rev-parse", "--git-dir"))
	cmd.Env = gitEnv(nil)
	return cmd.Run() == nil
}

// CheckRepo tells "this is not a repository" apart from "git refused to look":
// IsRepo folds both into false, so a repository git declines to open (a changed
// owner, "dubious ownership") reads as one that has nothing to commit. ok is
// false with a nil error only when git says there is no repository.
func CheckRepo(dir string) (ok bool, err error) {
	out, runErr := runGit(dir, "rev-parse", "--git-dir")
	if runErr == nil {
		return true, nil
	}
	if strings.Contains(out, "not a git repository") {
		return false, nil
	}
	return false, fmt.Errorf("git rev-parse --git-dir: %w: %s", runErr, strings.TrimSpace(out))
}

// ErrCloneTimeout reports that a clone was stopped by its context deadline
// rather than failing on its own. The caller owns the remedy, since only it
// knows the flag that changes the bound.
var ErrCloneTimeout = errors.New("git clone timed out")

// Clone clones remote into dest ("git clone <remote> <dest>"), bounded by ctx.
// dest must not yet exist (or must be empty); git creates it. env carries
// extra per-KB variables (e.g. GIT_SSH_COMMAND) layered on top of the process
// environment — see runGitEnv.
//
// The child never prompts: GIT_TERMINAL_PROMPT=0, plus an SSH batch mode and
// connect timeout for ssh remotes. Without those, git blocks indefinitely on a
// credential or host-key prompt when stdin is not a usable terminal, and the
// caller shows nothing while it does. Host-key POLICY is left alone:
// StrictHostKeyChecking=accept-new would trade a hang for a silent
// trust-on-first-use decision on an operator's machine (D173).
//
// Progress goes to stderr as it happens — a clone with no output is
// indistinguishable from a hang — while a copy is kept to translate a failure
// into something actionable.
func Clone(ctx context.Context, remote, dest string, env ...string) error {
	if err := ValidateRemote(remote); err != nil {
		return err
	}
	cmd := netCommand(ctx, cloneEnv(remote, env), "clone", "--progress", "--", remote, dest)
	var stderr strings.Builder
	// The progress shown live is scrubbed of the remote's credentials too.
	tee := &scrubWriter{w: os.Stderr, remote: remote}
	cmd.Stderr = io.MultiWriter(tee, &stderr)
	out, err := cmd.Output()
	tee.Flush()
	if err != nil {
		return cloneError(ctx, remote, dest, err, stderr.String()+string(out))
	}
	return nil
}

// AnonymousEnv is the environment of a clone that must carry no identity of the
// operator: no global or system git config (so no credential helper, extra
// header, proxy or URL rewrite), no askpass program, no prompt, and no redirect
// followed (a redirect would leave the host that was checked). Layered after the
// process environment, it wins over it. For clones of a locator written by a KB
// writer (D1262), never for KB sync.
func AnonymousEnv() []string {
	return []string{
		"GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_CONFIG_SYSTEM=" + os.DevNull, "GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_PARAMETERS=", "GIT_ASKPASS=", "SSH_ASKPASS=", "GIT_TERMINAL_PROMPT=0",
		"GIT_ALLOW_PROTOCOL=https",
		"GIT_CONFIG_COUNT=3",
		"GIT_CONFIG_KEY_0=credential.helper", "GIT_CONFIG_VALUE_0=",
		"GIT_CONFIG_KEY_1=http.followRedirects", "GIT_CONFIG_VALUE_1=false",
		"GIT_CONFIG_KEY_2=http.extraHeader", "GIT_CONFIG_VALUE_2=",
	}
}

// AnonymousReadEnv is AnonymousEnv for the commands run in an anonymous clone
// afterwards (HeadSHA, IsAncestor, TreeBlobs, DiffTree), with HOME and
// XDG_CONFIG_HOME also pointed at home, an empty directory the caller owns:
// GIT_CONFIG_GLOBAL needs git 2.32, and an older git would still read the
// operator's ~/.gitconfig and $XDG_CONFIG_HOME/git/config (D1262).
func AnonymousReadEnv(home string) []string {
	return append(AnonymousEnv(), "HOME="+home, "XDG_CONFIG_HOME="+home, "USERPROFILE="+home)
}

// CloneAnonymous clones an https remote with AnonymousEnv and without the blobs
// of history (--filter=blob:none: the trees are what a listing reads, and the
// checkout fetches the blobs of HEAD). The caller bounds it with ctx.
//
// HOME and XDG_CONFIG_HOME point at an empty directory made for the call
// (AnonymousReadEnv): GIT_CONFIG_GLOBAL needs git 2.32, and an older git would
// read the operator's ~/.gitconfig during the clone itself.
func CloneAnonymous(ctx context.Context, remote, dest string) error {
	home, err := os.MkdirTemp("", "cartographer-git-home-*")
	if err != nil {
		return fmt.Errorf("no temporary directory: %w", err)
	}
	defer os.RemoveAll(home)
	return cloneAnonymous(ctx, remote, dest, AnonymousReadEnv(home)...)
}

// cloneAnonymous: extra is appended last (a test lets plain http through to reach
// a local server; the exported form cannot).
func cloneAnonymous(ctx context.Context, remote, dest string, extra ...string) error {
	if err := ValidateRemote(remote); err != nil {
		return err
	}
	cmd := procattr.Hide(exec.CommandContext(ctx, "git", "clone", "--no-tags", "--filter=blob:none", "--", remote, dest))
	cmd.Env = gitEnv(append(AnonymousEnv(), extra...))
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if out, err := cmd.Output(); err != nil {
		return cloneError(ctx, remote, dest, err, stderr.String()+string(out))
	}
	return nil
}

// cloneEnv layers the non-interactive defaults on top of the process
// environment and the caller's own variables.
//
// GIT_SSH_COMMAND is set only for an ssh remote and only when nobody else
// provided one: an operator who configured a proxy command, an identity file
// or a jump host has said how to reach their forge, and overwriting that
// breaks a working setup to prevent a hypothetical one.
func cloneEnv(remote string, extra []string) []string {
	env := gitEnv(extra)
	// A redirect or a submodule must not reintroduce a transport helper
	// (ext::, fd::) the allow-list in ValidateRemote refused up front.
	if !hasEnv(env, "GIT_ALLOW_PROTOCOL") {
		env = append(env, "GIT_ALLOW_PROTOCOL="+strings.Join(allowedRemoteSchemes, ":"))
	}
	if !isSSHRemote(remote) || hasEnv(env, "GIT_SSH_COMMAND") {
		return env
	}
	return append(env, "GIT_SSH_COMMAND=ssh -o BatchMode=yes -o ConnectTimeout=10")
}

// allowedRemoteSchemes are the URL schemes a remote may use.
var allowedRemoteSchemes = []string{"https", "http", "ssh", "git", "file"}

var bracketedRE = regexp.MustCompile(`\[[^\]]*\]`)

// ValidateRemote refuses an operator-supplied remote that git would read as an
// option (leading "-") or run as a transport helper ("ext::<command>"). It is
// an allow-list of forms, not a deny-list: scp-like (git@host:path), a URL
// with one of allowedRemoteSchemes, or a plain local path.
func ValidateRemote(remote string) error {
	if remote == "" {
		return errors.New("remote must not be empty")
	}
	for _, r := range remote {
		if r < 0x20 || r == 0x7f {
			return errors.New("remote must not contain control characters")
		}
	}
	if strings.HasPrefix(remote, "-") {
		return errors.New("remote must not start with '-'")
	}
	head, _, _ := strings.Cut(remote, "/")
	// "::" inside [...] is an IPv6 literal (git@[::1]:repo.git), not a helper.
	if strings.Contains(bracketedRE.ReplaceAllString(head, "[]"), "::") {
		return fmt.Errorf("remote %q uses git's <transport>::<address> helper syntax, which is not accepted", remote)
	}
	if scheme, _, found := strings.Cut(remote, "://"); found {
		for _, ok := range allowedRemoteSchemes {
			if strings.EqualFold(scheme, ok) {
				return nil
			}
		}
		return fmt.Errorf("remote scheme %q is not accepted: use one of %s", scheme, strings.Join(allowedRemoteSchemes, ", "))
	}
	return nil
}

// hasEnv reports whether key is assigned anywhere in env.
func hasEnv(env []string, key string) bool {
	for _, kv := range env {
		if strings.HasPrefix(kv, key+"=") {
			return true
		}
	}
	return false
}

// isSSHRemote covers both remote spellings git accepts: URL-style
// (ssh://host/path) and scp-style (git@host:path). A local path or an
// https URL is neither.
func isSSHRemote(remote string) bool {
	if strings.HasPrefix(remote, "ssh://") {
		return true
	}
	if strings.Contains(remote, "://") {
		return false
	}
	host, _, found := strings.Cut(remote, ":")
	return found && !strings.Contains(host, "/") && !filepath.IsAbs(remote)
}

// cloneError turns a failed clone into a message an operator can act on. The
// raw git output is kept in every case: the recognised phrases below are a
// convenience, not a filter, and an unrecognised failure must not be reduced
// to "git clone failed".
func cloneError(ctx context.Context, remote, dest string, err error, output string) error {
	return gitNetError(ctx, remote, "clone", dest, err, output)
}

// gitNetError is cloneError for the network call verb ("clone", "fetch"); what
// is the argument the message names after the remote: the destination of a
// clone, the ref of a fetch.
func gitNetError(ctx context.Context, remote, verb, what string, err error, output string) error {
	// The remote may carry credentials (https://user:token@host/...): the
	// error text reaches logs and agent transcripts, so it names the
	// redacted form and git's output is scrubbed of the raw one.
	shown := RedactRemoteURL(remote)
	output = RedactRemoteText(output, remote)
	if ctx.Err() != nil {
		gerund := map[string]string{"clone": "cloning", "fetch": "fetching"}[verb]
		return fmt.Errorf("%w while %s %s: %v", ErrCloneTimeout, gerund, shown, ctx.Err())
	}
	// A clone names its remote, a fetch its remote and the ref; the clone's
	// destination stays out of the remedy line as it always has.
	label := "git " + verb + " " + shown
	if verb == "fetch" {
		label += " " + what
	}
	if remedy := remoteRemedy(output); remedy != "" {
		return fmt.Errorf("%s: %s\n%s", label, remedy, strings.TrimSpace(output))
	}
	return fmt.Errorf("git %s %s %s: %w: %s", verb, shown, what, redactedErr(err, remote), strings.TrimSpace(output))
}

// RemoteRemedy is remoteRemedy for a caller that holds a git error's text: the
// fixed one-line sentence for a known failure phrase in output, "" otherwise.
// It never returns any of output.
func RemoteRemedy(output string) string { return remoteRemedy(output) }

// remoteRemedy maps git's output for a failed network operation to the one
// line an operator can act on, or "" when none of the known phrases appears.
func remoteRemedy(output string) string {
	remedies := []struct{ match, remedy string }{
		{"could not resolve host", "the host name does not resolve: check the remote URL and this machine's DNS"},
		{"permission denied (publickey", "the forge rejected this machine's SSH key: check the key loaded in your agent has access to the repository"},
		{"host key verification failed", "the forge's host key is not in known_hosts: connect once with ssh to review and accept it, then retry"},
		{"repository not found", "the remote repository does not exist or this account cannot see it"},
		{"the project you were looking for could not be found", "the remote repository does not exist or this account cannot see it"},
		{"not found in the known hosts", "the forge's host key is not in known_hosts: connect once with ssh to review and accept it, then retry"},
		{"authentication failed", "the credentials were rejected: check the token or credential helper for this forge"},
		// HTTP 401 only: a bare 403 can be a rate limit, not a verdict on the credential.
		{"returned error: 401", "the credentials were rejected: check the token or credential helper for this forge"},
		{"x509:", "the remote's TLS certificate is not trusted by this machine: point SSL_CERT_FILE (or git's http.sslCAInfo) at the CA bundle that signs it"},
		{"ssl certificate problem", "the remote's TLS certificate is not trusted by this machine: point SSL_CERT_FILE (or git's http.sslCAInfo) at the CA bundle that signs it"},
		{"terminal prompts disabled", "git needed credentials and could not ask: configure a credential helper or use an ssh remote"},
	}
	lower := strings.ToLower(output)
	for _, r := range remedies {
		if strings.Contains(lower, r.match) {
			return r.remedy
		}
	}
	return ""
}

// ErrProbeTimeout reports that ProbeRemoteContent was stopped by its context
// deadline: the remote did not answer in time.
var ErrProbeTimeout = errors.New("git ls-remote timed out")

// lsRemoteProbe asks remote which refs it has ("git ls-remote --symref"),
// under the same non-interactive environment as Clone. It is the read-only
// question `cartographer setup` asks before changing anything: it proves the
// remote is reachable with this machine's credentials.
func lsRemoteProbe(ctx context.Context, remote string) (RemoteRefs, error) {
	if err := ValidateRemote(remote); err != nil {
		return RemoteRefs{}, err
	}
	cmd := netCommand(ctx, cloneEnv(remote, nil), "ls-remote", "--symref", "--", remote)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		shown := RedactRemoteURL(remote)
		if ctx.Err() != nil {
			return RemoteRefs{}, fmt.Errorf("%w for %s: %v", ErrProbeTimeout, shown, ctx.Err())
		}
		output := RedactRemoteText(strings.TrimSpace(stderr.String()), remote)
		remedy := sshRemedy(ctx, remote, output)
		if remedy == "" {
			remedy = remoteRemedy(output)
		}
		if remedy != "" {
			return RemoteRefs{}, fmt.Errorf("git ls-remote %s: %s\n%s", shown, remedy, output)
		}
		return RemoteRefs{}, fmt.Errorf("git ls-remote %s: %w: %s", shown, redactedErr(err, remote), output)
	}
	return parseLsRemoteSymref(string(out)), nil
}

// RemoteProbe is what `cartographer setup` learns about a remote before it
// plans: whether it has refs, and whether they are only a forge's initial
// commit (ForgeInitialized), which is an empty project to build a KB in, not
// a KB to mount (D710).
type RemoteProbe struct {
	HasRefs   bool
	ForgeInit bool
}

// ProbeRemoteContent reports whether the remote has refs and what they hold:
// the question that distinguishes a repository to create a KB in from one that
// already holds something to clone. When ls-remote shows refs on exactly one branch it fetches that branch two
// commits deep, without blobs, into a temporary repository (two, so a root commit is told
// from a longer history) and applies ForgeInitialized to it. Anything else
// with refs is content to mount.
func ProbeRemoteContent(ctx context.Context, remote string) (RemoteProbe, error) {
	refs, err := lsRemoteProbe(ctx, remote)
	if err != nil {
		return RemoteProbe{}, err
	}
	probe := RemoteProbe{HasRefs: refs.HasRefs}
	if !refs.HasRefs || len(refs.Branches) != 1 {
		return probe, nil
	}
	tmp, err := os.MkdirTemp("", "cartographer-probe-")
	if err != nil {
		return probe, fmt.Errorf("probe %s: %w", RedactRemoteURL(remote), err)
	}
	defer os.RemoveAll(tmp)
	if out, err := runGit(tmp, "init", "--quiet"); err != nil {
		return probe, fmt.Errorf("git init: %w: %s", err, out)
	}
	cmd := netCommand(ctx, cloneEnv(remote, nil), "-C", tmp, "fetch", "--quiet", "--depth", "2", "--filter=blob:none", "--no-tags", "--", remote, "refs/heads/"+refs.Branches[0])
	if out, err := cmd.CombinedOutput(); err != nil {
		if ctx.Err() != nil {
			return probe, fmt.Errorf("%w for %s: %v", ErrProbeTimeout, RedactRemoteURL(remote), ctx.Err())
		}
		return probe, fmt.Errorf("git fetch %s: %w: %s", RedactRemoteURL(remote), redactedErr(err, remote), RedactRemoteText(strings.TrimSpace(string(out)), remote))
	}
	_, ok, err := ForgeInitialized(tmp, "FETCH_HEAD")
	if err != nil {
		return probe, err
	}
	probe.ForgeInit = ok
	return probe, nil
}

// DefaultBranch is the branch a freshly initialized KB is created on. Sync
// compares the checked-out branch against the remote's default one (D264), so
// inheriting the host's init.defaultBranch would make a KB unusable on any
// machine configured for "master".
const DefaultBranch = "main"

// Init initializes a git repository in the directory, if one does not already exist.
// Pins the initial branch to DefaultBranch and configures merge.conflictStyle=zdiff3
// locally. An existing repository whose HEAD is unborn (no commit yet) is
// pinned too: that is what `git clone` of an empty remote leaves, on a branch
// named by the host's git version and init.defaultBranch (often "master"), and
// a KB bootstrapped from it must land where `kb create` does (D264).
// Repositories with commits keep whatever branch they are on.
func Init(dir string) error {
	return InitOnBranch(dir, DefaultBranch)
}

// InitOnBranch is Init with the unborn HEAD pinned to branch instead of
// DefaultBranch.
func InitOnBranch(dir, branch string) error {
	created := false
	if !IsRepo(dir) {
		if out, err := runGit(dir, "init"); err != nil {
			return fmt.Errorf("git init: %w: %s", err, out)
		}
		created = true
	}
	// A repository this call just created is unborn by construction: asking
	// HeadUnborn would spend three more git processes to learn that.
	if created || HeadUnborn(dir) {
		// The repository has no commits yet, so moving HEAD is safe and does
		// not depend on a git version that supports "init -b".
		if out, err := runGit(dir, "symbolic-ref", "HEAD", "refs/heads/"+branch); err != nil {
			return fmt.Errorf("git symbolic-ref HEAD: %w: %s", err, out)
		}
	}
	// Configure zdiff3 for more readable diffs during merge conflicts.
	if out, err := runGit(dir, "config", "merge.conflictStyle", "zdiff3"); err != nil {
		return fmt.Errorf("git config merge.conflictStyle: %w: %s", err, out)
	}
	return nil
}

// Commit runs "git add -A" followed by a commit with the given message and
// author identity. The committer identity is taken from env (typically
// GIT_COMMITTER_NAME/GIT_COMMITTER_EMAIL, assembled per-KB by the caller);
// if env carries no committer variables, git falls back to its own config/
// process environment, and when git cannot resolve one either the author
// becomes the committer (withCommitterFallback). If there is nothing to commit, returns
// ErrNothingToCommit (not a fatal error).
//
// A KB never records a gitlink (a nested git repository, index mode 160000):
// the staging leaves a change to one out, and when the working tree holds a
// nested repository that is new it returns a *GitlinkError (ErrGitlink),
// nothing committed and nothing of it left in the index.
func Commit(dir, message, authorName, authorEmail string, env ...string) error {
	if err := stageAll(dir, env); err != nil {
		return err
	}
	return commit(dir, message, authorName, authorEmail, env...)
}

// CommitPaths creates one commit containing changes to paths only. It uses a
// temporary index seeded from HEAD, so pre-existing staged or unstaged work in
// the caller's real index is neither changed nor accidentally committed.
func CommitPaths(dir string, paths []string, message, authorName, authorEmail string, env ...string) error {
	if len(paths) == 0 {
		return ErrNothingToCommit
	}

	index, err := os.CreateTemp("", "cartographer-git-index-*")
	if err != nil {
		return fmt.Errorf("create temporary git index: %w", err)
	}
	indexPath := index.Name()
	if err := index.Close(); err != nil {
		os.Remove(indexPath)
		return fmt.Errorf("close temporary git index: %w", err)
	}
	// Git expects a missing index file, not an empty one.
	if err := os.Remove(indexPath); err != nil {
		return fmt.Errorf("remove temporary git index: %w", err)
	}
	defer os.Remove(indexPath)

	commitEnv := append(append([]string{}, env...), "GIT_INDEX_FILE="+indexPath)
	if out, err := runGitEnv(dir, commitEnv, "read-tree", "HEAD"); err != nil {
		return fmt.Errorf("git read-tree HEAD: %w: %s", err, out)
	}
	args := append([]string{"--literal-pathspecs", "add", "--"}, paths...)
	if out, err := runGitEnv(dir, commitEnv, args...); err != nil {
		return fmt.Errorf("git add paths: %w: %s", err, out)
	}
	return commit(dir, message, authorName, authorEmail, commitEnv...)
}

// ResetPaths resets the index entries of paths to HEAD ("git reset -q -- paths"),
// leaving the working tree alone. CommitPaths commits through a temporary index,
// so the real one still holds what it held before: after the commit, this brings
// the paths it committed back in line with HEAD, and git status shows them clean.
func ResetPaths(dir string, paths ...string) error {
	if len(paths) == 0 {
		return nil
	}
	if out, err := runGit(dir, append([]string{"--literal-pathspecs", "reset", "-q", "--"}, paths...)...); err != nil {
		return fmt.Errorf("git reset -- paths: %w: %s%s", err, out, indexLockHint(dir, out))
	}
	return nil
}

func commit(dir, message, authorName, authorEmail string, env ...string) error {
	// Every commit passes here, whatever staged its index (the real one, the
	// temporary one of CommitPaths, a command run by hand): a gitlink new to
	// the history is refused, not recorded.
	if err := refuseGitlinks(dir, env); err != nil {
		return err
	}
	args := []string{"commit"}
	if authorName != "" && authorEmail != "" {
		args = append(args, "--author", fmt.Sprintf("%s <%s>", authorName, authorEmail))
	}
	args = append(args, "-m", message)
	env = withCommitterFallback(dir, env, authorName, authorEmail)
	started := time.Now()
	out, err := runGitEnvTimeout(dir, env, CommitTimeout, args...)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("git commit: no answer after %s, stopped: a hook or a prompt of the host's git is waiting for input%s",
				CommitTimeout, clearKilledCommitLock(dir, env, started))
		}
		// git commit exits with code 1 when there is nothing to commit.
		if strings.Contains(out, "nothing to commit") ||
			strings.Contains(out, "nothing added to commit") {
			return ErrNothingToCommit
		}
		return fmt.Errorf("git commit: %w: %s%s", err, out, indexLockHint(dir, out))
	}
	return nil
}

// withCommitterFallback returns env extended with GIT_COMMITTER_NAME/EMAIL set
// to the author when an author is supplied and git cannot resolve a committer
// on its own. --author sets only the author: on a machine with no user.email
// (a fresh Git for Windows install) git still exits 128 with "Committer
// identity unknown", so every commit Cartographer makes would fail (D265).
//
// The fallback applies only when `git var GIT_COMMITTER_IDENT` fails, rather
// than always making the committer the author: a committer the operator did
// configure (per-KB env, GIT_COMMITTER_* in the process, or user.* in git
// config) is recorded unchanged, and the extra probe is skipped when env
// already names both committer variables.
func withCommitterFallback(dir string, env []string, authorName, authorEmail string) []string {
	if authorName == "" || authorEmail == "" {
		return env
	}
	if hasEnv(env, "GIT_COMMITTER_NAME") && hasEnv(env, "GIT_COMMITTER_EMAIL") {
		return env
	}
	if _, err := runGitEnv(dir, env, "var", "GIT_COMMITTER_IDENT"); err == nil {
		return env
	}
	return append(append([]string{}, env...),
		"GIT_COMMITTER_NAME="+authorName, "GIT_COMMITTER_EMAIL="+authorEmail)
}

// AuthorIdent returns the identity resolved by Git itself (including local,
// global and ambient GIT_AUTHOR_* configuration). Empty values mean Git could
// not resolve a usable identity.
func AuthorIdent(dir string, env ...string) (name, email string, err error) {
	out, err := runGitEnv(dir, env, "var", "GIT_AUTHOR_IDENT")
	if err != nil {
		return "", "", fmt.Errorf("git var GIT_AUTHOR_IDENT: %w: %s", err, out)
	}
	ident := strings.TrimSpace(out)
	start, end := strings.LastIndex(ident, " <"), strings.LastIndex(ident, ">")
	if start < 1 || end <= start+2 {
		return "", "", fmt.Errorf("git var GIT_AUTHOR_IDENT: malformed identity %q", ident)
	}
	return strings.TrimSpace(ident[:start]), ident[start+2 : end], nil
}

// AheadCount returns commits reachable from HEAD but not origin/<branch>.
// known is false when the remote-tracking ref is absent, so callers never
// mistake an unavailable comparison for zero unpushed commits.
func AheadCount(dir, remote, branch string) (count int, known bool, err error) {
	ahead, _, known, err := AheadBehind(dir, remote, branch)
	return ahead, known, err
}

// AheadBehind returns how many commits HEAD is ahead of and behind
// <remote>/<branch> in one rev-list call. Behind is relative to the
// remote-tracking ref as the last fetch left it: no network is touched.
// known is false when the ref is absent or branch is empty.
func AheadBehind(dir, remote, branch string) (ahead, behind int, known bool, err error) {
	if branch == "" {
		return 0, 0, false, nil
	}
	ref := remote + "/" + branch
	if _, err := runGit(dir, "rev-parse", "--verify", "refs/remotes/"+ref); err != nil {
		return 0, 0, false, nil
	}
	out, err := runGit(dir, "rev-list", "--left-right", "--count", ref+"...HEAD")
	if err != nil {
		return 0, 0, false, fmt.Errorf("git rev-list %s...HEAD: %w: %s", ref, err, out)
	}
	if _, err := fmt.Sscanf(strings.TrimSpace(out), "%d %d", &behind, &ahead); err != nil {
		return 0, 0, false, fmt.Errorf("git rev-list count %q: %w", out, err)
	}
	return ahead, behind, true, nil
}

// HeadUnborn reports whether dir is a git repository whose HEAD names a branch
// with no commit yet: a fresh `git init`, or a clone of an empty remote.
func HeadUnborn(dir string) bool {
	if !IsRepo(dir) {
		return false
	}
	if _, err := runGit(dir, "rev-parse", "--verify", "--quiet", "HEAD"); err == nil {
		return false
	}
	// A detached or corrupt HEAD fails rev-parse too; only a symbolic HEAD
	// pointing at a missing branch is "unborn".
	_, err := runGit(dir, "symbolic-ref", "--quiet", "HEAD")
	return err == nil
}

// RemoteRefs is what a remote advertises, as far as the canonical-branch
// check needs it (D264).
type RemoteRefs struct {
	// HasRefs is false for an empty remote: no branch, no tag.
	HasRefs bool
	// Default is the branch the remote's HEAD points at, or "" when the
	// remote does not advertise one (empty, or HEAD naming a branch that does
	// not exist — common on a self-hosted `git init --bare` whose HEAD is
	// "master" while only "main" was pushed).
	Default string
	// Branches are the remote's branch names.
	Branches []string
}

// HasBranch reports whether the remote has branch.
func (r RemoteRefs) HasBranch(branch string) bool {
	for _, b := range r.Branches {
		if b == branch {
			return true
		}
	}
	return false
}

// LsRemote asks remote for its branches and default branch
// ("git ls-remote --symref"), bounded by FetchTimeout like Fetch. It asks the
// remote every time rather than reading refs/remotes/<remote>/HEAD: that ref
// is written once by clone and never refreshed by fetch, so it goes stale when
// the forge's default branch changes (and is absent in a repository that was
// init'ed and then given a remote). On success, and when the default branch
// has a remote-tracking ref, <remote>/HEAD is re-pointed at it (local only,
// best-effort) so an operator's `git log origin/HEAD` agrees with Cartographer.
// env carries extra per-KB variables (e.g. GIT_SSH_COMMAND) — see runGitEnv.
func LsRemote(dir, remote string, env ...string) (RemoteRefs, error) {
	if remote == "" {
		remote = "origin"
	}
	env = netEnv(dir, env) // before the deadline: the probe is not network time (#1870)
	ctx, cancel := context.WithTimeout(context.Background(), FetchTimeout)
	defer cancel()
	cmd := netCommand(ctx, env, "-C", dir, "ls-remote", "--symref", remote)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if ctx.Err() == context.DeadlineExceeded {
		return RemoteRefs{}, fmt.Errorf("git ls-remote %s: %w", RedactRemoteURL(remote), &RemoteTimeoutError{Remote: RedactRemoteURL(remote), After: FetchTimeout})
	}
	if err != nil {
		return RemoteRefs{}, fmt.Errorf("git ls-remote %s: %w: %s", RedactRemoteURL(remote), redactedErr(err, remote), RedactRemoteText(scrubRepoOutput(dir, strings.TrimSpace(stderr.String())), remote))
	}
	refs := parseLsRemoteSymref(string(out))
	if refs.Default != "" && RemoteBranchExists(dir, remote, refs.Default) {
		_, _ = runGit(dir, "symbolic-ref", "refs/remotes/"+remote+"/HEAD", "refs/remotes/"+remote+"/"+refs.Default)
	}
	return refs, nil
}

// parseLsRemoteSymref parses `git ls-remote --symref` output:
//
//	ref: refs/heads/main	HEAD
//	<sha>	HEAD
//	<sha>	refs/heads/main
func parseLsRemoteSymref(out string) RemoteRefs {
	var r RemoteRefs
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		r.HasRefs = true
		fields := strings.Fields(line)
		if len(fields) == 3 && fields[0] == "ref:" && fields[2] == "HEAD" {
			r.Default = strings.TrimPrefix(fields[1], "refs/heads/")
			continue
		}
		if len(fields) == 2 && strings.HasPrefix(fields[1], "refs/heads/") {
			r.Branches = append(r.Branches, strings.TrimPrefix(fields[1], "refs/heads/"))
		}
	}
	// A HEAD symref naming a branch the remote does not have is no default.
	if r.Default != "" && !r.HasBranch(r.Default) {
		r.Default = ""
	}
	return r
}

// IgnoredFiles lists the files under path (absolute, inside the repository at
// dir) that the repository's ignore rules exclude, as git prints them
// (relative to dir). Empty when there are none.
func IgnoredFiles(dir, path string) ([]string, error) {
	// -z: a name with a non-ASCII byte, a tab or a newline comes back intact
	// instead of quoted ("citt\303\240.png").
	out, errOut, err := runGitStdin(dir, "", "--literal-pathspecs", "ls-files", "-z", "--others", "--ignored", "--exclude-standard", "--", path)
	if err != nil {
		return nil, fmt.Errorf("git ls-files --ignored: %w: %s", err, errOut)
	}
	return splitNUL(out), nil
}

// splitNUL splits NUL-terminated git output, dropping empty fields.
func splitNUL(out string) []string {
	var items []string
	for _, f := range strings.Split(out, "\x00") {
		if f != "" {
			items = append(items, f)
		}
	}
	return items
}

// IgnoredPaths returns those of paths (relative to dir, which need not exist
// yet) that the repository's ignore rules would exclude from a commit. It asks
// git with --no-index, so a tracked path is judged by the rules alone. The paths
// go to git on stdin, NUL-separated, so the call does not grow with them (argv
// has a hard limit) and a name is never quoted or split.
func IgnoredPaths(dir string, paths []string) ([]string, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	out, errOut, err := runGitStdin(dir, strings.Join(paths, "\x00")+"\x00", "check-ignore", "--no-index", "--stdin", "-z")
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() == 1 {
			return nil, nil // exit 1: none of them is ignored
		}
		return nil, fmt.Errorf("git check-ignore: %w: %s", err, errOut)
	}
	var ignored []string
	for _, p := range strings.Split(out, "\x00") {
		if p != "" {
			ignored = append(ignored, p)
		}
	}
	return ignored, nil
}

// HeadSHA returns the SHA of the HEAD commit.
//
// env is layered on the process environment like runGitEnv's; the poll passes
// AnonymousReadEnv for a clone of a locator a KB writer chose.
func HeadSHA(dir string, env ...string) (string, error) {
	out, err := runGitEnv(dir, env, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("git rev-parse HEAD: %w: %s", err, out)
	}
	return strings.TrimSpace(out), nil
}

// runGit runs a git command in the specified directory, inheriting the
// process environment unchanged, and returns the combined output.
func runGit(dir string, args ...string) (string, error) {
	return runGitEnv(dir, nil, args...)
}

// runGitEnv runs a git command in the specified directory with extra
// environment variables layered on top of the process environment
// (gitEnv: process environment, then env, then LC_ALL=C): entries in env take precedence
// over the process environment (later entries win on duplicate keys), the
// inverse of setupGitSSH's "process environment wins" rule for the global
// GIT_SSH_COMMAND fallback — see D46. A nil/empty env
// behaves exactly like runGit.
func runGitEnv(dir string, env []string, args ...string) (string, error) {
	runCount.Add(1)
	fullArgs := append([]string{"-C", dir}, args...)
	cmd := procattr.Hide(exec.Command("git", fullArgs...))
	cmd.Env = gitEnv(env)
	out, err := cmd.CombinedOutput()
	if err != nil {
		// The output reaches errors, logs and sync_status: git echoes a
		// remote's URL, and the credentials in it, in its failures.
		return scrubRepoOutput(dir, string(out)), err
	}
	return string(out), err
}

// CommitTimeout bounds one git commit. Server commits are isolated from the
// user's commit-time configuration (serverConfig), so one that takes this long
// is stuck on something the isolation does not cover; it is stopped, process
// group included, rather than holding the KB's lock. A variable so tests can
// shorten it.
var CommitTimeout = 60 * time.Second

// clearKilledCommitLock runs after a commit was killed by CommitTimeout. The
// kill is a SIGKILL: git cannot remove the index.lock it holds, and every later
// write would fail on it. The caller holds the KB's git lock, so no other
// server git runs; a lock whose mtime is at or after the commit's start (to the
// second, for coarse filesystems) is the killed commit's and is removed. An older
// lock belongs to someone else (a git run by hand, an editor): it is kept and
// named in the returned sentence. "" when there was nothing to clear.
func clearKilledCommitLock(dir string, env []string, started time.Time) string {
	lock := indexLockPath(dir, env)
	if lock == "" {
		return ""
	}
	fi, err := os.Stat(lock)
	if err != nil {
		return ""
	}
	if fi.ModTime().Before(started.Truncate(time.Second)) {
		return fmt.Sprintf("; %s predates this commit and was left in place: remove it once no git is using it", lock)
	}
	if err := os.Remove(lock); err != nil {
		return fmt.Sprintf("; the killed commit left %s and it could not be removed: %v", lock, err)
	}
	return ""
}

// indexLockPath is the lock file of the index the commit used: the temporary
// index of GIT_INDEX_FILE when env names one (CommitPaths), else the repository's.
func indexLockPath(dir string, env []string) string {
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "GIT_INDEX_FILE="); ok && v != "" {
			return v + ".lock"
		}
	}
	out, err := runGit(dir, "rev-parse", "--git-path", "index.lock")
	if err != nil {
		return ""
	}
	p := strings.TrimSpace(out)
	if p == "" {
		return ""
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(dir, p)
	}
	return p
}

// runGitEnvTimeout is runGitEnv bounded by timeout; the error wraps
// context.DeadlineExceeded when the bound was hit.
func runGitEnvTimeout(dir string, env []string, timeout time.Duration, args ...string) (string, error) {
	runCount.Add(1)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := netCommand(ctx, gitEnv(env), append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return scrubRepoOutput(dir, string(out)), ctx.Err()
	}
	if err != nil {
		return scrubRepoOutput(dir, string(out)), err
	}
	return string(out), err
}

// runCount counts the git processes runGitEnv started. Tests only: it is how
// one proves an operation starts a constant number of processes (RunCount).
var runCount atomic.Int64

// RunCount returns how many git processes runGitEnv has started so far.
func RunCount() int64 { return runCount.Load() }

// runGitStdin is runGit with stdin fed to the command: for a list too long for
// argv. It returns stdout alone, and stderr as the second value: the caller
// parses a NUL-separated list, and a warning git prints while it works (on
// Windows, "unable to access '<dir>/.gitignore': Filename too long" for a path
// past MAX_PATH) would otherwise be glued to one of its names.
func runGitStdin(dir, stdin string, args ...string) (stdout, stderr string, err error) {
	cmd := procattr.Hide(exec.Command("git", append([]string{"-C", dir}, args...)...))
	cmd.Env = gitEnv(nil)
	cmd.Stdin = strings.NewReader(stdin)
	var errBuf strings.Builder
	cmd.Stderr = &errBuf
	out, err := cmd.Output()
	return string(out), errBuf.String(), err
}

// ErrRebaseConflict signals that a pull --rebase hit a conflict and was aborted.
var ErrRebaseConflict = errors.New("gitx: rebase conflict")

// RebaseConflictError carries structured details about a rebase conflict.
// It satisfies errors.Is(err, ErrRebaseConflict) so existing callers are unaffected.
type RebaseConflictError struct {
	Files     []string // git-relative paths of unmerged files (e.g. "data/notes/c.md")
	LocalSHA  string   // HEAD SHA captured before the rebase started
	RemoteSHA string   // SHA of <remote>/<branch> after fetch
	Remote    string   // remote name (e.g. "origin")
	Branch    string   // branch name (e.g. "main")
}

// Error implements the error interface.
func (e *RebaseConflictError) Error() string {
	return fmt.Sprintf("%s: pull --rebase %s %s: %d conflicting file(s): %s",
		ErrRebaseConflict.Error(), e.Remote, e.Branch, len(e.Files), strings.Join(e.Files, ", "))
}

// Is makes errors.Is(err, ErrRebaseConflict) return true for *RebaseConflictError values,
// preserving backward compatibility with all callers that use the sentinel.
func (e *RebaseConflictError) Is(target error) bool {
	return target == ErrRebaseConflict
}

// PullRebaseAutostash runs "git pull --rebase --autostash <remote> <branch>".
// It talks to the network without FetchTimeout or a process-group kill, so the
// KB sync path does not use it: after Fetch it rebases locally with
// RebaseOntoAutostash (one round-trip, bounded).
// If the operation encounters a rebase conflict, it collects the list of unmerged
// files and the relevant SHAs, runs "git rebase --abort" (best-effort), and returns
// a *RebaseConflictError. errors.Is(err, ErrRebaseConflict) remains true for it.
// Other errors (e.g. remote unreachable) are returned as-is without abort.
// env carries extra per-KB variables (e.g. GIT_SSH_COMMAND) — see runGitEnv.
func PullRebaseAutostash(dir, remote, branch string, env ...string) error {
	if remote == "" {
		remote = "origin"
	}

	// Capture local HEAD before the rebase so callers can record the pre-conflict state.
	localSHA, _ := HeadSHA(dir)

	out, err := runGitEnv(dir, env, "pull", "--rebase", "--autostash", remote, branch)
	if err == nil {
		return nil
	}
	// A leftover index.lock is not a conflict, though git words it "error: could
	// not detach HEAD": name the lock before the conflict markers are read.
	if hint := indexLockHint(dir, out); hint != "" {
		hint += abortRebaseIfStarted(dir)
		return fmt.Errorf("git pull --rebase --autostash %s %s: %w: %s%s", RedactRemoteURL(remote), branch, redactedErr(err, remote), RedactRemoteText(out, remote), hint)
	}
	// Detect rebase conflict: output contains "CONFLICT" or "rebase" stall markers.
	if strings.Contains(out, "CONFLICT") ||
		strings.Contains(out, "could not apply") ||
		strings.Contains(out, "error: could not") {

		// Collect unmerged files before aborting.
		conflictFiles, _ := UnmergedFiles(dir)

		// Capture remote SHA.
		var remoteSHA string
		if shaOut, serr := runGit(dir, "rev-parse", remote+"/"+branch); serr == nil {
			remoteSHA = strings.TrimSpace(shaOut)
		}

		// Best-effort abort so the working tree is left clean.
		_, _ = runGit(dir, "rebase", "--abort")

		return &RebaseConflictError{
			Files:     conflictFiles,
			LocalSHA:  localSHA,
			RemoteSHA: remoteSHA,
			Remote:    RedactRemoteURL(remote),
			Branch:    branch,
		}
	}
	return fmt.Errorf("git pull --rebase --autostash %s %s: %w: %s", RedactRemoteURL(remote), branch, redactedErr(err, remote), RedactRemoteText(out, remote))
}

// Branch returns the current branch name, or "" if detached HEAD.
func Branch(dir string) (string, error) {
	out, err := branchMemo.do(dir, func() (string, error) {
		return runGit(dir, "symbolic-ref", "--short", "HEAD")
	})
	if err != nil {
		// detached HEAD — not an error for the caller
		return "", nil
	}
	return strings.TrimSpace(out), nil
}

// Checkout checks out an existing branch.
func Checkout(dir, branch string) error {
	out, err := runGit(dir, "switch", "--end-of-options", branch)
	if err != nil {
		return fmt.Errorf("git checkout %s: %w: %s", branch, err, out)
	}
	return nil
}

// Fetch runs git fetch for a given remote (default "origin"). Returns error on
// failure. env carries extra per-KB variables (e.g. GIT_SSH_COMMAND) — see runGitEnv.
func Fetch(dir, remote string, env ...string) error {
	return fetchWith(dir, remote, nil, env)
}

// FetchNoTags is Fetch without following tags: refs/tags/* is left as it was,
// for a caller that may have to undo the fetch (kb attach removes the remote
// again, which drops refs/remotes/<remote>/* but not tags).
func FetchNoTags(dir, remote string, env ...string) error {
	return fetchWith(dir, remote, []string{"--no-tags"}, env)
}

func fetchWith(dir, remote string, opts, env []string) error {
	if remote == "" {
		remote = "origin"
	}
	env = netEnv(dir, env) // before the deadline: the probe is not network time (#1870)
	ctx, cancel := context.WithTimeout(context.Background(), FetchTimeout)
	defer cancel()
	args := append([]string{"-C", dir, "fetch"}, opts...)
	cmd := netCommand(ctx, env, append(args, remote)...)
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("git fetch %s: %w", RedactRemoteURL(remote), &RemoteTimeoutError{Remote: RedactRemoteURL(remote), After: FetchTimeout})
	}
	if err != nil {
		return fmt.Errorf("git fetch %s: %w: %s", RedactRemoteURL(remote), redactedErr(err, remote), RedactRemoteText(scrubRepoOutput(dir, string(out)), remote))
	}
	return nil
}

// FetchTimeout bounds one git fetch. Without it a remote whose host drops
// packets (a VPN that is down) holds the fetch for the OS TCP connect timeout
// -- about 75s on macOS -- and every tool call waiting on the KB's git lock
// with it, past the client's 30s HTTP timeout (#348). The fetch is the
// operation that meets an unreachable remote first; pull and push run after a
// fetch has just succeeded. The deadline starts after netEnv, so one fetch
// takes at most FetchTimeout plus the separate core.sshCommand probe, bounded
// at 5s plus a 1s pipe wait (skipped when GIT_SSH_COMMAND or GIT_SSH is set).
// A variable so tests can shorten it.
var FetchTimeout = 15 * time.Second

// RemoteTimeoutError reports a git remote that did not answer in time. It is
// a distinct type so a caller can say "the remote is unreachable" rather than
// pass on a bare exit status.
type RemoteTimeoutError struct {
	Remote string
	After  time.Duration
}

func (e *RemoteTimeoutError) Error() string {
	return fmt.Sprintf("git remote %q did not answer within %s (unreachable or blocked)", e.Remote, e.After)
}

// RemoteBranchExists reports whether refs/remotes/<remote>/<branch> exists.
// Call Fetch first when the answer must reflect the forge's current state.
func RemoteBranchExists(dir, remote, branch string) bool {
	if remote == "" {
		remote = "origin"
	}
	_, err := runGit(dir, "rev-parse", "--verify", "refs/remotes/"+remote+"/"+branch)
	return err == nil
}

// RemoteTrackingBranches lists the branch names under refs/remotes/<remote>/
// (HEAD excluded), from the local clone: no network.
func RemoteTrackingBranches(dir, remote string) ([]string, error) {
	if remote == "" {
		remote = "origin"
	}
	prefix := "refs/remotes/" + remote + "/"
	out, err := runGit(dir, "for-each-ref", "--format=%(refname)", prefix)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, l := range strings.Split(out, "\n") {
		if b := strings.TrimPrefix(strings.TrimSpace(l), prefix); b != "" && b != "HEAD" && strings.HasPrefix(strings.TrimSpace(l), prefix) {
			names = append(names, b)
		}
	}
	return names, nil
}

// CheckoutNewBranch checks out branch at start. It never resets an existing
// branch; callers that see an existing branch must make an explicit, safe
// decision instead of discarding history.
func CheckoutNewBranch(dir, branch, start string, env ...string) error {
	out, err := runGitEnv(dir, env, "switch", "-c", branch, "--end-of-options", start)
	if err != nil {
		return fmt.Errorf("git checkout -b %s %s: %w: %s", branch, start, err, out)
	}
	return nil
}

// IsAncestor reports whether ancestor is reachable from descendant.
// env as in HeadSHA.
func IsAncestor(dir, ancestor, descendant string, env ...string) (bool, error) {
	out, err := runGitEnv(dir, env, "merge-base", "--is-ancestor", "--end-of-options", ancestor, descendant)
	if err == nil {
		return true, nil
	}
	if strings.TrimSpace(out) == "" {
		return false, nil
	}
	return false, fmt.Errorf("git merge-base --is-ancestor %s %s: %w: %s", ancestor, descendant, err, out)
}

// Ancestors returns the commits reachable from rev, rev itself included, that
// touch one of paths ("git rev-list --full-history rev -- paths"): one process
// answers "is this commit an ancestor of rev" for every commit of those paths'
// history, where IsAncestor costs one each, and the walk is bounded by the
// paths' history, not the repository's. --full-history keeps every commit that
// differs from a parent on a path, a superset of what `git log -- path` lists,
// so a commit of that history is in the set exactly when it is an ancestor. The
// "--" after rev keeps a file named like the rev from making it ambiguous.
func Ancestors(dir, rev string, paths []string) (map[string]bool, error) {
	if len(paths) == 0 {
		return map[string]bool{}, nil
	}
	args := append([]string{"--literal-pathspecs", "rev-list", "--full-history", "--end-of-options", rev, "--"}, paths...)
	out, err := runGit(dir, args...)
	if err != nil {
		return nil, fmt.Errorf("git rev-list %s: %w: %s", rev, err, out)
	}
	set := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			set[line] = true
		}
	}
	return set, nil
}

// RebaseOntoAutostash rebases the checked-out branch onto remote/branch. It
// preserves a dirty tree with Git's autostash and returns the same structured
// conflict error used by PullRebaseAutostash.
func RebaseOntoAutostash(dir, remote, branch string, env ...string) error {
	if remote == "" {
		remote = "origin"
	}
	localSHA, _ := HeadSHA(dir)
	target := remote + "/" + branch
	out, err := runGitEnv(dir, env, "rebase", "--autostash", target)
	if err == nil {
		return nil
	}
	if hint := indexLockHint(dir, out); hint != "" {
		hint += abortRebaseIfStarted(dir)
		return fmt.Errorf("git rebase --autostash %s: %w: %s%s", target, err, out, hint)
	}
	if strings.Contains(out, "CONFLICT") || strings.Contains(out, "could not apply") || strings.Contains(out, "error: could not") {
		files, _ := UnmergedFiles(dir)
		remoteSHA, _ := HeadSHAAt(dir, target)
		_, _ = runGit(dir, "rebase", "--abort")
		return &RebaseConflictError{Files: files, LocalSHA: localSHA, RemoteSHA: remoteSHA, Remote: RedactRemoteURL(remote), Branch: branch}
	}
	return fmt.Errorf("git rebase --autostash %s: %w: %s", target, err, out)
}

// TreePaths returns the paths of every file in ref's tree, slash-separated
// ("git ls-tree -r --name-only -z <ref>").
func TreePaths(dir, ref string) ([]string, error) {
	cmd := procattr.Hide(exec.Command("git", "-C", dir, "ls-tree", "-r", "--name-only", "-z", ref))
	cmd.Env = gitEnv(nil)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-tree %s: %w", ref, err)
	}
	var paths []string
	for _, p := range strings.Split(string(out), "\x00") {
		if p != "" {
			paths = append(paths, p)
		}
	}
	return paths, nil
}

// TreeBlob is one entry of a tree listing: the file mode ("100644", "120000"
// for a symlink), the blob id and the path.
type TreeBlob struct{ Mode, ID, Path string }

// TreeBlobs lists the blobs of ref's tree with their ids ("git ls-tree -r -z").
// Submodule entries are left out. DiffNameStatus is not used for this family of
// reads: it splits on whitespace and has no -z, so a path with a space or a
// quoted non-ASCII name would be mangled (D1262).
// env as in HeadSHA.
func TreeBlobs(dir, ref string, env ...string) ([]TreeBlob, error) {
	cmd := procattr.Hide(exec.Command("git", "-C", dir, "ls-tree", "-r", "-z", "--end-of-options", ref))
	cmd.Env = gitEnv(env)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-tree %s: %w", ref, err)
	}
	var blobs []TreeBlob
	for _, rec := range strings.Split(string(out), "\x00") {
		meta, path, ok := strings.Cut(rec, "\t")
		f := strings.Fields(meta)
		if !ok || len(f) != 3 || f[1] != "blob" {
			continue
		}
		blobs = append(blobs, TreeBlob{Mode: f[0], ID: f[2], Path: path})
	}
	return blobs, nil
}

// TreeChange is one path that differs between two commits. Status is A, M, D
// or T; Mode and ID are the new ones (ID "" for D).
type TreeChange struct{ Status, Mode, ID, Path string }

// DiffTree lists the paths that differ between two commits ("git diff-tree -r
// -z --no-renames --raw"): a rename is a D plus an A. -z for the reason given at
// TreeBlobs.
// env as in HeadSHA.
func DiffTree(dir, from, to string, env ...string) ([]TreeChange, error) {
	cmd := procattr.Hide(exec.Command("git", "-C", dir, "diff-tree", "-r", "-z", "--no-renames", "--raw", "--end-of-options", from, to))
	cmd.Env = gitEnv(env)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git diff-tree %s %s: %w", from, to, err)
	}
	parts := strings.Split(string(out), "\x00")
	var changes []TreeChange
	for i := 0; i+1 < len(parts); i += 2 {
		f := strings.Fields(parts[i]) // :oldmode newmode oldid newid status
		if len(f) != 5 || parts[i+1] == "" {
			return nil, fmt.Errorf("git diff-tree %s %s: unexpected output", from, to)
		}
		c := TreeChange{Status: f[4][:1], Mode: f[1], ID: f[3], Path: parts[i+1]}
		if c.Status == "D" {
			c.ID = ""
		}
		changes = append(changes, c)
	}
	return changes, nil
}

// ForgeInitialized reports whether ref is what a forge writes when it creates
// a project with "initialize with README": exactly one commit whose tree holds
// only top-level README/LICENSE/COPYING/.gitignore/.gitattributes files
// (isForgeInitPath). It returns the tree's paths either way. A git failure is
// an error, never a silent false. "Exactly one branch" is the caller's
// condition (RemoteRefs.Branches).
func ForgeInitialized(dir, ref string) (paths []string, ok bool, err error) {
	out, err := runGit(dir, "rev-list", "--count", "--end-of-options", ref)
	if err != nil {
		return nil, false, fmt.Errorf("git rev-list --count %s: %w: %s", ref, err, out)
	}
	if strings.TrimSpace(out) != "1" {
		return nil, false, nil
	}
	paths, err = TreePaths(dir, ref)
	if err != nil {
		return nil, false, err
	}
	if len(paths) == 0 {
		return paths, false, nil
	}
	for _, p := range paths {
		if !isForgeInitPath(p) {
			return paths, false, nil
		}
	}
	return paths, true, nil
}

// isForgeInitPath reports whether p is a top-level file a forge seeds a new
// project with: README, LICENSE, LICENCE or COPYING (each optionally followed
// by "." and any extension), .gitignore or .gitattributes, case-insensitive.
func isForgeInitPath(p string) bool {
	if p == "" || strings.Contains(p, "/") {
		return false
	}
	base := strings.ToLower(p)
	if base == ".gitignore" || base == ".gitattributes" {
		return true
	}
	for _, stem := range []string{"readme", "license", "licence", "copying"} {
		if base == stem || strings.HasPrefix(base, stem+".") {
			return true
		}
	}
	return false
}

// RepoNameFromURL is the repository name of a git remote URL: its last path
// segment without a trailing slash or ".git" suffix. It handles https://,
// ssh:// and scp-style (git@host:group/sub/name.git) URLs, and a local path.
func RepoNameFromURL(remote string) string {
	s := strings.TrimRight(strings.TrimSpace(remote), "/")
	s = strings.TrimSuffix(s, ".git")
	s = strings.TrimRight(s, "/")
	if i := strings.LastIndexAny(s, "/:"); i >= 0 {
		s = s[i+1:]
	}
	return s
}

// ForgeReadmePath picks the README a forge's initial commit carries out of its
// top-level paths: the one file named README.md (case-insensitive). ok is
// false when there is none, or when another README file (README, README.rst,
// a second README.md in another case) sits beside it, so nothing is ever
// decided for an ambiguous tree.
func ForgeReadmePath(paths []string) (path string, ok bool) {
	n := 0
	for _, p := range paths {
		lower := strings.ToLower(p)
		if lower == "readme" || strings.HasPrefix(lower, "readme.") {
			n++
			if lower == "readme.md" {
				path = p
			}
		}
	}
	return path, n == 1 && path != ""
}

const (
	gitlabIntro      = "To make it easy for you to get started with GitLab"
	gitlabGettingHdr = "## Getting started"
	gitlabAddHdr     = "## Add your files"
)

// ForgeBoilerplateReadme reports whether text, the README a forge's initial
// commit holds in a repository called repoName, is that forge's untouched
// boilerplate, and the one-line project description it carries when it has
// one. Only two shapes are recognised, whole-file, after normalising a BOM,
// CRLF line ends, trailing blanks and surrounding blank lines:
//
//   - `# <repoName>`, optionally followed by one description line (GitHub,
//     Gitea and Forgejo), with or without a blank line between;
//   - `# <repoName>` and GitLab's "Getting started" template, identified by
//     its three fixed markers (no description is carried over).
//
// Anything else is the operator's text and reports false.
func ForgeBoilerplateReadme(repoName, text string) (boilerplate bool, description string) {
	lines := normalizeReadme(text)
	if repoName == "" || len(lines) == 0 || !strings.EqualFold(lines[0], "# "+repoName) {
		return false, ""
	}
	switch rest := lines[1:]; {
	case len(rest) == 0:
		return true, ""
	case len(rest) == 1 && rest[0] != "":
		return true, rest[0]
	case len(rest) == 2 && rest[0] == "" && rest[1] != "":
		return true, rest[1]
	}
	var intro, getting, add bool
	for _, l := range lines[1:] {
		intro = intro || strings.Contains(l, gitlabIntro)
		getting = getting || l == gitlabGettingHdr
		add = add || l == gitlabAddHdr
	}
	return intro && getting && add, ""
}

func normalizeReadme(text string) []string {
	text = strings.TrimPrefix(text, "\ufeff")
	text = strings.ReplaceAll(text, "\r\n", "\n")
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t\r")
	}
	for len(lines) > 0 && lines[0] == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// ForgeReadmeBoilerplate reads the README at ref:path and applies
// ForgeBoilerplateReadme with the repository name of remote.
func ForgeReadmeBoilerplate(dir, ref, path, remote string) (boilerplate bool, description string, err error) {
	text, err := ShowFile(dir, ref, path)
	if err != nil {
		return false, "", err
	}
	boilerplate, description = ForgeBoilerplateReadme(RepoNameFromURL(remote), text)
	return boilerplate, description, nil
}

// RenameBranch renames the checked-out branch ("git branch -m <name>").
func RenameBranch(dir, name string) error {
	out, err := runGit(dir, "branch", "-m", "--end-of-options", name)
	if err != nil {
		return fmt.Errorf("git branch -m %s: %w: %s", name, err, out)
	}
	return nil
}

// HeadSHAAt returns the commit SHA for ref.
func HeadSHAAt(dir, ref string) (string, error) {
	out, err := runGit(dir, "rev-parse", ref)
	if err != nil {
		return "", fmt.Errorf("git rev-parse %s: %w: %s", ref, err, out)
	}
	return strings.TrimSpace(out), nil
}

// ResetHardTo resets a clean working tree to ref. It is deliberately narrow:
// callers check cleanliness before using it.
func ResetHardTo(dir, ref string, env ...string) error {
	// git before 2.44 refuses "reset --hard --end-of-options <ref>" ("must come
	// before non-option arguments"), so a ref that could be read as an option is
	// refused here and the trailing "--" ends the revision.
	if strings.HasPrefix(ref, "-") {
		return fmt.Errorf("git reset --hard %s: ref starts with '-'", ref)
	}
	out, err := runGitEnv(dir, env, "reset", "--hard", ref, "--")
	if err != nil {
		return fmt.Errorf("git reset --hard %s: %w: %s%s", ref, err, out, indexLockHint(dir, out))
	}
	return nil
}

// Push pushes the current branch to the remote. Never force-pushes.
// Returns error on non-fast-forward (caller should rebase and retry).
// env carries extra per-KB variables (e.g. GIT_SSH_COMMAND) — see runGitEnv.
func Push(dir, remote, branch string, env ...string) error {
	out, err := runGitNet(dir, remote, PushTimeout, env, "push", "--", remote, branch)
	if err != nil {
		return pushError(fmt.Sprintf("git push %s %s", RedactRemoteURL(remote), branch), err, out)
	}
	return nil
}

// pushError words a failed push: a timeout as is, a known remote failure with
// its remedy (the raw output kept), anything else as the bare status.
func pushError(what string, err error, out string) error {
	var rte *RemoteTimeoutError
	if errors.As(err, &rte) {
		return fmt.Errorf("%s: %w", what, err)
	}
	if remedy := remoteRemedy(out); remedy != "" {
		return fmt.Errorf("%s: %s\n%s: %w", what, remedy, strings.TrimSpace(out), err)
	}
	return fmt.Errorf("%s: %w: %s", what, err, out)
}

// PushTimeout bounds one git push. A push carries data, so it gets a longer
// bound than FetchTimeout, but it is bounded all the same: an unbounded push
// holds the KB's git lock, and every tool call behind it, for as long as the
// remote stalls. As for FetchTimeout, the deadline starts after netEnv: a push
// takes at most PushTimeout plus the core.sshCommand probe, bounded at 5s plus
// a 1s pipe wait. A variable so tests can shorten it.
var PushTimeout = 60 * time.Second

// PushSetUpstream pushes branch to remote and sets it as the branch's
// upstream ("git push -u"). Used once per KB: when its origin is first
// attached (kb create --remote), or by the local-profile SyncOut for the very
// first push of DefaultBranch to an empty remote (D264). Every other sync path
// in internal/kb uses Push, which must not touch tracking configuration.
// env carries extra per-KB variables (e.g. GIT_SSH_COMMAND) — see runGitEnv.
func PushSetUpstream(dir, remote, branch string, env ...string) error {
	out, err := runGitNet(dir, remote, PushTimeout, env, "push", "-u", "--", remote, branch)
	if err != nil {
		return pushError(fmt.Sprintf("git push -u %s %s", RedactRemoteURL(remote), branch), err, out)
	}
	return nil
}

// Rebase state kinds returned by RebaseInProgress.
const (
	// RebaseStateNormal: a real rebase is in progress (a head-name or todo is
	// present), so someone is mid-operation and git owns the worktree.
	RebaseStateNormal = "rebase"
	// RebaseStateOrphanAutostash: the rebase directory exists but holds no
	// rebase — only an autostash. Observed after a process was killed mid
	// `pull --rebase --autostash`: from then on every write failed with "there is
	// already a rebase-merge directory", and the KB was frozen until the
	// directory was removed by hand (D155).
	RebaseStateOrphanAutostash = "orphan-autostash"
)

// gitStatePath is where git keeps the per-worktree state file or directory name
// for the repository rooted at dir, without starting a git process and without
// git's upward discovery: a folder with no `.git` of its own, or a bogus one,
// must not be judged by the repository enclosing it. `.git` a directory: the state
// is inside it. `.git` a file (a linked worktree or a submodule): its `gitdir:`
// line names the git dir that holds the state. Anything else yields a path that
// does not exist, i.e. no state.
func gitStatePath(dir, name string) string {
	dotGit := filepath.Join(dir, ".git")
	if fi, err := os.Stat(dotGit); err == nil && fi.Mode().IsRegular() {
		if gd := gitFileDir(dotGit); gd != "" {
			return filepath.Join(gd, name)
		}
	}
	return filepath.Join(dotGit, name)
}

// gitFileDir is the directory the `gitdir: <path>` line of the gitfile at path
// names (a relative path is relative to the gitfile's folder); "" when the file
// holds no such line.
func gitFileDir(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		v, ok := strings.CutPrefix(strings.TrimSpace(line), "gitdir:")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		if v == "" {
			return ""
		}
		v = filepath.FromSlash(v)
		if !filepath.IsAbs(v) {
			v = filepath.Join(filepath.Dir(path), v)
		}
		return v
	}
	return ""
}

// RebaseInProgress reports whether dir has a rebase state directory and what kind
// it is. A caller must not run git in the worktree while this returns true: the
// point is to refuse with an actionable message instead of letting git fail with
// one that names no way out.
func RebaseInProgress(dir string) (kind string, ok bool) {
	for _, name := range []string{"rebase-merge", "rebase-apply"} {
		d := gitStatePath(dir, name)
		if _, err := os.Stat(d); err != nil {
			continue
		}
		for _, marker := range []string{"head-name", "git-rebase-todo", "onto", "next"} {
			if _, err := os.Stat(filepath.Join(d, marker)); err == nil {
				return RebaseStateNormal, true
			}
		}
		return RebaseStateOrphanAutostash, true
	}
	return "", false
}

// RebaseStateDir returns the path of the rebase state directory in dir, or "".
// Named in the operator-facing message so the remedy points at a real path.
func RebaseStateDir(dir string) string {
	for _, name := range []string{"rebase-merge", "rebase-apply"} {
		d := gitStatePath(dir, name)
		if _, err := os.Stat(d); err == nil {
			return d
		}
	}
	return ""
}

// ShowFile returns the contents of path at the given git ref ("git show <ref>:<path>").
// Used to materialise the "ours"/"theirs" sides of a conflict by content, which avoids
// the inverted --ours/--theirs semantics that apply during a rebase.
func ShowFile(dir, ref, path string) (string, error) {
	out, err := runGit(dir, "show", ref+":"+path)
	if err != nil {
		return "", fmt.Errorf("git show %s:%s: %w: %s", ref, path, err, out)
	}
	return out, nil
}

// ShowFileIfExists is ShowFile that tells a path absent from ref (exists=false,
// nil error) from every other failure (a bad ref, a damaged repository), which
// is returned: an error must never be read as absence.
func ShowFileIfExists(dir, ref, path string) (text string, exists bool, err error) {
	out, err := runGit(dir, "--literal-pathspecs", "ls-tree", "--name-only", "-z", ref, "--", path)
	if err != nil {
		return "", false, fmt.Errorf("git ls-tree %s -- %s: %w: %s", ref, path, err, out)
	}
	if out == "" {
		return "", false, nil
	}
	text, err = ShowFile(dir, ref, path)
	if err != nil {
		return "", false, err
	}
	return text, true, nil
}

// MergeNoCommitNoFFEnv runs "git merge --no-commit --no-ff <ref>" and reports
// whether the merge produced conflicts. A clean merge (conflicted=false) is left
// staged but uncommitted; a conflicting merge (conflicted=true) leaves the
// conflicting files unmerged for the caller to resolve. Any other failure is
// returned as err. env is layered on the git process environment. A merge needs a committer identity even with
// --no-commit, so a caller inside a KB passes kb.ServerGitEnv() or it fails
// with "empty ident name" on a host with no git identity.
func MergeNoCommitNoFFEnv(dir, ref string, env ...string) (conflicted bool, err error) {
	out, gerr := runGitEnv(dir, env, "merge", "--no-commit", "--no-ff", "--end-of-options", ref)
	if gerr == nil {
		return false, nil
	}
	if strings.Contains(out, "CONFLICT") || strings.Contains(out, "Automatic merge failed") {
		return true, nil
	}
	return false, fmt.Errorf("git merge --no-commit --no-ff %s: %w: %s%s", ref, gerr, out, indexLockHint(dir, out))
}

// MergeAbort aborts an in-progress merge ("git merge --abort").
func MergeAbort(dir string) error {
	out, err := runGit(dir, "merge", "--abort")
	if err != nil {
		return fmt.Errorf("git merge --abort: %w: %s", err, out)
	}
	return nil
}

// UnmergedFiles returns the git-relative paths of files with unresolved conflicts
// (git diff --name-only --diff-filter=U).
func UnmergedFiles(dir string) ([]string, error) {
	out, errOut, err := runGitStdin(dir, "", "diff", "--name-only", "--diff-filter=U", "-z")
	if err != nil {
		return nil, fmt.Errorf("git diff --diff-filter=U: %w: %s", err, errOut)
	}
	return splitNUL(out), nil
}

// ListFiles returns the files git tracks in dir, plus the untracked ones it
// would add at the next commit (not ignored): "git ls-files --cached --others
// --exclude-standard", slash-separated and relative to dir. An error means dir
// is not inside a git work tree (or git is missing).
func ListFiles(dir string) ([]string, error) {
	cmd := procattr.Hide(exec.Command("git", "-C", dir, "ls-files", "-z", "--cached", "--others", "--exclude-standard"))
	cmd.Env = gitEnv(nil)
	out, err := cmd.Output() // stdout only: a warning on stderr is not a path
	if err != nil {
		return nil, fmt.Errorf("git ls-files: %w", err)
	}
	var files []string
	seen := map[string]bool{}
	for _, f := range strings.Split(string(out), "\x00") {
		if f != "" && !seen[f] {
			seen[f] = true
			files = append(files, f)
		}
	}
	return files, nil
}

// TrackedPaths returns which of paths (taken literally, never as pathspecs) (slash-separated, relative to dir) git
// tracks in the index.
func TrackedPaths(dir string, paths ...string) (map[string]bool, error) {
	out := map[string]bool{}
	if len(paths) == 0 {
		return out, nil
	}
	cmd := procattr.Hide(exec.Command("git", append([]string{"--literal-pathspecs", "-C", dir, "ls-files", "-z", "--"}, paths...)...))
	cmd.Env = gitEnv(nil)
	b, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-files: %w", err)
	}
	for _, f := range strings.Split(string(b), "\x00") {
		if f != "" {
			out[f] = true
		}
	}
	return out, nil
}

// pathspecVars are the environment variables that change how git reads a
// pathspec. An inherited GIT_LITERAL_PATHSPECS=1 (an editor's `git
// --literal-pathspecs commit` exports it to its hooks) makes git read the magic
// of `:(literal,icase)X` as part of a file name.
var pathspecVars = []string{
	"GIT_LITERAL_PATHSPECS", "GIT_GLOB_PATHSPECS", "GIT_NOGLOB_PATHSPECS", "GIT_ICASE_PATHSPECS",
}

// withoutPathspecVars returns a copy of env without the pathspecVars.
func withoutPathspecVars(env []string) []string {
	out := make([]string, 0, len(env))
next:
	for _, kv := range env {
		for _, v := range pathspecVars {
			if strings.HasPrefix(kv, v+"=") {
				continue next
			}
		}
		out = append(out, kv)
	}
	return out
}

// TrackedPathsFold is TrackedPaths for a caller that must not be fooled by
// spelling: it returns which of paths (slash-separated, relative to dir, taken
// literally) git tracks in the index under any capitalisation, keyed by the
// path as asked. A case-insensitive filesystem (macOS and Windows by default)
// writes `CLAUDE.md` into the file a repository tracks as `claude.md`, and an
// exact question about `CLAUDE.md` answers "no". Asking per pathspec
// (`:(literal,icase)`, which the global --literal-pathspecs would turn off) folds
// the whole path, directories included, on every filesystem: on a case-sensitive
// one the answer errs toward "tracked", the safe side for a caller that withholds
// something from a shared file. git folds ASCII letters only: a non-ASCII
// directory that differs from the tracked one only by case is not matched.
//
// Each path is asked on its own, with the GIT_*_PATHSPECS variables dropped, and
// a name git lists counts when it has as many components as the path asked: it
// is the file itself, not a file below a directory that folds to it. The names
// are not compared as strings, since git may spell them differently from the
// request (core.precomposeunicode returns a decomposed name composed).
func TrackedPathsFold(dir string, paths ...string) (map[string]bool, error) {
	out := map[string]bool{}
	for _, p := range paths {
		cmd := procattr.Hide(exec.Command("git", "-C", dir, "ls-files", "-z", "--", ":(literal,icase)"+p))
		cmd.Env = withoutPathspecVars(gitEnv(nil))
		b, err := cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("git ls-files: %w", err)
		}
		depth := strings.Count(p, "/")
		for _, f := range strings.Split(string(b), "\x00") {
			if f != "" && strings.Count(f, "/") == depth { // not a file below a directory
				out[p] = true
			}
		}
	}
	return out, nil
}

// TrackedModes returns the index mode ("100644", "100755", ...) of every file
// git tracks at or below pathspecs (taken literally, slash-separated, relative
// to dir). It is the source of truth for the execute bit of a tracked file:
// a filesystem without the bit (NTFS) reads every file as non-executable, the
// index does not.
func TrackedModes(dir string, paths ...string) (map[string]string, error) {
	out := map[string]string{}
	if len(paths) == 0 {
		return out, nil
	}
	cmd := procattr.Hide(exec.Command("git", append([]string{"--literal-pathspecs", "-C", dir, "ls-files", "-s", "-z", "--"}, paths...)...))
	cmd.Env = gitEnv(nil)
	b, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-files -s: %w", err)
	}
	for _, rec := range strings.Split(string(b), "\x00") {
		// "<mode> <sha> <stage>\t<path>"
		meta, path, ok := strings.Cut(rec, "\t")
		if !ok {
			continue
		}
		if f := strings.Fields(meta); len(f) == 3 && f[2] == "0" {
			out[path] = f[0]
		}
	}
	return out, nil
}

// StageExecutable stages paths (literal, relative to dir, existing files) with
// the execute bit set in the index ("git add --chmod=+x"), so the next commit
// records 100755 even where the filesystem cannot carry the bit. A later
// "git add -A" keeps the index mode of a file whose content it restages when
// core.fileMode is false, and sets the same one when the filesystem has it.
func StageExecutable(dir string, paths ...string) error {
	if len(paths) == 0 {
		return nil
	}
	args := append([]string{"--literal-pathspecs", "add", "--chmod=+x", "--"}, paths...)
	if out, err := runGit(dir, args...); err != nil {
		return fmt.Errorf("git add --chmod=+x: %w: %s%s", err, out, indexLockHint(dir, out))
	}
	return nil
}

// Untrack removes paths from the index and leaves the working tree alone
// ("git rm --cached"). The next commit records the removal.
func Untrack(dir string, paths ...string) error {
	if len(paths) == 0 {
		return nil
	}
	if out, err := runGit(dir, append([]string{"--literal-pathspecs", "rm", "--cached", "-q", "--"}, paths...)...); err != nil {
		return fmt.Errorf("git rm --cached: %w: %s%s", err, out, indexLockHint(dir, out))
	}
	return nil
}

// AddPath stages a single path ("git add -- <path>").
func AddPath(dir, path string) error {
	out, err := runGit(dir, "--literal-pathspecs", "add", "--", path)
	if err != nil {
		return fmt.Errorf("git add %s: %w: %s%s", path, err, out, indexLockHint(dir, out))
	}
	return nil
}

// FileChange represents a changed file in a diff.
type FileChange struct {
	Status  string // "A", "M", "D", "R"
	Path    string
	OldPath string // set for renames; Path is the destination
}

// CommitChanges contains the files changed by one commit in git log order
// (newest first). Files retain git's name-status order.
type CommitChanges struct {
	SHA     string
	At      time.Time
	Author  string
	Subject string
	// Agent is the value of the commit's Agent: trailer (D718), empty when
	// absent. With several trailers only the first is kept.
	Agent string
	// MovedTo and MovedFrom are the values of the commit's Moved-To: and
	// Moved-From: trailers (D760): the other KB of a concept_transfer, empty
	// when absent.
	MovedTo, MovedFrom string
	// Token is the value of the commit's Cartographer-Token: trailer (D904):
	// the id of the token that wrote it, appended by the server last. Empty when
	// absent (a commit that is not a write, or one made before it existed).
	Token string
	// Reason is the value of the commit's Reason: trailer (D272), empty when
	// absent. With several trailers only the first is kept.
	Reason string
	// AcceptedFindings are the values of the commit's Accepted-Finding:
	// trailers (D350), "<check>: <reason>" each, in commit order.
	AcceptedFindings []string
	Files            []FileChange
}

// LogNameStatus returns commits since the supplied instant with their changed
// files. Renames are normalised to status "R", with OldPath and Path holding
// the source and destination respectively. A directory that is not a git
// repository, or a repository with no commits in range, returns an empty slice.
func LogNameStatus(dir string, since time.Time) ([]CommitChanges, error) {
	return logNameStatus(dir, "--since="+since.Format(time.RFC3339))
}

// ErrUnknownRev is returned by LogNameStatusRange when the revision is not a
// commit reachable from HEAD: malformed, absent from the repository (a gc'd
// sha, a sha of another KB) or on a history a realign or reset left behind.
var ErrUnknownRev = errors.New("gitx: unknown revision")

// ErrRangeTooLarge is returned by LogNameStatusRange when more than
// MaxRangeCommits commits lie between the revision and HEAD.
var ErrRangeTooLarge = errors.New("gitx: too many commits in range")

// MaxRangeCommits bounds how many commits LogNameStatusRange reads, so a
// cursor far behind HEAD never walks the whole history. A variable so tests
// can shorten it.
var MaxRangeCommits = 5000

// fullSHA matches a full object name, SHA-1 or SHA-256, lower case: the only
// shape a client-supplied revision may have.
var fullSHA = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

// LogNameStatusRange returns the commits in fromSHA..head, newest first, with
// their changed files, as LogNameStatus does, and the head it read: the range
// ends there, so a client that continues from that head loses no commit that
// lands later. fromSHA comes from a client, so it must be a full lower-case hex
// object name that is, or peels to (an annotated tag), a commit reachable from
// HEAD (a ref, a range or an option never reaches git); anything else, and HEAD
// itself unborn, is ErrUnknownRev. More than MaxRangeCommits commits is
// ErrRangeTooLarge. fromSHA equal to head is an empty range.
func LogNameStatusRange(dir, fromSHA string) ([]CommitChanges, string, error) {
	if !fullSHA.MatchString(fromSHA) || !IsRepo(dir) {
		return nil, "", ErrUnknownRev
	}
	head, unborn, err := readHead(dir)
	if err != nil {
		return nil, "", err
	}
	if unborn {
		return nil, "", ErrUnknownRev // no commit at all: nothing is reachable
	}
	// rev-parse prints a HEAD that names a missing object: HEAD must be a commit
	// we can read, or the failure is ours and not the client's revision.
	if _, err := peelCommit(dir, head); err != nil {
		return nil, "", errors.New("gitx: cannot read HEAD")
	}
	from, err := peelCommit(dir, fromSHA)
	if err != nil {
		return nil, "", err
	}
	ok, err := IsAncestor(dir, from, head)
	if err != nil {
		return nil, "", err
	}
	if !ok {
		return nil, "", ErrUnknownRev
	}
	commits, err := logNameStatus(dir, "-n", strconv.Itoa(MaxRangeCommits+1), "--end-of-options", from+".."+head)
	if err != nil {
		return nil, "", err
	}
	if len(commits) > MaxRangeCommits {
		return nil, "", ErrRangeTooLarge
	}
	return commits, head, nil
}

// LogNameStatusAt is LogNameStatus that also returns the head it read and
// starts the walk there, not at whatever HEAD is by the time git runs: the
// answer's commits and its head are one snapshot. A directory that is not a
// repository, or one with no commit yet, returns no commits and an empty head.
func LogNameStatusAt(dir string, since time.Time) ([]CommitChanges, string, error) {
	if !IsRepo(dir) {
		return []CommitChanges{}, "", nil
	}
	head, unborn, err := readHead(dir)
	if err != nil {
		return nil, "", err
	}
	if unborn {
		return []CommitChanges{}, "", nil // no commit yet: nothing to report
	}
	commits, err := logNameStatus(dir, "--since="+since.Format(time.RFC3339), "--end-of-options", head)
	if err != nil {
		return nil, "", err
	}
	return commits, head, nil
}

// readHead reads HEAD's commit. unborn is true only for a repository whose HEAD
// names a branch that has no commit yet (a fresh "git init"); any other failure
// to read HEAD (a corrupt HEAD file, a missing object, git failing) is an error,
// never an empty answer.
func readHead(dir string) (head string, unborn bool, err error) {
	if head, err = HeadSHA(dir); err == nil {
		return head, false, nil
	}
	ref, serr := runGit(dir, "symbolic-ref", "-q", "HEAD")
	ref = strings.TrimSpace(ref)
	if serr != nil || !strings.HasPrefix(ref, "refs/") {
		return "", false, err
	}
	if _, verr := runGit(dir, "show-ref", "--verify", "-q", "--", ref); verr == nil {
		return "", false, err // the branch exists yet HEAD does not resolve
	}
	return "", true, nil
}

// peelCommit resolves a validated full hex name to the commit it is or peels
// to ("^{commit}": an annotated tag names its commit), or ErrUnknownRev when it
// is no commit of the repository. Its output is the only form of a client's
// revision that reaches later git calls.
func peelCommit(dir, sha string) (string, error) {
	out, err := runGit(dir, "rev-parse", "--verify", "--quiet", "--end-of-options", sha+"^{commit}")
	if err != nil {
		return "", ErrUnknownRev
	}
	peeled := strings.TrimSpace(out)
	if !fullSHA.MatchString(peeled) {
		return "", ErrUnknownRev
	}
	return peeled, nil
}

// CommitTime returns the committer time of a commit (a tag peels to its
// commit). sha must be a full hex object name; anything else, or no such
// commit, is ErrUnknownRev. The errors carry no git output.
func CommitTime(dir, sha string) (time.Time, error) {
	if !fullSHA.MatchString(sha) {
		return time.Time{}, ErrUnknownRev
	}
	c, err := peelCommit(dir, sha)
	if err != nil {
		return time.Time{}, err
	}
	out, err := runGit(dir, "show", "-s", "--format=%cI", "--end-of-options", c)
	if err != nil {
		return time.Time{}, errors.New("gitx: cannot read the commit time")
	}
	at, err := time.Parse(time.RFC3339, strings.TrimSpace(out))
	if err != nil {
		return time.Time{}, errors.New("gitx: cannot read the commit time")
	}
	return at, nil
}

// logNameStatus runs "git log --name-status" with rangeArgs (the selection of
// commits) and parses it.
func logNameStatus(dir string, rangeArgs ...string) ([]CommitChanges, error) {
	if !IsRepo(dir) {
		return []CommitChanges{}, nil
	}
	if _, err := HeadSHA(dir); err != nil {
		// A freshly initialised repository has no HEAD yet. It has no history to
		// report, just like a repository whose commits all predate since.
		return []CommitChanges{}, nil
	}

	// The record separator gives each commit an unambiguous boundary while the
	// NUL-separated header keeps subjects with spaces intact. --name-status is
	// deliberately left line-oriented to match git's ordinary path format.
	// Reason must stay the LAST field (after Moved-To/Moved-From/Cartographer-Token): the name-status block is split off at the
	// first newline after it.
	format := "%x1e%H%x00%aI%x00%an%x00%s%x00%(trailers:key=Accepted-Finding,valueonly,separator=%x1f)%x00%(trailers:key=Agent,valueonly,separator=%x1f)%x00%(trailers:key=Moved-To,valueonly,separator=%x1f)%x00%(trailers:key=Moved-From,valueonly,separator=%x1f)%x00%(trailers:key=Cartographer-Token,valueonly,separator=%x1f)%x00%(trailers:key=Reason,valueonly,separator=%x1f)"
	// -z: the name-status block is NUL-separated ("S\0path\0", a rename
	// "R100\0old\0new\0", a "\0" after the commit), so a path is never quoted
	// and may hold a tab or a newline.
	// rangeArgs go last: they may end with --end-of-options and a revision.
	args := append([]string{"log", "-z", "--name-status", "-M", "--pretty=format:" + format}, rangeArgs...)
	out, errOut, err := runGitStdin(dir, "", args...)
	if err != nil {
		return nil, fmt.Errorf("git log --name-status: %w: %s", err, errOut)
	}

	commits := []CommitChanges{}
	for _, record := range strings.Split(out, "\x1e") {
		if record == "" {
			continue
		}
		head := strings.SplitN(record, "\x00", 10)
		if len(head) != 10 {
			continue
		}
		at, err := time.Parse(time.RFC3339, head[1])
		if err != nil {
			return nil, fmt.Errorf("parse git commit time %q: %w", head[1], err)
		}
		// The trailers value ends with a newline that precedes the name-status
		// block, so the block is whatever follows the first newline.
		// With -z a commit with no changed file (a merge, --allow-empty) ends
		// with a lone NUL right after the trailers: not part of the reason.
		reasonAndFiles := strings.SplitN(strings.TrimRight(head[9], "\x00"), "\n", 2)
		accepted := splitTrailerValues(head[4])
		reason, _, _ := strings.Cut(reasonAndFiles[0], "\x1f")
		agent, _, _ := strings.Cut(head[5], "\x1f")
		movedTo, _, _ := strings.Cut(head[6], "\x1f")
		movedFrom, _, _ := strings.Cut(head[7], "\x1f")
		token := lastTrailerValue(head[8])
		commit := CommitChanges{SHA: head[0], At: at, Author: head[2], Subject: head[3], Agent: strings.TrimSpace(agent), MovedTo: strings.TrimSpace(movedTo), MovedFrom: strings.TrimSpace(movedFrom), Token: token, Reason: strings.TrimSpace(reason), AcceptedFindings: accepted}
		if len(reasonAndFiles) == 2 {
			commit.Files = parseNameStatusZ(reasonAndFiles[1])
		}
		commits = append(commits, commit)
	}
	return commits, nil
}

// Rename is one file rename a move tool committed: the concept file moved
// from OldPath to Path (git-relative) by the commit SHA.
type Rename struct {
	SHA     string
	At      time.Time
	Subject string
	OldPath string
	Path    string
}

// moveSubjectTools are the tools whose commits move a concept inside a KB. The
// server writes a subject as "<tool>" or "<tool>: <what>" (commitMessage), so a
// subject is a move's only when it is one of those two shapes: "concept_moveX"
// or a free-text subject that merely starts with the name is not.
var moveSubjectTools = []string{"concept_move", "concept_archive", "kb_repair"}

func isMoveSubject(subject string) bool {
	for _, t := range moveSubjectTools {
		if subject == t || strings.HasPrefix(subject, t+": ") {
			return true
		}
	}
	return false
}

// MoveWalkCommits bounds how many commits (not only rename commits) a read of
// the move history examines: git log -n bounds the output, not the walk, and a
// KB with few renames would otherwise be diffed commit by commit to its root.
// A variable so tests can shorten it.
var MoveWalkCommits = 2000

// MoveHistoryTimeout bounds one git call of the move history, like FetchTimeout
// bounds a fetch: the read runs under the KB's locks and on every review.
var MoveHistoryTimeout = 15 * time.Second

func runGitCtx(ctx context.Context, dir string, args ...string) (stdout, stderr string, err error) {
	cmd := procattr.Hide(exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...))
	cmd.Env = gitEnv(nil)
	var errBuf strings.Builder
	cmd.Stderr = &errBuf
	out, err := cmd.Output()
	if ctx.Err() == context.DeadlineExceeded {
		err = fmt.Errorf("timed out after %s", MoveHistoryTimeout)
	}
	return string(out), errBuf.String(), err
}

// MoveRenames returns the file renames of the last limit commits that contain a
// rename within the last MoveWalkCommits commits of HEAD, keeping the ones a
// move tool made (subject "<tool>" or "<tool>: ..." for concept_move,
// concept_archive or kb_repair; a hand git mv, "external changes" or
// concept_expand are not recorded moves). Oldest first. A directory that is not
// a repository, or has no commit, returns an empty slice. The pair is git's
// rename pairing (-M, 50 % similarity): a page rewritten a lot in the move can
// read as delete + add and is missed, and identical pages moved in one commit
// may be paired crosswise.
func MoveRenames(dir string, limit int) ([]Rename, error) {
	if limit <= 0 || !IsRepo(dir) {
		return []Rename{}, nil
	}
	if _, err := HeadSHA(dir); err != nil {
		return []Rename{}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), MoveHistoryTimeout)
	defer cancel()
	// The walk is bounded on raw commits: the oldest of the last MoveWalkCommits
	// commits, and everything below its parents is excluded. (The bound is
	// approximate on a history with merges: it is a cost cap, not a contract.)
	out, errOut, err := runGitCtx(ctx, dir, "rev-list", "-n", strconv.Itoa(MoveWalkCommits), "HEAD")
	if err != nil {
		return nil, fmt.Errorf("git rev-list: %w: %s", err, errOut)
	}
	shas := strings.Fields(out)
	var revs []string
	if len(shas) >= MoveWalkCommits {
		revs = []string{"HEAD", "--not", shas[len(shas)-1] + "^@"}
	} else {
		revs = []string{"HEAD"}
	}
	return moveRenamesIn(ctx, dir, limit, revs)
}

// MoveRenamesSince is MoveRenames over the commits after since (since..HEAD),
// for a caller that holds the moves up to since. ok is false when it cannot
// answer incrementally: since is not an ancestor of HEAD (a rewrite, a forced
// sync), is unknown, or more than MoveWalkCommits commits follow it; the caller
// then reads the whole bounded history.
func MoveRenamesSince(dir, since string, limit int) (renames []Rename, ok bool, err error) {
	if limit <= 0 || !IsRepo(dir) {
		return nil, false, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), MoveHistoryTimeout)
	defer cancel()
	if _, _, aerr := runGitCtx(ctx, dir, "merge-base", "--is-ancestor", "--end-of-options", since, "HEAD"); aerr != nil {
		return nil, false, nil
	}
	out, errOut, err := runGitCtx(ctx, dir, "rev-list", "--count", "--end-of-options", since+"..HEAD")
	if err != nil {
		return nil, false, fmt.Errorf("git rev-list --count: %w: %s", err, errOut)
	}
	if n, perr := strconv.Atoi(strings.TrimSpace(out)); perr != nil || n > MoveWalkCommits {
		return nil, false, nil
	}
	renames, err = moveRenamesIn(ctx, dir, limit, []string{since + "..HEAD"})
	return renames, err == nil, err
}

func moveRenamesIn(ctx context.Context, dir string, limit int, revs []string) ([]Rename, error) {
	args := []string{"-c", "core.quotepath=off", "log", "-z", "-n", strconv.Itoa(limit), "-M", "--diff-filter=R", "--name-status", "--pretty=format:%x1e%H%x00%aI%x00%s"}
	out, errOut, err := runGitCtx(ctx, dir, append(args, revs...)...)
	if err != nil {
		return nil, fmt.Errorf("git log --diff-filter=R: %w: %s", err, errOut)
	}
	var newest []Rename
	for _, record := range strings.Split(out, "\x1e") {
		if record == "" {
			continue
		}
		head := strings.SplitN(record, "\x00", 3)
		if len(head) != 3 {
			continue
		}
		// The subject is one line; the name-status block follows the newline.
		subject, block, _ := strings.Cut(head[2], "\n")
		if !isMoveSubject(subject) {
			continue
		}
		at, err := time.Parse(time.RFC3339, head[1])
		if err != nil {
			return nil, fmt.Errorf("parse git commit time %q: %w", head[1], err)
		}
		for _, f := range parseNameStatusZ(block) {
			if f.Status == "R" {
				newest = append(newest, Rename{SHA: head[0], At: at, Subject: subject, OldPath: f.OldPath, Path: f.Path})
			}
		}
	}
	// Newest first as git prints; oldest first as returned.
	oldest := make([]Rename, 0, len(newest))
	for i := len(newest) - 1; i >= 0; i-- {
		oldest = append(oldest, newest[i])
	}
	return oldest, nil
}

// parseNameStatusZ parses the "git log -z --name-status" block of one commit:
// status, path (and, for a rename or copy, the destination), each NUL-ended.
func parseNameStatusZ(block string) []FileChange {
	var files []FileChange
	f := strings.Split(block, "\x00")
	for i := 0; i < len(f); {
		status := f[i]
		if status == "" {
			break
		}
		switch status[0] {
		case 'R', 'C':
			if i+2 >= len(f) {
				return files
			}
			c := FileChange{Status: string(status[0]), Path: f[i+2]}
			if status[0] == 'R' {
				c.OldPath = f[i+1]
			}
			files = append(files, c)
			i += 3
		default:
			if i+1 >= len(f) {
				return files
			}
			files = append(files, FileChange{Status: string(status[0]), Path: f[i+1]})
			i += 2
		}
	}
	return files
}

// FileRevision is one commit that touched a single file, as reported by
// FileHistory.
type FileRevision struct {
	SHA     string
	At      time.Time
	Author  string
	Subject string
	// Agent is the value of the commit's Agent: trailer (D718), empty when
	// absent (first value only, like Reason).
	Agent string
	// MovedTo and MovedFrom are the values of the commit's Moved-To: and
	// Moved-From: trailers (D760): the other KB of a concept_transfer, empty
	// when absent.
	MovedTo, MovedFrom string
	// Token is the value of the commit's Cartographer-Token: trailer (D904),
	// as in CommitChanges.
	Token string
	// Reason is the value of the commit's Reason: trailer (D272), empty when
	// absent.
	Reason string
	// AcceptedFindings are the commit's Accepted-Finding: trailer values (D350).
	AcceptedFindings []string
	// Path is the file's path in that commit (the destination for a rename),
	// relative to the repository root.
	Path string
	// Status is A, M, D or R.
	Status string
}

// FileHistory returns the commits that touched path (relative to the
// repository root), newest first. It never follows a rename by similarity
// (#1281: git log --follow paired a merely similar file and listed a commit of
// another concept). It lists the commits that touch the concept's two forms,
// "<id>.md" and the expanded "<id>/index.md" (a concept_expand stays one
// history), and continues behind a rename only when the commit is a
// concept_move: the old path is then walked from that commit's parent. At most
// limit entries are returned (limit <= 0 means no cap). A path with no
// history, a directory that is not a git repository and a repository with no
// commits all return an empty slice.
func FileHistory(dir, path string, limit int) ([]FileRevision, error) {
	if !IsRepo(dir) {
		return []FileRevision{}, nil
	}
	if _, err := HeadSHA(dir); err != nil {
		return []FileRevision{}, nil
	}
	all := []FileRevision{}
	rev := ""
	seen := map[string]bool{}
	for {
		want := 0
		if limit > 0 {
			want = limit - len(all)
			if want <= 0 {
				break
			}
		}
		revs, err := fileHistoryOnce(dir, rev, path, want)
		if err != nil {
			return nil, err
		}
		for _, r := range revs {
			if !seen[r.SHA] {
				seen[r.SHA] = true
				all = append(all, r)
			}
		}
		if len(revs) == 0 {
			break
		}
		last := revs[len(revs)-1]
		if !strings.HasPrefix(last.Subject, "concept_move") || (last.Status != "A" && last.Status != "R") {
			break
		}
		from := movedFrom(dir, last.SHA, last.Path)
		if from == "" {
			break
		}
		rev, path = last.SHA+"^", from
	}
	return all, nil
}

// CommitFiles is the files commit sha changed, renames detected (OldPath holds
// the source), the same shape LogNameStatus reports. A root commit lists all
// its files. It reads the whole commit: a visibility filter needs every path a
// commit touched, not only the one a history was asked for.
func CommitFiles(dir, sha string) ([]FileChange, error) {
	out, errOut, err := runGitStdin(dir, "", "diff-tree", "--root", "-r", "-M", "-z", "--name-status", "--no-commit-id", sha)
	if err != nil {
		return nil, fmt.Errorf("git diff-tree %s: %w: %s", sha, err, errOut)
	}
	return parseNameStatusZ(out), nil
}

// movedFrom is the path that commit renamed to dest, "" when it renamed none
// to it. It reads the whole commit: a pathspec that leaves the old path out
// cannot show the rename.
func movedFrom(dir, sha, dest string) string {
	out, err := runGitEnv(dir, nil, "-c", "core.quotepath=off", "diff-tree", "-r", "-M", "--name-status", "--no-commit-id", sha)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(out, "\n") {
		parts := strings.Split(line, "\t")
		if len(parts) == 3 && parts[0] != "" && parts[0][0] == 'R' && parts[2] == dest {
			return parts[1]
		}
	}
	return ""
}

// fileHistoryOnce is one git log over the two forms of path, starting at rev
// ("" is HEAD).
func fileHistoryOnce(dir, startRev, path string, limit int) ([]FileRevision, error) {
	// Reason must stay the LAST field (see LogNameStatus).
	format := "%x1e%H%x00%aI%x00%an%x00%s%x00%(trailers:key=Accepted-Finding,valueonly,separator=%x1f)%x00%(trailers:key=Agent,valueonly,separator=%x1f)%x00%(trailers:key=Moved-To,valueonly,separator=%x1f)%x00%(trailers:key=Moved-From,valueonly,separator=%x1f)%x00%(trailers:key=Cartographer-Token,valueonly,separator=%x1f)%x00%(trailers:key=Reason,valueonly,separator=%x1f)"
	args := []string{"--literal-pathspecs", "-c", "core.quotepath=off", "log", "-M", "--name-status", "--pretty=format:" + format}
	if limit > 0 {
		args = append(args, "-n", strconv.Itoa(limit))
	}
	if startRev != "" {
		args = append(args, startRev)
	}
	args = append(args, "--", path)
	if alt := otherConceptForm(path); alt != "" {
		args = append(args, alt)
	}
	out, err := runGitEnv(dir, nil, args...)
	if err != nil {
		if startRev != "" {
			return nil, nil // a root commit has no parent: nothing behind it
		}
		return nil, fmt.Errorf("git log %s: %w: %s", path, err, out)
	}
	revs := []FileRevision{}
	for _, record := range strings.Split(out, "\x1e") {
		if record == "" {
			continue
		}
		head := strings.SplitN(record, "\x00", 10)
		if len(head) != 10 {
			continue
		}
		at, err := time.Parse(time.RFC3339, head[1])
		if err != nil {
			return nil, fmt.Errorf("parse git commit time %q: %w", head[1], err)
		}
		reasonAndFiles := strings.SplitN(head[9], "\n", 2)
		accepted := splitTrailerValues(head[4])
		reason, _, _ := strings.Cut(reasonAndFiles[0], "\x1f")
		agent, _, _ := strings.Cut(head[5], "\x1f")
		movedTo, _, _ := strings.Cut(head[6], "\x1f")
		movedFrom, _, _ := strings.Cut(head[7], "\x1f")
		token := lastTrailerValue(head[8])
		rev := FileRevision{SHA: head[0], At: at, Author: head[2], Subject: head[3], Agent: strings.TrimSpace(agent), MovedTo: strings.TrimSpace(movedTo), MovedFrom: strings.TrimSpace(movedFrom), Token: token, Reason: strings.TrimSpace(reason), AcceptedFindings: accepted, Path: path, Status: "M"}
		if len(reasonAndFiles) == 2 {
			for _, line := range strings.Split(reasonAndFiles[1], "\n") {
				parts := strings.Split(line, "\t")
				if len(parts) < 2 || parts[0] == "" {
					continue
				}
				// A commit that touches both forms lists the old one as D:
				// the surviving form decides.
				if rev.Status != "M" && parts[0][0] == 'D' {
					continue
				}
				rev.Status = string(parts[0][0])
				rev.Path = parts[len(parts)-1]
				if rev.Status != "D" {
					break
				}
			}
		}
		revs = append(revs, rev)
	}
	return revs, nil
}

// otherConceptForm is the other file a concept can live in: "<id>.md" for
// "<id>/index.md" and the reverse, "" for a path that is neither.
func otherConceptForm(path string) string {
	switch {
	case strings.HasSuffix(path, "/index.md"):
		return strings.TrimSuffix(path, "/index.md") + ".md"
	case strings.HasSuffix(path, ".md") && !strings.HasSuffix(path, "/index.md"):
		return strings.TrimSuffix(path, ".md") + "/index.md"
	}
	return ""
}

// Status returns the working tree status (short format).
func Status(dir string) (string, error) {
	out, err := runGit(dir, "status", "--short")
	if err != nil {
		return "", fmt.Errorf("git status --short: %w: %s", err, out)
	}
	return out, nil
}

// StatusPaths is Status restricted to paths: empty when none of them has a change.
func StatusPaths(dir string, paths ...string) (string, error) {
	out, err := runGit(dir, append([]string{"--literal-pathspecs", "status", "--short", "--"}, paths...)...)
	if err != nil {
		return "", fmt.Errorf("git status --short: %w: %s", err, out)
	}
	return out, nil
}

// RemoteURL returns the URL of the given remote, or error if not configured.
func RemoteURL(dir, remote string) (string, error) {
	out, err := runGit(dir, "remote", "get-url", remote)
	if err != nil {
		return "", fmt.Errorf("git remote get-url %s: %w: %s", remote, err, out)
	}
	return strings.TrimSpace(out), nil
}

// RemotePushURL is the URL a push to the named remote goes to: its
// remote.<name>.pushurl when set (and url.<base>.pushInsteadOf), else its URL.
func RemotePushURL(dir, remote string) (string, error) {
	out, err := runGit(dir, "remote", "get-url", "--push", "--", remote)
	if err != nil {
		return "", fmt.Errorf("git remote get-url --push %s: %w: %s", remote, err, out)
	}
	return strings.TrimSpace(out), nil
}

// AddRemote adds a remote with the given name and URL.
func AddRemote(dir, name, url string) error {
	if err := ValidateRemote(url); err != nil {
		return err
	}
	out, err := runGit(dir, "remote", "add", "--", name, url)
	if err != nil {
		return fmt.Errorf("git remote add %s: %w: %s", name, redactedErr(err, url), RedactRemoteText(out, url))
	}
	return nil
}

// RemoveRemote removes the named remote, with its remote-tracking refs and
// the tracking configuration of the branches that followed it.
func RemoveRemote(dir, name string) error {
	out, err := runGit(dir, "remote", "remove", "--", name)
	if err != nil {
		return fmt.Errorf("git remote remove %s: %w: %s", name, err, out)
	}
	return nil
}

// BranchExists checks if a branch exists locally.
func BranchExists(dir, branch string) bool {
	_, err := runGit(dir, "rev-parse", "--verify", "refs/heads/"+branch)
	return err == nil
}

// noEOL keeps a stash round trip byte for byte: with core.autocrlf=true (the
// Git for Windows default) a pop checks the stashed files out with CRLF, so a
// write the gate stashed and restored came back rewritten.
var noEOL = []string{"-c", "core.autocrlf=false"}

// StashPushEnv stashes uncommitted changes, with env layered on the git process
// environment. git stash creates commits, so a host with no git identity needs
// the caller to pass one (kb.ServerGitEnv) or the stash fails with "Committer
// identity unknown".
func StashPushEnv(dir string, env ...string) error {
	out, err := runGitEnv(dir, env, append(noEOL, "stash", "push")...)
	if err != nil {
		return fmt.Errorf("git stash push: %w: %s%s", err, out, indexLockHint(dir, out))
	}
	return nil
}

// StashPop pops the last stash. Returns error if nothing to pop.
func StashPop(dir string) error {
	out, err := runGit(dir, append(noEOL, "stash", "pop")...)
	if err != nil {
		return fmt.Errorf("git stash pop: %w: %s%s", err, out, indexLockHint(dir, out))
	}
	return nil
}

// StashDrop discards the last stash without applying it ("git stash drop").
func StashDrop(dir string) error {
	out, err := runGit(dir, "stash", "drop")
	if err != nil {
		return fmt.Errorf("git stash drop: %w: %s", err, out)
	}
	return nil
}

// MergeBase returns the best common ancestor of a and b ("git merge-base").
func MergeBase(dir, a, b string) (string, error) {
	out, err := runGit(dir, "merge-base", "--end-of-options", a, b)
	if err != nil {
		return "", fmt.Errorf("git merge-base %s %s: %w: %s", a, b, err, out)
	}
	return strings.TrimSpace(out), nil
}

// MergeFileUnion returns the line-level union of ours and theirs against base
// ("git merge-file -p --union"): the content git's built-in merge=union driver
// produces, computed without touching the index or the working tree. It is
// how an append-only file such as log.md is reconciled when two clones added
// entries concurrently (D311). Only stdout is read: CombinedOutput would mix a
// warning into the merged content.
func MergeFileUnion(base, ours, theirs string) (string, error) {
	dir, err := os.MkdirTemp("", "cartographer-merge-file-*")
	if err != nil {
		return "", fmt.Errorf("merge-file temp dir: %w", err)
	}
	defer os.RemoveAll(dir)
	paths := make([]string, 3)
	for i, content := range []string{ours, base, theirs} {
		paths[i] = filepath.Join(dir, fmt.Sprintf("f%d", i))
		if err := os.WriteFile(paths[i], []byte(content), 0o600); err != nil {
			return "", fmt.Errorf("merge-file temp file: %w", err)
		}
	}
	cmd := procattr.Hide(exec.Command("git", "merge-file", "-p", "--union", paths[0], paths[1], paths[2]))
	cmd.Env = gitEnv(nil)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git merge-file --union: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return string(out), nil
}

// ResolveCommit returns the full SHA of the commit that ref names, or an error
// when ref is not a commit of this repository.
func ResolveCommit(dir, ref string) (string, error) {
	out, err := runGit(dir, "rev-parse", "--verify", "--quiet", "--end-of-options", ref+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("%q is not a commit of this KB", ref)
	}
	return strings.TrimSpace(out), nil
}

// CommitSummary is the part of a commit a revert guard reads.
type CommitSummary struct {
	Subject string
	// Reason is the Reason: trailer (D272), empty when absent.
	Reason  string
	Parents int
}

// ShowCommit reads the subject, the Reason trailer and the number of parents
// of one commit.
func ShowCommit(dir, sha string) (CommitSummary, error) {
	out, err := runGit(dir, "show", "-s", "--format=%P%x00%s%x00%(trailers:key=Reason,valueonly,separator=%x1f)", sha)
	if err != nil {
		return CommitSummary{}, fmt.Errorf("git show %s: %w: %s", sha, err, out)
	}
	parts := strings.SplitN(strings.TrimRight(out, "\n"), "\x00", 3)
	if len(parts) != 3 {
		return CommitSummary{}, fmt.Errorf("git show %s: unexpected output %q", sha, out)
	}
	reason, _, _ := strings.Cut(parts[2], "\x1f")
	return CommitSummary{Subject: parts[1], Reason: strings.TrimSpace(reason), Parents: len(strings.Fields(parts[0]))}, nil
}

// RevertNoCommit applies the inverse of commit sha to the working tree and the
// index without committing, so the caller's own commit path records it. On a
// conflict it returns an error and leaves the tree for the caller to reset.
func RevertNoCommit(dir, sha string, env ...string) error {
	if out, err := runGitEnv(dir, env, "revert", "--no-commit", "--end-of-options", sha); err != nil {
		return fmt.Errorf("git revert %s: %w: %s%s", sha, err, strings.TrimSpace(out), indexLockHint(dir, out))
	}
	return nil
}

// StagedChanges stages every change of the working tree (stageAll: git add -A,
// leaving a gitlink out, a *GitlinkError for a new nested repository) and
// returns what differs from HEAD, renames detected (-M), parsed from the
// NUL-separated form so a path with spaces or unusual bytes survives (D350).
// The index is left staged; DiscardChanges or a commit clears it.
func StagedChanges(dir string) ([]FileChange, error) {
	if err := stageAll(dir, nil); err != nil {
		return nil, err
	}
	out, err := runGit(dir, "-c", "core.quotepath=off", "diff", "--cached", "-M", "--name-status", "-z", "HEAD")
	if err != nil {
		return nil, fmt.Errorf("git diff --cached: %w: %s", err, out)
	}
	fields := strings.Split(out, "\x00")
	var changes []FileChange
	for i := 0; i < len(fields); i++ {
		st := fields[i]
		if st == "" {
			continue
		}
		switch st[0] {
		case 'R', 'C':
			if i+2 >= len(fields) {
				return changes, nil
			}
			changes = append(changes, FileChange{Status: "R", OldPath: fields[i+1], Path: fields[i+2]})
			i += 2
		default:
			if i+1 >= len(fields) {
				return changes, nil
			}
			changes = append(changes, FileChange{Status: string(st[0]), Path: fields[i+1]})
			i++
		}
	}
	return changes, nil
}

// DiscardChanges returns the working tree and index to HEAD, untracked files
// included (git reset --hard HEAD + git clean -fd). It is deliberately narrow,
// like ResetHardTo: the caller holds the KB lock and has checked the tree was
// clean before the write it undoes (D350). Ignored files are kept.
func DiscardChanges(dir string) error {
	if out, err := runGit(dir, "reset", "--hard", "HEAD"); err != nil {
		return fmt.Errorf("git reset --hard HEAD: %w: %s%s", err, out, indexLockHint(dir, out))
	}
	if out, err := runGit(dir, "clean", "-fd"); err != nil {
		return fmt.Errorf("git clean -fd: %w: %s", err, out)
	}
	return nil
}

// StashPushAllEnv stashes every uncommitted change including untracked files, so
// the tree is at HEAD until StashPop (StashPushEnv leaves untracked files
// behind). env is layered on the git process environment; see StashPushEnv for
// why a caller passes the server identity.
func StashPushAllEnv(dir string, env ...string) error {
	out, err := runGitEnv(dir, env, append(noEOL, "stash", "push", "--include-untracked")...)
	if err != nil {
		return fmt.Errorf("git stash push --include-untracked: %w: %s%s", err, out, indexLockHint(dir, out))
	}
	return nil
}

// lastTrailerValue is the last value of a %(trailers:...separator=%x1f) field.
// The server appends Cartographer-Token last, so the last one is the real one.
func lastTrailerValue(field string) string {
	values := splitTrailerValues(field)
	if len(values) == 0 {
		return ""
	}
	return strings.TrimSpace(values[len(values)-1])
}

// splitTrailerValues splits the value-only rendering of a repeated trailer
// (separator %x1f) into its non-empty, trimmed values.
func splitTrailerValues(raw string) []string {
	var out []string
	for _, v := range strings.Split(raw, "\x1f") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// IsProtectedBranchRejection reports whether the output of a failed push says
// the remote refused it because the branch is protected, or because a
// server-side hook declined it (D903). Forges word it differently (GitHub:
// "GH006: Protected branch update failed", GitLab: "You are not allowed to push
// code to protected branches", and "pre-receive hook declined" for both), so it
// is matched on those phrases and not tied to one provider. Retrying such a push
// repeats the refusal: the caller reports it and stops.
func IsProtectedBranchRejection(output string) bool {
	l := strings.ToLower(output)
	return strings.Contains(l, "gh006") ||
		strings.Contains(l, "protected branch") ||
		strings.Contains(l, "pre-receive hook declined")
}

// IsIdentityRejection reports whether the output of a failed push says the
// remote refused it because of who authored or committed a commit: GitLab's
// membership push rule ("GitLab: Author 'x' is not a member of team", and the
// "Committer" variant), GitHub's private-email protection ("GH007: Your push
// would publish a private email address"). It is a different refusal from a
// protected branch (IsProtectedBranchRejection) and needs a different fix:
// callers test it first, because the generic "pre-receive hook declined" line
// accompanies both. It never matches GH006 or a protected-branch wording.
func IsIdentityRejection(output string) bool {
	l := strings.ToLower(output)
	if strings.Contains(l, "gh006") || strings.Contains(l, "protected branch") {
		return false
	}
	if strings.Contains(l, "gh007") || strings.Contains(l, "private email address") ||
		strings.Contains(l, "is not a member of team") {
		return true
	}
	// GitLab's variants: "Author 'x' is not a member of team", "Committer 'x' ...".
	for _, who := range []string{"author '", "committer '"} {
		if i := strings.Index(l, who); i >= 0 && strings.Contains(l[i:], "' is not a member") {
			return true
		}
	}
	return false
}

// Births replays commits (newest first, as LogNameStatus returns them) from
// the oldest and returns when each key first entered the repository: the
// commit that added its file, carried across renames. key maps a file path to
// the identity being tracked; a path it rejects is ignored. A file deleted and
// added again is born again; one with no history is absent.
func Births(commits []CommitChanges, key func(path string) (string, bool)) map[string]time.Time {
	born := map[string]time.Time{}
	for i := len(commits) - 1; i >= 0; i-- {
		for _, file := range commits[i].Files {
			id, ok := key(file.Path)
			switch file.Status {
			case "A":
				if ok {
					if _, seen := born[id]; !seen {
						born[id] = commits[i].At
					}
				}
			case "R":
				at := commits[i].At
				if oldID, oldOK := key(file.OldPath); oldOK {
					if prev, seen := born[oldID]; seen {
						at = prev
					}
					delete(born, oldID)
				}
				if ok {
					born[id] = at
				}
			case "D":
				if ok {
					delete(born, id)
				}
			}
		}
	}
	return born
}

// OldestUnpushedAt is the committer time of the oldest commit in
// <remote>/<branch>..HEAD, the one that has waited longest to be pushed; ok is
// false when nothing is unpushed or the remote-tracking ref is absent. No
// network is touched.
func OldestUnpushedAt(dir, remote, branch string) (at time.Time, ok bool, err error) {
	if branch == "" {
		return time.Time{}, false, nil
	}
	ref := remote + "/" + branch
	if _, err := runGit(dir, "rev-parse", "--verify", "refs/remotes/"+ref); err != nil {
		return time.Time{}, false, nil
	}
	out, err := runGit(dir, "log", "--format=%ct", ref+"..HEAD")
	if err != nil {
		return time.Time{}, false, fmt.Errorf("git log %s..HEAD: %w: %s", ref, err, out)
	}
	lines := strings.Fields(out)
	if len(lines) == 0 {
		return time.Time{}, false, nil
	}
	sec, perr := strconv.ParseInt(lines[len(lines)-1], 10, 64)
	if perr != nil {
		return time.Time{}, false, fmt.Errorf("git log %s..HEAD: commit time %q: %w", ref, lines[len(lines)-1], perr)
	}
	return time.Unix(sec, 0).UTC(), true, nil
}
