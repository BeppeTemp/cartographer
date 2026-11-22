package repodocs

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The gate mode is decided once (docs/testing.md §Before a pull request): signoff.sh gates hosted unless
// SIGNOFF_GATE=host asks for the build host, the lander only by its --gate option, and the mode
// travels out of band: the signoff's first line, the status description, the lander's log lines
// and the `Gate:` line of the train PR.

// statusGrammar is every description the signoff posts: the mode first, so the 140-character
// cut can only lose the tail (` - make <targets>`).
var statusGrammar = regexp.MustCompile(`^gate=(hosted( fallback=host-unavailable)? run=[0-9]+ legs=[0-9]+ [0-9]+m[0-9]{2}s|` +
	`host [a-z0-9]+/[a-z0-9]+ windows=([0-9]+|none|unavailable) web=(yes|no) [0-9]+m[0-9]{2}s( flaky=[0-9]+)?) - make [a-z0-9 -]+$`)

// description is the one status the signoff posted.
func (e *signoffEnv) description() string {
	e.t.Helper()
	p := e.posts()
	if len(p) != 1 {
		e.t.Fatalf("want one status, got %v", p)
	}
	return p[0][strings.Index(p[0], "description=")+len("description="):]
}

// With no variable at all, the signoff gates on ci.yml and runs no make, here or anywhere.
func TestSignoffGatesHostedWhenNothingIsSet(t *testing.T) {
	e := newSignoffEnv(t)
	e.unset("SIGNOFF_GATE")
	e.unset("SIGNOFF_MAKE")
	// A make on PATH that logs, as the fake does: the default `make` must not run either.
	fake, err := os.ReadFile(filepath.Join(filepath.Dir(e.makeLog), "bin", "fakemake"))
	if err != nil {
		t.Fatal(err)
	}
	e.write(filepath.Join(filepath.Dir(e.makeLog), "bin", "make"), string(fake), 0o755)
	e.branch(e.work, "feat/x", "x.txt")
	out, ok := e.signoff(e.work, `FAKE_GH_DISPATCHED=[{"databaseId":7}]`, "FAKE_GH_JOBS="+hostedRun("completed", "completed success", nil))
	if !ok {
		t.Fatalf("signoff failed\n%s", out)
	}
	if !strings.HasPrefix(out, "signoff: gate: hosted\n") {
		t.Errorf("the first line names the mode:\n%s", out)
	}
	if strings.Contains(out, "are not used") {
		t.Errorf("no build-host variable is set, so no notice:\n%s", out)
	}
	if got := e.log(e.makeLog); got != "" {
		t.Errorf("a hosted gate runs no make:\n%s", got)
	}
	if !strings.Contains(e.log(e.ghLog), "workflow run ci.yml -R up/proj --ref feat/x") {
		t.Errorf("ci.yml must be dispatched on the branch:\n%s", e.log(e.ghLog))
	}
	if d := e.description(); !strings.HasPrefix(d, "gate=hosted run=7 legs=8 ") || !strings.HasSuffix(d, " - make gate smoke-http smoke e2e") {
		t.Errorf("the status starts with the mode, the run and the legs: %q", d)
	}
}

// SIGNOFF_GATE=host runs the targets on the build host, and says so first.
func TestSignoffGateHostRunsTheTargets(t *testing.T) {
	e := newSignoffEnv(t)
	e.branch(e.work, "feat/x", "x.txt")
	out, ok := e.signoff(e.work)
	if !ok {
		t.Fatalf("signoff failed\n%s", out)
	}
	if !strings.HasPrefix(out, "signoff: gate: host\n") {
		t.Errorf("the first line names the mode:\n%s", out)
	}
	if got := strings.TrimSpace(e.log(e.makeLog)); got != "-k gate smoke-http smoke e2e" {
		t.Errorf("the build host runs the targets, got %q", got)
	}
	if strings.Contains(e.log(e.ghLog), "workflow run") {
		t.Errorf("a build-host gate dispatches nothing:\n%s", e.log(e.ghLog))
	}
}

