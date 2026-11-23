package landertest

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// A red `main` says what the train gate did not run (docs/testing.md §Go tests): main-status
// appends the output of scripts/main-red.sh to the `ci-red` issue, which reads the `Gate:` line
// of each train PR of the range (scripts/land.sh gate_line) and names, for each red leg, the
// trains whose gate did not run it whole. It fails open: what it cannot read never stops the issue.

// mrPR is a pull request the fake gh knows: its head branch and repository, and its body.
type mrPR struct {
	ref, repo, body string
	open            bool
}

// mrRange is what the fake gh answers: the commits of the range, oldest first, each with the
// number of its pull request (0: none), and the pull requests.
type mrRange struct {
	commits []string
	prOf    map[string]int
	prs     map[int]mrPR
}

const mainRedGH = `#!/bin/bash
echo "$*" >> "$FAKE_DIR/gh.log"
jq_expr=; prev=
for a in "$@"; do [ "$prev" = --jq ] && jq_expr=$a; prev=$a; done
out() { if [ -n "$jq_expr" ]; then jq -r "$jq_expr"; else cat; fi; }
case "$1 $2" in
  "api repos/up/proj/compare/"*) step=compare f=compare.json ;;
  "api repos/up/proj/commits/"*/pulls) c=${2#repos/up/proj/commits/}; step=pulls f=pulls-${c%/pulls}.json ;;
  "api repos/up/proj/pulls/"*) step=body f=pull-${2##*/}.json ;;
  *) echo "unexpected gh $*" >&2; exit 1 ;;
esac
[ "${FAKE_FAIL:-}" != "$step" ] || { echo "HTTP 502" >&2; exit 1; }
out < "$FAKE_DIR/$f"
`

// mrResult is one run of the script: its stdout, its exit code, the fake gh's log and the
// directory it ran in.
type mrResult struct {
	out, log, dir string
	rc            int
}

// mainRed runs scripts/main-red.sh <up/proj> <sha> <green> over r with NEEDS_JSON=needs, in a
// temporary directory.
func mainRed(t *testing.T, needs string, r mrRange, sha, green string, env ...string) mrResult {
	t.Helper()
	bash := bashOrSkip(t)
	dir := t.TempDir()
	write := func(name string, v any) {
		t.Helper()
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var commits []map[string]string
	for _, c := range r.commits {
		commits = append(commits, map[string]string{"sha": c})
		pulls := []map[string]any{}
		if n := r.prOf[c]; n != 0 {
			p := r.prs[n]
			repo := p.repo
			if repo == "" {
				repo = "up/proj"
			}
			var merged any = "2026-01-01T10:00:00Z"
			if p.open {
				merged = nil
			}
			pulls = append(pulls, map[string]any{"number": n, "merged_at": merged, "body": "not read from here",
				"head": map[string]any{"ref": p.ref, "repo": map[string]string{"full_name": repo}}})
		}
		write("pulls-"+c+".json", pulls)
	}
	write("compare.json", map[string]any{"commits": commits})
	for n, p := range r.prs {
		write(fmt.Sprintf("pull-%d.json", n), map[string]string{"body": p.body})
	}
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(mainRedGH), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bash, filepath.Join(repoRoot(t), "scripts", "main-red.sh"), "up/proj", sha, green)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), append([]string{"FAKE_DIR=" + dir, "NEEDS_JSON=" + needs,
		"PATH=" + dir + string(os.PathListSeparator) + os.Getenv("PATH")}, env...)...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	rc := 0
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		rc = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("main-red.sh: %v", err)
	}
	log, _ := os.ReadFile(filepath.Join(dir, "gh.log"))
	if rc != 0 {
		t.Logf("stderr:\n%s", stderr.String())
	}
	return mrResult{out: string(out), rc: rc, log: string(log), dir: dir}
}

