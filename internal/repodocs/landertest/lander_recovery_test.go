package landertest

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// failingGh puts a wrapper before the fake gh: the first FAKE_FAILS calls whose first two words
// are FAKE_FAIL_CMD fail with FAKE_FAIL_ERR on stderr, the others reach the fake.
func failingGh(t *testing.T, bin string, write func(path, body string)) {
	t.Helper()
	if err := os.Rename(filepath.Join(bin, "gh"), filepath.Join(bin, "gh-fake")); err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(bin, "gh"), `#!/bin/bash
if [ "$1 $2" = "${FAKE_FAIL_CMD:-}" ]; then
  n=$(cat "$FAKE_GH_LOG.fails" 2>/dev/null || echo 0); echo $((n + 1)) > "$FAKE_GH_LOG.fails"
  if [ "$n" -lt "${FAKE_FAILS:-0}" ]; then echo "$*" >> "$FAKE_GH_LOG"; echo "$FAKE_FAIL_ERR" >&2; exit 1; fi
fi
exec "$(dirname "$0")/gh-fake" "$@"
`)
}

// #2438: the status POST and the gh reads of the signoff are tried again on a transport error
// (the connection, a 5xx), LAND_INFRA_TRIES times, and only then: a 4xx is an answer.
func TestSignoffRetriesAGhCallOnATransportErrorOnly(t *testing.T) {
	for _, c := range []struct {
		name, cmd, err string
		fails, calls   int
		ok             bool
	}{
		{"the POST, a dropped connection twice", "api -X", "error connecting to api.github.com", 2, 3, true},
		{"the POST, a 5xx once", "api -X", "HTTP 502: Bad Gateway", 1, 2, true},
		{"the POST, a 422 is an answer", "api -X", "HTTP 422: Validation Failed", 1, 1, false},
		{"the POST, a connection that stays down", "api -X", "dial tcp: lookup api.github.com: no such host", 9, 3, false},
		{"the permission read, a dropped connection", "repo view", "error connecting to api.github.com", 2, 3, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newSignoffEnv(t)
			failingGh(t, filepath.Join(filepath.Dir(e.makeLog), "bin"), func(p, b string) { e.write(p, b, 0o755) })
			e.branch(e.work, "feat/x", "x.txt")
			out, ok := e.signoff(e.work, "LAND_INFRA_WAIT=0", "FAKE_FAIL_CMD="+c.cmd, "FAKE_FAIL_ERR="+c.err, "FAKE_FAILS="+strconv.Itoa(c.fails))
			if ok != c.ok {
				t.Fatalf("ok=%v, want %v\n%s", ok, c.ok, out)
			}
			n := 0
			for _, l := range strings.Split(e.log(e.ghLog), "\n") {
				if strings.HasPrefix(l, c.cmd+" ") {
					n++
				}
			}
			if n != c.calls {
				t.Errorf("%d calls of gh %s, want %d\n%s", n, c.cmd, c.calls, out)
			}
			if c.ok && c.fails > 0 && !strings.Contains(out, "GitHub did not answer") && c.cmd == "api -X" {
				t.Errorf("the retry is said:\n%s", out)
			}
		})
	}
}

