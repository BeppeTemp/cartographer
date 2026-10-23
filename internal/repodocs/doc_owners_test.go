package repodocs

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// The doc-owner map (scripts/doc-owners.tsv) mirrors the path-mappable rows of
// docs/index.md §Documentation maintenance rules; scripts/doc-owners.sh turns
// "changed files + PR body" into advisory WARN lines for scripts/pr-triage.sh.

type ownerRow struct {
	line       int
	glob, page string
}

const ownersHint = "the file mirrors docs/index.md §Documentation maintenance rules (see its header)"

func readOwners(t *testing.T, root string) []ownerRow {
	t.Helper()
	var rows []ownerRow
	for i, l := range strings.Split(readFile(t, root, "scripts/doc-owners.tsv"), "\n") {
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		f := strings.Split(l, "\t")
		if len(f) != 3 || f[0] == "" || f[1] == "" || f[2] == "" {
			t.Errorf("scripts/doc-owners.tsv:%d: a row needs exactly three non-empty tab-separated fields (glob, page, why); %s", i+1, ownersHint)
			continue
		}
		rows = append(rows, ownerRow{i + 1, f[0], f[1]})
	}
	return rows
}

// globRegexp is the Go form of the script's matcher: a shell case pattern
// restricted to * and ?, where * also crosses /.
func globRegexp(glob string) *regexp.Regexp {
	var b strings.Builder
	b.WriteString("^")
	for _, r := range glob {
		switch r {
		case '*':
			b.WriteString(".*")
		case '?':
			b.WriteString(".")
		default:
			b.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	b.WriteString("$")
	return regexp.MustCompile(b.String())
}

func TestDocOwnersMapIsWellFormed(t *testing.T) {
	root := repoRoot(t)
	rows := readOwners(t, root)
	if len(rows) == 0 {
		t.Fatal("scripts/doc-owners.tsv has no row, so no area is watched")
	}
	valid := regexp.MustCompile(`^[A-Za-z0-9_./*?-]+$`)
	pageRe := regexp.MustCompile(`^docs/[A-Za-z0-9_-]+\.md$`)
	seen := map[string]int{}
	for _, r := range rows {
		at := fmt.Sprintf("scripts/doc-owners.tsv:%d", r.line)
		if !valid.MatchString(r.glob) || strings.HasPrefix(r.glob, "/") {
			t.Errorf("%s: glob %q has a character outside [A-Za-z0-9_./*?-] or a leading /; %s", at, r.glob, ownersHint)
		}
		if first, dup := seen[r.glob]; dup {
			t.Errorf("%s: glob %q repeats line %d (one page per glob); %s", at, r.glob, first, ownersHint)
		}
		seen[r.glob] = r.line
		if !pageRe.MatchString(r.page) || r.page == "docs/index.md" || r.page == "docs/decisions.md" {
			t.Errorf("%s: page %q must be docs/<name>.md and neither index.md nor decisions.md; %s", at, r.page, ownersHint)
			continue
		}
		if _, err := os.Stat(filepath.Join(root, r.page)); err != nil {
			t.Errorf("%s: page %q does not exist; %s", at, r.page, ownersHint)
		}
	}
}

func TestDocOwnersGlobsMatchAFile(t *testing.T) {
	root := repoRoot(t)
	files, err := AllFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("the repository walk returned no file, so every glob would match nothing")
	}
	for _, r := range readOwners(t, root) {
		re := globRegexp(r.glob)
		found := false
		for _, f := range files {
			if re.MatchString(f) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("scripts/doc-owners.tsv:%d: glob %q matches no file, so the row never fires; fix the glob or delete the row", r.line, r.glob)
		}
	}
}

func TestDocOwnersPagesAreInTheMaintenanceTable(t *testing.T) {
	root := repoRoot(t)
	idx := readFile(t, root, "docs/index.md")
	_, rest, ok := strings.Cut(idx, "## Documentation maintenance rules")
	if !ok {
		t.Fatal("docs/index.md has no '## Documentation maintenance rules' section")
	}
	section, _, _ := strings.Cut(rest, "\n## ")
	for _, r := range readOwners(t, root) {
		if !strings.Contains(section, filepath.Base(r.page)) {
			t.Errorf("scripts/doc-owners.tsv: %s is mapped for %q but docs/index.md §Documentation maintenance rules does not name it; add the row to the table, or drop the mapping (the table and the TSV change together)", r.page, r.glob)
		}
	}
}

// runDocOwners runs scripts/doc-owners.sh over classified rows and returns its
// stdout and exit code.
func runDocOwners(t *testing.T, tsv, body string, rows ...string) (string, int) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the script runs on ubuntu and macOS; bash on Windows may be WSL")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not on PATH")
	}
	root := repoRoot(t)
	path := filepath.Join(t.TempDir(), "owners.tsv")
	if err := os.WriteFile(path, []byte(tsv), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bash, "scripts/doc-owners.sh")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "DOC_OWNERS_TSV="+path, "DOCS_PR_BODY="+body)
	cmd.Stdin = strings.NewReader(strings.Join(rows, "\n") + "\n")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	code := 0
	if err := cmd.Run(); err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("doc-owners.sh: %v", err)
		}
		code = ee.ExitCode()
	}
	return out.String(), code
}