// mrNeeds is a NEEDS_JSON with the given legs red and test green.
func mrNeeds(red ...string) string {
	m := map[string]map[string]string{"test": {"result": "success"}}
	for _, l := range red {
		m[l] = map[string]string{"result": "failure"}
	}
	b, _ := json.Marshal(m)
	return string(b)
}

func mrSha(c byte) string { return strings.Repeat(string(c), 40) }

// trainBodyWith is a train PR body as the lander writes it, its third line the Gate line.
func trainBodyWith(gate string) string {
	return "Size exception: a train of 1 change(s), each under the cap when it was queued (make ready).\n\n" + gate +
		"\n\nLanded by scripts/land.sh after one gate of the whole train (the `signoff` status of the head).\n"
}

func TestMainRedNamesTheLegsABuildHostGateDidNotRun(t *testing.T) {
	r := mrRange{
		commits: []string{mrSha('a'), mrSha('b'), mrSha('c')},
		prOf:    map[string]int{mrSha('a'): 11, mrSha('b'): 11, mrSha('c'): 12},
		prs: map[int]mrPR{
			// Edited on the web, the body has CRLF line ends.
			11: {ref: "train/1", body: strings.ReplaceAll(trainBodyWith("Gate: gate=host linux/amd64 windows=3 web=no 5m00s"), "\n", "\r\n")},
			12: {ref: "train/2", body: trainBodyWith("Gate: gate=hosted run=7 legs=8 9m59s")},
		},
	}
	res := mainRed(t, `{"test":{"result":"success"},"test-windows":{"result":"failure"},"test-macos":{"result":"failure"}}`,
		r, mrSha('c'), mrSha('9'))
	want := `Red legs: test-windows, test-macos
#12 train, Gate: gate=hosted run=7 legs=8 9m59s
#11 train, Gate: gate=host linux/amd64 windows=3 web=no 5m00s
The gate of #11 did not run test-windows: 3 package(s) only
The gate of #11 did not run test-macos: not a leg of the build-host gate
Found-by: main-ci
`
	if res.rc != 0 || res.out != want {
		t.Errorf("exit %d, got:\n%s\nwant:\n%s", res.rc, res.out, want)
	}
	if !strings.Contains(res.log, "api repos/up/proj/compare/"+mrSha('9')+"..."+mrSha('c')) {
		t.Errorf("the range is compare <green>...<sha>:\n%s", res.log)
	}
	if n := strings.Count(res.log, "repos/up/proj/pulls/11"); n != 1 {
		t.Errorf("a train of two commits is read once, read %d times:\n%s", n, res.log)
	}
}

