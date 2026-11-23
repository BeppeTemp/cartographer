package landertest

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// escapesNow is the clock of the toy histories (ESCAPES_NOW): the window of the default 7 days
// starts on 2026-01-13, the 60 days on 2025-11-21. The commits newLandEnv and newEvalEnv make
// carry the real time, after it, so they are in neither.
var escapesNow = time.Date(2026, 1, 20, 0, 0, 0, 0, time.UTC)

// escapesFakeGH answers `gh api` from the issues of $FAKE_ISSUES, filtered by the labels= and
// state= fields as GitHub does, and applies the --jq expression with the real jq.
const escapesFakeGH = `#!/bin/bash
echo "$*" >> "$FAKE_GH_LOG"
if [ -n "${FAKE_GH_FAIL:-}" ]; then echo "HTTP 502: Bad Gateway" >&2; exit 1; fi
[ "$1" = api ] || exit 0
jq_expr=. label= state= prev=
for a in "$@"; do
  if [ "$prev" = --jq ]; then jq_expr=$a; fi
  case "$a" in labels=*) label=${a#labels=} ;; state=*) state=${a#state=} ;; esac
  prev=$a
done
jq --arg l "$label" --arg s "$state" '[.[] | select(any(.labels[]; .name == $l)) | select($s == "all" or .state == $s)]' "$FAKE_ISSUES" | jq -r "$jq_expr"
`

// newEscapesEnv is a newEvalEnv with the stand-in gh above, the clock, and the issues given.
func newEscapesEnv(t *testing.T, issues string) *landEnv {
	t.Helper()
	e := newEvalEnv(t)
	e.write(filepath.Join(e.bin, "gh"), escapesFakeGH)
	path := filepath.Join(e.tmp, "issues.json")
	e.write(path, issues)
	e.env = append(e.env, "FAKE_ISSUES="+path, "GH_REPO=up/proj", "ESCAPES_NOW="+strconv.FormatInt(escapesNow.Unix(), 10))
	return e
}

// writeScores writes the working copy's evals/scores.md with one score line of that date.
func (e *landEnv) writeScores(date string) {
	e.write(filepath.Join(e.work, filepath.FromSlash(evalSkill+"/evals/scores.md")),
		"# scores\n\n| date | commit | pin | caught/cases | noise/clean |\n|---|---|---|---|---|\n"+
			"| 2025-10-01 | a | m | 0/1 | 0/1 |\n| "+date+" | b | m | 0/1 | 0/1 |\n")
}

// escapes runs scripts/escapes.sh in e.work; stdout, stderr and the exit code.
func (e *landEnv) escapes(env []string, args ...string) (string, string, int) {
	e.t.Helper()
	cmd := exec.Command("bash", append([]string{filepath.Join(e.work, "scripts", "escapes.sh")}, args...)...)
	cmd.Dir, cmd.Env = e.work, append(append([]string{}, e.env...), env...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	code := 0
	if err := cmd.Run(); err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			e.t.Fatalf("escapes.sh: %v", err)
		}
		code = ee.ExitCode()
	}
	return out.String(), errb.String(), code
}

// at is a date of the toy history, days before escapesNow plus some hours.
func at(days, hours int) string {
	return escapesNow.Add(time.Duration(hours)*time.Hour - time.Duration(days)*24*time.Hour).Format(time.RFC3339)
}

// reportSection returns the body of the section whose heading starts with "### <n>.".
func reportSection(t *testing.T, out string, n int) string {
	t.Helper()
	head := "\n### " + strconv.Itoa(n) + "."
	i := strings.Index(out, head)
	if i < 0 {
		t.Fatalf("no section %d in:\n%s", n, out)
	}
	s := out[i+len(head):]
	if j := strings.Index(s, "\n### "); j >= 0 {
		s = s[:j]
	}
	return s + "\n"
}

// wantRows fails on each line of rows that is not a whole line of section n.
func wantRows(t *testing.T, out string, n int, rows ...string) {
	t.Helper()
	s := reportSection(t, out, n)
	for _, r := range rows {
		if !strings.Contains(s, "\n"+r+"\n") {
			t.Errorf("section %d has no line %q:\n%s", n, r, s)
		}
	}
}