// An unknown mode, or the build host asked for together with SIGNOFF_HOSTED, stops before any
// call to gh or make: nothing runs, is pushed or posted.
func TestSignoffRefusesAnUnknownOrContradictoryGate(t *testing.T) {
	for _, c := range []struct {
		name, want string
		env        []string
	}{
		{"unknown value", "signoff: SIGNOFF_GATE is hosted or host: other", []string{"SIGNOFF_GATE=other"}},
		{"host and SIGNOFF_HOSTED", "signoff: SIGNOFF_GATE=host and SIGNOFF_HOSTED contradict each other", []string{"SIGNOFF_GATE=host", "SIGNOFF_HOSTED=1"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newSignoffEnv(t)
			e.branch(e.work, "feat/x", "x.txt")
			out, rc := e.signoffArgs(e.work, nil, c.env...)
			if rc != 1 || !strings.Contains(out, c.want) {
				t.Fatalf("rc=%d, want 1 with %q:\n%s", rc, c.want, out)
			}
			if got := e.log(e.ghLog); got != "" {
				t.Errorf("no gh call before the mode is known:\n%s", got)
			}
			if strings.Contains(out, "signoff: gate: ") {
				t.Errorf("a refused mode is not announced:\n%s", out)
			}
			e.assertUntouched("feat/x", out)
		})
	}
}

// Hosted, the build host's variables are not used, and the notice never prints their value
// (a wrapper's name may be private).
func TestSignoffHostedSaysTheBuildHostVariablesAreUnused(t *testing.T) {
	e := newSignoffEnv(t)
	e.goModule()
	e.branch(e.work, "feat/x", "internal/a/a2.go")
	makeCmd := filepath.Join(filepath.Dir(e.makeLog), "bin", "fakemake")
	winCmd := filepath.Join(filepath.Dir(e.winLog), "bin", "fakewin")
	out, ok := e.signoff(e.work, "SIGNOFF_GATE=hosted", "SIGNOFF_WINDOWS="+winCmd,
		`FAKE_GH_DISPATCHED=[{"databaseId":7}]`, "FAKE_GH_JOBS="+hostedRun("completed", "completed success", nil))
	if !ok {
		t.Fatalf("signoff failed\n%s", out)
	}
	notice := "signoff: the gate is hosted: SIGNOFF_MAKE and SIGNOFF_WINDOWS are not used (SIGNOFF_GATE=host runs the gate on the build host)\n"
	if strings.Count(out, notice) != 1 || !strings.HasPrefix(out, "signoff: gate: hosted\n"+notice) {
		t.Errorf("one notice, right after the mode line:\n%s", out)
	}
	for _, v := range []string{makeCmd, winCmd, "fakemake", "fakewin"} {
		if strings.Contains(out, v) {
			t.Errorf("the output carries the value %q:\n%s", v, out)
		}
	}
	if e.log(e.makeLog) != "" || e.log(e.winLog) != "" {
		t.Errorf("neither command runs: make %q, windows %q", e.log(e.makeLog), e.log(e.winLog))
	}
}