// Each why of the build-host gate, and a hosted gate (also reached by the fallback) covering every leg.
func TestMainRedCoverageOfEachGate(t *testing.T) {
	green := "Every red leg ran green in the train gate: a flaky test or a condition only main has"
	for _, c := range []struct {
		name, gate string
		red        []string
		want       []string
	}{
		{"hosted", "gate=hosted run=7 legs=8 9m59s", []string{"test", "test-windows", "web"}, []string{green}},
		{"hosted fallback", "gate=hosted fallback=host-unavailable run=7 legs=8 9m59s", []string{"test-macos", "docker"}, []string{green}},
		{"host, test", "gate=host linux/amd64 windows=none web=yes 1m00s", []string{"test", "web"},
			[]string{"The gate of #7 did not run test: not its test-install step, the rest ran"}},
		{"host, no web", "gate=host linux/amd64 windows=none web=no 1m00s", []string{"web"},
			[]string{"The gate of #7 did not run web: no web-gate among its targets"}},
		{"host, no Windows leg", "gate=host linux/amd64 windows=none web=no 1m00s", []string{"test-windows"},
			[]string{"The gate of #7 did not run test-windows: no Windows leg"}},
		{"host, Windows unavailable", "gate=host linux/amd64 windows=unavailable web=no 1m00s flaky=2", []string{"test-windows"},
			[]string{"The gate of #7 did not run test-windows: the Windows machine was unavailable"}},
		{"host, the other legs", "gate=host darwin/arm64 windows=4 web=yes 1m00s", []string{"test-linux-nosystemd", "helm", "demo-edge"},
			[]string{"The gate of #7 did not run test-linux-nosystemd: not a leg of the build-host gate",
				"The gate of #7 did not run helm: not a leg of the build-host gate",
				"The gate of #7 did not run demo-edge: not a leg of the build-host gate"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			// No green run: the range is the one commit, no compare.
			r := mrRange{commits: []string{mrSha('a')}, prOf: map[string]int{mrSha('a'): 7},
				prs: map[int]mrPR{7: {ref: "train/7", body: trainBodyWith("Gate: " + c.gate)}}}
			res := mainRed(t, mrNeeds(c.red...), r, mrSha('a'), "")
			if res.rc != 0 || !strings.Contains(res.out, "\n#7 train, Gate: "+c.gate+"\n") {
				t.Fatalf("exit %d, the train's Gate line is not read:\n%s", res.rc, res.out)
			}
			for _, w := range c.want {
				if !strings.Contains(res.out, "\n"+w+"\n") {
					t.Errorf("want the line %q:\n%s", w, res.out)
				}
			}
			if strings.HasPrefix(c.gate, "gate=host ") && strings.Contains(res.out, "Every red leg") {
				t.Errorf("a leg the build host did not run whole is no green claim:\n%s", res.out)
			}
			if strings.Contains(res.log, "compare") {
				t.Errorf("no green run, no compare:\n%s", res.log)
			}
		})
	}
}

// A contributor's merge, a fork's PR named like a train, an unmerged one and a commit with no PR
// are not trains: no coverage line names them, and the range makes no green claim.
func TestMainRedMakesNoClaimOnWhatIsNotATrain(t *testing.T) {
	hosted := trainBodyWith("Gate: gate=hosted run=1 legs=8 1m00s")
	r := mrRange{
		commits: []string{mrSha('a'), mrSha('b'), mrSha('c'), mrSha('d'), mrSha('e')},
		prOf:    map[string]int{mrSha('a'): 31, mrSha('b'): 32, mrSha('c'): 33, mrSha('e'): 34},
		prs: map[int]mrPR{
			31: {ref: "feature/x", body: hosted},
			32: {ref: "train/x", repo: "someone/proj", body: hosted},
			33: {ref: "train/y", open: true, body: hosted},
			34: {ref: "train/z", body: trainBodyWith("Gate: gate=hosted run=7 legs=8 9m59s")},
		},
	}
	res := mainRed(t, mrNeeds("test-windows"), r, mrSha('e'), mrSha('9'))
	for _, w := range []string{"#31 not a train", "#32 not a train", "#33 not a train", "dddddddddddd no pull request",
		"#34 train, Gate: gate=hosted run=7 legs=8 9m59s"} {
		if !strings.Contains(res.out, "\n"+w+"\n") {
			t.Errorf("want the line %q:\n%s", w, res.out)
		}
	}
	if res.rc != 0 || strings.Contains(res.out, "The gate of") || strings.Contains(res.out, "Every red leg") {
		t.Errorf("exit %d: no coverage claim on a range with PRs that are not trains:\n%s", res.rc, res.out)
	}
	for _, n := range []string{"31", "32", "33"} {
		if strings.Contains(res.log, "repos/up/proj/pulls/"+n) {
			t.Errorf("the body of #%s, not a train, is read:\n%s", n, res.log)
		}
	}
}

