package repodocs

import (
	"os/exec"
	"runtime"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// scripts/release-as.sh is the one place that compares the release PR version
// with the next release's milestone (D1371, D1384).
func TestReleaseAsVerdict(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the script runs on ubuntu only; bash on Windows may be WSL")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not on PATH")
	}
	cases := []struct {
		title, milestone, want string
		fail                   bool
	}{
		{"chore(main): release 0.27.1", "v0.28.0", "act 0.28.0", false},
		{"chore(main): release 0.28.0", "v0.28.0", "same", false},
		{"chore(main): release 0.28.0", "v0.27.0", "higher 0.28.0 0.27.0", false},
		{"chore(main): release 1.0.0", "v0.28.0", "higher 1.0.0 0.28.0", false},
		{"chore(main): release 0.9.9", "v0.10.0", "act 0.10.0", false}, // numeric, not lexical
		{"chore(main): release 0.27.1", "Backlog", "skip", false},
		{"chore(main): release 0.27.1", "0.28.0", "skip", false},
		{"chore(ci): align the next release", "v0.28.0", "", true},
	}
	for _, c := range cases {
		cmd := exec.Command(bash, "scripts/release-as.sh", c.title, c.milestone)
		cmd.Dir = repoRoot(t)
		out, err := cmd.Output()
		if c.fail {
			if err == nil {
				t.Errorf("%q: want a failure, got %q", c.title, out)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q vs %q: %v", c.title, c.milestone, err)
			continue
		}
		got := strings.TrimSpace(string(out))
		if got != c.want && !(c.want == "skip" && strings.HasPrefix(got, "skip ")) {
			t.Errorf("%q vs %q: %q, want %q", c.title, c.milestone, got, c.want)
		}
	}
}

// The workflow's safety is a few properties of one file (D1371): the PAT does
// every write, nothing of a PR is run, and the merge body carries Release-As.
func TestReleaseAsWorkflowPinsTokenAndTriggers(t *testing.T) {
	raw := readFile(t, repoRoot(t), ".github/workflows/release-as.yml")
	var wf struct {
		On          map[string]any    `yaml:"on"`
		Permissions map[string]string `yaml:"permissions"`
		Jobs        map[string]struct {
			Steps []struct {
				Name string            `yaml:"name"`
				Uses string            `yaml:"uses"`
				Run  string            `yaml:"run"`
				With map[string]any    `yaml:"with"`
				Env  map[string]string `yaml:"env"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal([]byte(raw), &wf); err != nil {
		t.Fatalf("release-as.yml: %v", err)
	}
	if len(wf.On) != 2 {
		t.Errorf("release-as.yml: triggers %v, want workflow_run and workflow_dispatch only", wf.On)
	}
	for _, k := range []string{"workflow_run", "workflow_dispatch"} {
		if _, ok := wf.On[k]; !ok {
			t.Errorf("release-as.yml: missing trigger %s", k)
		}
	}
	if len(wf.Permissions) != 1 || wf.Permissions["contents"] != "read" {
		t.Errorf("release-as.yml: permissions %v, want only contents: read", wf.Permissions)
	}
	const pat = "${{ secrets.RELEASE_PLEASE_TOKEN }}"
	ran, writes, checkouts := false, 0, 0
	for _, job := range wf.Jobs {
		for _, st := range job.Steps {
			if strings.Contains(st.Run, "scripts/release-as.sh") {
				ran = true
			}
			if strings.HasPrefix(st.Uses, "actions/checkout@") {
				checkouts++
				if st.With["persist-credentials"] != false {
					t.Error("release-as.yml: the checkout must set persist-credentials: false")
				}
			}
			for k, v := range st.Env {
				if strings.Contains(v, "github.token") || strings.Contains(v, "secrets.GITHUB_TOKEN") {
					t.Errorf("release-as.yml: step %q passes %s=%q: a write by github.token starts no workflow", st.Name, k, v)
				}
			}
			if strings.Contains(st.Run, "gh pr create") || strings.Contains(st.Run, "gh pr merge") || strings.Contains(st.Run, "git push") {
				writes++
				if st.Env["GH_TOKEN"] != pat || st.Env["RELEASE_PLEASE_TOKEN"] != pat {
					t.Errorf("release-as.yml: step %q must get the PAT as GH_TOKEN and RELEASE_PLEASE_TOKEN (push, PR and auto-merge)", st.Name)
				}
			}
			if strings.Contains(st.Run, "gh pr merge") && !strings.Contains(st.Run, `--body "Release-As: `) {
				t.Errorf("release-as.yml: step %q arms the merge without --body \"Release-As: ...\"; the squash body defaults to the PR body", st.Name)
			}
		}
	}
	// The guard PR lives on one fixed branch, and an open one for the same version
	// ends the run: never a second open guard PR.
	for _, want := range []string{"branch=release-as/next-train", `--head "$branch"`, "already open for", "gh pr close"} {
		if !strings.Contains(raw, want) {
			t.Errorf("release-as.yml: missing %q, which keeps a single guard PR open", want)
		}
	}
	// The next release is the open milestone with the lowest version, numerically;
	// the due date is no longer read (D1384).
	if !strings.Contains(raw, "sort_by(split(\".\")|map(ltrimstr(\"v\")|tonumber))[0]") || strings.Contains(raw, "due_on") {
		t.Error("release-as.yml: the milestone must be selected by version (numeric sort_by), not by due_on")
	}
	if strings.Contains(raw, "|| true") || strings.Contains(raw, "< <(") {
		t.Error("release-as.yml: `|| true` or `read < <(...)` hides a failing gh call or fails -e on an empty result")
	}
	if !ran {
		t.Error("release-as.yml: no step runs scripts/release-as.sh, which is what decides")
	}
	if checkouts != 1 || writes != 1 {
		t.Errorf("release-as.yml: %d checkouts and %d writing steps, want one each", checkouts, writes)
	}
}
