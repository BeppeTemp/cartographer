package repodocs

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// evalSkill is the directory of the review skill's eval scripts and tables.
const evalSkill = ".claude/skills/cartographer-review-pr"

// newEvalEnv is a landEnv whose main also holds the two eval scripts and a copy of the
// real evals/cases.md, at their paths: the scripts read the cases.md of the working copy.
func newEvalEnv(t *testing.T) *landEnv {
	t.Helper()
	e := newLandEnv(t)
	root := repoRoot(t)
	for _, f := range []string{"scripts/eval-case.sh", "scripts/eval-setup.sh", "evals/cases.md"} {
		e.write(filepath.Join(e.work, filepath.FromSlash(evalSkill+"/"+f)), readFile(t, root, evalSkill+"/"+f))
	}
	e.git("add", ".")
	e.git("commit", "-q", "-m", "chore: the eval scripts")
	e.git("push", "-q", "origin", "main")
	return e
}

// commitAt commits msg (no file) on the current branch, committed and authored at date,
// and returns its sha.
func (e *landEnv) commitAt(msg, date string) string {
	e.t.Helper()
	cmd := exec.Command("git", "commit", "-q", "--allow-empty", "-F", "-")
	cmd.Dir, cmd.Env = e.work, append(append([]string{}, e.env...), "GIT_COMMITTER_DATE="+date, "GIT_AUTHOR_DATE="+date)
	cmd.Stdin = strings.NewReader(msg)
	if out, err := cmd.CombinedOutput(); err != nil {
		e.t.Fatalf("commit: %v\n%s", err, out)
	}
	return e.git("rev-parse", "HEAD")
}