// A train landed before the Gate line, a forged line and the lander's `Gate: unknown` cover nothing
// and say so; a line is printed only in the shape the lander writes.
func TestMainRedSaysWhenATrainsGateIsUnknown(t *testing.T) {
	r := mrRange{
		commits: []string{mrSha('a'), mrSha('b'), mrSha('c')},
		prOf:    map[string]int{mrSha('a'): 41, mrSha('b'): 42, mrSha('c'): 43},
		prs: map[int]mrPR{
			41: {ref: "train/1", body: "Landed by scripts/land.sh after one gate of the whole train.\n"},
			42: {ref: "train/2", body: trainBodyWith("Gate: gate=hosted @user [x](https://example.com)")},
			43: {ref: "train/3", body: trainBodyWith("Gate: unknown (the signoff status of 0123456789ab could not be read)")},
		},
	}
	res := mainRed(t, mrNeeds("test-windows", "test-macos"), r, mrSha('c'), mrSha('9'))
	for _, w := range []string{"#41 train, no readable Gate line", "#42 train, no readable Gate line",
		"#43 train, Gate: unknown (the signoff status of 0123456789ab could not be read)",
		"The gate of #41 is unknown: it may not have run test-macos, test-windows",
		"The gate of #42 is unknown: it may not have run test-macos, test-windows",
		"The gate of #43 is unknown: it may not have run test-macos, test-windows"} {
		if !strings.Contains(res.out, "\n"+w+"\n") {
			t.Errorf("want the line %q:\n%s", w, res.out)
		}
	}
	if res.rc != 0 || strings.Contains(res.out, "@") || strings.Contains(res.out, "](") || strings.Contains(res.out, "Every red leg") {
		t.Errorf("exit %d: a forged line reached the output, or an unknown gate made a claim:\n%s", res.rc, res.out)
	}
}

// Past 10 pull requests the rest of the range is not read, and the range makes no claim.
func TestMainRedReadsAtMostTenPullRequests(t *testing.T) {
	r := mrRange{prOf: map[string]int{}, prs: map[int]mrPR{}}
	for i := 0; i < 12; i++ {
		c := fmt.Sprintf("%040x", i+1)
		r.commits = append(r.commits, c)
		r.prOf[c] = 100 + i
		r.prs[100+i] = mrPR{ref: fmt.Sprintf("train/%d", i), body: trainBodyWith("Gate: gate=hosted run=7 legs=8 9m59s")}
	}
	res := mainRed(t, mrNeeds("test-windows"), r, r.commits[11], mrSha('9'))
	if n := strings.Count(res.out, " train, Gate: "); res.rc != 0 || n != 10 {
		t.Errorf("exit %d, %d trains read, want 10:\n%s", res.rc, n, res.out)
	}
	if !strings.Contains(res.out, "\n#111 train, ") || strings.Contains(res.out, "#100 ") || strings.Contains(res.out, "#101 ") {
		t.Errorf("the newest 10 are read:\n%s", res.out)
	}
	if !strings.Contains(res.out, "\nand 2 more commit(s) of the range, not read\n") || strings.Contains(res.out, "Every red leg") {
		t.Errorf("the rest is named, and no claim is made on it:\n%s", res.out)
	}
}

