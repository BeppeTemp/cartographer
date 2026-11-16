package kb

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/gitx"
)

const (
	allowedName  = "Ada Example"
	allowedEmail = "ada@example.test"
)

// commitLocal makes a commit in the KB clone by name <email>, as author and
// committer, the way a write made under that identity would be.
func commitLocal(t *testing.T, k *KB, file, name, email string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(k.Root, "data", file), []byte(file+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitHere(t, k.Root, "add", "-A")
	cmd := exec.Command("git", "-C", k.Root, "commit", "-q", "-m", "write "+file)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME="+name, "GIT_AUTHOR_EMAIL="+email,
		"GIT_COMMITTER_NAME="+name, "GIT_COMMITTER_EMAIL="+email,
		"GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("commit: %v\n%s", err, out)
	}
}

// seededMembershipKB is a KB pushed once, then guarded by a membership rule
// that lets only the allowed email through.
func seededMembershipKB(t *testing.T) (*KB, string) {
	t.Helper()
	k, bare := setupKBWithRemote(t)
	if err := k.SyncOut(); err != nil {
		t.Fatalf("seed SyncOut: %v", err)
	}
	installForgeHook(t, bare, allowedEmail)
	return k, bare
}

// remoteHeadIdent reads the KB's own branch on the remote: the bare
// repository's HEAD may name another branch than the one the KB pushes.
func remoteHeadIdent(t *testing.T, k *KB, bare string) string {
	t.Helper()
	branch, err := gitx.Branch(k.Root)
	if err != nil {
		t.Fatal(err)
	}
	return gitHere(t, bare, "log", "-1", "--format=%an <%ae> | %cn <%ce>", branch)
}

// A membership refusal is an identity error, not a protected branch: it names
// the identities, says how to fix them, and never sends the operator to
// unprotect anything (D1063).
func TestSyncOut_MembershipRefusalIsAnIdentityError(t *testing.T) {
	t.Parallel()
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k, _ := seededMembershipKB(t)
	commitLocal(t, k, "a.md", "tok-1", "cartographer@host.example.test")

	err := k.SyncOut()
	var ie *ErrIdentityRejected
	if !errors.As(err, &ie) {
		t.Fatalf("SyncOut err = %v, want *ErrIdentityRejected", err)
	}
	var pb *ErrProtectedBranch
	if errors.As(err, &pb) {
		t.Error("an identity refusal was also taken for a protected branch")
	}
	if ie.Configured {
		t.Error("Configured = true with no identity set")
	}
	if len(ie.Identities) != 1 || ie.Identities[0] != "tok-1 <cartographer@host.example.test>" {
		t.Errorf("Identities = %v, want the token identity", ie.Identities)
	}
	msg := err.Error()
	for _, want := range []string{"tok-1 <cartographer@host.example.test>", "git.identities.default", "cartographer kb reauthor", "safe in the local clone"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q does not say %q", msg, want)
		}
	}
	if strings.Contains(strings.ToLower(msg), "unprotect") {
		t.Errorf("message %q sends the operator to unprotect a branch", msg)
	}
	if s := k.GitStatusSnapshot(); s.State != "failed" || s.Attempts != 1 {
		t.Errorf("status = %+v, want failed after one attempt (not retried)", s)
	}
}

// Configured is reported, and the identities that differ from it come first.
func TestSyncOut_IdentityErrorSaysWhenIdentityIsConfigured(t *testing.T) {
	t.Parallel()
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k, _ := seededMembershipKB(t)
	k.SetDefaultIdentity(allowedName, "someone@example.test") // not on the allow-list
	commitLocal(t, k, "a.md", "tok-1", "cartographer@host.example.test")
	commitLocal(t, k, "b.md", allowedName, "someone@example.test")
	err := k.SyncOut()
	var ie *ErrIdentityRejected
	if !errors.As(err, &ie) {
		t.Fatalf("SyncOut err = %v, want *ErrIdentityRejected", err)
	}
	if !ie.Configured || strings.Contains(err.Error(), "No git identity is configured") {
		t.Errorf("Configured = %v, message %q", ie.Configured, err)
	}
	if len(ie.Identities) != 2 || !strings.HasPrefix(ie.Identities[0], "tok-1 ") {
		t.Errorf("Identities = %v, want the differing one first", ie.Identities)
	}
}

