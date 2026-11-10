package repodocs

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

// landEnv is a maintainer's clone of a repository `up/proj` (a bare repository
// standing for GitHub), with every script of scripts/ and the changelog checker
// copied in, a fake gh that answers like GitHub (a merged train PR moves main to
// its head, as a rebase merge of a linear train does) and a fake make that fails
// the gate when the tree holds BAD, or the first time it holds FLAKY.
type landEnv struct {
	t                  *testing.T
	env                []string
	work, up, tmp, bin string
	ghLog              string
}

func newLandEnv(t *testing.T) *landEnv {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("bash scripts, run by the Unix legs")
	}
	for _, b := range []string{"bash", "git", "jq", "perl", "go"} {
		if _, err := exec.LookPath(b); err != nil {
			t.Skipf("%s not found", b)
		}
	}
	root := repoRoot(t)
	tmp, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	e := &landEnv{t: t, tmp: tmp, bin: filepath.Join(tmp, "bin"), ghLog: filepath.Join(tmp, "gh.log")}
	e.up = filepath.Join(tmp, "up", "proj.git")
	e.work = filepath.Join(tmp, "work")
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GIT_") && !strings.HasPrefix(kv, "SIGNOFF_") && !strings.HasPrefix(kv, "LAND_") &&
			!strings.HasPrefix(kv, "FAKE_") && !strings.HasPrefix(kv, "XDG_STATE_HOME=") {
			env = append(env, kv)
		}
	}
	e.env = append(env,
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=user", "GIT_AUTHOR_EMAIL=user@example.com",
		"GIT_COMMITTER_NAME=user", "GIT_COMMITTER_EMAIL=user@example.com",
		"FAKE_GH_LOG="+e.ghLog, "FAKE_UP="+e.up, "FAKE_STATE="+filepath.Join(tmp, "gh-state"),
		"SIGNOFF_MAKE="+filepath.Join(e.bin, "fakemake"), "SIGNOFF_POLL=0",
		"LAND_STATE="+filepath.Join(tmp, "lander"), "LAND_POLL=0", "LAND_MERGE_TRIES=3",
		"PATH="+e.bin+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	e.write(filepath.Join(e.bin, "fakemake"), `#!/bin/sh
case "$*" in *signoff-platform*) echo linux/amd64; exit 0 ;; esac
echo "$*" >> "$FAKE_STATE.make"
case "$*" in test-rerun*) [ -e CONFIRMED ] && exit 1; exit 0 ;; esac
[ -e BAD ] && exit 2
# Another PR merges while the first gate runs (FAKE_MOVE_MAIN): main moves under the train.
if [ -n "${FAKE_MOVE_MAIN:-}" ] && [ ! -e "$FAKE_STATE.moved" ]; then
  touch "$FAKE_STATE.moved"
  c=$(echo "chore: other" | git --git-dir="$FAKE_UP" commit-tree "$(git --git-dir="$FAKE_UP" rev-parse 'main^{tree}')" -p main)
  git --git-dir="$FAKE_UP" update-ref refs/heads/main "$c"
fi
if [ -e CONFIRMED ] || { [ -e FLAKYGO ] && [ ! -e "$FAKE_STATE.flakedgo" ]; }; then
  touch "$FAKE_STATE.flakedgo"
  printf -- '--- FAIL: TestNamed (0.01s)\nFAIL\tgithub.com/x/p/internal/kb\t0.1s\nmake: *** [Makefile:23: test] Error 1\n'
  exit 2
fi
if [ -e FLAKY ] && [ ! -e "$FAKE_STATE.flaked" ]; then touch "$FAKE_STATE.flaked"; echo "--- FAIL: TestSometimes"; exit 2; fi
exit 0
`)
	e.write(filepath.Join(e.bin, "gh"), `#!/bin/bash
echo "$*" >> "$FAKE_GH_LOG"
jq_expr=; prev=
for a in "$@"; do [ "$prev" = --jq ] && jq_expr=$a; prev=$a; done
out() { if [ -n "$jq_expr" ]; then jq -r "$jq_expr"; else cat; fi; }
# The required checks of the train PR: red until the failed jobs are rerun (FAKE_CHECKS_RED=once),
# or whatever is rerun (=always).
red() { case "${FAKE_CHECKS_RED:-}" in always) return 0 ;; once) [ ! -e "$FAKE_STATE.reran" ] ;; *) return 1 ;; esac; }
case "$1 $2" in
  "repo view") echo "${FAKE_PERM:-ADMIN}" ;;
  "api user") echo '{"login":"user"}' | out ;;
  "api -X") ;;
  "api repos/up/proj") echo '{"fork":false}' | out ;;
  "api repos/up/proj/issues"*) if [ -n "${FAKE_GH_LAST:-}" ]; then echo "[{\"number\":$FAKE_GH_LAST}]"; else echo '[]'; fi | out ;;
  "run list")
    # The hosted pre-check of a queued commit (ready-check.yml): green unless the commit is
    # named in FAKE_PRECHECK_RED (red) or FAKE_PRECHECK_RUNNING (in progress).
    case "$*" in
      *ready-check.yml*)
        c=; prev=; for a in "$@"; do [ "$prev" = --commit ] && c=$a; prev=$a; done
        if [[ " ${FAKE_PRECHECK_RUNNING:-} " == *" $c "* ]]; then echo '[{"status":"in_progress","conclusion":""}]'
        elif [[ " ${FAKE_PRECHECK_RED:-} " == *" $c "* ]]; then echo '[{"status":"completed","conclusion":"failure"}]'
        else echo '[{"status":"completed","conclusion":"success"}]'; fi | out ;;
      *--status*failure*)
        # The failed run completes only after FAKE_FAILED_RUNS_AFTER listings (a failed job of a run in progress).
        n=$(cat "$FAKE_STATE.fl" 2>/dev/null || echo 0); echo $((n + 1)) > "$FAKE_STATE.fl"
        if [ "$n" -lt "${FAKE_FAILED_RUNS_AFTER:-0}" ]; then echo '[]' | out; else echo '[{"databaseId":7}]' | out; fi ;;
      *) echo '[]' | out ;;
    esac ;;
  "run rerun") touch "$FAKE_STATE.reran" ;;
  "pr list") if [ -e "$FAKE_STATE.pr" ] && [ "$(cat "$FAKE_STATE.pr")" != OPEN ]; then echo '[]'; else echo "${FAKE_GH_TRAINS:-[]}"; fi | out ;;
  "pr create")
    head=; prev=; for a in "$@"; do [ "$prev" = --head ] && head=$a; prev=$a; done
    echo "$head" > "$FAKE_STATE.head"; echo OPEN > "$FAKE_STATE.pr"
    # Another PR merges between the train PR's creation and its merge (FAKE_MOVE_ON_CREATE).
    if [ -n "${FAKE_MOVE_ON_CREATE:-}" ] && [ ! -e "$FAKE_STATE.moved" ]; then
      touch "$FAKE_STATE.moved"
      c=$(echo "chore: other" | git --git-dir="$FAKE_UP" commit-tree "$(git --git-dir="$FAKE_UP" rev-parse 'main^{tree}')" -p main)
      git --git-dir="$FAKE_UP" update-ref refs/heads/main "$c"
    fi
    echo "https://github.com/up/proj/pull/42" ;;
  "pr merge")
    git --git-dir="$FAKE_UP" update-ref refs/heads/main "refs/heads/$(cat "$FAKE_STATE.head")"
    echo MERGED > "$FAKE_STATE.pr" ;;
  "pr close") [ -n "${FAKE_CLOSE_FAILS:-}" ] && exit 1; echo CLOSED > "$FAKE_STATE.pr" ;;
  "pr checks") if red; then echo '[{"bucket":"fail"}]'; else echo '[{"bucket":"pass"}]'; fi | out ;;
  "pr view") ms=${FAKE_MERGE_STATE:-CLEAN}; red && ms=BLOCKED
    printf '{"state":"%s","mergeStateStatus":"%s"}\n' "$(cat "$FAKE_STATE.pr" 2>/dev/null || echo OPEN)" "$ms" | out ;;
  "issue create") ;;
esac
exit 0
`)
	e.run(tmp, "git", "init", "-q", "--bare", "-b", "main", e.up)
	e.run(tmp, "git", "init", "-q", "-b", "main", e.work)
	e.run(e.work, "git", "remote", "add", "origin", e.up)
	scripts, err := os.ReadDir(filepath.Join(root, "scripts"))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range scripts {
		if !s.IsDir() {
			e.write(filepath.Join(e.work, "scripts", s.Name()), readFile(t, root, "scripts/"+s.Name()))
		}
	}
	// commit-lint.sh reads the scopes from pr-title.yml and runs the changelog checker
	// from the repository it lives in.
	e.write(filepath.Join(e.work, ".github", "workflows", "pr-title.yml"), readFile(t, root, ".github/workflows/pr-title.yml"))
	goLine := regexp.MustCompile(`(?m)^go \S+$`).FindString(readFile(t, root, "go.mod"))
	e.write(filepath.Join(e.work, "go.mod"), "module github.com/BeppeTemp/cartographer\n\n"+goLine+"\n")
	for _, dir := range []string{"internal/changelog", "internal/changelog/gen"} {
		files, _ := filepath.Glob(filepath.Join(root, dir, "*.go"))
		for _, f := range files {
			if !strings.HasSuffix(f, "_test.go") {
				rel, _ := filepath.Rel(root, f)
				e.write(filepath.Join(e.work, rel), readFile(t, root, filepath.ToSlash(rel)))
			}
		}
	}
	e.write(filepath.Join(e.work, "scripts", "freeze-open.sh"), "#!/bin/sh\nexit \"${FAKE_FREEZE:-1}\"\n")
	e.write(filepath.Join(e.work, "docs", "decisions", "D10-old.md"), "---\ntopic: x\n---\n\n# D10 — Old\n")
	e.write(filepath.Join(e.work, "a.txt"), "a\n")
	e.git("add", ".")
	e.git("commit", "-q", "-m", "init")
	e.git("push", "-q", "origin", "main")
	return e
}

