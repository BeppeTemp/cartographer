package repodocs

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// stubBin writes executables named after the keys of scripts into a fresh directory.
func stubBin(t *testing.T, scripts map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range scripts {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// runHostedCheck runs scripts/hosted-check.sh with PATH reduced to path and returns the exit code.
// The Windows list is the empty one unless env sets HOSTED_CHECK_TEST_WIN_PKGS (the script's test seam):
// the toy repositories here have no upstream/main for the script to compute it from
// (TestHostedCheckComputesWindowsPackages does).
func runHostedCheck(t *testing.T, dir, path string, env []string, args ...string) (int, string) {
	t.Helper()
	return runHostedCheckScript(t, filepath.Join(repoRoot(t), "scripts/hosted-check.sh"), dir, path, append([]string{"HOSTED_CHECK_TEST_WIN_PKGS="}, env...), args...)
}

// runHostedCheckScript runs the script at the given path (absolute or relative to dir), env as is.
func runHostedCheckScript(t *testing.T, script, dir, path string, env []string, args ...string) (int, string) {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not on PATH")
	}
	cmd := exec.Command(bash, append([]string{script}, args...)...)
	cmd.Dir = dir
	cmd.Env = append([]string{"PATH=" + path, "HOME=" + t.TempDir()}, env...)
	out, err := cmd.CombinedOutput()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode(), string(out)
	}
	if err != nil {
		t.Fatal(err)
	}
	return 0, string(out)
}

// The arguments are validated before any git or gh call: the stubs fail the test if reached.
func TestHostedCheckRejects(t *testing.T) {
	needBash(t)
	stubs := stubBin(t, map[string]string{"git": "exit 99", "gh": "exit 99"})
	cases := [][]string{
		nil,
		{"build"},
		{"check-run", "CHECK_PKGS=./a;rm"},
		{"check-run", "CHECK_PKGS=$(x)"},
		{"check-run", "CHECK_PKGS=./a`x`"},
		{"check-run", "CHECK_FOO=1"},
		{"check-run", "CHECK_PKGS=./a", "CHECK_WIN_PKGS=./a"}, // the script computes it: no argument carries it
		{"check-run", "CHECK_PKGS=./a\nrm -rf x"},
		{"check-run", "CHECK_PKGS=./a\r./b"},
		{"check-run", "CHECK_PKGS=-exec=/bin/x"},
		{"check-run", "CHECK_PKGS=./a -exec=/bin/x"},
	}
	for _, args := range cases {
		code, out := runHostedCheck(t, repoRoot(t), stubs, nil, args...)
		if code != 2 {
			t.Errorf("%q: exit %d, want 2\n%s", args, code, out)
		}
	}
}

// GitHub that cannot run the check is exit 75, never a red check (D1704).
func TestHostedCheckUnavailableIs75(t *testing.T) {
	needBash(t)
	stubs := stubBin(t, map[string]string{"git": "exit 99", "gh": "[ \"$1 $2\" = 'auth status' ] && exit 1; exit 99"})
	code, out := runHostedCheck(t, repoRoot(t), stubs, nil, "check-run", "CHECK_PKGS=./internal/kb ./cmd/x", "CHECK_E2E=1")
	if code != 75 {
		t.Errorf("exit %d, want 75\n%s", code, out)
	}
	// No gh at all.
	code, out = runHostedCheck(t, repoRoot(t), stubBin(t, map[string]string{"git": "exit 99"}), nil, "check-run", "CHECK_PKGS=")
	if code != 75 {
		t.Errorf("no gh: exit %d, want 75\n%s", code, out)
	}
}

