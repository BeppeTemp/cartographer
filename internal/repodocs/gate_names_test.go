package repodocs

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// A red gate must say what failed (#1952, #1870): the e2e runner names each failed scenario
// with its first failing assertion, the signoff prints the output of every package a red
// Windows leg failed (not the last 40 lines of the leg), and the lander names the scenarios and
// the panicking packages in the issue it files. These tests pin the three ends of that chain.

// landFns returns the text of the named top-level functions of scripts/land.sh, to run them
// alone: the script is a lander, not a library.
func landFns(t *testing.T, names ...string) string {
	t.Helper()
	script := readFile(t, repoRoot(t), "scripts/land.sh")
	var fns strings.Builder
	for _, name := range names {
		start := strings.Index(script, "\n"+name+"() {")
		if start < 0 {
			t.Fatalf("%s() not found in scripts/land.sh", name)
		}
		end := strings.Index(script[start+1:], "\n}\n")
		if end < 0 {
			t.Fatalf("%s() has no end in scripts/land.sh", name)
		}
		fns.WriteString(script[start : start+1+end+3])
	}
	return fns.String()
}

// landBash runs a bash snippet after the named functions of scripts/land.sh.
func landBash(t *testing.T, fns []string, snippet string, env []string, args ...string) string {
	t.Helper()
	needBash(t)
	cmd := exec.Command("bash", append([]string{"-c", landFns(t, fns...) + "\n" + snippet, "bash"}, args...)...)
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s: %v\n%s", snippet, err, out)
	}
	return string(out)
}

// writeLog writes a gate log to a temporary file.
func writeLog(t *testing.T, body string) string {
	t.Helper()
	logFile := filepath.Join(t.TempDir(), "gate.log")
	if err := os.WriteFile(logFile, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return logFile
}

// failingOf runs scripts/land.sh's failing() over a gate log.
func failingOf(t *testing.T, body string) string {
	t.Helper()
	return strings.TrimSpace(landBash(t, []string{"failing", "safe_names"}, `failing "$1"`, nil, writeLog(t, body)))
}

// The e2e runner's report lists every failed scenario by name and first failing line, in the
// shape the lander reads, and shows the failing scenario's [FAIL] lines after the report.
func TestE2ERunnerNamesEachFailedScenario(t *testing.T) {
	needBash(t)
	if runtime.GOOS == "windows" {
		t.Skip("bash script")
	}
	root := repoRoot(t)
	tmp := t.TempDir()
	e2e := filepath.Join(tmp, "test", "e2e")
	write := func(path, body string, mode os.FileMode) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(e2e, "run.sh"), readFile(t, root, "test/e2e/run.sh"), 0o755)
	// run.sh runs lib/selftest.sh before the scenarios; the real one, not a stub, so the step
	// stays as strict here as in production.
	for _, lib := range []string{"selftest.sh", "assert.sh"} {
		write(filepath.Join(e2e, "lib", lib), readFile(t, root, "test/e2e/lib/"+lib), 0o755)
	}
	write(filepath.Join(tmp, "bin", "make"), "#!/bin/sh\nexit 0\n", 0o755) // run.sh builds first
	sc := filepath.Join(e2e, "scenarios")
	write(filepath.Join(sc, "01_green.sh"), "echo '[PASS] fine'\nexit 0\n", 0o755)
	write(filepath.Join(sc, "02_two_asserts.sh"), "echo '[PASS] setup'\necho '[FAIL] first thing broke'\necho '[FAIL] second thing broke'\nexit 1\n", 0o755)
	write(filepath.Join(sc, "03_crash.sh"), "echo 'starting'\necho 'server did not start'\nexit 3\n", 0o755)
	write(filepath.Join(sc, "04_other.sh"), "echo '[FAIL] another one' >&2\nexit 1\n", 0o755)

	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GIT_") && !strings.HasPrefix(kv, "E2E_") && !strings.HasPrefix(kv, "TMPDIR=") {
			env = append(env, kv)
		}
	}
	// Run outside any repository: run.sh checks that git has no identity, and a clone may set one.
	run := func() ([]byte, error) {
		cmd := exec.Command("bash", filepath.Join(e2e, "run.sh"))
		cmd.Dir = tmp
		cmd.Env = append(env, "TMPDIR="+tmp, "PATH="+filepath.Join(tmp, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
		return cmd.CombinedOutput()
	}
	b, err := run()
	out := string(b)
	if err == nil {
		t.Fatalf("failed scenarios must fail the runner\n%s", out)
	}
	for _, want := range []string{
		"E2E FAILED: 02_two_asserts: [FAIL] first thing broke\n",
		"E2E FAILED: 03_crash: no [FAIL] line, exit 3: server did not start\n",
		"E2E FAILED: 04_other: [FAIL] another one\n",
		" Failed  : 3\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the report lacks %q\n%s", want, out)
		}
	}
	if strings.Contains(out, "E2E FAILED: 01_green") {
		t.Errorf("a green scenario is not named\n%s", out)
	}
	// After the report: the [FAIL] lines of the failed scenario again, so a cut of the middle
	// of the log keeps them.
	report := strings.Index(out, " E2E Report")
	if report < 0 || !strings.Contains(out[report:], "[FAIL] second thing broke") {
		t.Errorf("the failed scenario's [FAIL] lines must follow the report\n%s", out)
	}

	// All green: nothing is named.
	for _, f := range []string{"02_two_asserts", "03_crash", "04_other"} {
		if err := os.Remove(filepath.Join(sc, f+".sh")); err != nil {
			t.Fatal(err)
		}
	}
	b, err = run()
	if err != nil || strings.Contains(string(b), "E2E FAILED") {
		t.Errorf("a green run names nothing and passes: %v\n%s", err, b)
	}
}