func (e *landEnv) write(path, body string) {
	e.t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		e.t.Fatal(err)
	}
}

func (e *landEnv) run(dir, name string, args ...string) string {
	e.t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir, cmd.Env = dir, e.env
	out, err := cmd.CombinedOutput()
	if err != nil {
		e.t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (e *landEnv) git(args ...string) string { e.t.Helper(); return e.run(e.work, "git", args...) }

// queue pushes ready/<slug>: one commit on origin/main adding files, with message
// msg, committed at date (the queue's order).
func (e *landEnv) queue(slug, msg, date string, files map[string]string) {
	e.t.Helper()
	e.git("fetch", "-q", "origin")
	e.git("checkout", "-q", "-B", "work-"+slug, "origin/main")
	for p, body := range files {
		e.write(filepath.Join(e.work, p), body)
	}
	e.git("add", ".")
	cmd := exec.Command("git", "commit", "-q", "-F", "-")
	cmd.Dir, cmd.Env = e.work, append(append([]string{}, e.env...), "GIT_COMMITTER_DATE="+date, "GIT_AUTHOR_DATE="+date)
	cmd.Stdin = strings.NewReader(msg)
	if out, err := cmd.CombinedOutput(); err != nil {
		e.t.Fatalf("commit: %v\n%s", err, out)
	}
	e.git("push", "-q", "--force", "origin", "HEAD:refs/heads/ready/"+slug)
	e.git("checkout", "-q", "main")
}

func (e *landEnv) land(extra ...string) (string, bool) {
	e.t.Helper()
	cmd := exec.Command("bash", filepath.Join(e.work, "scripts", "land.sh"))
	cmd.Dir, cmd.Env = e.work, append(append([]string{}, e.env...), extra...)
	out, err := cmd.CombinedOutput()
	return string(out), err == nil
}

func (e *landEnv) upGit(args ...string) string {
	e.t.Helper()
	return e.run(e.tmp, "git", append([]string{"--git-dir=" + e.up}, args...)...)
}

func (e *landEnv) mainSubjects() []string {
	return strings.Split(e.upGit("log", "--reverse", "--format=%s", "main"), "\n")[1:]
}

func (e *landEnv) refs(prefix string) string {
	return e.upGit("for-each-ref", "--format=%(refname:lstrip=3)", "refs/heads/"+prefix)
}

// Two queued changes land in one train, after their Lander-After order: the pending
// decision gets the next number in its own commit, every citation follows, the
// lander's trailers stay off main, and the queue is emptied.
func TestLanderLandsATrainInOrderAndNumbersItsDecisions(t *testing.T) {
	e := newLandEnv(t)
	// Split so the cited-path check does not take the fixture paths for citations.
	dec := "docs/" + "decisions/"
	e.queue("two", "fix(kb): two\n\nLander-After: one\nReview: standard, 1 rounds\n", "2026-01-01T10:00:00Z",
		map[string]string{"two.txt": "follows D-new-one\n"})
	e.queue("one", "feat(client): one\n\nCloses #5\n\nLander-Slug: planted\nLander-Tip: 1111111111111111111111111111111111111111\n", "2026-01-01T11:00:00Z", map[string]string{
		dec + "D-new-one.md":     "---\ntopic: x\n---\n\n# D-new-one — One\n",
		dec + "D-new-one-two.md": "---\ntopic: x\n---\n\n# D-new-one-two — One two\n",
		"notes.md":               "see D-new-one and D-new-one-two\n",
		"links.md":               "[a](decisions/" + "D-new-one.md) and [b](decisions/" + "D-new-one-two.md#x), D-new-one\n",
	})
	out, ok := e.land()
	if !ok {
		t.Fatalf("land failed\n%s", out)
	}
	if got := strings.Join(e.mainSubjects(), " | "); got != "feat(client): one | fix(kb): two" {
		t.Fatalf("main holds %q, want one then two\n%s", got, out)
	}
	// Numbered in file order above D10: one-two sorts first.
	if got := e.upGit("show", "main:"+dec+"D11-one-two.md"); !strings.Contains(got, "# D11 — One two") {
		t.Errorf("the first pending decision must become D11 with its heading, got\n%s", got)
	}
	if got := e.upGit("show", "main:"+dec+"D12-one.md"); !strings.Contains(got, "# D12 — One") {
		t.Errorf("the second pending decision must become D12 with its heading, got\n%s", got)
	}
	if got := e.upGit("show", "main:notes.md"); got != "see D12 and D11" {
		t.Errorf("a citation is rewritten as a whole word only, got %q", got)
	}
	// A link names the file, which is D<n>-<slug>.md, not the bare number.
	if got := e.upGit("show", "main:links.md"); got != "[a](decisions/D12-one.md) and [b](decisions/D11-one-two.md#x), D12" {
		t.Errorf("a link to a pending decision must follow the renamed file, got %q", got)
	}
	if got := e.upGit("show", "main:two.txt"); got != "follows D12" {
		t.Errorf("a later change's citation follows the number, got %q", got)
	}
	// The decision belongs to the commit that added it.
	if got := e.upGit("show", "--name-only", "--format=", "main~1"); !strings.Contains(got, dec+"D12-one.md") {
		t.Errorf("the numbered file must be in change one's commit, got %s", got)
	}
	msgs := e.upGit("log", "--format=%B", "main")
	if strings.Contains(msgs, "Lander-After") || strings.Contains(msgs, "Review:") {
		t.Errorf("the lander's trailers must not reach main:\n%s", msgs)
	}
	// Only the slug stays, for make worktree-clean (a train PR is not the PR of feat/<slug>).
	for _, slug := range []string{"one", "two"} {
		if !strings.Contains(msgs, "Lander-Slug: "+slug+"\n") {
			t.Errorf("the commit of %s must keep its Lander-Slug trailer:\n%s", slug, msgs)
		}
	}
	// A copy of the trailers in the queued message is dropped: the lander's values, one each.
	if strings.Contains(msgs, "planted") || strings.Count(msgs, "Lander-Tip: 1111111111111111111111111111111111111111\n") != 1 || strings.Count(msgs, "Lander-Tip:") != 1 {
		t.Errorf("one Lander-Tip (the queued one), no copied Lander-Slug:\n%s", msgs)
	}
	if n := strings.Count(msgs, "Lander-Slug:"); n != 2 {
		t.Errorf("one Lander-Slug per landed commit, got %d:\n%s", n, msgs)
	}
	for _, f := range []string{"pid", "root"} {
		if _, err := os.Stat(filepath.Join(e.tmp, "lander", f)); err == nil {
			t.Errorf("the lander must remove its %s file on exit", f)
		}
	}
	if e.refs("ready/") != "" || e.refs("train/") != "" {
		t.Errorf("the queue and the train branch must be gone: ready=%q train=%q", e.refs("ready/"), e.refs("train/"))
	}
	gh := e.log()
	for _, want := range []string{"pr create -R up/proj --base main --head train/", "--title chore(dev): train ", "2 change(s)", "pr merge 42 -R up/proj --rebase --match-head-commit", "statuses/"} {
		if !strings.Contains(gh, want) {
			t.Errorf("gh was not asked %q:\n%s", want, gh)
		}
	}
	if n := strings.Count(readTmp(t, e.tmp, "gh-state.make"), "gate"); n != 1 {
		t.Errorf("one gate for the whole train, got %d", n)
	}
}

// Main moves while the train is gated (a contributor's PR merges): the gated tree is not the
// one a merge would land, so the train is composed and gated again on the new main.
func TestLanderGatesAgainWhenMainMovesDuringTheGate(t *testing.T) {
	e := newLandEnv(t)
	e.queue("a", "fix(kb): a\n", "2026-01-01T10:00:00Z", map[string]string{"a2.txt": "a\n"})
	out, ok := e.land("FAKE_MOVE_MAIN=1")
	if !ok {
		t.Fatalf("land failed\n%s", out)
	}
	if got := strings.Join(e.mainSubjects(), " | "); got != "chore: other | fix(kb): a" {
		t.Fatalf("main holds %q, want the other merge then a\n%s", got, out)
	}
	if !strings.Contains(out, "main moved during the gate") {
		t.Errorf("the lander must say why it gates again:\n%s", out)
	}
	if n := strings.Count(e.makeLog(), "-k gate"); n != 2 {
		t.Errorf("a gate on the old main and one on the new: %d gates\n%s", n, e.makeLog())
	}
	if n := strings.Count(e.log(), "pr create"); n != 1 {
		t.Errorf("no PR for the tree gated on the old main: %d PRs\n%s", n, e.log())
	}
}

// Main moves between the train PR's creation and its merge: the lander closes the PR instead
// of merging it onto a main no gate saw, and lands the train gated again.
func TestLanderClosesTheTrainPRWhenMainMovesBeforeTheMerge(t *testing.T) {
	e := newLandEnv(t)
	e.queue("a", "fix(kb): a\n", "2026-01-01T10:00:00Z", map[string]string{"a2.txt": "a\n"})
	out, ok := e.land("FAKE_MOVE_ON_CREATE=1")
	if !ok {
		t.Fatalf("land failed\n%s", out)
	}
	if got := strings.Join(e.mainSubjects(), " | "); got != "chore: other | fix(kb): a" {
		t.Fatalf("main holds %q, want the other merge then a\n%s", got, out)
	}
	gh := e.log()
	if !strings.Contains(gh, "pr close 42 -R up/proj --delete-branch") {
		t.Errorf("the PR gated on the old main must be closed:\n%s", gh)
	}
	if n := strings.Count(gh, "pr merge"); n != 1 {
		t.Errorf("one merge, of the train gated on the new main: %d\n%s", n, gh)
	}
	if n := strings.Count(e.makeLog(), "-k gate"); n != 2 {
		t.Errorf("two gates: %d\n%s", n, e.makeLog())
	}
}

// A change whose hosted pre-check is red never joins a train of several: the green ones land
// together, and it is gated alone afterwards (green there: the pre-check was the flaky one).
func TestLanderGatesARedPrecheckAlone(t *testing.T) {
	e := newLandEnv(t)
	e.queue("a", "fix(kb): a\n", "2026-01-01T10:00:00Z", map[string]string{"a2.txt": "a\n"})
	e.queue("b", "fix(kb): b\n", "2026-01-01T11:00:00Z", map[string]string{"b.txt": "b\n"})
	e.queue("c", "fix(kb): c\n", "2026-01-01T12:00:00Z", map[string]string{"c.txt": "c\n"})
	out, ok := e.land("FAKE_PRECHECK_RED=" + e.upGit("rev-parse", "ready/b"))
	if !ok {
		t.Fatalf("land failed\n%s", out)
	}
	if got := strings.Join(e.mainSubjects(), " | "); got != "fix(kb): a | fix(kb): c | fix(kb): b" {
		t.Fatalf("main holds %q, want a and c, then b alone\n%s", got, out)
	}
	if !strings.Contains(out, "b: its hosted pre-check is red: gated alone") {
		t.Errorf("the lander must say why b is alone:\n%s", out)
	}
	if n := strings.Count(e.makeLog(), "-k gate"); n != 2 {
		t.Errorf("one gate for a and c, one for b: %d\n%s", n, e.makeLog())
	}
}

// While every queued change's pre-check still runs, the build host does not idle: the
// oldest change is gated alone, where a broken leg costs no bisection; the next one too.
func TestLanderGatesAChangeAloneWhileThePrechecksRun(t *testing.T) {
	e := newLandEnv(t)
	e.queue("a", "fix(kb): a\n", "2026-01-01T10:00:00Z", map[string]string{"a2.txt": "a\n"})
	e.queue("b", "fix(kb): b\n", "2026-01-01T11:00:00Z", map[string]string{"b.txt": "b\n"})
	running := e.upGit("rev-parse", "ready/a") + " " + e.upGit("rev-parse", "ready/b")
	out, ok := e.land("FAKE_PRECHECK_RUNNING="+running, "LAND_PRECHECK_WAIT=999999999")
	if !ok {
		t.Fatalf("land failed\n%s", out)
	}
	if got := strings.Join(e.mainSubjects(), " | "); got != "fix(kb): a | fix(kb): b" {
		t.Fatalf("main holds %q\n%s", got, out)
	}
	if n := strings.Count(out, "is still running and nothing else is ready: gated alone"); n != 2 {
		t.Errorf("each change alone while its pre-check runs: %d\n%s", n, out)
	}
	// Past LAND_PRECHECK_WAIT a running pre-check is no verdict: the changes travel together.
	e2 := newLandEnv(t)
	e2.queue("a", "fix(kb): a\n", "2026-01-01T10:00:00Z", map[string]string{"a2.txt": "a\n"})
	e2.queue("b", "fix(kb): b\n", "2026-01-01T11:00:00Z", map[string]string{"b.txt": "b\n"})
	running = e2.upGit("rev-parse", "ready/a") + " " + e2.upGit("rev-parse", "ready/b")
	if out, ok := e2.land("FAKE_PRECHECK_RUNNING=" + running); !ok {
		t.Fatalf("land failed\n%s", out)
	}
	if n := strings.Count(e2.makeLog(), "-k gate"); n != 1 {
		t.Errorf("a pre-check running past the wait does not split the train: %d gates\n%s", n, e2.makeLog())
	}
}

// A change that breaks the gate is found by bisection and rejected with the
// reason; the green prefix lands, the rest lands in the next train.
// A pending decision is numbered above the newest issue or PR too, so it never takes
// the number a contributor's PR, and its decision, will have.
func TestLanderNumbersDecisionsAboveTheIssueCounter(t *testing.T) {
	e := newLandEnv(t)
	dec := "docs/" + "decisions/"
	e.queue("one", "docs: one\n", "2026-01-01T10:00:00Z", map[string]string{
		dec + "D-new-one.md": "---\ntopic: x\n---\n\n# D-new-one — One\n",
	})
	if out, ok := e.land("FAKE_GH_LAST=40"); !ok {
		t.Fatalf("land failed\n%s", out)
	}
	if got := e.upGit("show", "main:"+dec+"D41-one.md"); !strings.Contains(got, "# D41 — One") {
		t.Fatalf("the decision is not D41 above issue #40:\n%s", got)
	}
}

func TestLanderRejectsTheChangeThatBreaksTheTrain(t *testing.T) {
	e := newLandEnv(t)
	e.queue("a", "fix(kb): a\n", "2026-01-01T10:00:00Z", map[string]string{"a2.txt": "a\n"})
	e.queue("b", "fix(kb): b\n", "2026-01-01T11:00:00Z", map[string]string{"BAD": "x\n"})
	e.queue("c", "fix(kb): c\n", "2026-01-01T12:00:00Z", map[string]string{"c.txt": "c\n"})
	out, ok := e.land()
	if !ok {
		t.Fatalf("land failed\n%s", out)
	}
	if got := strings.Join(e.mainSubjects(), " | "); got != "fix(kb): a | fix(kb): c" {
		t.Fatalf("main holds %q, want a and c\n%s", got, out)
	}
	if e.refs("failed/") != "b" || e.refs("ready/") != "" {
		t.Fatalf("b must be rejected and the queue empty: failed=%q ready=%q\n%s", e.refs("failed/"), e.refs("ready/"), out)
	}
	if reason := e.upGit("log", "-1", "--format=%s", "failed/b"); !strings.HasPrefix(reason, "lander: the train gate is red with it") {
		t.Errorf("the failed ref carries the reason, got %q", reason)
	}
}

// A failure the signoff confirmed on named Go tests is bisected with those tests' packages
// only (signoff.sh --probe), not a full gate per prefix; the green prefix still gets one full
// gate before it lands, and the changes after the rejected one land in the next train.
func TestLanderBisectsWithTheConfirmedTestsOnly(t *testing.T) {
	e := newLandEnv(t)
	e.queue("a", "fix(kb): a\n", "2026-01-01T10:00:00Z", map[string]string{"a2.txt": "a\n"})
	e.queue("b", "fix(kb): b\n", "2026-01-01T11:00:00Z", map[string]string{"CONFIRMED": "x\n"})
	e.queue("c", "fix(kb): c\n", "2026-01-01T12:00:00Z", map[string]string{"c.txt": "c\n"})
	e.queue("d", "fix(kb): d\n", "2026-01-01T13:00:00Z", map[string]string{"d.txt": "d\n"})
	out, ok := e.land()
	if !ok {
		t.Fatalf("land failed\n%s", out)
	}
	if got := strings.Join(e.mainSubjects(), " | "); got != "fix(kb): a | fix(kb): c | fix(kb): d" {
		t.Fatalf("main holds %q, want a, then c and d\n%s", got, out)
	}
	if e.refs("failed/") != "b" {
		t.Fatalf("b must be rejected: failed=%q\n%s", e.refs("failed/"), out)
	}
	if !strings.Contains(out, "bisecting with the confirmed tests only: linux github.com/x/p/internal/kb -") {
		t.Errorf("the bisection must use the confirmed packages:\n%s", out)
	}
	mk := e.makeLog()
	// The red train, the full gate of the green prefix [a], the train of c and d.
	if n := strings.Count(mk, "-k gate"); n != 3 {
		t.Errorf("three full gates, none per bisection step: %d\n%s", n, mk)
	}
	// The signoff's own retry, then one probe per bisection step (prefixes [a b] and [a]).
	if n := strings.Count(mk, "test-rerun RERUN_PKGS=github.com/x/p/internal/kb"); n != 3 {
		t.Errorf("the retry and two probes: %d\n%s", n, mk)
	}
}

// Red once and green on the re-run is a flaky test: the train lands and a bug
// issue names the test.
func TestLanderLandsOnAFlakyRerunAndOpensAnIssue(t *testing.T) {
	e := newLandEnv(t)
	e.queue("f", "fix(kb): f\n", "2026-01-01T10:00:00Z", map[string]string{"FLAKY": "x\n"})
	out, ok := e.land()
	if !ok {
		t.Fatalf("land failed\n%s", out)
	}
	if got := strings.Join(e.mainSubjects(), " | "); got != "fix(kb): f" {
		t.Fatalf("main holds %q\n%s", got, out)
	}
	if gh := e.log(); !strings.Contains(gh, "issue create -R up/proj --label bug --title flaky: TestSometimes") {
		t.Errorf("a flaky issue must be opened:\n%s", gh)
	}
}

// A named Go test that fails and passes on the signoff's targeted retry lands the
// train on its first gate, with a bug naming the test (D-new-targeted-retry).
func TestLanderLandsOnATargetedRetryAndOpensAnIssue(t *testing.T) {
	e := newLandEnv(t)
	e.queue("f", "fix(kb): f\n", "2026-01-01T10:00:00Z", map[string]string{"FLAKYGO": "x\n"})
	out, ok := e.land()
	if !ok {
		t.Fatalf("land failed\n%s", out)
	}
	if got := strings.Join(e.mainSubjects(), " | "); got != "fix(kb): f" {
		t.Fatalf("main holds %q\n%s", got, out)
	}
	if runs := strings.Count(e.makeLog(), "-k gate"); runs != 1 {
		t.Errorf("one gate, its retry inside the signoff: %d gates\n%s", runs, e.makeLog())
	}
	if gh := e.log(); !strings.Contains(gh, "--title flaky: kb.TestNamed failed and passed on a targeted retry") {
		t.Errorf("a flaky issue must name the test:\n%s", gh)
	}
}

// A named Go test that fails again on the targeted retry is no flake: the lander
// rejects the change without re-running the whole gate.
func TestLanderSkipsTheRerunWhenTheRetryConfirms(t *testing.T) {
	e := newLandEnv(t)
	e.queue("c", "fix(kb): c\n", "2026-01-01T10:00:00Z", map[string]string{"CONFIRMED": "x\n"})
	out, ok := e.land()
	if !ok {
		t.Fatalf("land failed\n%s", out)
	}
	if e.refs("failed/") != "c" {
		t.Fatalf("c must be rejected: failed=%q\n%s", e.refs("failed/"), out)
	}
	if runs := strings.Count(e.makeLog(), "-k gate"); runs != 1 {
		t.Errorf("a confirmed failure is not gated again: %d gates\n%s", runs, e.makeLog())
	}
	if strings.Contains(e.log(), "issue create") {
		t.Errorf("a confirmed failure opens no flaky issue:\n%s", e.log())
	}
}

// A held change, and a feat while the freeze is open, stay queued; a fix lands.
func TestLanderKeepsHeldAndFrozenChangesQueued(t *testing.T) {
	e := newLandEnv(t)
	e.queue("held", "fix(kb): held\n\nLander-Hold: yes\n", "2026-01-01T10:00:00Z", map[string]string{"h.txt": "h\n"})
	e.queue("feature", "feat(kb): feature\n", "2026-01-01T11:00:00Z", map[string]string{"f.txt": "f\n"})
	e.queue("fixed", "fix(kb): fixed\n", "2026-01-01T12:00:00Z", map[string]string{"x.txt": "x\n"})
	out, ok := e.land("FAKE_FREEZE=0")
	if !ok {
		t.Fatalf("land failed\n%s", out)
	}
	if got := strings.Join(e.mainSubjects(), " | "); got != "fix(kb): fixed" {
		t.Fatalf("main holds %q, want only the fix\n%s", got, out)
	}
	if got := strings.Fields(e.refs("ready/")); strings.Join(got, " ") != "feature held" {
		t.Errorf("the held and the frozen change stay queued, got %v", got)
	}
}

// A change that no longer applies on main is rejected as a conflict, and a change
// that cites a pending decision nobody lands is rejected too.
func TestLanderRejectsConflictsAndUnknownPlaceholders(t *testing.T) {
	e := newLandEnv(t)
	e.queue("stale", "fix(kb): stale\n", "2026-01-01T10:00:00Z", map[string]string{"a.txt": "mine\n"})
	e.queue("orphan", "fix(kb): orphan\n", "2026-01-01T11:00:00Z", map[string]string{"o.txt": "see D-new-nobody\n"})
	e.queue("ok", "fix(kb): ok\n", "2026-01-01T12:00:00Z", map[string]string{"ok.txt": "ok\n"})
	// main moves a.txt after "stale" was queued.
	e.git("checkout", "-q", "-B", "move", "origin/main")
	e.write(filepath.Join(e.work, "a.txt"), "theirs\n")
	e.git("commit", "-q", "-am", "fix: move a")
	e.git("push", "-q", "origin", "move:main")
	e.git("checkout", "-q", "main")
	out, ok := e.land()
	if !ok {
		t.Fatalf("land failed\n%s", out)
	}
	if got := strings.Join(e.mainSubjects(), " | "); got != "fix: move a | fix(kb): ok" {
		t.Fatalf("main holds %q\n%s", got, out)
	}
	if got := strings.Fields(e.refs("failed/")); strings.Join(got, " ") != "orphan stale" {
		t.Fatalf("stale and orphan must be rejected, got %v\n%s", got, out)
	}
	if r := e.upGit("log", "-1", "--format=%s", "failed/stale"); !strings.HasPrefix(r, "lander: conflict") {
		t.Errorf("the reason of stale is the conflict, got %q", r)
	}
	if r := e.upGit("log", "-1", "--format=%s", "failed/orphan"); !strings.Contains(r, "D-new-nobody") {
		t.Errorf("the reason of orphan names the placeholder, got %q", r)
	}
}

// A queued change whose diff main already holds (its train landed, the queue entry
// outlived it) leaves the queue without a failed/ ref, and the train goes on.
func TestLanderDequeuesAChangeAlreadyOnMain(t *testing.T) {
	e := newLandEnv(t)
	e.queue("dup", "fix(kb): dup\n", "2026-01-01T10:00:00Z", map[string]string{"dup.txt": "dup\n"})
	e.queue("ok", "fix(kb): ok\n", "2026-01-01T11:00:00Z", map[string]string{"ok.txt": "ok\n"})
	e.landByHand("dup")
	out, ok := e.land()
	if !ok {
		t.Fatalf("land failed\n%s", out)
	}
	if !strings.Contains(out, "dup is already on main: dequeued") {
		t.Errorf("the lander must say why dup left the queue\n%s", out)
	}
	if got := strings.Join(e.mainSubjects(), " | "); got != "fix(kb): dup | fix(kb): ok" {
		t.Fatalf("main holds %q\n%s", got, out)
	}
	if q, f := e.refs("ready/"), e.refs("failed/"); q != "" || f != "" {
		t.Fatalf("queue %q and failed %q must be empty\n%s", q, f, out)
	}
}

// landByHand puts the diff of ready/<slug> on main, leaving the queue entry behind.
func (e *landEnv) landByHand(slug string) {
	e.t.Helper()
	e.git("fetch", "-q", "origin")
	e.git("checkout", "-q", "-B", "landed", "origin/main")
	e.git("cherry-pick", "origin/ready/"+slug)
	e.git("push", "-q", "origin", "landed:main")
	e.git("checkout", "-q", "main")
}

// A dry run whose train has nothing that applies ends, and leaves the queue as it was
// (it moves nothing out of it, so composing again would find the same train forever).
func TestLanderDryRunEndsWhenNothingApplies(t *testing.T) {
	e := newLandEnv(t)
	e.queue("dup", "fix(kb): dup\n", "2026-01-01T10:00:00Z", map[string]string{"dup.txt": "dup\n"})
	e.landByHand("dup")
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", filepath.Join(e.work, "scripts", "land.sh"), "--dry-run")
	cmd.Dir, cmd.Env, cmd.WaitDelay = e.work, e.env, 5*time.Second
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("the dry run did not end\n%s", out)
	}
	if err != nil || !strings.Contains(string(out), "no queued change applies") {
		t.Fatalf("dry run: %v\n%s", err, out)
	}
	if q := e.refs("ready/"); q != "dup" {
		t.Fatalf("a dry run leaves the queue alone, got %q\n%s", q, out)
	}
}

// After the merge, a deletion of a queue entry that fails for a reason that is not the
// lease (the network after a wake, here a hook refusing it) is retried; still failing,
// the lander stops instead of leaving a landed change in the queue for the next train.
func TestLanderRetriesTheQueueDeletionAfterTheMerge(t *testing.T) {
	for _, tc := range []struct {
		deny   string
		landed bool
	}{{"2", true}, {"99", false}} {
		t.Run("deny="+tc.deny, func(t *testing.T) {
			e := newLandEnv(t)
			e.write(filepath.Join(e.up, "hooks", "pre-receive"), `#!/bin/sh
while read old new ref; do
  case "$ref:$new" in refs/heads/ready/*:0000000000000000000000000000000000000000)
    n=$(cat "$FAKE_STATE.denied" 2>/dev/null || echo 0)
    if [ "$n" -lt "$FAKE_DENY" ]; then echo $((n + 1)) > "$FAKE_STATE.denied"; exit 1; fi ;;
  esac
done
`)
			e.queue("one", "fix(kb): one\n", "2026-01-01T10:00:00Z", map[string]string{"one.txt": "one\n"})
			out, ok := e.land("FAKE_DENY=" + tc.deny)
			if got := strings.Join(e.mainSubjects(), " | "); got != "fix(kb): one" {
				t.Fatalf("main holds %q\n%s", got, out)
			}
			if f := e.refs("failed/"); f != "" {
				t.Fatalf("nothing may be rejected, got %q\n%s", f, out)
			}
			q := e.refs("ready/")
			if tc.landed {
				if !ok || q != "" {
					t.Fatalf("two refusals then the deletion: ok=%v queue=%q\n%s", ok, q, out)
				}
				return
			}
			if ok || q != "one" || !strings.Contains(out, "could not be deleted") {
				t.Fatalf("the lander must stop and leave ready/one: ok=%v queue=%q\n%s", ok, q, out)
			}
		})
	}
}

// make ready squashes the branch into one commit on its merge-base, with the
// message and the trailers, and refuses a title pr-title would refuse.
func TestReadyQueuesOneCommitWithTheMessage(t *testing.T) {
	e := newLandEnv(t)
	e.git("checkout", "-q", "-b", "feat/my-change")
	for _, f := range []string{"x1.txt", "x2.txt"} {
		e.write(filepath.Join(e.work, f), f+"\n")
		e.git("add", f)
		e.git("commit", "-q", "-m", "wip "+f)
	}
	ready := func(msg string, extra ...string) (string, bool) {
		m := filepath.Join(e.tmp, "msg")
		e.write(m, msg)
		cmd := exec.Command("bash", filepath.Join(e.work, "scripts", "ready.sh"))
		cmd.Dir = e.work
		cmd.Env = append(append([]string{}, e.env...), append([]string{"MSG=" + m, "READY_NO_LANDER=1"}, extra...)...)
		out, err := cmd.CombinedOutput()
		return string(out), err == nil
	}
	if out, ok := ready("Feat: not conventional\n"); ok || e.refs("ready/") != "" {
		t.Fatalf("a bad title must be refused and nothing queued: ok=%v\n%s", ok, out)
	}
	out, ok := ready("feat(client): my change\n\nCloses #7\n", "AFTER=other", "REVIEW=light, 1 rounds")
	if !ok {
		t.Fatalf("ready failed\n%s", out)
	}
	if e.refs("ready/") != "my-change" {
		t.Fatalf("the change must be queued as ready/my-change, got %q", e.refs("ready/"))
	}
	base := e.upGit("rev-parse", "main")
	if parent := e.upGit("rev-parse", "ready/my-change^"); parent != base {
		t.Errorf("one commit on the merge-base: parent %s, want %s", parent, base)
	}
	msg := e.upGit("log", "-1", "--format=%B", "ready/my-change")
	if want := "Lander-Tip: " + e.git("rev-parse", "HEAD"); !strings.Contains(msg, want) {
		t.Errorf("the queued commit must record the branch head squashed (%s):\n%s", want, msg)
	}
	for _, want := range []string{"feat(client): my change", "Closes #7", "Lander-After: other", "Review: light, 1 rounds"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the queued message lacks %q:\n%s", want, msg)
		}
	}
	if files := e.upGit("show", "--name-only", "--format=", "ready/my-change"); files != "x1.txt\nx2.txt" {
		t.Errorf("the squashed commit holds both files, got %q", files)
	}

	// A change built on the queued one carries only its own diff, as its child, and the
	// two land in one train in that order.
	if out, ok := ready("fix(kb): my change\n"); !ok {
		t.Fatalf("queuing again failed\n%s", out)
	}
	e.git("checkout", "-q", "-b", "feat/next")
	e.write(filepath.Join(e.work, "x3.txt"), "x3\n")
	e.git("add", "x3.txt")
	e.git("commit", "-q", "-m", "wip x3")
	if out, ok := ready("fix(kb): next\n", "AFTER=my-change"); !ok {
		t.Fatalf("ready AFTER failed\n%s", out)
	}
	if parent, want := e.upGit("rev-parse", "ready/next^"), e.upGit("rev-parse", "ready/my-change"); parent != want {
		t.Errorf("the dependent change must be a child of ready/my-change: parent %s, want %s", parent, want)
	}
	if files := e.upGit("show", "--name-only", "--format=", "ready/next"); files != "x3.txt" {
		t.Errorf("the dependent change holds its own file only, got %q", files)
	}
	e.git("checkout", "-q", "main")
	if out, ok := e.land(); !ok {
		t.Fatalf("land failed\n%s", out)
	}
	if got := strings.Join(e.mainSubjects(), " | "); got != "fix(kb): my change | fix(kb): next" {
		t.Fatalf("main holds %q", got)
	}
}

