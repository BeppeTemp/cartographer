package repodocs

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// pr-title is red after the push when a title's scope is outside its list, and an
// armed auto-merge stalls on it. pr-triage warns before: from the workflow's own
// `scopes` input, never a copy.
func prTitleScopes(t *testing.T) []string {
	t.Helper()
	var wf struct {
		Jobs map[string]struct {
			Steps []struct {
				With map[string]any `yaml:"with"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal([]byte(readFile(t, repoRoot(t), ".github/workflows/pr-title.yml")), &wf); err != nil {
		t.Fatalf("pr-title.yml: %v", err)
	}
	for _, j := range wf.Jobs {
		for _, s := range j.Steps {
			if v, ok := s.With["scopes"].(string); ok {
				return strings.Fields(v)
			}
		}
	}
	t.Fatal("pr-title.yml has no `scopes` input")
	return nil
}

func bashOrSkip(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the script runs on ubuntu and macOS only; bash on Windows may be WSL")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not on PATH")
	}
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not on PATH")
	}
	return bash
}

func TestPRTriageReadsTheWorkflowScopes(t *testing.T) {
	bash := bashOrSkip(t)
	cmd := exec.Command(bash, "scripts/pr-triage.sh", "--title-scopes")
	cmd.Dir = repoRoot(t)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("pr-triage.sh --title-scopes: %v", err)
	}
	got, want := strings.Fields(string(out)), prTitleScopes(t)
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("pr-triage.sh reads scopes %v, pr-title.yml lists %v", got, want)
	}
}

func TestPRTriageWarnsOnAnUnlistedTitleScope(t *testing.T) {
	bash := bashOrSkip(t)
	// A stand-in gh: a PR with the given title and one doc file, no issues.
	dir := t.TempDir()
	fake := `#!/bin/sh
case "$1 $2" in
  "pr view") printf '{"title":"%s","author":{"login":"u"},"closingIssuesReferences":[],"statusCheckRollup":[],"body":"","headRefName":"b","labels":[],"assignees":[]}' "$TITLE" ;;
  "api --paginate") printf '1\t0\tREADME.md\n' ;;
  "api repos/{owner}/{repo}/pulls/1") printf 'MEMBER\tUser\n' ;;
  *) exit 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	scopes := prTitleScopes(t)
	cases := []struct {
		title string
		warn  bool
	}{
		{"test(e2e): anything", true},
		{"docs(brand): anything", true},
		{"fix(" + scopes[0] + ",e2e): two", true},
		{"fix(" + scopes[0] + "): listed", false},
		{"fix(" + scopes[len(scopes)-1] + ")!: listed", false},
		{"fix: no scope is fine", false},
	}
	for _, c := range cases {
		cmd := exec.Command(bash, "scripts/pr-triage.sh", "1")
		cmd.Dir = repoRoot(t)
		cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"), "TITLE="+c.title)
		var out, errb bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errb
		if err := cmd.Run(); err != nil {
			t.Fatalf("%q: pr-triage.sh: %v\n%s%s", c.title, err, out.String(), errb.String())
		}
		got := strings.Contains(out.String(), "is not in the pr-title list")
		if got != c.warn {
			t.Errorf("%q: warned = %v, want %v\n%s", c.title, got, c.warn, out.String())
		}
	}
}

// gh pr view --json files stops at 100 files: a supply-chain file past the
// hundredth must still be seen (#1158).
func TestPRTriageReadsEveryFileOfALargePR(t *testing.T) {
	bash := bashOrSkip(t)
	dir := t.TempDir()
	fake := `#!/bin/sh
case "$1 $2" in
  "pr view") printf '{"title":"docs: many pages","author":{"login":"u"},"closingIssuesReferences":[],"statusCheckRollup":[],"body":"","headRefName":"b","labels":[],"assignees":[]}' ;;
  "api --paginate") i=1; while [ $i -le 150 ]; do printf '1\t0\tdocs/page%d.md\n' $i; i=$((i+1)); done; printf '1\t0\t.github/workflows/ci.yml\n' ;;
  "api repos/{owner}/{repo}/pulls/1") printf 'MEMBER\tUser\n' ;;
  *) exit 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bash, "scripts/pr-triage.sh", "1")
	cmd.Dir = repoRoot(t)
	cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("pr-triage.sh: %v\n%s%s", err, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "supply-chain files changed") {
		t.Errorf("the workflow file after 150 docs pages was not seen\n%s", out.String())
	}
}

// prFile is one element of the array GitHub returns for pulls/<n>/files.
type prFile struct {
	Filename  string `json:"filename"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
}

