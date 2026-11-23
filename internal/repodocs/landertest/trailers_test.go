package landertest

import (
	. "github.com/BeppeTemp/cartographer/internal/repodocs"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// fixLines are the two lines a queued `fix` carries (scripts/trailers-lint.sh), for the
// tests that queue one with the real scripts/ready.sh.
const fixLines = "Found-by: gate\nIntroduced-by: unknown\n"

// trailersLint runs the copy of scripts/trailers-lint.sh in e.work on msg.
func (e *landEnv) trailersLint(msg string) (string, bool) {
	e.t.Helper()
	m := filepath.Join(e.tmp, "lint-msg")
	e.write(m, msg)
	cmd := exec.Command("bash", filepath.Join(e.work, "scripts", "trailers-lint.sh"), m)
	cmd.Dir, cmd.Env = e.work, e.env
	out, err := cmd.CombinedOutput()
	return string(out), err == nil
}

// foundByVocabulary is the first column of scripts/found-by.tsv, its one source.
func foundByVocabulary(t *testing.T, root string) []string {
	t.Helper()
	var vals []string
	seen := map[string]bool{}
	for i, line := range strings.Split(readFile(t, root, "scripts/found-by.tsv"), "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) != 2 || !regexp.MustCompile(`^[a-z][a-z0-9-]*$`).MatchString(f[0]) || strings.TrimSpace(f[1]) == "" {
			t.Errorf("scripts/found-by.tsv:%d: want <value><TAB><what it means>, got %q", i+1, line)
			continue
		}
		if seen[f[0]] {
			t.Errorf("scripts/found-by.tsv:%d: %s is listed twice", i+1, f[0])
		}
		seen[f[0]] = true
		vals = append(vals, f[0])
	}
	if len(vals) == 0 {
		t.Fatal("scripts/found-by.tsv holds no value")
	}
	return vals
}

// The message of a queued fix says where its defect was found and what introduced it,
// on lines anywhere in the message: a `Closes #n` line beside them hides them from
// `git interpret-trailers --parse`, and the trailers ready.sh appends do not change that.
func TestTrailersLintAFixSaysWhereItsDefectWasFound(t *testing.T) {
	e := newLandEnv(t)
	tip := e.git("rev-parse", "HEAD")
	e.git("commit", "-q", "--allow-empty", "-m", "feat(kb): old\n\nLander-Slug: old-change\nLander-Tip: "+tip)
	e.git("commit", "-q", "--allow-empty", "-m", "feat(kb): prose\n\nsee Lander-Slug: ghost here\n\nmore text")
	e.git("push", "-q", "origin", "main")
	landed := e.git("rev-parse", "HEAD~1")
	e.git("checkout", "-q", "-b", "side")
	e.git("commit", "-q", "--allow-empty", "-m", "wip")
	off := e.git("rev-parse", "HEAD")
	e.git("checkout", "-q", "main")

	msg := "fix(kb): repair the index\n\nWhy.\n\nFound-by: gate\nIntroduced-by: " + landed[:12] + "\nCloses #12\n"
	if out, ok := e.trailersLint(msg); !ok {
		t.Fatalf("both lines beside Closes #12 must pass\n%s", out)
	}
	m := filepath.Join(e.tmp, "trailered")
	e.write(m, msg)
	if parsed := e.run(e.work, "git", "interpret-trailers", "--parse", m); strings.Contains(parsed, "Found-by") {
		t.Fatalf("the case pinned here is the one where git parses no trailer, got\n%s", parsed)
	}
	e.run(e.work, "git", "interpret-trailers", "--in-place", "--trailer", "Lander-Tip: "+tip, m)
	if out, ok := e.trailersLint(readFileAbs(t, m)); !ok {
		t.Fatalf("the trailers ready.sh appends must not hide the lines\n%s", out)
	}

	for name, msg := range map[string]string{
		"a bundle: one line per defect, refs of every form": "fix: three defects\n\nFound-by: gate\nFound-by: gate\nFound-by: rc\n" +
			"Introduced-by: old-change\nIntroduced-by: #7\nIntroduced-by: " + landed + "\n",
		"unknown alone":                 "fix(kb)!: x\n\nFound-by: user\nIntroduced-by: unknown\n",
		"a contributor's Regression-of": "fix: x\n\nFound-by: review\nRegression-of: #7\n",
		"a feat with neither line":      "feat(kb): x\n\nWhy.\n",
		"a Cost line":                   "feat: x\n\nCost: impl 120k, review 85k, rounds 2\n",
		"a Cost line with ? figures":    "fix: x\n\nFound-by: agent\nIntroduced-by: unknown\nCost: impl ?, review 85k, rounds ?\n",
		"CRLF and no final newline":     "fix: x\r\n\r\nFound-by: main-ci\r\nIntroduced-by: unknown",
	} {
		if out, ok := e.trailersLint(msg); !ok {
			t.Errorf("%s must pass\n%s", name, out)
		}
	}

	for name, c := range map[string]struct{ msg, why string }{
		"no Found-by":                {"fix: x\n\nIntroduced-by: unknown\n", "where its defect was found"},
		"a value outside the tsv":    {"fix: x\n\nFound-by: weekly\nIntroduced-by: unknown\n", "'weekly' is not one of: review gate"},
		"a key in another case":      {"fix: x\n\nfound-by: gate\nIntroduced-by: unknown\n", "the key is spelled Found-by"},
		"no Introduced-by":           {"fix!: x\n\nFound-by: gate\n", "what introduced its defect"},
		"an empty ref":               {"fix: x\n\nFound-by: gate\nIntroduced-by:\n", "names a ref"},
		"a sha not on origin/main":   {"fix: x\n\nFound-by: gate\nIntroduced-by: " + off[:10] + "\n", "not on origin/main"},
		"a slug no commit carries":   {"fix: x\n\nFound-by: gate\nIntroduced-by: nobody\n", "carries Lander-Slug: nobody"},
		"a prefix of a landed slug":  {"fix: x\n\nFound-by: gate\nIntroduced-by: old\n", "carries Lander-Slug: old"},
		"a slug named in prose":      {"fix: x\n\nFound-by: gate\nIntroduced-by: ghost\n", "carries Lander-Slug: ghost"},
		"a ref of no form":           {"fix: x\n\nFound-by: gate\nIntroduced-by: Old_Change\n", "none of"},
		"unknown beside a ref":       {"fix: x\n\nFound-by: gate\nIntroduced-by: unknown\nIntroduced-by: #7\n", "alone only"},
		"two Cost lines":             {"feat: x\n\nCost: impl 1k, review 1k, rounds 1\nCost: impl 1k, review 1k, rounds 1\n", "at most one Cost line"},
		"a Cost field missing":       {"feat: x\n\nCost: impl 1k, review 1k\n", "a Cost line is exactly"},
		"a Cost unit other than k":   {"feat: x\n\nCost: impl 1m, review 1k, rounds 1\n", "a Cost line is exactly"},
		"a Cost key in another case": {"feat: x\n\ncost: impl 1k, review 1k, rounds 1\n", "a Cost line is exactly"},
	} {
		out, ok := e.trailersLint(c.msg)
		if ok || !strings.Contains(out, c.why) {
			t.Errorf("%s must be refused with %q: ok=%v\n%s", name, c.why, ok, out)
		}
	}

	// Every reason is given at once, one per line.
	out, _ := e.trailersLint("fix: x\n\nFound-by: weekly\nIntroduced-by: nobody\nCost: impl 1k\n")
	if n := strings.Count(out, "trailers-lint: "); n != 3 {
		t.Errorf("three reasons, one per line, want 3, got %d\n%s", n, out)
	}

	// --values is the tsv's first column, the vocabulary's one source.
	got := e.run(e.work, "bash", filepath.Join(e.work, "scripts", "trailers-lint.sh"), "--values")
	if want := strings.Join(foundByVocabulary(t, repoRoot(t)), "\n"); got != want {
		t.Errorf("--values = %q, want the first column of scripts/found-by.tsv %q", got, want)
	}
}