func (e *landEnv) log() string { b, _ := os.ReadFile(e.ghLog); return string(b) }

func readTmp(t *testing.T, dir, name string) string {
	t.Helper()
	b, _ := os.ReadFile(filepath.Join(dir, name))
	return string(b)
}

// makeLog is what the fake make was asked, one invocation per line.
func (e *landEnv) makeLog() string {
	b, _ := os.ReadFile(filepath.Join(e.tmp, "gh-state.make"))
	return string(b)
}

// A red gate log names two kinds of failure: the linux leg's own lines (a vitest flake)
// and the `signoff: confirmed on a targeted retry:` line of the Windows leg. The reason
// of a rejection names the confirmed tests first, each once, then the rest (#1794).
func TestLanderNamesTheConfirmedTestsFirstInTheRejection(t *testing.T) {
	needBash(t)
	root := repoRoot(t)
	script := readFile(t, root, "scripts/land.sh")
	start := strings.Index(script, "\nfailing() {")
	end := strings.Index(script[start+1:], "\n}\n")
	if start < 0 || end < 0 {
		t.Fatal("failing() not found in scripts/land.sh")
	}
	fn := script[start : start+1+end+3]
	logFile := filepath.Join(t.TempDir(), "gate.log")
	body := " FAIL  src/test/releases.test.tsx > shows the releases\n" +
		"--- FAIL: TestLinuxOnly (0.01s)\n" +
		"--- FAIL: TestResolveProviderDocumentsInstructions (0.01s)\n" +
		"signoff: confirmed on a targeted retry: plugin.TestResolveProviderDocumentsInstructions plugin.TestOther\n"
	if err := os.WriteFile(logFile, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", "-c", fn+"\nfailing \"$1\"", "bash", logFile)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("failing: %v\n%s", err, out)
	}
	want := "plugin.TestResolveProviderDocumentsInstructions plugin.TestOther TestLinuxOnly src/test/releases.test.tsx"
	if got := strings.TrimSpace(string(out)); got != want {
		t.Errorf("failing() = %q, want %q", got, want)
	}
}