func TestDocOwnersScript(t *testing.T) {
	const tsv = "# header\n\nsrc/a/*.go\tdocs/sync.md\trow a\nsrc/b/*.go\tdocs/testing.md\trow b\n"
	prod := func(a, d int, p string) string { return fmt.Sprintf("production\t%d\t%d\t%s", a, d, p) }
	hits := func(out string) int { return strings.Count(out, "WARN: ") }

	t.Run("ten lines hit and nine do not", func(t *testing.T) {
		out, code := runDocOwners(t, tsv, "", prod(10, 0, "src/a/x.go"))
		if code != 0 || hits(out) != 1 || !strings.Contains(out, "src/a/*.go changed (+10/-0 production lines) but docs/sync.md not touched") {
			t.Errorf("10 lines: code %d, output %q", code, out)
		}
		if out, _ := runDocOwners(t, tsv, "", prod(9, 0, "src/a/x.go")); out != "" {
			t.Errorf("9 lines warned: %q", out)
		}
	})
	t.Run("added and deleted both count", func(t *testing.T) {
		if out, _ := runDocOwners(t, tsv, "", prod(5, 0, "src/a/x.go"), prod(0, 5, "src/a/y.go")); hits(out) != 1 {
			t.Errorf("5+5 lines: %q", out)
		}
	})
	t.Run("only production rows count", func(t *testing.T) {
		for _, k := range []string{"test", "docs", "generated"} {
			if out, _ := runDocOwners(t, tsv, "", k+"\t50\t50\tsrc/a/x.go"); out != "" {
				t.Errorf("kind %s warned: %q", k, out)
			}
		}
	})
	t.Run("a touched page silences its row only", func(t *testing.T) {
		out, _ := runDocOwners(t, tsv, "", prod(20, 0, "src/a/x.go"), prod(20, 0, "src/b/x.go"), "docs\t1\t0\tdocs/sync.md")
		if hits(out) != 1 || !strings.Contains(out, "docs/testing.md") || strings.Contains(out, "docs/sync.md") {
			t.Errorf("output %q", out)
		}
	})
	t.Run("the page counts as touched in any kind", func(t *testing.T) {
		if out, _ := runDocOwners(t, tsv, "", prod(20, 0, "src/a/x.go"), "production\t1\t0\tdocs/sync.md"); out != "" {
			t.Errorf("output %q", out)
		}
	})
	t.Run("Docs none with a reason silences and echoes it", func(t *testing.T) {
		for _, body := range []string{"intro\nDocs: none - because\nmore", "intro\r\nDOCS: NONE - because\r\n"} {
			out, code := runDocOwners(t, tsv, body, prod(20, 0, "src/a/x.go"), prod(20, 0, "src/b/x.go"))
			if code != 0 || hits(out) != 0 || !strings.Contains(strings.ToLower(out), "none - because (2 docs-owner warnings silenced)") {
				t.Errorf("body %q: code %d, output %q", body, code, out)
			}
		}
	})
	t.Run("Docs none without a reason does not silence", func(t *testing.T) {
		for _, body := range []string{"Docs: none", "Docs: none -", "see Docs: none - x"} {
			if out, _ := runDocOwners(t, tsv, body, prod(20, 0, "src/a/x.go")); hits(out) != 1 {
				t.Errorf("body %q: %q", body, out)
			}
		}
	})
	t.Run("a malformed map exits 2", func(t *testing.T) {
		if _, code := runDocOwners(t, "src/a/*.go\tdocs/sync.md\n", "", prod(20, 0, "src/a/x.go")); code != 2 {
			t.Errorf("exit code %d, want 2", code)
		}
	})
}

