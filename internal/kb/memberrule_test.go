package kb

import (
	"os/exec"
	"strings"
	"testing"
)

// A remote that applies GitLab's member_check (the commit's author and
// committer must be a member; here only ada@example.test) must
// accept a KB once an identity is configured for it, including the commit made
// before it was (D950). TRAP: the rule checks the committer as well as the
// author, which is what made 0.25.0 unable to publish at all.
func TestSyncOut_MemberCheckRemoteAcceptsTheConfiguredIdentity(t *testing.T) {
	t.Parallel()
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k, bare := setupKBWithRemote(t)
	if err := k.SyncOut(); err != nil { // the published history, before the forge's rule
		t.Fatal(err)
	}
	installForgeHook(t, bare, "ada@example.test")
	write := func(rel, subject string) {
		t.Helper()
		if err := k.WriteFileAtomic(rel, []byte(subject+"\n")); err != nil {
			t.Fatal(err)
		}
		if _, err := k.CommitOp(subject); err != nil {
			t.Fatal(err)
		}
	}
	// No identity configured: the server identity, refused.
	write("data/a.md", "no identity")
	if err := k.SyncOut(); err == nil {
		t.Fatal("the member check must refuse the server identity")
	}
	// Configured: the stuck commit is re-committed by it and the push goes through.
	k.SetDefaultIdentity("Ada Example", "ada@example.test")
	write("data/b.md", "with identity")
	if err := k.SyncOut(); err != nil {
		t.Fatalf("SyncOut with the identity configured: %v", err)
	}
	out, err := exec.Command("git", "-C", bare, "log", "-2", "--format=%an <%ae> | %cn <%ce>", "main").CombinedOutput()
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if l != "Ada Example <ada@example.test> | Ada Example <ada@example.test>" {
			t.Errorf("published commit = %q, want the configured identity as author and committer", l)
		}
	}
}