// failing() names the e2e scenarios, the panicking packages and the tests of a red log, at
// most 10, each test once, in the charset an issue title is built from.
func TestLanderFailingNamesScenariosAndPanics(t *testing.T) {
	const (
		e2e = " E2E Report\n Failed  : 2\n" +
			"E2E FAILED: 03_config_opencode: [FAIL] file not found: x\n" +
			"E2E FAILED: 15_operational_audit: [FAIL] no key\n"
		testPanic = "--- FAIL: TestBridge (0.00s)\npanic: boom [recovered]\n\tpanic: boom\n\ngoroutine 7 [running]:\nFAIL\tgithub.com/x/p/internal/mcpbridge\t0.5s\n"
	)
	for _, c := range []struct{ name, log, want string }{
		{"two scenarios", e2e + "make: *** [Makefile:188: e2e] Error 1\n",
			"e2e.03_config_opencode e2e.15_operational_audit"},
		{"a panic in a test names the package and the test, once", "ok  \tgithub.com/x/p/internal/a\t0.1s\n" + testPanic,
			"mcpbridge.TestBridge (panic)"},
		{"a panic outside a test names the package and the message",
			"panic: runtime error: invalid memory address [recovered]\ngoroutine 1 [running]:\nFAIL\tgithub.com/x/p/internal/kb\t0.5s\n",
			"kb (panic: runtime error: invalid memory address)"},
		{"a timeout names the running test", "panic: test timed out after 20m0s\n\trunning tests:\n\t\tTestSlow (20m0s)\nFAIL\tgithub.com/x/p/internal/slow\t1200s\n",
			"slow.TestSlow (panic)"},
		{"a frame of the trace names the test", "panic: oops\ngoroutine 5 [running]:\ngithub.com/x/p/internal/frame.TestFr(0xc000)\nFAIL\tgithub.com/x/p/internal/frame\t0.1s\n",
			"frame.TestFr (panic)"},
		{"a panic of a package that passes is not one", "panic: expected and harmless\nok  \tgithub.com/x/p/internal/fine\t0.1s\n", ""},
		{"a message is cut to the title charset", "panic: bad `$(id)` ; rm\nFAIL\tgithub.com/x/p/internal/kb\t0.5s\n", "kb (panic: bad id  rm)"},
		{"the same log, the colours and CRLF of a Windows leg", "\x1b[31m" + strings.ReplaceAll(testPanic, "\n", "\r\n") + "\x1b[0m" + e2e,
			"mcpbridge.TestBridge (panic) e2e.03_config_opencode e2e.15_operational_audit"},
		{"a plain test still named", "--- FAIL: TestPlain (0.01s)\nFAIL\tgithub.com/x/p/internal/kb\t0.1s\n" + e2e,
			"e2e.03_config_opencode e2e.15_operational_audit TestPlain"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := failingOf(t, c.log); got != c.want {
				t.Errorf("failing() = %q, want %q", got, c.want)
			}
		})
	}
	t.Run("a plain test that cuts to the same 60 characters as another is listed once", func(t *testing.T) {
		long := "Test" + strings.Repeat("L", 70)
		got := failingOf(t, "--- FAIL: "+long+"A (0.00s)\n--- FAIL: "+long+"B (0.00s)\nFAIL\tgithub.com/x/p/internal/kb\t0.1s\n")
		if got != long[:60] {
			t.Errorf("failing() = %q, want %q once", got, long[:60])
		}
	})
	t.Run("a confirmed name cut to 60 covers the plain test it ends in", func(t *testing.T) {
		long := "Test" + strings.Repeat("L", 70)
		got := failingOf(t, "--- FAIL: "+long+" (0.00s)\nFAIL\tgithub.com/x/p/internal/kb\t0.1s\nsignoff: confirmed on a targeted retry: kb."+long+"\n")
		if want := ("kb." + long)[:60]; got != want {
			t.Errorf("failing() = %q, want %q", got, want)
		}
	})
	t.Run("at most ten", func(t *testing.T) {
		var b strings.Builder
		for i := 0; i < 13; i++ {
			b.WriteString("E2E FAILED: s" + strconv.Itoa(i) + ": [FAIL] x\n")
		}
		if n := len(strings.Fields(failingOf(t, b.String()))); n != 10 {
			t.Errorf("%d names, want 10", n)
		}
	})
}

// The planner of the signoff's targeted retry never plans a panic or an e2e failure as a Go
// test to run again (its header says so): the new lines the log holds do not widen it.
func TestTestFailuresDoesNotPlanPanicsOrE2EScenarios(t *testing.T) {
	needBash(t)
	for _, c := range []struct{ name, log string }{
		{"e2e report", "E2E FAILED: 03_config_opencode: [FAIL] x\nmake: *** [Makefile:188: e2e] Error 1\n"},
		{"e2e report beside a named test", "--- FAIL: TestA (0.01s)\nFAIL\tgithub.com/x/p/internal/kb\t0.2s\n" +
			"E2E FAILED: 03_config_opencode: [FAIL] x\nmake: *** [Makefile:188: e2e] Error 1\nmake: *** [Makefile:23: test] Error 1\n"},
		{"a Windows panic among passing packages", "ok  \tgithub.com/x/p/internal/a\t0.1s\n--- FAIL: TestBridge (0.00s)\n" +
			"panic: boom [recovered]\n\tpanic: boom\n\ngoroutine 7 [running]:\nFAIL\tgithub.com/x/p/internal/mcpbridge\t0.5s\nok  \tgithub.com/x/p/internal/z\t0.1s\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			log := filepath.Join(t.TempDir(), "gate.log")
			if err := os.WriteFile(log, []byte(c.log), 0o644); err != nil {
				t.Fatal(err)
			}
			err := exec.Command("bash", filepath.Join(repoRoot(t), "scripts", "test-failures.sh"), log).Run()
			ee, ok := err.(*exec.ExitError)
			if !ok || ee.ExitCode() != 3 {
				t.Errorf("want exit 3 (nothing a retry covers), got %v", err)
			}
		})
	}
}