// evalScript runs one of the eval scripts of e.work; stdout, stderr and the exit code.
func (e *landEnv) evalScript(name string, args ...string) (string, string, int) {
	e.t.Helper()
	cmd := exec.Command("bash", append([]string{filepath.Join(e.work, filepath.FromSlash(evalSkill+"/scripts/"+name))}, args...)...)
	cmd.Dir, cmd.Env = e.work, e.env
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

var caseRow = regexp.MustCompile(`^\| *c([0-9]+) *\|`)

// maxCase is the highest cN id of a cases.md.
func maxCase(text string) int {
	m := 0
	for _, l := range strings.Split(text, "\n") {
		if g := caseRow.FindStringSubmatch(l); g != nil {
			if n, _ := strconv.Atoi(g[1]); n > m {
				m = n
			}
		}
	}
	return m
}

// An escaped defect becomes a case by script: --pending lists the landed fixes whose
// Introduced-by names a change a reviewer read, one per (fix, introducing change), and
// <sha> appends the row, which the real eval-setup.sh then prepares.
func TestEvalCaseListsAndAddsTheEscapes(t *testing.T) {
	e := newEvalEnv(t)
	introFull := e.commitAt("feat(kb): a\n\nWhy.\n\nLander-Slug: intro-full\nReview: full, 2 rounds\n", "2026-01-02T10:00:00Z")
	introNone := e.commitAt("feat(kb): b (#9941)\n", "2026-01-03T10:00:00Z")
	introLight := e.commitAt("feat(kb): c\n\nLander-Slug: intro-light\nReview: light, 1 rounds\n", "2026-01-04T10:00:00Z")
	e.commitAt("feat(kb): d\n\nLander-Slug: intro-odd\nReview: heavy, 1 rounds\n", "2026-01-04T11:00:00Z")
	// The lines beside Closes #12 are no git trailers: they are read anywhere in the message.
	fixFull := e.commitAt("fix(kb): repair a\n\nWhy.\n\nFound-by: rc\nIntroduced-by: intro-full\nCloses #12\n\nLander-Slug: fix-a\n", "2026-01-05T10:00:00Z")
	fixReview := e.commitAt("fix: found in review\n\nFound-by: review\nIntroduced-by: intro-full\n", "2026-01-06T10:00:00Z")
	fixUnknown := e.commitAt("fix: unknown origin\n\nFound-by: gate\nIntroduced-by: unknown\n", "2026-01-07T10:00:00Z")
	fixLight := e.commitAt("fix: after a light change\n\nFound-by: user\nIntroduced-by: "+introLight[:12]+"\n", "2026-01-08T10:00:00Z")
	fixOdd := e.commitAt("fix: after an odd tier\n\nFound-by: user\nIntroduced-by: intro-odd\n", "2026-01-08T11:00:00Z")
	// A contributor's squash-merged PR: Regression-of, no Found-by line.
	fixNone := e.commitAt("fix(kb)!: repair b (#9945)\n\nRegression-of: #9941\n", "2026-01-09T10:00:00Z")
	// A bundle: one row per introducing change a reviewer read; the light one is left out.
	fixBundle := e.commitAt("fix: three defects\n\nFound-by: review\nFound-by: main-ci\nIntroduced-by: intro-light\n"+
		"Introduced-by: "+introFull[:10]+"\nIntroduced-by: #9941\n", "2026-01-10T10:00:00Z")
	e.git("push", "-q", "origin", "main")
	e.git("checkout", "-q", "-b", "side")
	offMain := e.commitAt("fix: not landed\n\nFound-by: gate\nIntroduced-by: intro-full\n", "2026-01-11T10:00:00Z")
	e.git("checkout", "-q", "main")

	line := func(fix, intro, tier, title string) string {
		return fix[:12] + " " + intro[:12] + " " + tier + " " + title + "\n"
	}
	out, errs, code := e.evalScript("eval-case.sh", "--pending")
	want := line(fixFull, introFull, "full", "fix(kb): repair a") +
		line(fixNone, introNone, "not recorded", "fix(kb)!: repair b (#9945)") +
		line(fixBundle, introFull, "full", "fix: three defects") +
		line(fixBundle, introNone, "not recorded", "fix: three defects")
	if code != 0 || out != want {
		t.Fatalf("--pending: exit %d\n--- got\n%s--- want\n%s%s", code, out, want, errs)
	}

	casesPath := filepath.Join(e.work, filepath.FromSlash(evalSkill+"/evals/cases.md"))
	before := readFileAbs(t, casesPath)
	id := maxCase(before) + 1
	out, errs, code = e.evalScript("eval-case.sh", fixFull)
	row := fmt.Sprintf("| c%d | intro-full | %s | %s | none | TODO | TODO | TODO | TODO |\n", id, introFull, fixFull[:12])
	if code != 0 || out != row {
		t.Fatalf("eval-case.sh <sha>: exit %d, printed\n%s\nwant\n%s%s", code, out, row, errs)
	}
	// The row goes after the last c row; every other line stays as it was.
	lines := strings.SplitAfter(before, "\n")
	last := -1
	for i, l := range lines {
		if caseRow.MatchString(l) {
			last = i
		}
	}
	if last < 0 {
		t.Fatal("the copied cases.md has no c row")
	}
	wantFile := strings.Join(lines[:last+1], "") + row + strings.Join(lines[last+1:], "")
	if got := readFileAbs(t, casesPath); got != wantFile {
		t.Errorf("cases.md after the append\n%s\nwant\n%s", got, wantFile)
	}

	// The real eval-setup.sh reads the new row: its worktree at the introducing commit.
	out, errs, code = e.evalScript("eval-setup.sh", fmt.Sprintf("c%d", id))
	wt := filepath.Join(e.work, ".worktrees", fmt.Sprintf("eval-c%d", id))
	parent := e.git("rev-parse", introFull+"^")
	wantSetup := fmt.Sprintf("c%d: worktree %s · base %s · plan none · round 1 · eval:", id, wt, parent)
	if code != 0 || !strings.HasPrefix(out, wantSetup) {
		t.Fatalf("eval-setup.sh c%d: exit %d\n%s\nwant a line starting %q%s", id, code, out, wantSetup, errs)
	}
	if got := e.run(wt, "git", "rev-parse", "HEAD"); got != introFull {
		t.Errorf("the eval worktree is at %s, want the introducing commit %s", got, introFull)
	}
	if _, errs, code = e.evalScript("eval-setup.sh", "--clean"); code != 0 {
		t.Fatalf("eval-setup.sh --clean: exit %d\n%s", code, errs)
	}

	// A row is not listed again.
	out, errs, code = e.evalScript("eval-case.sh", "--pending")
	want = line(fixNone, introNone, "not recorded", "fix(kb)!: repair b (#9945)") +
		line(fixBundle, introFull, "full", "fix: three defects") +
		line(fixBundle, introNone, "not recorded", "fix: three defects")
	if code != 0 || out != want {
		t.Errorf("--pending after the row: exit %d\n--- got\n%s--- want\n%s%s", code, out, want, errs)
	}
	if out, _, code = e.evalScript("eval-case.sh", "--pending", "--since", "2026-01-10"); code != 0 || out != line(fixBundle, introFull, "full", "fix: three defects")+line(fixBundle, introNone, "not recorded", "fix: three defects") {
		t.Errorf("--since 2026-01-10: exit %d\n%s", code, out)
	}
	if out, _, code = e.evalScript("eval-case.sh", "--pending", "--since", "2099-01-01"); code != 0 || out != "" {
		t.Errorf("an empty window: exit %d, want 0 and nothing\n%s", code, out)
	}

	// A bundle appends one row per introducing change, the next ids.
	out, errs, code = e.evalScript("eval-case.sh", fixBundle[:12])
	rows := fmt.Sprintf("| c%d | intro-full | %s | %s | none | TODO | TODO | TODO | TODO |\n", id+1, introFull, fixBundle[:12]) +
		fmt.Sprintf("| c%d | #9941 | %s | %s | none | TODO | TODO | TODO | TODO |\n", id+2, introNone, fixBundle[:12])
	if code != 0 || out != rows {
		t.Errorf("the bundle: exit %d, printed\n%s\nwant\n%s%s", code, out, rows, errs)
	}
	// With no --since there is no lower bound: the newest row's fix (2026-01-10) does not hide
	// an older pending escape (2026-01-09).
	if out, errs, code = e.evalScript("eval-case.sh", "--pending"); code != 0 || out != line(fixNone, introNone, "not recorded", "fix(kb)!: repair b (#9945)") {
		t.Errorf("--pending after a newer row: exit %d, want the older escape\n%s%s", code, out, errs)
	}

	for name, c := range map[string]struct{ arg, why string }{
		"already a row":           {fixFull, "already a row"},
		"found in review":         {fixReview, "says review"},
		"unknown origin":          {fixUnknown, "unknown names no change"},
		"a light change":          {fixLight, "reviewed light"},
		"a tier of no known name": {fixOdd, "none of light, standard, full"},
		"not a fix":               {introFull, "is not a fix"},
		"not on origin/main":      {offMain, "not on origin/main"},
		"not a commit":            {"nothing-here", "is not a commit"},
	} {
		before := readFileAbs(t, casesPath)
		_, errs, code := e.evalScript("eval-case.sh", c.arg)
		if code != 1 || !strings.Contains(errs, c.why) {
			t.Errorf("%s: want exit 1 and %q, got exit %d\n%s", name, c.why, code, errs)
		}
		if readFileAbs(t, casesPath) != before {
			t.Errorf("%s: a refused fix changed cases.md", name)
		}
	}
	if _, _, code := e.evalScript("eval-case.sh", "--pending", "--since", "2026-1-5"); code != 2 {
		t.Errorf("a --since that is no YYYY-MM-DD: exit %d, want 2", code)
	}

	// --scored: the ten newest c rows, every security row, every k row, for eval-setup.sh.
	var b strings.Builder
	b.WriteString("| id | introducing change | squash commit | fixing change | plan | defect | expected finding | class | not in front of the reviewer |\n|---|---|---|---|---|---|---|---|---|\n")
	for n := 1; n <= 13; n++ {
		class := "edge case"
		if n == 2 {
			class = "security"
		}
		fmt.Fprintf(&b, "| c%d | x | %040d | y | none | f:1 | e | %s | z |\n", n, n, class)
	}
	b.WriteString("\n| id | PR | squash commit | plan |\n|---|---|---|---|\n| k1 | #1 | a | 1 |\n| k2 | #2 | b | 2 |\n")
	e.write(casesPath, b.String())
	if out, errs, code := e.evalScript("eval-case.sh", "--scored"); code != 0 || out != "c2 c4 c5 c6 c7 c8 c9 c10 c11 c12 c13 k1 k2\n" {
		t.Errorf("--scored: exit %d\n%s%s", code, out, errs)
	}
}

// A row of evals/cases.md that a person has not filled cannot land: eval-case.sh writes
// TODO in the cells it cannot know, and eval-setup.sh reads the cells by position.
func TestEvalCasesHaveNoTodoCell(t *testing.T) {
	text := readFile(t, repoRoot(t), evalSkill+"/evals/cases.md")
	id := regexp.MustCompile(`^[ck][0-9]+$`)
	header, rows := 0, map[byte]int{}
	for i, l := range strings.Split(text, "\n") {
		if !strings.HasPrefix(l, "|") {
			header = 0
			continue
		}
		cells := strings.Split(strings.TrimSuffix(strings.TrimSpace(l), "|"), "|")[1:]
		for j := range cells {
			cells[j] = strings.TrimSpace(cells[j])
		}
		if header == 0 {
			header = len(cells)
			continue
		}
		if !id.MatchString(cells[0]) {
			continue
		}
		rows[cells[0][0]]++
		if len(cells) != header {
			t.Errorf("cases.md:%d: %s has %d cells, its table %d", i+1, cells[0], len(cells), header)
		}
		for j, c := range cells {
			if c == "" || c == "TODO" {
				t.Errorf("cases.md:%d: cell %d of %s is %q: fill it (cartographer-review-pr §When a defect escapes)", i+1, j+1, cells[0], c)
			}
		}
	}
	if rows['c'] == 0 || rows['k'] == 0 {
		t.Fatalf("cases.md: %d c rows and %d k rows read: the parse is wrong", rows['c'], rows['k'])
	}
}

// fakeGHReviewerPR serves one PR: $META for `pr view`, $ROWS for its file list, $ASSOC as
// its author's association.
const fakeGHReviewerPR = `#!/bin/sh
case "$1 $2" in
  "pr view") cat "$META" ;;
  "api --paginate") cat "$ROWS" ;;
  "api repos/{owner}/{repo}/pulls/1") printf '%s\tUser\n' "$ASSOC" ;;
  *) exit 1 ;;
esac
`

// triagePR runs scripts/pr-triage.sh on PR 1 (with --gate when gate) as the stand-in gh
// serves it; the report and the exit code.
func triagePR(t *testing.T, gate bool, assoc, body, rows string) (string, int) {
	t.Helper()
	bash := bashOrSkip(t)
	dir := t.TempDir()
	meta, err := json.Marshal(map[string]any{"title": "docs: reviewer wording", "author": map[string]string{"login": "u"},
		"closingIssuesReferences": []any{}, "statusCheckRollup": []any{}, "body": body, "headRefName": "b", "labels": []any{}})
	if err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{"gh": fakeGHReviewerPR, "meta.json": string(meta), "rows.tsv": rows} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	args := []string{"scripts/pr-triage.sh", "1"}
	if gate {
		args = []string{"scripts/pr-triage.sh", "--gate", "1"}
	}
	cmd := exec.Command(bash, args...)
	cmd.Dir = repoRoot(t)
	cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"), "AUTHOR_ASSOCIATION=",
		"META="+filepath.Join(dir, "meta.json"), "ROWS="+filepath.Join(dir, "rows.tsv"), "ASSOC="+assoc)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	code := 0
	if err := cmd.Run(); err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("pr-triage.sh: %v", err)
		}
		code = ee.ExitCode()
	}
	return out.String(), code
}