// The dry run builds the temporary commit of the worktree state without touching the
// repository's index or HEAD, and names the branch after the state.
func TestHostedCheckDryRunBuildsWorktreeCommit(t *testing.T) {
	needBash(t)
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not on PATH")
	}
	repo := t.TempDir()
	run := func(args ...string) string {
		c := exec.Command(git, args...)
		c.Dir = repo
		c.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
			"GIT_AUTHOR_NAME=u", "GIT_AUTHOR_EMAIL=u@example.com", "GIT_COMMITTER_NAME=u", "GIT_COMMITTER_EMAIL=u@example.com")
		out, err := c.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("init", "-q", "-b", "Feat/My_Change")
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "a.txt")
	run("commit", "-q", "-m", "init")
	head := run("rev-parse", "HEAD")
	env := []string{"HOSTED_CHECK_DRY=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null"}
	path := filepath.Dir(git) + string(os.PathListSeparator) + "/usr/bin" + string(os.PathListSeparator) + "/bin"
	re := regexp.MustCompile(`would push ([0-9a-f]{40}) to origin (check/feat-my-change-[0-9a-f]{6}-[0-9a-f]{12})`)

	// Clean tree: HEAD itself is pushed.
	code, out := runHostedCheck(t, repo, path, env, "check-run", "CHECK_PKGS=./a")
	m := re.FindStringSubmatch(out)
	if code != 0 || m == nil || m[1] != head || !strings.HasSuffix(m[2], head[:12]) {
		t.Fatalf("clean tree: exit %d, out %q", code, out)
	}
	// An untracked file is never pushed (the branch is public): same commit, and it is listed.
	if err := os.WriteFile(filepath.Join(repo, "new.txt"), []byte("n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out = runHostedCheck(t, repo, path, env, "check-run", "CHECK_PKGS=./a")
	m = re.FindStringSubmatch(out)
	if code != 0 || m == nil || m[1] != head || !strings.Contains(out, "new.txt") || !strings.Contains(out, "NOT in the checked state") {
		t.Fatalf("untracked: exit %d, out %q", code, out)
	}
	// A tracked edit: a temporary commit on top of HEAD, index and HEAD untouched.
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out = runHostedCheck(t, repo, path, env, "check-run", "CHECK_PKGS=./a")
	m = re.FindStringSubmatch(out)
	if code != 0 || m == nil || m[1] == head {
		t.Fatalf("dirty tree: exit %d, out %q", code, out)
	}
	if got := run("rev-list", "--parents", "-n1", m[1]); got != m[1]+" "+head {
		t.Errorf("temporary commit parents = %q", got)
	}
	if got := run("show", m[1]+":a.txt"); got != "changed" {
		t.Errorf("a.txt in the commit = %q", got)
	}
	if run("ls-tree", "--name-only", m[1]) != "a.txt" {
		t.Errorf("the commit holds more than the tracked file")
	}
	if got := run("rev-parse", "HEAD"); got != head {
		t.Errorf("HEAD moved to %s", got)
	}
	if got := run("status", "--porcelain"); got != "M a.txt\n?? new.txt" && got != " M a.txt\n?? new.txt" {
		t.Errorf("status = %q: the index was touched", got)
	}
}

// ghStub answers the gh calls of steps 2-9. The run list before the dispatch holds a finished
// run 100 whose verdict is poisoned (success); after it, the first poll still shows only 100 and
// later ones show the new run 101, whose conclusion is $GH_CONCL and whose jobs are $GH_JOBS
// ("check=success,windows=failure": the legs, read in the same call as the verdict). The failed log is
// $GH_LOG (printf %b: tabs, newlines) or one plain line. The dispatch arguments are kept in $S/dispatch.
const ghStub = `S="$GH_STATE"
case "$1 $2" in
"auth status") exit 0 ;;
"api "*) echo; exit 0 ;;
"workflow run") [ -z "$GH_DISPATCH_FAIL" ] || exit 1; echo "$*" > "$S/dispatch"; : > "$S/dispatched"; exit 0 ;;
"run list")
  if [ ! -e "$S/dispatched" ]; then [ -z "$GH_LIST_FAIL" ] || exit 1; echo 100; exit 0; fi
  n=$(cat "$S/polls" 2>/dev/null || echo 0); echo $((n + 1)) > "$S/polls"
  if [ "$n" -ge 1 ]; then echo 101; fi
  echo 100; exit 0 ;;
"run view")
  case "$*" in
  *--log-failed*) if [ -n "$GH_LOG" ]; then printf '%b\n' "$GH_LOG"; else echo "FAILED-LOG-LINE"; fi; exit 0 ;;
  *jobs*) ;;
  *) exit 99 ;; # the verdict and the legs come from one poll
  esac
  if [ "$3" = 100 ]; then echo "completed success "; else echo "completed $GH_CONCL $GH_JOBS"; fi
  exit 0 ;;
"run cancel") echo "$3" > "$S/cancelled"; exit 0 ;;
esac
exit 99`

// Steps 5-9 end to end against a local bare remote and a stubbed gh: the verdict-to-exit mapping,
// a stale finished run of the same state never read as the verdict, and the branch deleted on
// every path (including a refused dispatch).
func TestHostedCheckRun(t *testing.T) {
	needBash(t)
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not on PATH")
	}
	gitEnv := []string{"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=u", "GIT_AUTHOR_EMAIL=u@example.com", "GIT_COMMITTER_NAME=u", "GIT_COMMITTER_EMAIL=u@example.com"}
	run := func(dir string, args ...string) string {
		c := exec.Command(git, args...)
		c.Dir = dir
		c.Env = append(os.Environ(), gitEnv...)
		out, err := c.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	cases := []struct {
		name, concl string
		dispatchBad bool
		listBad     bool
		code        int
		want        string
	}{
		{"green", "success", false, false, 0, "check: OK (hosted-check run 101)"},
		{"red prints the log", "failure", false, false, 1, "FAILED-LOG-LINE"},
		{"timed out is red", "timed_out", false, false, 1, "FAILED-LOG-LINE"},
		{"cancelled is 1 and says so", "cancelled", false, false, 1, "was cancelled"},
		{"a refused dispatch is 75", "", true, false, 75, "cannot dispatch"},
		{"a failed run list before the dispatch is 75", "", false, true, 75, "cannot list"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			bare, repo, state := t.TempDir(), t.TempDir(), t.TempDir()
			run(bare, "init", "-q", "--bare")
			run(repo, "init", "-q", "-b", "main")
			if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("a"), 0o644); err != nil {
				t.Fatal(err)
			}
			run(repo, "add", "a.txt")
			run(repo, "commit", "-q", "-m", "init")
			run(repo, "remote", "add", "origin", "https://example.com/owner/name.git")
			run(repo, "config", "url."+bare+".pushInsteadOf", "https://example.com/owner/name.git")
			run(repo, "config", "url."+bare+".insteadOf", "https://example.com/owner/name.git")
			stubs := stubBin(t, map[string]string{"gh": ghStub})
			path := stubs + string(os.PathListSeparator) + filepath.Dir(git) + string(os.PathListSeparator) + "/usr/bin" + string(os.PathListSeparator) + "/bin"
			env := append([]string{"GH_STATE=" + state, "GH_CONCL=" + c.concl, "HOSTED_CHECK_POLL=0", "HOSTED_CHECK_DISPATCH_POLL=0"}, gitEnv...)
			if c.listBad {
				env = append(env, "GH_LIST_FAIL=1")
			}
			if c.dispatchBad {
				env = append(env, "GH_DISPATCH_FAIL=1")
			}
			code, out := runHostedCheck(t, repo, path, env, "check-run", "CHECK_PKGS=./a")
			if code != c.code || !strings.Contains(out, c.want) {
				t.Fatalf("exit %d, want %d with %q:\n%s", code, c.code, c.want, out)
			}
			if heads := run(bare, "for-each-ref", "refs/heads/check/"); heads != "" {
				t.Errorf("the check branch was left on the remote:\n%s", heads)
			}
			if c.listBad {
				if _, err := os.Stat(filepath.Join(state, "dispatched")); err == nil {
					t.Error("dispatched despite the failed list")
				}
			}
		})
	}
}