// The stand-in gh serves the PR's file list the way the API does and applies the
// --jq expression it is given the way `gh api --jq` does (jq -r), so what the test
// drives is the expression pr-triage.sh really passes. With RAW_ROWS set it prints
// those bytes instead, whatever the expression: the rows a regressed expression
// would hand the rest of the script.
const fakeGHFileList = `#!/bin/sh
case "$1 $2" in
  "pr view") printf '{"title":"fix: rows","author":{"login":"u"},"closingIssuesReferences":[],"statusCheckRollup":[],"body":"","headRefName":"b","labels":[],"assignees":[]}' ;;
  "api --paginate")
    if [ -n "$RAW_ROWS" ]; then cat "$RAW_ROWS"; exit 0; fi
    expr=; prev=
    for a in "$@"; do if [ "$prev" = --jq ]; then expr=$a; fi; prev=$a; done
    jq -r "$expr" "$FILES_JSON" ;;
  "api repos/{owner}/{repo}/pulls/1") printf 'MEMBER\tUser\n' ;;
  *) exit 1 ;;
esac
`

// ownersMap watches internal/a/*.go, so a row for a path there reaches the
// arithmetic of doc-owners.sh.
const ownersMap = "internal/a/*.go\tdocs/sync.md\trow a\n"

// gatePR runs `pr-triage.sh --gate 1` on a PR served by the stand-in gh: files as
// the API would list them, or rawRows (exact bytes) in their place when not empty.
// It returns the report and the exit code.
func gatePR(t *testing.T, files []prFile, rawRows string) (string, int) {
	t.Helper()
	bash := bashOrSkip(t)
	dir := t.TempDir()
	write := func(name, content string, mode os.FileMode) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), mode); err != nil {
			t.Fatal(err)
		}
		return p
	}
	js, err := json.Marshal(files)
	if err != nil {
		t.Fatal(err)
	}
	write("gh", fakeGHFileList, 0o755)
	env := append(os.Environ(),
		"PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"FILES_JSON="+write("files.json", string(js), 0o644),
		"DOC_OWNERS_TSV="+write("owners.tsv", ownersMap, 0o644),
		"RAW_ROWS=")
	if rawRows != "" {
		env = append(env, "RAW_ROWS="+write("rows.tsv", rawRows, 0o644))
	}
	cmd := exec.Command(bash, "scripts/pr-triage.sh", "--gate", "1")
	cmd.Dir = repoRoot(t)
	cmd.Env = env
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	code := 0
	if err := cmd.Run(); err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("pr-triage.sh: %v", err)
		}
		code = ee.ExitCode()
	}
	return out.String() + errb.String(), code
}

// markerPath is a file that exists only if a command substitution in a file name
// or a size field was run.
func markerPath(t *testing.T) string {
	t.Helper()
	m := filepath.Join(t.TempDir(), "marker")
	if strings.ContainsAny(m, " \t'\"") {
		t.Skipf("the temporary directory %q has a character the test's file name cannot carry", m)
	}
	return m
}

func noMarker(t *testing.T, marker string) {
	t.Helper()
	if _, err := os.Stat(marker); err == nil {
		t.Errorf("%s exists: a command substitution from the PR file list was run", marker)
	}
}