// A red Windows leg prints the output of the packages that failed, from their first failing
// line: the `panic:` header and an earlier package are not lost to a tail of the whole log, and
// the passing packages' output stays out of it. A panic is not retried.
func TestSignoffPrintsTheFailingPackagesOfTheWindowsLeg(t *testing.T) {
	e := newSignoffEnv(t)
	e.goModule()
	e.branch(e.work, "feat/x", "internal/a/a2.go")
	e.write(filepath.Join(e.work, "internal", "a", "a2.go"), "package a\n", 0o644)
	e.run(e.work, "git", "commit", "-q", "-am", "a2")
	fakewin := filepath.Join(filepath.Dir(e.winLog), "bin", "fakewin")

	var b strings.Builder
	b.WriteString("ok  \\tgithub.com/x/p/internal/a\\t0.1s\\r\\n")
	// A package that failed before the panicking one, with the noise a test prints before its verdict.
	b.WriteString("starting a server\\r\\n--- FAIL: TestFirst (0.10s)\\r\\n    first_test.go:9: got 1, want 2\\r\\nFAIL\\r\\nFAIL\\tgithub.com/x/p/internal/first\\t0.2s\\r\\n")
	b.WriteString("noise of the panicking package, before its failure\\r\\n--- FAIL: TestBridge (0.00s)\\r\\npanic: boom [recovered]\\r\\n\\tpanic: boom\\r\\n\\r\\ngoroutine 7 [running]:\\r\\n")
	for i := 0; i < 80; i++ {
		b.WriteString("frame-" + strconv.Itoa(i) + "\\r\\n")
	}
	b.WriteString("exit status 2\\r\\nFAIL\\tgithub.com/x/p/internal/mcpbridge\\t0.5s\\r\\n")
	// What the old `tail -n 40` showed instead: the output of a passing package that ended the log.
	for i := 0; i < 60; i++ {
		b.WriteString("passing-noise-" + strconv.Itoa(i) + "\\r\\n")
	}
	b.WriteString("ok  \\tgithub.com/x/p/internal/z\\t0.1s\\r\\n")

	out, ok := e.signoff(e.work, "SIGNOFF_WINDOWS="+fakewin, "FAKE_WIN_RC=1", "FAKE_WIN_OUT="+b.String())
	if ok || !strings.Contains(out, "the Windows leg failed") {
		t.Fatalf("a red Windows leg must fail the signoff: ok=%v\n%s", ok, out)
	}
	for _, want := range []string{
		"--- FAIL: TestFirst (0.10s)", "first_test.go:9: got 1, want 2", "FAIL\tgithub.com/x/p/internal/first",
		"--- FAIL: TestBridge (0.00s)", "panic: boom [recovered]", "goroutine 7 [running]:", "frame-0\n",
		"lines of the output of github.com/x/p/internal/mcpbridge cut)", "FAIL\tgithub.com/x/p/internal/mcpbridge\t0.5s",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the output lacks %q\n%s", want, out)
		}
	}
	for _, not := range []string{"passing-noise", "noise of the panicking package", "starting a server", "frame-40\n"} {
		if strings.Contains(out, not) {
			t.Errorf("the output holds %q, which is not a failing package's failure (or was cut)\n%s", not, out)
		}
	}
	if got := e.log(e.winLog); strings.Contains(got, "go test -count=1") {
		t.Errorf("a panic is not retried, got %q", got)
	}
	if e.remoteTip("origin", "feat/x") != "" || len(e.posts()) != 0 {
		t.Errorf("nothing may be pushed or posted\n%s", out)
	}
}

// A queued train whose gate failed on two e2e scenarios and passed on the re-run lands, and
// the flaky issue names both scenarios and keeps the first failing line of each (#1952).
func TestLanderNamesTheE2EScenariosOfAFlakyGate(t *testing.T) {
	e := newLandEnv(t)
	e.queue("f", "fix(kb): f\n", "2026-01-01T10:00:00Z", map[string]string{"E2EFLAKY": "x\n"})
	out, ok := e.land()
	if !ok {
		t.Fatalf("land failed\n%s", out)
	}
	if got := strings.Join(e.mainSubjects(), " | "); got != "fix(kb): f" {
		t.Fatalf("main holds %q\n%s", got, out)
	}
	gh := e.log()
	if !strings.Contains(gh, "--title flaky: e2e.03_config_opencode e2e.15_operational_audit failed once and passed on a re-run") {
		t.Errorf("the issue title must name both scenarios:\n%s", gh)
	}
	for _, want := range []string{"Failing tests: e2e.03_config_opencode e2e.15_operational_audit.", "    E2E FAILED: 03_config_opencode: [FAIL] file not found: x", "    E2E FAILED: 15_operational_audit: [FAIL] no key"} {
		if !strings.Contains(gh, want) {
			t.Errorf("the issue body lacks %q:\n%s", want, gh)
		}
	}
	if strings.Contains(gh, "none named") {
		t.Errorf("the failing scenarios are named:\n%s", gh)
	}
}

// A Windows leg that panics among passing packages is named by package (and test) in the
// flaky issue, and the signoff's output the lander keeps holds the `panic:` header.
func TestLanderNamesAWindowsPanic(t *testing.T) {
	e := newLandEnv(t)
	e.write(filepath.Join(e.bin, "fakewin"), `#!/bin/sh
echo "$*" >> "$FAKE_STATE.win"
if [ ! -e "$FAKE_STATE.winpanic" ]; then
  touch "$FAKE_STATE.winpanic"
  printf 'ok  \tgithub.com/x/p/internal/a\t0.1s\n--- FAIL: TestBridge (0.00s)\npanic: boom [recovered]\n\tpanic: boom\n\ngoroutine 7 [running]:\nexit status 2\nFAIL\tgithub.com/x/p/internal/mcpbridge\t0.5s\npanic: oops\n\ngoroutine 9 [running]:\nFAIL\tgithub.com/x/p/internal/other\t0.1s\nok  \tgithub.com/x/p/internal/z\t0.1s\n'
  exit 1
fi
exit 0
`)
	e.queue("w", "fix(changelog): w\n", "2026-01-01T10:00:00Z", map[string]string{"internal/changelog/zz.go": "package changelog\n"})
	out, ok := e.land("SIGNOFF_WINDOWS=" + filepath.Join(e.bin, "fakewin"))
	if !ok {
		t.Fatalf("land failed\n%s", out)
	}
	if got := strings.Join(e.mainSubjects(), " | "); got != "fix(changelog): w" {
		t.Fatalf("main holds %q\n%s", got, out)
	}
	if !strings.Contains(out, "panic: boom [recovered]") {
		t.Errorf("the gate log must keep the panic header of the Windows leg\n%s", out)
	}
	if gh := e.log(); !strings.Contains(gh, "--title flaky: mcpbridge.TestBridge (panic) other (panic: oops) failed once and passed on a re-run") {
		t.Errorf("the issue title must name the panicking packages:\n%s", gh)
	}
}

