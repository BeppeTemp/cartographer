package landertest

import (
	"context"
	"errors"
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
		// A hosted gate (land.sh without --gate host): every job of the dispatched ci.yml run green.
		"FAKE_GH_JOBS="+hostedRun("completed", "completed success", nil),
		"LAND_STATE="+filepath.Join(tmp, "lander"), "LAND_POLL=0", "LAND_WINDOW=0", "LAND_INFRA_WAIT=0", "LAND_MERGE_TRIES=3",
		// The scripts of this clone, not origin/main's (the tests of that path set LAND_SELF=).
		"LAND_SELF=1",
		"PATH="+e.bin+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	e.write(filepath.Join(e.bin, "fakemake"), `#!/bin/sh
case "$*" in *signoff-platform*) echo linux/amd64; exit 0 ;; esac
echo "$*" >> "$FAKE_STATE.make"
# The connection to the build host drops: HOSTDOWN every time, HOSTDOWN1 the first gate only.
[ -e HOSTDOWN ] && { echo "client_loop: send disconnect: Broken pipe"; exit 255; }
if [ -e HOSTDOWN1 ] && [ ! -e "$FAKE_STATE.hostdown" ]; then
  touch "$FAKE_STATE.hostdown"; echo "client_loop: send disconnect: Broken pipe"; exit 255
fi
# FAKE_PROBE255: the second targeted run (the first is the signoff's retry, the next the
# bisection's first probe) loses the connection, as a probe of a bisection can.
case "$*" in test-rerun*)
  # RETRY75: the signoff's targeted retry of the first gate cannot run (the host was lost since).
  if [ -e RETRY75 ] && [ ! -e "$FAKE_STATE.retry75" ]; then touch "$FAKE_STATE.retry75"; exit 75; fi
  if [ -n "${FAKE_PROBE255:-}" ]; then
    n=$(cat "$FAKE_STATE.rr" 2>/dev/null || echo 0); echo $((n + 1)) > "$FAKE_STATE.rr"
    [ "$n" = 1 ] && { echo "client_loop: send disconnect: Broken pipe"; exit 255; }
  fi
  [ -e CONFIRMED ] && exit 2; exit 0 ;; esac
case "$*" in web-perf*)
  # WEBPERF75: the host is lost at the last step of the first gate (the deferred web-perf).
  if [ -e WEBPERF75 ] && [ ! -e "$FAKE_STATE.wp75" ]; then touch "$FAKE_STATE.wp75"; exit 75; fi
  [ -e WEBPERF ] && exit 2; exit 0 ;; esac
[ -e BAD ] && exit 2
# A test that prints a well-formed probe line while nothing was confirmed (a panic, no named Go
# test): FORGEPROBE, every gate. The lander must bisect with full gates, not with that line.
if [ -e FORGEPROBE ]; then
  printf 'panic: boom\nFAIL\tgithub.com/x/p/internal/kb\t0.1s\nsignoff: probe: linux github.com/x/p/internal/kb -\n'; exit 2
fi
# Names a test forged to become a mention or a link in the public issue (FORGEDNAME, the first gate only).
if [ -e FORGEDNAME ] && [ ! -e "$FAKE_STATE.forgedname" ]; then
  touch "$FAKE_STATE.forgedname"
  printf ' FAIL  www.example.org > t\n ✘ 1 @user x\n FAIL  ![i](http://example.com/i.png) > t\n'; exit 2
fi
# Another PR merges while the first gate runs (FAKE_MOVE_MAIN): main moves under the train.
if [ -n "${FAKE_MOVE_MAIN:-}" ] && [ ! -e "$FAKE_STATE.moved" ]; then
  touch "$FAKE_STATE.moved"
  c=$(echo "chore: other" | git --git-dir="$FAKE_UP" commit-tree "$(git --git-dir="$FAKE_UP" rev-parse 'main^{tree}')" -p main)
  git --git-dir="$FAKE_UP" update-ref refs/heads/main "$c"
fi
# A test that finds the lander's probe file of this gate and writes a probe of its own there
# (FORGEFILE; FORGEFILE_RO also makes it read-only, so the signoff cannot overwrite it).
if [ -e FORGEFILE ]; then
  for f in "$LAND_STATE"/gate-*.probefile; do
    [ -e "$f" ] || continue
    { echo 'linux github.com/x/p/evil -' > "$f"; [ ! -e FORGEFILE_RO ] || chmod 444 "$f"; } 2>/dev/null || true
  done
fi
if [ -e CONFIRMED ] || { [ -e FLAKYGO ] && [ ! -e "$FAKE_STATE.flakedgo" ]; }; then
  touch "$FAKE_STATE.flakedgo"
  # A test that prints the signoff's line for a lost host, at the start of a line (SPOOF).
  [ -e SPOOF ] && echo "signoff: the build host failed (exit 255) before the gate finished: nothing pushed, nothing posted"
  printf -- '--- FAIL: TestNamed (0.01s)\nFAIL\tgithub.com/x/p/internal/kb\t0.1s\nmake: *** [Makefile:23: test] Error 1\n'
  exit 2
fi
if [ -e FLAKY ] && [ ! -e "$FAKE_STATE.flaked" ]; then touch "$FAKE_STATE.flaked"; echo "--- FAIL: TestSometimes"; exit 2; fi
# The e2e runner's report of two failed scenarios (E2EFLAKY: the first gate only), no Go test named.
if [ -e E2EFLAKY ] && [ ! -e "$FAKE_STATE.e2eflaked" ]; then
  touch "$FAKE_STATE.e2eflaked"
  printf ' E2E Report\n Failed  : 2\nE2E FAILED: 03_config_opencode: [FAIL] file not found: x\nE2E FAILED: 15_operational_audit: [FAIL] no key\n[run] FAIL - 2 scenario(s) failed\nmake: *** [Makefile:188: e2e] Error 1\n'
  exit 2
fi
exit 0
`)
	e.write(filepath.Join(e.bin, "gh"), `#!/bin/bash
echo "$*" >> "$FAKE_GH_LOG"
jq_expr=; prev=
for a in "$@"; do [ "$prev" = --jq ] && jq_expr=$a; prev=$a; done
out() { if [ -n "$jq_expr" ]; then jq -r "$jq_expr"; else cat; fi; }
# The required checks of the train PR: red until the failed jobs are rerun (FAKE_CHECKS_RED=once),
# or whatever is rerun (=always).
red() { [ -z "${FAKE_STARTUP_FAILURE:-}" ] || return 0; case "${FAKE_CHECKS_RED:-}" in always) return 0 ;; once) [ ! -e "$FAKE_STATE.reran" ] ;; *) return 1 ;; esac; }
case "$1 $2" in
  "repo view")
    # FAKE_PERM_FAILS=n: the first n reads fail as a GitHub 5xx does.
    n=$(cat "$FAKE_STATE.pf" 2>/dev/null || echo 0); echo $((n + 1)) > "$FAKE_STATE.pf"
    if [ "$n" -lt "${FAKE_PERM_FAILS:-0}" ]; then echo "HTTP 502" >&2; exit 1; fi
    echo "${FAKE_PERM:-ADMIN}" ;;
  "api user") echo '{"login":"user"}' | out ;;
  "api -X")
    # The status the signoff posts (api -X POST repos/<repo>/statuses/<sha> ... -f description=<d>)
    # is kept, so the read of the train head's statuses answers with it.
    st=; d=; for a in "$@"; do case "$a" in repos/*/statuses/*) st=${a##*/} ;; description=*) d=${a#description=} ;; esac; done
    [ -z "$st" ] || printf '%s' "$d" > "$FAKE_STATE.status-$st" ;;
  "api repos/up/proj/commits/"*)
    # The statuses of a commit, newest first: another context's, then the signoff's.
    [ -z "${FAKE_STATUS_FAILS:-}" ] || { echo "HTTP 502" >&2; exit 1; }
    c=${2#repos/up/proj/commits/}; c=${c%%/*}
    if [ -f "$FAKE_STATE.status-$c" ]; then
      jq -n --arg d "$(cat "$FAKE_STATE.status-$c")" '[{context:"other",state:"success",description:"gate=host forged"},{context:"signoff",state:"success",description:$d}]'
    else echo '[]'; fi | out ;;
  "api repos/up/proj") echo '{"fork":false}' | out ;;
  "api repos/up/proj/issues"*)
    # Another machine pushes train/<FAKE_TRAIN_ON_COMPOSE> while this lander composes (it reads the floor then).
    [ -z "${FAKE_TRAIN_ON_COMPOSE:-}" ] || git --git-dir="$FAKE_UP" update-ref "refs/heads/train/$FAKE_TRAIN_ON_COMPOSE" main
    if [ -n "${FAKE_GH_LAST:-}" ]; then echo "[{\"number\":$FAKE_GH_LAST}]"; else echo '[]'; fi | out ;;
  "run list")
    # The hosted pre-check of a queued commit (ready-check.yml): green unless the commit is
    # named in FAKE_PRECHECK_RED (red) or FAKE_PRECHECK_RUNNING (in progress).
    case "$*" in
      *workflow_dispatch*)
        # The hosted gate's run of ci.yml: none before the first dispatch, then one per dispatch.
        n=$(cat "$FAKE_STATE.dispatch" 2>/dev/null || echo 0)
        if [ "$n" = 0 ]; then echo '[]'; else echo "[{\"databaseId\":$((6 + n))}]"; fi | out ;;
      *ready-check.yml*)
        c=; prev=; for a in "$@"; do [ "$prev" = --commit ] && c=$a; prev=$a; done
        # FAKE_SECOND_REF: a change is queued while the lander reads the first pre-check (once).
        if [ -n "${FAKE_SECOND_REF:-}" ] && [ ! -e "$FAKE_STATE.second" ]; then
          touch "$FAKE_STATE.second"; git --git-dir="$FAKE_UP" update-ref refs/heads/ready/second "$FAKE_SECOND_REF"
        fi
        if [[ " ${FAKE_PRECHECK_RUNNING:-} " == *" $c "* ]]; then echo '[{"status":"in_progress","conclusion":""}]'
        elif [[ " ${FAKE_PRECHECK_RED:-} " == *" $c "* ]]; then echo '[{"databaseId":55,"status":"completed","conclusion":"failure"}]'
        else echo '[{"status":"completed","conclusion":"success"}]'; fi | out ;;
      *--status*failure*)
        # The failed run completes only after FAKE_FAILED_RUNS_AFTER listings (a failed job of a run in progress).
        n=$(cat "$FAKE_STATE.fl" 2>/dev/null || echo 0); echo $((n + 1)) > "$FAKE_STATE.fl"
        if [ "$n" -lt "${FAKE_FAILED_RUNS_AFTER:-0}" ]; then echo '[]' | out; else echo '[{"databaseId":7}]' | out; fi ;;
      *) echo '[]' | out ;;
    esac ;;
  "run rerun") touch "$FAKE_STATE.reran" ;;
  "workflow run")
    # The hosted gate dispatches ci.yml; what the running lander's gate file says is kept.
    n=$(cat "$FAKE_STATE.dispatch" 2>/dev/null || echo 0); echo $((n + 1)) > "$FAKE_STATE.dispatch"
    cat "$LAND_STATE/gate" >> "$FAKE_STATE.gate-seen" 2>/dev/null || true ;;
  "run view") case "$*" in *--log*) ;; *) printf '%s\n' "${FAKE_GH_JOBS:-}" | out ;; esac ;;
  "pr list")
    # FAKE_PR_LIST_FAILS: GitHub does not answer; FAKE_GH_TRAINS_LISTS=n: the trains are open for n lists only.
    [ -z "${FAKE_PR_LIST_FAILS:-}" ] || { echo "HTTP 502" >&2; exit 1; }
    n=$(cat "$FAKE_STATE.lists" 2>/dev/null || echo 0); echo $((n + 1)) > "$FAKE_STATE.lists"
    if [ -n "${FAKE_GH_TRAINS_LISTS:-}" ] && [ "$n" -ge "$FAKE_GH_TRAINS_LISTS" ]; then echo '[]'
    elif [ -e "$FAKE_STATE.pr" ] && [ "$(cat "$FAKE_STATE.pr")" != OPEN ]; then echo '[]'; else echo "${FAKE_GH_TRAINS:-[]}"; fi | out ;;
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
  "pr close") [ -n "${FAKE_CLOSE_FAILS:-}" ] && exit 1; echo CLOSED > "$FAKE_STATE.pr"
    # --delete-branch deletes the head on origin, as GitHub does.
    case "$*" in *--delete-branch*) git --git-dir="$FAKE_UP" update-ref -d "refs/heads/$(cat "$FAKE_STATE.head")" 2>/dev/null || true ;; esac ;;
  "pr checks") if [ -n "${FAKE_STARTUP_FAILURE:-}" ]; then echo '[{"bucket":"fail","state":"STARTUP_FAILURE"}]'; elif red; then echo '[{"bucket":"fail","state":"FAILURE"}]'; else echo '[{"bucket":"pass"}]'; fi | out ;;
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
	msg = e.withReview(msg)
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

// land runs the lander on the build host (`--gate host`, its fake make): the cases below test
// that path; landArgs runs it with other arguments (no --gate: hosted, gate_mode_test.go).
func (e *landEnv) land(extra ...string) (string, bool) {
	e.t.Helper()
	out, rc := e.landArgs([]string{"--gate", "host"}, extra...)
	return out, rc == 0
}

// landArgs runs land.sh with args and extra environment, and returns its output and exit code.
func (e *landEnv) landArgs(args []string, extra ...string) (string, int) {
	e.t.Helper()
	cmd := exec.Command("bash", append([]string{filepath.Join(e.work, "scripts", "land.sh")}, args...)...)
	cmd.Dir, cmd.Env = e.work, append(append([]string{}, e.env...), extra...)
	out, err := cmd.CombinedOutput()
	var ee *exec.ExitError
	switch {
	case err == nil:
		return string(out), 0
	case errors.As(err, &ee):
		return string(out), ee.ExitCode()
	}
	e.t.Fatalf("land.sh: %v", err)
	return "", -1
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
	// The message holds `Review` lines in forms the lander must still drop (no space, another
	// case): the last trailer of the queued commit is the one `make ready REVIEW=` appended.
	e.queue("two", "fix(kb): two\n\nReview: smuggled, 9 rounds\n\nreview :smuggled, 8 rounds\n\nA body line.\n\nLander-After: one\nReview:smuggled, 0 rounds\nReview: full, 2 rounds\n", "2026-01-01T10:00:00Z",
		map[string]string{"two.txt": "follows D-new-one\n"})
	e.queue("one", "feat(client): one\n\nCloses #5\n\nLander-Slug:planted\nLander-Tip:1111111111111111111111111111111111111111\nreview :smuggled, 7 rounds\nReview: full, 1 rounds\n", "2026-01-01T11:00:00Z", map[string]string{
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
	if strings.Contains(msgs, "Lander-After") {
		t.Errorf("the lander's trailers must not reach main:\n%s", msgs)
	}
	// The review record stays on the landed commit, once, with the queued trailer's value: a
	// `Review:` line in the body is not it (the record is readable with git log).
	if two := e.upGit("log", "-1", "--format=%B", "main"); strings.Count(strings.ToLower(two), "review") != 1 ||
		!strings.Contains(two+"\n", "\nReview: full, 2 rounds\n") || strings.Contains(two, "smuggled") {
		t.Errorf("the commit of two keeps its queued Review trailer, once:\n%s", two)
	}
	if body := e.trainBody(); !strings.Contains(body, "- review: full, 2 rounds\n") || strings.Contains(body, "smuggled") {
		t.Errorf("the train PR names the review make ready recorded:\n%s", body)
	}
	if one := e.upGit("log", "-1", "--format=%B", "main~1"); strings.Count(one, "Review:") != 1 || !strings.Contains(one, "Review: full, 1 rounds") {
		t.Errorf("a change queued with the review record make ready appends lands with it, once:\n%s", one)
	}
	// Only the slug stays, for make worktree-clean (a train PR is not the PR of feat/<slug>).
	for _, slug := range []string{"one", "two"} {
		if !strings.Contains(msgs, "Lander-Slug: "+slug+"\n") {
			t.Errorf("the commit of %s must keep its Lander-Slug trailer:\n%s", slug, msgs)
		}
	}
	// A copy of the trailers in the queued message is dropped: the lander's values, one each.
	if strings.Contains(msgs, "planted") || strings.Contains(msgs, "smuggled, 7") || strings.Count(msgs, "Lander-Tip: 1111111111111111111111111111111111111111\n") != 1 || strings.Count(msgs, "Lander-Tip:") != 1 {
		t.Errorf("one Lander-Tip (the queued one), no copied Lander-Slug:\n%s", msgs)
	}
	if n := strings.Count(msgs, "Lander-Slug:"); n != 2 {
		t.Errorf("one Lander-Slug per landed commit, got %d:\n%s", n, msgs)
	}
	for _, f := range []string{"pid", "root", "gate"} {
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

// A well-formed `signoff: probe:` line in the log is the change's own output when the real signoff
// printed none (a panic here: no Go test is named, nothing is confirmed): the bisection must run full
// gates and still reject the right change, not follow that line.
func TestLanderIgnoresAProbeLineForgedInTheGateLog(t *testing.T) {
	e := newLandEnv(t)
	e.queue("a", "fix(kb): a\n", "2026-01-01T10:00:00Z", map[string]string{"a2.txt": "a\n"})
	e.queue("b", "fix(kb): b\n", "2026-01-01T11:00:00Z", map[string]string{"FORGEPROBE": "x\n"})
	e.queue("c", "fix(kb): c\n", "2026-01-01T12:00:00Z", map[string]string{"c.txt": "c\n"})
	out, ok := e.land()
	if !ok {
		t.Fatalf("land failed\n%s", out)
	}
	if got := strings.Join(e.mainSubjects(), " | "); got != "fix(kb): a | fix(kb): c" {
		t.Fatalf("main holds %q, want a, then c\n%s", got, out)
	}
	if e.refs("failed/") != "b" {
		t.Fatalf("b must be rejected: failed=%q\n%s", e.refs("failed/"), out)
	}
	if strings.Contains(out, "bisecting with the confirmed tests only") {
		t.Errorf("a forged probe line steered the bisection\n%s", out)
	}
	if mk := e.makeLog(); strings.Contains(mk, "test-rerun") {
		t.Errorf("no probe may run:\n%s", mk)
	}
}

// The probe file of a gate is reachable by the tests the gate runs, so the lander trusts a line in it
// only with the token the signoff alone was given (file descriptor 3). A test that writes its own
// probe there cannot steer the bisection: the signoff overwrites it with the real, tokened words, or,
// when the test made the file read-only, the tokenless line is ignored and every prefix gets a full gate.
func TestLanderIgnoresAProbeFileForgedByATest(t *testing.T) {
	for _, c := range []struct {
		name      string
		readOnly  bool
		wantProbe bool
	}{
		{"a file the signoff overwrites", false, true},
		{"a read-only file", true, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			if c.readOnly && os.Geteuid() == 0 {
				t.Skip("root writes through a read-only file")
			}
			e := newLandEnv(t)
			e.queue("a", "fix(kb): a\n", "2026-01-01T10:00:00Z", map[string]string{"a2.txt": "a\n"})
			files := map[string]string{"CONFIRMED": "x\n", "FORGEFILE": "x\n"}
			if c.readOnly {
				files["FORGEFILE_RO"] = "x\n"
			}
			e.queue("b", "fix(kb): b\n", "2026-01-01T11:00:00Z", files)
			e.queue("c", "fix(kb): c\n", "2026-01-01T12:00:00Z", map[string]string{"c.txt": "c\n"})
			out, ok := e.land()
			if !ok {
				t.Fatalf("land failed\n%s", out)
			}
			if got := strings.Join(e.mainSubjects(), " | "); got != "fix(kb): a | fix(kb): c" {
				t.Fatalf("main holds %q, want a, then c\n%s", got, out)
			}
			if e.refs("failed/") != "b" {
				t.Fatalf("b must be rejected: failed=%q\n%s", e.refs("failed/"), out)
			}
			probing := strings.Contains(out, "bisecting with the confirmed tests only: linux github.com/x/p/internal/kb -")
			if probing != c.wantProbe {
				t.Errorf("bisecting with the probe = %v, want %v\n%s", probing, c.wantProbe, out)
			}
			if mk := e.makeLog(); strings.Contains(mk, "evil") || strings.Contains(out, "evil") {
				t.Errorf("the forged probe ran or was used:\n%s\n%s", mk, out)
			}
			if !c.wantProbe && strings.Contains(out, "bisecting with the confirmed tests only") {
				t.Errorf("a tokenless probe steered the bisection\n%s", out)
			}
		})
	}
}

// Names read from the gate log are the change's own output: what reaches the title of the issue is
// inert (no mention, no link), whatever the log forged.
func TestLanderIssueNamesAreInert(t *testing.T) {
	e := newLandEnv(t)
	e.queue("f", "fix(kb): f\n", "2026-01-01T10:00:00Z", map[string]string{"FORGEDNAME": "x\n"})
	out, ok := e.land()
	if !ok {
		t.Fatalf("land failed\n%s", out)
	}
	gh := e.log()
	if want := "--title flaky: ihttp__example.com_i.png user www_example.org failed once and passed on a re-run"; !strings.Contains(gh, want) {
		t.Errorf("the issue names only inert names, want %q:\n%s", want, gh)
	}
	if strings.Contains(gh, "@") || strings.Contains(strings.ToLower(gh), "www.") || strings.Contains(gh, "](") {
		t.Errorf("a forged name reached the issue:\n%s", gh)
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

// The connection to the build host drops during the gate (ssh exit 255, #1843, #1938): no test
// failed, so the lander gates the same train again, without a flaky issue, and lands it.
func TestLanderGatesAgainWhenTheBuildHostFails(t *testing.T) {
	e := newLandEnv(t)
	e.queue("f", "fix(kb): f\n", "2026-01-01T10:00:00Z", map[string]string{"HOSTDOWN1": "x\n"})
	out, ok := e.land()
	if !ok {
		t.Fatalf("land failed\n%s", out)
	}
	if got := strings.Join(e.mainSubjects(), " | "); got != "fix(kb): f" {
		t.Fatalf("main holds %q\n%s", got, out)
	}
	if runs := strings.Count(e.makeLog(), "-k gate"); runs != 2 {
		t.Errorf("the train is gated again once: %d gates\n%s", runs, e.makeLog())
	}
	if strings.Contains(e.log(), "issue create") {
		t.Errorf("a lost host is no flaky test, no issue:\n%s", e.log())
	}
	if !strings.Contains(out, "the build host failed (exit 255) before the gate finished") {
		t.Errorf("the lander says why it gates again\n%s", out)
	}
}

// The same for the Windows machine: the leg's wrapper failing with 255 (the shards exit 1 when a
// test fails) gates the train again, with no flaky issue.
func TestLanderGatesAgainWhenTheWindowsMachineFails(t *testing.T) {
	e := newLandEnv(t)
	e.write(filepath.Join(e.bin, "fakewin"), `#!/bin/sh
echo "$*" >> "$FAKE_STATE.win"
if [ ! -e "$FAKE_STATE.windown" ]; then touch "$FAKE_STATE.windown"; echo "Timeout, server not responding"; exit 255; fi
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
	if runs := strings.Count(e.makeLog(), "-k gate"); runs != 2 {
		t.Errorf("the train is gated again once: %d gates\n%s", runs, e.makeLog())
	}
	if !strings.Contains(out, "the Windows machine failed (exit 255) before the gate finished") {
		t.Errorf("the lander says why it gates again\n%s", out)
	}
	if strings.Contains(e.log(), "issue create") {
		t.Errorf("a lost machine is no flaky test, no issue:\n%s", e.log())
	}
}

// A build host that stays down stops the lander after LAND_INFRA_TRIES gates, with the queue
// untouched and the failure named: nothing is rejected, nothing is bisected.
func TestLanderStopsWhenTheBuildHostStaysDown(t *testing.T) {
	e := newLandEnv(t)
	e.queue("f", "fix(kb): f\n", "2026-01-01T10:00:00Z", map[string]string{"HOSTDOWN": "x\n"})
	out, ok := e.land("LAND_INFRA_TRIES=2")
	if ok {
		t.Fatalf("a host that stays down must stop the lander\n%s", out)
	}
	if !strings.Contains(out, "infrastructure, not a test: the build host failed (exit 255) before the gate finished") ||
		!strings.Contains(out, "the queue is untouched") {
		t.Errorf("the stop names the infrastructure failure\n%s", out)
	}
	if runs := strings.Count(e.makeLog(), "-k gate"); runs != 2 {
		t.Errorf("LAND_INFRA_TRIES bounds the gates: %d gates\n%s", runs, e.makeLog())
	}
	if q, f := e.refs("ready/"), e.refs("failed/"); q != "f" || f != "" {
		t.Errorf("the queue is untouched: ready=%q failed=%q", q, f)
	}
	if got := e.mainSubjects(); len(got) != 0 {
		t.Errorf("nothing lands: %v", got)
	}
	if strings.Contains(e.log(), "issue create") {
		t.Errorf("no issue for a lost host:\n%s", e.log())
	}
}

// The log of a gate holds the change's own test output: a test that prints the signoff's line for
// a lost host does not turn a red gate into infrastructure. The signal is the signoff's exit code
// (69), so this gate is red, confirmed on the targeted retry, and the change is rejected after one
// gate, instead of the lander gating the same tree again until it gives up and the queue wedges.
func TestLanderReadsARedGateAsRedWhateverALogLineSays(t *testing.T) {
	e := newLandEnv(t)
	e.queue("c", "fix(kb): c\n", "2026-01-01T10:00:00Z", map[string]string{"CONFIRMED": "x\n", "SPOOF": "x\n"})
	out, ok := e.land()
	if !ok {
		t.Fatalf("land failed\n%s", out)
	}
	if e.refs("failed/") != "c" {
		t.Fatalf("c must be rejected: failed=%q\n%s", e.refs("failed/"), out)
	}
	if runs := strings.Count(e.makeLog(), "-k gate"); runs != 1 {
		t.Errorf("a red gate is not gated again as a lost host: %d gates\n%s", runs, e.makeLog())
	}
	if strings.Contains(out, "gating") && strings.Contains(out, "again") || strings.Contains(out, "infrastructure, not a test") {
		t.Errorf("the printed line must not read as infrastructure\n%s", out)
	}
}

// A probe of the bisection that loses the build host (ssh exit 255) is no verdict: it must not
// move the bisection's bound and reject a change no test condemned. The lander falls back to
// full gates, which find the real culprit (b), and a, before it, lands.
func TestLanderABisectionProbeThatLosesTheHostRejectsNoInnocentChange(t *testing.T) {
	e := newLandEnv(t)
	e.queue("a", "fix(kb): a\n", "2026-01-01T10:00:00Z", map[string]string{"a2.txt": "a\n"})
	e.queue("b", "fix(kb): b\n", "2026-01-01T11:00:00Z", map[string]string{"CONFIRMED": "x\n"})
	e.queue("c", "fix(kb): c\n", "2026-01-01T12:00:00Z", map[string]string{"c.txt": "c\n"})
	out, ok := e.land("FAKE_PROBE255=1")
	if !ok {
		t.Fatalf("land failed\n%s", out)
	}
	if e.refs("failed/") != "b" {
		t.Fatalf("only b is rejected: failed=%q\n%s", e.refs("failed/"), out)
	}
	if got := strings.Join(e.mainSubjects(), " | "); got != "fix(kb): a | fix(kb): c" {
		t.Fatalf("main holds %q, want a, then c\n%s", got, out)
	}
	if !strings.Contains(out, "the probe's machine is unavailable: bisecting with full gates") {
		t.Errorf("a failed probe falls back to full gates\n%s", out)
	}
}

// A failing deferred web-perf is a red gate, like any other make failure: the lander re-runs the
// gate once and, red twice, rejects the change, instead of stopping on "not a test".
func TestLanderReadsAFailingWebPerfAsRed(t *testing.T) {
	e := newLandEnv(t)
	e.write(filepath.Join(e.bin, "fakewin"), "#!/bin/sh\nexit 0\n")
	e.queue("w", "fix(kb): w\n", "2026-01-01T10:00:00Z", map[string]string{"web/src/a.ts": "x\n", "WEBPERF": "x\n"})
	out, ok := e.land("SIGNOFF_WINDOWS=" + filepath.Join(e.bin, "fakewin"))
	if !ok {
		t.Fatalf("land failed\n%s", out)
	}
	if e.refs("failed/") != "w" {
		t.Fatalf("w must be rejected: failed=%q\n%s", e.refs("failed/"), out)
	}
	if strings.Contains(out, "not a test") {
		t.Errorf("a failing web-perf is a test failure\n%s", out)
	}
	if !strings.Contains(out, "make web-perf failed (exit 2): nothing pushed, nothing posted") {
		t.Errorf("the signoff names the exit\n%s", out)
	}
}

// The host is lost at the last step of the gate (exit 75 of the deferred web-perf, or of the
// signoff's targeted retry): it says nothing about the tests, so the lander gates the same tree
// again, with no re-run's flaky issue and no rejection. Before, both ended in the red line
// (`failed (exit N): nothing pushed`) that makes the lander re-run, and bisect if the host is still down.
func TestLanderGatesAgainWhenTheHostIsLostAtTheLastStepOfTheGate(t *testing.T) {
	for _, c := range []struct {
		name    string
		files   map[string]string
		windows bool // the deferred web-perf runs only beside a Windows leg
	}{
		{"the deferred web-perf", map[string]string{"web/src/a.ts": "x\n", "WEBPERF75": "x\n"}, true},
		{"the targeted retry", map[string]string{"FLAKYGO": "x\n", "RETRY75": "x\n"}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newLandEnv(t)
			var env []string
			if c.windows {
				e.write(filepath.Join(e.bin, "fakewin"), "#!/bin/sh\nexit 0\n")
				env = append(env, "SIGNOFF_WINDOWS="+filepath.Join(e.bin, "fakewin"))
			}
			e.queue("h", "fix(kb): h\n", "2026-01-01T10:00:00Z", c.files)
			out, ok := e.land(env...)
			if !ok {
				t.Fatalf("land failed\n%s", out)
			}
			if got := strings.Join(e.mainSubjects(), " | "); got != "fix(kb): h" {
				t.Fatalf("main holds %q\n%s", got, out)
			}
			if e.refs("failed/") != "" {
				t.Errorf("nothing is rejected: failed=%q\n%s", e.refs("failed/"), out)
			}
			if runs := strings.Count(e.makeLog(), "-k gate"); runs != 2 {
				t.Errorf("the train is gated again once: %d gates\n%s", runs, e.makeLog())
			}
			if strings.Contains(e.log(), "issue create") {
				t.Errorf("a lost host is no flaky test, no issue:\n%s", e.log())
			}
			if !strings.Contains(out, "the build host failed (exit 75) before the gate finished") {
				t.Errorf("the lander says why it gates again\n%s", out)
			}
		})
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

// Two frontend changes always conflict in the committed bundle, never in their sources: a
// change whose only conflict is under internal/webui/dist is picked with the bundle built
// again (LAND_BUNDLE_BUILD stands in for make web), and lands; a conflict anywhere else is
// still a conflict.
func TestLanderRebuildsAConflictingBundle(t *testing.T) {
	e := newLandEnv(t)
	e.queue("ui", "fix(atlas): ui\n", "2026-01-01T10:00:00Z", map[string]string{"internal/webui/dist/index.js": "mine\n", "web/src/a.ts": "a\n"})
	e.queue("mixed", "fix(atlas): mixed\n", "2026-01-01T11:00:00Z", map[string]string{"internal/webui/dist/index.js": "other\n", "b.txt": "mine\n"})
	e.git("checkout", "-q", "-B", "move", "origin/main")
	e.write(filepath.Join(e.work, "internal/webui/dist/index.js"), "theirs\n")
	e.write(filepath.Join(e.work, "b.txt"), "theirs\n")
	e.git("add", "-A")
	e.git("commit", "-q", "-m", "fix: move the bundle")
	e.git("push", "-q", "origin", "move:main")
	e.git("checkout", "-q", "main")
	build := filepath.Join(t.TempDir(), "build")
	e.write(build, "#!/bin/sh\nprintf 'built\\n' > internal/webui/dist/index.js\n")
	if err := os.Chmod(build, 0o755); err != nil {
		t.Fatal(err)
	}
	out, ok := e.land("LAND_BUNDLE_BUILD=" + build)
	if !ok {
		t.Fatalf("land failed\n%s", out)
	}
	if got := strings.Join(e.mainSubjects(), " | "); got != "fix: move the bundle | fix(atlas): ui" {
		t.Fatalf("main holds %q\n%s", got, out)
	}
	if got := e.upGit("show", "main:internal/webui/dist/index.js"); got != "built" {
		t.Errorf("the bundle on main is the rebuilt one, got %q", got)
	}
	if got := strings.Fields(e.refs("failed/")); strings.Join(got, " ") != "mixed" {
		t.Errorf("a conflict outside the bundle is still rejected, got %v\n%s", got, out)
	}
}

// bundleBuildScript writes a build that records its run in marker and, when ok, writes the
// bundle's provenance from the content of web/src/a.ts (the lander's `make web` stand-in).
func bundleBuildScript(t *testing.T, e *landEnv, marker string, ok bool) string {
	t.Helper()
	build := filepath.Join(t.TempDir(), "build")
	body := "#!/bin/sh\ntouch '" + marker + "'\n"
	if ok {
		body += "mkdir -p internal/webui/dist && cp web/src/a.ts internal/webui/dist/provenance.json\n"
	} else {
		body += "echo boom; exit 1\n"
	}
	e.write(build, body)
	if err := os.Chmod(build, 0o755); err != nil {
		t.Fatal(err)
	}
	return build
}

// Numbering a decision rewrites a citation under web/ after the author built the bundle: the
// lander builds it again, so the committed bundle matches the sources it lands with (#2067). A
// change whose web/ does not cite a pending decision does not run the build; a failed build
// rejects the change with that reason.
func TestLanderRebuildsTheBundleAfterNumberingACitationUnderWeb(t *testing.T) {
	dec := "docs/" + "decisions/"
	files := map[string]string{
		dec + "D-new-foo.md":                  "---\ntopic: x\n---\n\n# D-new-foo — Foo\n",
		"web/src/a.ts":                        "// D-new-foo\n",
		"internal/webui/dist/provenance.json": "stale\n",
	}
	t.Run("rebuilt", func(t *testing.T) {
		e := newLandEnv(t)
		e.queue("a", "feat(atlas): a\n", "2026-01-01T10:00:00Z", files)
		out, ok := e.land("LAND_BUNDLE_BUILD=" + bundleBuildScript(t, e, filepath.Join(t.TempDir(), "ran"), true))
		if !ok || e.refs("failed/") != "" {
			t.Fatalf("the change must land, failed=%q\n%s", e.refs("failed/"), out)
		}
		src := e.upGit("show", "main:web/src/a.ts")
		if strings.Contains(src, "D-new-") {
			t.Fatalf("the citation is numbered: %q", src)
		}
		if got := e.upGit("show", "main:internal/webui/dist/provenance.json"); got != src {
			t.Errorf("the bundle on main is built from the numbered source: %q, source %q", got, src)
		}
		if got := e.upGit("log", "-1", "--format=%s", "main"); got != "feat(atlas): a" {
			t.Errorf("the rebuild amends the change, got %q", got)
		}
	})
	t.Run("build fails", func(t *testing.T) {
		e := newLandEnv(t)
		e.queue("a", "feat(atlas): a\n", "2026-01-01T10:00:00Z", files)
		out, ok := e.land("LAND_BUNDLE_BUILD=" + bundleBuildScript(t, e, filepath.Join(t.TempDir(), "ran"), false))
		if !ok {
			t.Fatalf("land failed\n%s", out)
		}
		if got := strings.Join(e.mainSubjects(), " | "); strings.Contains(got, "feat(atlas): a") {
			t.Fatalf("the change must not land, main holds %q\n%s", got, out)
		}
		if got := strings.Fields(e.refs("failed/")); strings.Join(got, " ") != "a" {
			t.Fatalf("a must be rejected, got %v\n%s", got, out)
		}
		if r := e.upGit("log", "-1", "--format=%s", "failed/a"); !strings.Contains(r, "bundle could not be rebuilt") {
			t.Errorf("the reason says the bundle was not rebuilt, got %q", r)
		}
	})
	t.Run("citation early in a large diff", func(t *testing.T) {
		e := newLandEnv(t)
		big := "// D-new-foo\n" + strings.Repeat("// padding padding padding padding padding padding\n", 3000)
		e.queue("a", "feat(atlas): a\n", "2026-01-01T10:00:00Z", map[string]string{
			dec + "D-new-foo.md":                  files[dec+"D-new-foo.md"],
			"web/src/a.ts":                        big,
			"internal/webui/dist/provenance.json": "stale\n",
		})
		out, ok := e.land("LAND_BUNDLE_BUILD=" + bundleBuildScript(t, e, filepath.Join(t.TempDir(), "ran"), true))
		if !ok || e.refs("failed/") != "" {
			t.Fatalf("the change must land, failed=%q\n%s", e.refs("failed/"), out)
		}
		if got, src := e.upGit("show", "main:internal/webui/dist/provenance.json"), e.upGit("show", "main:web/src/a.ts"); got != src {
			t.Errorf("the bundle was not rebuilt after a diff over 64 KB")
		}
	})
	t.Run("citation in a test file, no build", func(t *testing.T) {
		e := newLandEnv(t)
		marker := filepath.Join(t.TempDir(), "ran")
		e.queue("a", "feat(atlas): a\n", "2026-01-01T10:00:00Z", map[string]string{
			dec + "D-new-foo.md":   files[dec+"D-new-foo.md"],
			"web/x_test.go":        "// D-new-foo\n",
			"web/testdata/fix.txt": "D-new-foo\n",
		})
		out, ok := e.land("LAND_BUNDLE_BUILD=" + bundleBuildScript(t, e, marker, true))
		if !ok || e.refs("failed/") != "" {
			t.Fatalf("the change must land, failed=%q\n%s", e.refs("failed/"), out)
		}
		if _, err := os.Stat(marker); err == nil {
			t.Error("the bundle was built for a citation the numbering does not rewrite")
		}
	})
	t.Run("no citation, no build", func(t *testing.T) {
		e := newLandEnv(t)
		marker := filepath.Join(t.TempDir(), "ran")
		e.queue("a", "feat(atlas): a\n", "2026-01-01T10:00:00Z", map[string]string{
			dec + "D-new-foo.md": "---\ntopic: x\n---\n\n# D-new-foo — Foo\n",
			"web/src/a.ts":       "// plain\n",
		})
		out, ok := e.land("LAND_BUNDLE_BUILD=" + bundleBuildScript(t, e, marker, true))
		if !ok || e.refs("failed/") != "" {
			t.Fatalf("the change must land, failed=%q\n%s", e.refs("failed/"), out)
		}
		if _, err := os.Stat(marker); err == nil {
			t.Error("the bundle was built although web/ cites no pending decision")
		}
	})
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
// A permission read that fails (GitHub 5xx) is asked again; only a definite answer outside the
// maintainer roles says the user is not a maintainer (#1886).
func TestLanderRetriesThePermissionRead(t *testing.T) {
	e := newLandEnv(t)
	out, ok := e.land("FAKE_PERM_FAILS=2")
	if !ok || strings.Contains(out, "permission") {
		t.Fatalf("a failing read is retried: %v\n%s", ok, out)
	}
	if n := strings.Count(e.log(), "repo view"); n != 3 {
		t.Fatalf("want 3 reads, got %d", n)
	}
}

func TestLanderDiesOnADefiniteNonMaintainerPermission(t *testing.T) {
	e := newLandEnv(t)
	out, ok := e.land("FAKE_PERM=READ")
	if ok || !strings.Contains(out, "permission on up/proj is READ: only a maintainer lands") {
		t.Fatalf("READ: %v\n%s", ok, out)
	}
	if n := strings.Count(e.log(), "repo view"); n != 1 {
		t.Fatalf("a definite answer is not retried, got %d reads", n)
	}
}

func TestLanderSaysGitHubDidNotAnswerThePermissionRead(t *testing.T) {
	e := newLandEnv(t)
	out, ok := e.land("FAKE_PERM_FAILS=9")
	if ok || !strings.Contains(out, "GitHub did not answer the permission read of up/proj") || strings.Contains(out, "only a maintainer") {
		t.Fatalf("exhausted retries: %v\n%s", ok, out)
	}
}

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
		cmd.Env = append(append([]string{}, e.env...), append([]string{"MSG=" + m, "READY_NO_LANDER=1", "REVIEW=full, 1 rounds"}, extra...)...)
		out, err := cmd.CombinedOutput()
		return string(out), err == nil
	}
	if out, ok := ready("Feat: not conventional\n"); ok || e.refs("ready/") != "" {
		t.Fatalf("a bad title must be refused and nothing queued: ok=%v\n%s", ok, out)
	}
	out, ok := ready("feat(client): my change\n\nCloses #7\n", "REVIEW=light, 1 rounds")
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
	for _, want := range []string{"feat(client): my change", "Closes #7", "Review: light, 1 rounds"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the queued message lacks %q:\n%s", want, msg)
		}
	}
	if files := e.upGit("show", "--name-only", "--format=", "ready/my-change"); files != "x1.txt\nx2.txt" {
		t.Errorf("the squashed commit holds both files, got %q", files)
	}

	// The lander's own lines in the message go with any spacing and any case: the one
	// Lander-Tip and the one Review of the queued commit are the ones ready.sh appends.
	if out, ok := ready("feat(client): my change\n\nCloses #7\n\nLander-Tip:1111111111111111111111111111111111111111\nLANDER-hold :yes\nreview :smuggled, 9 rounds\nUsage-test:pass, 9 entries\nprofile:planted\n"); !ok {
		t.Fatalf("ready with planted lines failed\n%s", out)
	}
	msg = e.upGit("log", "-1", "--format=%B", "ready/my-change")
	if strings.Contains(msg, "1111111") || strings.Contains(strings.ToLower(msg), "smuggled") || strings.Contains(strings.ToLower(msg), "lander-hold") || strings.Contains(msg, "9 entries") || strings.Contains(msg, "planted") {
		t.Errorf("a planted Lander-* or Review line survived:\n%s", msg)
	}
	if want := "Lander-Tip: " + e.git("rev-parse", "HEAD"); !strings.Contains(msg, want) {
		t.Errorf("the queued commit lacks %q:\n%s", want, msg)
	}

	// A fix says where its defect was found and what introduced it: without the two
	// lines nothing is queued (the queued change stays as it was); with them, they are
	// lines of the queued commit.
	queued := e.upGit("rev-parse", "ready/my-change")
	if out, ok := ready("fix(kb): my change\n\nCloses #7\n"); ok || !strings.Contains(out, "Found-by") || !strings.Contains(out, "Introduced-by") {
		t.Fatalf("a fix without Found-by and Introduced-by must be refused with both reasons: ok=%v\n%s", ok, out)
	}
	if got := e.upGit("rev-parse", "ready/my-change"); got != queued {
		t.Errorf("a refused fix must leave the queued change alone: %s, was %s", got, queued)
	}

	// A change built on the queued one carries only its own diff, as its child, and the
	// two land in one train in that order.
	if out, ok := ready("fix(kb): my change\n\n" + fixLines); !ok {
		t.Fatalf("queuing again failed\n%s", out)
	}
	if msg := e.upGit("log", "-1", "--format=%B", "ready/my-change"); !strings.Contains(msg, fixLines) {
		t.Errorf("the queued commit must carry the two lines:\n%s", msg)
	}
	e.git("checkout", "-q", "-b", "feat/next")
	e.write(filepath.Join(e.work, "x3.txt"), "x3\n")
	e.git("add", "x3.txt")
	e.git("commit", "-q", "-m", "wip x3")
	if out, ok := ready("fix(kb): next\n\n"+fixLines, "AFTER=my-change"); !ok {
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
	if msg := e.upGit("log", "-1", "--format=%B", "main"); !strings.Contains(msg, fixLines) {
		t.Errorf("the landed commit must keep the two lines:\n%s", msg)
	}
}

// A change built on one that has already landed (squashed, with the lander's trailers)
// is queued with only its own diff, on main; a predecessor neither queued nor landed is
// refused with the rebase to run.
func TestReadyAfterAPredecessorThatLanded(t *testing.T) {
	e := newLandEnv(t)
	ready := func(msg string, extra ...string) (string, bool) {
		m := filepath.Join(e.tmp, "msg")
		e.write(m, msg)
		cmd := exec.Command("bash", filepath.Join(e.work, "scripts", "ready.sh"))
		cmd.Dir = e.work
		cmd.Env = append(append([]string{}, e.env...), append([]string{"MSG=" + m, "READY_NO_LANDER=1", "REVIEW=full, 1 rounds"}, extra...)...)
		out, err := cmd.CombinedOutput()
		return string(out), err == nil
	}
	commit := func(f string) {
		e.write(filepath.Join(e.work, f), f+"\n")
		e.git("add", f)
		e.git("commit", "-q", "-m", "wip "+f)
	}
	e.git("checkout", "-q", "-b", "feat/first")
	commit("a1.txt")
	commit("a2.txt")
	if out, ok := ready("fix(kb): first\n\n" + fixLines); !ok {
		t.Fatalf("queuing first failed\n%s", out)
	}
	e.git("checkout", "-q", "-b", "feat/second")
	commit("b1.txt")
	e.git("checkout", "-q", "main")
	if out, ok := e.land(); !ok {
		t.Fatalf("land failed\n%s", out)
	}
	if e.refs("ready/") != "" {
		t.Fatalf("first must have left the queue, got %q", e.refs("ready/"))
	}
	// Another change lands between the predecessor and the successor: the successor's
	// queued diff must not undo it.
	e.queue("other", "fix(kb): other\n", "2026-01-01T10:00:00Z", map[string]string{"c1.txt": "c1\n"})
	if out, ok := e.land(); !ok {
		t.Fatalf("landing other failed\n%s", out)
	}
	e.git("checkout", "-q", "feat/second")
	for _, bad := range []string{"nowhere", "Bad_Slug", "-x"} {
		if out, ok := ready("fix(kb): second\n\n"+fixLines, "AFTER="+bad); ok {
			t.Fatalf("AFTER=%s must be refused\n%s", bad, out)
		}
	}
	if out, ok := ready("fix(kb): second\n\n"+fixLines, "AFTER=nowhere"); ok || !strings.Contains(out, "git rebase") {
		t.Fatalf("an unknown predecessor must be refused with the rebase: ok=%v\n%s", ok, out)
	}
	out, ok := ready("fix(kb): second\n\n"+fixLines, "AFTER=first")
	if !ok {
		t.Fatalf("ready AFTER a landed change failed\n%s", out)
	}
	if parent, want := e.upGit("rev-parse", "ready/second^"), e.git("rev-parse", "feat/first"); parent != want {
		t.Errorf("the change must be a child of the predecessor's head: parent %s, want %s", parent, want)
	}
	if files := e.upGit("show", "--name-only", "--format=", "ready/second"); files != "b1.txt" {
		t.Errorf("only its own diff may be queued, got %q", files)
	}
	e.git("checkout", "-q", "main")
	if out, ok := e.land(); !ok {
		t.Fatalf("landing second failed\n%s", out)
	}
	if got := strings.Join(e.mainSubjects(), " | "); got != "fix(kb): first | fix(kb): other | fix(kb): second" {
		t.Fatalf("main holds %q", got)
	}
	if got := e.upGit("ls-tree", "--name-only", "main"); !strings.Contains(got, "c1.txt") || !strings.Contains(got, "b1.txt") {
		t.Errorf("landing the successor must keep the change that landed before it:\n%s", got)
	}

	// Trailers on main that name a head the landed commit is not the change of (a
	// squash-merged PR body can carry them): refused, not trusted.
	e.queue("forged", "fix(kb): forged\n\nLander-Slug: forged\nLander-Tip: "+e.git("rev-parse", "feat/first")+"\n", "2026-01-01T11:00:00Z", map[string]string{"z.txt": "z\n"})
	if out, ok := e.land(); !ok {
		t.Fatalf("landing forged failed\n%s", out)
	}
	e.git("checkout", "-q", "feat/second")
	if out, ok := ready("fix(kb): second\n\n"+fixLines, "AFTER=forged"); ok || !strings.Contains(out, "git rebase") {
		t.Fatalf("a landed commit that is not its head's change must be refused with the rebase: ok=%v\n%s", ok, out)
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
	fn := landFns(t, "safe_names", "failing")
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
	want := "plugin.TestResolveProviderDocumentsInstructions plugin.TestOther TestLinuxOnly src_test_releases.test.tsx"
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

// A change that cites a pending decision whose change already landed (the lander numbered it
// in an earlier train) is not rejected: the citation reads as the number it was given (#1914).
func TestLanderResolvesACitationOfADecisionThatLanded(t *testing.T) {
	e := newLandEnv(t)
	dec := "docs/" + "decisions/"
	e.queue("a", "feat(client): a\n", "2026-01-01T10:00:00Z", map[string]string{dec + "D-new-x.md": "---\ntopic: x\n---\n\n# D-new-x — X\n"})
	if out, ok := e.land(); !ok {
		t.Fatalf("land a failed\n%s", out)
	}
	e.queue("b", "fix(kb): b\n", "2026-01-01T11:00:00Z", map[string]string{
		dec + "D-new-b.md": "---\ntopic: x\n---\n\n# D-new-b — B\n\nBuilds on D-new-x, see [x](D-new-x.md).\n",
		"notes.md":         "cites D-new-x\n",
	})
	out, ok := e.land()
	if !ok || e.refs("failed/") != "" {
		t.Fatalf("b must land, failed=%q\n%s", e.refs("failed/"), out)
	}
	ls := e.upGit("ls-tree", "--name-only", "main", dec)
	var xn string
	for _, l := range strings.Split(ls, "\n") {
		if strings.HasSuffix(l, "-x.md") {
			xn = strings.TrimSuffix(strings.TrimPrefix(l, dec), "-x.md")
		}
	}
	if xn == "" {
		t.Fatalf("a's decision is not numbered on main\n%s", ls)
	}
	var b string
	for _, l := range strings.Split(ls, "\n") {
		if strings.HasSuffix(l, "-b.md") {
			b = e.upGit("show", "main:"+l)
		}
	}
	if !strings.Contains(b, "Builds on "+xn+", see ["+"x]("+xn+"-x.md).") {
		t.Errorf("the citation must read %s:\n%s", xn, b)
	}
	if n := e.upGit("show", "main:notes.md"); n != "cites "+xn {
		t.Errorf("the citation is rewritten: %q", n)
	}
}

// Files under a testdata/ directory and test files may spell a placeholder: they are neither
// rewritten nor counted as citations (#2052).
func TestLanderLeavesPlaceholdersInTestdataAlone(t *testing.T) {
	e := newLandEnv(t)
	e.queue("a", "fix(kb): a\n", "2026-01-01T10:00:00Z", map[string]string{
		"x/testdata/y/D-new-foo.md": "# D-new-foo\n",
		"x/testdata/fixture.txt":    "cites D-new-foo\n",
		"x/z_test.go":               "// D-new-foo\n",
	})
	out, ok := e.land()
	if !ok || e.refs("failed/") != "" {
		t.Fatalf("the change must land, failed=%q\n%s", e.refs("failed/"), out)
	}
	for f, want := range map[string]string{"x/testdata/y/D-new-foo.md": "# D-new-foo", "x/testdata/fixture.txt": "cites D-new-foo", "x/z_test.go": "// D-new-foo"} {
		if got := e.upGit("show", "main:"+f); got != want {
			t.Errorf("%s = %q, want it verbatim %q", f, got, want)
		}
	}
}

// The same files next to a real pending decision: the decision is numbered and cited as D<n>
// in a normal file, the fixtures keep the placeholder (the rewrite loop skips them).
func TestLanderNumbersADecisionButLeavesItsFixturesAlone(t *testing.T) {
	e := newLandEnv(t)
	dec := "docs/" + "decisions/"
	e.queue("a", "feat(client): a\n", "2026-01-01T10:00:00Z", map[string]string{
		dec + "D-new-foo.md":     "---\ntopic: x\n---\n\n# D-new-foo — Foo\n",
		"notes.md":               "cites D-new-foo\n",
		"x/testdata/fixture.txt": "cites D-new-foo\n",
		"x/z_test.go":            "// D-new-foo\n",
	})
	out, ok := e.land()
	if !ok || e.refs("failed/") != "" {
		t.Fatalf("the change must land, failed=%q\n%s", e.refs("failed/"), out)
	}
	var n string
	for _, l := range strings.Split(e.upGit("ls-tree", "--name-only", "main", dec), "\n") {
		if strings.HasSuffix(l, "-foo.md") {
			n = strings.TrimSuffix(strings.TrimPrefix(l, dec), "-foo.md")
		}
	}
	if n == "" {
		t.Fatalf("the decision is not numbered on main\n%s", out)
	}
	if got := e.upGit("show", "main:notes.md"); got != "cites "+n {
		t.Errorf("notes.md = %q, want the citation read as %s", got, n)
	}
	for f, want := range map[string]string{"x/testdata/fixture.txt": "cites D-new-foo", "x/z_test.go": "// D-new-foo"} {
		if got := e.upGit("show", "main:"+f); got != want {
			t.Errorf("%s = %q, want it verbatim %q", f, got, want)
		}
	}
}

// An edit of docs/decisions/D-new-<slug>.md whose first change landed numbered is applied to
// the numbered file, its heading read as the number (#1914).
func TestLanderMapsAnEditOfALandedPendingDecisionOntoItsNumberedFile(t *testing.T) {
	e := newLandEnv(t)
	dec := "docs/" + "decisions/"
	body := "---\ntopic: x\n---\n\n# D-new-x — X\n\nOne.\n"
	e.queue("a", "feat(client): a\n", "2026-01-01T10:00:00Z", map[string]string{dec + "D-new-x.md": body})
	// b is built on a's pending state, as a chained change is.
	e.git("fetch", "-q", "origin")
	e.git("checkout", "-q", "-B", "work-b", "origin/ready/a")
	e.write(filepath.Join(e.work, dec, "D-new-x.md"), strings.Replace(body, "# D-new-x — X", "# D-new-x — X again", 1)+"Two.\n")
	e.git("add", ".")
	e.git("commit", "-q", "-m", "fix(client): b\n", "-m", "Review: full, 1 rounds")
	e.git("push", "-q", "--force", "origin", "HEAD:refs/heads/ready/b")
	e.git("checkout", "-q", "main")
	// Only a is landed first: b is queued after it.
	e.git("fetch", "-q", "origin")
	e.git("update-ref", "refs/lander-hold", "origin/ready/b")
	e.upGit("update-ref", "-d", "refs/heads/ready/b")
	if out, ok := e.land(); !ok {
		t.Fatalf("land a failed\n%s", out)
	}
	e.git("push", "-q", "--force", "origin", "refs/lander-hold:refs/heads/ready/b")
	out, ok := e.land()
	if !ok || e.refs("failed/") != "" {
		t.Fatalf("b must land, failed=%q\n%s", e.refs("failed/"), out)
	}
	ls := e.upGit("ls-tree", "--name-only", "main", dec)
	if strings.Contains(ls, "D-new") || strings.Count(ls, "-x.md") != 1 {
		t.Fatalf("one numbered decision, no pending one:\n%s", ls)
	}
	for _, l := range strings.Split(ls, "\n") {
		if strings.HasSuffix(l, "-x.md") {
			got := e.upGit("show", "main:"+l)
			n := strings.TrimSuffix(strings.TrimPrefix(l, dec), "-x.md")
			if !strings.Contains(got, "# "+n+" — X again") || !strings.Contains(got, "Two.") {
				t.Errorf("the edit must land on the numbered file with its heading as %s:\n%s", n, got)
			}
		}
	}
}

// A change adding a pending decision whose slug main already holds is rejected with the file
// it collides with.
func TestLanderRejectsAPendingDecisionWhoseSlugLanded(t *testing.T) {
	e := newLandEnv(t)
	dec := "docs/" + "decisions/"
	e.queue("a", "feat(client): a\n", "2026-01-01T10:00:00Z", map[string]string{dec + "D-new-x.md": "---\ntopic: x\n---\n\n# D-new-x\n"})
	if out, ok := e.land(); !ok {
		t.Fatalf("land a failed\n%s", out)
	}
	e.queue("dupe", "feat(client): dupe\n", "2026-01-01T11:00:00Z", map[string]string{dec + "D-new-x.md": "---\ntopic: x\n---\n\n# D-new-x — other\n"})
	out, ok := e.land()
	if !ok {
		t.Fatalf("land failed\n%s", out)
	}
	if e.refs("failed/") != "dupe" {
		t.Fatalf("dupe must be rejected, failed=%q\n%s", e.refs("failed/"), out)
	}
	if r := e.upGit("log", "-1", "--format=%s", "failed/dupe"); !strings.Contains(r, "-x.md (two decisions with one slug)") || !strings.Contains(r, "already holds docs/decisions/D") {
		t.Errorf("the reason names the landed file, got %q", r)
	}
}

// A required check that ends in startup_failure never turns green: the lander says so, stops
// at once (no rerun, no wait) and records it in its state (#1872).
func TestLanderStopsOnARequiredCheckInStartupFailure(t *testing.T) {
	e := newLandEnv(t)
	e.queue("a", "fix(kb): a\n", "2026-01-01T10:00:00Z", map[string]string{"a2.txt": "a\n"})
	out, ok := e.land("FAKE_STARTUP_FAILURE=1", "LAND_MERGE_TRIES=6")
	if ok || !strings.Contains(out, "ended in startup_failure") {
		t.Fatalf("the lander must stop on a startup_failure: ok=%v\n%s", ok, out)
	}
	if n := strings.Count(e.log(), "run rerun"); n != 0 {
		t.Errorf("a startup_failure is not rerun, got %d\n%s", n, e.log())
	}
	if q := e.refs("ready/"); q != "a" {
		t.Errorf("the queue is untouched, got %q", q)
	}
	if b := readTmp(t, filepath.Join(e.tmp, "lander"), "blocked"); !strings.Contains(b, "startup_failure") {
		t.Errorf("the state records it, got %q", b)
	}
}

// A rejected change leaves no number behind: a change adding two decisions, the second of a
// landed slug, is rejected whole, and a later change citing the first is rejected too (its
// decision never landed) instead of getting a number nothing holds. The two are units of one:
// a member of a chain is never picked after its predecessor's rejection.
func TestLanderForgetsTheNumbersOfARejectedChange(t *testing.T) {
	e := newLandEnv(t)
	dec := "docs/" + "decisions/"
	e.queue("a", "feat(client): a\n", "2026-01-01T09:00:00Z", map[string]string{dec + "D-new-z.md": "---\ntopic: x\n---\n\n# D-new-z\n"})
	if out, ok := e.land(); !ok {
		t.Fatalf("land a failed\n%s", out)
	}
	e.queue("two", "feat(client): two\n", "2026-01-01T10:00:00Z", map[string]string{
		dec + "D-new-a.md": "---\ntopic: x\n---\n\n# D-new-a\n",
		dec + "D-new-z.md": "---\ntopic: x\n---\n\n# D-new-z again\n",
	})
	e.queue("cites", "fix(kb): cites\n", "2026-01-01T11:00:00Z", map[string]string{"c.txt": "see D-new-a\n"})
	out, ok := e.land()
	if !ok {
		t.Fatalf("land failed\n%s", out)
	}
	if got := strings.Fields(e.refs("failed/")); strings.Join(got, " ") != "cites two" {
		t.Fatalf("both are rejected, cites for a citation nothing holds, got %v\n%s", got, out)
	}
	if strings.Contains(e.upGit("ls-tree", "-r", "--name-only", "main"), "c.txt") {
		t.Errorf("cites must not land with a number nothing holds")
	}
}

// remap_pick leaves test files alone, as the numbering of a train does.
func TestLanderMapsAnEditButNotInTestFiles(t *testing.T) {
	e := newLandEnv(t)
	dec := "docs/" + "decisions/"
	body := "---\ntopic: x\n---\n\n# D-new-x — X\n"
	e.queue("a", "feat(client): a\n", "2026-01-01T10:00:00Z", map[string]string{dec + "D-new-x.md": body, "x_test.go": "// D-new-x\n"})
	e.git("fetch", "-q", "origin")
	e.git("checkout", "-q", "-B", "work-b", "origin/ready/a")
	e.write(filepath.Join(e.work, dec, "D-new-x.md"), body+"Two.\n")
	e.write(filepath.Join(e.work, "x_test.go"), "// D-new-x\n// more D-new-x\n")
	e.git("add", ".")
	e.git("commit", "-q", "-m", "fix(client): b\n", "-m", "Review: full, 1 rounds")
	e.git("push", "-q", "--force", "origin", "HEAD:refs/heads/ready/b")
	e.git("checkout", "-q", "main")
	e.git("fetch", "-q", "origin")
	e.git("update-ref", "refs/lander-hold", "origin/ready/b")
	e.upGit("update-ref", "-d", "refs/heads/ready/b")
	if out, ok := e.land(); !ok {
		t.Fatalf("land a failed\n%s", out)
	}
	e.git("push", "-q", "--force", "origin", "refs/lander-hold:refs/heads/ready/b")
	out, ok := e.land()
	if !ok || e.refs("failed/") != "" {
		t.Fatalf("b must land, failed=%q\n%s", e.refs("failed/"), out)
	}
	if got := e.upGit("show", "main:x_test.go"); got != "// D-new-x\n// more D-new-x" {
		t.Errorf("the test file is not rewritten, got %q", got)
	}
}

// remap_pick leaves a testdata/ file alone too.
func TestLanderMapsAnEditButNotInTestdata(t *testing.T) {
	e := newLandEnv(t)
	dec := "docs/" + "decisions/"
	body := "---\ntopic: x\n---\n\n# D-new-x — X\n"
	fx := "x/testdata/fixture.txt"
	e.queue("a", "feat(client): a\n", "2026-01-01T10:00:00Z", map[string]string{dec + "D-new-x.md": body, fx: "// D-new-x\n"})
	e.git("fetch", "-q", "origin")
	e.git("checkout", "-q", "-B", "work-b", "origin/ready/a")
	e.write(filepath.Join(e.work, dec, "D-new-x.md"), body+"Two.\n")
	e.write(filepath.Join(e.work, fx), "// D-new-x\n// more D-new-x\n")
	e.git("add", ".")
	e.git("commit", "-q", "-m", "fix(client): b\n", "-m", "Review: full, 1 rounds")
	e.git("push", "-q", "--force", "origin", "HEAD:refs/heads/ready/b")
	e.git("checkout", "-q", "main")
	e.git("fetch", "-q", "origin")
	e.git("update-ref", "refs/lander-hold", "origin/ready/b")
	e.upGit("update-ref", "-d", "refs/heads/ready/b")
	if out, ok := e.land(); !ok {
		t.Fatalf("land a failed\n%s", out)
	}
	e.git("push", "-q", "--force", "origin", "refs/lander-hold:refs/heads/ready/b")
	out, ok := e.land()
	if !ok || e.refs("failed/") != "" {
		t.Fatalf("b must land, failed=%q\n%s", e.refs("failed/"), out)
	}
	if got := e.upGit("show", "main:"+fx); got != "// D-new-x\n// more D-new-x" {
		t.Errorf("the testdata file is not rewritten, got %q", got)
	}
}

// pushTrain puts train/<name> on origin, one commit on main with no PR, committed at date
// (now when empty): another machine's lander gating it, or one that died.
func (e *landEnv) pushTrain(name, date string) {
	e.t.Helper()
	cmd := exec.Command("git", "--git-dir="+e.up, "commit-tree", e.upGit("rev-parse", "main^{tree}"), "-p", "main", "-m", "chore(dev): train "+name)
	cmd.Env = e.env
	if date != "" {
		cmd.Env = append(append([]string{}, e.env...), "GIT_COMMITTER_DATE="+date)
	}
	out, err := cmd.Output()
	if err != nil {
		e.t.Fatalf("commit-tree: %v", err)
	}
	e.upGit("update-ref", "refs/heads/train/"+name, strings.TrimSpace(string(out)))
}

// withLandMarker is land.sh with a line that prints marker and the root the lander recorded,
// once its pid and root are written, and removes $FAKE_RM when it is set.
func withLandMarker(t *testing.T, script, marker string) string {
	t.Helper()
	at := "# reject <slug> <sha> <reason>: the change leaves the queue"
	if strings.Count(script, at) != 1 {
		t.Fatalf("land.sh has no single %q", at)
	}
	return strings.Replace(script, at, `echo "`+marker+`, root $(cat "$state/root" 2>/dev/null)" >&2
[ -z "${FAKE_RM:-}" ] || rm -rf "$FAKE_RM"
`+at, 1)
}

// A lander started from a worktree made before a lander fix landed runs origin/main's
// land.sh, from its own checkout: the worktree can go while it gates. LAND_SELF=1 runs the
// scripts beside it.
func TestLanderRunsMainsScriptsFromAnOlderWorktree(t *testing.T) {
	e := newLandEnv(t)
	wt := filepath.Join(e.tmp, "wt")
	e.git("worktree", "add", "-q", "-b", "feat/old", wt, "main")
	marker := "land: marker: main's land.sh"
	e.git("checkout", "-q", "-B", "fix", "origin/main")
	e.write(filepath.Join(e.work, "scripts", "land.sh"), withLandMarker(t, readTmp(t, e.work, filepath.Join("scripts", "land.sh")), marker))
	e.git("commit", "-q", "-am", "fix(dev): the lander fix")
	e.git("push", "-q", "origin", "fix:main")
	e.git("checkout", "-q", "main")
	src := filepath.Join(e.tmp, "lander", "src")

	if out, ok := e.land(); !ok || strings.Contains(out, marker) {
		t.Fatalf("LAND_SELF=1 runs this clone's land.sh (ok=%v):\n%s", ok, out)
	}
	if _, err := os.Stat(src); err == nil {
		t.Errorf("LAND_SELF=1 makes no checkout of main")
	}

	e.queue("a", "fix(kb): a\n", "2026-01-01T10:00:00Z", map[string]string{"qa.txt": "a\n"})
	cmd := exec.Command("bash", filepath.Join(wt, "scripts", "land.sh"), "--gate", "host")
	cmd.Dir, cmd.Env = wt, append(append([]string{}, e.env...), "LAND_SELF=", "FAKE_RM="+wt)
	b, err := cmd.CombinedOutput()
	out := string(b)
	if err != nil {
		t.Fatalf("land.sh from the worktree: %v\n%s", err, out)
	}
	if !strings.Contains(out, marker+", root "+src+"\n") {
		t.Errorf("the lander runs main's land.sh from %s:\n%s", src, out)
	}
	if got := strings.Join(e.mainSubjects(), " | "); got != "fix(dev): the lander fix | fix(kb): a" {
		t.Errorf("main holds %q: the change lands with the worktree gone\n%s", got, out)
	}
	if _, err := os.Stat(wt); err == nil {
		t.Errorf("the marker line removes the worktree before the gate")
	}
	if strings.Contains(out, "--gate host") || !strings.Contains(out, "gate: host, the build host") {
		t.Errorf("main's land.sh gets the same --gate:\n%s", out)
	}
}

// A lander change lands in one train: the next train is composed and gated by the new land.sh.
func TestLanderRunsMainsNewScriptsBetweenTrains(t *testing.T) {
	e := newLandEnv(t)
	marker := "land: marker: the new land.sh"
	e.queue("lander-fix", "fix(dev): lander fix\n", "2026-01-01T10:00:00Z", map[string]string{
		"scripts/land.sh": withLandMarker(t, readTmp(t, e.work, filepath.Join("scripts", "land.sh")), marker),
	})
	e.queue("b", "fix(kb): b\n", "2026-01-01T11:00:00Z", map[string]string{"qb.txt": "b\n"})
	out, rc := e.landArgs([]string{"--gate", "host"}, "LAND_SELF=", "LAND_MAX=1")
	if rc != 0 {
		t.Fatalf("land failed (rc=%d)\n%s", rc, out)
	}
	if got := strings.Join(e.mainSubjects(), " | "); got != "fix(dev): lander fix | fix(kb): b" {
		t.Fatalf("main holds %q\n%s", got, out)
	}
	changed, m, second := strings.Index(out, "main's scripts changed: running them"), strings.Index(out, marker), strings.Index(out, "1 change(s): b [gate: host]")
	if changed < 0 || m < changed || second < m || strings.Count(out, marker) != 1 {
		t.Errorf("after the first train the lander runs the new land.sh, which lands b:\n%s", out)
	}
}

// A train branch with no open PR and a young tip is another machine's lander gating it: the
// lander waits on it as on an open PR, then stops naming it, the queue untouched.
func TestLanderWaitsOnATrainAnotherMachineGates(t *testing.T) {
	e := newLandEnv(t)
	e.queue("a", "fix(kb): a\n", "2026-01-01T10:00:00Z", map[string]string{"qa.txt": "a\n"})
	e.pushTrain("20260101-000000", "")
	out, ok := e.land("LAND_OPEN_TRIES=1")
	if ok || !strings.Contains(out, "train/20260101-000000 is on origin with no open PR: another machine's lander is gating it, waiting") ||
		!strings.Contains(out, "train/20260101-000000 is still on origin after the wait") {
		t.Fatalf("the lander waits, then stops naming the branch (ok=%v):\n%s", ok, out)
	}
	if e.refs("ready/") != "a" || e.refs("train/") != "20260101-000000" || strings.Contains(e.makeLog(), "gate") {
		t.Errorf("nothing gated, the queue and the branch untouched: ready=%q train=%q\n%s", e.refs("ready/"), e.refs("train/"), out)
	}
}

// A train branch with no open PR whose tip is older than LAND_TRAIN_STALE was left by a
// lander that died: it is deleted and the train lands.
func TestLanderDeletesATrainWhoseLanderDied(t *testing.T) {
	e := newLandEnv(t)
	e.queue("a", "fix(kb): a\n", "2026-01-01T10:00:00Z", map[string]string{"qa.txt": "a\n"})
	e.pushTrain("20200101-000000", "2020-01-01T00:00:00Z")
	out, ok := e.land()
	if !ok || !strings.Contains(out, "train/20200101-000000 is a train with no open PR, its tip ") || !strings.Contains(out, "its lander died, deleted") {
		t.Fatalf("the stale train is deleted (ok=%v):\n%s", ok, out)
	}
	if got := strings.Join(e.mainSubjects(), " | "); got != "fix(kb): a" || e.refs("train/") != "" {
		t.Errorf("the train lands and no train branch is left: main %q, train %q\n%s", got, e.refs("train/"), out)
	}
}

// This machine's own train left on origin (a lander that died after its push) is deleted at
// once, however young.
func TestLanderDeletesItsOwnLeftoverTrain(t *testing.T) {
	e := newLandEnv(t)
	e.queue("a", "fix(kb): a\n", "2026-01-01T10:00:00Z", map[string]string{"qa.txt": "a\n"})
	e.pushTrain("own", "")
	e.write(filepath.Join(e.tmp, "lander", "branch"), "train/own\n")
	out, ok := e.land()
	if !ok || !strings.Contains(out, "train/own is a train this machine left on origin with no open PR: deleted") {
		t.Fatalf("the leftover is deleted (ok=%v):\n%s", ok, out)
	}
	if got := strings.Join(e.mainSubjects(), " | "); got != "fix(kb): a" || e.refs("train/") != "" {
		t.Errorf("the train lands and no train branch is left: main %q, train %q\n%s", got, e.refs("train/"), out)
	}
	if _, err := os.Stat(filepath.Join(e.tmp, "lander", "branch")); err == nil {
		t.Errorf("the branch file goes once its train ended")
	}
}

// A train another machine pushes while this lander composes is seen right before the gate:
// the composition is dropped, nothing gated or pushed.
func TestLanderDropsItsCompositionForATrainPushedMeanwhile(t *testing.T) {
	e := newLandEnv(t)
	e.queue("a", "fix(kb): a\n", "2026-01-01T10:00:00Z", map[string]string{"qa.txt": "a\n"})
	out, ok := e.land("FAKE_TRAIN_ON_COMPOSE=x", "LAND_OPEN_TRIES=1")
	if ok || !strings.Contains(out, "train/x appeared on origin while train/") || !strings.Contains(out, "train/x is still on origin after the wait") {
		t.Fatalf("the composition is dropped, then the lander waits on train/x (ok=%v):\n%s", ok, out)
	}
	if e.refs("ready/") != "a" || e.refs("train/") != "x" || strings.Contains(e.makeLog(), "gate") {
		t.Errorf("nothing gated or pushed: ready=%q train=%q\n%s", e.refs("ready/"), e.refs("train/"), out)
	}
}

// --daemon starts no lander while another machine's is gating a train (make ready on a second
// machine queues only); LAND_FORCE=1 starts one anyway, which does not wait on that train.
func TestLanderDaemonStartsNothingWhileAnotherMachineGates(t *testing.T) {
	e := newLandEnv(t)
	state := filepath.Join(e.tmp, "lander")
	e.pushTrain("20260101-000000", "")
	out, rc := e.landArgs([]string{"--daemon"})
	if rc != 0 || !strings.Contains(out, "land: a lander elsewhere is gating train/20260101-000000: not started here, the change is queued and that lander takes it (make land-status; if that lander is dead: LAND_FORCE=1 make land, or delete train/20260101-000000 on origin)") ||
		strings.Contains(out, "lander started") {
		t.Fatalf("--daemon says why it starts nothing (rc=%d):\n%s", rc, out)
	}
	if _, err := os.Stat(filepath.Join(state, "pid")); err == nil {
		t.Errorf("no pid is written")
	}
	out, rc = e.landArgs([]string{"--daemon"}, "LAND_FORCE=1", "LAND_OPEN_TRIES=0")
	if rc != 0 || !strings.Contains(out, "land: lander started (pid ") {
		t.Fatalf("LAND_FORCE=1 starts one (rc=%d):\n%s", rc, out)
	}
	deadline := time.Now().Add(time.Minute)
	for !strings.Contains(readTmpOrEmpty(state, "log"), "the queue is empty") {
		if time.Now().After(deadline) {
			t.Fatalf("the forced lander did not go past the train:\n%s", readTmpOrEmpty(state, "log"))
		}
		time.Sleep(100 * time.Millisecond)
	}
	for _, err := os.Stat(filepath.Join(state, "pid")); err == nil; _, err = os.Stat(filepath.Join(state, "pid")) {
		if time.Now().After(deadline) {
			t.Fatal("the forced lander did not exit")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// A tag named origin/main, on origin or already in the clone, on a commit whose land.sh is not
// main's, chooses neither the scripts the lander runs nor the main it composes on: no fetch
// takes tags, and main is refs/remotes/origin/main spelled out. The change lands as usual.
func TestLanderIgnoresATagNamedOriginMain(t *testing.T) {
	e := newLandEnv(t)
	marker := "land: marker: a tag's land.sh"
	e.git("checkout", "-q", "-B", "evil", "origin/main")
	e.write(filepath.Join(e.work, "scripts", "land.sh"), withLandMarker(t, readTmp(t, e.work, filepath.Join("scripts", "land.sh")), marker))
	e.git("commit", "-q", "-am", "chore: not main")
	tagged := e.git("rev-parse", "HEAD")
	e.git("push", "-q", "origin", "evil")
	e.git("checkout", "-q", "main")
	src := filepath.Join(e.tmp, "lander", "src")
	queue := func(slug string) {
		e.queue(slug, "fix(kb): "+slug+"\n", "2026-01-01T10:00:00Z", map[string]string{"q" + slug + ".txt": slug + "\n"})
	}
	land := func(when, slug string) {
		t.Helper()
		out, rc := e.landArgs([]string{"--gate", "host"}, "LAND_SELF=")
		if rc != 0 || strings.Contains(out, marker) {
			t.Fatalf("%s: the lander runs main's land.sh and lands (rc=%d):\n%s", when, rc, out)
		}
		if got := e.mainSubjects(); got[len(got)-1] != "fix(kb): "+slug || strings.Contains(strings.Join(got, "|"), "not main") {
			t.Errorf("%s: %s lands on main, not on the tagged commit: %q", when, slug, got)
		}
		if got, want := e.run(src, "git", "rev-parse", "HEAD:scripts"), e.upGit("rev-parse", "main:scripts"); got != want {
			t.Errorf("%s: the checkout holds scripts %s, not main's %s", when, got, want)
		}
	}
	queue("a") // the harness's own fetch would take the tag: it comes after
	e.upGit("tag", "origin/main", tagged)
	land("a tag on origin", "a")
	if got := e.git("tag", "-l", "origin/main"); got != "" {
		t.Errorf("the lander's fetches take no tag, got %q", got)
	}
	e.upGit("tag", "-d", "origin/main") // the harness's queue cannot run with it in the clone
	queue("b")
	e.git("tag", "origin/main", tagged)
	land("a tag in the clone", "b")
}

// The wait on another machine's train has its own budget: polls already spent waiting on an
// open PR do not end it early.
func TestLanderWaitsOnAForeignTrainWithItsOwnBudget(t *testing.T) {
	e := newLandEnv(t)
	e.queue("a", "fix(kb): a\n", "2026-01-01T10:00:00Z", map[string]string{"qa.txt": "a\n"})
	head, _ := e.trainBranch("fork", "origin/main", "x")
	pr := `FAKE_GH_TRAINS=[{"number":42,"headRefName":"train/fork","headRefOid":"` + head + `","isCrossRepository":true,"baseRefName":"main"}]`
	out, ok := e.land(pr, "FAKE_GH_TRAINS_LISTS=2", "LAND_OPEN_TRIES=2")
	if ok || strings.Count(out, "train PR #42 is open") != 2 ||
		strings.Count(out, "train/fork is on origin with no open PR: another machine's lander is gating it, waiting") != 2 ||
		!strings.Contains(out, "train/fork is still on origin after the wait") {
		t.Fatalf("two polls on the PR, then two of its own on the branch (ok=%v):\n%s", ok, out)
	}
}

// When the open PRs cannot be listed, nothing is deleted: neither a stale train nor this
// machine's leftover, which stays this machine's when the next train is composed.
func TestLanderDeletesNothingWhenThePRListFails(t *testing.T) {
	e := newLandEnv(t)
	e.queue("a", "fix(kb): a\n", "2026-01-01T10:00:00Z", map[string]string{"qa.txt": "a\n"})
	e.pushTrain("20200101-000000", "2020-01-01T00:00:00Z")
	e.pushTrain("own", "")
	branch := filepath.Join(e.tmp, "lander", "branch")
	e.write(branch, "train/own\n")
	out, ok := e.land("FAKE_PR_LIST_FAILS=1")
	if !ok || strings.Contains(out, "deleted") {
		t.Fatalf("the train lands and nothing is deleted (ok=%v):\n%s", ok, out)
	}
	if got := strings.Join(e.mainSubjects(), " | "); got != "fix(kb): a" {
		t.Errorf("main holds %q\n%s", got, out)
	}
	if got := e.refs("train/"); got != "20200101-000000\nown" {
		t.Errorf("both train branches stay, got %q", got)
	}
	if got := readTmpOrEmpty(filepath.Dir(branch), "branch"); !strings.Contains(got, "train/own\n") {
		t.Errorf("the leftover stays this machine's, got %q", got)
	}
}

// make ready's path: --daemon from a worktree's script runs main's land.sh, detached, with the
// same --gate.
func TestLanderDaemonRunsMainsScripts(t *testing.T) {
	e := newLandEnv(t)
	marker := "land: marker: main's land.sh"
	e.git("checkout", "-q", "-B", "fix", "origin/main")
	e.write(filepath.Join(e.work, "scripts", "land.sh"), withLandMarker(t, readTmp(t, e.work, filepath.Join("scripts", "land.sh")), marker))
	e.git("commit", "-q", "-am", "fix(dev): the lander fix")
	e.git("push", "-q", "origin", "fix:main")
	e.git("checkout", "-q", "main")
	e.queue("a", "fix(kb): a\n", "2026-01-01T10:00:00Z", map[string]string{"qa.txt": "a\n"})
	state := filepath.Join(e.tmp, "lander")
	out, rc := e.landArgs([]string{"--daemon", "--gate", "host"}, "LAND_SELF=")
	if rc != 0 || !strings.Contains(out, "land: lander started (pid ") || !strings.Contains(out, ", gate: host)") {
		t.Fatalf("--daemon (rc=%d):\n%s", rc, out)
	}
	deadline := time.Now().Add(time.Minute)
	for {
		_, err := os.Stat(filepath.Join(state, "pid"))
		if strings.Contains(readTmpOrEmpty(state, "log"), "the queue is empty") && os.IsNotExist(err) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the detached lander did not finish:\n%s", readTmpOrEmpty(state, "log"))
		}
		time.Sleep(100 * time.Millisecond)
	}
	log := readTmpOrEmpty(state, "log")
	if !strings.Contains(log, marker+", root "+filepath.Join(state, "src")+"\n") || !strings.Contains(log, "1 change(s): a [gate: host]") {
		t.Errorf("the detached lander is main's, on the build host:\n%s", log)
	}
	if got := strings.Join(e.mainSubjects(), " | "); got != "fix(dev): the lander fix | fix(kb): a" {
		t.Errorf("main holds %q\n%s", got, log)
	}
	if _, err := os.Stat(filepath.Join(state, "src.lock")); err == nil {
		t.Errorf("the start lock is dropped once the lander's pid file guards the checkout")
	}
}

// One starting lander at a time resets the checkout: a second one says a lander runs and exits
// 0. The start lock is taken over when its holder died, when it is older than
// LAND_START_TIMEOUT though its holder lives (stuck on the network), and when it is a directory,
// not one of these links, older than 5 s.
func TestLanderStartsAloneWhileAnotherResetsItsCheckout(t *testing.T) {
	e := newLandEnv(t)
	state := filepath.Join(e.tmp, "lander")
	src, lock := filepath.Join(state, "src"), filepath.Join(state, "src.lock")
	if err := os.MkdirAll(state, 0o755); err != nil {
		t.Fatal(err)
	}
	link := func(pid int, age time.Duration) {
		t.Helper()
		_ = os.RemoveAll(lock)
		if err := os.Symlink(strconv.Itoa(pid)+"."+strconv.FormatInt(time.Now().Add(-age).Unix(), 10), lock); err != nil {
			t.Fatal(err)
		}
	}
	refused := func(when string) {
		t.Helper()
		out, rc := e.landArgs([]string{"--gate", "host"}, "LAND_SELF=")
		if rc != 0 || !strings.Contains(out, "land: a lander already runs here (starting, pid ") {
			t.Fatalf("%s: the start lock is held (rc=%d):\n%s", when, rc, out)
		}
		if _, err := os.Stat(src); err == nil {
			t.Fatalf("%s: the checkout is not touched", when)
		}
	}
	taken := func(when string) {
		t.Helper()
		out, rc := e.landArgs([]string{"--gate", "host"}, "LAND_SELF=")
		if rc != 0 || !strings.Contains(out, "the queue is empty") {
			t.Fatalf("%s: the lock is taken over (rc=%d):\n%s", when, rc, out)
		}
		if _, err := os.Stat(filepath.Join(src, "scripts", "land.sh")); err != nil {
			t.Errorf("%s: the checkout is made: %v", when, err)
		}
		if _, err := os.Lstat(lock); err == nil {
			t.Errorf("%s: the lock is dropped", when)
		}
		_ = os.RemoveAll(src)
		e.git("worktree", "prune")
	}
	link(os.Getpid(), 0)
	refused("a live holder")
	if out, _ := e.landArgs([]string{"--gate", "host"}, "LAND_SELF="); !strings.Contains(out, "(starting, pid "+strconv.Itoa(os.Getpid())+")") {
		t.Errorf("the message names the holder:\n%s", out)
	}
	link(os.Getpid(), 400*time.Second)
	taken("a live holder past LAND_START_TIMEOUT")
	dead := exec.Command("true")
	if err := dead.Run(); err != nil {
		t.Fatal(err)
	}
	link(dead.Process.Pid, 0)
	taken("a holder that died")
	if err := os.Mkdir(lock, 0o755); err != nil {
		t.Fatal(err)
	}
	refused("a fresh directory with no pid")
	old := time.Now().Add(-time.Minute)
	if err := os.Chtimes(lock, old, old); err != nil {
		t.Fatal(err)
	}
	taken("a directory with no pid, older than 5 s")
}

// A lander run with LAND_FORCE=1 does not wait on a young train with no open PR (a dead
// lander's, which would stall it until it goes stale): it lands.
func TestLanderForcedDoesNotWaitOnATrain(t *testing.T) {
	e := newLandEnv(t)
	e.queue("a", "fix(kb): a\n", "2026-01-01T10:00:00Z", map[string]string{"qa.txt": "a\n"})
	e.pushTrain("20260101-000000", "")
	out, ok := e.land("LAND_FORCE=1", "LAND_OPEN_TRIES=0")
	if !ok || strings.Contains(out, "waiting") || strings.Contains(out, "appeared on origin") {
		t.Fatalf("the forced lander lands past the train (ok=%v):\n%s", ok, out)
	}
	if got := strings.Join(e.mainSubjects(), " | "); got != "fix(kb): a" {
		t.Errorf("main holds %q\n%s", got, out)
	}
}

// queueAfter pushes ready/<slug> as `make ready AFTER=<after>` does: one commit on the queued
// commit of <after> adding files, with message msg and the Lander-After trailer, added as
// ready.sh adds it (git interpret-trailers: in msg's trailer block, when it ends with one).
// withReview adds the review record the lander requires (`Review: full, 1 rounds`, as make ready
// REVIEW= writes it) to a queued message that has none.
func (e *landEnv) withReview(msg string) string {
	e.t.Helper()
	if regexp.MustCompile(`(?im)^review\s*:`).MatchString(msg) {
		return msg
	}
	tr := exec.Command("git", "interpret-trailers", "--trailer", "Review: full, 1 rounds")
	tr.Dir, tr.Env, tr.Stdin = e.work, e.env, strings.NewReader(msg)
	b, err := tr.Output()
	if err != nil {
		e.t.Fatalf("interpret-trailers: %v", err)
	}
	return string(b)
}

func (e *landEnv) queueAfter(slug, after, msg, date string, files map[string]string) {
	e.t.Helper()
	tr := exec.Command("git", "interpret-trailers", "--trailer", "Lander-After: "+after)
	tr.Dir, tr.Env, tr.Stdin = e.work, e.env, strings.NewReader(msg)
	b, err := tr.Output()
	if err != nil {
		e.t.Fatalf("interpret-trailers: %v", err)
	}
	msg = e.withReview(string(b))
	e.git("fetch", "-q", "origin")
	e.git("checkout", "-q", "-B", "work-"+slug, "origin/ready/"+after)
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

// queueChain queues a -> b -> c, a minute apart from 10:00, each adding its own file.
func (e *landEnv) queueChain(msgs map[string]string) {
	e.t.Helper()
	msg := func(s string) string {
		if m, ok := msgs[s]; ok {
			return m
		}
		return "fix(kb): " + s + "\n"
	}
	e.queue("a", msg("a"), "2026-01-01T10:00:00Z", map[string]string{"qa.txt": "a\n"})
	e.queueAfter("b", "a", msg("b"), "2026-01-01T10:01:00Z", map[string]string{"qb.txt": "b\n"})
	e.queueAfter("c", "b", msg("c"), "2026-01-01T10:02:00Z", map[string]string{"qc.txt": "c\n"})
}

// A chain is one unit: its verdict is its tip's pre-check, so a chain whose first two changes'
// pre-checks are red and whose tip's is green lands in one train, and the pre-checks of the
// members that are not tips are never read (the plan is checked and reviewed on its tip).
func TestLanderLandsAChainOnItsTipsPrecheck(t *testing.T) {
	e := newLandEnv(t)
	e.queueChain(nil)
	a, b, c := e.upGit("rev-parse", "ready/a"), e.upGit("rev-parse", "ready/b"), e.upGit("rev-parse", "ready/c")
	out, ok := e.land("FAKE_PRECHECK_RED=" + a + " " + b)
	if !ok {
		t.Fatalf("land failed\n%s", out)
	}
	if got := strings.Join(e.mainSubjects(), " | "); got != "fix(kb): a | fix(kb): b | fix(kb): c" {
		t.Fatalf("main holds %q, want the chain in one train\n%s", got, out)
	}
	if n := strings.Count(e.makeLog(), "-k gate"); n != 1 || !strings.Contains(out, "3 change(s): a b c") {
		t.Errorf("one train, one gate: %d gates\n%s", n, out)
	}
	gh := e.log()
	if strings.Contains(gh, "--commit "+a) || strings.Contains(gh, "--commit "+b) || !strings.Contains(gh, "--commit "+c) {
		t.Errorf("only the tip's pre-check is read:\n%s", gh)
	}
}

// A chain whose tip's pre-check is red is gated alone, as a unit of three, after the green
// unit queued later.
func TestLanderGatesAChainWithARedTipAloneAsAUnit(t *testing.T) {
	e := newLandEnv(t)
	e.queueChain(nil)
	e.queue("x", "fix(kb): x\n", "2026-01-01T11:00:00Z", map[string]string{"qx.txt": "x\n"})
	out, ok := e.land("FAKE_PRECHECK_RED=" + e.upGit("rev-parse", "ready/c"))
	if !ok {
		t.Fatalf("land failed\n%s", out)
	}
	if got := strings.Join(e.mainSubjects(), " | "); got != "fix(kb): x | fix(kb): a | fix(kb): b | fix(kb): c" {
		t.Fatalf("main holds %q, want x, then the chain alone\n%s", got, out)
	}
	if !strings.Contains(out, "the chain a..c (3 changes): the hosted pre-check of its tip is red: gated alone") {
		t.Errorf("the lander must say why the chain is alone:\n%s", out)
	}
	if n := strings.Count(e.makeLog(), "-k gate"); n != 2 {
		t.Errorf("one gate for x, one for the chain: %d\n%s", n, e.makeLog())
	}
}

// A held tip holds its whole chain: no train lands a strict prefix of a unit.
func TestLanderKeepsAChainWithAHeldTipQueued(t *testing.T) {
	e := newLandEnv(t)
	e.queueChain(map[string]string{"c": "fix(kb): c\n\nLander-Hold: yes\n"})
	e.queue("x", "fix(kb): x\n", "2026-01-01T11:00:00Z", map[string]string{"qx.txt": "x\n"})
	out, ok := e.land()
	if !ok {
		t.Fatalf("land failed\n%s", out)
	}
	if got := strings.Join(e.mainSubjects(), " | "); got != "fix(kb): x" {
		t.Fatalf("main holds %q, want only x\n%s", got, out)
	}
	if got := strings.Join(strings.Fields(e.refs("ready/")), " "); got != "a b c" {
		t.Errorf("the held chain stays queued whole, got %q", got)
	}
}

// A member that does not apply rejects that member only: the unit leaves the train, its other
// members stay queued and are blocked by the rejected one (a second run gates nothing of it),
// and once it is queued again (its failed/ ref still there) the unit lands whole.
func TestLanderRejectsOnlyTheConflictingMemberOfAChain(t *testing.T) {
	e := newLandEnv(t)
	e.queue("a", "fix(kb): a\n", "2026-01-01T10:00:00Z", map[string]string{"qa.txt": "a\n"})
	e.queueAfter("b", "a", "fix(kb): b\n", "2026-01-01T10:01:00Z", map[string]string{"a.txt": "mine\n"})
	e.queueAfter("c", "b", "fix(kb): c\n", "2026-01-01T10:02:00Z", map[string]string{"qc.txt": "c\n"})
	e.git("checkout", "-q", "-B", "move", "origin/main")
	e.write(filepath.Join(e.work, "a.txt"), "theirs\n")
	e.git("commit", "-q", "-am", "fix: move a")
	e.git("push", "-q", "origin", "move:main")
	e.git("checkout", "-q", "main")
	for run := 1; run <= 2; run++ {
		out, ok := e.land()
		if !ok {
			t.Fatalf("run %d: land failed\n%s", run, out)
		}
		if got := strings.Join(e.mainSubjects(), " | "); got != "fix: move a" {
			t.Fatalf("run %d: main holds %q, want nothing of the chain\n%s", run, got, out)
		}
		if e.refs("failed/") != "b" || strings.Join(strings.Fields(e.refs("ready/")), " ") != "a c" {
			t.Fatalf("run %d: b alone is rejected, a and c stay queued: failed=%q ready=%q\n%s", run, e.refs("failed/"), e.refs("ready/"), out)
		}
		if strings.Contains(e.makeLog(), "-k gate") {
			t.Fatalf("run %d: nothing of the unit is gated\n%s", run, e.makeLog())
		}
	}
	if r := e.upGit("log", "-1", "--format=%s", "failed/b"); !strings.HasPrefix(r, "lander: conflict") || !strings.HasSuffix(r, " (chain a..c)") {
		t.Errorf("the reason is the conflict, with the chain, got %q", r)
	}
	e.queueAfter("b", "a", "fix(kb): b\n", "2026-01-01T13:00:00Z", map[string]string{"qb.txt": "b\n"})
	out, ok := e.land()
	if !ok {
		t.Fatalf("land failed\n%s", out)
	}
	if got := strings.Join(e.mainSubjects(), " | "); got != "fix: move a | fix(kb): a | fix(kb): b | fix(kb): c" {
		t.Fatalf("main holds %q, want the chain once b is queued again\n%s", got, out)
	}
	if e.refs("failed/") != "" || e.refs("ready/") != "" {
		t.Errorf("the queue and the rejection are gone: failed=%q ready=%q", e.refs("failed/"), e.refs("ready/"))
	}
}

// A red train is bisected over units: [x, a -> b, y] with b breaking the gate splits only at
// unit boundaries, so a never lands without b; b, the chain's tip, is rejected with the chain
// sentence, x lands, y lands in the next train and a stays queued. With full gates and with
// the probe of confirmed tests alike.
func TestLanderBisectsAtUnitBoundaries(t *testing.T) {
	for _, tc := range []struct {
		marker string
		gates  int
	}{{"BAD", 5}, {"CONFIRMED", 3}} {
		t.Run(tc.marker, func(t *testing.T) {
			e := newLandEnv(t)
			e.queue("x", "fix(kb): x\n", "2026-01-01T10:00:00Z", map[string]string{"qx.txt": "x\n"})
			e.queue("a", "fix(kb): a\n", "2026-01-01T11:00:00Z", map[string]string{"qa.txt": "a\n"})
			e.queueAfter("b", "a", "fix(kb): b\n", "2026-01-01T11:01:00Z", map[string]string{tc.marker: "x\n"})
			e.queue("y", "fix(kb): y\n", "2026-01-01T12:00:00Z", map[string]string{"qy.txt": "y\n"})
			out, ok := e.land()
			if !ok {
				t.Fatalf("land failed\n%s", out)
			}
			if got := strings.Join(e.mainSubjects(), " | "); got != "fix(kb): x | fix(kb): y" {
				t.Fatalf("main holds %q, want x then y\n%s", got, out)
			}
			if e.refs("failed/") != "b" || e.refs("ready/") != "a" {
				t.Fatalf("b is rejected, a stays queued: failed=%q ready=%q\n%s", e.refs("failed/"), e.refs("ready/"), out)
			}
			if r := e.upGit("log", "-1", "--format=%s", "failed/b"); !strings.HasPrefix(r, "lander: the train gate is red with the chain a..b and green without it (tests: ") {
				t.Errorf("the reason names the chain, got %q", r)
			}
			if n := strings.Count(e.makeLog(), "-k gate"); n != tc.gates {
				t.Errorf("%d full gates, want %d\n%s", n, tc.gates, e.makeLog())
			}
		})
	}
}

// LAND_MAX counts changes and a train takes whole units: with LAND_MAX=2 and units of 1, 2 and
// 1 changes the first train is the two units of one; a unit longer than LAND_MAX is gated alone.
func TestLanderComposesWholeUnitsWithinLandMax(t *testing.T) {
	e := newLandEnv(t)
	e.queue("p", "fix(kb): p\n", "2026-01-01T09:00:00Z", map[string]string{"qp.txt": "p\n"})
	e.queue("a", "fix(kb): a\n", "2026-01-01T10:00:00Z", map[string]string{"qa.txt": "a\n"})
	e.queueAfter("b", "a", "fix(kb): b\n", "2026-01-01T10:01:00Z", map[string]string{"qb.txt": "b\n"})
	e.queue("q", "fix(kb): q\n", "2026-01-01T11:00:00Z", map[string]string{"qq.txt": "q\n"})
	out, ok := e.land("LAND_MAX=2")
	if !ok {
		t.Fatalf("land failed\n%s", out)
	}
	if got := strings.Join(e.mainSubjects(), " | "); got != "fix(kb): p | fix(kb): q | fix(kb): a | fix(kb): b" {
		t.Fatalf("main holds %q, want p and q, then the chain\n%s", got, out)
	}
	if !strings.Contains(out, "2 change(s): p q [gate") || !strings.Contains(out, "2 change(s): a b [gate") {
		t.Errorf("two trains of two changes each:\n%s", out)
	}
	e2 := newLandEnv(t)
	e2.queue("a", "fix(kb): a\n", "2026-01-01T10:00:00Z", map[string]string{"qa.txt": "a\n"})
	e2.queueAfter("b", "a", "fix(kb): b\n", "2026-01-01T10:01:00Z", map[string]string{"qb.txt": "b\n"})
	out, ok = e2.land("LAND_MAX=1")
	if !ok || !strings.Contains(out, "the chain a..b has 2 changes, more than LAND_MAX (1): gated alone") ||
		!strings.Contains(out, "2 change(s): a b [gate") {
		t.Fatalf("a unit longer than LAND_MAX is one train (ok=%v)\n%s", ok, out)
	}
}

// A member of a chain rejected after its numbering (a decision whose slug main holds) takes its
// whole unit out of the train: the earlier member leaves it, its decision gets no number, the
// unit stays queued but the rejected member, and the next unit's decision takes the first free
// number.
func TestLanderForgetsTheNumbersOfARejectedChain(t *testing.T) {
	e := newLandEnv(t)
	dec := "docs/" + "decisions/"
	e.queue("z", "feat(client): z\n", "2026-01-01T09:00:00Z", map[string]string{dec + "D-new-z.md": "---\ntopic: x\n---\n\n# D-new-z\n"})
	if out, ok := e.land(); !ok {
		t.Fatalf("land z failed\n%s", out)
	}
	e.queue("a", "feat(client): a\n", "2026-01-01T10:00:00Z", map[string]string{dec + "D-new-a.md": "---\ntopic: x\n---\n\n# D-new-a\n"})
	e.queueAfter("b", "a", "feat(client): b\n", "2026-01-01T10:01:00Z", map[string]string{dec + "D-new-z.md": "---\ntopic: x\n---\n\n# D-new-z again\n"})
	e.queue("x", "feat(client): x\n", "2026-01-01T11:00:00Z", map[string]string{dec + "D-new-x.md": "---\ntopic: x\n---\n\n# D-new-x\n"})
	out, ok := e.land()
	if !ok {
		t.Fatalf("land failed\n%s", out)
	}
	if e.refs("failed/") != "b" || e.refs("ready/") != "a" {
		t.Fatalf("b alone is rejected, a stays queued: failed=%q ready=%q\n%s", e.refs("failed/"), e.refs("ready/"), out)
	}
	if r := e.upGit("log", "-1", "--format=%s", "failed/b"); !strings.HasSuffix(r, " (chain a..b)") {
		t.Errorf("the reason names the chain, got %q", r)
	}
	if got := strings.Join(e.mainSubjects(), " | "); got != "feat(client): z | feat(client): x" {
		t.Fatalf("main holds %q, want z then x\n%s", got, out)
	}
	if files := e.upGit("ls-tree", "--name-only", "main", dec); !strings.Contains(files, dec+"D12-x.md") || strings.Contains(files, "-a.md") {
		t.Errorf("x takes D12, the number a gave back:\n%s", files)
	}
}

// A feat at the tip of a chain freezes the whole chain while the freeze is open: its fix
// members stay queued with it, and a fix queued alone lands.
func TestLanderKeepsAFrozenChainQueued(t *testing.T) {
	e := newLandEnv(t)
	e.queueChain(map[string]string{"c": "feat(kb): c\n"})
	e.queue("x", "fix(kb): x\n", "2026-01-01T11:00:00Z", map[string]string{"qx.txt": "x\n"})
	out, ok := e.land("FAKE_FREEZE=0")
	if !ok {
		t.Fatalf("land failed\n%s", out)
	}
	if got := strings.Join(e.mainSubjects(), " | "); got != "fix(kb): x" {
		t.Fatalf("main holds %q, want only x\n%s", got, out)
	}
	if got := strings.Join(strings.Fields(e.refs("ready/")), " "); got != "a b c" {
		t.Errorf("the frozen chain stays queued whole, got %q", got)
	}
}

// A queued change that main already holds is dequeued with any rejection of its slug left on
// failed/ (an earlier version of it), which would otherwise block a change queued AFTER it: by
// its diff and by its Lander-Slug and Lander-Tip trailers alike.
func TestLanderDeletesTheRejectionOfAChangeAlreadyOnMain(t *testing.T) {
	for _, tc := range []struct{ name, msg, log string }{
		{"diff", "fix(kb): a\n", "a is already on main: dequeued"},
		{"trailers", "fix(kb): a\n\nLander-Slug: a\nLander-Tip: 1111111111111111111111111111111111111111\n", "a is already on main (a commit there carries its Lander-Slug and Lander-Tip): dequeued"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newLandEnv(t)
			e.queue("a", tc.msg, "2026-01-01T10:00:00Z", map[string]string{"qa.txt": "a\n"})
			e.landByHand("a")
			old := e.upGit("commit-tree", "ready/a^{tree}", "-p", "ready/a", "-m", "lander: conflict")
			e.upGit("update-ref", "refs/heads/failed/a", old)
			out, ok := e.land()
			if !ok || !strings.Contains(out, tc.log) {
				t.Fatalf("a is dequeued (ok=%v)\n%s", ok, out)
			}
			if q, f := e.refs("ready/"), e.refs("failed/"); q != "" || f != "" {
				t.Fatalf("queue %q and failed %q must be empty\n%s", q, f, out)
			}
			e.queue("c", "fix(kb): c\n\nLander-After: a\n", "2026-01-01T11:00:00Z", map[string]string{"qc.txt": "c\n"})
			if out, ok := e.land(); !ok || strings.Join(e.mainSubjects(), " | ") != "fix(kb): a | fix(kb): c" {
				t.Fatalf("c, queued after a, lands (ok=%v): main %q\n%s", ok, strings.Join(e.mainSubjects(), " | "), out)
			}
		})
	}
}

// make ready AFTER=<slug> cancels the pre-check still running on the change it follows: that
// change is no longer its unit's tip, so the lander never reads it, and the new tip's pre-check
// replays it too. Best effort: a cancel GitHub refuses is one warning, the change stays queued.
func TestReadyAfterCancelsThePredecessorsPrecheck(t *testing.T) {
	for _, fails := range []bool{false, true} {
		name := "the cancel succeeds"
		if fails {
			name = "the cancel fails"
		}
		t.Run(name, func(t *testing.T) {
			e := newLandEnv(t)
			e.queue("a", "fix(kb): a\n", "2026-01-01T10:00:00Z", map[string]string{"qa.txt": "a\n"})
			a := e.upGit("rev-parse", "ready/a")
			// The fake gh answers ready.sh's list with a run in progress and one completed.
			if err := os.Rename(filepath.Join(e.bin, "gh"), filepath.Join(e.bin, "gh-base")); err != nil {
				t.Fatal(err)
			}
			e.write(filepath.Join(e.bin, "gh"), `#!/bin/bash
case "$*" in
  "run list"*"--json databaseId,status"*)
    echo "$*" >> "$FAKE_GH_LOG"
    jq_expr=; prev=; for a in "$@"; do [ "$prev" = --jq ] && jq_expr=$a; prev=$a; done
    echo '[{"databaseId":91,"status":"in_progress"},{"databaseId":90,"status":"completed"}]' | jq -r "$jq_expr" ;;
  "run cancel"*) echo "$*" >> "$FAKE_GH_LOG"; [ -z "${FAKE_CANCEL_FAILS:-}" ] ;;
  *) exec "$(dirname "$0")/gh-base" "$@" ;;
esac
`)
			e.git("fetch", "-q", "origin")
			e.git("checkout", "-q", "-b", "feat/b", "origin/ready/a")
			e.write(filepath.Join(e.work, "qb.txt"), "b\n")
			e.git("add", "qb.txt")
			e.git("commit", "-q", "-m", "wip b")
			m := filepath.Join(e.tmp, "msg")
			e.write(m, "feat(kb): b\n")
			cmd := exec.Command("bash", filepath.Join(e.work, "scripts", "ready.sh"))
			cmd.Dir = e.work
			cmd.Env = append(append([]string{}, e.env...), "MSG="+m, "READY_NO_LANDER=1", "REVIEW=full, 1 rounds", "AFTER=a")
			if fails {
				cmd.Env = append(cmd.Env, "FAKE_CANCEL_FAILS=1")
			}
			out, err := cmd.CombinedOutput()
			if err != nil || e.upGit("rev-parse", "ready/b^") != a {
				t.Fatalf("b must be queued after a (err %v)\n%s", err, out)
			}
			log := e.log()
			if !strings.Contains(log, "--workflow ready-check.yml --commit "+a) || !strings.Contains(log, "run cancel -R up/proj 91") ||
				strings.Contains(log, "run cancel -R up/proj 90") {
				t.Errorf("ready.sh must cancel a's run in progress (91), not the completed one (90):\n%s", log)
			}
			warned := strings.Contains(string(out), "ready: warning: the pre-check of ready/a could not be cancelled")
			if warned != fails {
				t.Errorf("warning printed %v, want %v:\n%s", warned, fails, out)
			}
		})
	}
}

// decisionsOn lists the decision file names (no directory) on a ref of the upstream repository.
func (e *landEnv) decisionsOn(ref string) []string {
	e.t.Helper()
	var names []string
	for _, l := range strings.Fields(e.upGit("ls-tree", "--name-only", ref, "docs/"+"decisions/")) {
		names = append(names, strings.TrimPrefix(l, "docs/"+"decisions/"))
	}
	return names
}

// AC-P3-10, AC-P3-11. The pending decisions of a train are numbered once, after the picks: a
// later change of a chain that edits the lines around an earlier change's citation applies as
// written (no pick meets a renumbered line), and every landed commit shows its own decision
// numbered and no placeholder.
func TestLanderNumbersTheTrainOnceAfterThePicks(t *testing.T) {
	e := newLandEnv(t)
	dec := "docs/" + "decisions/"
	e.queue("a", "feat(client): a\n", "2026-01-01T10:00:00Z", map[string]string{
		dec + "D-new-x.md": "---\ntopic: x\n---\n\n# D-new-x — X\n",
		"notes.md":         "first line\ncites D-new-x here\nlast line\n",
	})
	e.queueAfter("b", "a", "fix(client): b\n", "2026-01-01T10:01:00Z", map[string]string{
		"notes.md":         "first line edited\ncites D-new-x here\nlast line edited\n",
		dec + "D-new-x.md": "---\ntopic: x\n---\n\n# D-new-x — X\n\nMore.\n",
	})
	out, ok := e.land()
	if !ok || e.refs("failed/") != "" || e.refs("ready/") != "" {
		t.Fatalf("the chain lands: ok=%v failed=%q ready=%q\n%s", ok, e.refs("failed/"), e.refs("ready/"), out)
	}
	if got := strings.Join(e.mainSubjects(), " | "); got != "feat(client): a | fix(client): b" {
		t.Fatalf("main holds %q\n%s", got, out)
	}
	for _, ref := range []string{"main~1", "main"} {
		var num string
		for _, n := range e.decisionsOn(ref) {
			if strings.HasSuffix(n, "-x.md") {
				num = strings.TrimSuffix(n, "-x.md")
			}
			if strings.Contains(n, "D-new") {
				t.Errorf("%s still holds a pending decision %s", ref, n)
			}
		}
		if num == "" {
			t.Fatalf("%s lacks the numbered decision: %v", ref, e.decisionsOn(ref))
		}
		if notes := e.upGit("show", ref+":notes.md"); strings.Contains(notes, "D-new") || !strings.Contains(notes, "cites "+num+" here") {
			t.Errorf("%s: the citation reads %s:\n%s", ref, num, notes)
		}
	}
	// The second commit edited the decision file the first added: the edit is in the numbered file.
	for _, n := range e.decisionsOn("main") {
		if strings.HasSuffix(n, "-x.md") && !strings.Contains(e.upGit("show", "main:"+dec+n), "More.") {
			t.Errorf("the edit of b must be in %s", n)
		}
	}
}

// A citation of a pending decision a later change of the same chain adds lands, numbered; the
// same citation across two units (the decision's change is in a later unit) is rejected with the
// sentence of an unknown placeholder, and the later unit lands on its own.
func TestLanderChecksAForwardCitationPerUnit(t *testing.T) {
	dec := "docs/" + "decisions/"
	t.Run("inside a chain", func(t *testing.T) {
		e := newLandEnv(t)
		e.queue("a", "fix(client): a\n", "2026-01-01T10:00:00Z", map[string]string{"notes.md": "cites D-new-y\n"})
		e.queueAfter("b", "a", "fix(client): b\n", "2026-01-01T10:01:00Z", map[string]string{dec + "D-new-y.md": "---\ntopic: x\n---\n\n# D-new-y\n"})
		out, ok := e.land()
		if !ok || e.refs("failed/") != "" {
			t.Fatalf("the chain lands (ok=%v, failed=%q)\n%s", ok, e.refs("failed/"), out)
		}
		notes := e.upGit("show", "main:notes.md")
		if strings.Contains(notes, "D-new") || !strings.HasPrefix(notes, "cites D") {
			t.Errorf("the forward citation reads the number:\n%s", notes)
		}
	})
	t.Run("across two units", func(t *testing.T) {
		e := newLandEnv(t)
		e.queue("a", "fix(client): a\n", "2026-01-01T10:00:00Z", map[string]string{"notes.md": "cites D-new-y\n"})
		e.queue("b", "fix(client): b\n", "2026-01-01T10:01:00Z", map[string]string{dec + "D-new-y.md": "---\ntopic: x\n---\n\n# D-new-y\n"})
		out, ok := e.land()
		if !ok {
			t.Fatalf("land failed\n%s", out)
		}
		if e.refs("failed/") != "a" {
			t.Fatalf("a is rejected: failed=%q\n%s", e.refs("failed/"), out)
		}
		if r := e.upGit("log", "-1", "--format=%s", "failed/a"); !strings.Contains(r, "neither on main nor earlier in the train") {
			t.Errorf("the reason says so, got %q", r)
		}
		if got := strings.Join(e.mainSubjects(), " | "); got != "fix(client): b" {
			t.Errorf("b lands alone, main holds %q", got)
		}
	})
}

// A bisection that lands a prefix keeps the numbers the whole train gave it: the lander numbered
// once, before the gate, and the prefix is the first commits of that train.
func TestLanderABisectedPrefixKeepsItsNumbers(t *testing.T) {
	e := newLandEnv(t)
	dec := "docs/" + "decisions/"
	e.queue("a", "fix(kb): a\n", "2026-01-01T10:00:00Z", map[string]string{dec + "D-new-a.md": "---\ntopic: x\n---\n\n# D-new-a\n", "a.txt": "see D-new-a\n"})
	e.queue("b", "fix(kb): b\n", "2026-01-01T11:00:00Z", map[string]string{"BAD": "x\n"})
	e.queue("c", "fix(kb): c\n", "2026-01-01T12:00:00Z", map[string]string{dec + "D-new-c.md": "---\ntopic: x\n---\n\n# D-new-c\n"})
	out, ok := e.land()
	if !ok || e.refs("failed/") != "b" {
		t.Fatalf("b is rejected, a and c land (ok=%v failed=%q)\n%s", ok, e.refs("failed/"), out)
	}
	var na, nc string
	for _, n := range e.decisionsOn("main") {
		switch {
		case strings.HasSuffix(n, "-a.md"):
			na = n
		case strings.HasSuffix(n, "-c.md"):
			nc = n
		}
	}
	numOf := func(n, slug string) int {
		v, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(n, "D"), "-"+slug+".md"))
		if err != nil {
			return -1
		}
		return v
	}
	if numOf(na, "a") < 0 || numOf(nc, "c") <= numOf(na, "a") {
		t.Fatalf("a and c are numbered, a first: %q %q\n%s", na, nc, out)
	}
	if got := e.upGit("show", "main~1:a.txt"); got != "see "+strings.TrimSuffix(na, "-a.md") {
		t.Errorf("the citation of a reads its number, got %q", got)
	}
}

// make ready warns when the change cites a pending decision that nothing queued before it adds,
// and stays quiet for one the change adds itself.
func TestReadyWarnsOfACitationNothingBeforeItAdds(t *testing.T) {
	e := newLandEnv(t)
	dec := "docs/" + "decisions/"
	ready := func(slug, msg string, files map[string]string) string {
		e.git("checkout", "-q", "-B", "feat/"+slug, "origin/main")
		for p, b := range files {
			e.write(filepath.Join(e.work, p), b)
		}
		e.git("add", ".")
		e.git("commit", "-q", "-m", "wip")
		m := filepath.Join(e.tmp, "msg")
		e.write(m, msg)
		cmd := exec.Command("bash", filepath.Join(e.work, "scripts", "ready.sh"))
		cmd.Dir = e.work
		cmd.Env = append(append([]string{}, e.env...), "MSG="+m, "READY_NO_LANDER=1", "REVIEW=full, 1 rounds")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("ready %s: %v\n%s", slug, err, out)
		}
		return string(out)
	}
	if out := ready("lone", "feat(kb): lone\n", map[string]string{"n.txt": "cites D-new-nobody\n", "t_test.go": "// D-new-fixture\n"}); !strings.Contains(out, "ready: warning: it cites D-new-nobody, which nothing queued before it adds") || strings.Contains(out, "D-new-fixture") {
		t.Errorf("a warning for the citation, none for a test file:\n%s", out)
	}
	if out := ready("self", "feat(kb): self\n", map[string]string{"n2.txt": "cites D-new-mine\n", dec + "D-new-mine.md": "---\ntopic: x\n---\n\n# D-new-mine\n"}); strings.Contains(out, "warning") {
		t.Errorf("no warning for a decision the change adds:\n%s", out)
	}
}

// countingBuild is a LAND_BUNDLE_BUILD script that counts its runs in <dir>/count and builds the
// bundle from web/src/a.ts, as bundleBuildScript does.
func countingBuild(t *testing.T, e *landEnv) (script, count string) {
	t.Helper()
	dir := t.TempDir()
	script, count = filepath.Join(dir, "build"), filepath.Join(dir, "count")
	e.write(script, "#!/bin/sh\necho run >> '"+count+"'\nmkdir -p internal/webui/dist && cp web/src/a.ts internal/webui/dist/provenance.json\n")
	return script, count
}

// A change picked through remap_pick (an edit of a pending decision main holds numbered) that
// cites that decision under web/ gets its bundle built again: remap_pick has already rewritten
// the citation, so the numbering of the train changes nothing, and the bundle is stale all the
// same (D2128).
func TestLanderRebuildsTheBundleOfARemappedChangeCitingUnderWeb(t *testing.T) {
	e := newLandEnv(t)
	dec := "docs/" + "decisions/"
	body := "---\ntopic: x\n---\n\n# D-new-x — X\n"
	e.queue("a", "feat(client): a\n", "2026-01-01T10:00:00Z", map[string]string{dec + "D-new-x.md": body, "internal/webui/dist/provenance.json": "stale\n"})
	e.git("fetch", "-q", "origin")
	e.git("checkout", "-q", "-B", "work-b", "origin/ready/a")
	e.write(filepath.Join(e.work, dec, "D-new-x.md"), body+"Two.\n")
	e.write(filepath.Join(e.work, "web/src/a.ts"), "// D-new-x\n")
	e.git("add", ".")
	e.git("commit", "-q", "-m", "feat(atlas): b\n", "-m", "Review: full, 1 rounds")
	e.git("push", "-q", "--force", "origin", "HEAD:refs/heads/ready/b")
	e.git("checkout", "-q", "main")
	e.git("fetch", "-q", "origin")
	e.git("update-ref", "refs/lander-hold", "origin/ready/b")
	e.upGit("update-ref", "-d", "refs/heads/ready/b")
	if out, ok := e.land(); !ok {
		t.Fatalf("land a failed\n%s", out)
	}
	e.git("push", "-q", "--force", "origin", "refs/lander-hold:refs/heads/ready/b")
	build, count := countingBuild(t, e)
	out, ok := e.land("LAND_BUNDLE_BUILD=" + build)
	if !ok || e.refs("failed/") != "" {
		t.Fatalf("b must land, failed=%q\n%s", e.refs("failed/"), out)
	}
	src := e.upGit("show", "main:web/src/a.ts")
	if strings.Contains(src, "D-new-") || e.upGit("show", "main:internal/webui/dist/provenance.json") != src {
		t.Errorf("the bundle must be built from the numbered source %q", src)
	}
	if n, _ := os.ReadFile(count); strings.Count(string(n), "run") != 1 {
		t.Errorf("one build, got %q", n)
	}
}

// The bundle is built again for a commit whose own diff cites a pending decision under web/, not
// for every later commit of the train: a train of three where only the first touches web/ builds
// once, and the others carry the previous commit's bundle.
func TestLanderBuildsTheBundleOnlyForTheCommitThatCitesUnderWeb(t *testing.T) {
	e := newLandEnv(t)
	dec := "docs/" + "decisions/"
	e.queue("a", "feat(atlas): a\n", "2026-01-01T10:00:00Z", map[string]string{
		dec + "D-new-foo.md": "---\ntopic: x\n---\n\n# D-new-foo\n", "web/src/a.ts": "// D-new-foo\n", "internal/webui/dist/provenance.json": "stale\n"})
	e.queue("b", "fix(kb): b\n", "2026-01-01T11:00:00Z", map[string]string{"b.txt": "b\n"})
	e.queue("c", "fix(kb): c\n", "2026-01-01T12:00:00Z", map[string]string{"c.txt": "c\n"})
	build, count := countingBuild(t, e)
	out, ok := e.land("LAND_BUNDLE_BUILD=" + build)
	if !ok || e.refs("failed/") != "" || len(e.mainSubjects()) != 3 {
		t.Fatalf("the three land (ok=%v, failed=%q)\n%s", ok, e.refs("failed/"), out)
	}
	if n, _ := os.ReadFile(count); strings.Count(string(n), "run") != 1 {
		t.Errorf("one build for the three commits, got %q\n%s", n, out)
	}
	src := e.upGit("show", "main:web/src/a.ts")
	for _, ref := range []string{"main~2", "main~1", "main"} {
		if got := e.upGit("show", ref+":internal/webui/dist/provenance.json"); got != src {
			t.Errorf("%s carries the rebuilt bundle, got %q want %q", ref, got, src)
		}
	}
}

// remap_pick applies the edit of a pending decision to the numbered file when main changed that
// file elsewhere meanwhile: hunks that do not overlap apply with their offset.
func TestLanderRemapsAnEditWhenMainChangedTheNumberedFileElsewhere(t *testing.T) {
	e := newLandEnv(t)
	dec := "docs/" + "decisions/"
	lines := func(first, last string) string {
		return "---\ntopic: x\n---\n\n# D-new-x — X\n\n" + first + "\n" + strings.Repeat("filler line\n", 12) + last + "\n"
	}
	e.queue("a", "feat(client): a\n", "2026-01-01T10:00:00Z", map[string]string{dec + "D-new-x.md": lines("first", "last")})
	e.git("fetch", "-q", "origin")
	e.git("checkout", "-q", "-B", "work-b", "origin/ready/a")
	e.write(filepath.Join(e.work, dec, "D-new-x.md"), lines("first edited", "last"))
	e.git("add", ".")
	e.git("commit", "-q", "-m", "fix(client): b\n", "-m", "Review: full, 1 rounds")
	e.git("push", "-q", "--force", "origin", "HEAD:refs/heads/ready/b")
	e.git("checkout", "-q", "main")
	e.git("fetch", "-q", "origin")
	e.git("update-ref", "refs/lander-hold", "origin/ready/b")
	e.upGit("update-ref", "-d", "refs/heads/ready/b")
	if out, ok := e.land(); !ok {
		t.Fatalf("land a failed\n%s", out)
	}
	// Main changes the numbered file's last line, as another change would.
	e.git("fetch", "-q", "origin")
	e.git("checkout", "-q", "-B", "elsewhere", "origin/main")
	var num string
	for _, n := range e.decisionsOn("main") {
		if strings.HasSuffix(n, "-x.md") {
			num = n
		}
	}
	got, err := os.ReadFile(filepath.Join(e.work, dec, num))
	if err != nil {
		t.Fatal(err)
	}
	e.write(filepath.Join(e.work, dec, num), strings.Replace(string(got), "last\n", "last on main\n", 1))
	e.git("add", ".")
	e.git("commit", "-q", "-m", "docs: edit elsewhere")
	e.git("push", "-q", "origin", "elsewhere:main")
	e.git("checkout", "-q", "main")
	e.git("push", "-q", "--force", "origin", "refs/lander-hold:refs/heads/ready/b")
	out, ok := e.land()
	if !ok || e.refs("failed/") != "" {
		t.Fatalf("b must land, failed=%q\n%s", e.refs("failed/"), out)
	}
	if final := e.upGit("show", "main:"+dec+num); !strings.Contains(final, "first edited") || !strings.Contains(final, "last on main") {
		t.Errorf("both edits are in the numbered file:\n%s", final)
	}
}

// nowDate is a committer date for a change queued just now.
func nowDate() string { return time.Now().UTC().Format("2006-01-02T15:04:05Z") }

// AC-P3-12: a change queued alone is composed once the collection window has run, no later than
// LAND_WINDOW after it was queued, and the lander says it collects. AC-P3-13: a second change
// queued inside the window lands in the same train.
func TestLanderWaitsForCompanyBeforeComposing(t *testing.T) {
	e := newLandEnv(t)
	e.queue("first", "fix(kb): first\n", nowDate(), map[string]string{"f.txt": "f\n"})
	e.queue("second", "fix(kb): second\n", nowDate(), map[string]string{"s.txt": "s\n"})
	second := e.upGit("rev-parse", "ready/second")
	e.upGit("update-ref", "-d", "refs/heads/ready/second")
	// A wide window that closes on the second green change, not on the clock: a loaded gate
	// cannot close it before the second change is queued, and the test waits no longer.
	out, ok := e.land("LAND_WINDOW=600", "LAND_WINDOW_MIN=2", "LAND_POLL=1", "FAKE_SECOND_REF="+second)
	if !ok {
		t.Fatalf("land failed\n%s", out)
	}
	if !strings.Contains(out, "collecting: 1 queued, 1 green: composing at ") || strings.Count(out, "collecting:") != 1 {
		t.Errorf("one collecting line is logged:\n%s", out)
	}
	if got := strings.Join(e.mainSubjects(), " | "); !strings.Contains(got, "fix(kb): first") || !strings.Contains(got, "fix(kb): second") || strings.Contains(e.refs("ready/"), "first") {
		t.Errorf("both changes land: %q\n%s", got, out)
	}
	if n := strings.Count(e.log(), "pr create"); n != 1 {
		t.Errorf("one train, got %d\n%s", n, out)
	}
}

// A change queued before the window, one that asks for no wait (Lander-Now) and a queue holding
// LAND_WINDOW_MIN green changes compose at once.
func TestLanderComposesAtOnceWhenTheWindowIsClosed(t *testing.T) {
	for _, c := range []struct {
		name, msg, date string
		extra           []string
	}{
		{"queued before the window", "fix(kb): x\n", "2026-01-01T10:00:00Z", nil},
		{"Lander-Now", "fix(kb): x\n\nLander-Now: yes\n", "", nil},
		{"enough green changes", "fix(kb): x\n", "", []string{"LAND_WINDOW_MIN=1"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newLandEnv(t)
			d := c.date
			if d == "" {
				d = nowDate()
			}
			e.queue("x", c.msg, d, map[string]string{"x.txt": "x\n"})
			out, ok := e.land(append([]string{"LAND_WINDOW=600", "LAND_POLL=1"}, c.extra...)...)
			if !ok || strings.Contains(out, "collecting:") || !strings.Contains(strings.Join(e.mainSubjects(), "|"), "fix(kb): x") {
				t.Errorf("composes at once (ok=%v):\n%s", ok, out)
			}
		})
	}
}

// AC-P3-14: --status shows each unit's state, with the failed jobs of a red pre-check, a chain's
// members under its first, a unit waiting on a rejected change, and the window's closing time.
func TestLanderStatusShowsThePrecheckVerdicts(t *testing.T) {
	e := newLandEnv(t)
	e.queue("green", "fix(kb): green\n", "2026-01-01T10:00:00Z", map[string]string{"g.txt": "g\n"})
	e.queue("b", "fix(kb): b\n", "2026-01-01T10:01:00Z", map[string]string{"qb.txt": "b\n"})
	e.queueAfter("c", "b", "fix(kb): c\n", "2026-01-01T10:02:00Z", map[string]string{"qc.txt": "c\n"})
	e.queue("x", "fix(kb): x\n", "2026-01-01T10:03:00Z", map[string]string{"x.txt": "x\n"})
	e.queueAfter("y", "x", "fix(kb): y\n", "2026-01-01T10:04:00Z", map[string]string{"qy.txt": "y\n"})
	x := e.upGit("rev-parse", "ready/x")
	rej := e.upGit("commit-tree", x+"^{tree}", "-p", x, "-m", "fix(kb): rejected: a conflict")
	e.upGit("update-ref", "refs/heads/failed/x", rej)
	e.upGit("update-ref", "-d", "refs/heads/ready/x")
	out, rc := e.landArgs([]string{"--status"}, "FAKE_PRECHECK_RED="+e.upGit("rev-parse", "ready/c"),
		"FAKE_GH_JOBS="+hostedRun("completed", "completed success", map[string]string{"test-windows": "completed failure", "web": "completed failure"}))
	if rc != 0 {
		t.Fatalf("--status exits %d\n%s", rc, out)
	}
	for _, want := range []string{
		"  green: fix(kb): green [pre-check: ok]\n",
		"  b: fix(kb): b [pre-check: red (failed jobs: test-windows, web)]\n    c: fix(kb): c (after b)\n",
		"  y: fix(kb): y (after x) [waits on rejected x]\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("--status lacks %q:\n%s", want, out)
		}
	}
	// A gh that fails everywhere: the status still prints, with unknown verdicts.
	e.write(filepath.Join(e.bin, "gh"), "#!/bin/sh\nexit 1\n")
	if out, rc := e.landArgs([]string{"--status"}); rc != 0 || !strings.Contains(out, "[pre-check: unknown]") {
		t.Errorf("--status never fails (rc=%d):\n%s", rc, out)
	}
	// A queued change inside the window: the time it closes.
	e = newLandEnv(t)
	e.queue("fresh", "fix(kb): fresh\n", nowDate(), map[string]string{"fr.txt": "f\n"})
	if out, _ := e.landArgs([]string{"--status"}, "LAND_WINDOW=600"); !strings.Contains(out, "collecting until ") {
		t.Errorf("the window's closing time is shown:\n%s", out)
	}
}

// The lander names the failed jobs of a red pre-check when it gates that unit alone.
func TestLanderLogsTheFailedJobsOfARedPrecheck(t *testing.T) {
	e := newLandEnv(t)
	e.queue("r", "fix(kb): r\n", "2026-01-01T10:00:00Z", map[string]string{"r.txt": "r\n"})
	out, _ := e.land("FAKE_PRECHECK_RED="+e.upGit("rev-parse", "ready/r"),
		"FAKE_GH_JOBS="+hostedRun("completed", "completed success", map[string]string{"test-windows": "completed failure"}))
	if !strings.Contains(out, "its hosted pre-check is red (failed jobs: test-windows): gated alone") {
		t.Errorf("the log names the failed jobs:\n%s", out)
	}
}

// requeue runs scripts/requeue.sh in the work clone.
func (e *landEnv) requeue(slug string, extra ...string) (string, bool) {
	e.t.Helper()
	cmd := exec.Command("bash", filepath.Join(e.work, "scripts", "requeue.sh"))
	cmd.Dir = e.work
	cmd.Env = append(append([]string{}, e.env...), append([]string{"SLUG=" + slug, "READY_NO_LANDER=1", "REVIEW=full, 1 rounds"}, extra...)...)
	out, err := cmd.CombinedOutput()
	return string(out), err == nil
}

// queueChild queues ready/<slug> as a child of ready/<after>, with Lander-After.
func (e *landEnv) queueChild(slug, after, date string, files map[string]string) {
	e.t.Helper()
	e.git("fetch", "-q", "origin")
	e.git("checkout", "-q", "-B", "work-"+slug, "origin/ready/"+after)
	for p, body := range files {
		e.write(filepath.Join(e.work, p), body)
	}
	e.git("add", ".")
	cmd := exec.Command("git", "commit", "-q", "-F", "-")
	cmd.Dir, cmd.Env = e.work, append(append([]string{}, e.env...), "GIT_COMMITTER_DATE="+date, "GIT_AUTHOR_DATE="+date)
	cmd.Stdin = strings.NewReader(e.withReview("fix(kb): " + slug + "\n\nLander-After: " + after + "\n"))
	if out, err := cmd.CombinedOutput(); err != nil {
		e.t.Fatalf("commit: %v\n%s", err, out)
	}
	e.git("push", "-q", "--force", "origin", "HEAD:refs/heads/ready/"+slug)
	e.git("checkout", "-q", "main")
}

func (e *landEnv) moveMain(file string) {
	e.t.Helper()
	e.git("fetch", "-q", "origin")
	e.git("checkout", "-q", "-B", "mv", "origin/main")
	e.write(filepath.Join(e.work, file), "moved\n")
	e.git("add", ".")
	e.git("commit", "-q", "-m", "chore: moved")
	e.git("push", "-q", "origin", "HEAD:refs/heads/main")
	e.git("checkout", "-q", "main")
}

// AC-P3-16: after main moved, requeue rebuilds a queued change on it, with its message, its dates
// and its place in the queue; a chain of three keeps its parents.
func TestRequeueRebuildsAChainOnAMovedMainKeepingDatesAndMessage(t *testing.T) {
	e := newLandEnv(t)
	e.queue("a", "fix(kb): a\n\nCloses #1\n", "2026-01-01T10:00:00Z", map[string]string{"qa.txt": "a\n"})
	e.queueChild("b", "a", "2026-01-01T11:00:00Z", map[string]string{"b.txt": "b\n"})
	e.queueChild("c", "b", "2026-01-01T12:00:00Z", map[string]string{"c.txt": "c\n"})
	e.queue("other", "fix(kb): other\n", "2026-01-01T13:00:00Z", map[string]string{"o.txt": "o\n"})
	oldA, oldC, oldO := e.upGit("rev-parse", "ready/a"), e.upGit("rev-parse", "ready/c"), e.upGit("rev-parse", "ready/other")
	msgA := e.upGit("log", "-1", "--format=%B", "ready/a")
	e.moveMain("m.txt")
	out, ok := e.requeue("a")
	if !ok {
		t.Fatalf("requeue failed\n%s", out)
	}
	mainSha := e.upGit("rev-parse", "main")
	if p := e.upGit("rev-parse", "ready/a^"); p != mainSha {
		t.Errorf("a is rebuilt on the moved main: parent %s, want %s", p, mainSha)
	}
	if e.upGit("rev-parse", "ready/b^") != e.upGit("rev-parse", "ready/a") || e.upGit("rev-parse", "ready/c^") != e.upGit("rev-parse", "ready/b") {
		t.Errorf("the chain keeps its parents")
	}
	if e.upGit("rev-parse", "ready/c") == oldC || e.upGit("rev-parse", "ready/a") == oldA {
		t.Errorf("every member of the chain is a new commit")
	}
	if e.upGit("rev-parse", "ready/other") != oldO {
		t.Errorf("an unrelated change is left alone")
	}
	if got := e.upGit("log", "-1", "--format=%B", "ready/a"); got != msgA {
		t.Errorf("the message is kept verbatim:\n%q\n%q", got, msgA)
	}
	// Epoch seconds: git spells a UTC date +00:00 or Z by version (%cI), the instant is the same.
	if got, want := e.upGit("log", "-1", "--format=%ct|%at", "ready/b"), "1767265200|1767265200"; got != want {
		t.Errorf("author and committer dates are kept: %s, want %s (2026-01-01T11:00:00Z)", got, want)
	}
	if !strings.Contains(out, "ready/a "+oldA[:12]+" -> "+e.upGit("rev-parse", "ready/a")[:12]) {
		t.Errorf("one line per ref with the old and the new sha:\n%s", out)
	}
	if got := e.upGit("for-each-ref", "--sort=committerdate", "--format=%(refname:lstrip=3)", "refs/heads/ready/"); got != "a\nb\nc\nother" {
		t.Errorf("same place in the queue: %q", got)
	}
}

// FIRST=1 dates the chain before the oldest queue entry, in chain order, and sets Lander-Now on the first.
func TestRequeueFirstOrdersTheChainFirstAndSetsLanderNow(t *testing.T) {
	e := newLandEnv(t)
	e.queue("old", "fix(kb): old\n", "2026-01-01T09:00:00Z", map[string]string{"o.txt": "o\n"})
	e.queue("a", "fix(kb): a\n", "2026-01-01T10:00:00Z", map[string]string{"qa.txt": "a\n"})
	e.queueChild("b", "a", "2026-01-01T11:00:00Z", map[string]string{"b.txt": "b\n"})
	if out, ok := e.requeue("a", "FIRST=1"); !ok {
		t.Fatalf("requeue failed\n%s", out)
	}
	if got := e.upGit("for-each-ref", "--sort=committerdate", "--format=%(refname:lstrip=3)", "refs/heads/ready/"); got != "a\nb\nold" {
		t.Errorf("the chain is first, in order: %q", got)
	}
	if m := e.upGit("log", "-1", "--format=%B", "ready/a"); !strings.Contains(m, "Lander-Now: yes") {
		t.Errorf("the first member carries Lander-Now:\n%s", m)
	}
	if m := e.upGit("log", "-1", "--format=%B", "ready/b"); strings.Contains(m, "Lander-Now") {
		t.Errorf("only the first does:\n%s", m)
	}
}

// From failed/ the rejection ref is gone, and the commit queued as it was is rebuilt.
func TestRequeueFromARejectedChangeDeletesItsRejectionRef(t *testing.T) {
	e := newLandEnv(t)
	e.queue("x", "fix(kb): x\n", "2026-01-01T10:00:00Z", map[string]string{"x.txt": "x\n"})
	x := e.upGit("rev-parse", "ready/x")
	rej := e.upGit("commit-tree", x+"^{tree}", "-p", x, "-m", "lander: rejected")
	e.upGit("update-ref", "refs/heads/failed/x", rej)
	e.upGit("update-ref", "-d", "refs/heads/ready/x")
	e.moveMain("m.txt")
	if out, ok := e.requeue("x"); !ok {
		t.Fatalf("requeue failed\n%s", out)
	}
	if e.refs("failed/") != "" || e.refs("ready/") != "x" {
		t.Errorf("failed=%q ready=%q", e.refs("failed/"), e.refs("ready/"))
	}
}

// AC-P3-17: an unknown slug, a conflict and an empty change leave the queue byte-identical.
func TestRequeueThatFailsLeavesTheQueueByteIdentical(t *testing.T) {
	e := newLandEnv(t)
	e.queue("a", "fix(kb): a\n", "2026-01-01T10:00:00Z", map[string]string{"qa.txt": "a\n"})
	e.queueChild("b", "a", "2026-01-01T11:00:00Z", map[string]string{"b.txt": "b\n"})
	snap := func() string { return e.upGit("for-each-ref", "--format=%(refname) %(objectname)", "refs/heads/") }
	before := snap()
	if out, ok := e.requeue("nope"); ok || !strings.Contains(out, "queued: a b") {
		t.Errorf("an unknown slug lists the queue (ok=%v):\n%s", ok, out)
	}
	// main now holds a conflicting qa.txt: the first member does not apply.
	e.git("fetch", "-q", "origin")
	e.git("checkout", "-q", "-B", "mv", "origin/main")
	e.write(filepath.Join(e.work, "qa.txt"), "conflict\n")
	e.git("add", ".")
	e.git("commit", "-q", "-m", "chore: qa.txt")
	e.git("push", "-q", "origin", "HEAD:refs/heads/main")
	e.git("checkout", "-q", "main")
	before = snap()
	out, ok := e.requeue("a")
	if ok || !strings.Contains(out, "a does not apply") || !strings.Contains(out, "qa.txt") || snap() != before {
		t.Errorf("a conflict names the slug and the files and changes no ref (ok=%v):\n%s", ok, out)
	}
	// A change main already holds becomes empty.
	e.queue("same", "fix(kb): same\n", "2026-01-01T12:00:00Z", map[string]string{"s.txt": "s\n"})
	e.moveMain("s.txt")
	e.git("checkout", "-q", "-B", "mv2", "origin/main")
	e.write(filepath.Join(e.work, "s.txt"), "s\n")
	e.git("add", ".")
	e.git("commit", "-q", "-m", "chore: s")
	e.git("push", "-q", "origin", "HEAD:refs/heads/main")
	e.git("checkout", "-q", "main")
	before = snap()
	if out, ok := e.requeue("same"); ok || !strings.Contains(out, "already on main") || snap() != before {
		t.Errorf("an empty change is already on main (ok=%v):\n%s", ok, out)
	}
}

// Only a maintainer requeues.
func TestRequeueRefusesAContributor(t *testing.T) {
	e := newLandEnv(t)
	e.queue("a", "fix(kb): a\n", "2026-01-01T10:00:00Z", map[string]string{"qa.txt": "a\n"})
	before := e.upGit("rev-parse", "ready/a")
	if out, ok := e.requeue("a", "FAKE_PERM=READ"); ok || !strings.Contains(out, "only a maintainer") || e.upGit("rev-parse", "ready/a") != before {
		t.Errorf("a contributor does not requeue (ok=%v):\n%s", ok, out)
	}
}

// The leases of the push are the shas read when the queue was read: a ref that moves between
// that read and the push (here, a pre-push hook of the clone moves it) fails the lease, nothing is
// pushed, and the message says the queue changed. The same for the rejection ref of a rejected change.
func TestRequeueFailsWhenTheQueueMovesBeforeThePush(t *testing.T) {
	e := newLandEnv(t)
	e.queue("a", "fix(kb): a\n", "2026-01-01T10:00:00Z", map[string]string{"a2.txt": "a\n"})
	e.queue("x", "fix(kb): x\n", "2026-01-01T11:00:00Z", map[string]string{"x.txt": "x\n"})
	x := e.upGit("rev-parse", "ready/x")
	e.upGit("update-ref", "refs/heads/failed/x", e.upGit("commit-tree", x+"^{tree}", "-p", x, "-m", "lander: rejected"))
	e.upGit("update-ref", "-d", "refs/heads/ready/x")
	e.moveMain("m.txt")
	snap := func() string { return e.upGit("for-each-ref", "--format=%(refname) %(objectname)", "refs/heads/") }
	hook := filepath.Join(e.work, ".git", "hooks", "pre-push")
	for _, c := range []struct{ slug, ref string }{{"a", "refs/heads/ready/a"}, {"x", "refs/heads/failed/x"}} {
		// The hook queues a newer version of the ref the lease guards, as another maintainer's push would.
		e.write(hook, "#!/bin/sh\ngit --git-dir='"+e.up+"' update-ref "+c.ref+" \"$(git --git-dir='"+e.up+"' rev-parse main)\"\n")
		out, ok := e.requeue(c.slug)
		if ok || !strings.Contains(out, "the queue changed") {
			t.Errorf("%s: a moved ref fails the lease and says so (ok=%v):\n%s", c.slug, ok, out)
		}
		if got := e.upGit("rev-parse", c.ref); got != e.upGit("rev-parse", "main") {
			t.Errorf("%s: nothing of the requeue was pushed, %s holds %s", c.slug, c.ref, got)
		}
		if c.slug == "x" && strings.Contains(snap(), "refs/heads/ready/x ") {
			t.Errorf("the atomic push wrote ready/x although the lease on failed/x failed:\n%s", snap())
		}
		if err := os.Remove(hook); err != nil {
			t.Fatal(err)
		}
	}
}

// The lander matches the signoff's line for a run whose hosted runners were never acquired
// (#2313) with its anchored infra_line, so a gate that fails like that names the failure when
// it stops; a line that merely contains it does not match.
func TestLanderInfraLineMatchesTheHostedRunnersLine(t *testing.T) {
	src, err := os.ReadFile(filepath.Join(repoRoot(t), "scripts", "land.sh"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^infra_line='(.*)'$`).FindSubmatch(src)
	if m == nil {
		t.Fatal("scripts/land.sh has no infra_line")
	}
	re := regexp.MustCompile(string(m[1]))
	for line, want := range map[string]bool{
		"signoff: the hosted runners failed (exit 0) before the gate finished: nothing pushed, nothing posted":      true,
		"signoff: the build host failed (exit 255) before the gate finished: nothing pushed, nothing posted":        true,
		"x signoff: the hosted runners failed (exit 0) before the gate finished: nothing pushed, nothing posted":    false,
		"signoff: the hosted runners failed (exit 0) before the gate finished: nothing pushed, nothing posted tail": false,
	} {
		if re.MatchString(line) != want {
			t.Errorf("infra_line on %q = %v, want %v", line, !want, want)
		}
	}
}