// Decisions 3 and 4 of the hosted check as a gate: dispatch only, read-only token, no secret,
// no expression inside a script, a branch guard, a concurrency group per ref.
func TestHostedCheckWorkflow(t *testing.T) {
	root := repoRoot(t)
	wf := readFile(t, root, ".github/workflows/hosted-check.yml")
	var code []string
	for _, l := range strings.Split(wf, "\n") {
		if i := strings.Index(l, "#"); i >= 0 && !strings.Contains(l[:i], "'") {
			l = l[:i]
		}
		code = append(code, l)
	}
	body := strings.Join(code, "\n")
	on := regexp.MustCompile(`(?m)^on:\n((?:[ ]+.*\n|\n)*)`).FindStringSubmatch(body)
	if on == nil {
		t.Fatal("no on: block")
	}
	events := regexp.MustCompile(`(?m)^  ([a-z_]+):`).FindAllStringSubmatch(on[1], -1)
	if len(events) != 1 || events[0][1] != "workflow_dispatch" {
		t.Errorf("events = %v, want only workflow_dispatch", events)
	}
	if !regexp.MustCompile(`(?m)^permissions:\n  contents: read\n`).MatchString(body) {
		t.Error("permissions are not exactly contents: read at workflow level")
	}
	if strings.Contains(body, "secrets.") || strings.Contains(body, "GITHUB_TOKEN") {
		t.Error("the workflow references a secret or the token")
	}
	if !strings.Contains(body, "[[ $PKGS =~ ") || !strings.Contains(body, "[[ $w == -* ]]") {
		t.Error("the package list is not validated as a whole string and for flags")
	}
	if strings.Count(body, "permissions:") != 1 {
		t.Error("a job widens the permissions")
	}
	if !strings.Contains(body, "if: startsWith(github.ref, 'refs/heads/check/')") {
		t.Error("the job lacks the check/ branch guard")
	}
	if !strings.Contains(body, "group: hosted-check-${{ github.ref }}") {
		t.Error("the concurrency group is not keyed by github.ref")
	}
	inRun, indent := false, 0
	for _, l := range code {
		trim := strings.TrimLeft(l, " ")
		ind := len(l) - len(trim)
		switch {
		case inRun && trim != "" && ind <= indent:
			inRun = false
		case inRun && strings.Contains(l, "${{"):
			t.Errorf("expression inside a run: block: %s", l)
		}
		if !inRun && (strings.HasPrefix(trim, "run:")) {
			if strings.Contains(l, "${{") {
				t.Errorf("expression inside a run: line: %s", l)
			}
			inRun, indent = true, ind
		}
	}
}

// The opt-in cannot become the default silently.
func TestHostedCheckIsOptIn(t *testing.T) {
	mk := readFile(t, repoRoot(t), "Makefile")
	if !strings.Contains(mk, "\nCHECK_MAKE ?= $(MAKE)\n") {
		t.Error("CHECK_MAKE is no longer defaulted to $(MAKE) with ?=")
	}
	if !regexp.MustCompile(`(?m)^check-hosted:.*\n\t@\$\(MAKE\) check CHECK_MAKE=\./scripts/hosted-check\.sh$`).MatchString(mk) {
		t.Error("check-hosted does not pass CHECK_MAKE=./scripts/hosted-check.sh")
	}
}

