package landertest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// #2438: before the bisection rejects a change, a failing test with an open flaky issue, or a
// failed package the change does not touch, buys the train up to it one more gate, once: green,
// it lands and a flaky issue is opened; red, it is rejected as before.
func TestLanderGatesASuspectOnceMoreBeforeRejectingIt(t *testing.T) {
	const far = "panic: boom\nFAIL\tgithub.com/BeppeTemp/cartographer/internal/far\t0.1s\n"
	flaky := `FAKE_ISSUES=[{"title":"flaky (2nd): kb.TestSometimes TestOther failed once and passed on a re-run"}]`
	for _, c := range []struct {
		name   string
		files  map[string]string
		extra  string
		gates  int
		landed bool
		why    string
	}{
		{"an open flaky issue, green on the gate", map[string]string{"FLAKY2": "--- FAIL: TestSometimes (0.01s)\n"}, flaky, 3, true, "TestSometimes has an open flaky issue"},
		{"no flaky issue: rejected", map[string]string{"FLAKY2": "--- FAIL: TestSometimes (0.01s)\n"}, "FAKE_ISSUES=[]", 2, false, ""},
		{"another test's flaky issue: rejected", map[string]string{"FLAKY2": "--- FAIL: TestSometimesNot (0.01s)\n"}, flaky, 2, false, ""},
		{"a package it does not touch, green on the gate", map[string]string{"FLAKY2": far}, "FAKE_ISSUES=[]", 3, true, "internal_far fails and it changes no file there"},
		{"a package it touches: rejected", map[string]string{"FLAKY2": far, "internal/far/x.go": "package far\n"}, "FAKE_ISSUES=[]", 2, false, ""},
		{"red on the gate too: rejected, once", map[string]string{"BAD": "x\n", "FLAKY2": "--- FAIL: TestSometimes (0.01s)\n"}, flaky, 3, false, "TestSometimes has an open flaky issue"},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newLandEnv(t)
			e.queue("s", "fix(kb): s\n", "2026-01-01T10:00:00Z", c.files)
			out, ok := e.land(c.extra)
			if !ok {
				t.Fatalf("land failed\n%s", out)
			}
			if n := strings.Count(e.makeLog(), "-k gate"); n != c.gates {
				t.Errorf("%d gates, want %d\n%s", n, c.gates, out)
			}
			if landed := strings.Join(e.mainSubjects(), "|") == "fix(kb): s"; landed != c.landed || (e.refs("failed/") == "s") == c.landed {
				t.Errorf("landed=%v, want %v (failed=%q)\n%s", landed, c.landed, e.refs("failed/"), out)
			}
			if c.why != "" && !strings.Contains(out, "before rejecting s: "+c.why+": gating the train up to it once more") {
				t.Errorf("the lander says why it gates again (%q):\n%s", c.why, out)
			}
			if c.why == "" && strings.Contains(out, "once more") {
				t.Errorf("no reason for one more gate:\n%s", out)
			}
			if opened := strings.Contains(e.log(), "issue create"); opened != c.landed {
				t.Errorf("a flaky issue is opened only when the suspect is cleared: %v\n%s", opened, e.log())
			}
		})
	}
}

// #2438: a cleared suspect lands with the prefix before it on the gate that cleared it, and the
// units after it stay queued for the next train.
func TestLanderLandsTheClearedPrefixAndKeepsTheRestQueued(t *testing.T) {
	e := newLandEnv(t)
	e.queue("s", "fix(kb): s\n", "2026-01-01T10:00:00Z", map[string]string{"FLAKY2": "--- FAIL: TestSometimes (0.01s)\n"})
	e.queue("later", "fix(kb): later\n", "2026-01-01T11:00:00Z", map[string]string{"l.txt": "l\n"})
	// Red, red again, the prefix s red (the bisection names s), then green on its one more gate.
	out, ok := e.land("FAKE_FLAKY2=3", `FAKE_ISSUES=[{"title":"flaky: TestSometimes failed once and passed on a re-run"}]`)
	if !ok || !strings.Contains(out, "green on that gate: s is not rejected") {
		t.Fatalf("s is cleared (ok=%v)\n%s", ok, out)
	}
	if !regexp.MustCompile(`train \S+ landed: 1 change\(s\), \S+ from its composition, 4 gate\(s\)`).MatchString(out) {
		t.Errorf("the first train lands s alone, on its fourth gate:\n%s", out)
	}
	if got := strings.Join(e.mainSubjects(), "|"); got != "fix(kb): s|fix(kb): later" || e.refs("failed/") != "" || strings.Count(e.log(), "pr create") != 2 {
		t.Errorf("s lands, later stays queued and lands in the next train, nothing rejected: main %q, failed %q\n%s", got, e.refs("failed/"), out)
	}
}

type landEvent struct {
	At, Event, Train, Slug, Reason, Result, Slugs, Why string
	Changes, Secs, Gates, Probes                       *int
}

func (e *landEnv) events() []landEvent {
	e.t.Helper()
	var evs []landEvent
	for _, l := range strings.Split(strings.TrimSpace(readTmpOrEmpty(filepath.Join(e.tmp, "lander"), "events.jsonl")), "\n") {
		if l == "" {
			continue
		}
		var ev landEvent
		if err := json.Unmarshal([]byte(l), &ev); err != nil {
			e.t.Fatalf("events.jsonl: %q: %v", l, err)
		}
		evs = append(evs, ev)
	}
	return evs
}