// The issues of the stand-in gh: one per way an issue is counted, and the ones left out.
var escapesIssues = `[
 {"number":101,"state":"open","labels":[{"name":"bug"}],"author_association":"OWNER","user":{"type":"User"},"created_at":"2026-01-18T10:00:00Z","body":"Found-by: review\nIntroduced-by: intro-full"},
 {"number":102,"state":"closed","labels":[{"name":"bug"}],"author_association":"OWNER","user":{"type":"User"},"created_at":"2026-01-17T10:00:00Z","body":"Steps\r\nfound-by: rc\r\n"},
 {"number":103,"state":"open","labels":[{"name":"bug"}],"author_association":"OWNER","user":{"type":"User"},"created_at":"2026-01-16T10:00:00Z","body":"No line."},
 {"number":104,"state":"open","labels":[{"name":"bug"}],"author_association":"NONE","user":{"type":"User"},"created_at":"2026-01-16T11:00:00Z","body":"It broke."},
 {"number":105,"state":"open","labels":[{"name":"ci-red"}],"author_association":"NONE","user":{"type":"Bot"},"created_at":"2026-01-15T10:00:00Z","body":"CI failed on main"},
 {"number":106,"state":"open","labels":[{"name":"bug"},{"name":"ci-red"}],"author_association":"MEMBER","user":{"type":"User"},"created_at":"2026-01-15T11:00:00Z","body":"Found-by: main-ci"},
 {"number":107,"state":"open","labels":[{"name":"bug"}],"author_association":"OWNER","user":{"type":"User"},"created_at":"2026-01-14T10:00:00Z","body":"Found-by: @someone [x](http://example.com)"},
 {"number":108,"state":"open","labels":[{"name":"bug"}],"author_association":"NONE","user":{"type":"User"},"created_at":"2026-01-01T10:00:00Z","body":"Before the window."},
 {"number":109,"state":"open","labels":[{"name":"bug"}],"author_association":"NONE","user":{"type":"User"},"created_at":"2026-01-18T10:00:00Z","body":"A pull request.","pull_request":{}},
 {"number":110,"state":"open","labels":[{"name":"bug"}],"author_association":"COLLABORATOR","user":{"type":"User"},"created_at":"2026-01-19T10:00:00Z","body":"x\n Found-by: gate"},
 {"number":201,"state":"open","labels":[{"name":"process"}],"author_association":"OWNER","user":{"type":"User"},"created_at":"2025-06-01T10:00:00Z","body":""},
 {"number":202,"state":"open","labels":[{"name":"process"}],"author_association":"OWNER","user":{"type":"User"},"created_at":"2025-06-01T10:00:00Z","body":""},
 {"number":203,"state":"closed","labels":[{"name":"process"}],"author_association":"OWNER","user":{"type":"User"},"created_at":"2025-06-01T10:00:00Z","body":""}
]`

