package kb

import (
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/gitx"
)

func intp(n int) *int { return &n }

// The threshold decides what humans see: a failed push at once, unpushed
// commits only once the oldest has waited long enough, nothing otherwise.
func TestPushLagFrom_Threshold(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	th := 3 * time.Minute
	cases := []struct {
		name   string
		s      GitStatus
		oldest time.Time
		has    bool
		want   bool
	}{
		{"failed shows at once", GitStatus{State: "failed", UnpushedCommits: intp(1)}, now.Add(-time.Second), true, true},
		{"failed with unknown count shows", GitStatus{State: "failed"}, time.Time{}, false, true},
		{"pending inside the threshold is quiet", GitStatus{State: "pending", UnpushedCommits: intp(2)}, now.Add(-th + time.Second), true, false},
		{"pending at the threshold shows", GitStatus{State: "pending", UnpushedCommits: intp(2)}, now.Add(-th), true, true},
		{"pending past the threshold shows", GitStatus{State: "pending", UnpushedCommits: intp(2)}, now.Add(-time.Hour), true, true},
		{"clean is quiet", GitStatus{State: "clean", UnpushedCommits: intp(0)}, time.Time{}, false, false},
		{"old commit but nothing unpushed is quiet", GitStatus{State: "clean", UnpushedCommits: intp(0)}, now.Add(-time.Hour), true, false},
		{"unknown comparison is quiet", GitStatus{State: "pending"}, now.Add(-time.Hour), true, false},
		{"no remote is quiet", GitStatus{State: "no_remote"}, time.Time{}, false, false},
	}
	for _, c := range cases {
		lag, ok := PushLagFrom(c.s, c.oldest, c.has, now, th)
		if ok != c.want {
			t.Errorf("%s: shown = %v, want %v", c.name, ok, c.want)
		}
		if ok && c.s.UnpushedCommits != nil && lag.Unpushed != *c.s.UnpushedCommits {
			t.Errorf("%s: unpushed = %d, want %d", c.name, lag.Unpushed, *c.s.UnpushedCommits)
		}
	}
}

// The remedy is a fixed sentence chosen by classification; none of git's own
// text (remote lines, URLs with userinfo, hosts) can reach it.
func TestRemedy_ClassifiedTextOnly(t *testing.T) {
	t.Parallel()
	secret := "https://user:s3cr3t-token@git.example.com/org/repo.git"
	cases := map[string]string{
		"git push " + secret + ": remote: GH006: Protected branch update failed\nremote: see ssh://git@internal.example.com:2222/x": "protected",
		"remote: GitLab: Author 'a' is not a member of team " + secret:                                                              "authored or committed",
		"git push " + secret + ": Could not resolve host: git.example.com":                                                          "does not resolve",
		"fatal: something unknown happened with " + secret + "\nremote: private line":                                               "the push failed",
	}
	for in, want := range cases {
		got := Remedy(in)
		if !strings.Contains(got, want) {
			t.Errorf("Remedy(%q) = %q, want it to say %q", in, got, want)
		}
		for _, leak := range []string{"s3cr3t", "user:", "example.com", "remote:", "private line", "2222"} {
			if strings.Contains(got, leak) {
				t.Errorf("Remedy(%q) = %q leaks %q", in, got, leak)
			}
		}
	}
	if Remedy("") != "" || Remedy("  \n") != "" {
		t.Error("Remedy of no error must be empty")
	}
}

// A remote that refuses every push: the KB reports the lag at once, and a
// commit that merely has not been pushed yet reports it only when old.
func TestKBPushLag_RefusingRemote(t *testing.T) {
	t.Parallel()
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k, bare := setupKBWithRemote(t)
	k.SetDefaultIdentity("Ada Example", "ada@example.test")
	if err := k.SyncOut(); err != nil {
		t.Fatalf("seed SyncOut: %v", err)
	}
	if _, ok := k.PushLag(time.Now()); ok {
		t.Fatal("a KB in sync reports a push lag")
	}
	protectBranch(t, bare, "remote: push rules declined by the pre-receive hook")
	if err := k.WriteFileAtomic("data/a.md", []byte("a\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := k.CommitOpAs("write a", "", ""); err != nil {
		t.Fatalf("CommitOp: %v", err)
	}

	// Committed, not yet pushed: quiet now, shown once it is old enough.
	if _, ok := k.PushLag(time.Now()); ok {
		t.Error("a fresh unpushed commit is reported before the threshold")
	}
	lag, ok := k.PushLag(time.Now().Add(PushLagThreshold + time.Minute))
	if !ok || lag.Unpushed != 1 {
		t.Fatalf("PushLag past the threshold = %+v, %v, want 1 unpushed", lag, ok)
	}
	if _, ok, err := gitx.OldestUnpushedAt(k.Root, "origin", "main"); err != nil || !ok {
		t.Errorf("OldestUnpushedAt = %v, %v, want a commit", ok, err)
	}

	if err := k.SyncOut(); err == nil {
		t.Fatal("the refusing remote accepted the push")
	}
	lag, ok = k.PushLag(time.Now())
	if !ok || lag.State != "failed" || lag.Unpushed != 1 || Remedy(lag.Error) == "" {
		t.Errorf("PushLag after a refused push = %+v, %v, want failed, 1 unpushed and a remedy", lag, ok)
	}
}
