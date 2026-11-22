package repodocs

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The "what runs where" table of docs/testing.md is generated from ci.yml and
// scripts/signoff.sh, so adding, removing or re-conditioning a job, or changing
// HOSTED_JOBS or TARGETS, fails here until the table is regenerated. With -update this
// is `make runs-where`.
func TestRunsWhereTableIsUpToDate(t *testing.T) {
	root := repoRoot(t)
	body, err := RenderRunsWhere(readFile(t, root, ".github/workflows/ci.yml"), readFile(t, root, "scripts/signoff.sh"))
	if err != nil {
		t.Fatal(err)
	}
	checkGeneratedBlock(t, root, "docs/testing.md", RunsWhereBegin, RunsWhereEnd, body, "make runs-where")
}

// A job whose `if:` the generator has no mapping for is a row nobody decided: the
// generator refuses it and names the job.
func TestRunsWhereRefusesAnUnknownCondition(t *testing.T) {
	root := repoRoot(t)
	ci := readFile(t, root, ".github/workflows/ci.yml")
	sh := readFile(t, root, "scripts/signoff.sh")
	extra := "\n  brand-new-job:\n    name: brand-new-job\n    if: github.event_name == 'schedule'\n" +
		"    runs-on: ubuntu-latest\n    steps:\n      - run: echo hi\n"
	_, err := RenderRunsWhere(strings.TrimRight(ci, "\n")+"\n"+extra, sh)
	if err == nil || !strings.Contains(err.Error(), "brand-new-job") {
		t.Fatalf("an unknown if: must fail with the job's name, got %v", err)
	}

	// The two other inputs it cross-checks: a HOSTED_JOBS name that is no job, and a
	// build-host row whose job is gone.
	bad := regexp.MustCompile(`(?m)^HOSTED_JOBS="`).ReplaceAllString(sh, `HOSTED_JOBS="no-such-job `)
	if _, err := RenderRunsWhere(ci, bad); err == nil || !strings.Contains(err.Error(), "no-such-job") {
		t.Errorf("a HOSTED_JOBS name that ci.yml lacks must fail, got %v", err)
	}
	gone := strings.Replace(ci, "\n  test-windows-shard:\n", "\n  test-windows-shard-renamed:\n", 1)
	if gone == ci {
		t.Fatal("ci.yml has no test-windows-shard job: update this test")
	}
	if _, err := RenderRunsWhere(gone, sh); err == nil || !strings.Contains(err.Error(), "test-windows-shard") {
		t.Errorf("a build-host row whose job is gone must fail, got %v", err)
	}

	// A job named in HOSTED_JOBS that a dispatched run skips: hosted_verdict would read it as red.
	skipped := regexp.MustCompile(`(?m)^HOSTED_JOBS="`).ReplaceAllString(sh, `HOSTED_JOBS="quarantine `)
	if _, err := RenderRunsWhere(ci, skipped); err == nil || !strings.Contains(err.Error(), "quarantine") {
		t.Errorf("a HOSTED_JOBS job skipped on a dispatch must fail, got %v", err)
	}

	// The same through a matrix leg: `foo (<x>)` with no `foo` aggregator, skipped on a dispatch.
	leg := strings.TrimRight(ci, "\n") + "\n\n  foo:\n    name: foo (${{ matrix.x }})\n" +
		"    if: github.event_name == 'push'\n    runs-on: ubuntu-latest\n" +
		"    strategy:\n      matrix:\n        x: [a, b]\n    steps:\n      - run: echo hi\n"
	fooSh := regexp.MustCompile(`(?m)^HOSTED_JOBS="`).ReplaceAllString(sh, `HOSTED_JOBS="foo `)
	if _, err := RenderRunsWhere(leg, fooSh); err == nil || !strings.Contains(err.Error(), `"foo"`) {
		t.Errorf("a HOSTED_JOBS matrix leg skipped on a dispatch must fail, got %v", err)
	}
	if _, err := RenderRunsWhere(leg, sh); err != nil {
		t.Errorf("the toy matrix job alone must render (the refusal is HOSTED_JOBS'), got %v", err)
	}

	// A make step of `test` the table cannot read fails rather than drop out of the comparison.
	for _, form := range []string{"PATH=/x:$PATH make test-install", "make smoke e2e", "|\n          echo first\n          make test-install"} {
		odd := strings.Replace(ci, "      - run: make test-install\n", "      - run: "+form+"\n", 1)
		if odd == ci {
			t.Fatal("ci.yml's test job has no `- run: make test-install` step: update this test")
		}
		if _, err := RenderRunsWhere(odd, sh); err == nil || !strings.Contains(err.Error(), "job test") {
			t.Errorf("a test step %q must fail, got %v", form, err)
		}
	}
}

