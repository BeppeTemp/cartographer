package landertest

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// landStatus is $state/status.json, as land.sh writes it.
type landStatus struct {
	Phase       string   `json:"phase"`
	Gate        string   `json:"gate"`
	Branch      *string  `json:"branch"`
	Train       []string `json:"train"`
	Run         *int     `json:"run"`
	Why         *string  `json:"why"`
	NotLandable []struct {
		Unit, Reason string
	} `json:"not_landable"`
	LastRejection *struct {
		Slug, Reason string
	} `json:"last_rejection"`
}

func (e *landEnv) statusFile(path string) landStatus {
	e.t.Helper()
	var st landStatus
	if err := json.Unmarshal([]byte(readFileAbs(e.t, path)), &st); err != nil {
		e.t.Fatalf("%s: %v", path, err)
	}
	return st
}

// A fetch of origin that fails is tried again before the lander gives up (LAND_INFRA_TRIES), and
// one that keeps failing stops it with the queue untouched.
func TestLanderRetriesAFailedFetch(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not found")
	}
	e := newLandEnv(t)
	e.write(filepath.Join(e.bin, "git"), `#!/bin/sh
case " $1 $2 $3 " in *" fetch "*)
  if [ -n "${FAKE_FETCH_FAILS:-}" ]; then
    n=$(cat "$FAKE_STATE.fetch" 2>/dev/null || echo 0); echo $((n + 1)) > "$FAKE_STATE.fetch"
    [ "$n" -ge "$FAKE_FETCH_FAILS" ] || { echo "fatal: unable to access origin" >&2; exit 128; }
  fi ;;
esac
exec `+git+` "$@"
`)
	e.queue("x", "fix(kb): x\n", "2026-01-01T10:00:00Z", map[string]string{"x.txt": "x\n"})
	out, ok := e.land("FAKE_FETCH_FAILS=2")
	if !ok || !strings.Contains(out, "cannot fetch origin: trying again (1/3 done") || !strings.Contains(out, "(2/3 done") {
		t.Fatalf("two failed fetches are tried again, then it lands (ok=%v)\n%s", ok, out)
	}
	if got := strings.Join(e.mainSubjects(), "|"); got != "fix(kb): x" {
		t.Errorf("main holds %q", got)
	}
	e.queue("y", "fix(kb): y\n", "2026-01-01T10:00:00Z", map[string]string{"y.txt": "y\n"})
	_ = os.Remove(filepath.Join(e.tmp, "gh-state.fetch"))
	out, ok = e.land("FAKE_FETCH_FAILS=9")
	if ok || !strings.Contains(out, "cannot fetch origin, in 3 tries: the queue is untouched") || e.refs("ready/") != "y" {
		t.Errorf("a fetch that keeps failing stops the lander, the queue untouched (ok=%v, ready=%q)\n%s", ok, e.refs("ready/"), out)
	}
	if st := e.statusFile(filepath.Join(e.tmp, "lander", "status.json")); st.Phase != "stopped" || st.Why == nil || !strings.Contains(*st.Why, "cannot fetch origin") {
		t.Errorf("status.json says it stopped and why: %+v", st)
	}
}

// A release listing that cannot be read is neither open nor closed: the last state read stands, and
// while it freezes a unit and nothing else lands the lander reads it again instead of stopping.
func TestLanderWaitsOnAnUnreadableFreeze(t *testing.T) {
	e := newLandEnv(t)
	// Unknown for FAKE_UNKNOWN reads, then closed; written after each queue (which commits the tree).
	fake := func() {
		e.write(filepath.Join(e.work, "scripts", "freeze-open.sh"), `#!/bin/sh
n=$(cat "$FAKE_STATE.freeze" 2>/dev/null || echo 0); echo $((n + 1)) > "$FAKE_STATE.freeze"
[ "$n" -ge "${FAKE_UNKNOWN:-0}" ] || exit 3
exit 1
`)
	}
	e.queue("feature", "feat(kb): feature\n", "2026-01-01T10:00:00Z", map[string]string{"f.txt": "f\n"})
	fake()
	// Never read before: unknown is open, the feat waits, and lands once the listing reads closed.
	out, ok := e.land("FAKE_UNKNOWN=3")
	if !ok || strings.Count(out, "the release listing cannot be read") != 1 || strings.Contains(out, "nothing landable") {
		t.Fatalf("the lander waits for the listing, saying so once (ok=%v)\n%s", ok, out)
	}
	if got := strings.Join(e.mainSubjects(), "|"); got != "feat(kb): feature" {
		t.Errorf("main holds %q\n%s", got, out)
	}
	// Read closed once: an unreadable listing afterwards keeps it closed, the feat lands at once.
	e.git("checkout", "--", "scripts/freeze-open.sh")
	e.queue("other", "feat(kb): other\n", "2026-01-01T10:00:00Z", map[string]string{"o.txt": "o\n"})
	fake()
	_ = os.Remove(filepath.Join(e.tmp, "gh-state.freeze"))
	out, ok = e.land("FAKE_UNKNOWN=99")
	if !ok || strings.Contains(out, "the release listing cannot be read") || !strings.Contains(strings.Join(e.mainSubjects(), "|"), "feat(kb): other") {
		t.Errorf("the last state read (closed) stands (ok=%v)\n%s", ok, out)
	}
}