// Every description the signoff can post starts with the mode and fits GitHub's 140 characters
// whole, with an eleven-digit run id, every target and web-gate (the old hosted description,
// with its job names, was cut); the lander's reader turns each into its `Gate:` line.
func TestSignoffStatusDescriptionsStartWithTheGateAndFit(t *testing.T) {
	green := hostedRun("completed", "completed success", nil)
	long := `FAKE_GH_DISPATCHED=[{"databaseId":12345678901}]`
	webBranch := func(e *signoffEnv) {
		e.goModule()
		e.branch(e.work, "feat/x", "internal/a/a2.go")
		e.write(filepath.Join(e.work, "internal", "a", "a2.go"), "package a\n", 0o644)
		e.write(filepath.Join(e.work, "web", "src", "a.ts"), "x\n", 0o644)
		e.run(e.work, "git", "add", ".")
		e.run(e.work, "git", "commit", "-q", "-m", "web")
	}
	for _, c := range []struct {
		name, prefix string
		env          func(e *signoffEnv) []string
	}{
		{"build host, Windows leg, web legs and a flaky retry", "gate=host linux/amd64 windows=3 web=yes ", func(e *signoffEnv) []string {
			return []string{"SIGNOFF_WINDOWS=" + filepath.Join(filepath.Dir(e.winLog), "bin", "fakewin"), "FAKE_WIN_RC=1",
				"FAKE_WIN_OUT=--- FAIL: TestW (1.0s)\\nFAIL\\tgithub.com/x/p/internal/a\\t2.0s\\n"}
		}},
		{"hosted", "gate=hosted run=12345678901 legs=8 ", func(*signoffEnv) []string {
			return []string{"SIGNOFF_GATE=hosted", long, "FAKE_GH_JOBS=" + green}
		}},
		{"hosted, the build host unavailable", "gate=hosted fallback=host-unavailable run=12345678901 legs=8 ", func(*signoffEnv) []string {
			return []string{"FAKE_MAKE_RC=75", long, "FAKE_GH_JOBS=" + green}
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newSignoffEnv(t)
			webBranch(e)
			out, ok := e.signoff(e.work, c.env(e)...)
			if !ok {
				t.Fatalf("signoff failed\n%s", out)
			}
			d := e.description()
			if len(d) > 140 || !strings.HasSuffix(d, " - make gate smoke-http smoke e2e web-gate") {
				t.Errorf("the description is cut or too long (%d characters): %q", len(d), d)
			}
			if !strings.HasPrefix(d, c.prefix) || !statusGrammar.MatchString(d) {
				t.Errorf("the description %q does not start with %q in the signoff's grammar", d, c.prefix)
			}
			if c.name == "build host, Windows leg, web legs and a flaky retry" && !strings.Contains(d, " flaky=1 - make ") {
				t.Errorf("the flaky retry is counted: %q", d)
			}
			if got := gateLineOf(t, d); got != "Gate: "+strings.TrimSuffix(d, " - make gate smoke-http smoke e2e web-gate") {
				t.Errorf("the lander reads %q as %q", d, got)
			}
		})
	}
}

// The platform is the one word of the description the tree under test prints (its
// signoff-platform target): anything but <os>/<arch> stops the signoff, so a change cannot add
// a `web=yes` or `windows=` word to the status the lander copies into the train PR.
func TestSignoffRefusesAPlatformThatIsNotOsArch(t *testing.T) {
	e := newSignoffEnv(t)
	e.branch(e.work, "feat/x", "x.txt")
	out, ok := e.signoff(e.work, "FAKE_PLATFORM=linux/amd64 web=yes windows=99")
	if ok || !strings.Contains(out, "the platform probe printed no <os>/<arch>") {
		t.Fatalf("a platform with words after it must stop the signoff: ok=%v\n%s", ok, out)
	}
	e.assertUntouched("feat/x", out)
}

// gateLineOf runs scripts/land.sh's gate_line over a status description, a stub gh answering it.
func gateLineOf(t *testing.T, desc string) string {
	t.Helper()
	return strings.TrimSpace(landBash(t, []string{"gate_line"},
		`repo=up/proj; gh() { printf '%s' "$DESC"; }; gate_line 0123456789abcdef0123`, []string{"DESC=" + desc}))
}

// The lander copies a description of the signoff's shape up to ` - make`, and anything else (the
// old description, a forged word, nothing) is `Gate: unknown`: the line reaches a public PR body.
func TestLanderGateLineAcceptsOnlyTheSignoffsShape(t *testing.T) {
	unknown := "Gate: unknown (the signoff status of 0123456789ab could not be read)"
	for _, c := range []struct{ desc, want string }{
		{"gate=hosted run=7 legs=8 9m59s - make gate smoke-http smoke e2e", "Gate: gate=hosted run=7 legs=8 9m59s"},
		{"gate=host linux/amd64 windows=none web=no 1m00s - make gate smoke-http smoke e2e", "Gate: gate=host linux/amd64 windows=none web=no 1m00s"},
		{"make gate smoke-http smoke e2e - linux/amd64 - 1m00s - windows not run", unknown},
		{"gate=host linux/amd64 @user web=yes 1m00s - make gate", unknown},
		{"gate=host [x](https://example.com) - make gate", unknown},
		{"gate=other run=7 - make gate", unknown},
		{"", unknown},
	} {
		if got := gateLineOf(t, c.desc); got != c.want {
			t.Errorf("%q: got %q, want %q", c.desc, got, c.want)
		}
	}
}