// The cap counts pull requests, not commits: the further commits of the 10th, a train of several,
// are not left over, and only the commits from an 11th pull request on are.
func TestMainRedCapsPullRequestsNotCommits(t *testing.T) {
	gate := trainBodyWith("Gate: gate=hosted run=7 legs=8 9m59s")
	build := func(beyond bool) mrRange {
		r := mrRange{prOf: map[string]int{}, prs: map[int]mrPR{}}
		add := func(n int, c string) {
			r.commits = append(r.commits, c)
			r.prOf[c] = n
			r.prs[n] = mrPR{ref: fmt.Sprintf("train/%d", n), body: gate}
		}
		if beyond {
			add(90, fmt.Sprintf("%040x", 0x90))
		}
		for k := 0; k < 3; k++ { // the 10th pull request, read last: a train of 3 commits
			add(100, fmt.Sprintf("%040x", 0x100+k))
		}
		for n := 101; n <= 109; n++ {
			add(n, fmt.Sprintf("%040x", n))
		}
		return r
	}
	t.Run("ten pull requests, the last a train of three", func(t *testing.T) {
		r := build(false)
		res := mainRed(t, mrNeeds("test-windows"), r, r.commits[len(r.commits)-1], mrSha('9'))
		if n := strings.Count(res.out, " train, Gate: "); res.rc != 0 || n != 10 || strings.Contains(res.out, "not read") {
			t.Errorf("exit %d, %d trains read, want 10 and nothing left:\n%s", res.rc, n, res.out)
		}
		if !strings.Contains(res.out, "\nEvery red leg ran green in the train gate: ") {
			t.Errorf("a range of 10 pull requests, all read, makes the claim:\n%s", res.out)
		}
	})
	t.Run("and one pull request beyond", func(t *testing.T) {
		r := build(true)
		res := mainRed(t, mrNeeds("test-windows"), r, r.commits[len(r.commits)-1], mrSha('9'))
		if n := strings.Count(res.out, " train, Gate: "); res.rc != 0 || n != 10 || strings.Contains(res.out, "#90 ") {
			t.Errorf("exit %d, %d trains read, want the newest 10:\n%s", res.rc, n, res.out)
		}
		if !strings.Contains(res.out, "\nand 1 more commit(s) of the range, not read\n") || strings.Contains(res.out, "Every red leg") {
			t.Errorf("only the commit of the 11th is left, and no claim is made:\n%s", res.out)
		}
		if n := strings.Count(res.log, "/pulls --jq "); n != len(r.commits) {
			t.Errorf("%d pulls calls, want one per commit up to the first of the 11th (%d):\n%s", n, len(r.commits), res.log)
		}
	})
}

// Every gh or jq failure prints the line and exits 0: the issue opens with what there is.
func TestMainRedFailsOpen(t *testing.T) {
	r := mrRange{commits: []string{mrSha('a')}, prOf: map[string]int{mrSha('a'): 7},
		prs: map[int]mrPR{7: {ref: "train/7", body: trainBodyWith("Gate: gate=hosted run=7 legs=8 9m59s")}}}
	for _, c := range []struct {
		name, needs, sha, step string
		env                    []string
	}{
		{"compare", mrNeeds("web"), mrSha('a'), "compare", []string{"FAKE_FAIL=compare"}},
		{"pulls", mrNeeds("web"), mrSha('a'), "pulls", []string{"FAKE_FAIL=pulls"}},
		{"body", mrNeeds("web"), mrSha('a'), "body", []string{"FAKE_FAIL=body"}},
		{"needs not JSON", "{not json", mrSha('a'), "needs", nil},
		{"needs empty", "", mrSha('a'), "needs", nil},
		{"a sha that is not one", mrNeeds("web"), "main;x", "arguments", nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			res := mainRed(t, c.needs, r, c.sha, mrSha('9'), c.env...)
			lines := strings.Split(strings.TrimSuffix(res.out, "\n"), "\n")
			if res.rc != 0 || len(lines) < 2 || lines[len(lines)-2] != "Gate coverage: could not be read ("+c.step+")" ||
				lines[len(lines)-1] != "Found-by: main-ci" {
				t.Errorf("exit %d, want the could-not-be-read line for %s, then Found-by:\n%s", res.rc, c.step, res.out)
			}
			if c.step == "arguments" && res.log != "" {
				t.Errorf("an invalid sha reaches no API path:\n%s", res.log)
			}
			if c.step != "needs" && !strings.HasPrefix(res.out, "Red legs: web\n") {
				t.Errorf("what was read before the failure stays:\n%s", res.out)
			}
		})
	}
}