// The test a panic is named after is the one whose `--- FAIL` line directly precedes the
// `panic:` line, else go test's `running tests:` entry, else the first `.TestX(` frame of the
// first goroutine; a test that failed earlier is not the one that panicked. A `fatal error:`
// (deadlock, concurrent map writes) is named like a panic. A package and a message carry no `.`
// (`www.example.org` would be a link in the issue), and a name is at most 60 characters.
func TestLanderFailingAttributesPanicsToTheRightTest(t *testing.T) {
	const kb = "FAIL\tgithub.com/x/p/internal/kb\t0.1s\n"
	for _, c := range []struct{ name, log, want string }{
		{"an earlier failed test is not the one that panicked",
			"--- FAIL: TestA (0.00s)\n    a_test.go:5: x\n--- FAIL: TestA2 (0.00s)\n    a_test.go:9: y\npanic: boom\n\ngoroutine 7 [running]:\ngithub.com/x/p/internal/kb.TestB(0xc0)\n\t/x/b_test.go:3 +0x1\n" + kb,
			"kb.TestB (panic) TestA TestA2"},
		{"no frame and no verdict line: the package",
			"--- FAIL: TestA (0.00s)\n    a_test.go:5: x\n--- FAIL: TestA2 (0.00s)\n    a_test.go:9: y\npanic: boom\n\ngoroutine 7 [running]:\nmain.helper(0x1)\n" + kb,
			"kb (panic: boom) TestA TestA2"},
		{"the verdict line right before the panic, also of a subtest",
			"--- FAIL: TestX (0.00s)\n    --- FAIL: TestX/sub (0.00s)\npanic: boom [recovered]\n\tpanic: boom\n\ngoroutine 7 [running]:\n" + kb,
			"kb.TestX (panic)"},
		{"the first running test of a timeout, without its subtest",
			"panic: test timed out after 10m0s\n\trunning tests:\n\t\tTestSlow/case (9m0s)\n\t\tTestOther (1m0s)\n" + kb,
			"kb.TestSlow (panic)"},
		{"a timeout is the running test's even after a failed test",
			"--- FAIL: TestA (1.00s)\npanic: test timed out after 10m0s\n\trunning tests:\n\t\tTestSlow (9m0s)\n" + kb,
			"kb.TestSlow (panic) TestA"},
		{"a deadlock is named like a panic",
			"fatal error: all goroutines are asleep - deadlock!\n\ngoroutine 1 [chan receive]:\ngithub.com/x/p/internal/kb.TestDead(0xc0)\n\t/x.go:1 +0x1\n" + kb,
			"kb.TestDead (panic)"},
		{"a fatal error has no verdict line: a failed test before it is not its test",
			"--- FAIL: TestA (0.00s)\nfatal error: concurrent map writes\n\ngoroutine 9 [running]:\nmain.helper(0x1)\n" + kb,
			"kb (panic: concurrent map writes) TestA"},
		{"only the first goroutine's frames name the test",
			"panic: x\n\ngoroutine 1 [running]:\nmain.f()\n\ngoroutine 9 [chan receive]:\ngithub.com/x/p/internal/kb.TestOther(0xc)\n" + kb,
			"kb (panic: x)"},
		{"a closure of a test", "panic: x\n\ngoroutine 5 [running]:\ngithub.com/x/p/internal/kb.TestClo.func1(0xc)\n\t/x/t.go:4\n" + kb,
			"kb.TestClo (panic)"},
		{"a file path is not a frame", "panic: x\n\ngoroutine 5 [running]:\nmain.f()\n\t/x/internal/.TestData(/f.go:4\n" + kb,
			"kb (panic: x)"},
		{"no dot in a message or a package", "panic: see www.example.org now\nFAIL\twww.example.org\t0.1s\n",
			"wwwexampleorg (panic: see wwwexampleorg now)"},
		{"a test the panic name cut is listed once, not as the cut name and the whole one",
			"--- FAIL: Test" + strings.Repeat("L", 80) + " (0.00s)\npanic: " + strings.Repeat("m", 80) + "\nFAIL\tgithub.com/x/p/internal/" + strings.Repeat("p", 80) + "\t0.1s\n",
			strings.Repeat("p", 25) + ".Test" + strings.Repeat("L", 21) + " (panic)"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := failingOf(t, c.log); got != c.want {
				t.Errorf("failing() = %q, want %q", got, c.want)
			}
		})
	}
	t.Run("a message is cut so that the whole name stays within 60", func(t *testing.T) {
		got := failingOf(t, "panic: "+strings.Repeat("m", 90)+"\nFAIL\tgithub.com/x/p/internal/"+strings.Repeat("p", 90)+"\t0.1s\n")
		if len(got) > 60 || !strings.HasPrefix(got, strings.Repeat("p", 25)+" (panic: m") {
			t.Errorf("failing() = %q (%d)", got, len(got))
		}
	})
	t.Run("lines gives one name per line", func(t *testing.T) {
		log := writeLog(t, "panic: oops\nFAIL\tgithub.com/x/p/internal/kb\t0.1s\nE2E FAILED: s1: [FAIL] x\nE2E FAILED: s2: [FAIL] y\n")
		got := landBash(t, []string{"failing", "safe_names"}, `failing "$1" lines`, nil, log)
		if want := "kb (panic: oops)\\ne2e.s1\\ne2e.s2\\n"; got != strings.ReplaceAll(want, "\\n", "\n") {
			t.Errorf("failing lines = %q", got)
		}
	})
}