// Every gh call is bounded: one that hangs (here with a child holding its output open) is killed
// past LAND_GH_TIMEOUT and read as a failed call.
func TestLanderBoundsAHangingGhCall(t *testing.T) {
	e := newLandEnv(t)
	if err := os.Rename(filepath.Join(e.bin, "gh"), filepath.Join(e.bin, "gh-fake")); err != nil {
		t.Fatal(err)
	}
	e.write(filepath.Join(e.bin, "gh"), `#!/bin/bash
if [ "$1 $2" = "pr list" ] && [ ! -e "$FAKE_STATE.slept" ]; then touch "$FAKE_STATE.slept"; sleep 60; fi
exec "$(dirname "$0")/gh-fake" "$@"
`)
	e.queue("x", "fix(kb): x\n", "2026-01-01T10:00:00Z", map[string]string{"x.txt": "x\n"})
	start := time.Now()
	out, ok := e.land("LAND_GH_TIMEOUT=2")
	if !ok || time.Since(start) > 45*time.Second || strings.Join(e.mainSubjects(), "|") != "fix(kb): x" {
		t.Errorf("the hanging call is cut and the train lands (ok=%v, %s)\n%s", ok, time.Since(start), out)
	}
}

// status.json follows the lander: the train, its branch, the hosted run and the phase while it
// merges; at the end the phase is stopped with why, the units that could not land with their
// reason, and the last rejection.
func TestLanderWritesItsStatusFile(t *testing.T) {
	e := newLandEnv(t)
	e.queue("x", "fix(kb): x\n", "2026-01-01T10:00:00Z", map[string]string{"x.txt": "x\n"})
	e.queue("held", "fix(kb): held\n\nLander-Hold: yes\n", "2026-01-01T10:01:00Z", map[string]string{"h.txt": "h\n"})
	out, rc := e.landArgs(nil) // hosted: the signoff names the ci.yml run
	if rc != 0 {
		t.Fatalf("land failed\n%s", out)
	}
	m := e.statusFile(filepath.Join(e.tmp, "gh-state.status-at-merge"))
	if m.Phase != "merging" || strings.Join(m.Train, " ") != "x" || m.Branch == nil || !strings.HasPrefix(*m.Branch, "train/") || m.Run == nil || *m.Run != 7 || m.Gate != "hosted" {
		t.Errorf("at the merge: %+v", m)
	}
	if fi, err := os.Stat(filepath.Join(e.tmp, "lander")); runtime.GOOS != "windows" && (err != nil || fi.Mode().Perm() != 0o700) {
		t.Errorf("the state directory is private to the user: %v %v", fi.Mode(), err)
	}
	st := e.statusFile(filepath.Join(e.tmp, "lander", "status.json"))
	if st.Phase != "stopped" || st.Why == nil || *st.Why != "nothing landable" || len(st.NotLandable) != 1 || st.NotLandable[0].Unit != "held" || st.NotLandable[0].Reason != "held" {
		t.Errorf("at the end: %+v", st)
	}

	e = newLandEnv(t)
	e.queue("bad", "fix(kb): bad\n", "2026-01-01T10:00:00Z", map[string]string{"BAD": "x\n"})
	if out, ok := e.land(); !ok {
		t.Fatalf("land failed\n%s", out)
	}
	st = e.statusFile(filepath.Join(e.tmp, "lander", "status.json"))
	if st.LastRejection == nil || st.LastRejection.Slug != "bad" || !strings.Contains(st.LastRejection.Reason, "red") || st.Why == nil || *st.Why != "the queue is empty" {
		t.Errorf("the last rejection is kept: %+v", st)
	}
	// --status and a dry run write nothing.
	_ = os.Remove(filepath.Join(e.tmp, "lander", "status.json"))
	e.landArgs([]string{"--status"})
	e.landArgs([]string{"--dry-run"})
	if _, err := os.Stat(filepath.Join(e.tmp, "lander", "status.json")); err == nil {
		t.Errorf("--status or --dry-run wrote status.json")
	}
}

// make ready refuses a review record below the tier the triage computes for the change, which the
// lander would reject only once the change had waited in the queue.
func TestReadyRefusesAReviewBelowTheTriageTier(t *testing.T) {
	e := newLandEnv(t)
	e.git("checkout", "-q", "-b", "feat/tool")
	e.write(filepath.Join(e.work, "scripts", "tool.sh"), "#!/bin/sh\necho tool\n")
	e.git("add", ".")
	e.git("commit", "-q", "-m", "wip")
	m := filepath.Join(e.tmp, "msg")
	e.write(m, "feat(dev): a tool\n")
	ready := func(review string) (string, bool) {
		cmd := exec.Command("bash", filepath.Join(e.work, "scripts", "ready.sh"))
		cmd.Dir = e.work
		cmd.Env = append(append([]string{}, e.env...), "MSG="+m, "READY_NO_LANDER=1", "REVIEW="+review)
		out, err := cmd.CombinedOutput()
		return string(out), err == nil
	}
	if out, ok := ready("light, 1 rounds"); ok || !strings.Contains(out, "REVIEW records a light review, and the triage computes full") || e.refs("ready/") != "" {
		t.Errorf("a light record on a full-tier change is refused, nothing queued (ok=%v)\n%s", ok, out)
	}
	if out, ok := ready("full, 2 rounds"); !ok || e.refs("ready/") != "tool" {
		t.Errorf("a full record is queued (ok=%v)\n%s", ok, out)
	}
}