// A job name in NEEDS_JSON reaches the issue body: it is printed inert, never run.
func TestMainRedPrintsLegNamesInert(t *testing.T) {
	r := mrRange{commits: []string{mrSha('a')}, prOf: map[string]int{mrSha('a'): 7},
		prs: map[int]mrPR{7: {ref: "train/7", body: trainBodyWith("Gate: gate=hosted run=7 legs=8 9m59s")}}}
	needs := `{"$(touch pwned)":{"result":"failure"},"` + "`touch pwned2`" + `":{"result":"failure"},"@user [x](https://example.com)":{"result":"failure"}}`
	res := mainRed(t, needs, r, mrSha('a'), "")
	first := strings.SplitN(res.out, "\n", 2)[0]
	if res.rc != 0 || first != "Red legs: __touch_pwned_, _touch_pwned2_, _user__x__https___example_com_" {
		t.Errorf("exit %d, the red legs line is %q", res.rc, first)
	}
	if strings.ContainsAny(res.out, "$`@[]()") {
		t.Errorf("a forged name reached the output:\n%s", res.out)
	}
	if !strings.Contains(res.out, "\nThe gate of #7 did not run __touch_pwned_: a leg scripts/main-red.sh does not know\n") {
		t.Errorf("a leg the table does not name is never covered:\n%s", res.out)
	}
	if files, _ := filepath.Glob(filepath.Join(res.dir, "pwned*")); len(files) != 0 {
		t.Errorf("a leg name was run: %v", files)
	}
}

// mainStatusNeeds is the needs list of main-status in ci.yml.
func mainStatusNeeds(t *testing.T) []string {
	t.Helper()
	wf, _ := loadWorkflow(t, "ci.yml")
	needs := needsOf(wf.Jobs["main-status"].Needs)
	if len(needs) == 0 {
		t.Fatal("ci.yml: main-status has no needs")
	}
	return needs
}

// The coverage table of main-red.sh names exactly the legs main-status watches, for each mode: a
// leg added to main-status is never covered by default, and this fails until the table names it.
func TestMainRedKnowsEveryLegOfMainStatus(t *testing.T) {
	needs := mainStatusNeeds(t)
	sort.Strings(needs)
	script := readFile(t, repoRoot(t), "scripts/main-red.sh")
	for _, mode := range []string{"hosted", "host"} {
		seen := map[string]bool{}
		for _, m := range regexp.MustCompile(`"gate=`+mode+` ([a-z0-9-]+)"`).FindAllStringSubmatch(script, -1) {
			seen[m[1]] = true
		}
		var got []string
		for l := range seen {
			got = append(got, l)
		}
		sort.Strings(got)
		if !reflect.DeepEqual(got, needs) {
			t.Errorf("the table of gate=%s names %v, main-status watches %v", mode, got, needs)
		}
	}
	t.Run("every leg red", func(t *testing.T) {
		for _, c := range []struct{ gate, want string }{
			{"gate=hosted run=7 legs=8 9m59s", "Every red leg ran green in the train gate"},
			{"gate=host linux/amd64 windows=2 web=yes 1m00s", "The gate of #7 did not run test-macos"},
		} {
			r := mrRange{commits: []string{mrSha('a')}, prOf: map[string]int{mrSha('a'): 7},
				prs: map[int]mrPR{7: {ref: "train/7", body: trainBodyWith("Gate: " + c.gate)}}}
			res := mainRed(t, mrNeeds(needs...), r, mrSha('a'), "")
			if !strings.Contains(res.out, c.want) || strings.Contains(res.out, "does not know") {
				t.Errorf("%s over every leg of main-status:\n%s", c.gate, res.out)
			}
		}
	})
}