// A required check that fails right after the train PR is opened (a 5xx reading its diff) is
// rerun once, and the lander merges when the rerun is green.
func TestLanderRerunsAFailedRequiredCheckOnceThenMerges(t *testing.T) {
	e := newLandEnv(t)
	e.queue("a", "fix(kb): a\n", "2026-01-01T10:00:00Z", map[string]string{"a2.txt": "a\n"})
	out, ok := e.land("FAKE_CHECKS_RED=once", "LAND_MERGE_TRIES=6")
	if !ok {
		t.Fatalf("land failed\n%s", out)
	}
	if !strings.Contains(out, "rerunning the failed checks of train PR #42 once") {
		t.Errorf("the lander must say it reruns:\n%s", out)
	}
	gh := e.log()
	if n := strings.Count(gh, "run rerun 7 -R up/proj --failed"); n != 1 {
		t.Errorf("one rerun of the failed run: %d\n%s", n, gh)
	}
	if got := strings.Join(e.mainSubjects(), " | "); got != "fix(kb): a" || e.refs("ready/") != "" {
		t.Errorf("main holds %q, queue %q\n%s", got, e.refs("ready/"), out)
	}
}

// Red again after the rerun: the lander stops, the queue untouched, and reruns only once.
func TestLanderStopsWhenARequiredCheckIsRedAfterTheRerun(t *testing.T) {
	e := newLandEnv(t)
	e.queue("a", "fix(kb): a\n", "2026-01-01T10:00:00Z", map[string]string{"a2.txt": "a\n"})
	out, ok := e.land("FAKE_CHECKS_RED=always", "LAND_MERGE_TRIES=6")
	if ok || !strings.Contains(out, "failed again after a rerun") {
		t.Fatalf("the lander must stop on a second red: ok=%v\n%s", ok, out)
	}
	if n := strings.Count(e.log(), "run rerun"); n != 1 {
		t.Errorf("one rerun, not %d\n%s", n, e.log())
	}
	if q := e.refs("ready/"); q != "a" {
		t.Errorf("the queue is untouched, got %q", q)
	}
}

