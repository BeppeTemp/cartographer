package repodocs

import (
	"bytes"
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
  "pr view") printf '{"title":"%s","author":{"login":"u"},"files":[{"additions":1,"deletions":0,"path":"README.md"}],"closingIssuesReferences":[],"statusCheckRollup":[],"body":"","headRefName":"b","labels":[],"assignees":[]}' "$TITLE" ;;
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
