package repodocs

import (
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
)

// ghLineRe is a logical shell line that runs `gh`: only there is `-l` a label flag
// (`ls -l rc`, `wc -l` are not).
var ghLineRe = regexp.MustCompile(`(?:^|[\s($|;&])gh\s`)

// labelUseRe is a label a `gh` command sets or reads: the word after --label, -l,
// --add-label or --remove-label, the name `gh label create` makes, and the `labels=`
// field of a `gh api` call (`-f`/`-F`, a comma-separated list there; a shell variable
// named `labels` is not one). The value is bare or quoted; one that starts with `$` is
// a variable, listed by hand in labelVariables.
var labelUseRe = regexp.MustCompile(`(?:(?:^|\s)(?:--label|--add-label|--remove-label|-l)(?:\s+|=)|\bgh\s+label\s+create\s+|(?:^|\s)(?:-[fF]|--field|--raw-field)\s+["']?labels=)("[^"]*"|'[^']*'|[^\s"'|;&)]+)`)

// labelVariables are the labels a script or a workflow gives `gh` through a variable,
// which no scan can read: each names the file, the variable as the `gh` line spells it,
// the labels it takes, and the text of the line that sets them, which must still be in
// the file (so the list cannot outlive the code).
var labelVariables = []struct {
	file, variable, setBy string
	labels                []string
}{
	{"scripts/escapes.sh", "$1", "gh_issues bug && gh_issues ci-red", []string{"bug", "ci-red"}},
}

// labelsUsed returns the literal labels of the `gh` lines of text, and the variables
// given as labels.
func labelsUsed(text string) (literal, variables []string) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\\\n", " ")
	for _, line := range strings.Split(text, "\n") {
		if !ghLineRe.MatchString(line) {
			continue
		}
		for _, m := range labelUseRe.FindAllStringSubmatch(line, -1) {
			v := strings.Trim(m[1], `"'`)
			if strings.HasPrefix(v, "$") {
				variables = append(variables, v)
				continue
			}
			for _, l := range strings.Split(v, ",") {
				if l = strings.TrimSpace(l); l != "" {
					literal = append(literal, l)
				}
			}
		}
	}
	return literal, variables
}

// contributingLabels is the first column of the label table of CONTRIBUTING.md
// §Issues: every backticked name of that cell (one row may name several).
func contributingLabels(t *testing.T, root string) map[string]bool {
	t.Helper()
	text := strings.ReplaceAll(readFile(t, root, "CONTRIBUTING.md"), "\r\n", "\n")
	start := strings.Index(text, "\n## Issues")
	if start < 0 {
		t.Fatal("CONTRIBUTING.md has no §Issues")
	}
	section := text[start+1:]
	if end := strings.Index(section[1:], "\n## "); end >= 0 {
		section = section[:end+1]
	}
	names := map[string]bool{}
	inTable := false
	for _, line := range strings.Split(section, "\n") {
		switch {
		case strings.HasPrefix(line, "| Label |"):
			inTable = true
		case inTable && strings.HasPrefix(line, "|---"):
		case inTable && strings.HasPrefix(line, "|"):
			cell := strings.SplitN(line, "|", 3)[1]
			for _, m := range regexp.MustCompile("`([^`]+)`").FindAllStringSubmatch(cell, -1) {
				names[m[1]] = true
			}
		case inTable:
			inTable = false
		}
	}
	if !names["bug"] || !names["ci-red"] {
		t.Fatalf("the label table (CONTRIBUTING.md §Issues) was not read: %v", names)
	}
	return names
}

// templateLabels is the `labels:` value of an issue template's frontmatter.
func templateLabels(text string) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return nil
	}
	end := strings.Index(text[4:], "\n---")
	if end < 0 {
		return nil
	}
	var out []string
	for _, line := range strings.Split(text[4:4+end], "\n") {
		v, ok := strings.CutPrefix(line, "labels:")
		if !ok {
			continue
		}
		for _, l := range strings.Split(strings.Trim(strings.TrimSpace(v), "[]"), ",") {
			if l = strings.Trim(strings.TrimSpace(l), `"'`); l != "" {
				out = append(out, l)
			}
		}
	}
	return out
}