// trainBranch pushes train/<name> to origin: commits on top of base (default origin/main), each
// with a Lander-Slug and Lander-Tip trailer; returns its head. It also records the PR the fake
// gh shows for it (open, number 42) with FAKE_GH_TRAINS.
func (e *landEnv) trainBranch(name, base string, slugs ...string) (head, trains string) {
	e.t.Helper()
	e.git("fetch", "-q", "origin")
	e.git("checkout", "-q", "-B", "tr-"+name, base)
	for _, s := range slugs {
		e.write(filepath.Join(e.work, "t-"+s+".txt"), s+"\n")
		e.git("add", ".")
		e.git("commit", "-q", "-m", "fix(kb): "+s+"\n\nLander-Slug: "+s+"\nLander-Tip: tip-"+s+"\n")
	}
	head = e.git("rev-parse", "HEAD")
	e.git("push", "-q", "--force", "origin", "HEAD:refs/heads/train/"+name)
	e.git("checkout", "-q", "main")
	e.write(e.tmp+"/gh-state.head", "train/"+name+"\n")
	e.write(e.tmp+"/gh-state.pr", "OPEN\n")
	return head, `[{"number":42,"headRefName":"train/` + name + `","headRefOid":"` + head + `","isCrossRepository":false,"baseRefName":"main"}]`
}

