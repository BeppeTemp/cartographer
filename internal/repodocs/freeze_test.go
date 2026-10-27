package repodocs

import (
	"errors"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// scripts/freeze-open.sh is the one place that says whether the freeze (Wednesday
// 00:00 to Thursday 18:00 Europe/Rome, D1192) is open; the Dependabot workflows
// ask it (D1180). The table crosses both daylight-saving offsets: the clocks
// went back on Sunday 2026-10-25.
func ptr(s string) *string { return &s }

func TestFreezeOpenWindow(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the script runs on ubuntu only; bash on Windows may be WSL")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not on PATH")
	}
	cases := []struct {
		utc  string
		runs *string // statuses of today's scheduled train runs; nil = not read
		open bool
	}{
		{"2026-10-20T21:59:00Z", nil, false},                         // Tue 23:59 CEST
		{"2026-10-20T22:00:00Z", nil, true},                          // Wed 00:00 CEST
		{"2026-10-27T22:59:00Z", nil, false},                         // Tue 23:59 CET
		{"2026-10-27T23:00:00Z", nil, true},                          // Wed 00:00 CET
		{"2026-10-29T16:59:00Z", nil, true},                          // Thu 17:59 CET
		{"2026-10-29T17:00:00Z", ptr("completed completed"), false},  // Thu 18:00 CET, train ran
		{"2026-10-29T17:30:00Z", ptr("completed in_progress"), true}, // Thu, train still running
		{"2026-10-29T17:30:00Z", ptr("completed"), true},             // Thu, second entry not started
		{"2026-10-22T15:59:00Z", nil, true},                          // Thu 17:59 CEST
		{"2026-10-22T16:00:00Z", ptr("completed completed"), false},  // Thu 18:00 CEST, train ran
		{"2026-10-24T12:00:00Z", nil, false},                         // Saturday
		{"2026-10-26T07:00:00Z", nil, false},                         // Monday
		{"2026-10-27T12:00:00Z", nil, false},                         // Tuesday
	}
	for _, c := range cases {
		at, err := time.Parse(time.RFC3339, c.utc)
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(bash, "scripts/freeze-open.sh")
		cmd.Dir = repoRoot(t)
		cmd.Env = append(cmd.Environ(), "FREEZE_NOW="+strconv.FormatInt(at.Unix(), 10))
		if c.runs != nil {
			cmd.Env = append(cmd.Env, "FREEZE_RUNS="+*c.runs)
		}
		err = cmd.Run()
		var ee *exec.ExitError
		switch {
		case err == nil:
			if !c.open {
				t.Errorf("%s: freeze reported open, want closed", c.utc)
			}
		case errors.As(err, &ee) && ee.ExitCode() == 1:
			if c.open {
				t.Errorf("%s: freeze reported closed, want open", c.utc)
			}
		default:
			t.Errorf("%s: freeze-open.sh: %v", c.utc, err)
		}
	}
}

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
// candidate and the Thursday train skips (D1180): the arming step asks the script,
// what was armed before Wednesday is disarmed, and the re-arm after the train
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
		"workflow_run", "workflows: [release-train]", "github.event.workflow_run.event == 'schedule'",
		"scripts/freeze-open.sh", "scripts/dependabot-eligible.sh", "gh pr merge --auto --squash", "gh pr merge --disable-auto",
		"cancel-in-progress: false", "GITHUB_STEP_SUMMARY", "exit \"$failed\"", "ref: main",
		`"5 22 * * 2"`, `"5 23 * * 2"`, `"+0200 5 22 * * 2" | "+0100 5 23 * * 2"`,
	} {
		if !strings.Contains(arm, want) {
			t.Errorf("dependabot-arm.yml: missing %q", want)
		}
	}
	// freeze-open.sh lists workflow runs on a Monday: without `actions: read` and a
	// token the call fails and the freeze counts as open, so nothing is ever armed.
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
					if wf.Permissions["actions"] != "read" && job.Permissions["actions"] != "read" {
						t.Errorf("%s: job %s runs freeze-open.sh without `actions: read`", name, jobName)
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
	if strings.Contains(arm, "|| echo") {
		t.Error("dependabot-arm.yml: a failed arm must fail the job, not be swallowed")
	}
}
