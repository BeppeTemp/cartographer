package landertest

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestUnlandedListsWorkNoQueueShows pins scripts/unlanded.sh (make unlanded, and the `unlanded:`
// section of make status): a local branch with commits ahead of origin/main is listed unless its
// slug is queued, rejected or landed; a worktree with uncommitted tracked changes and a wip/*
// branch on origin are listed too. Exit 0 and no output when there is nothing.
func TestUnlandedListsWorkNoQueueShows(t *testing.T) {
	e := newLandEnv(t)
	run := func() (string, int) {
		out, _, code := e.acScript("unlanded.sh", nil)
		return out, code
	}
	if out, code := run(); code != 0 || out != "" {
		t.Fatalf("nothing unlanded must print nothing and exit 0: exit %d\n%s", code, out)
	}
	branch := func(name, file string) {
		e.git("checkout", "-q", "-B", name, "origin/main")
		e.commitFiles("feat: "+name, map[string]string{file: name + "\n"})
		e.git("checkout", "-q", "main")
	}
	branch("feat/never", "never.txt")
	branch("feat/queued", "queued.txt")
	branch("feat/rejected", "rejected.txt")
	branch("feat/landed", "landed.txt")
	e.git("push", "-q", "origin", "feat/queued:refs/heads/ready/queued", "feat/rejected:refs/heads/failed/rejected")
	// A commit on main that carries the Lander-Slug trailer of feat/landed, and one whose
	// trailer only mentions another slug in its body (not a landing of it).
	e.git("checkout", "-q", "-B", "work-main", "origin/main")
	e.commitFiles("feat: landed\n\nLander-Slug: landed\n", map[string]string{"l.txt": "l\n"})
	e.commitFiles("feat: other\n\nsee Lander-Slug: never in prose\n", map[string]string{"o.txt": "o\n"})
	e.git("push", "-q", "origin", "work-main:refs/heads/main")
	e.git("checkout", "-q", "main")
	e.git("branch", "-q", "-D", "work-main")
	// A worktree with a tracked change, and a parked branch.
	wt := filepath.Join(e.work, ".worktrees", "dirty")
	e.git("worktree", "add", "-q", "-b", "feat/dirty-tree", wt, "origin/main")
	e.write(filepath.Join(wt, "README.md"), "changed\n")
	e.run(wt, "git", "add", "README.md")
	e.run(wt, "git", "commit", "-q", "-m", "x")
	// A detached worktree outside the repository (the lander's train checkout) is not a session's work.
	out2 := filepath.Join(e.tmp, "state", "train-src")
	e.git("worktree", "add", "-q", "--detach", out2, "origin/main")
	e.write(filepath.Join(out2, "README.md"), "lander scratch\n")
	e.git("push", "-q", "origin", "feat/never:refs/heads/wip/never")
	e.write(filepath.Join(wt, "README.md"), "changed again\n")

	out, code := run()
	if code != 1 {
		t.Errorf("exit %d, want 1\n%s", code, out)
	}
	for _, want := range []string{"  branch feat/never: 1 commit(s)", "  worktree " + wt + ": uncommitted changes", "  wip never: parked on origin"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q\n%s", want, out)
		}
	}
	if strings.Contains(out, "train-src") {
		t.Errorf("a worktree outside .worktrees/ is not listed\n%s", out)
	}
	if !strings.Contains(out, "not queued") || strings.Contains(out, "never queued") {
		t.Errorf("the branch line says not queued\n%s", out)
	}
	for _, not := range []string{"feat/queued", "feat/rejected", "feat/landed"} {
		if strings.Contains(out, "branch "+not) {
			t.Errorf("%s is queued, rejected or landed, not unlanded\n%s", not, out)
		}
	}

	// make status prints the same lines under `unlanded:`.
	e.withStatusGH()
	if st := e.status(); !strings.Contains(st, "unlanded:\n  branch feat/") || !strings.Contains(st, "  wip never: parked on origin") {
		t.Errorf("status lacks the unlanded section\n%s", st)
	}
}

// TestParkedBranchIsClassedAndDeletedByReady pins the two sides of a parked change: make issues
// lists wip/<slug> as class `parked`, and make ready deletes it once ready/<slug> is pushed.
func TestParkedBranchIsClassedAndDeletedByReady(t *testing.T) {
	e := newLandEnv(t)
	e.git("checkout", "-q", "-b", "feat/parked-x")
	e.commitFiles("wip: x\x1b[31mred", map[string]string{"x.txt": "x\n"})
	e.git("push", "-q", "origin", "feat/parked-x:refs/heads/wip/parked-x")

	out, _, code := e.acScript("issue-queue.sh", []string{"FAKE_ISSUES=[]"})
	if code != 0 || !strings.HasPrefix(out, "wip/parked-x parked parked: ") || !strings.Contains(out, " — wip: x[31mred\n") {
		t.Errorf("the queue must list the parked branch first, class parked (exit %d)\n%s", code, out)
	}

	m := filepath.Join(e.tmp, "msg")
	e.write(m, "feat(client): parked x\n")
	if out, errb, code := e.acScript("ready.sh", []string{"MSG=" + m, "READY_NO_LANDER=1", "REVIEW=full, 1 rounds"}); code != 0 {
		t.Fatalf("ready failed (exit %d)\n%s%s", code, out, errb)
	}
	if got := e.refs("ready/"); got != "parked-x" {
		t.Errorf("queued %q, want parked-x", got)
	}
	if got := e.refs("wip/"); got != "" {
		t.Errorf("ready must delete wip/parked-x once queued, left %q", got)
	}
}