// #2438: with SIGNOFF_ADOPT the hosted signoff dispatches nothing: the run that already ran this
// sha is the gate, and the status is posted only when that run is green.
func TestSignoffAdoptsTheRunOfTheSameSha(t *testing.T) {
	green := hostedRun("completed", "completed success", nil)
	red := hostedRun("completed", "completed success", map[string]string{"test-macos": "completed failure"})
	for _, c := range []struct {
		name, before, jobs, want string
		ok                       bool
	}{
		{"a green run", `[{"databaseId":5},{"databaseId":7}]`, green, "ci.yml run 7 already ran this tree", true},
		{"a red run", `[{"databaseId":7}]`, red, "the jobs test-macos of ci.yml run 7 are red", false},
		{"no run", `[]`, green, "no ci.yml run of", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newSignoffEnv(t)
			e.branch(e.work, "feat/x", "x.txt")
			out, ok := e.signoff(e.work, "SIGNOFF_GATE=hosted", "SIGNOFF_ADOPT=1", "FAKE_GH_BEFORE="+c.before, "FAKE_GH_JOBS="+c.jobs)
			if ok != c.ok || !strings.Contains(out, c.want) {
				t.Fatalf("ok=%v, want %v and %q\n%s", ok, c.ok, c.want, out)
			}
			if strings.Contains(e.log(e.ghLog), "workflow run") {
				t.Errorf("an adopted gate dispatches nothing:\n%s", e.log(e.ghLog))
			}
			if got := len(e.posts()); (got == 1) != c.ok {
				t.Errorf("%d status posted, ok=%v", got, c.ok)
			}
			if c.ok && !strings.HasPrefix(e.description(), "gate=hosted run=7 legs=8 ") {
				t.Errorf("the status names the adopted run: %q", e.description())
			}
		})
	}
}

