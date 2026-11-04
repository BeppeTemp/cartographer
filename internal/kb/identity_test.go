package kb

import "testing"

// The default identity is the configured one, else the server's; both empty
// removes it (D950).
func TestDefaultIdentity_ConfiguredElseServer(t *testing.T) {
	t.Parallel()
	k, _ := initGitKB(t)
	serverName, serverEmail := ServerIdentity()
	if n, e := k.DefaultIdentity(); n != serverName || e != serverEmail {
		t.Errorf("unconfigured: %q <%q>, want the server's", n, e)
	}
	if _, _, ok := k.ConfiguredIdentity(); ok {
		t.Error("nothing is configured yet")
	}
	k.SetDefaultIdentity("Ada Example", "ada@example.test")
	if n, e := k.DefaultIdentity(); n != "Ada Example" || e != "ada@example.test" {
		t.Errorf("configured: %q <%q>", n, e)
	}
	k.SetDefaultIdentity("", "")
	if n, e := k.DefaultIdentity(); n != serverName || e != serverEmail {
		t.Errorf("removed: %q <%q>, want the server's", n, e)
	}
}

// What the server commits with no writer of its own is by the configured
// identity as author and committer: git's config and the process environment
// change nothing (D950).
func TestCommitOp_ConfiguredIdentityIsTheDefault(t *testing.T) {
	t.Parallel()
	k, _ := initGitKB(t)
	gitHere(t, k.Root, "config", "user.name", "Repository User")
	gitHere(t, k.Root, "config", "user.email", "repo@example.test")
	k.SetDefaultIdentity("Ada Example", "ada@example.test")
	if err := k.WriteFileAtomic("data/a.md", []byte("x\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := k.CommitOp("test: default identity"); err != nil {
		t.Fatal(err)
	}
	want := "Ada Example <ada@example.test> | Ada Example <ada@example.test>"
	if got, err := gitLogFormat(t, k.Root, "%an <%ae> | %cn <%ce>"); err != nil || got != want {
		t.Errorf("commit = %q, %v, want %q", got, err, want)
	}
}
