package repodocs

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// signoffEnv is a maintainer's clone: two bare remotes named like a fork
// setup (`up/proj` the upstream, `me/proj` origin), the scripts copied in, a
// fake gh and a fake make command that log what they are asked.
type signoffEnv struct {
	t                    *testing.T
	env                  []string
	work, up, origin     string
	makeLog, ghLog, runs string
	winLog               string
}

func newSignoffEnv(t *testing.T) *signoffEnv {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("bash script, run by the Unix legs")
	}
	for _, bin := range []string{"bash", "git", "jq"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not found", bin)
		}
	}
	root := repoRoot(t)
	tmp, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	e := &signoffEnv{t: t}
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GIT_") && !strings.HasPrefix(kv, "SIGNOFF_") && !strings.HasPrefix(kv, "FAKE_") {
			env = append(env, kv)
		}
	}
	bin := filepath.Join(tmp, "bin")
	e.makeLog = filepath.Join(tmp, "make.log")
	e.ghLog = filepath.Join(tmp, "gh.log")
	e.runs = filepath.Join(tmp, "runs.json")
	e.env = append(env,
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=user", "GIT_AUTHOR_EMAIL=user@example.com",
		"GIT_COMMITTER_NAME=user", "GIT_COMMITTER_EMAIL=user@example.com",
		"FAKE_MAKE_LOG="+e.makeLog, "FAKE_GH_LOG="+e.ghLog, "FAKE_GH_RUNS="+e.runs,
		"FAKE_PLATFORM=linux/amd64", "FAKE_PERM=WRITE",
		"SIGNOFF_MAKE="+filepath.Join(bin, "fakemake"),
		"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	e.write(filepath.Join(bin, "fakemake"), `#!/bin/sh
case "$*" in *signoff-platform*) echo "make: Entering directory"; echo "$FAKE_PLATFORM"; exit 0 ;; esac
echo "$*" >> "$FAKE_MAKE_LOG"
case "$*" in test-rerun*) exit "${FAKE_RERUN_RC:-0}" ;; esac
[ -z "${FAKE_MAKE_OUT:-}" ] || printf '%b' "$FAKE_MAKE_OUT"
exit "${FAKE_MAKE_RC:-0}"
`, 0o755)
	// --jq is applied as gh applies it; the hosted fallback's calls answer from
	// FAKE_GH_DISPATCHED (the dispatched run) and FAKE_GH_JOBS (its jobs).
	e.write(filepath.Join(bin, "gh"), `#!/bin/bash
echo "$*" >> "$FAKE_GH_LOG"
jq_expr=; prev=
for a in "$@"; do [ "$prev" = --jq ] && jq_expr=$a; prev=$a; done
out() { if [ -n "$jq_expr" ]; then jq -r "$jq_expr"; else cat; fi; }
case "$1 $2" in
  "repo view") echo "$FAKE_PERM" ;;
  "run list")
    case "$*" in
      *workflow_dispatch*) echo "${FAKE_GH_DISPATCHED:-[]}" | out ;;
      *) if [ -f "$FAKE_GH_RUNS" ]; then cat "$FAKE_GH_RUNS"; else echo '[]'; fi | out ;;
    esac ;;
  "run view") printf '%s\n' "${FAKE_GH_JOBS:-}" | out ;;
  "workflow run") exit "${FAKE_GH_DISPATCH_RC:-0}" ;;
  "pr view") exit 1 ;;
esac
exit 0
`, 0o755)
	e.winLog = filepath.Join(tmp, "win.log")
	e.write(filepath.Join(bin, "fakewin"), `#!/bin/sh
