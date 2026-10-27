package repodocs

import (
	"errors"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// scripts/freeze-open.sh is the one place that says whether the freeze (Saturday
// 00:00 to Monday 08:30 Europe/Rome, D989) is open; the Dependabot workflows
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
		{"2026-10-23T21:59:00Z", nil, false},                        // Fri 23:59 CEST
		{"2026-10-23T22:00:00Z", nil, true},                         // Sat 00:00 CEST
		{"2026-10-25T12:00:00Z", nil, true},                         // Sunday
		{"2026-10-26T07:29:00Z", nil, true},                         // Mon 08:29 CET
		{"2026-10-26T07:30:00Z", ptr("completed completed"), false}, // Mon 08:30 CET
		{"2026-10-19T06:29:00Z", nil, true},                         // Mon 08:29 CEST
		{"2026-10-19T06:30:00Z", ptr("completed completed"), false}, // Mon 08:30 CEST
		{"2026-10-27T12:00:00Z", nil, false},                        // Tuesday
		{"2026-10-22T12:00:00Z", nil, false},                        // Thursday
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
// candidate and the Monday train skips (D1180): the arming step asks the script,
// what was armed before Saturday is disarmed, and the re-arm after the train
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
		`"5 22 * * 5"`, `"5 23 * * 5"`, `"+0200 5 22 * * 5" | "+0100 5 23 * * 5"`,
	} {
		if !strings.Contains(arm, want) {
			t.Errorf("dependabot-arm.yml: missing %q", want)
		}
	}
	if strings.Contains(arm, "|| echo") {
		t.Error("dependabot-arm.yml: a failed arm must fail the job, not be swallowed")
	}
}