// #2438: a train this machine left on origin (the lander stopped after its push) whose changes
// are still queued lands on the ci.yml run of its own commit: nothing is composed or dispatched
// again. With no such run, a red one, or a change queued again since, it is deleted and the queue
// composed again, as before.
func TestLanderLandsItsLeftoverTrainOnTheRunOfItsTree(t *testing.T) {
	setup := func(t *testing.T, tip string) (*landEnv, string) {
		e := newLandEnv(t)
		e.queue("a", "fix(kb): a\n\nLander-Tip: "+tip+"\n", "2026-01-01T10:00:00Z", map[string]string{"t-a.txt": "a\n"})
		head, _ := e.trainBranch("20260101-000000", "origin/main", "a")
		e.write(filepath.Join(e.tmp, "lander", "branch"), "train/20260101-000000\n")
		return e, head
	}
	t.Run("a green run", func(t *testing.T) {
		e, head := setup(t, "tip-a")
		e.write(filepath.Join(e.tmp, "gh-state.dispatch"), "1\n") // the run the dead lander dispatched
		out, rc := e.landArgs(nil)
		if rc != 0 || !strings.Contains(out, "its ci.yml run is its gate") {
			t.Fatalf("the leftover train is resumed (rc=%d)\n%s", rc, out)
		}
		gh := e.log()
		if strings.Contains(gh, "workflow run") || !strings.Contains(gh, "statuses/"+head) || strings.Count(gh, "pr create") != 1 {
			t.Errorf("no dispatch, the status on the train's head, one PR:\n%s", gh)
		}
		if got := e.upGit("rev-parse", "main"); got != head || e.refs("ready/") != "" || e.refs("train/") != "" {
			t.Errorf("main is the train's head, the queue and the train branch are gone: main %s, ready %q, train %q\n%s", got, e.refs("ready/"), e.refs("train/"), out)
		}
	})
	t.Run("no run", func(t *testing.T) {
		e, _ := setup(t, "tip-a")
		out, rc := e.landArgs(nil)
		if rc != 0 || !strings.Contains(out, "has no green ci.yml run to land on: deleted") {
			t.Fatalf("no run to adopt: deleted and composed again (rc=%d)\n%s", rc, out)
		}
		if n := strings.Count(e.log(), "workflow run"); n != 1 || strings.Join(e.mainSubjects(), "|") != "fix(kb): a" {
			t.Errorf("one new gate, then a lands: %d dispatches, main %q\n%s", n, e.mainSubjects(), out)
		}
	})
	t.Run("a red run", func(t *testing.T) {
		e, head := setup(t, "tip-a")
		e.write(filepath.Join(e.tmp, "gh-state.dispatch"), "1\n")
		red := hostedRun("completed", "completed success", map[string]string{"test-macos": "completed failure"})
		out, _ := e.landArgs(nil, "FAKE_GH_JOBS="+red)
		if !strings.Contains(out, "has no green ci.yml run to land on: deleted") || strings.Contains(e.log(), "statuses/"+head) || len(e.mainSubjects()) != 0 {
			t.Errorf("a red run lands nothing and posts nothing: main %q\n%s", e.mainSubjects(), out)
		}
	})
	// Not the train the lander would build now: deleted, never resumed, whatever its trailers say.
	for _, c := range []struct {
		name  string
		setup func(t *testing.T) (*landEnv, []string)
	}{
		{"queued again since", func(t *testing.T) (*landEnv, []string) { e, _ := setup(t, "another-tip"); return e, nil }},
		{"no Lander-Tip on the queued change", func(t *testing.T) (*landEnv, []string) {
			e := newLandEnv(t)
			e.queue("a", "fix(kb): a\n", "2026-01-01T10:00:00Z", map[string]string{"t-a.txt": "a\n"})
			return e, nil
		}},
		{"forged content under copied trailers", func(t *testing.T) (*landEnv, []string) {
			e := newLandEnv(t)
			e.queue("a", "fix(kb): a\n\nLander-Tip: tip-a\n", "2026-01-01T10:00:00Z", map[string]string{"t-a.txt": "what was reviewed\n"})
			return e, nil
		}},
		{"a changed title", func(t *testing.T) (*landEnv, []string) {
			e := newLandEnv(t)
			e.queue("a", "fix(kb): a, renamed\n\nLander-Tip: tip-a\n", "2026-01-01T10:00:00Z", map[string]string{"t-a.txt": "a\n"})
			return e, nil
		}},
		{"held since", func(t *testing.T) (*landEnv, []string) {
			e := newLandEnv(t)
			e.queue("a", "fix(kb): a\n\nLander-Tip: tip-a\nLander-Hold: yes\n", "2026-01-01T10:00:00Z", map[string]string{"t-a.txt": "a\n"})
			return e, nil
		}},
		{"frozen since", func(t *testing.T) (*landEnv, []string) {
			e := newLandEnv(t)
			e.queue("a", "fix(kb): a\n\nBREAKING CHANGE: it breaks\n\nLander-Tip: tip-a\n", "2026-01-01T10:00:00Z", map[string]string{"t-a.txt": "a\n"})
			return e, []string{"FAKE_FREEZE=0"}
		}},
		{"a failed ref of its slug", func(t *testing.T) (*landEnv, []string) {
			e, _ := setup(t, "tip-a")
			e.upGit("update-ref", "refs/heads/failed/a", e.upGit("rev-parse", "main"))
			return e, nil
		}},
		{"an extra commit", func(t *testing.T) (*landEnv, []string) {
			e := newLandEnv(t)
			e.queue("a", "fix(kb): a\n\nLander-Tip: tip-a\n", "2026-01-01T10:00:00Z", map[string]string{"t-a.txt": "a\n"})
			e.trainBranch("20260101-000000", "origin/main", "a", "smuggled")
			return e, nil
		}},
		{"a slug twice", func(t *testing.T) (*landEnv, []string) {
			e, head := setup(t, "tip-a")
			twice := e.upGit("commit-tree", head+"^{tree}", "-p", head, "-m", "fix(kb): a\n\nLander-Slug: a\nLander-Tip: tip-a\n")
			e.upGit("update-ref", "refs/heads/train/20260101-000000", twice)
			return e, nil
		}},
		{"a merge, not a chain", func(t *testing.T) (*landEnv, []string) {
			e := newLandEnv(t)
			e.queue("a", "fix(kb): a\n\nLander-Tip: tip-a\n", "2026-01-01T10:00:00Z", map[string]string{"t-a.txt": "a\n"})
			e.queue("b", "fix(kb): b\n\nLander-Tip: tip-b\n", "2026-01-01T10:01:00Z", map[string]string{"t-b.txt": "b\n"})
			head, _ := e.trainBranch("20260101-000000", "origin/main", "b", "a")
			merge := e.upGit("commit-tree", head+"^{tree}", "-p", "main", "-p", head+"^", "-m", "fix(kb): a\n\nLander-Slug: a\nLander-Tip: tip-a\n")
			e.upGit("update-ref", "refs/heads/train/20260101-000000", merge)
			return e, nil
		}},
		{"main moved", func(t *testing.T) (*landEnv, []string) {
			e, _ := setup(t, "tip-a")
			e.moveMain("moved.txt")
			return e, nil
		}},
		{"a gate on the build host", func(t *testing.T) (*landEnv, []string) { e, _ := setup(t, "tip-a"); return e, []string{"host"} }},
	} {
		t.Run(c.name, func(t *testing.T) {
			e, extra := c.setup(t)
			if e.refs("train/") == "" {
				e.trainBranch("20260101-000000", "origin/main", "a")
			}
			e.write(filepath.Join(e.tmp, "lander", "branch"), "train/20260101-000000\n")
			e.write(filepath.Join(e.tmp, "gh-state.dispatch"), "1\n")
			var args []string
			if len(extra) == 1 && extra[0] == "host" {
				args, extra = []string{"--gate", "host"}, nil
			}
			out, _ := e.landArgs(args, extra...)
			if !strings.Contains(out, "is a train this machine left on origin with no open PR: deleted") || strings.Contains(out, "its ci.yml run is its gate") {
				t.Errorf("not resumed, deleted:\n%s", out)
			}
			if strings.Contains(strings.Join(e.mainSubjects(), "|"), "smuggled") {
				t.Errorf("nothing of the leftover train lands: %q", e.mainSubjects())
			}
		})
	}
}