// #2438: every log line carries an ISO time, events.jsonl records what happened to each train
// (composed, gated, rejected with the reason, landed, died), and a landed train logs its whole
// cost, every gate of it, not the last run's duration.
func TestLanderLogsISOTimesEventsAndTheTrainsTotal(t *testing.T) {
	e := newLandEnv(t)
	e.queue("a", "fix(kb): a\n", "2026-01-01T10:00:00Z", map[string]string{"a2.txt": "a\n"})
	e.queue("bad", "fix(kb): bad\n", "2026-01-01T11:00:00Z", map[string]string{"BAD": "x\n"})
	out, ok := e.land()
	if !ok {
		t.Fatalf("land failed\n%s", out)
	}
	iso := regexp.MustCompile(`^land: \d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ `)
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "land: ") && !iso.MatchString(l) {
			t.Errorf("a log line without an ISO time: %q", l)
		}
	}
	// Red, red again, the prefix a (green): three gates before a lands alone.
	if !regexp.MustCompile(`train train/\S+ landed: 1 change\(s\), \d+m\d\ds from its composition, 3 gate\(s\) and 0 probe\(s\) in all`).MatchString(out) {
		t.Errorf("the train's total is logged:\n%s", out)
	}
	var kinds []string
	for _, ev := range e.events() {
		kinds = append(kinds, ev.Event)
		if _, err := time.Parse(time.RFC3339, ev.At); err != nil {
			t.Errorf("event %s: at %q", ev.Event, ev.At)
		}
		switch ev.Event {
		case "composed":
			if !strings.HasPrefix(ev.Train, "train/") || ev.Changes == nil || *ev.Changes != 2 || ev.Slugs != "a bad" {
				t.Errorf("composed: %+v", ev)
			}
		case "rejected":
			if ev.Slug != "bad" || !strings.Contains(ev.Reason, "the train gate is red with it") {
				t.Errorf("rejected: %+v", ev)
			}
		case "landed":
			if ev.Slugs != "a" || ev.Changes == nil || *ev.Changes != 1 || ev.Gates == nil || *ev.Gates != 3 || ev.Secs == nil || ev.Probes == nil {
				t.Errorf("landed: %+v", ev)
			}
		}
	}
	if got := strings.Join(kinds, " "); got != "composed gated gated gated rejected landed" {
		t.Errorf("the events of the train: %q", got)
	}
	// A lander that stops on an error records why; a dry run and --status record nothing.
	e = newLandEnv(t)
	e.queue("a", "fix(kb): a\n", "2026-01-01T10:00:00Z", map[string]string{"a2.txt": "a\n"})
	e.landArgs([]string{"--dry-run"})
	e.landArgs([]string{"--status"})
	if evs := e.events(); len(evs) != 0 {
		t.Errorf("a dry run or --status wrote events: %+v", evs)
	}
	if out, ok := e.land("FAKE_PERM=READ"); ok {
		t.Fatalf("a contributor's lander must stop\n%s", out)
	}
	if evs := e.events(); len(evs) != 1 || evs[0].Event != "died" || !strings.Contains(evs[0].Why, "only a maintainer lands") {
		t.Errorf("died, with why: %+v", evs)
	}
}

// #2438: --daemon rotates the log and the events past LAND_LOG_MAX before it starts a lander.
func TestLanderDaemonRotatesItsLog(t *testing.T) {
	e := newLandEnv(t)
	state := filepath.Join(e.tmp, "lander")
	e.write(filepath.Join(state, "log"), strings.Repeat("old line\n", 20))
	e.write(filepath.Join(state, "log.1"), "older\n")
	e.write(filepath.Join(state, "events.jsonl"), "{}\n")
	out, rc := e.landArgs([]string{"--daemon"}, "LAND_LOG_MAX=100")
	if rc != 0 || !strings.Contains(out, "land: lander started (pid ") {
		t.Fatalf("--daemon (rc=%d):\n%s", rc, out)
	}
	deadline := time.Now().Add(time.Minute)
	for {
		_, err := os.Stat(filepath.Join(state, "pid"))
		if strings.Contains(readTmpOrEmpty(state, "log"), "the queue is empty") && os.IsNotExist(err) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the detached lander did not finish:\n%s", readTmpOrEmpty(state, "log"))
		}
		time.Sleep(100 * time.Millisecond)
	}
	if log := readTmpOrEmpty(state, "log"); strings.Contains(log, "old line") {
		t.Errorf("the new log starts empty:\n%s", log)
	}
	if got := readTmpOrEmpty(state, "log.1"); !strings.HasPrefix(got, "old line\n") {
		t.Errorf("log.1 is the previous log: %q", got)
	}
	if got := readTmpOrEmpty(state, "log.2"); got != "older\n" {
		t.Errorf("log.2 is the one before: %q", got)
	}
	if got := readTmpOrEmpty(state, "events.jsonl"); got != "{}\n" {
		t.Errorf("events.jsonl is under the limit and stays: %q", got)
	}
}