const (
	reviewerNeedsScore = "a reviewer definition changed with no new line in evals/scores.md"
	contractDef        = "3\t1\t.claude/agents/cartographer-contract-reviewer.md\n"
	securityDef        = "2\t0\t.claude/agents/cartographer-security-reviewer.md\n"
	reviewSkill        = "4\t1\t.claude/skills/cartographer-review-pr/SKILL.md\n"
	scoreLine          = "1\t0\t.claude/skills/cartographer-review-pr/evals/scores.md\n"
)

// On a pull request a reviewer definition changed with no score is a WARN, whatever the
// role, and --gate passes: a train's body is the lander's and cannot carry the change's
// Evals: line, so a BLOCK there would stop every train that carries the change.
func TestPRTriageWarnsOnAReviewerChangeWithNoScore(t *testing.T) {
	for _, c := range []struct {
		name, body, rows string
		warn             bool
	}{
		{"the contract reviewer alone", "", contractDef, true},
		{"the security reviewer alone", "", securityDef, true},
		{"the review skill alone", "", reviewSkill, true},
		{"another file of the review skill", "", "1\t0\t.claude/skills/cartographer-review-pr/scripts/eval-case.sh\n", false},
		{"with a new score line", "", contractDef + scoreLine, false},
		{"with scores.md only shortened", "", contractDef + "0\t1\t.claude/skills/cartographer-review-pr/evals/scores.md\n", true},
		{"with an Evals line", "Why.\r\n\r\nEvals: none - wording only\r\n", contractDef, false},
		{"with an Evals line and no reason", "Evals: none - \n", contractDef, true},
		{"another agent definition", "", "3\t0\t.claude/agents/cartographer-plan-writer.md\n", false},
	} {
		for _, assoc := range []string{"OWNER", "CONTRIBUTOR"} {
			for _, gate := range []bool{false, true} {
				out, code := triagePR(t, gate, assoc, c.body, c.rows)
				if code != 0 {
					t.Errorf("%s, %s, gate=%v: exit %d, want 0\n%s", c.name, assoc, gate, code, out)
				}
				if strings.Contains(out, "BLOCK: "+reviewerNeedsScore) {
					t.Errorf("%s, %s, gate=%v: a PR is never refused for it\n%s", c.name, assoc, gate, out)
				}
				if got := strings.Contains(out, "WARN: "+reviewerNeedsScore); got != c.warn {
					t.Errorf("%s, %s, gate=%v: warned = %v, want %v\n%s", c.name, assoc, gate, got, c.warn, out)
				}
			}
		}
	}
}

