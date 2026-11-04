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
exit "${FAKE_MAKE_RC:-0}"
`, 0o755)
	e.write(filepath.Join(bin, "gh"), `#!/bin/sh
echo "$*" >> "$FAKE_GH_LOG"
case "$1 $2" in
  "repo view") echo "$FAKE_PERM" ;;
  "run list") if [ -f "$FAKE_GH_RUNS" ]; then cat "$FAKE_GH_RUNS"; else echo '[]'; fi ;;
  "pr view") exit 1 ;;
esac
exit 0
`, 0o755)

	e.up = filepath.Join(tmp, "up", "proj.git")
	e.origin = filepath.Join(tmp, "me", "proj.git")
	e.work = filepath.Join(tmp, "work")
	e.run(tmp, "git", "init", "-q", "--bare", "-b", "main", e.up)
	e.run(tmp, "git", "init", "-q", "--bare", "-b", "main", e.origin)
	e.run(tmp, "git", "init", "-q", "-b", "main", e.work)
	e.run(e.work, "git", "remote", "add", "upstream", e.up)
	e.run(e.work, "git", "remote", "add", "origin", e.origin)
	for _, s := range []string{"signoff.sh", "upstream-repo.sh", "ci-filters.sh"} {
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
	for _, want := range []string{"POST", "repos/up/proj/statuses/" + head, "state=success", "context=signoff", "make gate - linux/amd64 - "} {
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
	if got := strings.TrimSpace(e.log(e.makeLog)); got != "gate" {
		t.Errorf("a diff without web paths runs gate alone, got %q", got)
	}
}

// The web decision has one source, ci-filters.sh: a web path adds web-gate.
func TestSignoffAddsWebGateWhenTheDiffNeedsIt(t *testing.T) {
	e := newSignoffEnv(t)
	e.branch(e.work, "feat/x", "web/src/a.ts")
	if out, ok := e.signoff(e.work); !ok {
		t.Fatalf("signoff failed\n%s", out)
	}
	if got := strings.TrimSpace(e.log(e.makeLog)); got != "gate web-gate" {
		t.Errorf("a web/src change runs gate and web-gate, got %q", got)
	}
	if p := e.posts(); len(p) != 1 || !strings.Contains(p[0], "make gate web-gate - ") {
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