// The sweep deletes the older check/ branches of this worktree and no one else's.
func TestHostedCheckSweepKeepsOtherWorktrees(t *testing.T) {
	needBash(t)
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not on PATH")
	}
	gitEnv := []string{"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=u", "GIT_AUTHOR_EMAIL=u@example.com", "GIT_COMMITTER_NAME=u", "GIT_COMMITTER_EMAIL=u@example.com"}
	bare, repo, state := t.TempDir(), t.TempDir(), t.TempDir()
	run := func(dir string, args ...string) string {
		c := exec.Command(git, args...)
		c.Dir = dir
		c.Env = append(os.Environ(), gitEnv...)
		out, err := c.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run(bare, "init", "-q", "--bare")
	run(repo, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(repo, "add", "a.txt")
	run(repo, "commit", "-q", "-m", "init")
	run(repo, "remote", "add", "origin", bare)
	stubs := stubBin(t, map[string]string{"gh": ghStub})
	path := stubs + string(os.PathListSeparator) + filepath.Dir(git) + string(os.PathListSeparator) + "/usr/bin" + string(os.PathListSeparator) + "/bin"
	env := append([]string{"GH_STATE=" + state, "GH_CONCL=success", "HOSTED_CHECK_POLL=0", "HOSTED_CHECK_DISPATCH_POLL=0"}, gitEnv...)
	// This worktree's branch name, from a dry run.
	_, out := runHostedCheck(t, repo, path, append([]string{"HOSTED_CHECK_DRY=1"}, env...), "check-run", "CHECK_PKGS=./a")
	m := regexp.MustCompile(`(check/[a-z0-9-]+-[0-9a-f]{6})-[0-9a-f]{12}`).FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("no branch in %q", out)
	}
	mine := m[1] + "-000000000000"
	others := []string{"check/main-ffffff-000000000000", "check/other-" + strings.TrimPrefix(m[1], "check/main-") + "-000000000000", "feature/x"}
	for _, b := range append([]string{mine}, others...) {
		run(repo, "push", "-q", "origin", "HEAD:refs/heads/"+b)
	}
	if code, out := runHostedCheck(t, repo, path, env, "check-run", "CHECK_PKGS=./a"); code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	got := run(bare, "for-each-ref", "--format=%(refname:short)", "refs/heads/")
	if strings.Contains(got, mine) {
		t.Errorf("the older branch of this worktree survived:\n%s", got)
	}
	for _, b := range others {
		if !strings.Contains(got, b) {
			t.Errorf("%s was swept:\n%s", b, got)
		}
	}
}

// New code that is not pushed would be skipped by the affected-package list and pass untested.
func TestHostedCheckRefusesUntrackedCode(t *testing.T) {
	needBash(t)
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not on PATH")
	}
	repo := t.TempDir()
	c := exec.Command(git, "init", "-q", "-b", "main")
	c.Dir = repo
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, f := range []string{"a.txt", "internal/newpkg/x.go"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(repo, f)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo, f), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gitEnv := []string{"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=u", "GIT_AUTHOR_EMAIL=u@example.com", "GIT_COMMITTER_NAME=u", "GIT_COMMITTER_EMAIL=u@example.com"}
	for _, args := range [][]string{{"add", "a.txt"}, {"commit", "-q", "-m", "init"}} {
		c := exec.Command(git, args...)
		c.Dir = repo
		c.Env = append(os.Environ(), gitEnv...)
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
	}
	path := filepath.Dir(git) + string(os.PathListSeparator) + "/usr/bin" + string(os.PathListSeparator) + "/bin"
	code, out := runHostedCheck(t, repo, path, append([]string{"HOSTED_CHECK_DRY=1"}, gitEnv...), "check-run", "CHECK_PKGS=./internal/newpkg")
	if code != 2 || !strings.Contains(out, "internal/newpkg/x.go") || !strings.Contains(out, "git add the new files first") {
		t.Errorf("exit %d, want 2 naming the file:\n%s", code, out)
	}
}