// Every description the signoff can post becomes, through the lander's gate_line, a Gate line
// main-red.sh reads: the three ends of the chain agree on one grammar.
func TestMainRedReadsEveryGateLineTheLanderWrites(t *testing.T) {
	needs := mainStatusNeeds(t)
	for _, d := range []string{
		"gate=hosted run=12345678901 legs=8 12m03s - make gate smoke-http smoke e2e web-gate",
		"gate=hosted fallback=host-unavailable run=12345678901 legs=8 12m03s - make gate smoke-http smoke e2e",
		"gate=host linux/amd64 windows=3 web=yes 7m40s flaky=1 - make gate smoke-http smoke e2e web-gate",
		"gate=host darwin/arm64 windows=none web=no 7m40s - make gate smoke-http smoke e2e",
		"gate=host linux/amd64 windows=unavailable web=no 7m40s flaky=12 - make gate smoke-http smoke e2e",
	} {
		if !statusGrammar.MatchString(d) {
			t.Fatalf("%q is not a description the signoff posts", d)
		}
		line := gateLineOf(t, d)
		r := mrRange{commits: []string{mrSha('a')}, prOf: map[string]int{mrSha('a'): 7},
			prs: map[int]mrPR{7: {ref: "train/7", body: trainBodyWith(line)}}}
		res := mainRed(t, mrNeeds(needs...), r, mrSha('a'), "")
		if res.rc != 0 || !strings.Contains(res.out, "\n#7 train, "+line+"\n") || strings.Contains(res.out, "unknown") ||
			strings.Contains(res.out, "does not know") {
			t.Errorf("%q, written as %q, is not read:\n%s", d, line, res.out)
		}
	}
}

// main-status runs the script from a checkout without credentials, with the job results in the
// environment only, the two read permissions it needs beside the two it had, and a guarded call:
// neither a failed checkout nor a broken script keeps the issue from opening.
func TestMainStatusRunsTheCoverageScriptWithoutCredentials(t *testing.T) {
	var wf struct {
		Jobs map[string]struct {
			Permissions map[string]string `yaml:"permissions"`
			Steps       []struct {
				Uses            string            `yaml:"uses"`
				With            map[string]any    `yaml:"with"`
				Env             map[string]string `yaml:"env"`
				Run             string            `yaml:"run"`
				ContinueOnError bool              `yaml:"continue-on-error"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal([]byte(readFile(t, repoRoot(t), ".github/workflows/ci.yml")), &wf); err != nil {
		t.Fatal(err)
	}
	job := wf.Jobs["main-status"]
	if want := map[string]string{"issues": "write", "actions": "read", "contents": "read", "pull-requests": "read"}; !reflect.DeepEqual(job.Permissions, want) {
		t.Errorf("main-status permissions are %v, want exactly %v", job.Permissions, want)
	}
	if len(job.Steps) != 2 {
		t.Fatalf("main-status has %d steps, want the checkout and the issue step", len(job.Steps))
	}
	co, step := job.Steps[0], job.Steps[1]
	if !strings.HasPrefix(co.Uses, "actions/checkout@") || fmt.Sprint(co.With["persist-credentials"]) != "false" ||
		fmt.Sprint(co.With["sparse-checkout"]) != "scripts/main-red.sh" || !co.ContinueOnError {
		t.Errorf("main-status checks out only scripts/main-red.sh, without credentials, and goes on when the checkout fails: %+v", co)
	}
	if step.Env["NEEDS_JSON"] != "${{ toJSON(needs) }}" {
		t.Errorf("NEEDS_JSON is %q, want the job results through env", step.Env["NEEDS_JSON"])
	}
	for k, v := range step.Env {
		if strings.Contains(v, "secrets.") {
			t.Errorf("main-status holds a secret in %s", k)
		}
	}
	if strings.Contains(step.Run, "${{") {
		t.Error("main-status interpolates an expression into its script")
	}
	call := regexp.MustCompile(`coverage=\$\(bash scripts/main-red\.sh "\$REPO" "\$SHA" "\$green"\) \|\|\s+coverage=`)
	if !call.MatchString(step.Run) || !strings.Contains(step.Run, `"$body" "$coverage"`) {
		t.Errorf("main-status must append the script's output to the body, guarded by ||:\n%s", step.Run)
	}
}

const mainStatusGH = `#!/bin/bash
case "$1 $2" in
  "issue list" | "label create") ;;
  "run list") echo 9999999999999999999999999999999999999999 ;;
  "issue create")
    while [ "$#" -gt 0 ]; do [ "$1" != -b ] || printf '%s' "$2" > "$FAKE_DIR/body"; shift; done ;;
  *) echo "unexpected gh $*" >&2; exit 1 ;;