// An open train PR built on the current main that this lander did not open is adopted:
// merged with the same rule, its changes leave the queue (their Lander-Slug is on main), and
// the lander goes on with the rest.
func TestLanderAdoptsAnOpenTrainBuiltOnMain(t *testing.T) {
	e := newLandEnv(t)
	e.queue("a", "fix(kb): a\n\nLander-Tip: tip-a\n", "2026-01-01T10:00:00Z", map[string]string{"qa.txt": "a\n"})
	e.queue("b", "fix(kb): b\n", "2026-01-01T11:00:00Z", map[string]string{"qb.txt": "b\n"})
	_, trains := e.trainBranch("20260101-000000", "origin/main", "a")
	out, ok := e.land("FAKE_GH_TRAINS=" + trains)
	if !ok {
		t.Fatalf("land failed\n%s", out)
	}
	if !strings.Contains(out, "adopting the open train PR #42") {
		t.Errorf("the lander must say it adopts:\n%s", out)
	}
	if got := strings.Join(e.mainSubjects(), " | "); got != "fix(kb): a | fix(kb): b" {
		t.Fatalf("main holds %q: a once (the adopted train), then b\n%s", got, out)
	}
	if !strings.Contains(e.log(), "pr merge 42 -R up/proj --rebase --match-head-commit") {
		t.Errorf("the adopted train merges on its head:\n%s", e.log())
	}
	if q := e.refs("ready/"); q != "" {
		t.Errorf("queue %q must be empty\n%s", q, out)
	}
}