// Ten long names measured 278 characters in a title; GitHub refuses past 256, and issue_flaky
// only logged a warning: a systemic e2e flake filed nothing. The title is cut at a name
// boundary to at most 200 characters ("and N more"); the body is not touched.
func TestLanderFlakyIssueTitleIsBounded(t *testing.T) {
	var log strings.Builder
	var names []string
	for i := 0; i < 10; i++ {
		n := "2" + strconv.Itoa(i) + "_a_rather_long_scenario_name"
		names = append(names, "e2e."+n)
		log.WriteString("E2E FAILED: " + n + ": [FAIL] x\n")
	}
	if full := "flaky: " + strings.Join(names, " ") + " failed once and passed on a re-run"; len(full) <= 256 {
		t.Fatalf("the fixture is not long enough to be refused: %d", len(full))
	}
	const what = "failed once and passed on a re-run"
	lf := writeLog(t, log.String())
	title := strings.TrimSpace(landBash(t, []string{"failing", "safe_names", "issue_title"},
		`issue_title "$(failing "$1" lines)" "$2"`, nil, lf, what))
	if len(title) > 200 {
		t.Errorf("title of %d characters: %q", len(title), title)
	}
	m := regexp.MustCompile(`^flaky: ((?:e2e\.\S+ )+)and (\d+) more ` + what + `$`).FindStringSubmatch(title)
	if m == nil {
		t.Fatalf("a cut title is `flaky: <names> and N more <what>`: %q", title)
	}
	kept := strings.Fields(m[1])
	if !reflect.DeepEqual(kept, names[:len(kept)]) {
		t.Errorf("the names kept are the first ones, whole: %q", kept)
	}
	if n, _ := strconv.Atoi(m[2]); n != 10-len(kept) || n < 1 {
		t.Errorf("%d kept, %q more", len(kept), m[2])
	}

	// Every name on its own is long: each is cut to 60, and the title still fits.
	long := strings.Repeat("n", 100)
	var b strings.Builder
	for i := 0; i < 10; i++ {
		b.WriteString(long + strconv.Itoa(i) + "\n")
	}
	title = strings.TrimSpace(landBash(t, []string{"safe_names", "issue_title"}, `issue_title "$1" "$2"`, nil, b.String(), what))
	if len(title) > 200 || !strings.HasPrefix(title, "flaky: "+strings.Repeat("n", 60)+" ") || !strings.Contains(title, " and ") {
		t.Errorf("title of %d characters: %q", len(title), title)
	}

	// Short names are left alone, and the title is what gh receives.
	dir := t.TempDir()
	gh := filepath.Join(dir, "gh")
	if err := os.WriteFile(gh, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$OUT\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "args")
	landBash(t, []string{"safe_names", "issue_title", "issue_flaky"},
		`repo=up/proj; log() { :; }; issue_flaky "$1" "$2" "$3"`,
		[]string{"PATH=" + dir + string(os.PathListSeparator) + os.Getenv("PATH"), "OUT=" + out},
		"kb.TestA\nkb.TestB", what, "the body")
	args := strings.Split(strings.TrimSpace(readFile(t, dir, "args")), "\n")
	if want := "flaky: kb.TestA kb.TestB " + what; !reflect.DeepEqual(args, []string{"issue", "create", "-R", "up/proj", "--label", "bug", "--title", want, "--body", "the body"}) {
		t.Errorf("gh got %q", args)
	}
}

// The `E2E FAILED:` lines copied into the body of a public issue come from the gate log, which
// holds the output of the change under test: a carriage return, an escape or a non-ASCII
// character could end the code block, and an `@` is a mention.
func TestLanderE2EReportIsInert(t *testing.T) {
	var b strings.Builder
	b.WriteString("E2E FAILED: s0: [FAIL] x\r@user [x](http://example.com) ![i](http://example.com/i.png)\n")
	b.WriteString("E2E FAILED: s1: [FAIL] \x1b[31mred\x1b[0m \u202e\u2028 \x00 end\n")
	b.WriteString("E2E FAILED: s2: " + strings.Repeat("long ", 100) + "\n")
	for i := 3; i < 14; i++ {
		b.WriteString("E2E FAILED: s" + strconv.Itoa(i) + ": [FAIL] y\n")
	}
	got := landBash(t, []string{"e2e_report"}, `e2e_report "$1"`, nil, writeLog(t, b.String()))
	lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
	if len(lines) != 10 {
		t.Errorf("%d lines, want 10:\n%s", len(lines), got)
	}
	for _, l := range lines {
		if !strings.HasPrefix(l, "    E2E FAILED: s") || len(l) > 204 {
			t.Errorf("a line of the code block is cut and indented: %q", l)
		}
		for _, r := range l {
			if r < ' ' || r > '~' || r == '@' {
				t.Errorf("%q in %q", r, l)
				break
			}
		}
	}
	if !strings.Contains(got, "    E2E FAILED: s0: [FAIL] xuser [x](http://example.com) ![i](http://example.com/i.png)\n") {
		t.Errorf("the forged line is kept, inert (no CR, its @ gone):\n%s", got)
	}
	if out := landBash(t, []string{"e2e_report"}, `e2e_report "$1"`, nil, writeLog(t, "nothing named\n")); out != "" {
		t.Errorf("no E2E FAILED line, no output: %q", out)
	}
}

