package landertest

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestIssueQueueSortsTheFixableFirst pins scripts/issue-queue.sh (make issues): the state of each
// open issue comes from its labels, its assignees and the fix-<n> refs, and the idle ci-red and
// flaky bugs come first; an accepted bug an outsider opened is `external`, outside the standing
// approval; an idea is class `export`, never triaged or accepted here (#2440).
func TestIssueQueueSortsTheFixableFirst(t *testing.T) {
	e := newLandEnv(t)
	e.git("push", "-q", "origin", "HEAD:refs/heads/ready/fix-3", "HEAD:refs/heads/failed/fix-4")
	issues := `[
 {"number":1,"title":"an idea","labels":[{"name":"idea"}],"assignees":[],"user":{"login":"user"},"author_association":"MEMBER"},
 {"number":12,"title":"an idea accepted before","labels":[{"name":"idea"},{"name":"accepted"}],"assignees":[],"user":{"login":"user"},"author_association":"MEMBER"},
 {"number":13,"title":"no label at all","labels":[],"assignees":[],"user":{"login":"user"},"author_association":"MEMBER"},
 {"number":2,"title":"untriaged report","labels":[{"name":"bug"}],"assignees":[],"user":{"login":"user"},"author_association":"MEMBER"},
 {"number":3,"title":"queued fix","labels":[{"name":"bug"},{"name":"accepted"}],"assignees":[],"user":{"login":"user"},"author_association":"MEMBER"},
 {"number":4,"title":"rejected fix","labels":[{"name":"bug"},{"name":"accepted"}],"assignees":[],"user":{"login":"user"},"author_association":"MEMBER"},
 {"number":5,"title":"taken","labels":[{"name":"bug"},{"name":"accepted"}],"assignees":[{"login":"user"}],"user":{"login":"user"},"author_association":"MEMBER"},
 {"number":6,"title":"old bug","labels":[{"name":"bug"},{"name":"accepted"}],"assignees":[],"user":{"login":"user"},"author_association":"MEMBER"},
 {"number":7,"title":"flaky (2nd): TestX failed","labels":[{"name":"bug"},{"name":"accepted"}],"assignees":[],"user":{"login":"user"},"author_association":"MEMBER"},
 {"number":8,"title":"main is red","labels":[{"name":"ci-red"}],"assignees":[],"user":{"login":"github-actions[bot]"},"author_association":"NONE"},
 {"number":9,"title":"which way?","labels":[{"name":"bug"},{"name":"accepted"},{"name":"needs-maintainer"}],"assignees":[],"user":{"login":"user"},"author_association":"MEMBER"},
 {"number":10,"title":"an outsider's report","labels":[{"name":"bug"},{"name":"accepted"}],"assignees":[],"user":{"login":"someone"},"author_association":"NONE"},
 {"number":11,"title":"a pull request","labels":[],"assignees":[],"user":{"login":"someone"},"author_association":"NONE","pull_request":{}}
]`
	cmd := exec.Command("bash", filepath.Join(e.work, "scripts", "issue-queue.sh"))
	cmd.Dir = e.work
	cmd.Env = append(append([]string{}, e.env...), "FAKE_ISSUES="+issues)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("issue-queue.sh: %v\n%s", err, out)
	}
	var got []string
	for l := range strings.Lines(string(out)) {
		f := strings.Fields(l)
		got = append(got, f[0]+" "+f[1]+" "+strings.TrimSuffix(f[2], ":"))
	}
	want := []string{
		"#7 bug idle", "#8 ci-red idle", "#6 bug idle",
		"#1 export open", "#2 bug untriaged", "#3 bug queued", "#4 bug rejected", "#5 bug fixing", "#9 bug needs-maintainer", "#10 bug external",
		"#12 export open", "#13 other untriaged",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("queue:\n%s\nwant:\n%s\n(output:\n%s)", strings.Join(got, "\n"), strings.Join(want, "\n"), out)
	}
}

