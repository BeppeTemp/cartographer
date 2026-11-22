package repodocs

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The acceptance criteria of a plan travel in the change's message as `AC:` lines
// (CONTRIBUTING.md §Plans): scripts/ac-trace.sh traces them to the tests of the tree for
// the triage, scripts/ac-report.sh lists those of a range. The fixtures use the plan id Q9,
// so that no fixture string traces a real criterion.

// acScript runs scripts/<name> of e.work with extra environment; stdout, stderr, exit code.
func (e *landEnv) acScript(name string, env []string, args ...string) (string, string, int) {
	e.t.Helper()
	cmd := exec.Command("bash", append([]string{filepath.Join(e.work, "scripts", name)}, args...)...)
	cmd.Dir, cmd.Env = e.work, append(append([]string{}, e.env...), env...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	code := 0
	if err := cmd.Run(); err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			e.t.Fatalf("%s: %v", name, err)
		}
		code = ee.ExitCode()
	}
	return out.String(), errb.String(), code
}

// commitFiles writes files under e.work and commits them with msg on the current branch.
func (e *landEnv) commitFiles(msg string, files map[string]string) string {
	e.t.Helper()
	for p, body := range files {
		e.write(filepath.Join(e.work, filepath.FromSlash(p)), body)
	}
	e.git("add", ".")
	return e.commitAt(msg, "2026-01-02T00:00:00Z")
}

const noTestCiting = " has no test citing it — add the id in a comment on the test, or mark it manual or deferred"

func TestACTraceScript(t *testing.T) {
	e := newLandEnv(t)
	e.commitFiles("test: fixtures", map[string]string{
		// A test file cites AC-Q9-10 and AC-Q9-1-2, never AC-Q9-1: whole words only.
		"internal/a/a_test.go": "package a\n\n// AC-Q9-10 and AC-Q9-1-2\n",
		// Files pr-triage.sh's kind() does not call `test` trace nothing, whatever their name.
		"internal/a/a.go":       "package a\n\n// AC-Q9-3\n",
		"scripts/check_test.go": "// AC-Q9-4\n",
	})
	trace := func(body string) (string, int) {
		t.Helper()
		out, errb, code := e.acScript("ac-trace.sh", []string{"TRIAGE_BODY=" + body})
		if errb != "" {
			t.Errorf("stderr: %s", errb)
		}
		return out, code
	}

	body := strings.Join([]string{
		"feat: x", "", "Why; see AC: AC-Q9-7 test not at column 0.", "",
		"AC: AC-Q9-10 test traced by its test",
		"AC: AC-Q9-1 test a prefix of a traced id",
		"AC: AC-Q9-3 test cited by production code only",
		"AC: AC-Q9-4 test cited by a file under scripts/",
		"AC: AC-Q9-5 manual tried by hand",
		"AC: AC-Q9-6 deferred waits for the next plan",
		"AC: AC-Q9-10 manual the same id again",
		"AC: AC-Q9-8 sometimes an unknown kind",
		"AC: Q9-9 test an id without its prefix",
		"AC: AC-Q9-11 test",
	}, "\r\n") + "\r\n"
	out, code := trace(body)
	want := []string{
		"Criteria: 6 declared · 1 traced · 1 manual · 1 deferred",
		"WARN: AC-Q9-1" + noTestCiting,
		"WARN: AC-Q9-3" + noTestCiting,
		"WARN: AC-Q9-4" + noTestCiting,
		"WARN: AC-Q9-10 is declared twice — keep one AC: line per criterion",
		"WARN: unreadable AC line: AC: AC-Q9-8 sometimes an unknown kind",
		"WARN: unreadable AC line: AC: Q9-9 test an id without its prefix",
		"WARN: unreadable AC line: AC: AC-Q9-11 test",
	}
	if code != 0 || strings.TrimRight(out, "\n") != strings.Join(want, "\n") {
		t.Errorf("exit %d, output\n%s\nwant\n%s", code, out, strings.Join(want, "\n"))
	}
	if strings.Contains(out, "AC-Q9-10"+noTestCiting) || strings.Contains(out, "AC-Q9-7") {
		t.Errorf("AC-Q9-10 is traced and a line not at column 0 is no criterion\n%s", out)
	}

	// A missing test alone: the warning, exit 0, never a BLOCK.
	if out, code := trace("feat: x\n\nAC: AC-Q9-2 test nothing cites it\n"); code != 0 ||
		out != "Criteria: 1 declared · 0 traced · 0 manual · 0 deferred\nWARN: AC-Q9-2"+noTestCiting+"\n" {
		t.Errorf("exit %d, output %q", code, out)
	}
	// No AC: line, no output.
	for _, b := range []string{"", "feat: x\n\nWhy.\n\nDocs: none - wording\n"} {
		if out, code := trace(b); code != 0 || out != "" {
			t.Errorf("body %q: exit %d, output %q; want nothing", b, code, out)
		}
	}

	// The report's view of the same tree: every citation in a test file, with its line.
	cites, errb, code := e.acScript("ac-trace.sh", nil, "--citations", "HEAD")
	if code != 0 || errb != "" || cites != "AC-Q9-10\tinternal/a/a_test.go:3\nAC-Q9-1-2\tinternal/a/a_test.go:3\n" {
		t.Errorf("--citations: exit %d, stderr %q, output %q", code, errb, cites)
	}
	if _, errb, code := e.acScript("ac-trace.sh", nil, "--citations", "no-such-rev"); code != 2 || errb == "" {
		t.Errorf("an unknown rev: exit %d, stderr %q; want 2 and a message", code, errb)
	}
}