// trainBody is the body of the one train PR the lander opened in e.
func (e *landEnv) trainBody() string {
	e.t.Helper()
	files, _ := filepath.Glob(filepath.Join(e.tmp, "lander", "body-*.md"))
	if len(files) != 1 {
		e.t.Fatalf("want one train PR body, got %v", files)
	}
	b, err := os.ReadFile(files[0])
	if err != nil {
		e.t.Fatal(err)
	}
	return string(b)
}

// assertGateLine checks the body holds exactly one `Gate:` line, its third, matching want.
func (e *landEnv) assertGateLine(want *regexp.Regexp) {
	e.t.Helper()
	body := e.trainBody()
	lines := strings.Split(body, "\n")
	if len(lines) < 3 || !want.MatchString(lines[2]) || strings.Count(body, "Gate: ") != 1 {
		e.t.Errorf("the train PR's third line, and its only Gate line, must match %s:\n%s", want, body)
	}
}

// No environment variable puts the lander on the build host: without --gate it gates on ci.yml
// whatever the shell exported, says why once, and says which gate the train had; --gate host
// runs the build host's make, whatever SIGNOFF_HOSTED says.
func TestLanderGatesHostedUnlessTheOptionSaysHost(t *testing.T) {
	t.Run("no option, SIGNOFF_GATE=host in the environment", func(t *testing.T) {
		e := newLandEnv(t)
		e.queue("one", "fix(kb): one\n", "2026-01-01T10:00:00Z", map[string]string{"one.txt": "1\n"})
		out, rc := e.landArgs(nil, "SIGNOFF_GATE=host")
		if rc != 0 {
			t.Fatalf("land failed (%d)\n%s", rc, out)
		}
		notice := "the environment asks for the build host, the lander gates hosted: pass --gate host to use it"
		if strings.Count(out, notice) != 1 {
			t.Errorf("the notice is logged once:\n%s", out)
		}
		for _, want := range []string{" gate: hosted, ci.yml, 8 legs\n", ": 1 change(s): one [gate: hosted]\n", "signoff: gate: hosted\n"} {
			if !strings.Contains(out, want) {
				t.Errorf("the log lacks %q:\n%s", want, out)
			}
		}
		if !strings.Contains(e.log(), "workflow run ci.yml -R up/proj --ref train/") {
			t.Errorf("the train is gated on ci.yml:\n%s", e.log())
		}
		if strings.Contains(readTmpOrEmpty(e.tmp, "gh-state.make"), "-k gate") {
			t.Errorf("a hosted train runs no make gate:\n%s", readTmpOrEmpty(e.tmp, "gh-state.make"))
		}
		if got := strings.TrimSpace(readTmpOrEmpty(e.tmp, "gh-state.gate-seen")); got != "hosted" {
			t.Errorf("the running lander's gate file says %q, want hosted", got)
		}
		e.assertGateLine(regexp.MustCompile(`^Gate: gate=hosted run=7 legs=8 [0-9]+m[0-9]{2}s$`))
		if got := strings.Join(e.mainSubjects(), " | "); got != "fix(kb): one" {
			t.Errorf("main holds %q", got)
		}
	})
	t.Run("--gate host, SIGNOFF_HOSTED in the environment", func(t *testing.T) {
		e := newLandEnv(t)
		e.queue("one", "fix(kb): one\n", "2026-01-01T10:00:00Z", map[string]string{"one.txt": "1\n"})
		out, ok := e.land("SIGNOFF_HOSTED=1")
		if !ok {
			t.Fatalf("land failed\n%s", out)
		}
		for _, want := range []string{" gate: host, the build host\n", ": 1 change(s): one [gate: host]\n", "signoff: gate: host\n"} {
			if !strings.Contains(out, want) {
				t.Errorf("the log lacks %q:\n%s", want, out)
			}
		}
		if strings.Contains(out, "the environment asks") || strings.Contains(e.log(), "workflow run") {
			t.Errorf("--gate host gates on the build host, no notice and no dispatch:\n%s", out)
		}
		if n := strings.Count(readTmpOrEmpty(e.tmp, "gh-state.make"), "-k gate"); n != 1 {
			t.Errorf("one gate on the build host's make, got %d", n)
		}
		e.assertGateLine(regexp.MustCompile(`^Gate: gate=host linux/amd64 windows=none web=no [0-9]+m[0-9]{2}s$`))
	})
}