// TestIssueQueueAgesEachIssueAndFlagsTheLate pins the age of scripts/issue-queue.sh (#2440): the
// hours since the issue was opened, `late` past the threshold of its class (ci-red and flaky 2 h,
// a bug found by a candidate 8 h, any other bug 24 h, a question to the maintainer 24 h), and
// what blocks the next candidate; a Found-by line counts only in a body the process trusts.
func TestIssueQueueAgesEachIssueAndFlagsTheLate(t *testing.T) {
	e := newLandEnv(t)
	e.git("push", "-q", "origin", "HEAD:refs/heads/failed/fix-8")
	// The clock is 2026-01-03T00:00:00Z.
	issue := func(n int, title, labels, created, body, assoc string) string {
		var ls []string
		for _, l := range strings.Fields(labels) {
			ls = append(ls, `{"name":"`+l+`"}`)
		}
		return fmt.Sprintf(`{"number":%d,"title":%q,"labels":[%s],"assignees":[],"created_at":%q,"body":%q,"user":{"login":"user"},"author_association":%q}`,
			n, title, strings.Join(ls, ","), created, body, assoc)
	}
	issues := "[" + strings.Join([]string{
		issue(1, "main is red", "ci-red", "2026-01-02T21:00:00Z", "", "MEMBER"),                                        // 3h > 2h
		issue(2, "flaky: TestX", "bug accepted", "2026-01-02T22:30:00Z", "", "MEMBER"),                                 // 1h
		issue(3, "found by the candidate", "bug accepted", "2026-01-02T15:00:00Z", "x\r\nFound-by: rc\r\n", "OWNER"),   // 9h > 8h
		issue(4, "a bug", "bug accepted", "2026-01-02T15:00:00Z", "Found-by: review\n", "MEMBER"),                      // 9h
		issue(5, "an old bug", "bug accepted", "2025-12-30T00:00:00Z", "", "MEMBER"),                                   // 4d > 24h
		issue(6, "a question", "bug accepted needs-maintainer", "2026-01-01T23:00:00Z", "Found-by: nightly", "MEMBER"), // 25h > 24h
		issue(7, "an outsider's line", "bug accepted", "2026-01-02T23:00:00Z", "Found-by: rc", "NONE"),                 // not trusted
		issue(8, "rejected fix", "bug accepted", "2026-01-02T23:00:00Z", "", "MEMBER"),
		issue(9, "an idea", "idea", "2025-01-01T00:00:00Z", "", "MEMBER"), // no threshold
		issue(10, "not accepted", "bug", "2026-01-02T23:00:00Z", "Found-by: rc", "MEMBER"),
		issue(11, "main is red, a bug too", "ci-red bug accepted", "2026-01-02T23:00:00Z", "Found-by: nightly", "MEMBER"), // blocks whatever its class
	}, ",") + "]"
	env := []string{"FAKE_ISSUES=" + issues, "ISSUE_QUEUE_NOW=1767398400"}
	out, errb, code := e.acScript("issue-queue.sh", env)
	if code != 0 {
		t.Fatalf("issue-queue.sh exits %d\n%s%s", code, out, errb)
	}
	for _, want := range []string{
		"#1 ci-red idle: fix it (cartographer-fix-issues) [3h, late: over 2h] — main is red\n",
		" [1h] — flaky: TestX\n",
		"#3 bug idle: fix it (cartographer-fix-issues) [9h, late: over 8h, blocks the candidate] — found by the candidate\n",
		" [9h] — a bug\n",
		" [4d, late: over 24h] — an old bug\n",
		"#6 bug needs-maintainer: the maintainer answers the question on the issue [25h, late: over 24h, blocks the candidate] — a question\n",
		"#7 bug external: the maintainer: a design comment, then an explicit make ready (outside the standing approval) [1h] — an outsider's line\n",
		" [1h, blocks the candidate] — rejected fix\n",
		"#9 export open: the maintainer records it in the backlog, then closes it [367d] — an idea\n",
		" [1h] — not accepted\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the queue lacks %q:\n%s", want, out)
		}
	}
	// The rows a script reads: the same order, no field empty.
	tsv, errb, code := e.acScript("issue-queue.sh", env, "--tsv")
	if code != 0 {
		t.Fatalf("issue-queue.sh --tsv exits %d\n%s%s", code, tsv, errb)
	}
	for _, want := range []string{
		"1\tci-red\tidle\t3\tlate\t-\t-\tmain is red\n",
		"3\tbug\tidle\t9\tlate\tblocks-rc\trc\tfound by the candidate\n",
		"6\tbug\tneeds-maintainer\t25\tlate\tblocks-rc\tnightly\ta question\n",
		"7\tbug\texternal\t1\t-\t-\t-\tan outsider's line\n",
		"8\tbug\trejected\t1\t-\t-\t-\trejected fix\n",
		"9\texport\topen\t8808\t-\t-\t-\tan idea\n",
		"10\tbug\tuntriaged\t1\t-\t-\trc\tnot accepted\n",
		"11\tci-red\tidle\t1\t-\tblocks-rc\tnightly\tmain is red, a bug too\n",
	} {
		if !strings.Contains("\n"+tsv, "\n"+want) {
			t.Errorf("the rows lack %q:\n%s", want, tsv)
		}
	}
	if !strings.HasPrefix(tsv, "1\t") {
		t.Errorf("the rows keep the order of the queue, the late ci-red first:\n%s", tsv)
	}
}