// A fake gh on PATH and a sleep that only advances a virtual clock: a 10-minute run costs a
// bounded number of `run view` calls, and an exhausted core quota waits for its reset instead of
// counting as a lost GitHub (exit 75).
func TestHostedCheckPollBudget(t *testing.T) {
	needBash(t)
	const gh = `S="$GH_STATE"
now() { cat "$S/clock" 2>/dev/null || echo 0; }
case "$1 $2" in
"auth status") exit 0 ;;
"workflow run") exit 0 ;;
"run list") if [ -e "$S/dispatched" ] || [ "$(now)" -gt 0 ]; then echo 101; fi; : > "$S/dispatched"; exit 0 ;;
"api rate_limit")
  if [ -n "$GH_QUOTA_OUT" ] && [ ! -e "$S/recovered" ]; then echo "0 $(( $(date +%s) + 120 ))"; else echo "4000 0"; fi
  exit 0 ;;
"run view")
  echo x >> "$S/views"
  if [ -n "$GH_QUOTA_OUT" ] && [ ! -e "$S/recovered" ]; then
    if [ "$(now)" -ge 120 ]; then : > "$S/recovered"; else exit 1; fi
  fi
  if [ "$(now)" -ge 600 ]; then echo "completed success"; else echo "in_progress "; fi
  exit 0 ;;
"run cancel") exit 0 ;;
esac
exit 99`
	const sleep = `S="$GH_STATE"
echo "$1" >> "$S/sleeps"
n=$(cat "$S/clock" 2>/dev/null || echo 0); echo $((n + ${1%.*})) > "$S/clock"`
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not on PATH")
	}
	gitEnv := []string{"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=u", "GIT_AUTHOR_EMAIL=u@example.com", "GIT_COMMITTER_NAME=u", "GIT_COMMITTER_EMAIL=u@example.com"}
	for _, quota := range []bool{false, true} {
		name := "ten minutes"
		if quota {
			name = "quota exhausted"
		}
		t.Run(name, func(t *testing.T) {
			bare, repo, state := t.TempDir(), t.TempDir(), t.TempDir()
			run := func(dir string, args ...string) {
				c := exec.Command(git, args...)
				c.Dir = dir
				c.Env = append(os.Environ(), gitEnv...)
				if out, err := c.CombinedOutput(); err != nil {
					t.Fatalf("git %v: %v\n%s", args, err, out)
				}
			}
			run(bare, "init", "-q", "--bare")
			run(repo, "init", "-q", "-b", "main")
			if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("a"), 0o644); err != nil {
				t.Fatal(err)
			}
			run(repo, "add", "a.txt")
			run(repo, "commit", "-q", "-m", "init")
			run(repo, "remote", "add", "origin", bare)
			stubs := stubBin(t, map[string]string{"gh": gh, "sleep": sleep})
			path := stubs + string(os.PathListSeparator) + filepath.Dir(git) + string(os.PathListSeparator) + "/usr/bin" + string(os.PathListSeparator) + "/bin"
			env := append([]string{"GH_STATE=" + state}, gitEnv...)
			if quota {
				env = append(env, "GH_QUOTA_OUT=1")
			}
			code, out := runHostedCheck(t, repo, path, env, "check-run", "CHECK_PKGS=./a")
			if code != 0 || !strings.Contains(out, "check: OK") {
				t.Fatalf("exit %d, want 0:\n%s", code, out)
			}
			views := strings.Count(readFileAbs(t, filepath.Join(state, "views")), "x")
			if views > 20 {
				t.Errorf("%d run view calls for a 10-minute run, want <= 20", views)
			}
			if quota {
				if !strings.Contains(out, "quota exhausted") {
					t.Errorf("the quota wait was not announced:\n%s", out)
				}
				if !regexp.MustCompile(`(?m)^1[12][0-9]$`).MatchString(readFileAbs(t, filepath.Join(state, "sleeps"))) {
					t.Errorf("no sleep until the reset:\n%s", readFileAbs(t, filepath.Join(state, "sleeps")))
				}
			}
		})
	}
}

func readFileAbs(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The Windows leg of the train gate (#1935): the script takes the packages from the environment as
// HOSTED_CHECK_TEST_WIN_PKGS (the seam), dispatches them as win_pkgs, says what the leg tests and, when the
// run is red, names the red leg(s) with each one's log excerpt, from the one poll that gave the verdict.
func TestHostedCheckWindowsLeg(t *testing.T) {
	needBash(t)
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not on PATH")
	}
	gitEnv := []string{"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=u", "GIT_AUTHOR_EMAIL=u@example.com", "GIT_COMMITTER_NAME=u", "GIT_COMMITTER_EMAIL=u@example.com"}
	const log = `check\tmake check-run\tLINUX-LINE\nwindows\ttest-shard\tWINDOWS-LINE\nwindows\ttest-shard\t--- FAIL: TestX (plugins_dir "/srv/p": must be an absolute path)`
	cases := []struct {
		name, concl, jobs string
		code              int
		want, notWant     []string
	}{
		{"both green", "success", "check=success,windows=success", 0, []string{"check: OK (hosted-check run 101)"}, []string{"red:"}},
		{"no Windows leg (skipped)", "success", "check=success,windows=skipped", 0, []string{"check: OK"}, nil},
		{"only Windows red", "failure", "check=success,windows=failure", 1,
			[]string{"run 101: failure (red: Windows)", "--- Windows leg", "WINDOWS-LINE", "must be an absolute path"}, []string{"LINUX-LINE", "red: Linux"}},
		{"only Linux red", "failure", "check=failure,windows=success", 1,
			[]string{"run 101: failure (red: Linux)", "--- Linux leg", "LINUX-LINE"}, []string{"WINDOWS-LINE", "red: Windows"}},
		{"both red", "failure", "check=failure,windows=failure", 1,
			[]string{"(red: Linux, Windows)", "--- Linux leg", "--- Windows leg", "LINUX-LINE", "WINDOWS-LINE"}, nil},
		{"Windows timed out", "failure", "check=success,windows=timed_out", 1, []string{"(red: Windows)", "--- Windows leg (windows): timed_out"}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			bare, repo, state := t.TempDir(), t.TempDir(), t.TempDir()
			run := func(dir string, args ...string) {
				cmd := exec.Command(git, args...)
				cmd.Dir = dir
				cmd.Env = append(os.Environ(), gitEnv...)
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("git %v: %v\n%s", args, err, out)
				}
			}
			run(bare, "init", "-q", "--bare")
			run(repo, "init", "-q", "-b", "main")
			if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("a"), 0o644); err != nil {
				t.Fatal(err)
			}
			run(repo, "add", "a.txt")
			run(repo, "commit", "-q", "-m", "init")
			run(repo, "remote", "add", "origin", bare)
			stubs := stubBin(t, map[string]string{"gh": ghStub})
			path := stubs + string(os.PathListSeparator) + filepath.Dir(git) + string(os.PathListSeparator) + "/usr/bin" + string(os.PathListSeparator) + "/bin"
			env := append([]string{"HOSTED_CHECK_TEST_WIN_PKGS=./a ./b", "GH_STATE=" + state, "GH_CONCL=" + c.concl, "GH_JOBS=" + c.jobs, "GH_LOG=" + log,
				"HOSTED_CHECK_POLL=0", "HOSTED_CHECK_DISPATCH_POLL=0"}, gitEnv...)
			code, out := runHostedCheck(t, repo, path, env, "check-run", "CHECK_PKGS=./a")
			if code != c.code {
				t.Fatalf("exit %d, want %d:\n%s", code, c.code, out)
			}
			for _, w := range c.want {
				if !strings.Contains(out, w) {
					t.Errorf("output lacks %q:\n%s", w, out)
				}
			}
			for _, w := range c.notWant {
				if strings.Contains(out, w) {
					t.Errorf("output has %q:\n%s", w, out)
				}
			}
			// A real run (not a dry one) says what the Windows leg tests, and that the seam was used.
			for _, w := range []string{"hosted-check: Windows leg: 2 package(s): ./a ./b", "HOSTED_CHECK_TEST_WIN_PKGS is set"} {
				if !strings.Contains(out, w) {
					t.Errorf("output lacks %q:\n%s", w, out)
				}
			}
			if got := readFileAbs(t, filepath.Join(state, "dispatch")); !strings.Contains(got, "-f pkgs=./a -f win_pkgs=./a ./b -f e2e=false") {
				t.Errorf("the dispatch does not carry both lists: %q", got)
			}
		})
	}
}

