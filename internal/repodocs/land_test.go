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
[ -e BAD ] && exit 2
if [ -e FLAKY ] && [ ! -e "$FAKE_STATE.flaked" ]; then touch "$FAKE_STATE.flaked"; echo "--- FAIL: TestSometimes"; exit 2; fi
exit 0
`)
	e.write(filepath.Join(e.bin, "gh"), `#!/bin/bash
echo "$*" >> "$FAKE_GH_LOG"
jq_expr=; prev=
for a in "$@"; do [ "$prev" = --jq ] && jq_expr=$a; prev=$a; done
out() { if [ -n "$jq_expr" ]; then jq -r "$jq_expr"; else cat; fi; }
case "$1 $2" in
  "repo view") echo "${FAKE_PERM:-ADMIN}" ;;
  "api user") echo '{"login":"user"}' | out ;;
  "api -X") ;;
  "api repos/up/proj") echo '{"fork":false}' | out ;;
  "api repos/up/proj/issues"*) if [ -n "${FAKE_GH_LAST:-}" ]; then echo "[{\"number\":$FAKE_GH_LAST}]"; else echo '[]'; fi | out ;;
  "run list") echo '[]' | out ;;
  "pr list") echo "${FAKE_GH_TRAINS:-[]}" | out ;;
  "pr create")
    head=; prev=; for a in "$@"; do [ "$prev" = --head ] && head=$a; prev=$a; done
    echo "$head" > "$FAKE_STATE.head"; echo OPEN > "$FAKE_STATE.pr"
    echo "https://github.com/up/proj/pull/42" ;;
  "pr merge")
    git --git-dir="$FAKE_UP" update-ref refs/heads/main "refs/heads/$(cat "$FAKE_STATE.head")"
    echo MERGED > "$FAKE_STATE.pr" ;;
  "pr view") printf '{"state":"%s"}\n' "$(cat "$FAKE_STATE.pr" 2>/dev/null || echo OPEN)" | out ;;
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
	e.queue("one", "feat(client): one\n\nCloses #5\n", "2026-01-01T11:00:00Z", map[string]string{
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
	if msgs := e.upGit("log", "--format=%B", "main"); strings.Contains(msgs, "Lander-") || strings.Contains(msgs, "Review:") {
		t.Errorf("the lander's trailers must not reach main:\n%s", msgs)
	}
	if e.refs("ready/") != "" || e.refs("train/") != "" {
		t.Errorf("the queue and the train branch must be gone: ready=%q train=%q", e.refs("ready/"), e.refs("train/"))
	}
	gh := e.log()
	for _, want := range []string{"pr create -R up/proj --base main --head train/", "--title chore(dev): train ", "2 change(s)", "pr merge 42 -R up/proj --rebase --auto", "statuses/"} {
		if !strings.Contains(gh, want) {
			t.Errorf("gh was not asked %q:\n%s", want, gh)
		}
	}
	if n := strings.Count(readTmp(t, e.tmp, "gh-state.make"), "gate"); n != 1 {
		t.Errorf("one gate for the whole train, got %d", n)
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