// TestLabelTableNamesEveryLabelUsed: the label table (CONTRIBUTING.md §Issues) names
// every label a script or a workflow sets or reads, and every label an issue template
// sets, so the table cannot lag the next label. One way only: a label a maintainer or a
// skill sets (`accepted`) is in the table and in no script.
func TestLabelTableNamesEveryLabelUsed(t *testing.T) {
	for _, c := range []struct {
		line      string
		literal   []string
		variables []string
	}{
		{`gh pr edit 1 --add-label "needs work" --remove-label old`, []string{"needs work", "old"}, nil},
		{`  issue=$(gh issue create -t x -l planted -b "$body")`, []string{"planted"}, nil},
		{"gh issue list \\\n  --label continued --state open", []string{"continued"}, nil},
		{`gh label create made -c B60205 -d "x"`, []string{"made"}, nil},
		{`gh api repos/o/r/issues -f labels=a,b -f "labels=$lbl"`, []string{"a", "b"}, []string{"$lbl"}},
		{`labels=$(gh issue view "$n" --json labels)`, nil, nil},
		{`ls -l rc`, nil, nil},
		{`n=$(gh pr list --limit 5 | wc -l)`, nil, nil},
	} {
		literal, variables := labelsUsed(c.line)
		if strings.Join(literal, "|") != strings.Join(c.literal, "|") || strings.Join(variables, "|") != strings.Join(c.variables, "|") {
			t.Errorf("labelsUsed(%q) = %q, %q; want %q, %q", c.line, literal, variables, c.literal, c.variables)
		}
	}

	root := repoRoot(t)
	table := contributingLabels(t, root)
	files, err := AllFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	used := map[string][]string{} // label → the files that use it
	vars := map[string]bool{}     // file + " " + variable
	read := 0
	for _, f := range files {
		switch {
		case strings.HasPrefix(f, "scripts/") || strings.HasPrefix(f, ".github/workflows/"):
			read++
			literal, variables := labelsUsed(readFile(t, root, f))
			for _, l := range literal {
				used[l] = append(used[l], f)
			}
			for _, v := range variables {
				vars[f+" "+v] = true
			}
		case strings.HasPrefix(f, ".github/ISSUE_TEMPLATE/") && path.Ext(f) == ".md":
			for _, l := range templateLabels(readFile(t, root, f)) {
				used[l] = append(used[l], f)
			}
		}
	}
	if read < 10 {
		t.Fatalf("the scan read %d files of scripts/ and .github/workflows/: the corpus is wrong", read)
	}
	for _, must := range []string{"ci-red", "vuln-scan", "process", "rc-ok", "bug", "idea"} {
		if len(used[must]) == 0 {
			t.Errorf("the scan found no use of the label %q, which a workflow or a template sets: the extraction is wrong", must)
		}
	}

	listed := map[string]bool{}
	for _, v := range labelVariables {
		listed[v.file+" "+v.variable] = true
		if !vars[v.file+" "+v.variable] {
			t.Errorf("labelVariables lists %s %s, which no `gh` line there gives as a label: drop the entry", v.file, v.variable)
		}
		if !strings.Contains(readFile(t, root, v.file), v.setBy) {
			t.Errorf("labelVariables: %s no longer has the line %q that sets %s: update the entry", v.file, v.setBy, v.variable)
		}
		for _, l := range v.labels {
			used[l] = append(used[l], v.file)
		}
	}
	for k := range vars {
		if !listed[k] {
			t.Errorf("%s gives `gh` a label through a variable: list it in labelVariables with the line that sets it", k)
		}
	}

	var missing []string
	for l, where := range used {
		if !table[l] {
			sort.Strings(where)
			missing = append(missing, l+" ("+strings.Join(slices.Compact(where), ", ")+")")
		}
	}
	sort.Strings(missing)
	for _, m := range missing {
		t.Errorf("the label table (CONTRIBUTING.md §Issues) does not name the label %s", m)
	}
}