// A statuses read that fails is `Gate: unknown`, a warning, and a merged train: the gate was green.
func TestLanderGateLineIsUnknownWhenTheStatusCannotBeRead(t *testing.T) {
	e := newLandEnv(t)
	e.queue("one", "fix(kb): one\n", "2026-01-01T10:00:00Z", map[string]string{"one.txt": "1\n"})
	out, ok := e.land("FAKE_STATUS_FAILS=1")
	if !ok {
		t.Fatalf("an unreadable status must not stop the lander\n%s", out)
	}
	if !strings.Contains(out, "warning: Gate: unknown (the signoff status of ") {
		t.Errorf("the lander warns:\n%s", out)
	}
	e.assertGateLine(regexp.MustCompile(`^Gate: unknown \(the signoff status of [0-9a-f]{12} could not be read\)$`))
	if got := strings.Join(e.mainSubjects(), " | "); got != "fix(kb): one" {
		t.Errorf("the train lands anyway, main holds %q", got)
	}
}

// A bad option is usage and exit 2 before anything starts: no state, no git or gh call.
func TestLanderRefusesABadOption(t *testing.T) {
	for _, args := range [][]string{
		{"--gate", "nope"}, {"--gate"}, {"--gate", "host", "--gate", "hosted"}, {"--status", "--gate", "host"},
		{"--daemon", "--dry-run"}, {"--bogus"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			e := newLandEnv(t)
			out, rc := e.landArgs(args)
			if rc != 2 || !strings.Contains(out, "usage: land.sh") {
				t.Fatalf("rc=%d, want 2 with the usage:\n%s", rc, out)
			}
			if _, err := os.Stat(filepath.Join(e.tmp, "lander")); err == nil {
				t.Error("nothing starts: no state directory")
			}
			if got := e.log(); got != "" {
				t.Errorf("no gh call:\n%s", got)
			}
		})
	}
}

// --status names the gate of the running lander, from the file it writes beside its pid.
func TestLanderStatusSaysTheRunningLandersGate(t *testing.T) {
	e := newLandEnv(t)
	state := filepath.Join(e.tmp, "lander")
	e.write(filepath.Join(state, "pid"), strconv.Itoa(os.Getpid())+"\n")
	out, rc := e.landArgs([]string{"--status"})
	if rc != 0 || !strings.Contains(out, "lander: running (pid ") || !strings.Contains(out, "\ngate: not recorded\n") {
		t.Errorf("a lander that wrote no gate file is not recorded (rc=%d):\n%s", rc, out)
	}
	e.write(filepath.Join(state, "gate"), "hosted\n")
	if out, _ := e.landArgs([]string{"--status"}); !strings.Contains(out, "\ngate: hosted\n") {
		t.Errorf("--status prints the running lander's gate:\n%s", out)
	}
	if out, _ := e.landArgs([]string{"--daemon", "--gate", "host"}); !strings.Contains(out, "a lander already runs here (pid ") || !strings.Contains(out, ", gate: hosted)") {
		t.Errorf("--daemon with one running names its gate:\n%s", out)
	}
	if err := os.Remove(filepath.Join(state, "pid")); err != nil {
		t.Fatal(err)
	}
	if out, _ := e.landArgs([]string{"--status"}); strings.Contains(out, "gate:") {
		t.Errorf("no lander runs, no gate line:\n%s", out)
	}
}

// A foreground lander that finds one running names the running one's gate and, asked for
// another, says the trains stay on it: it exits 0 and leaves the running lander's files alone.
func TestLanderForegroundSaysTheRunningLandersGate(t *testing.T) {
	e := newLandEnv(t)
	state := filepath.Join(e.tmp, "lander")
	pid := strconv.Itoa(os.Getpid()) + "\n"
	e.write(filepath.Join(state, "pid"), pid)
	e.write(filepath.Join(state, "gate"), "hosted\n")
	out, rc := e.landArgs([]string{"--gate", "host"})
	if rc != 0 || !strings.Contains(out, "land: a lander already runs here (pid "+strings.TrimSpace(pid)+", gate: hosted)") ||
		!strings.Contains(out, "asked for gate host: the trains stay on the running lander's gate (hosted)") {
		t.Errorf("--gate host against a hosted lander says so (rc=%d):\n%s", rc, out)
	}
	if got := readTmpOrEmpty(state, "pid") + readTmpOrEmpty(state, "gate"); got != pid+"hosted\n" {
		t.Errorf("the running lander's files are left alone, got %q", got)
	}
	if out, rc := e.landArgs(nil); rc != 0 || !strings.Contains(out, ", gate: hosted)") || strings.Contains(out, "trains stay") {
		t.Errorf("the same mode: the gate named, no notice (rc=%d):\n%s", rc, out)
	}
	if got := e.log(); strings.Contains(got, "repo view") || strings.Contains(got, "pr ") {
		t.Errorf("it stops before the permission read and any train:\n%s", got)
	}
}