// A decline that does not name authorship or identity is a protected branch or
// a server-side hook, whatever identities the unpushed commits carry, and the
// remote's reason is quoted; sync_status and the doctor's Remedy agree (#2151).
func TestSyncOut_HookDeclineWithoutIdentityWordingIsNotIdentity(t *testing.T) {
	t.Parallel()
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	for _, reason := range []string{
		"remote: rejected by policy: protected",
		"remote: push rules declined by the pre-receive hook",
		"remote: rejected by policy: hook declined",
		"remote: error: GH006: Protected branch update failed for refs/heads/main.",
	} {
		k, bare := setupKBWithRemote(t)
		if err := k.SyncOut(); err != nil {
			t.Fatal(err)
		}
		protectBranch(t, bare, reason)
		commitLocal(t, k, "a.md", "tok-1", "cartographer@host.example.test")
		err := k.SyncOut()
		var pb *ErrProtectedBranch
		if !errors.As(err, &pb) {
			t.Fatalf("%q: SyncOut err = %v, want *ErrProtectedBranch", reason, err)
		}
		if !strings.Contains(err.Error(), strings.TrimPrefix(reason, "remote: ")) {
			t.Errorf("%q: error does not quote the remote's reason: %v", reason, err)
		}
		if strings.Contains(err.Error(), "who authored") || strings.Contains(Remedy(err.Error()), "who authored") {
			t.Errorf("%q: reported as an identity refusal: %v", reason, err)
		}
		if got := Remedy(err.Error()); !strings.Contains(got, "protected or a server-side hook") {
			t.Errorf("%q: Remedy = %q", reason, got)
		}
	}
}

// A bare decline (only the generic "pre-receive hook declined") with fallback
// identities ahead and none configured is inferred to be an identity refusal
// (D1063), worded as inferred; the doctor's Remedy does not contradict it. With
// an identity configured it stays a protected branch.
func TestSyncOut_BareHookDeclineWithFallbackIdentityIsInferredIdentity(t *testing.T) {
	t.Parallel()
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k, bare := setupKBWithRemote(t)
	if err := k.SyncOut(); err != nil {
		t.Fatal(err)
	}
	protectBranch(t, bare, "remote: pre-receive hook declined")
	commitLocal(t, k, "a.md", "tok-1", "cartographer@host.example.test")
	err := k.SyncOut()
	var ie *ErrIdentityRejected
	if !errors.As(err, &ie) || !ie.Inferred {
		t.Fatalf("SyncOut err = %v, want an inferred *ErrIdentityRejected", err)
	}
	for _, want := range []string{"gave no reason", "fallback identity", "none is configured", "the same output"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Error() lacks %q: %v", want, err)
		}
		if !strings.Contains(ie.NeutralText(), want) {
			t.Errorf("NeutralText() lacks %q: %v", want, ie.NeutralText())
		}
	}
	if got := Remedy(err.Error()); got != "the remote gave no reason; identity is likely (see sync_status)" {
		t.Errorf("Remedy = %q", got)
	}

	k2, bare2 := setupKBWithRemote(t)
	if err := k2.SyncOut(); err != nil {
		t.Fatal(err)
	}
	protectBranch(t, bare2, "remote: pre-receive hook declined")
	k2.SetDefaultIdentity(allowedName, allowedEmail)
	commitLocal(t, k2, "a.md", "tok-1", "cartographer@host.example.test")
	var pb *ErrProtectedBranch
	if err := k2.SyncOut(); !errors.As(err, &pb) {
		t.Fatalf("SyncOut err = %v, want *ErrProtectedBranch with an identity configured", err)
	}
}

func TestIsBareHookDecline(t *testing.T) {
	t.Parallel()
	yes := []string{
		"git push origin main: exit status 1: remote: pre-receive hook declined\nTo /x/remote.git\n ! [remote rejected] main -> main (pre-receive hook declined)\nerror: failed to push some refs to '/x/remote.git'",
		" ! [remote rejected] main -> main (pre-receive hook declined)",
	}
	no := []string{
		"git push origin main: exit status 1: remote: remote: rejected by policy: protected\nTo /x\n ! [remote rejected] main -> main (pre-receive hook declined)",
		"remote: push rules declined by the pre-receive hook\n ! [remote rejected] main -> main (pre-receive hook declined)",
		"! [rejected] main -> main (non-fast-forward)",
		" ! [remote rejected] main -> main (protected branch hook declined)",
		" ! [remote rejected] main -> main (rejected by policy: hook declined)",
	}
	for _, s := range yes {
		if !gitx.IsBareHookDecline(s) {
			t.Errorf("%q is bare", s)
		}
	}
	for _, s := range no {
		if gitx.IsBareHookDecline(s) {
			t.Errorf("%q has a reason of its own", s)
		}
	}
}

// A refusal that names the author is the identity cause, in the status text and
// in the doctor's Remedy alike.
func TestSyncOut_IdentityWordingIsIdentityInStatusAndRemedy(t *testing.T) {
	t.Parallel()
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k, bare := setupKBWithRemote(t)
	if err := k.SyncOut(); err != nil {
		t.Fatal(err)
	}
	protectBranch(t, bare, "remote: GitLab: Author 'tok-1@example.test' is not a member of team")
	commitLocal(t, k, "a.md", "tok-1", "cartographer@host.example.test")
	err := k.SyncOut()
	var ie *ErrIdentityRejected
	if !errors.As(err, &ie) {
		t.Fatalf("SyncOut err = %v, want *ErrIdentityRejected", err)
	}
	if got := Remedy(err.Error()); !strings.Contains(got, "who authored") {
		t.Errorf("Remedy = %q", got)
	}
}