// TestIssueTemplatesHaveNoProvenanceLine: scripts/escapes.sh reads the first body line
// of an issue that starts with `Found-by:` (any case) as where its defect was found, so a
// template line of that shape would be counted for every issue opened from it. The
// bug template's hint names the two lines inside a comment that starts its line.
func TestIssueTemplatesHaveNoProvenanceLine(t *testing.T) {
	root := repoRoot(t)
	if !strings.Contains(readFile(t, root, "scripts/escapes.sh"), `select(test("^found-by:"; "i"))`) {
		t.Fatal("scripts/escapes.sh no longer matches `^found-by:` case-insensitively: align this test with it")
	}
	provenance := regexp.MustCompile(`(?im)^(found-by|introduced-by):`)
	if provenance.FindString("x\nfound-by: <value>\n") == "" || provenance.FindString(`<!-- add "Found-by: <value>" -->`) != "" {
		t.Fatal("the provenance matcher must flag a line that starts with the field and pass a commented hint")
	}
	files, err := AllFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	read := 0
	for _, f := range files {
		if !strings.HasPrefix(f, ".github/ISSUE_TEMPLATE/") || path.Ext(f) != ".md" {
			continue
		}
		read++
		if m := provenance.FindString(strings.ReplaceAll(readFile(t, root, f), "\r\n", "\n")); m != "" {
			t.Errorf("%s has a line that starts with %q: the escape report would count it on every issue opened from the template", f, m)
		}
	}
	if read < 2 {
		t.Fatalf("read %d issue templates: the corpus is wrong", read)
	}
	bug := readFile(t, root, ".github/ISSUE_TEMPLATE/bug.md")
	for _, want := range []string{"Found-by:", "Introduced-by:", "scripts/found-by.tsv"} {
		if !strings.Contains(bug, want) {
			t.Errorf(".github/ISSUE_TEMPLATE/bug.md has no hint naming %s", want)
		}
	}
}

// textUnits splits markdown into the units an instruction lives in: a paragraph, or one
// item of a list (a numbered list with no blank line between its items is one paragraph,
// and a later item naming the lines must not cover an earlier one). Each unit is joined
// on one line with its whitespace collapsed.
func textUnits(text string) []string {
	var units, cur []string
	flush := func() {
		if len(cur) > 0 {
			units = append(units, strings.Join(strings.Fields(strings.Join(cur, " ")), " "))
			cur = nil
		}
	}
	item := regexp.MustCompile(`^\s*(?:[-*+]|\d+[.)])\s`)
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") || item.MatchString(line) {
			flush()
		}
		if strings.TrimSpace(line) != "" {
			cur = append(cur, line)
		}
	}
	flush()
	return units
}

// opensBugIssueRe is an instruction to open a `bug` issue: a verb of filing, then the
// label in the same sentence (no `.` or `—` between them, which keep "an open issue …
// — … of a `bug` issue" out).
var opensBugIssueRe = regexp.MustCompile("(?i)\\b(?:becomes?|open(?:s|ed)?|files?|filed|records?)\\b[^.—]*?`bug`\\s+issues?\\b")