// The Windows list of the environment is validated before any git or gh call, like CHECK_PKGS, and
// shown by the dry run.
func TestHostedCheckWindowsListIsValidated(t *testing.T) {
	needBash(t)
	stubs := stubBin(t, map[string]string{"git": "exit 99", "gh": "exit 99"})
	for _, win := range []string{"./a;rm", "$(x)", "./a`x`", "./a\nrm -rf x", "-exec=/bin/x", "./a -exec=/bin/x"} {
		code, out := runHostedCheck(t, repoRoot(t), stubs, []string{"HOSTED_CHECK_TEST_WIN_PKGS=" + win}, "check-run", "CHECK_PKGS=./a")
		if code != 2 || !strings.Contains(out, "bad package list") {
			t.Errorf("HOSTED_CHECK_TEST_WIN_PKGS=%q: exit %d, want 2 with %q\n%s", win, code, "bad package list", out)
		}
	}
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not on PATH")
	}
	repo := t.TempDir()
	cmd := exec.Command(git, "init", "-q", "-b", "main")
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	gitEnv := []string{"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=u", "GIT_AUTHOR_EMAIL=u@example.com", "GIT_COMMITTER_NAME=u", "GIT_COMMITTER_EMAIL=u@example.com"}
	for _, args := range [][]string{{"commit", "-q", "--allow-empty", "-m", "init"}} {
		cmd := exec.Command(git, args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), gitEnv...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
	}
	path := filepath.Dir(git) + string(os.PathListSeparator) + "/usr/bin" + string(os.PathListSeparator) + "/bin"
	code, out := runHostedCheck(t, repo, path, append([]string{"HOSTED_CHECK_DRY=1", "HOSTED_CHECK_TEST_WIN_PKGS=./a ./b"}, gitEnv...), "check-run", "CHECK_PKGS=./a")
	if code != 0 || !strings.Contains(out, "win_pkgs='./a ./b'") {
		t.Errorf("dry run: exit %d, want 0 naming the Windows packages:\n%s", code, out)
	}
}

// hosted-check.yml has a `windows` job beside `check`: parallel, on the packages `make check` passes,
// the train gate's command, the same permissions and pins, and the list validated and never interpolated.
func TestHostedCheckWorkflowHasWindowsLeg(t *testing.T) {
	root := repoRoot(t)
	wf := readFile(t, root, ".github/workflows/hosted-check.yml")
	if !regexp.MustCompile(`(?m)^      win_pkgs:\n(?:        .*\n)*?        type: string\n`).MatchString(wf) {
		t.Error("no win_pkgs string input")
	}
	_, win, ok := strings.Cut(wf, "\n  windows:\n")
	if !ok {
		t.Fatal("no windows job")
	}
	if !strings.Contains(win, "runs-on: windows-latest") {
		t.Error("the windows job does not run on windows-latest")
	}
	if !strings.Contains(win, "if: startsWith(github.ref, 'refs/heads/check/') && inputs.win_pkgs != ''") {
		t.Error("the windows job lacks the check/ branch guard or the empty-list skip")
	}
	if strings.Contains(win, "needs:") {
		t.Error("the windows job waits for another: the legs must run in parallel")
	}
	if strings.Contains(win, "permissions:") || strings.Contains(win, "secrets.") {
		t.Error("the windows job widens the permissions or uses a secret")
	}
	if !strings.Contains(win, "WIN_PKGS: ${{ inputs.win_pkgs }}") || !strings.Contains(win, "[[ $WIN_PKGS =~ ^[A-Za-z0-9_./\\ -]*$ ]]") || !strings.Contains(win, "[[ $w == -* ]]") {
		t.Error("the list reaches the job without env or without the whole-string and flag validation")
	}
	if !strings.Contains(win, "bash scripts/test-shard.sh 4 all $WIN_PKGS") {
		t.Error("the windows job does not run the gate's command (test-shard.sh 4 all <packages>)")
	}
	if !strings.Contains(win, "shell: bash") || !strings.Contains(win, "git config --global init.defaultBranch master") {
		t.Error("the windows job lacks the bash shell or the git identity of ci.yml's Windows legs")
	}
	// Every action is pinned to a full commit, as the whole file does.
	for _, m := range regexp.MustCompile(`(?m)^\s*- uses: (\S+)`).FindAllStringSubmatch(win, -1) {
		if !regexp.MustCompile(`@[0-9a-f]{40}$`).MatchString(m[1]) {
			t.Errorf("action %s is not pinned to a commit", m[1])
		}
	}
	// The Linux job and the Windows job use the same pins.
	for _, a := range []string{"actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1", "actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e"} {
		if strings.Count(wf, a) != 2 {
			t.Errorf("%s is not used (pinned) by both jobs", a)
		}
	}
}