// A train PR built on a main that moved is closed, and its changes are composed again.
func TestLanderClosesAnOpenTrainBuiltOnAnOlderMain(t *testing.T) {
	e := newLandEnv(t)
	e.queue("a", "fix(kb): a\n", "2026-01-01T10:00:00Z", map[string]string{"qa.txt": "a\n"})
	_, trains := e.trainBranch("20260101-000000", "origin/main", "old")
	e.git("checkout", "-q", "-B", "other", "origin/main")
	e.write(filepath.Join(e.work, "other.txt"), "o\n")
	e.git("add", ".")
	e.git("commit", "-q", "-m", "chore: other")
	e.git("push", "-q", "origin", "other:main")
	e.git("checkout", "-q", "main")
	out, ok := e.land("FAKE_GH_TRAINS=" + trains)
	if !ok {
		t.Fatalf("land failed\n%s", out)
	}
	if !strings.Contains(e.log(), "pr close 42 -R up/proj --delete-branch") {
		t.Errorf("the stale train PR must be closed:\n%s", e.log())
	}
	if got := strings.Join(e.mainSubjects(), " | "); got != "chore: other | fix(kb): a" {
		t.Fatalf("main holds %q: the old train never lands\n%s", got, out)
	}
}

// A train PR from a fork is never adopted; the wait has a deadline.
func TestLanderStopsWaitingOnATrainPRItCannotAdopt(t *testing.T) {
	e := newLandEnv(t)
	e.queue("a", "fix(kb): a\n", "2026-01-01T10:00:00Z", map[string]string{"qa.txt": "a\n"})
	head, _ := e.trainBranch("fork", "origin/main", "x")
	out, ok := e.land(`FAKE_GH_TRAINS=[{"number":42,"headRefName":"train/fork","headRefOid":"`+head+`","isCrossRepository":true,"baseRefName":"main"}]`, "LAND_OPEN_TRIES=2")
	if ok || !strings.Contains(out, "is still open after the wait") {
		t.Fatalf("the lander must stop with a reason: ok=%v\n%s", ok, out)
	}
	if strings.Contains(e.log(), "pr merge") || strings.Contains(e.log(), "pr close") {
		t.Errorf("a fork's PR is left alone:\n%s", e.log())
	}
}