// AC-P5-3, AC-P5-4: the branch-mode triage (the review's first step, and what make ready
// runs) prints the criteria line and a WARN for a test criterion no test cites, and make
// ready, which shows only BLOCK lines, still queues the change; a message with no AC: line,
// and any pull request (no tree there), get no criteria line.
func TestPRTriageTracesTheCriteriaInBranchModeOnly(t *testing.T) {
	e := newLandEnv(t)
	e.git("checkout", "-q", "-b", "feat/traced")
	e.commitFiles("wip", map[string]string{"internal/a/a_test.go": "package a\n\n// AC-Q9-1\n"})
	msg := "feat: traced\n\nWhy.\n\nAC: AC-Q9-1 test traced\nAC: AC-Q9-2 test cited by no test\n"
	triage := func(body string) string {
		t.Helper()
		out, errb, code := e.acScript("pr-triage.sh", []string{"TRIAGE_BODY=" + body})
		if code != 0 {
			t.Fatalf("pr-triage.sh: exit %d\n%s%s", code, out, errb)
		}
		return out
	}
	out := triage(msg)
	if !strings.Contains(out, "\nCriteria: 2 declared · 1 traced · 0 manual · 0 deferred\nWARN: AC-Q9-2"+noTestCiting+"\n") ||
		strings.Contains(out, "AC-Q9-1"+noTestCiting) || strings.Contains(out, "BLOCK") {
		t.Errorf("branch mode must trace the criteria, advisory\n%s", out)
	}
	if out := triage("feat: traced\n\nWhy.\n"); strings.Contains(out, "Criteria:") || strings.Contains(out, "ac-trace") {
		t.Errorf("no AC: line, no criteria line\n%s", out)
	}

	m := filepath.Join(e.tmp, "msg")
	e.write(m, msg)
	if out, _, code := e.acScript("ready.sh", []string{"MSG=" + m, "READY_NO_LANDER=1"}); code != 0 || e.refs("ready/") != "traced" {
		t.Fatalf("a criterion without a test must not stop make ready: exit %d, queue %q\n%s", code, e.refs("ready/"), out)
	}

	// Fail open: a script that cannot run is one WARN, and the triage goes on.
	e.write(filepath.Join(e.work, "scripts", "ac-trace.sh"), "#!/bin/sh\nexit 2\n")
	if out := triage(msg); !strings.Contains(out, "WARN: ac-trace.sh failed — the criteria check did not run") {
		t.Errorf("a failing ac-trace.sh must be a WARN\n%s", out)
	}

	// A pull request: the tree is not checked out, so nothing is traced.
	out, code := triagePR(t, false, "OWNER", msg, "1\t0\tREADME.md\n")
	if code != 0 || strings.Contains(out, "Criteria:") || strings.Contains(out, "ac-trace") {
		t.Errorf("a PR gets no criteria line: exit %d\n%s", code, out)
	}
}