// WP0 shapes. (a) a commit by exactly the server identity is re-authored by the
// automatic realign and publishes once the identity is configured (D950).
func TestSyncOut_ServerIdentityCommitPublishesOnceIdentityIsConfigured(t *testing.T) {
	t.Parallel()
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k, bare := seededMembershipKB(t)
	name, email := ServerIdentity()
	commitLocal(t, k, "a.md", name, email)
	var ie *ErrIdentityRejected
	if err := k.SyncOut(); !errors.As(err, &ie) {
		t.Fatalf("before configuring: err = %v, want *ErrIdentityRejected", err)
	}
	k.SetDefaultIdentity(allowedName, allowedEmail) // what a restart does
	if err := k.SyncOut(); err != nil {
		t.Fatalf("SyncOut after configuring the identity: %v", err)
	}
	if got := remoteHeadIdent(t, k, bare); got != "Ada Example <ada@example.test> | Ada Example <ada@example.test>" {
		t.Errorf("remote head = %s", got)
	}
}

// (b) a commit authored by a token is left alone by the automatic path and stays
// refused; `kb reauthor` (gitx.ReauthorCommits) then lets it publish, the
// trailers intact. (c) a merge in the range is refused by the rewrite.
func TestSyncOut_TokenAuthoredCommitNeedsReauthor(t *testing.T) {
	t.Parallel()
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k, bare := seededMembershipKB(t)
	k.SetDefaultIdentity(allowedName, allowedEmail)
	commitLocal(t, k, "a.md", "tok-1", "cartographer@host.example.test")
	var ie *ErrIdentityRejected
	if err := k.SyncOut(); !errors.As(err, &ie) || !ie.Configured {
		t.Fatalf("SyncOut err = %v, want *ErrIdentityRejected with the identity configured", err)
	}

	branch, _ := gitx.Branch(k.Root)
	done, err := gitx.ReauthorCommits(k.Root, "origin/"+branch, allowedName, allowedEmail)
	if err != nil || len(done) != 1 {
		t.Fatalf("ReauthorCommits = %v, %v", done, err)
	}
	if err := k.SyncOut(); err != nil {
		t.Fatalf("SyncOut after reauthor: %v", err)
	}
	if got := remoteHeadIdent(t, k, bare); got != "Ada Example <ada@example.test> | Ada Example <ada@example.test>" {
		t.Errorf("remote head = %s", got)
	}
}

func TestReauthor_MergeInTheRangeIsRefused(t *testing.T) {
	t.Parallel()
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k, _ := seededMembershipKB(t)
	branch, _ := gitx.Branch(k.Root)
	gitHere(t, k.Root, "checkout", "-q", "-b", "side")
	commitLocal(t, k, "s.md", "tok-1", "cartographer@host.example.test")
	gitHere(t, k.Root, "checkout", "-q", branch)
	commitLocal(t, k, "m.md", "tok-1", "cartographer@host.example.test")
	gitHere(t, k.Root, "-c", "user.name=T", "-c", "user.email=t@example.test", "merge", "-q", "--no-ff", "-m", "merge", "side")
	before := gitHere(t, k.Root, "rev-parse", "HEAD")
	if _, err := gitx.ReauthorCommits(k.Root, "origin/"+branch, allowedName, allowedEmail); !errors.Is(err, gitx.ErrNotLinear) {
		t.Fatalf("err = %v, want ErrNotLinear", err)
	}
	if gitHere(t, k.Root, "rev-parse", "HEAD") != before {
		t.Error("HEAD moved")
	}
}

// D1101: the neutral rendering is what a connection that is not the server's
// operator reads; Error() above stays the full text.
func TestErrIdentityRejected_NeutralText(t *testing.T) {
	t.Parallel()
	e := &ErrIdentityRejected{Branch: "main", Git: "remote: rejected", Identities: []string{"tok-1 <cartographer@host.example.test>"}}
	msg := e.NeutralText()
	for _, want := range []string{"tok-1 <cartographer@host.example.test>", "server's operator", "safe in the local clone", "Git said: remote: rejected"} {
		if !strings.Contains(msg, want) {
			t.Errorf("neutral text %q does not say %q", msg, want)
		}
	}
	for _, bad := range []string{"cartographer kb", "git.identity", "--apply"} {
		if strings.Contains(msg, bad) {
			t.Errorf("neutral text %q still says %q", msg, bad)
		}
	}
	configured := (&ErrIdentityRejected{Branch: "main", Git: "x", Configured: true}).NeutralText()
	if strings.Contains(configured, "No git identity is configured") {
		t.Errorf("a configured KB reads %q", configured)
	}
}