// The rows a reader acts on, pinned so a wrong mapping is not regenerated in silence.
func TestRunsWhereRows(t *testing.T) {
	root := repoRoot(t)
	body, err := RenderRunsWhere(readFile(t, root, ".github/workflows/ci.yml"), readFile(t, root, "scripts/signoff.sh"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"| `changes` | yes | yes | no | no | no | no |",
		"| `test` | yes | no | yes | partly: `make gate smoke-http smoke e2e` | yes | no |",
		"| `gate` | yes | yes | yes | no | yes, outside the verdict | yes |",
		"| `test-macos` | no | no | yes | no | yes | no |",
		"| `forge (<forge>)` | when `test/forge/` changed | when `test/forge/` changed | runs, not waited for | no | no | no |",
		"| `quarantine` | no | no | no | no | yes, outside the verdict | no |",
		"| `main-status` | no | no | no | no | yes | no |",
		"| Does not run, of the `test` job's steps (the hosted gate does) | `make test-install` |",
	} {
		if !strings.Contains(body, want+"\n") {
			t.Errorf("the table lacks the row\n  %s\ngot:\n%s", want, body)
		}
	}
}

// The CONTRIBUTING.md bullet names the required checks, and each is a job some workflow
// defines: the ruleset is not in the tree, so this is where the constant is held honest.
func TestContributingNamesTheRequiredChecks(t *testing.T) {
	root := repoRoot(t)
	defined := map[string]bool{}
	wfs, err := filepath.Glob(filepath.Join(root, ".github", "workflows", "*.yml"))
	if err != nil {
		t.Fatal(err)
	}
	jobName := regexp.MustCompile(`(?m)^    name: (\S+)\s*$`)
	for _, f := range wfs {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range jobName.FindAllStringSubmatch(string(data), -1) {
			defined[m[1]] = true
		}
	}
	c := readFile(t, root, "CONTRIBUTING.md")
	i := strings.Index(c, "- **What runs where**")
	if i < 0 {
		t.Fatal("CONTRIBUTING.md has no \"What runs where\" bullet")
	}
	bullet := c[i:]
	if j := strings.Index(bullet[1:], "\n- "); j >= 0 {
		bullet = bullet[:j+1]
	}
	var names []string
	for _, rc := range RequiredChecks {
		if !defined[rc] {
			t.Errorf("required check %q is the name of no job in .github/workflows", rc)
		}
		names = append(names, "`"+rc+"`")
	}
	want := "The required checks are " + strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1] + "."
	if !strings.Contains(strings.Join(strings.Fields(bullet), " "), want) {
		t.Errorf("the What runs where bullet of CONTRIBUTING.md does not say %q", want)
	}
	if !strings.Contains(bullet, "§Levels") {
		t.Error("the What runs where bullet must point to docs/testing.md §Levels for the table")
	}
}

// The two phrasings that described the build host as the train gate (D2040 made the
// hosted gate the default). A file states one when its text, comment markers and
// backticks dropped and its lines joined, contains it: each was wrapped differently.
var oldGatePhrasings = []string{"gated locally by", "only break is caught on main"}

var commentMarker = regexp.MustCompile(`^\s*(#+|//+|\*+|<!--|-->|;+)\s*`)

func joinedProse(text string) string {
	lines := strings.Split(strings.ReplaceAll(text, "`", ""), "\n")
	for i, l := range lines {
		lines[i] = commentMarker.ReplaceAllString(l, "")
	}
	return strings.Join(strings.Fields(strings.Join(lines, " ")), " ")
}

func TestJoinedProseFindsAWrappedPhrase(t *testing.T) {
	for _, in := range []string{
		"# a Windows- or macOS-only break is caught\n# on `main`, fixed later",
		"  // gated locally\n  // by make signoff",
		"so a macOS-only break is caught\n  on `main` and",
	} {
		got := joinedProse(in)
		if !strings.Contains(got, oldGatePhrasings[0]) && !strings.Contains(got, oldGatePhrasings[1]) {
			t.Errorf("joinedProse(%q) = %q: the phrase is not found", in, got)
		}
	}
}

func TestNoFileDescribesTheBuildHostAsTheGate(t *testing.T) {
	root := repoRoot(t)
	files, err := AllFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range files {
		if strings.HasPrefix(rel, "docs/decisions/") || filepath.Base(rel) == "CHANGELOG.md" ||
			rel == "internal/repodocs/runs_where_test.go" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		if strings.IndexByte(string(data), 0) >= 0 {
			continue // binary
		}
		prose := joinedProse(string(data))
		for _, p := range oldGatePhrasings {
			if strings.Contains(prose, p) {
				t.Errorf("%s says %q: the train gate is hosted by default (D2040); "+
					"describe the build host as an explicit choice (make land GATE=host)", rel, p)
			}
		}
	}
}