// The report counts the fixes of the window by Found-by line (a bundle once per line), where
// they came from by the review tier of the introducing commit, escapes by phase, the issues
// and the costs, and prints no title, no body and no value outside its fixed lists.
func TestEscapesReportCountsTheFixesByPhase(t *testing.T) {
	e := newEscapesEnv(t, escapesIssues)
	e.commitAt("fix: before the window\n\nFound-by: user\n", at(31, 0))
	e.commitAt("feat(kb): a\n\nWhy.\n\nLander-Slug: intro-full\nReview: full, 2 rounds\n", at(20, 0))
	introLight := e.commitAt("feat(kb): b\n\nLander-Slug: intro-light\nReview: light, 1 rounds\n", at(20, 0))
	introNone := e.commitAt("feat(kb): c (#9941)\n", at(20, 0))
	e.commitAt("feat(kb): d\n\nLander-Slug: intro-odd\nReview: `evil` | x, 1 rounds\n", at(20, 0))
	e.commitAt("fix(kb): found in review\n\nFound-by: review\nIntroduced-by: intro-full\n", at(2, 0))
	e.commitAt("fix: at the gate\n\nFound-by: gate\nIntroduced-by: "+introLight[:12]+"\nCost: impl 30k, review ?, rounds ?\n", at(3, 0))
	e.commitAt("fix: on main\n\nFound-by: main-ci\nIntroduced-by: #9941\n", at(4, 0))
	e.commitAt("fix: nightly\n\nFound-by: nightly\nIntroduced-by: intro-full\n", at(5, 0))
	// A bundle of two defects of two changes, beside Closes: one line each.
	e.commitAt("fix!: in the candidate\n\nFound-by: rc\nIntroduced-by: "+introNone[:10]+"\nIntroduced-by: intro-full\nCloses #12\n", at(6, 0))
	e.commitAt("fix: by a user\n\nFound-by: user\nIntroduced-by: no-such-change\n", at(6, 12))
	e.commitAt("fix: by an agent\n\nFound-by: agent\nIntroduced-by: intro-odd\n", at(6, 0))
	noLine := e.commitAt("fix(kb): a | broken `table` [x](http://example.com)\n\nNo line.\n", at(1, 0))
	e.commitAt("fix: two at once\n\nFound-by: gate\nFound-by: rc\nIntroduced-by: unknown\nCloses #12\n", at(1, 6))
	e.commitAt("fix: hostile\n\nFound-by: <img src=x onerror=alert(1)>\nIntroduced-by: unknown\n", at(1, 7))
	e.commitAt("feat: not a fix\n\nFound-by: review\n", at(2, 12))
	e.commitAt("feat: d\n\nCost: impl 120k, review 60k, rounds 1\n\nLander-Slug: d\nReview: full, 1 rounds\n", at(4, 12))
	e.commitAt("feat: e\n\nCost: impl ?, review 80k, rounds 2\n\nReview: full, 2 rounds\n", at(5, 12))
	e.commitAt("chore: x\n\nCost: impl 5 tokens\n", at(5, 13))
	e.git("push", "-q", "origin", "main")
	e.writeScores("2025-12-01")

	// A comma-decimal locale (where it is installed) must not turn 120k into 120,0k.
	out, errs, code := e.escapes([]string{"LC_ALL=de_DE.UTF-8"})
	if code != 0 {
		t.Fatalf("exit %d\n%s\n%s", code, out, errs)
	}
	for n := 1; n <= 7; n++ {
		reportSection(t, out, n)
	}
	wantRows(t, out, 1,
		"10 fix commits, 10 `Found-by:` lines (a fix that bundles defects has one per defect).",
		"| review | 1 | 9% |", "| gate | 2 | 18% |", "| main-ci | 1 | 9% |", "| nightly | 1 | 9% |",
		"| rc | 2 | 18% |", "| user | 1 | 9% |", "| agent | 1 | 9% |",
		"| other (not a value of `scripts/found-by.tsv`) | 1 | 9% |", "| missing (fixes with no line) | 1 | 9% |",
		"Fixes with no `Found-by:` line: `"+noLine[:12]+"`.")
	wantRows(t, out, 2,
		"| resolved | 7 |", "| unresolved (names no older commit of `main`) | 1 |", "| unknown | 2 |",
		"| missing (fixes with no line) | 1 |",
		"Median days from the introducing commit to the fix: 15 (7 resolved).",
		"| review tier of the introducing change | defects | changes landed in the 60 days to 2026-01-20 |",
		"| light | 1 | 1 |", "| standard | 0 | 0 |", "| fast | 0 | 0 |", "| full | 3 | 3 |", "| not recorded | 2 | 14 |",
		"| other (not a known tier) | 1 | 1 |")
	wantRows(t, out, 3,
		"| review | 8 | 7 |", "| gate | 7 | 5 |", "| main-ci | 5 | 4 |", "| nightly | 4 | 3 |", "| rc | 3 | 1 |",
		"| user | 1 | 0 |", "`agent` (a session working on something else, outside the order): 1.")
	wantRows(t, out, 4,
		"| review | 1 | #101 |", "| gate | 0 | - |", "| main-ci | 1 | #106 |", "| rc | 1 | #102 |", "| user | 1 | #104 |",
		"| other (not a value of `scripts/found-by.tsv`) | 1 | #107 |", "| missing | 3 | #103, #105, #110 |")
	if !strings.Contains(reportSection(t, out, 4), "\n8 `bug` or `ci-red` issues opened in the window") {
		t.Errorf("section 4 does not count 8 issues:\n%s", reportSection(t, out, 4))
	}
	wantRows(t, out, 5,
		"- Pending escapes (`eval-case.sh --pending`): 4 rows to add, from 3 fixes.",
		"- Last score in `evals/scores.md`: 2025-12-01, 50 days ago: run them (`cartographer-review-pr` §Changing the reviewer).")
	wantRows(t, out, 6,
		"3 of 14 changes of the window carry a `Cost:` line (`impl` and `review` in thousands of tokens; a `?` figure is left out of the medians).",
		"| light | 0 | 0 | - | - |", "| standard | 0 | 0 | - | - |", "| fast | 0 | 0 | - | - |", "| full | 2 | 2 | 120k | 70k |",
		"| not recorded | 12 | 1 | 30k | - |",
		"| 1 | 1 |", "| 2 | 1 |", "| 3 | 0 |", "| ? | 1 |")
	wantRows(t, out, 7, "Open `process` issues: 2.")
	// Titles, bodies and values outside the lists are counted, never printed.
	for _, s := range []string{"broken", "example.com", "@someone", "img", "evil", "found in review", "Steps"} {
		if strings.Contains(out, s) {
			t.Errorf("the report prints %q:\n%s", s, out)
		}
	}
}

