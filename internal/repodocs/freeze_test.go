package repodocs

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// scripts/freeze-open.sh is the one place that says whether the freeze is open: a
// release candidate newer than the latest stable release exists (D1384); the
// Dependabot workflows ask it (D1180). TRAP pinned here: the stable release is read
// from the releases listing, never `releases/latest` (a 0.x pre-release flag hides
// it), and a draft is not released yet.
func TestFreezeOpenFromTheCandidate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the script runs on ubuntu only; bash on Windows may be WSL")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not on PATH")
	}
	cases := []struct {
		name      string
		releases  *string // "<tag>:<draft> ..."; nil = not injected (unreadable)
		open      bool
		candidate string // oldest candidate of the pending version, when open
	}{
		{"no candidate", ptr("v0.8.0:false"), false, ""},
		{"candidate newer than stable", ptr("v0.9.0-rc.20260101:true v0.8.0:false"), true, "v0.9.0-rc.20260101"},
		{"stable published, candidate still there", ptr("v0.9.0-rc.20260101:false v0.9.0:false v0.8.0:false"), false, ""},
		{"stable still a draft", ptr("v0.9.0-rc.20260101:false v0.9.0:true v0.8.0:false"), true, "v0.9.0-rc.20260101"},
		{"numeric, not lexical", ptr("v0.10.0-rc.20260101:false v0.9.0:false"), true, "v0.10.0-rc.20260101"},
		{"two candidates of one version", ptr("v0.9.0-rc.20260103:false v0.9.0-rc.20260101:false v0.8.0:false"), true, "v0.9.0-rc.20260101"},
		{"candidate of a lower version", ptr("v0.8.0-rc.20260101:false v0.9.0:false"), false, ""},
		{"unreadable", nil, true, ""},
	}
	for _, c := range cases {
		cmd := exec.Command(bash, "scripts/freeze-open.sh", "--candidate")
		cmd.Dir = repoRoot(t)
		cmd.Env = append(cmd.Environ(), "GH_REPO=example/repo")
		if c.releases != nil {
			cmd.Env = append(cmd.Env, "FREEZE_RELEASES="+*c.releases)
		} else {
			// No injection and a gh that fails: the listing is unreadable and counts as open.
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "gh"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			cmd.Env = append(cmd.Env, "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"))
		}
		out, err := cmd.Output()
		var ee *exec.ExitError
		switch {
		case err == nil:
			if !c.open {
				t.Errorf("%s: freeze reported open, want closed", c.name)
			}
			if got := strings.TrimSpace(string(out)); got != c.candidate {
				t.Errorf("%s: candidate %q, want %q", c.name, got, c.candidate)
			}
		case errors.As(err, &ee) && ee.ExitCode() == 1:
			if c.open {
				t.Errorf("%s: freeze reported closed, want open", c.name)
			}
		default:
			t.Errorf("%s: freeze-open.sh: %v", c.name, err)
		}
	}
}

func ptr(s string) *string { return &s }

// The rule a bot follows to arm auto-merge, shared by both workflows (D329, D1020).
func TestDependabotEligible(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the script runs on ubuntu only; bash on Windows may be WSL")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not on PATH")
	}
	cases := []struct {
		args []string
		ok   bool
	}{
		{[]string{"go_modules", "version-update:semver-minor"}, true},
		{[]string{"github_actions", "version-update:semver-patch", "version-update:semver-minor"}, true},
		{[]string{"go_modules", "version-update:semver-minor", "version-update:semver-major"}, false},
		{[]string{"docker", "version-update:semver-patch"}, false},
	}
	for _, c := range cases {
		cmd := exec.Command(bash, append([]string{"scripts/dependabot-eligible.sh"}, c.args...)...)
		cmd.Dir = repoRoot(t)
		if got := cmd.Run() == nil; got != c.ok {
			t.Errorf("dependabot-eligible.sh %v: eligible=%v, want %v", c.args, got, c.ok)
		}
	}
}

// A Dependabot bump that arms itself in the freeze moves main off the release
// candidate and the release gate skips (D1180): the arming step asks the script,
// what was armed before the candidate is disarmed, and the re-arm after the release
// fails loudly and applies the same eligibility rule.
func TestDependabotWorkflowsHonourTheFreeze(t *testing.T) {
	root := repoRoot(t)
	auto := readFile(t, root, ".github/workflows/dependabot-automerge.yml")
	for _, want := range []string{"scripts/freeze-open.sh", "steps.freeze.outputs.open != 'true'", "scripts/dependabot-eligible.sh", "ref: main"} {
		if !strings.Contains(auto, want) {
			t.Errorf("dependabot-automerge.yml: missing %q (checkout main, never the PR, before running a script with a write token)", want)
		}
	}
	arm := readFile(t, root, ".github/workflows/dependabot-arm.yml")
	for _, want := range []string{
		"workflow_run", "workflows: [release, preview, release-train]", "workflow_dispatch",
		"scripts/freeze-open.sh", "scripts/dependabot-eligible.sh", "gh pr merge --auto --squash", "gh pr merge --disable-auto",
		"cancel-in-progress: false", "GITHUB_STEP_SUMMARY", "exit \"$failed\"", "ref: main",
	} {
		if !strings.Contains(arm, want) {
			t.Errorf("dependabot-arm.yml: missing %q", want)
		}
	}
	// freeze-open.sh lists the releases: without `contents: read` and a token the
	// call fails and the freeze counts as open, so nothing is ever armed.
	for name, raw := range map[string]string{"dependabot-automerge.yml": auto, "dependabot-arm.yml": arm} {
		var wf struct {
			Permissions map[string]string `yaml:"permissions"`
			Jobs        map[string]struct {
				Permissions map[string]string `yaml:"permissions"`
				Steps       []struct {
					Run string            `yaml:"run"`
					Env map[string]string `yaml:"env"`
				} `yaml:"steps"`
			} `yaml:"jobs"`
		}
		if err := yaml.Unmarshal([]byte(raw), &wf); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		calls := 0
		for jobName, job := range wf.Jobs {
			for _, st := range job.Steps {
				if strings.Contains(st.Run, "scripts/freeze-open.sh") {
					calls++
					// Workflow level or job level: zizmor wants write scopes on the job.
					ok := func(m map[string]string) bool { return m["contents"] == "read" || m["contents"] == "write" }
					if !ok(wf.Permissions) && !ok(job.Permissions) {
						t.Errorf("%s: job %s runs freeze-open.sh without `contents: read`", name, jobName)
					}
					if st.Env["GH_TOKEN"] == "" || st.Env["GH_REPO"] == "" {
						t.Errorf("%s: a step runs freeze-open.sh without GH_TOKEN and GH_REPO in its env", name)
					}
				}
			}
		}
		if calls == 0 {
			t.Errorf("%s: no step runs freeze-open.sh", name)
		}
	}
	if strings.Contains(arm, "schedule") || strings.Contains(arm, "Europe/Rome") {
		t.Error("dependabot-arm.yml: no schedule and no time zone: the freeze state decides (D1384)")
	}
	if strings.Contains(arm, "|| echo") {
		t.Error("dependabot-arm.yml: a failed arm must fail the job, not be swallowed")
	}
}