// The glob exists twice, as a bash case pattern in the script and as a regexp
// in the tests above; one table runs through both so they cannot disagree.
func TestDocOwnersGlobParity(t *testing.T) {
	cases := []struct {
		glob, path string
		match      bool
	}{
		{"internal/a/*.go", "internal/a/x.go", true},
		{"internal/a/*.go", "internal/a/b/x.go", true}, // * crosses /
		{"internal/a/*.go", "internal/a/x.gox", false},
		{"internal/a/*.go", "xinternal/a/x.go", false}, // anchored at the start
		{"internal/a/x?.go", "internal/a/xy.go", true},
		{"internal/a/x?.go", "internal/a/x.go", false},
		{"internal/a/x?.go", "internal/a/xyz.go", false},
		{"a.b/c", "aXb/c", false}, // . is literal
		{"a+b", "aab", false},     // + is literal
		{"a+b", "a+b", true},
		{"Makefile", "Makefile", true},
		{"Makefile", "sub/Makefile", false},
		{"scripts/*", "scripts/a.sh", true},
		{"internal/a", "internal/ab", false}, // a prefix does not match
	}
	for _, c := range cases {
		if got := globRegexp(c.glob).MatchString(c.path); got != c.match {
			t.Errorf("regexp: glob %q on %q = %v, want %v", c.glob, c.path, got, c.match)
		}
		out, code := runDocOwners(t, "# t\n"+c.glob+"\tdocs/sync.md\twhy\n", "", "production\t10\t0\t"+c.path)
		if got := strings.Contains(out, "WARN: "); code != 0 || got != c.match {
			t.Errorf("script: glob %q on %q = %v (code %d), want %v", c.glob, c.path, got, code, c.match)
		}
	}
}

func TestPRTriageRunsTheDocOwnersScript(t *testing.T) {
	root := repoRoot(t)
	src := readFile(t, root, "scripts/pr-triage.sh")
	lines := strings.Split(src, "\n")
	call := -1
	for i, l := range lines {
		if strings.Contains(l, "doc-owners.sh") && strings.Contains(l, "DOCS_PR_BODY=") && !strings.HasPrefix(strings.TrimSpace(l), "#") {
			call = i
			break
		}
	}
	if call < 0 {
		t.Fatal("scripts/pr-triage.sh no longer calls doc-owners.sh with DOCS_PR_BODY=$body; the docs-owner WARN never reaches a PR")
	}
	if !strings.Contains(lines[call], "|| echo") {
		t.Errorf("scripts/pr-triage.sh:%d: the doc-owners.sh call must fail open (|| echo \"WARN: ...\"), or a broken map fails the required plan-gate check", call+1)
	}
	if strings.Contains(lines[call], "blocked=1") || (call+1 < len(lines) && strings.Contains(lines[call+1], "blocked=1")) {
		t.Errorf("scripts/pr-triage.sh:%d: the docs-owner check is advisory and must not set blocked=1", call+1)
	}
}