esac
`

// When scripts/main-red.sh is missing (its checkout failed) or exits non-zero, the issue main-status
// opens still ends with the coverage line and then Found-by: main-ci, the line the escape report
// counts. The step's own run text is executed, with the shell GitHub runs it with.
func TestMainStatusFallbackSaysFoundBy(t *testing.T) {
	bash := bashOrSkip(t)
	var wf struct {
		Jobs map[string]struct {
			Steps []struct {
				Run string `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal([]byte(readFile(t, repoRoot(t), ".github/workflows/ci.yml")), &wf); err != nil {
		t.Fatal(err)
	}
	steps := wf.Jobs["main-status"].Steps
	if len(steps) != 2 || steps[1].Run == "" {
		t.Fatalf("main-status has no issue step: %+v", steps)
	}
	for _, c := range []struct{ name, script, partial string }{
		{"script missing", "", ""},
		{"script fails", "echo 'Red legs: web'; exit 3\n", "Red legs: web\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(mainStatusGH), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "step.sh"), []byte(steps[1].Run), 0o644); err != nil {
				t.Fatal(err)
			}
			work := filepath.Join(dir, "work")
			if err := os.MkdirAll(filepath.Join(work, "scripts"), 0o755); err != nil {
				t.Fatal(err)
			}
			if c.script != "" {
				if err := os.WriteFile(filepath.Join(work, "scripts", "main-red.sh"), []byte(c.script), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.Command(bash, "--noprofile", "--norc", "-eo", "pipefail", filepath.Join(dir, "step.sh"))
			cmd.Dir = work
			cmd.Env = append(os.Environ(), "FAKE_DIR="+dir, "REPO=up/proj", "RED=true", "SHA="+mrSha('a'),
				"RUN=https://example.com/run", "NEEDS_JSON="+mrNeeds("web"),
				"PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("the issue step: %v\n%s", err, out)
			}
			body, err := os.ReadFile(filepath.Join(dir, "body"))
			if err != nil {
				t.Fatalf("no issue opened: %v", err)
			}
			want := "\n" + c.partial + "Gate coverage: could not be read (scripts/main-red.sh)\nFound-by: main-ci"
			if !strings.HasSuffix(string(body), want) || strings.Count(string(body), "Found-by:") != 1 {
				t.Errorf("the issue body must end with the could-not-be-read line, then one Found-by: main-ci:\n%s", body)
			}
		})
	}
}

// Both flaky issues the lander opens, after a targeted retry and after a re-run (with the e2e
// lines it appends), end with `Found-by: gate`.
func TestLanderFlakyIssuesSayFoundByGate(t *testing.T) {
	for _, fixture := range []string{"FLAKYGO", "E2EFLAKY"} {
		t.Run(fixture, func(t *testing.T) {
			e := newLandEnv(t)
			e.queue("f", "fix(kb): f\n", "2026-01-01T10:00:00Z", map[string]string{fixture: "x\n"})
			if out, ok := e.land(); !ok {
				t.Fatalf("land failed\n%s", out)
			}
			// The fake gh logs its arguments, the body last: the issue's entry ends with its body.
			gh := e.log()
			i, j := strings.Index(gh, "issue create"), strings.Index(gh, "\n\nFound-by: gate\n")
			if i < 0 || j < i || strings.Count(gh, "Found-by:") != 1 {
				t.Fatalf("the flaky issue must end with one Found-by: gate line:\n%s", gh)
			}
			if fixture == "E2EFLAKY" && !strings.Contains(gh[i:j], "03_config_opencode") {
				t.Errorf("the e2e lines come before Found-by:\n%s", gh[i:])
			}
		})
	}
}
