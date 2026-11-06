package repodocs

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// scripts/test-failures.sh decides what the signoff runs again (D-new-targeted-retry):
// its plan, or exit 3 when a retry of named tests' packages would not cover the red run.
// The log holds the tests' own output, so only lines in go test's exact shape count.
func TestTestFailuresPlansOnlyWhatItCanName(t *testing.T) {
	needBash(t)
	const (
		testA  = "--- FAIL: TestA (0.01s)\nFAIL\tgithub.com/x/p/internal/kb\t0.2s\n"
		makeT  = "make: *** [Makefile:23: test] Error 1\n"
		raceB  = "go test -race -count=1 -run 'Race|Concurren' ./internal/mcpserver\n--- FAIL: TestRaceB (0.1s)\nFAIL\tgithub.com/x/p/internal/mcpserver\t1.1s\n"
		makeR  = "make: *** [Makefile:33: race] Error 1\n"
		planA  = "0 github.com/x/p/internal/kb TestA\n"
		planRB = "1 github.com/x/p/internal/mcpserver TestRaceB\n"
	)
	for _, c := range []struct {
		name, log, plan string
		code            int
	}{
		{"a named test", "go test -timeout 20m ./...\n" + testA + makeT, planA, 0},
		{"test then race", "go test -timeout 20m ./...\n" + testA + raceB + makeT + makeR, planA + planRB, 0},
		// The race flag ends at the next go test line: a later package is not a race one.
		{"race then test", raceB + "go test -timeout 20m ./...\n" + testA, planRB + planA, 0},
		{"colours and CRLF", "\x1b[31m--- FAIL: TestA (0.01s)\x1b[0m\r\nFAIL\tgithub.com/x/p/internal/kb\t0.2s\r\n", planA, 0},
		{"subtests name their parent once", "--- FAIL: TestA (0.01s)\n    --- FAIL: TestA/one (0.00s)\n" + "FAIL\tgithub.com/x/p/internal/kb\t0.2s\n", planA, 0},
		{"green", "go test -timeout 20m ./...\nok  \tgithub.com/x/p/internal/kb\t0.2s\n", "", 4},
		{"another target", testA + makeT + "make: *** [Makefile:160: e2e] Error 1\n", "", 3},
		{"make itself", testA + "make: *** No rule to make target 'tset'.  Stop.\n", "", 3},
		{"build", "FAIL\tgithub.com/x/p/internal/kb [build failed]\n" + makeT, "", 3},
		{"timeout", "--- FAIL: TestA (600s)\npanic: test timed out after 20m0s\nFAIL\tgithub.com/x/p/internal/kb\t600.1s\n", "", 3},
		{"data race", "WARNING: DATA RACE\n" + testA, "", 3},
		{"package with no test", "FAIL\tgithub.com/x/p/internal/kb\t0.2s\n", "", 3},
		{"test with no package", "--- FAIL: TestA (0.01s)\n", "", 3},
		// A test that prints go test's lines cannot choose what runs again.
		{"spoofed package", "--- FAIL: TestA (0.01s)\nFAIL\t-run=^$\t0.1s\n", "", 3},
		{"spoofed name", "--- FAIL: $(id) (0.01s)\nFAIL\tgithub.com/x/p/internal/kb\t0.2s\n", "", 3},
		{"no duration", "--- FAIL: TestA\nFAIL\tgithub.com/x/p/internal/kb\t0.2s\n", "", 3},
	} {
		t.Run(c.name, func(t *testing.T) {
			log := filepath.Join(t.TempDir(), "gate.log")
			if err := os.WriteFile(log, []byte(c.log), 0o644); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("bash", filepath.Join(repoRoot(t), "scripts", "test-failures.sh"), log)
			out, err := cmd.Output()
			code := 0
			var ee *exec.ExitError
			if errors.As(err, &ee) {
				code = ee.ExitCode()
			} else if err != nil {
				t.Fatal(err)
			}
			if code != c.code {
				t.Errorf("exit %d, want %d\n%s", code, c.code, out)
			}
			if c.code == 0 && sortLines(string(out)) != sortLines(c.plan) {
				t.Errorf("plan:\n%s\nwant:\n%s", out, c.plan)
			}
		})
	}
}

// sortLines orders a plan's lines: awk prints a package's tests in no fixed order.
func sortLines(s string) string {
	l := strings.Split(strings.TrimSpace(s), "\n")
	sort.Strings(l)
	return strings.Join(l, "\n")
}