// The script computes the Windows packages itself, from the same changed paths `make check` selects its
// packages from (`affected-pkgs.sh --changed`, then `--code` over them): the Makefile hands it nothing but
// CHECK_PKGS. A copy of the script runs beside a stub affected-pkgs.sh in a toy repository, from a
// subdirectory and by a relative path: the stub is found through an absolute directory, after the cd.
func TestHostedCheckComputesWindowsPackages(t *testing.T) {
	needBash(t)
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not on PATH")
	}
	gitEnv := []string{"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=u", "GIT_AUTHOR_EMAIL=u@example.com", "GIT_COMMITTER_NAME=u", "GIT_COMMITTER_EMAIL=u@example.com"}
	script := readFile(t, repoRoot(t), "scripts/hosted-check.sh")
	// The stub answers --changed with $AFF_CHANGED (failing when $AFF_FAIL is changed) and --code by
	// recording its stdin and printing $AFF_CODE (failing when $AFF_FAIL is code).
	const stub = `case "$1" in
--changed) [ "$AFF_FAIL" != changed ] || { echo "stub: no merge-base" >&2; exit 1; }; printf '%b' "$AFF_CHANGED" ;;
--code) cat > "$AFF_STDIN"; [ "$AFF_FAIL" != code ] || { echo "stub: go list failed" >&2; exit 1; }; printf '%b' "$AFF_CODE" ;;
*) exit 99 ;;
esac`
	cases := []struct {
		name, fail, code string
		env              []string
		wantExit         int
		want, notWant    []string
	}{
		{"computed from the changed paths", "", `./a\n./b\n`, nil, 0, []string{"win_pkgs='./a ./b'", "Windows leg: 2 package(s): ./a ./b"}, []string{"test seam"}},
		{"nothing to test on Windows", "", "", nil, 0, []string{"win_pkgs=''", "no Windows leg: no Go code changed"}, nil},
		{"a stray CHECK_WIN_PKGS in the environment is ignored", "", `./a\n./b\n`, []string{"CHECK_WIN_PKGS="}, 0, []string{"win_pkgs='./a ./b'"}, []string{"test seam"}},
		{"the changed paths cannot be computed", "changed", `./a\n`, nil, 1, []string{"cannot compute the changed paths", "stub: no merge-base"}, []string{"would push"}},
		{"the packages cannot be computed", "code", `./a\n`, nil, 1, []string{"cannot compute the Windows packages", "stub: go list failed"}, []string{"would push"}},
		{"a computed flag-like word is refused", "", `./a\n-exec=/bin/x\n`, nil, 2, []string{"bad package list"}, []string{"would push"}},
		{"the test seam replaces the computation and says so", "changed", "", []string{"HOSTED_CHECK_TEST_WIN_PKGS=./z"}, 0, []string{"win_pkgs='./z'", "HOSTED_CHECK_TEST_WIN_PKGS is set", "Windows leg: 1 package(s): ./z"}, []string{"stub:"}},
		{"an empty test seam means no Windows leg", "changed", "", []string{"HOSTED_CHECK_TEST_WIN_PKGS="}, 0, []string{"win_pkgs=''", "HOSTED_CHECK_TEST_WIN_PKGS is set", "no Windows leg"}, []string{"stub:"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			repo, state := t.TempDir(), t.TempDir()
			run := func(args ...string) {
				cmd := exec.Command(git, args...)
				cmd.Dir = repo
				cmd.Env = append(os.Environ(), gitEnv...)
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("git %v: %v\n%s", args, err, out)
				}
			}
			if err := os.MkdirAll(filepath.Join(repo, "scripts"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(repo, "sub"), 0o755); err != nil {
				t.Fatal(err)
			}
			for name, body := range map[string]string{"scripts/hosted-check.sh": script, "scripts/affected-pkgs.sh": "#!/bin/sh\n" + stub + "\n"} {
				if err := os.WriteFile(filepath.Join(repo, name), []byte(body), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			run("init", "-q", "-b", "main")
			run("add", "scripts")
			run("commit", "-q", "-m", "init")
			path := filepath.Dir(git) + string(os.PathListSeparator) + "/usr/bin" + string(os.PathListSeparator) + "/bin"
			env := append([]string{"HOSTED_CHECK_DRY=1", "AFF_FAIL=" + c.fail, `AFF_CHANGED=a.go\nb.md\n`, "AFF_CODE=" + c.code,
				"AFF_STDIN=" + filepath.Join(state, "stdin")}, gitEnv...)
			code, out := runHostedCheckScript(t, "../scripts/hosted-check.sh", filepath.Join(repo, "sub"), path, append(env, c.env...), "check-run", "CHECK_PKGS=./a")
			if code != c.wantExit {
				t.Fatalf("exit %d, want %d:\n%s", code, c.wantExit, out)
			}
			for _, w := range c.want {
				if !strings.Contains(out, w) {
					t.Errorf("output lacks %q:\n%s", w, out)
				}
			}
			for _, w := range c.notWant {
				if strings.Contains(out, w) {
					t.Errorf("output has %q:\n%s", w, out)
				}
			}
			if c.fail == "" && len(c.env) == 0 {
				if got := readFileAbs(t, filepath.Join(state, "stdin")); got != "a.go\nb.md\n" {
					t.Errorf("--code did not get the changed paths of --changed on stdin: %q", got)
				}
			}
		})
	}
}

// The link between the two real scripts, unstubbed: hosted-check.sh feeds the real affected-pkgs.sh
// (`--changed`, then `--code`) in a toy module with an `origin/main` (a bare remote), and the dry run
// prints the Windows list. A changed Go file gives its package and the packages whose test binary
// depends on it, plus the platform guard; a changed markdown file gives none (the readers of
// repository files are not the gate's Windows leg).
func TestHostedCheckRealAffectedPkgsFeedsTheWindowsList(t *testing.T) {
	needBash(t)
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not on PATH")
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go not on PATH")
	}
	gitEnv := []string{"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=u", "GIT_AUTHOR_EMAIL=u@example.com", "GIT_COMMITTER_NAME=u", "GIT_COMMITTER_EMAIL=u@example.com"}
	root := repoRoot(t)
	files := map[string]string{
		"go.mod":                      "module example.com/toy\n\ngo 1.20\n",
		"internal/a/a.go":             "package a\n\nfunc A() int { return 1 }\n",
		"internal/b/b.go":             "package b\n\nimport \"example.com/toy/internal/a\"\n\nfunc B() int { return a.A() }\n",
		"internal/b/b_test.go":        "package b\n\nimport \"testing\"\n\nfunc TestB(t *testing.T) { _ = B() }\n",
		"internal/c/c.go":             "package c\n\nfunc C() int { return 3 }\n",
		"README.md":                   "toy\n",
		"scripts/hosted-check.sh":     readFile(t, root, "scripts/hosted-check.sh"),
		"scripts/affected-pkgs.sh":    readFile(t, root, "scripts/affected-pkgs.sh"),
		"scripts/upstream-repo.sh":    "#!/bin/sh\necho example/toy\n",
		"internal/procattr/p.go":      "package procattr\n",
		"internal/procattr/p_test.go": "package procattr\n\nimport \"testing\"\n\nfunc TestP(t *testing.T) {}\n",
	}
	cases := []struct {
		name, file, body string
		want, notWant    []string
	}{
		{"a Go file: its package, its dependents and the guard", "internal/a/a.go", "package a\n\nfunc A() int { return 2 }\n",
			[]string{"win_pkgs='./internal/a ./internal/b ./internal/procattr'", "Windows leg: 3 package(s)"}, []string{"./internal/c", "./internal/repodocs"}},
		{"a markdown file: no Windows package", "README.md", "toy, changed\n",
			[]string{"win_pkgs=''", "no Windows leg: no Go code changed"}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			bare, repo := t.TempDir(), t.TempDir()
			run := func(dir string, args ...string) {
				cmd := exec.Command(git, args...)
				cmd.Dir = dir
				cmd.Env = append(os.Environ(), gitEnv...)
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("git %v: %v\n%s", args, err, out)
				}
			}
			for name, body := range files {
				p := filepath.Join(repo, name)
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			run(bare, "init", "-q", "--bare")
			run(repo, "init", "-q", "-b", "main")
			run(repo, "add", ".")
			run(repo, "commit", "-q", "-m", "init")
			run(repo, "remote", "add", "origin", bare)
			run(repo, "push", "-q", "origin", "main") // origin/main: the merge-base --changed computes
			if err := os.WriteFile(filepath.Join(repo, c.file), []byte(c.body), 0o755); err != nil {
				t.Fatal(err)
			}
			path := filepath.Dir(git) + string(os.PathListSeparator) + filepath.Dir(goBin) + string(os.PathListSeparator) + "/usr/bin" + string(os.PathListSeparator) + "/bin"
			env := append([]string{"HOSTED_CHECK_DRY=1", "GOTOOLCHAIN=local", "GOPROXY=off", "GOFLAGS=", "GOWORK=off"}, gitEnv...)
			code, out := runHostedCheckScript(t, "scripts/hosted-check.sh", repo, path, env, "check-run", "CHECK_PKGS=./internal/a")
			if code != 0 {
				t.Fatalf("exit %d, want 0:\n%s", code, out)
			}
			for _, w := range c.want {
				if !strings.Contains(out, w) {
					t.Errorf("output lacks %q:\n%s", w, out)
				}
			}
			for _, w := range c.notWant {
				if strings.Contains(out, w) {
					t.Errorf("output has %q:\n%s", w, out)
				}
			}
		})
	}
}