// `make land` takes GATE only from its command line: make imports the environment, and an
// exported GATE=host would otherwise put the lander on the build host without anyone asking.
func TestMakeLandTakesGATEOnlyFromTheCommandLine(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("make is not part of the Windows leg")
	}
	mk, err := exec.LookPath("make")
	if err != nil {
		t.Skip("make not on PATH")
	}
	run := func(env []string, args ...string) string {
		t.Helper()
		cmd := exec.Command(mk, append([]string{"-n", "land"}, args...)...)
		cmd.Dir = repoRoot(t)
		for _, kv := range os.Environ() {
			if k, _, _ := strings.Cut(kv, "="); k != "GATE" && k != "MAKEFLAGS" && k != "MFLAGS" && k != "DRY" {
				cmd.Env = append(cmd.Env, kv)
			}
		}
		cmd.Env = append(cmd.Env, env...)
		b, err := cmd.Output()
		if err != nil {
			t.Fatalf("make -n land %v: %v", args, err)
		}
		return string(b)
	}
	if got := run([]string{"GATE=host"}); strings.Contains(got, "--gate") {
		t.Errorf("GATE=host in the environment must not reach land.sh:\n%s", got)
	}
	if got := run(nil, "GATE=host"); !strings.Contains(got, "--gate host") {
		t.Errorf("make land GATE=host must pass --gate host:\n%s", got)
	}
	if got := run(nil, "GATE="); strings.Contains(got, "--gate") {
		t.Errorf("an empty GATE passes no option:\n%s", got)
	}
}

// --daemon hands the detached lander its option and nothing else: without one it gates hosted
// (and logs why, when the environment asked for the build host), with --gate host on the build host.
func TestLanderDaemonPassesTheGateOption(t *testing.T) {
	e := newLandEnv(t)
	state := filepath.Join(e.tmp, "lander")
	daemon := func(args []string, extra ...string) string {
		t.Helper()
		before := readTmpOrEmpty(state, "log")
		out, rc := e.landArgs(append([]string{"--daemon"}, args...), extra...)
		if rc != 0 || !strings.Contains(out, "land: lander started (pid ") {
			t.Fatalf("--daemon (rc=%d):\n%s", rc, out)
		}
		// The queue is empty: the detached lander logs its mode and exits.
		deadline := time.Now().Add(time.Minute)
		for {
			log := strings.TrimPrefix(readTmpOrEmpty(state, "log"), before)
			_, err := os.Stat(filepath.Join(state, "pid"))
			if strings.Contains(log, "the queue is empty") && os.IsNotExist(err) {
				return out + log
			}
			if time.Now().After(deadline) {
				t.Fatalf("the detached lander did not finish:\n%s", log)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	out := daemon(nil, "SIGNOFF_GATE=host")
	if !strings.Contains(out, "(pid ") || !strings.Contains(out, ", gate: hosted), log ") || !strings.Contains(out, " gate: hosted, ci.yml, 8 legs\n") ||
		!strings.Contains(out, "the environment asks for the build host") {
		t.Errorf("no option: a hosted lander that says why:\n%s", out)
	}
	out = daemon([]string{"--gate", "host"})
	if !strings.Contains(out, ", gate: host), log ") || !strings.Contains(out, " gate: host, the build host\n") {
		t.Errorf("--gate host reaches the detached lander:\n%s", out)
	}
}

// readTmpOrEmpty is the file name in dir, or "" when it does not exist.
func readTmpOrEmpty(dir, name string) string {
	b, _ := os.ReadFile(filepath.Join(dir, name))
	return string(b)
}