// A change whose commit is on main with its Lander-Slug and Lander-Tip is dequeued even when
// the patch differs there (the decision was numbered on its landing): it is not landed twice.
func TestLanderDequeuesAChangeByItsLanderSlugOnMain(t *testing.T) {
	e := newLandEnv(t)
	msg := "fix(kb): dup\n\nLander-Tip: tip-dup\n"
	e.queue("dup", msg, "2026-01-01T10:00:00Z", map[string]string{"dup.txt": "dup\n", "docs" + "/decisions/D-new-dup.md": "---\ntopic: x\n---\n\n# D-new-dup\n"})
	e.queue("ok", "fix(kb): ok\n", "2026-01-01T11:00:00Z", map[string]string{"ok.txt": "ok\n"})
	e.git("fetch", "-q", "origin")
	e.git("checkout", "-q", "-B", "landed", "origin/main")
	e.write(filepath.Join(e.work, "dup.txt"), "dup\n")
	e.write(filepath.Join(e.work, "docs", "decisions", "D11-dup.md"), "---\ntopic: x\n---\n\n# D11\n")
	e.git("add", ".")
	e.git("commit", "-q", "-m", "fix(kb): dup\n\nLander-Slug: dup\nLander-Tip: tip-dup\n")
	e.git("push", "-q", "origin", "landed:main")
	e.git("checkout", "-q", "main")
	out, ok := e.land()
	if !ok {
		t.Fatalf("land failed\n%s", out)
	}
	if !strings.Contains(out, "dup is already on main") {
		t.Errorf("the lander must say why dup left the queue\n%s", out)
	}
	if got := strings.Join(e.mainSubjects(), " | "); got != "fix(kb): dup | fix(kb): ok" {
		t.Fatalf("main holds %q: dup once\n%s", got, out)
	}
	if ls := e.upGit("ls-tree", "--name-only", "main", "docs/decisions/"); strings.Contains(ls, "D-new") || strings.Count(ls, "-dup.md") != 1 {
		t.Errorf("the decision exists once, numbered:\n%s", ls)
	}
	if q, f := e.refs("ready/"), e.refs("failed/"); q != "" || f != "" {
		t.Fatalf("queue %q and failed %q must be empty\n%s", q, f, out)
	}
}

// The failed job of a run still in progress is red in the checks before the run is listed as
// failed: nothing can be rerun yet, so the one rerun is not spent and the next poll looks again.
func TestLanderKeepsTheRerunWhenNoFailedRunIsCompleteYet(t *testing.T) {
	e := newLandEnv(t)
	e.queue("a", "fix(kb): a\n", "2026-01-01T10:00:00Z", map[string]string{"a2.txt": "a\n"})
	out, ok := e.land("FAKE_CHECKS_RED=once", "FAKE_FAILED_RUNS_AFTER=2", "LAND_MERGE_TRIES=10")
	if !ok {
		t.Fatalf("land failed\n%s", out)
	}
	if n := strings.Count(e.log(), "run rerun"); n != 1 {
		t.Errorf("one rerun, once the run is listed: %d\n%s", n, e.log())
	}
	if n := strings.Count(out, "rerunning the failed checks"); n != 1 {
		t.Errorf("the lander says it reruns once, not before a run was rerun: %d\n%s", n, out)
	}
}

// A train PR whose base is not main is neither adopted, merged nor closed: it is waited on
// until the deadline, then reported.
func TestLanderLeavesATrainPRWithAnotherBaseAlone(t *testing.T) {
	e := newLandEnv(t)
	e.queue("a", "fix(kb): a\n", "2026-01-01T10:00:00Z", map[string]string{"qa.txt": "a\n"})
	head, _ := e.trainBranch("other-base", "origin/main", "x")
	out, ok := e.land(`FAKE_GH_TRAINS=[{"number":42,"headRefName":"train/other-base","headRefOid":"`+head+`","isCrossRepository":false,"baseRefName":"release"}]`, "LAND_OPEN_TRIES=2")
	if ok || !strings.Contains(out, "is still open after the wait") {
		t.Fatalf("the lander must stop with a reason: ok=%v\n%s", ok, out)
	}
	if gh := e.log(); strings.Contains(gh, "pr merge") || strings.Contains(gh, "pr close") {
		t.Errorf("a PR on another base is left alone:\n%s", gh)
	}
}

// A stale train PR that cannot be closed is waited on against the deadline, not retried in a spin.
func TestLanderDoesNotSpinOnATrainPRItCannotClose(t *testing.T) {
	e := newLandEnv(t)
	e.queue("a", "fix(kb): a\n", "2026-01-01T10:00:00Z", map[string]string{"qa.txt": "a\n"})
	_, trains := e.trainBranch("stale", "origin/main", "old")
	e.git("checkout", "-q", "-B", "other", "origin/main")
	e.write(filepath.Join(e.work, "other.txt"), "o\n")
	e.git("add", ".")
	e.git("commit", "-q", "-m", "chore: other")
	e.git("push", "-q", "origin", "other:main")
	e.git("checkout", "-q", "main")
	out, ok := e.land("FAKE_GH_TRAINS="+trains, "FAKE_CLOSE_FAILS=1", "LAND_OPEN_TRIES=2")
	if ok || !strings.Contains(out, "is still open after the wait") {
		t.Fatalf("the lander must stop at the deadline: ok=%v\n%s", ok, out)
	}
}

// The same slug queued again with another Lander-Tip is a new version: main holding the old
// one does not dequeue it.
func TestLanderDoesNotDequeueANewVersionOfALandedSlug(t *testing.T) {
	e := newLandEnv(t)
	e.queue("dup", "fix(kb): dup again\n\nLander-Tip: tip-new\n", "2026-01-01T10:00:00Z", map[string]string{"dup2.txt": "dup\n"})
	e.git("fetch", "-q", "origin")
	e.git("checkout", "-q", "-B", "landed", "origin/main")
	e.write(filepath.Join(e.work, "dup.txt"), "dup\n")
	e.git("add", ".")
	e.git("commit", "-q", "-m", "fix(kb): dup\n\nLander-Slug: dup\nLander-Tip: tip-old\n")
	e.git("push", "-q", "origin", "landed:main")
	e.git("checkout", "-q", "main")
	out, ok := e.land()
	if !ok {
		t.Fatalf("land failed\n%s", out)
	}
	if got := strings.Join(e.mainSubjects(), " | "); got != "fix(kb): dup | fix(kb): dup again" {
		t.Fatalf("main holds %q: the new version lands\n%s", got, out)
	}
	if strings.Contains(out, "already on main") {
		t.Errorf("a new version is not on main:\n%s", out)
	}
}