// A file name with control characters is one row of the file list, not several: the
// tab-separated rows escape them, so the name never becomes a size field. The report
// of a PR holding such a file equals the report of the same PR with a plain name.
func TestPRTriageFileNameWithControlCharactersIsOneRow(t *testing.T) {
	marker := markerPath(t)
	crafted := "internal/a/b.go\na[$(touch " + marker + ")]\t0\tinternal/a/c.go"
	base := prFile{"internal/a/real.go", 12, 0}

	got, gotCode := gatePR(t, []prFile{base, {crafted, 7, 2}}, "")
	want, wantCode := gatePR(t, []prFile{base, {"internal/a/b.go", 7, 2}}, "")

	noMarker(t, marker)
	if got != want || gotCode != wantCode {
		t.Errorf("a file name with a newline, a tab and a substitution changes the report\n--- plain name (exit %d)\n%s--- crafted name (exit %d)\n%s", wantCode, want, gotCode, got)
	}
	// 12 + 7 added and 2 deleted: the file's own numbers, nothing taken from its name.
	for _, s := range []string{"Size: production +19/-2 ", "internal/a/*.go changed (+19/-2 production lines)"} {
		if !strings.Contains(got, s) {
			t.Errorf("the report lacks %q\n%s", s, got)
		}
	}
	if gotCode != 0 {
		t.Errorf("exit code %d, want 0 (19 production lines are under the cap)", gotCode)
	}
}

// A name that fakes a row with a negative size must not lower the production sum:
// 900 real lines stay 900 + 1, over the cap, and the review tier does not drop.
func TestPRTriageFileNameCannotLowerTheSizeSum(t *testing.T) {
	crafted := "internal/h/b.go\n-900\t0\tinternal/h/c.go"
	big := prFile{"internal/big/gen.go", 900, 0}

	got, code := gatePR(t, []prFile{big, {crafted, 1, 0}}, "")
	want, wantCode := gatePR(t, []prFile{big, {"internal/h/b.go", 1, 0}}, "")

	if code != 1 || !strings.Contains(got, "BLOCK: over the size cap (901 > 500 production lines)") {
		t.Errorf("exit code %d; the size-cap BLOCK did not fire\n%s", code, got)
	}
	if strings.Contains(got, "Tier: light") || !strings.Contains(got, "Tier: standard") {
		t.Errorf("the review tier dropped\n%s", got)
	}
	if got != want || code != wantCode {
		t.Errorf("a file name with a newline and a tab changes the report\n--- plain name (exit %d)\n%s--- crafted name (exit %d)\n%s", wantCode, want, code, got)
	}
}

// The expression above is the first line of defence; the sums and doc-owners.sh are
// the second: whatever rows reach them, a size field that is not all digits counts for
// nothing and is never evaluated.
func TestPRTriageSkipsRowsWithANonNumericSize(t *testing.T) {
	t.Run("an expression in a size field is not evaluated", func(t *testing.T) {
		marker := markerPath(t)
		rows := fmt.Sprintf("12\t0\tinternal/a/real.go\na[$(touch %s)]\t0\tinternal/a/c.go\n0\ta[$(touch %s)]\tinternal/a/d.go\n", marker, marker)
		out, code := gatePR(t, nil, rows)
		noMarker(t, marker)
		if code != 0 || !strings.Contains(out, "Size: production +12/-0 ") || !strings.Contains(out, "internal/a/*.go changed (+12/-0 production lines)") {
			t.Errorf("exit code %d\n%s", code, out)
		}
	})
	t.Run("a negative size does not lower the production sum", func(t *testing.T) {
		out, code := gatePR(t, nil, "900\t0\tinternal/big/gen.go\n-900\t0\tinternal/h/c.go\n0\t-900\tinternal/h/d.go\n")
		if code != 1 || !strings.Contains(out, "BLOCK: over the size cap (900 > 500 production lines)") || !strings.Contains(out, "Size: production +900/-0 ") {
			t.Errorf("exit code %d\n%s", code, out)
		}
		if strings.Contains(out, "Tier: light") {
			t.Errorf("the review tier dropped\n%s", out)
		}
	})
}