// The probe comes from a file only the signoff writes (`<log>.probefile`, signoff.sh), never from
// the gate log, which holds the change's own output: a test can print a well-formed line when the
// real signoff printed none. The file itself is reachable by a test on the lander's machine, so a
// line counts only if it starts with the token of its gate, which the signoff alone was given (file
// descriptor 3). What follows is still checked before it becomes the arguments of go test or make
// through `signoff.sh --probe`.
func TestLanderProbeIsValidated(t *testing.T) {
	const token = "0123456789abcdef0123456789abcdef"
	// probeOf: a gate log holding logBody and, when file is not nil, its probe file; tok is the
	// token the lander holds for that gate.
	probeOf := func(t *testing.T, logBody string, file *string, tok string) string {
		t.Helper()
		lf := writeLog(t, logBody)
		if file != nil {
			if err := os.WriteFile(lf+".probefile", []byte(*file), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return strings.TrimSpace(landBash(t, []string{"probe_of"}, `probe_of "$1" "$2"`, nil, lf, tok))
	}
	real := "linux github.com/x/p/internal/kb,github.com/x/p/internal/audit github.com/x/p/internal/mcpserver\n"
	for _, c := range []struct{ name, line, want string }{
		{"linux", real, strings.TrimSpace(real)},
		{"windows, no race list", "windows github.com/x/p/internal/kb -\n", "windows github.com/x/p/internal/kb -"},
		{"only a race list", "linux - github.com/x/p/internal/kb\n", "linux - github.com/x/p/internal/kb"},
		{"CRLF", "linux a/b -\r\n", "linux a/b -"},
		{"no newline", "linux a/b -", "linux a/b -"},
		{"empty: the signoff confirmed nothing", "\n", ""},
		{"nothing to run", "linux - -\n", ""},
		{"an option as a package", "linux -run=X -\n", ""},
		{"an option after a comma", "linux a/b,-bench=. -\n", ""},
		{"an option in the race list", "linux a/b -exec=sh\n", ""},
		{"an extra word", "linux a/b - -x\n", ""},
		{"a missing word", "linux a/b\n", ""},
		{"an unknown platform", "darwin a/b -\n", ""},
		{"a command substitution", "linux $(id) -\n", ""},
		{"a glob", "linux a/*,b -\n", ""},
		{"a semicolon", "linux a;b -\n", ""},
		{"an empty package between commas", "linux a,,b -\n", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			file := token + " " + c.line
			if c.line == "\n" {
				file = c.line
			}
			if got := probeOf(t, "FAIL\tgithub.com/x/p/internal/kb\t0.1s\n", &file, token); got != c.want {
				t.Errorf("probe_of = %q, want %q", got, c.want)
			}
		})
	}
	t.Run("a well-formed line in the log with no file is no probe: full gates", func(t *testing.T) {
		forged := "panic: boom\nFAIL\tgithub.com/x/p/internal/kb\t0.1s\nsignoff: probe: linux github.com/x/p/internal/kb -\n" +
			"signoff: make gate failed (exit 2): nothing pushed, nothing posted\n"
		if got := probeOf(t, forged, nil, token); got != "" {
			t.Errorf("probe_of = %q, want none", got)
		}
	})
	t.Run("a forged line in the log never wins over the file, empty or not", func(t *testing.T) {
		forged := "signoff: probe: linux evil/pkg -\n"
		empty := ""
		if got := probeOf(t, forged, &empty, token); got != "" {
			t.Errorf("empty file: probe_of = %q, want none", got)
		}
		withTok := token + " " + real
		if got := probeOf(t, forged, &withTok, token); got != strings.TrimSpace(real) {
			t.Errorf("probe_of = %q, want the file's words", got)
		}
	})
	t.Run("only the last line of the file counts, as the last signoff line did", func(t *testing.T) {
		two := token + " linux evil/pkg -\n" + token + " " + real
		if got := probeOf(t, "", &two, token); got != strings.TrimSpace(real) {
			t.Errorf("probe_of = %q", got)
		}
		// A line a test appended after the signoff's has no token: the last line is then no probe.
		forgedLast := token + " " + real + "linux evil/pkg -\n"
		if got := probeOf(t, "", &forgedLast, token); got != "" {
			t.Errorf("a tokenless last line: probe_of = %q, want none", got)
		}
	})
	t.Run("a file without the gate's token is no probe", func(t *testing.T) {
		for _, c := range []struct{ name, file, tok string }{
			{"no token at all, a test's file", real, token},
			{"a wrong token", strings.Repeat("f", 32) + " " + real, token},
			{"the token with more characters", token + "0 " + real, token},
			{"the token as a prefix of the word", token[:31] + " " + real, token},
			{"the token alone", token + "\n", token},
			{"the token, no space", token + "linux a/b -\n", token},
			{"the token of another gate", strings.Repeat("a", 32) + " " + real, token},
			{"the lander holds no token", token + " " + real, ""},
		} {
			f := c.file
			if got := probeOf(t, "", &f, c.tok); got != "" {
				t.Errorf("%s: probe_of = %q, want none", c.name, got)
			}
		}
	})
	t.Run("land.sh reads the file of the last gate only, through probe_of and its token", func(t *testing.T) {
		script := readFile(t, repoRoot(t), "scripts/land.sh")
		if !strings.Contains(script, `if [ -e "$glog.2" ]; then probe=$(probe_of "$glog.2" "$gate_token"); else probe=$(probe_of "$glog" "$gate_token"); fi`) {
			t.Error("the bisection must take its probe from probe_of over the last gate, with that gate's token")
		}
		if strings.Contains(script, "signoff: probe: //p") {
			t.Error("the probe must not be read from the gate log")
		}
		if !strings.Contains(script, `SIGNOFF_PROBE_FILE="$pf" "$here/signoff.sh" 3<<<"$gate_token")`) {
			t.Error("each gate hands the signoff its own probe file and its token on descriptor 3")
		}
		if strings.Contains(script, "export gate_token") || strings.Contains(script, "set -a") {
			t.Error("the token must stay a plain shell variable")
		}
	})
}

// gate_once makes the probe file empty, gives the signoff its path and a fresh random token on file
// descriptor 3 (in no environment, no argv and no log), and keeps the file only when the signoff's own
// exit is the red one (1): after any other exit, whatever the file holds, there is no probe.
func TestLanderGateHandsTheTokenAndKeepsTheFileOfARedSignoffOnly(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "scripts", "signoff.sh")
	if err := os.MkdirAll(filepath.Dir(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	// The probe file arrives empty; the fake signoff writes a tokened line, as the real one does, and
	// dumps its environment and arguments to look for the token in them.
	body := "#!/bin/bash\n" +
		"[ ! -s \"$SIGNOFF_PROBE_FILE\" ] || echo 'NOT EMPTY' >&2\n" +
		"read -r -u 3 tok; exec 3<&-\n" +
		"{ env; echo \"args: $*\"; } > \"$DUMP\"\n" +
		"printf '%s linux a/b -\\n' \"$tok\" > \"$SIGNOFF_PROBE_FILE\"\n" +
		"[ \"$FAKE_RC\" != 1 ] || echo \"signoff: make gate failed (exit 2): nothing pushed, nothing posted\"\n" +
		"exit \"$FAKE_RC\"\n"
	if err := os.WriteFile(fake, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		rc          string
		wantProbe   bool
		wantReturns string
	}{
		{"1", true, "1"},
		{"0", false, "0"},
		{"69", false, "3"},
		{"2", false, "2"},
		{"130", false, "2"},
	} {
		t.Run("signoff exit "+c.rc, func(t *testing.T) {
			lf := filepath.Join(t.TempDir(), "gate.log")
			dump := filepath.Join(t.TempDir(), "env")
			// A file left by an earlier attempt, with the token of another gate, is gone.
			if err := os.WriteFile(lf+".probefile", []byte("old linux evil/pkg -\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			out := landBash(t, []string{"gate_once", "probe_of"},
				`here=`+dir+`/scripts; wt=`+dir+`; host_rc=69; rc=0; gate_once "$1" >/dev/null 2>&1 || rc=$?
echo "returns=$rc"
echo "token=$gate_token"
if [ -e "$1.probefile" ]; then echo "probe=$(probe_of "$1" "$gate_token")"; else echo "probe=nofile"; fi
echo "tokenenv=$(env | grep -c "$gate_token" || true)"`,
				[]string{"FAKE_RC=" + c.rc, "DUMP=" + dump}, lf)
			get := func(k string) string {
				for _, l := range strings.Split(out, "\n") {
					if v, ok := strings.CutPrefix(l, k+"="); ok {
						return v
					}
				}
				return ""
			}
			if got := get("returns"); got != c.wantReturns {
				t.Errorf("gate_once returned %q, want %q\n%s", got, c.wantReturns, out)
			}
			tok := get("token")
			if !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(tok) {
				t.Fatalf("the token = %q, want 32 hex digits\n%s", tok, out)
			}
			want := "nofile"
			if c.wantProbe {
				want = "linux a/b -"
			}
			if got := get("probe"); got != want {
				t.Errorf("probe = %q, want %q\n%s", got, want, out)
			}
			// The token is in the lander's shell only: not exported, so not in a child's environment, and
			// not in what the signoff saw (its environment, its arguments) or printed (the log).
			if got := get("tokenenv"); got != "0" {
				t.Errorf("the token is in the environment of a child of the lander\n%s", out)
			}
			if d := readFile(t, filepath.Dir(dump), "env"); strings.Contains(d, tok) {
				t.Errorf("the signoff saw the token in its environment or arguments:\n%s", d)
			}
			if l := readFile(t, filepath.Dir(lf), "gate.log"); strings.Contains(l, tok) || strings.Contains(l, "NOT EMPTY") {
				t.Errorf("the gate log holds the token, or the file was not empty at the start:\n%s", l)
			}
		})
	}
}

// Every name a title, a body or a commit message gets is read from the gate log, the change's own
// output: safe_names keeps [A-Za-z0-9_.-] (and a panic's suffix), and defuses what that charset can
// still link: a `www.` (GitHub links `www.example.org`) and a run of 7 to 40 lowercase hex digits (a
// commit link). Each forged shape must come out inert in the names failing() returns, in the title
// issue_title builds and in the body flaky_issue writes.
func TestLanderNamesAreInertInTitleAndBody(t *testing.T) {
	inert := regexp.MustCompile(`^[A-Za-z0-9_.-]+( \(panic(: [A-Za-z0-9 _:-]*)?\))?$`)
	issueRef := regexp.MustCompile(`(?i)gh-[0-9]`)
	token := regexp.MustCompile(`[A-Za-z0-9]+`)
	check := func(t *testing.T, where, got string) {
		t.Helper()
		for _, l := range strings.Split(strings.TrimSpace(got), "\n") {
			if l == "" {
				continue
			}
			if !inert.MatchString(l) || strings.Contains(strings.ToLower(l), "www.") || issueRef.MatchString(l) {
				t.Errorf("%s: %q is not inert", where, l)
			}
			if len(l) > 60 {
				t.Errorf("%s: %q is %d characters, the cut is 60", where, l, len(l))
			}
			// A run of 7 to 40 lowercase hex digits is a commit link unless a `_` follows it, whatever was cut.
			for _, loc := range token.FindAllStringIndex(l, -1) {
				run := l[loc[0]:loc[1]]
				if len(run) >= 7 && len(run) <= 40 && strings.Trim(run, "0123456789abcdef") == "" && !strings.HasPrefix(l[loc[1]:], "_") {
					t.Errorf("%s: %q ends a hex run %q with no `_`", where, l, run)
				}
			}
		}
	}
	z := func(n int) string { return strings.Repeat("z", n) }
	forged := []struct{ name, log, want string }{
		{"an image", " FAIL  ![i](http://example.com/i.png) > t\n", "ihttp__example.com_i.png"},
		{"a link", " FAIL  [x](http://example.com) > t\n", "xhttp__example.com"},
		{"a mention", " FAIL  @user > t\n", "user"},
		{"a mention of a team", " FAIL  @org/team > t\n", "org_team"},
		{"an issue reference", " FAIL  #123 > t\n", "123"},
		{"a www autolink", " FAIL  www.example.org > t\n", "www_example.org"},
		{"a www autolink in capitals", " FAIL  WWW.Example.ORG > t\n", "WWW_Example.ORG"},
		{"a www autolink after an underscore", " FAIL  a_www.example.org > t\n", "a_www_example.org"},
		{"a commit hash", " FAIL  deadbeef1 > t\n", "deadbeef1_"},
		{"a full commit hash", " FAIL  pkg." + strings.Repeat("a1", 20) + " > t\n", "pkg." + strings.Repeat("a1", 20) + "_"},
		{"an issue autolink", " FAIL  GH-123 > t\n", "GH_123"},
		{"an issue autolink in lowercase", " FAIL  gh-9 > t\n", "gh_9"},
		{"an issue autolink inside a name", " FAIL  pkg.TestGH-5x > t\n", "pkg.TestGH_5x"},
		{"a name of 60 ending _deadbeef: the cut keeps the `_` that defuses it", " FAIL  " + z(51) + "_deadbeef > t\n", z(51) + "_deadbee_"},
		{"a name of 60 ending .deadbeef", " FAIL  " + z(51) + ".deadbeef > t\n", z(51) + ".deadbee_"},
		{"a cut that leaves a hex run too short to link", " FAIL  " + z(52) + ".deadbeef > t\n", z(52) + ".deadbe"},
		{"a hex run in the middle grows the name past the cut", " FAIL  a.deadbeef." + z(49) + " > t\n", "a.deadbeef_." + z(48)},
		{"a cut that leaves an issue autolink", " FAIL  " + z(56) + "GH-12 > t\n", z(56) + "GH_1"},
		{"a playwright name of 60 ending _deadbeef", " ✘ 1 " + z(51) + "_deadbeef x\n", z(51) + "_deadbee_"},
		{"a confirmed name of 60 ending _deadbeef", "signoff: confirmed on a targeted retry: " + z(51) + "_deadbeef\n", z(51) + "_deadbee_"},
		{"an e2e scenario of 60 ending _deadbeef", "E2E FAILED: " + z(47) + "_deadbeef: [FAIL] x\n", "e2e." + z(47) + "_deadbee_"},
		{"a long hex run is no hash", " FAIL  " + strings.Repeat("a", 41) + " > t\n", strings.Repeat("a", 41)},
		{"a playwright name", " ✘ 1 www.example.org › x\n", "www_example.org"},
		{"a playwright mention", " ✘ 2 @user rest\n", "user"},
		{"a confirmed name", "signoff: confirmed on a targeted retry: www.example.org\n", "www_example.org"},
		{"an e2e scenario", "E2E FAILED: www.example.org: [FAIL] x\n", "e2e.www_example.org"},
		{"a panic's package", "panic: see www.example.org\nFAIL\twww.example.org\t0.1s\n", "wwwexampleorg (panic: see wwwexampleorg)"},
		{"a panic's test", "--- FAIL: Test.www.example.org (0.00s)\npanic: x\nFAIL\tgithub.com/x/p/internal/kb\t0.1s\n", "kb.Test (panic)"},
	}
	for _, c := range forged {
		t.Run(c.name, func(t *testing.T) {
			got := failingOf(t, c.log)
			if got != c.want {
				t.Errorf("failing() = %q, want %q", got, c.want)
			}
			check(t, "failing", got)
		})
	}
	// The title is built from the same names, whatever reaches it (a name passed in raw too).
	raw := "![i](http://example.com/i.png)\n[x](http://example.com)\n@user\nwww.example.org\ndeadbeef12\n   \n"
	title := strings.TrimSpace(landBash(t, []string{"safe_names", "issue_title"}, `issue_title "$1" "$2"`, nil, raw, "failed"))
	if want := "flaky: ihttp__example.com_i.png xhttp__example.com user www_example.org deadbeef12_ failed"; title != want {
		t.Errorf("title = %q, want %q", title, want)
	}
	// A name cut in a title is as inert as one cut by failing(), and a raw one too long is cut the same way.
	cutRaw := z(51) + "_deadbeef\nGH-77\n"
	cutTitle := strings.TrimSpace(landBash(t, []string{"safe_names", "issue_title"}, `issue_title "$1" "$2"`, nil, cutRaw, "failed"))
	if want := "flaky: " + z(51) + "_deadbee_ GH_77 failed"; cutTitle != want {
		t.Errorf("title = %q, want %q", cutTitle, want)
	}
	// The body of a green gate's issue (flaky_issue) and its title, as gh receives them.
	dir := t.TempDir()
	gh := filepath.Join(dir, "gh")
	if err := os.WriteFile(gh, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$OUT\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "args")
	lf := writeLog(t, "signoff: flaky: www.example.org deadbeef1234 kb.TestOk failed and passed on a targeted retry\n"+
		"signoff: flaky: @user.x failed and passed on a targeted retry\n")
	landBash(t, []string{"safe_names", "issue_title", "issue_flaky", "flaky_issue"},
		`repo=up/proj; branch=train/x; log() { :; }; flaky_issue "$1" "$2"`,
		[]string{"PATH=" + dir + string(os.PathListSeparator) + os.Getenv("PATH"), "OUT=" + out}, lf, "failed")
	args := strings.Split(strings.TrimSpace(readFile(t, dir, "args")), "\n")
	joined := strings.Join(args, "\n")
	if !strings.Contains(joined, "--title\nflaky: www_example.org deadbeef1234_ kb.TestOk failed") ||
		!strings.Contains(joined, "failed on www_example.org deadbeef1234_ kb.TestOk ") {
		t.Errorf("title and body name the inert forms:\n%s", joined)
	}
	if strings.Contains(joined, "@") || strings.Contains(strings.ToLower(joined), "www.") {
		t.Errorf("a forged name reached the issue:\n%s", joined)
	}
}
