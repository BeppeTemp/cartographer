package landertest

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestIssueQueueSortsTheFixableFirst pins scripts/issue-queue.sh (make issues): the state of each
// open issue comes from its labels, its assignees and the fix-<n> refs, and the idle ci-red and
// flaky bugs come first; an accepted bug an outsider opened is `external`, outside the standing approval.
func TestIssueQueueSortsTheFixableFirst(t *testing.T) {
	e := newLandEnv(t)
	e.git("push", "-q", "origin", "HEAD:refs/heads/ready/fix-3", "HEAD:refs/heads/failed/fix-4")
	issues := `[
 {"number":1,"title":"an idea","labels":[{"name":"idea"}],"assignees":[],"user":{"login":"user"},"author_association":"MEMBER"},
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
		"#1 idea untriaged", "#2 bug untriaged", "#3 bug queued", "#4 bug rejected", "#5 bug fixing", "#9 bug needs-maintainer", "#10 bug external",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("queue:\n%s\nwant:\n%s\n(output:\n%s)", strings.Join(got, "\n"), strings.Join(want, "\n"), out)
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
// has no Found-by line takes the issue's lines when a maintainer or github-actions[bot] wrote it, never otherwise.
func TestReadyCopiesTheIssueLinesOnlyFromATrustedBody(t *testing.T) {
	for _, c := range []struct {
		name, author string
		copied       bool
	}{
		{"a maintainer's body", `"user":{"login":"user","type":"User"},"author_association":"MEMBER"`, true},
		{"the workflows' bot's body", `"user":{"login":"github-actions[bot]","type":"Bot"},"author_association":"NONE"`, true},
		{"another bot's body", `"user":{"login":"other-app[bot]","type":"Bot"},"author_association":"NONE"`, false},
		{"anyone else's body", `"user":{"login":"user","type":"User"},"author_association":"NONE"`, false},
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
				`FAKE_ISSUE={`+c.author+`,"body":"x\r\nFound-by: gate\r\nIntroduced-by: unknown\r\nFound-by: forged value"}`)
			out, err := cmd.CombinedOutput()
			if !c.copied {
				if err == nil || e.refs("ready/") != "" {
					t.Errorf("lines from an untrusted body must not be copied, so the fix is refused\n%s", out)
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