echo "$*" >> "$FAKE_WIN_LOG"
case "$*" in "go test -count=1 "*) exit "${FAKE_WIN_RERUN_RC:-0}" ;; esac
[ -z "${FAKE_WIN_OUT:-}" ] || printf '%b' "$FAKE_WIN_OUT"
exit "${FAKE_WIN_RC:-0}"
`, 0o755)
	e.env = append(e.env, "FAKE_WIN_LOG="+e.winLog, "SIGNOFF_POLL=0")

	e.up = filepath.Join(tmp, "up", "proj.git")
	e.origin = filepath.Join(tmp, "me", "proj.git")
	e.work = filepath.Join(tmp, "work")
	e.run(tmp, "git", "init", "-q", "--bare", "-b", "main", e.up)
	e.run(tmp, "git", "init", "-q", "--bare", "-b", "main", e.origin)
	e.run(tmp, "git", "init", "-q", "-b", "main", e.work)
	e.run(e.work, "git", "remote", "add", "upstream", e.up)
	e.run(e.work, "git", "remote", "add", "origin", e.origin)
	for _, s := range []string{"signoff.sh", "upstream-repo.sh", "ci-filters.sh", "affected-pkgs.sh", "test-failures.sh"} {
		e.write(filepath.Join(e.work, "scripts", s), readFile(t, root, "scripts/"+s), 0o755)
	}
	e.write(filepath.Join(e.work, "README"), "x\n", 0o644)
	e.run(e.work, "git", "add", ".")
	e.run(e.work, "git", "commit", "-q", "-m", "init")
	e.run(e.work, "git", "push", "-q", "upstream", "main")
	return e
}

func (e *signoffEnv) run(dir, name string, args ...string) string {
	e.t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir, cmd.Env = dir, e.env
	out, err := cmd.CombinedOutput()
	if err != nil {
		e.t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (e *signoffEnv) write(path, body string, mode os.FileMode) {
	e.t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		e.t.Fatal(err)
	}
}

// branch creates a feature branch in dir's repository with one commit adding file.
func (e *signoffEnv) branch(dir, name, file string) {
	e.t.Helper()
	e.run(dir, "git", "switch", "-q", "-c", name)
	e.write(filepath.Join(dir, file), name+"\n", 0o644)
	e.run(dir, "git", "add", ".")
	e.run(dir, "git", "commit", "-q", "-m", name)
}

// signoff runs make signoff's script in dir with extra environment.
func (e *signoffEnv) signoff(dir string, extra ...string) (string, bool) {
	e.t.Helper()
	cmd := exec.Command("bash", filepath.Join(dir, "scripts", "signoff.sh"))
	cmd.Dir, cmd.Env = dir, append(append([]string{}, e.env...), extra...)
	out, err := cmd.CombinedOutput()
	return string(out), err == nil
}

func (e *signoffEnv) log(path string) string {
	b, _ := os.ReadFile(path)
	return string(b)
}

func (e *signoffEnv) posts() []string {
	var p []string
	for _, l := range strings.Split(e.log(e.ghLog), "\n") {
		if strings.Contains(l, "statuses/") {
			p = append(p, l)
		}
	}
	return p
}

func (e *signoffEnv) remoteTip(remote, branch string) string {
	out := e.run(e.work, "git", "ls-remote", "--heads", remote, "refs/heads/"+branch)
	if out == "" {
		return ""
	}
	return strings.Fields(out)[0]
}

// Nothing may run, be pushed or be posted when signoff refuses.
func (e *signoffEnv) assertUntouched(branch string, out string) {
	e.t.Helper()
	if e.log(e.makeLog) != "" {
		e.t.Errorf("a refusal must run no target:\n%s\n%s", e.log(e.makeLog), out)
	}
	if e.remoteTip("origin", branch) != "" {
		e.t.Errorf("a refusal must push nothing\n%s", out)
	}
	if len(e.posts()) != 0 {
		e.t.Errorf("a refusal must post nothing: %v", e.posts())
	}
}

func TestSignoffRefusesWithoutRunningAnything(t *testing.T) {
	cases := []struct {
		name  string
		setup func(e *signoffEnv)
	}{
		{"modified tracked file", func(e *signoffEnv) { e.write(filepath.Join(e.work, "README"), "changed\n", 0o644) }},
		{"staged file", func(e *signoffEnv) {
			e.write(filepath.Join(e.work, "staged.txt"), "s\n", 0o644)
			e.run(e.work, "git", "add", "staged.txt")
		}},
		{"untracked file", func(e *signoffEnv) { e.write(filepath.Join(e.work, "new.txt"), "n\n", 0o644) }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newSignoffEnv(t)
			e.branch(e.work, "feat/x", "x.txt")
			c.setup(e)
			out, ok := e.signoff(e.work)
			if ok || !strings.Contains(out, "not clean") {
				t.Fatalf("a dirty tree must refuse: ok=%v\n%s", ok, out)
			}
			e.assertUntouched("feat/x", out)
		})
	}

	t.Run("main", func(t *testing.T) {
		e := newSignoffEnv(t)
		out, ok := e.signoff(e.work)
		if ok {
			t.Fatalf("main must refuse\n%s", out)
		}
		e.assertUntouched("main", out)
	})
	t.Run("detached HEAD", func(t *testing.T) {
		e := newSignoffEnv(t)
		e.run(e.work, "git", "checkout", "-q", "--detach")
		out, ok := e.signoff(e.work)
		if ok || !strings.Contains(out, "detached") {
			t.Fatalf("a detached HEAD must refuse: ok=%v\n%s", ok, out)
		}
		e.assertUntouched("main", out)
	})
	for _, perm := range []string{"READ", "TRIAGE", "UNKNOWN"} {
		t.Run("permission "+perm, func(t *testing.T) {
			e := newSignoffEnv(t)
			e.branch(e.work, "feat/x", "x.txt")
			out, ok := e.signoff(e.work, "FAKE_PERM="+perm)
			if ok || !strings.Contains(out, "a contributor just pushes the branch and opens the PR, and CI tests it") {
				t.Fatalf("permission %s must refuse as a contributor: ok=%v\n%s", perm, ok, out)
			}
			e.assertUntouched("feat/x", out)
		})
	}
}

func TestSignoffFailingTargetPushesAndPostsNothing(t *testing.T) {
	e := newSignoffEnv(t)
	e.branch(e.work, "feat/x", "x.txt")
	out, ok := e.signoff(e.work, "FAKE_MAKE_RC=3")
	if ok {
		t.Fatalf("a failing target must fail signoff\n%s", out)
	}
	if e.remoteTip("origin", "feat/x") != "" || len(e.posts()) != 0 {
		t.Errorf("nothing may be pushed or posted\n%s", out)
	}
}

// A broken e2e scenario makes `make` exit 2 (#1743): signoff must say "failed (exit 2):
// nothing pushed, nothing posted", the wording scripts/land.sh's gate() reads as a red
// test (exit 1: re-run, then bisect), not as infrastructure (exit 2: the lander stops).
func TestSignoffRedE2EIsATestFailureForTheLander(t *testing.T) {
	e := newSignoffEnv(t)
	e.branch(e.work, "feat/x", "x.txt")
	out, ok := e.signoff(e.work, "FAKE_MAKE_RC=2")
	if ok {
		t.Fatalf("a failing e2e must fail signoff\n%s", out)
	}
	if !strings.Contains(out, "gate smoke-http smoke e2e failed (exit 2): nothing pushed, nothing posted") {
		t.Errorf("the failure line must name the targets and keep the wording land.sh classifies as red\n%s", out)
	}
	if got := strings.TrimSpace(e.log(e.makeLog)); got != "-k gate smoke-http smoke e2e" {
		t.Errorf("the e2e runs in the signoff's make invocation, got %q", got)
	}
}

// A red run whose only failures are named Go tests runs their packages once more, uncached
// (D-new-targeted-retry): green, the signoff goes on and names them as flaky; red, it
// says the failure is confirmed, the line scripts/land.sh reads to skip its own re-run.
func TestSignoffRetriesNamedGoTestsOnce(t *testing.T) {
	red := "go test -timeout 20m ./...\\n--- FAIL: TestA (0.01s)\\nFAIL\\tgithub.com/x/p/internal/kb\\t0.2s\\n" +
		"go test -race -count=1 -run Race ./internal/mcpserver\\n--- FAIL: TestRaceB (0.1s)\\nFAIL\\tgithub.com/x/p/internal/mcpserver\\t1.1s\\n" +
		"make: *** [Makefile:23: test] Error 1\\nmake: *** [Makefile:33: race] Error 1\\n"
	t.Run("flaky", func(t *testing.T) {
		e := newSignoffEnv(t)
		e.branch(e.work, "feat/x", "x.txt")
		out, ok := e.signoff(e.work, "FAKE_MAKE_RC=2", "FAKE_MAKE_OUT="+red)
		if !ok {
			t.Fatalf("a green retry must let the signoff go on\n%s", out)
		}
		want := "-k gate smoke-http smoke e2e\n" +
			"test-rerun RERUN_PKGS=github.com/x/p/internal/kb\n" +
			"test-rerun RERUN_PKGS=github.com/x/p/internal/mcpserver RERUN_RACE=1"
		if got := strings.TrimSpace(e.log(e.makeLog)); got != want {
			t.Errorf("the retry runs the named tests' packages, the race one under -race:\ngot  %q\nwant %q", got, want)
		}
		if !strings.Contains(out, "signoff: flaky: kb.TestA mcpserver.TestRaceB failed and passed on a targeted retry") {
			t.Errorf("the flaky tests are named on the line the lander reads\n%s", out)
		}
		if p := e.posts(); len(p) != 1 || !strings.Contains(p[0], " - 2 flaky retried") {
			t.Errorf("the status says tests were retried: %v", p)
		}
	})
	t.Run("confirmed", func(t *testing.T) {
		e := newSignoffEnv(t)
		e.branch(e.work, "feat/x", "x.txt")
		out, ok := e.signoff(e.work, "FAKE_MAKE_RC=2", "FAKE_MAKE_OUT="+red, "FAKE_RERUN_RC=1")
		if ok {
			t.Fatalf("a red retry must fail the signoff\n%s", out)
		}
		for _, want := range []string{"signoff: confirmed on a targeted retry: kb.TestA mcpserver.TestRaceB",
			"gate smoke-http smoke e2e failed (exit 2): nothing pushed, nothing posted"} {
			if !strings.Contains(out, want) {
				t.Errorf("the output lacks %q\n%s", want, out)
			}
		}
		if e.remoteTip("origin", "feat/x") != "" || len(e.posts()) != 0 {
			t.Errorf("nothing may be pushed or posted\n%s", out)
		}
	})
}

// A failure that is not a named Go test gets no targeted retry: a broken e2e
// scenario, a build failure, a timeout. Nor does a red run make did not report itself
// (exit 2): the wrapper's own failure may cut the log short. The lander's whole re-run
// decides those.
func TestSignoffDoesNotRetryWhatItCannotName(t *testing.T) {
	named := "--- FAIL: TestA (0.01s)\\nFAIL\\tgithub.com/x/p/a\\t0.2s\\nmake: *** [Makefile:23: test] Error 1\\n"
	for name, c := range map[string]struct{ rc, log string }{
		"e2e":     {"2", named + "make: *** [Makefile:160: e2e] Error 1\\n"},
		"build":   {"2", "FAIL\\tgithub.com/x/p/a [build failed]\\nmake: *** [Makefile:23: test] Error 1\\n"},
		"web":     {"2", "make[1]: *** [Makefile:83: web-test] Error 1\\nmake: *** [Makefile:271: web-gate] Error 2\\n"},
		"timeout": {"2", "--- FAIL: TestA (600s)\\npanic: test timed out after 20m0s\\nFAIL\\tgithub.com/x/p/a\\t600.1s\\nmake: *** [Makefile:23: test] Error 1\\n"},
		"wrapper": {"255", named},
	} {
		t.Run(name, func(t *testing.T) {
			e := newSignoffEnv(t)
			e.branch(e.work, "feat/x", "x.txt")
			out, ok := e.signoff(e.work, "FAKE_MAKE_RC="+c.rc, "FAKE_MAKE_OUT="+c.log)
			if ok || strings.Contains(e.log(e.makeLog), "test-rerun") || strings.Contains(out, "targeted retry") {
				t.Errorf("no targeted retry, and the signoff fails: ok=%v\n%s\n%s", ok, e.log(e.makeLog), out)
			}
		})
	}
}

func TestSignoffPushesAndAttestsTheSha(t *testing.T) {
	e := newSignoffEnv(t)
	e.branch(e.work, "feat/x", "notes/a.txt")
	out, ok := e.signoff(e.work)
	if !ok {
		t.Fatalf("signoff failed\n%s", out)
	}
	head := e.run(e.work, "git", "rev-parse", "HEAD")
	if e.remoteTip("origin", "feat/x") != head {
		t.Errorf("the branch must be on origin at HEAD\n%s", out)
	}
	posts := e.posts()
	if len(posts) != 1 {
		t.Fatalf("want exactly one POST, got %v", posts)
	}
	p := posts[0]
	for _, want := range []string{"POST", "repos/up/proj/statuses/" + head, "state=success", "context=signoff", "make gate smoke-http smoke e2e - linux/amd64 - "} {
		if !strings.Contains(p, want) {
			t.Errorf("the POST lacks %q: %s", want, p)
		}
	}
	desc := p[strings.Index(p, "description=")+len("description="):]
	if len(desc) > 140 {
		t.Errorf("description is %d characters, the API limit is 140", len(desc))
	}
	host, _ := os.Hostname()
	user := os.Getenv("USER")
	for _, private := range []string{host, user} {
		if private != "" && strings.Contains(desc, private) {
			t.Errorf("the description %q carries %q: the repository is public", desc, private)
		}
	}
	if got := strings.TrimSpace(e.log(e.makeLog)); got != "-k gate smoke-http smoke e2e" {
		t.Errorf("a diff without web paths runs gate, smoke-http, smoke and e2e (test-install stays post-merge, #1743), got %q", got)
	}
}

// The web decision has one source, ci-filters.sh: a web path adds web-gate.
func TestSignoffAddsWebGateWhenTheDiffNeedsIt(t *testing.T) {
	e := newSignoffEnv(t)
	e.branch(e.work, "feat/x", "web/src/a.ts")
	if out, ok := e.signoff(e.work); !ok {
		t.Fatalf("signoff failed\n%s", out)
	}
	if got := strings.TrimSpace(e.log(e.makeLog)); got != "-k gate smoke-http smoke e2e web-gate" {
		t.Errorf("a web/src change adds web-gate to the default targets, got %q", got)
	}
	if p := e.posts(); len(p) != 1 || !strings.Contains(p[0], "make gate smoke-http smoke e2e web-gate - ") {
		t.Errorf("the status names both targets: %v", p)
	}
}

// make worktree-add branches from upstream/main, so the branch tracks it: a
// bare `git push` would target main of the upstream repository.
func TestSignoffFromAWorktreeTrackingUpstreamPushesToOrigin(t *testing.T) {
	e := newSignoffEnv(t)
	wt := filepath.Join(e.work, ".worktrees", "x")
	e.run(e.work, "git", "worktree", "add", "-q", "-b", "feat/x", wt, "upstream/main")
	e.run(wt, "git", "config", "push.default", "upstream")
	e.write(filepath.Join(wt, "x.txt"), "x\n", 0o644)
	e.run(wt, "git", "add", ".")
	e.run(wt, "git", "commit", "-q", "-m", "x")
	upMain := e.remoteTip("upstream", "main")
	if out, ok := e.signoff(wt); !ok {
		t.Fatalf("signoff failed\n%s", out)
	}
	head := e.run(wt, "git", "rev-parse", "HEAD")
	if e.remoteTip("origin", "feat/x") != head {
		t.Error("the branch must be pushed to origin/feat/x")
	}
	if e.remoteTip("upstream", "main") != upMain || e.remoteTip("upstream", "feat/x") != "" {
		t.Error("the upstream repository must be untouched")
	}
	if got := e.run(wt, "git", "rev-parse", "--abbrev-ref", "feat/x@{upstream}"); got != "origin/feat/x" {
		t.Errorf("the upstream of the branch must be fixed, got %s", got)
	}
}

func TestSignoffRerunsOnlyFailedCIRuns(t *testing.T) {
	e := newSignoffEnv(t)
	e.branch(e.work, "feat/x", "x.txt")
	e.write(e.runs, `[
 {"databaseId": 11, "status": "completed", "conclusion": "failure", "event": "pull_request"},
 {"databaseId": 12, "status": "completed", "conclusion": "success", "event": "pull_request"},
 {"databaseId": 13, "status": "in_progress", "conclusion": "", "event": "pull_request"}
]`, 0o644)
	if out, ok := e.signoff(e.work); !ok {
		t.Fatalf("signoff failed\n%s", out)
	}
	log := e.log(e.ghLog)
	if !strings.Contains(log, "run rerun -R up/proj 11 --failed") {
		t.Errorf("the failed run must be re-run:\n%s", log)
	}
	for _, id := range []string{" 12 ", " 13 "} {
		if strings.Contains(log, "run rerun") && strings.Contains(log, id+"--failed") {
			t.Errorf("run%sneed not be re-run:\n%s", id, log)
		}
	}
}

// goModule makes the work tree a Go module with two packages, b importing a, so
// affected-pkgs.sh has something to select.
func (e *signoffEnv) goModule() {
	e.t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		e.t.Skip("go not found")
	}
	e.write(filepath.Join(e.work, "go.mod"), "module example.com/proj\n\ngo 1.21\n", 0o644)
	e.write(filepath.Join(e.work, "internal", "a", "a.go"), "package a\n\nfunc A() int { return 1 }\n", 0o644)
	e.write(filepath.Join(e.work, "internal", "b", "b.go"), "package b\n\nimport \"example.com/proj/internal/a\"\n\nfunc B() int { return a.A() }\n", 0o644)
	e.write(filepath.Join(e.work, "internal", "c", "c.go"), "package c\n", 0o644)
	e.run(e.work, "git", "add", ".")
	e.run(e.work, "git", "commit", "-q", "-m", "module")
	e.run(e.work, "git", "push", "-q", "upstream", "main")
}

// SIGNOFF_WINDOWS gets the sharded run of the affected packages: the changed one and
// its reverse dependencies and the tree-scanning procattr guard, not the unrelated one;
// the status names the leg.
func TestSignoffRunsTheWindowsLegOnTheAffectedPackages(t *testing.T) {
	e := newSignoffEnv(t)
	e.goModule()
	e.branch(e.work, "feat/x", "internal/a/a2.go")
	e.write(filepath.Join(e.work, "internal", "a", "a2.go"), "package a\n", 0o644)
	e.run(e.work, "git", "commit", "-q", "-am", "a2")
	out, ok := e.signoff(e.work, "SIGNOFF_WINDOWS="+filepath.Join(filepath.Dir(e.winLog), "bin", "fakewin"))
	if !ok {
		t.Fatalf("signoff failed\n%s", out)
	}
	got := strings.TrimSpace(e.log(e.winLog))
	if got != "bash scripts/test-shard.sh 4 all ./internal/a ./internal/b ./internal/procattr" {
		t.Errorf("the Windows leg must test the affected packages, got %q", got)
	}
	if p := e.posts(); len(p) != 1 || !strings.Contains(p[0], " - windows ") || strings.Contains(p[0], "not run") {
		t.Errorf("the status names the Windows leg: %v", p)
	}
}

// A branch that changes only documents has no Windows leg: what a reader of the
// repository checks is not platform behaviour, and the Linux gate runs it.
func TestSignoffSkipsTheWindowsLegForDocuments(t *testing.T) {
	e := newSignoffEnv(t)
	e.goModule()
	e.branch(e.work, "feat/x", "docs/"+"x.md") // split: a whole path would be read as a citation
	out, ok := e.signoff(e.work, "SIGNOFF_WINDOWS="+filepath.Join(filepath.Dir(e.winLog), "bin", "fakewin"))
	if !ok {
		t.Fatalf("signoff failed\n%s", out)
	}
	if got := strings.TrimSpace(e.log(e.winLog)); got != "" {
		t.Errorf("a documents-only branch must not run the Windows leg, got %q", got)
	}
}

func TestSignoffWindowsLegFailurePushesAndPostsNothing(t *testing.T) {
	e := newSignoffEnv(t)
	e.goModule()
	e.branch(e.work, "feat/x", "internal/a/a2.go")
	e.write(filepath.Join(e.work, "internal", "a", "a2.go"), "package a\n", 0o644)
	e.run(e.work, "git", "commit", "-q", "-am", "a2")
	fakewin := filepath.Join(filepath.Dir(e.winLog), "bin", "fakewin")
	out, ok := e.signoff(e.work, "SIGNOFF_WINDOWS="+fakewin, "FAKE_WIN_RC=1")
	if ok || !strings.Contains(out, "the Windows leg failed") {
		t.Fatalf("a red Windows leg must fail signoff: ok=%v\n%s", ok, out)
	}
	if e.remoteTip("origin", "feat/x") != "" || len(e.posts()) != 0 {
		t.Errorf("nothing may be pushed or posted\n%s", out)
	}
	// An unavailable Windows machine (exit 75) is reported, not a failure.
	out, ok = e.signoff(e.work, "SIGNOFF_WINDOWS="+fakewin, "FAKE_WIN_RC=75")
	if !ok {
		t.Fatalf("an unavailable Windows machine must not fail signoff\n%s", out)
	}
	if p := e.posts(); len(p) != 1 || !strings.Contains(p[0], "windows unavailable") {
		t.Errorf("the status says the leg was skipped: %v", p)
	}
}

// A red Windows leg whose failures are named Go tests runs their packages once more on Windows.
func TestSignoffRetriesNamedWindowsTestsOnce(t *testing.T) {
	e := newSignoffEnv(t)
	e.goModule()
	e.branch(e.work, "feat/x", "internal/a/a2.go")
	e.write(filepath.Join(e.work, "internal", "a", "a2.go"), "package a\n", 0o644)
	e.run(e.work, "git", "commit", "-q", "-am", "a2")
	fakewin := filepath.Join(filepath.Dir(e.winLog), "bin", "fakewin")
	red := "--- FAIL: TestW (1.0s)\\r\\nFAIL\\tgithub.com/x/p/internal/a\\t2.0s\\r\\n"
	out, ok := e.signoff(e.work, "SIGNOFF_WINDOWS="+fakewin, "FAKE_WIN_RC=1", "FAKE_WIN_OUT="+red)
	if !ok {
		t.Fatalf("a green Windows retry must let the signoff go on\n%s", out)
	}
	if got := e.log(e.winLog); !strings.Contains(got, "go test -count=1 -timeout 20m github.com/x/p/internal/a") {
		t.Errorf("the retry runs the named test's package on Windows, got %q", got)
	}
	if p := e.posts(); len(p) != 1 || !strings.Contains(p[0], " - 1 flaky retried") {
		t.Errorf("the status says a test was retried: %v", p)
	}
	_, ok = e.signoff(e.work, "SIGNOFF_WINDOWS="+fakewin, "FAKE_WIN_RC=1", "FAKE_WIN_OUT="+red, "FAKE_WIN_RERUN_RC=1")
	if ok {
		t.Errorf("a red Windows retry must fail the signoff")
	}
}

// Without SIGNOFF_WINDOWS the status says so, and no package list is computed.
func TestSignoffWithoutAWindowsCommandSaysSo(t *testing.T) {
	e := newSignoffEnv(t)
	e.branch(e.work, "feat/x", "x.txt")
	if out, ok := e.signoff(e.work); !ok {
		t.Fatalf("signoff failed\n%s", out)
	}
	if p := e.posts(); len(p) != 1 || !strings.Contains(p[0], "windows not run") {
		t.Errorf("the status says the Windows leg did not run: %v", p)
	}
}

// Exit 75 from SIGNOFF_MAKE (the probe or the targets) means the machine is
// unavailable: the branch is pushed, ci.yml is dispatched on it, and the status is
// posted only when that run's gate job is green.
func TestSignoffFallsBackToCIWhenTheHostIsUnavailable(t *testing.T) {
	green := `{"jobs":[{"name":"test","status":"completed","conclusion":"success"},{"name":"gate","status":"completed","conclusion":"success"}]}`
	red := `{"jobs":[{"name":"gate","status":"completed","conclusion":"failure"}]}`
	for _, c := range []struct {
		name, jobs string
		ok         bool
	}{{"green gate", green, true}, {"red gate", red, false}} {
		t.Run(c.name, func(t *testing.T) {
			e := newSignoffEnv(t)
			e.branch(e.work, "feat/x", "x.txt")
			head := e.run(e.work, "git", "rev-parse", "HEAD")
			out, ok := e.signoff(e.work, "FAKE_MAKE_RC=75", `FAKE_GH_DISPATCHED=[{"databaseId":7}]`, "FAKE_GH_JOBS="+c.jobs)
			if ok != c.ok {
				t.Fatalf("ok=%v, want %v\n%s", ok, c.ok, out)
			}
			if !strings.Contains(e.log(e.ghLog), "workflow run ci.yml -R up/proj --ref feat/x") {
				t.Errorf("ci.yml must be dispatched on the branch:\n%s", e.log(e.ghLog))
			}
			if e.remoteTip("origin", "feat/x") != head {
				t.Errorf("the branch must be pushed before the dispatch\n%s", out)
			}
			posts := e.posts()
			if c.ok && (len(posts) != 1 || !strings.Contains(posts[0], "github-actions/ci.yml run 7")) {
				t.Errorf("the status names the CI run: %v", posts)
			}
			if !c.ok && len(posts) != 0 {
				t.Errorf("a red gate job posts nothing: %v", posts)
			}
		})
	}
}