// Anyone can open an issue: a `Found-by:` line is read only from a maintainer or the bot the
// workflows run as (ci.yml opens the ci-red issues as github-actions[bot], association
// CONTRIBUTOR). Anyone else's line is ignored and the issue counts by its author, as one with
// no line does.
func TestEscapesReportReadsFoundByOnlyFromMaintainersAndTheWorkflowBot(t *testing.T) {
	e := newEscapesEnv(t, `[
 {"number":301,"state":"open","labels":[{"name":"bug"}],"author_association":"NONE","user":{"login":"someone","type":"User"},"created_at":"2026-01-18T10:00:00Z","body":"Found-by: review"},
 {"number":302,"state":"open","labels":[{"name":"bug"}],"author_association":"CONTRIBUTOR","user":{"login":"someone","type":"User"},"created_at":"2026-01-18T11:00:00Z","body":"Found-by: rc"},
 {"number":303,"state":"open","labels":[{"name":"bug"}],"author_association":"OWNER","user":{"login":"user","type":"User"},"created_at":"2026-01-18T12:00:00Z","body":"Found-by: review"},
 {"number":304,"state":"open","labels":[{"name":"ci-red"}],"author_association":"CONTRIBUTOR","user":{"login":"github-actions[bot]","type":"Bot"},"created_at":"2026-01-17T10:00:00Z","body":"CI failed on main\nFound-by: main-ci"},
 {"number":305,"state":"open","labels":[{"name":"bug"}],"author_association":"NONE","user":{"login":"other-app[bot]","type":"Bot"},"created_at":"2026-01-17T11:00:00Z","body":"Found-by: gate"},
 {"number":306,"state":"open","labels":[{"name":"bug"}],"author_association":"NONE","user":{"login":"github-actions[bot]","type":"User"},"created_at":"2026-01-17T12:00:00Z","body":"Found-by: nightly"}
]`)
	out, errs, code := e.escapes(nil)
	if code != 0 {
		t.Fatalf("exit %d\n%s\n%s", code, out, errs)
	}
	wantRows(t, out, 4,
		"| review | 1 | #303 |", "| rc | 0 | - |", "| gate | 0 | - |", "| nightly | 0 | - |",
		"| main-ci | 1 | #304 |", "| user | 3 | #301, #302, #306 |", "| missing | 1 | #305 |")
}

// GitHub, the eval script and the scores file can each fail: the report says `not read` or
// `none` for what it could not read, keeps the git sections, and exits 0.
func TestEscapesReportFailsOpen(t *testing.T) {
	e := newEscapesEnv(t, escapesIssues)
	e.commitAt("fix: a\n\nFound-by: user\nIntroduced-by: unknown\n", at(1, 0))
	e.git("push", "-q", "origin", "main")
	e.writeScores("2026-01-10")

	out, errs, code := e.escapes([]string{"FAKE_GH_FAIL=1"})
	if code != 0 {
		t.Fatalf("a failing gh: exit %d\n%s\n%s", code, out, errs)
	}
	wantRows(t, out, 1, "| user | 1 | 100% |")
	wantRows(t, out, 4, "not read (gh failed, exit 1)")
	wantRows(t, out, 7, "not read (gh failed, exit 1)")
	wantRows(t, out, 5, "- Last score in `evals/scores.md`: 2026-01-10, 10 days ago.")

	if err := os.Remove(e.ghLog); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(e.work, filepath.FromSlash(evalSkill+"/evals/cases.md"))); err != nil {
		t.Fatal(err)
	}
	out, errs, code = e.escapes(nil, "--no-gh")
	if code != 0 {
		t.Fatalf("--no-gh: exit %d\n%s\n%s", code, out, errs)
	}
	wantRows(t, out, 4, "not read (--no-gh)")
	wantRows(t, out, 7, "not read (--no-gh)")
	wantRows(t, out, 5, "- Pending escapes: not read (eval-case.sh failed, exit 1)")
	if _, err := os.Stat(e.ghLog); err == nil {
		t.Errorf("--no-gh called gh:\n%s", e.log())
	}
}