// foundByLiteral is a `Found-by:` line a script or a workflow writes.
var foundByLiteral = regexp.MustCompile(`Found-by:[ \t]+([A-Za-z0-9][A-Za-z0-9_-]*)`)

// foundByOutside returns the `Found-by:` values of text that are not in vocab.
func foundByOutside(text string, vocab []string) []string {
	var out []string
	for _, m := range foundByLiteral.FindAllStringSubmatch(text, -1) {
		known := false
		for _, v := range vocab {
			known = known || m[1] == v
		}
		if !known {
			out = append(out, m[1])
		}
	}
	return out
}

// The fix implementer copies an issue's `Found-by:`/`Introduced-by:` lines only from a
// maintainer: anyone can open an issue, and a `Found-by: review` they plant would move the
// fix out of the reviewer evals.
func TestFixImplementerTakesProvenanceLinesOnlyFromAMaintainer(t *testing.T) {
	def := readFile(t, repoRoot(t), ".claude/agents/cartographer-fix-implementer.md")
	var line string
	for _, l := range strings.Split(def, "\n") {
		if strings.Contains(l, "`Found-by: <value>`") {
			line = l
		}
	}
	if line == "" {
		t.Fatal("no line of the fix implementer's definition says how it writes Found-by")
	}
	for _, want := range []string{"author_association", "OWNER, MEMBER or COLLABORATOR", "a comment the command above keeps"} {
		if !strings.Contains(line, want) {
			t.Errorf("the fix implementer's Found-by rule does not take the issue's lines from a maintainer only (missing %q):\n%s", want, line)
		}
	}
}

// A `Found-by:` value a script or a workflow emits (the lander's flaky issues, the
// ci-red issue) is one the lint accepts: scripts/found-by.tsv is the one list.
func TestFoundByValuesEmittedAreInTheVocabulary(t *testing.T) {
	root := repoRoot(t)
	vocab := foundByVocabulary(t, root)
	if bad := foundByOutside("printf 'Found-by: weekly\\n'", vocab); len(bad) != 1 || bad[0] != "weekly" {
		t.Fatalf("the scan must catch a value outside the vocabulary, got %v", bad)
	}
	files, err := AllFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	read := 0
	for _, f := range files {
		if !strings.HasPrefix(f, "scripts/") && !strings.HasPrefix(f, ".github/workflows/") {
			continue
		}
		read++
		for _, v := range foundByOutside(readFile(t, root, f), vocab) {
			t.Errorf("%s writes Found-by: %s, which is not a value of scripts/found-by.tsv", f, v)
		}
	}
	if read < 10 {
		t.Fatalf("the scan read %d files of scripts/ and .github/workflows/: the corpus is wrong", read)
	}
}