// make ready runs the triage in branch mode: there a maintainer's change to a reviewer
// definition (either reviewer agent file or the review skill) is refused without a new score
// line or an Evals: none - <reason> line, and so is one whose permission cannot be read (it
// fails closed); a contributor's run before opening a PR only warns.
func TestPRTriageRefusesAQueuedReviewerChangeWithNoScore(t *testing.T) {
	e := newLandEnv(t)
	triage := func(body, perm string, env ...string) string {
		t.Helper()
		cmd := exec.Command("bash", filepath.Join(e.work, "scripts", "pr-triage.sh"))
		cmd.Dir, cmd.Env = e.work, append(append(append([]string{}, e.env...), "TRIAGE_BODY="+body, "FAKE_PERM="+perm), env...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("pr-triage.sh: %v\n%s", err, out)
		}
		return string(out)
	}
	e.git("checkout", "-q", "-b", "feat/reviewer")
	e.write(filepath.Join(e.work, ".claude", "agents", "cartographer-contract-reviewer.md"), "wording\n")
	e.git("add", ".")
	e.git("commit", "-q", "-m", "docs: reviewer wording")

	if out := triage("Why.\n", "ADMIN"); !strings.Contains(out, "Role: maintainer\n") || !strings.Contains(out, "BLOCK: "+reviewerNeedsScore) {
		t.Errorf("a maintainer's queued reviewer change with no score must be refused\n%s", out)
	}
	if out := triage("Why.\n", "READ"); !strings.Contains(out, "WARN: "+reviewerNeedsScore) || strings.Contains(out, "BLOCK: "+reviewerNeedsScore) {
		t.Errorf("a contributor's branch only gets a WARN\n%s", out)
	}
	if out := triage("Why.\n\nEvals: none - wording only\n", "ADMIN"); strings.Contains(out, reviewerNeedsScore) {
		t.Errorf("an Evals: none - <reason> line answers it\n%s", out)
	}
	// gh repo view fails on every call (the fake's counter is shared, so a large number).
	const unread = "FAKE_PERM_FAILS=999"
	if out := triage("Why.\n", "ADMIN", unread); !strings.Contains(out, "BLOCK: "+reviewerNeedsScore) || !strings.Contains(out, "could not be read") {
		t.Errorf("a permission that cannot be read must refuse a reviewer change, saying so\n%s", out)
	}
	if out := triage("Why.\n\nEvals: none - wording only\n", "ADMIN", unread); strings.Contains(out, reviewerNeedsScore) {
		t.Errorf("with the permission unread, an Evals: none - <reason> line still answers it\n%s", out)
	}
	e.write(filepath.Join(e.work, filepath.FromSlash(evalSkill+"/evals/scores.md")), "| 2026-01-01 | x | y | 1/1 | 0/1 |\n")
	e.git("add", ".")
	e.git("commit", "-q", "-m", "docs: the score")
	if out := triage("Why.\n", "ADMIN"); strings.Contains(out, reviewerNeedsScore) {
		t.Errorf("a new score line answers it\n%s", out)
	}

	// The review skill alone counts as a reviewer definition.
	e.git("checkout", "-q", "main")
	e.git("checkout", "-q", "-b", "feat/review-skill")
	e.write(filepath.Join(e.work, filepath.FromSlash(evalSkill+"/SKILL.md")), "wording\n")
	e.git("add", ".")
	e.git("commit", "-q", "-m", "docs: review skill wording")
	if out := triage("Why.\n", "ADMIN"); !strings.Contains(out, "BLOCK: "+reviewerNeedsScore) {
		t.Errorf("a maintainer's queued review skill change with no score must be refused\n%s", out)
	}

	// A change to no reviewer definition gets no eval line, the permission read or not.
	e.git("checkout", "-q", "main")
	e.git("checkout", "-q", "-b", "feat/other")
	e.write(filepath.Join(e.work, "README.md"), "wording\n")
	e.git("add", ".")
	e.git("commit", "-q", "-m", "docs: readme wording")
	if out := triage("Why.\n", "ADMIN", unread); strings.Contains(out, reviewerNeedsScore) {
		t.Errorf("no reviewer definition changed: no eval line\n%s", out)
	}
}

// A fix that names its origin gets the triage's escape line, which names the script that
// turns it into a case; `unknown` names nothing.
func TestPRTriagePrintsTheEscapeLine(t *testing.T) {
	for _, c := range []struct {
		body string
		want bool
	}{
		{"Why.\n\nFound-by: rc\nIntroduced-by: abc1234\nCloses #12\n", true},
		{"Regression-of: #7\n", true},
		{"Found-by: rc\nIntroduced-by: unknown\n", false},
		{"Introduced-by: unknown\r\n", false},
		{"Introduced-by:\n", false},
		{"Why: Introduced-by: abc1234 is quoted here\n", false},
	} {
		out, code := triagePR(t, false, "OWNER", c.body, "1\t0\tREADME.md\n")
		got := strings.Contains(out, "an escape: once it lands, .claude/skills/cartographer-review-pr/scripts/eval-case.sh <sha> adds it")
		if code != 0 || got != c.want {
			t.Errorf("body %q: exit %d, escape line = %v, want %v\n%s", c.body, code, got, c.want, out)
		}
	}
}
