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
func runHostedCheck(t *testing.T, dir, path string, env []string, args ...string) (int, string) {
	t.Helper()
	root := repoRoot(t)
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not on PATH")
	}
	cmd := exec.Command(bash, append([]string{filepath.Join(root, "scripts/hosted-check.sh")}, args...)...)
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
// later ones show the new run 101, whose conclusion is $GH_CONCL.
const ghStub = `S="$GH_STATE"
case "$1 $2" in
"auth status") exit 0 ;;
"api "*) echo; exit 0 ;;
"workflow run") [ -z "$GH_DISPATCH_FAIL" ] || exit 1; : > "$S/dispatched"; exit 0 ;;
"run list")
  if [ ! -e "$S/dispatched" ]; then [ -z "$GH_LIST_FAIL" ] || exit 1; echo 100; exit 0; fi
  n=$(cat "$S/polls" 2>/dev/null || echo 0); echo $((n + 1)) > "$S/polls"
  if [ "$n" -ge 1 ]; then echo 101; fi
  echo 100; exit 0 ;;
"run view")
  case "$*" in
  *--log-failed*) echo "FAILED-LOG-LINE"; exit 0 ;;
  esac
  if [ "$3" = 100 ]; then echo "completed success"; else echo "completed $GH_CONCL"; fi
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
			env := append([]string{"GH_STATE=" + state, "GH_CONCL=" + c.concl, "HOSTED_CHECK_POLL=0"}, gitEnv...)
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
	env := append([]string{"GH_STATE=" + state, "GH_CONCL=success", "HOSTED_CHECK_POLL=0"}, gitEnv...)
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