// TestBugIssueInstructionsNameProvenance: every skill or agent text that has a session
// open a `bug` issue names the `Found-by:` and `Introduced-by:` lines the issue carries
// (CONTRIBUTING.md §Issues), so the fix copies them instead of guessing; and no text
// counts a flaky or broken test as found by `agent`: it is `Found-by: gate` wherever it
// was met (a train gate, the pre-check of `make ready`, a session's own check-hosted run).
func TestBugIssueInstructionsNameProvenance(t *testing.T) {
	for _, c := range []struct {
		text  string
		opens bool
	}{
		{"what becomes an `idea` or a `bug` issue, and whether", true},
		{"the follow-ups opened as `idea` or `bug` issues.", true},
		{"it becomes one `bug` issue each, with the `accepted` label", true},
		{"the coordinator records the review: `REVIEW=x` at queue time, and the findings as `bug` issues", true},
		{"a question or an open issue needs its cause — the design comment of a `bug` issue", false},
		{"never made public (it concerns released code): no `bug` issue, no PR comment", false},
		{"a `bug` issue number (the maintainer's decisions are in its comments)", false},
	} {
		if got := opensBugIssueRe.MatchString(c.text); got != c.opens {
			t.Errorf("opensBugIssueRe on %q = %v, want %v", c.text, got, c.opens)
		}
	}
	if u := textUnits("1. open a `bug` issue\n   still item one\n2. Found-by: x\n\npara\n"); len(u) != 3 || u[0] != "1. open a `bug` issue still item one" {
		t.Fatalf("textUnits splits list items apart and keeps a continuation line: got %q", u)
	}

	root := repoRoot(t)
	files, err := AllFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	clause := regexp.MustCompile(`[.;]`)
	opening := 0
	for _, f := range files {
		skill := strings.HasPrefix(f, ".claude/skills/") && path.Base(f) == "SKILL.md"
		agent := strings.HasPrefix(f, ".claude/agents/") && path.Ext(f) == ".md"
		if !skill && !agent && f != "CONTRIBUTING.md" {
			continue
		}
		for _, u := range textUnits(readFile(t, root, f)) {
			for _, c := range clause.Split(u, -1) {
				if strings.Contains(strings.ToLower(c), "flaky") && strings.Contains(c, "`agent`") {
					t.Errorf("%s counts a flaky or broken test as found by `agent`: it is `Found-by: gate` (CONTRIBUTING.md §Fixes without a plan): %q", f, c)
				}
			}
			if f == "CONTRIBUTING.md" || !opensBugIssueRe.MatchString(u) {
				continue
			}
			opening++
			if !strings.Contains(u, "Found-by:") || !strings.Contains(u, "Introduced-by:") {
				t.Errorf("%s has a session open a `bug` issue without naming its `Found-by:` and `Introduced-by:` lines (CONTRIBUTING.md §Issues): %q", f, u)
			}
		}
	}
	if opening < 4 {
		t.Fatalf("found %d skill or agent texts that open a `bug` issue: the corpus or the matcher is wrong", opening)
	}
	pinned := false
	for _, u := range textUnits(readFile(t, root, "CONTRIBUTING.md")) {
		for _, c := range clause.Split(u, -1) {
			if strings.Contains(c, "flaky") && strings.Contains(c, "`Found-by: gate`") && strings.Contains(c, "check-hosted") {
				pinned = true
			}
		}
	}
	if !pinned {
		t.Error("CONTRIBUTING.md no longer says that a flaky or broken test, met in a gate or a session's own check-hosted run, is `Found-by: gate`")
	}
	// scripts/found-by.tsv says the same in its meaning column: the row is what
	// `trailers-lint.sh --values` points a session at.
	meaning := map[string]string{}
	for _, line := range strings.Split(readFile(t, root, "scripts/found-by.tsv"), "\n") {
		if v, m, ok := strings.Cut(line, "\t"); ok && !strings.HasPrefix(line, "#") {
			meaning[v] = m
		}
	}
	if m := meaning["gate"]; !strings.Contains(m, "flaky") || !strings.Contains(m, "check-hosted") {
		t.Errorf("scripts/found-by.tsv: the gate row must count a flaky or broken test met in a session's own check-hosted run, got %q", meaning["gate"])
	}
	if m := meaning["agent"]; strings.Contains(m, "flaky") || !strings.Contains(m, "reading or using the code") {
		t.Errorf("scripts/found-by.tsv: the agent row is a defect found by reading or using the code, never a flaky test, got %q", m)
	}
}
