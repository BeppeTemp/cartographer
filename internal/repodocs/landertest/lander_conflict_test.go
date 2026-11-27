package landertest

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// #2439: make ready warns when the change conflicts with one already queued, naming the slug and
// AFTER=<slug>, and queues it all the same; a change that merges with the queue warns of nothing.
func TestReadyWarnsOfAConflictWithAQueuedChange(t *testing.T) {
	e := newLandEnv(t)
	e.queue("other", "fix(kb): other\n", "2026-01-01T10:00:00Z", map[string]string{"a.txt": "theirs\n"})
	ready := func(branch, file, body string) (string, bool) {
		t.Helper()
		e.git("checkout", "-q", "-B", "feat/"+branch, "origin/main")
		e.write(filepath.Join(e.work, file), body)
		e.git("add", ".")
		e.git("commit", "-q", "-m", "wip")
		m := filepath.Join(e.tmp, "msg")
		e.write(m, "docs: "+branch+"\n")
		cmd := exec.Command("bash", filepath.Join(e.work, "scripts", "ready.sh"))
		cmd.Dir = e.work
		cmd.Env = append(append([]string{}, e.env...), "MSG="+m, "READY_NO_LANDER=1", "REVIEW=full, 1 rounds")
		out, err := cmd.CombinedOutput()
		return string(out), err == nil
	}
	out, ok := ready("mine", "a.txt", "mine\n")
	if !ok || !strings.Contains(out, "warning: it conflicts with the queued change other (a.txt)") || !strings.Contains(out, "AFTER=other") {
		t.Errorf("the conflict is named with the slug and AFTER (ok=%v)\n%s", ok, out)
	}
	if got := strings.Join(strings.Fields(e.refs("ready/")), " "); got != "mine other" {
		t.Errorf("it is queued all the same: %q", got)
	}
	// Queued again, it does not conflict with itself; another file conflicts with nothing.
	if out, ok := ready("mine", "a.txt", "mine again\n"); !ok || strings.Count(out, "it conflicts with") != 1 {
		t.Errorf("queued again: one warning, for other (ok=%v)\n%s", ok, out)
	}
	if out, ok := ready("apart", "b.txt", "b\n"); !ok || strings.Contains(out, "it conflicts with") {
		t.Errorf("no conflict, no warning (ok=%v)\n%s", ok, out)
	}
}

// #2439: a fix that conflicts only with a change picked before it in the same train is left
// queued, not rejected: it lands when the gate rejects that change, and is rejected as a
// conflict only once that change is on main. Another type is rejected at its pick, as before.
func TestLanderLeavesAFixThatConflictsWithItsTrainQueued(t *testing.T) {
	const said = "y conflicts with x, picked before it in this train, and applies on main without it: left queued"
	t.Run("the earlier change is rejected by the gate", func(t *testing.T) {
		e := newLandEnv(t)
		e.queue("x", "fix(kb): x\n", "2026-01-01T10:00:00Z", map[string]string{"a.txt": "x\n", "BAD": "x\n"})
		e.queue("y", "fix(kb): y\n", "2026-01-01T11:00:00Z", map[string]string{"a.txt": "y\n"})
		out, ok := e.land()
		if !ok || !strings.Contains(out, said) {
			t.Fatalf("y is left queued (ok=%v)\n%s", ok, out)
		}
		if got := strings.Join(e.mainSubjects(), "|"); got != "fix(kb): y" || e.refs("failed/") != "x" || e.refs("ready/") != "" {
			t.Errorf("x is rejected by the gate and y lands: main %q, failed %q, ready %q\n%s", got, e.refs("failed/"), e.refs("ready/"), out)
		}
	})
	t.Run("the earlier change lands", func(t *testing.T) {
		e := newLandEnv(t)
		e.queue("x", "fix(kb): x\n", "2026-01-01T10:00:00Z", map[string]string{"a.txt": "x\n"})
		e.queue("y", "fix(kb): y\n", "2026-01-01T11:00:00Z", map[string]string{"a.txt": "y\n"})
		out, ok := e.land()
		if !ok || strings.Count(out, said) != 1 {
			t.Fatalf("y is left queued once (ok=%v)\n%s", ok, out)
		}
		if got := strings.Join(e.mainSubjects(), "|"); got != "fix(kb): x" || e.refs("failed/") != "y" {
			t.Errorf("x lands, then y conflicts with main and is rejected: main %q, failed %q\n%s", got, e.refs("failed/"), out)
		}
		if r := e.upGit("log", "-1", "--format=%s", "failed/y"); !strings.HasPrefix(r, "lander: conflict") {
			t.Errorf("the reason is the conflict, got %q", r)
		}
		if n := strings.Count(e.makeLog(), "-k gate"); n != 1 {
			t.Errorf("one gate, of x alone: %d\n%s", n, out)
		}
	})
	// Only merge-tree's exit 1 is a conflict: a git that fails the test-merge of a train commit any
	// other way (here from its second merge-tree on) leaves the rejection as it was.
	t.Run("a merge-tree that fails is not a conflict", func(t *testing.T) {
		git, err := exec.LookPath("git")
		if err != nil {
			t.Skip("git not found")
		}
		e := newLandEnv(t)
		e.write(filepath.Join(e.bin, "git"), `#!/bin/sh
case " $* " in *" merge-tree "*)
  n=$(cat "$FAKE_STATE.mt" 2>/dev/null || echo 0); echo $((n + 1)) > "$FAKE_STATE.mt"
  [ "$n" -lt 1 ] || exit 128 ;;
esac
exec `+git+` "$@"
`)
		e.queue("x", "fix(kb): x\n", "2026-01-01T10:00:00Z", map[string]string{"a.txt": "x\n"})
		e.queue("y", "fix(kb): y\n", "2026-01-01T11:00:00Z", map[string]string{"a.txt": "y\n"})
		out, ok := e.land()
		if !ok || strings.Contains(out, "left queued") || e.refs("failed/") != "y" || strings.Join(e.mainSubjects(), "|") != "fix(kb): x" {
			t.Errorf("y is rejected at its pick (ok=%v, failed %q)\n%s", ok, e.refs("failed/"), out)
		}
	})
	t.Run("not a fix", func(t *testing.T) {
		e := newLandEnv(t)
		e.queue("x", "fix(kb): x\n", "2026-01-01T10:00:00Z", map[string]string{"a.txt": "x\n", "BAD": "x\n"})
		e.queue("y", "docs(kb): y\n", "2026-01-01T11:00:00Z", map[string]string{"a.txt": "y\n"})
		out, ok := e.land()
		if !ok || strings.Contains(out, "left queued") || strings.Join(strings.Fields(e.refs("failed/")), " ") != "x y" {
			t.Errorf("a docs change that conflicts is rejected at its pick (ok=%v, failed %q)\n%s", ok, e.refs("failed/"), out)
		}
	})
}