// #2438: the collection window closes at once for the only queued unit when its pre-check is
// green and no check/* run is in flight; a check in flight, or a pre-check still running, keeps
// it open (TestLanderWaitsForCompanyBeforeComposing has the company arriving).
func TestLanderClosesTheWindowForALoneGreenChange(t *testing.T) {
	e := newLandEnv(t)
	e.queue("x", "fix(kb): x\n", nowDate(), map[string]string{"x.txt": "x\n"})
	if out, _ := e.landArgs([]string{"--status"}, "LAND_WINDOW=600"); strings.Contains(out, "collecting until") {
		t.Errorf("--status shows no open window:\n%s", out)
	}
	if out, _ := e.landArgs([]string{"--status"}, "LAND_WINDOW=600", `FAKE_CHECKS=[{"status":"queued"}]`); !strings.Contains(out, "collecting until") {
		t.Errorf("--status shows the window, a check is in flight:\n%s", out)
	}
	out, ok := e.land("LAND_WINDOW=20", "LAND_POLL=1")
	if !ok || strings.Contains(out, "collecting:") || strings.Join(e.mainSubjects(), "|") != "fix(kb): x" {
		t.Errorf("alone and green, no check in flight: composed at once (ok=%v)\n%s", ok, out)
	}
	for name, extra := range map[string]string{
		"a check in flight":  `FAKE_CHECKS=[{"status":"completed"},{"status":"in_progress"}]`,
		"its pre-check runs": "FAKE_PRECHECK_RUNNING=",
		"the checks unread":  "FAKE_CHECKS=not json",
	} {
		t.Run(name, func(t *testing.T) {
			e := newLandEnv(t)
			e.queue("x", "fix(kb): x\n", nowDate(), map[string]string{"x.txt": "x\n"})
			if strings.HasSuffix(extra, "=") {
				extra += e.upGit("rev-parse", "ready/x")
			}
			out, ok := e.land("LAND_WINDOW=3", "LAND_POLL=1", extra)
			if !ok || strings.Count(out, "collecting: 1 queued") != 1 || strings.Join(e.mainSubjects(), "|") != "fix(kb): x" {
				t.Errorf("the window stays open until it runs out (ok=%v)\n%s", ok, out)
			}
		})
	}
}