// The call stays advisory: on the line it fails open and sets no BLOCK.
func TestPRTriageRunsTheCriteriaTraceAdvisory(t *testing.T) {
	src := readFile(t, repoRoot(t), "scripts/pr-triage.sh")
	lines := strings.Split(src, "\n")
	for i, l := range lines {
		if !strings.Contains(l, "ac-trace.sh\"") || strings.HasPrefix(strings.TrimSpace(l), "#") {
			continue
		}
		if !strings.HasPrefix(l, "[ $# -ge 1 ] || ") || !strings.Contains(l, "TRIAGE_BODY=$body") || !strings.Contains(l, "|| echo \"WARN: ") ||
			strings.Contains(l, "blocked=") {
			t.Errorf("scripts/pr-triage.sh:%d: the ac-trace.sh call runs in branch mode only, with the body, fails open with a WARN and never blocks: %q", i+1, l)
		}
		return
	}
	t.Fatal("scripts/pr-triage.sh no longer calls ac-trace.sh; the criteria never reach make ready")
}

// AC-P5-5: once a plan's changes have landed (and the plan is gone), the report lists their
// criteria from the messages alone, manual rows first, each test row with its citing test.
func TestACReportListsTheCriteriaOfARange(t *testing.T) {
	e := newLandEnv(t)
	if out, _, code := e.acScript("ac-report.sh", nil); code != 0 || out != "No acceptance criteria in origin/main.\n" {
		t.Errorf("no tag and no criterion: exit %d, output %q", code, out)
	}
	e.git("tag", "v0.1.0")
	e.commitFiles("feat: one\n\nWhy.\n\nAC: AC-Q9-1 test a sentence | with a pipe\nAC: AC-Q9-2 manual tried on a candidate\nLander-Slug: one-change\n",
		map[string]string{"internal/a/a_test.go": "package a\n\n// AC-Q9-1\n"})
	e.git("tag", "v0.2.0-rc.20260101") // a candidate is no release: the range starts at v0.1.0
	two := e.commitAt("feat: two\n\nWhy.\n\nAC: AC-Q9-3 deferred waits for a later plan\nAC: AC-Q9-4 test cited by no test\n", "2026-01-03T00:00:00Z")
	// A later change declares AC-Q9-1 again (one row, both changes) and lands a line that
	// does not parse (listed, never dropped).
	three := e.commitAt("feat: three\n\nWhy.\n\nAC: AC-Q9-1 test said again\nAC: AC-Q9-5 sometimes no kind\nLander-Slug: three-change\n", "2026-01-04T00:00:00Z")
	e.git("push", "-q", "origin", "main")

	out, errb, code := e.acScript("ac-report.sh", nil)
	want := "Acceptance criteria landed in v0.1.0..origin/main (tests at origin/main):\n\n" +
		"| Criterion | Kind | Sentence | Change | Test |\n|---|---|---|---|---|\n" +
		"| AC-Q9-2 | manual | tried on a candidate | one-change | - |\n" +
		"| AC-Q9-1 | test | a sentence \\| with a pipe | one-change, three-change | internal/a/a_test.go:3 |\n" +
		"| AC-Q9-4 | test | cited by no test | " + e.git("rev-parse", "--short", two) + " | not found |\n" +
		"| AC-Q9-3 | deferred | waits for a later plan | " + e.git("rev-parse", "--short", two) + " | - |\n" +
		"\nunreadable: " + e.git("rev-parse", "--short", three) + " (three-change): AC: AC-Q9-5 sometimes no kind\n"
	if code != 0 || errb != "" || out != want {
		t.Errorf("exit %d, stderr %q, output\n%s\nwant\n%s", code, errb, out, want)
	}
	if out, _, code := e.acScript("ac-report.sh", nil, "HEAD..HEAD"); code != 0 || out != "No acceptance criteria in HEAD..HEAD.\n" {
		t.Errorf("an empty range: exit %d, output %q", code, out)
	}
	// A range whose only AC: line does not parse says so, and lists the line.
	bad := e.commitAt("feat: four\n\nAC: AC-Q9-6 test\n", "2026-01-05T00:00:00Z")
	if out, _, code := e.acScript("ac-report.sh", nil, "HEAD~1..HEAD"); code != 0 ||
		out != "No readable acceptance criteria in HEAD~1..HEAD.\n\nunreadable: "+e.git("rev-parse", "--short", bad)+": AC: AC-Q9-6 test\n" {
		t.Errorf("only an unreadable line: exit %d, output %q", code, out)
	}
}
