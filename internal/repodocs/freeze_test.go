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
		open bool
	}{
		{"2026-10-23T21:59:00Z", false}, // Fri 23:59 CEST
		{"2026-10-23T22:00:00Z", true},  // Sat 00:00 CEST
		{"2026-10-25T12:00:00Z", true},  // Sunday
		{"2026-10-26T07:29:00Z", true},  // Mon 08:29 CET
		{"2026-10-26T07:30:00Z", false}, // Mon 08:30 CET
		{"2026-10-19T06:29:00Z", true},  // Mon 08:29 CEST
		{"2026-10-19T06:30:00Z", false}, // Mon 08:30 CEST
		{"2026-10-27T12:00:00Z", false}, // Tuesday
		{"2026-10-22T12:00:00Z", false}, // Thursday
	}
	for _, c := range cases {
		at, err := time.Parse(time.RFC3339, c.utc)
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(bash, "scripts/freeze-open.sh")
		cmd.Dir = repoRoot(t)
		cmd.Env = append(cmd.Environ(), "FREEZE_NOW="+strconv.FormatInt(at.Unix(), 10))
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

// A Dependabot bump that arms itself in the freeze moves main off the release
// candidate and the Monday train skips (D1180). The arming step must ask the
// script, and the re-arm after the train must too.
func TestDependabotWorkflowsHonourTheFreeze(t *testing.T) {
	root := repoRoot(t)
	auto := readFile(t, root, ".github/workflows/dependabot-automerge.yml")
	if !strings.Contains(auto, "scripts/freeze-open.sh") || !strings.Contains(auto, "steps.freeze.outputs.open != 'true'") {
		t.Error("dependabot-automerge.yml: the auto-merge step must be skipped while scripts/freeze-open.sh says the freeze is open")
	}
	if !strings.Contains(auto, "ref: main") {
		t.Error("dependabot-automerge.yml: check out main, never the PR, before running a script with a write token")
	}
	arm := readFile(t, root, ".github/workflows/dependabot-arm.yml")
	for _, want := range []string{"workflow_run", "workflows: [release-train]", "scripts/freeze-open.sh", "dependabot-automerge.yml", "gh run rerun", "ref: main"} {
		if !strings.Contains(arm, want) {
			t.Errorf("dependabot-arm.yml: missing %q", want)
		}
	}
}
