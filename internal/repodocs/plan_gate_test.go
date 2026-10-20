package repodocs

import (
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The plan-gate workflow runs on `pull_request_target`, which gives the job the
// base repository's token while the PR's author controls the PR (D955). Its
// safety is four properties of one file, and nothing but this test reads them
// all: a later edit that "just needs the PR's script" would turn a rule check
// into remote code execution with a write-capable token.
func TestPlanGateWorkflowNeverRunsPRCode(t *testing.T) {
	root := repoRoot(t)
	raw := readFile(t, root, ".github/workflows/plan-gate.yml")

	var wf struct {
		On          map[string]any `yaml:"on"`
		Permissions map[string]string
		Jobs        map[string]struct {
			Permissions map[string]string `yaml:"permissions"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal([]byte(raw), &wf); err != nil {
		t.Fatalf("plan-gate.yml: %v", err)
	}

	if _, ok := wf.On["pull_request_target"]; !ok {
		t.Error("plan-gate.yml: the PR trigger must be `pull_request_target`, so the workflow and triage.sh come from the base branch")
	}
	if _, ok := wf.On["pull_request"]; ok {
		t.Error("plan-gate.yml: a `pull_request` trigger would run the PR's own copy of the workflow and of triage.sh")
	}
	// Comments carry the explanations (and name the forbidden strings): judge code only.
	var code []string
	for _, l := range strings.Split(raw, "\n") {
		if i := strings.Index(l, "#"); i >= 0 && strings.TrimSpace(l[:i]) == "" {
			continue
		}
		code = append(code, l)
	}
	body := strings.Join(code, "\n")

	if regexp.MustCompile(`github\.event\.pull_request\.head|github\.head_ref|refs/pull/`).MatchString(body) {
		t.Error("plan-gate.yml: references the PR head: only the base SHA may be checked out")
	}
	if regexp.MustCompile(`secrets\.`).MatchString(body) {
		t.Error("plan-gate.yml: uses secrets.*: the job runs with the default read-only token only")
	}
	for perm, level := range wf.Permissions {
		if level != "read" {
			t.Errorf("plan-gate.yml: top-level permission %s: %s, want read", perm, level)
		}
	}
	for name, job := range wf.Jobs {
		for perm, level := range job.Permissions {
			if level == "write" && !(name == "rerun" && perm == "actions") {
				t.Errorf("plan-gate.yml: job %s has %s: write; only the rerun job's actions: write is allowed", name, perm)
			}
		}
	}
}