// TestIssueNewOpensAnAcceptedBug pins scripts/issue-new.sh (make issue): a checked Found-by value,
// both lines appended, `bug` and `accepted`.
func TestIssueNewOpensAnAcceptedBug(t *testing.T) {
	e := newLandEnv(t)
	body := filepath.Join(e.tmp, "body")
	e.write(body, "The defect.\n")
	run := func(env ...string) (string, bool) {
		cmd := exec.Command("bash", filepath.Join(e.work, "scripts", "issue-new.sh"))
		cmd.Dir = e.work
		cmd.Env = append(append(append([]string{}, e.env...), "TITLE=a defect", "BODY="+body), env...)
		out, err := cmd.CombinedOutput()
		return string(out), err == nil
	}
	if out, ok := run("FOUND_BY=nonsense"); ok || !strings.Contains(out, "FOUND_BY=<value> is required") || strings.Contains(e.log(), "issue create") {
		t.Errorf("an unknown Found-by value is refused before gh (ok=%v)\n%s", ok, out)
	}
	if out, ok := run("FOUND_BY=gate", "INTRODUCED_BY=#12"); !ok {
		t.Fatalf("issue-new.sh failed\n%s", out)
	}
	gh := e.log()
	if !strings.Contains(gh, "issue create -R up/proj --label bug --label accepted --title a defect --body The defect.\n\nFound-by: gate\nIntroduced-by: #12") {
		t.Errorf("the issue is not an accepted bug with its two lines:\n%s", gh)
	}
}

// TestLanderTitlesAFlakyRepeatOffender pins flaky_head (land.sh): two earlier flaky issues of the
// window name the test as a word of their title, so the new one is the third.
func TestLanderTitlesAFlakyRepeatOffender(t *testing.T) {
	e := newLandEnv(t)
	e.queue("f", "fix(kb): f\n", "2026-01-01T10:00:00Z", map[string]string{"FLAKY": "x\n"})
	prior := `[{"title":"flaky: TestSometimes failed once"},{"title":"flaky (2nd): TestSometimes failed"},{"title":"flaky: TestSometimesNot failed"},{"title":"TestSometimes is slow"}]`
	if out, ok := e.land("FAKE_ISSUES=" + prior); !ok {
		t.Fatalf("land failed\n%s", out)
	}
	if gh := e.log(); !strings.Contains(gh, "--title flaky (3rd): TestSometimes") {
		t.Errorf("the third flaky issue of the test must say so:\n%s", gh)
	}
}

// TestReadyCopiesTheIssueLinesOnlyFromATrustedBody pins ready.sh: a fix that closes an issue and
// has no Found-by line takes the issue's lines when a maintainer or github-actions[bot] wrote it, never
// otherwise; with no line to take, on the issue or in the message, it is refused naming the issue (#2440).
func TestReadyCopiesTheIssueLinesOnlyFromATrustedBody(t *testing.T) {
	const lines = `x\r\nFound-by: gate\r\nIntroduced-by: unknown\r\nFound-by: forged value`
	for _, c := range []struct {
		name, author, body string
		copied             bool
	}{
		{"a maintainer's body", `"user":{"login":"user","type":"User"},"author_association":"MEMBER"`, lines, true},
		{"the workflows' bot's body", `"user":{"login":"github-actions[bot]","type":"Bot"},"author_association":"NONE"`, lines, true},
		{"another bot's body", `"user":{"login":"other-app[bot]","type":"Bot"},"author_association":"NONE"`, lines, false},
		{"anyone else's body", `"user":{"login":"user","type":"User"},"author_association":"NONE"`, lines, false},
		{"a maintainer's body with no line", `"user":{"login":"user","type":"User"},"author_association":"MEMBER"`, `x\r\nIntroduced-by: unknown`, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newLandEnv(t)
			e.git("checkout", "-q", "-b", "fix-12")
			e.write(filepath.Join(e.work, "internal", "kb", "x.go"), "package kb\n")
			e.git("add", ".")
			e.git("commit", "-q", "-m", "wip")
			m := filepath.Join(e.tmp, "msg")
			e.write(m, "fix(kb): x\n\nCloses #12\n")
			cmd := exec.Command("bash", filepath.Join(e.work, "scripts", "ready.sh"))
			cmd.Dir = e.work
			cmd.Env = append(append([]string{}, e.env...), "MSG="+m, "READY_NO_LANDER=1", "REVIEW=full, 1 rounds",
				`FAKE_ISSUE={`+c.author+`,"body":"`+c.body+`"}`)
			out, err := cmd.CombinedOutput()
			if !c.copied {
				if err == nil || e.refs("ready/") != "" {
					t.Errorf("lines from an untrusted body must not be copied, so the fix is refused\n%s", out)
				}
				if !strings.Contains(string(out), "ready: issue #12 has no `Found-by:` line") {
					t.Errorf("the refusal names the issue that lacks the line\n%s", out)
				}
				return
			}
			if err != nil {
				t.Fatalf("ready.sh failed\n%s", out)
			}
			msg := e.upGit("log", "-1", "--format=%B", "ready/"+e.refs("ready/"))
			if !strings.Contains(msg, "Found-by: gate\n") || !strings.Contains(msg, "Introduced-by: unknown") || strings.Contains(msg, "forged") {
				t.Errorf("the queued message does not carry the issue's two lines:\n%s", msg)
			}
		})
	}
}