// A window with no commit prints every section with zeros; a usage error exits 2.
func TestEscapesReportOnAnEmptyWindow(t *testing.T) {
	e := newEscapesEnv(t, "[]")
	out, errs, code := e.escapes(nil, "--no-gh", "--since", "3")
	if code != 0 {
		t.Fatalf("exit %d\n%s\n%s", code, out, errs)
	}
	if !strings.Contains(out, "the 3 days from 2026-01-17 to 2026-01-20 (UTC)") {
		t.Errorf("the window is not named:\n%s", out)
	}
	wantRows(t, out, 1, "0 fix commits, 0 `Found-by:` lines (a fix that bundles defects has one per defect).",
		"| review | 0 | 0% |", "| missing (fixes with no line) | 0 | 0% |")
	wantRows(t, out, 2, "| resolved | 0 |", "Median days from the introducing commit to the fix: - (0 resolved).",
		"| not recorded | 0 | 0 |")
	wantRows(t, out, 3, "| review | 0 | 0 |", "`agent` (a session working on something else, outside the order): 0.")
	wantRows(t, out, 5, "- Pending escapes (`eval-case.sh --pending`): 0 rows to add, from 0 fixes.",
		"- Last score in `evals/scores.md`: none: run them (`cartographer-review-pr` §Changing the reviewer).")
	wantRows(t, out, 6, "0 of 0 changes of the window carry a `Cost:` line (`impl` and `review` in thousands of tokens; a `?` figure is left out of the medians).",
		"| full | 0 | 0 | - | - |", "| ? | 0 |")
	for _, args := range [][]string{{"--since", "0"}, {"--since"}, {"--since", "x"}, {"--other"}} {
		if _, _, code := e.escapes(nil, args...); code != 2 {
			t.Errorf("escapes.sh %v: exit %d, want 2", args, code)
		}
	}
}

// The workflow runs weekly and by hand, holds two permissions, checks out without keeping
// credentials, and interpolates nothing into a run line.
func TestEscapesWorkflow(t *testing.T) {
	root := repoRoot(t)
	var wf struct {
		On          map[string]any    `yaml:"on"`
		Permissions map[string]string `yaml:"permissions"`
		Jobs        map[string]struct {
			Permissions map[string]string `yaml:"permissions"`
			Steps       []struct {
				Uses string            `yaml:"uses"`
				With map[string]string `yaml:"with"`
				Run  string            `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal([]byte(readFile(t, root, ".github/workflows/escapes.yml")), &wf); err != nil {
		t.Fatal(err)
	}
	if _, ok := wf.On["workflow_dispatch"]; !ok || len(wf.On) != 2 || wf.On["workflow_dispatch"] != nil {
		t.Errorf("on: %v, want schedule and a bare workflow_dispatch only", wf.On)
	}
	if cron, _ := yaml.Marshal(wf.On["schedule"]); strings.TrimSpace(string(cron)) != `- cron: 0 6 * * 1` {
		t.Errorf("schedule: %s, want Monday 06:00 UTC", cron)
	}
	if len(wf.Permissions) != 0 {
		t.Errorf("the workflow grants %v: the job holds its own", wf.Permissions)
	}
	if len(wf.Jobs) != 1 {
		t.Fatalf("%d jobs, want 1", len(wf.Jobs))
	}
	checkout := regexp.MustCompile(`actions/checkout@[0-9a-f]{40}`).FindString(readFile(t, root, ".github/workflows/ci.yml"))
	for name, j := range wf.Jobs {
		if len(j.Permissions) != 2 || j.Permissions["contents"] != "read" || j.Permissions["issues"] != "write" {
			t.Errorf("%s: permissions %v, want contents: read and issues: write", name, j.Permissions)
		}
		if len(j.Steps) == 0 || j.Steps[0].Uses != checkout || j.Steps[0].With["persist-credentials"] != "false" || j.Steps[0].With["fetch-depth"] != "0" {
			t.Errorf("%s: the first step is not the checkout of ci.yml (%s) with persist-credentials: false and fetch-depth: 0", name, checkout)
		}
		runs := ""
		for _, s := range j.Steps {
			if strings.Contains(s.Run, "${{") {
				t.Errorf("%s: a run line interpolates an expression:\n%s", name, s.Run)
			}
			runs += s.Run
		}
		if !strings.Contains(runs, "scripts/escapes.sh") || !strings.Contains(runs, "gh issue comment") {
			t.Errorf("%s: the steps do not run scripts/escapes.sh and comment on the issue", name)
		}
	}
}
