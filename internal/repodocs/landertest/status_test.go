package landertest

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withStatusGH wraps the fake gh of the harness with the two issue lists and the ci.yml run
// `make status` reads. FAKE_STATUS_GH=down makes every gh call fail.
func (e *landEnv) withStatusGH() {
	e.t.Helper()
	gh := filepath.Join(e.bin, "gh")
	e.run(e.tmp, "mv", gh, gh+".orig")
	e.write(gh, `#!/bin/bash
[ "${FAKE_STATUS_GH:-}" != down ] || exit 1
case "$1 $2" in
  "issue list")
    case "$*" in
      *ci-red*) echo '  #9 main is red' ;;
      *"--label idea"*) echo '  #4 an idea' ;;
      *) echo '  #3 an untriaged idea' ;;
    esac ;;
  "run list")
    case "$*" in *ci.yml*) echo failure ;; *) exec `+gh+`.orig "$@" ;; esac ;;
  *) exec `+gh+`.orig "$@" ;;
esac
`)
}

func (e *landEnv) status(extra ...string) string {
	e.t.Helper()
	out, errb, code := e.acScript("status.sh", extra)
	if code != 0 {
		e.t.Fatalf("status.sh exits %d\n%s%s", code, out, errb)
	}
	return out
}

// AC-P3-20: one command lists every rejected change with its reason, the queue with verdicts
// and the untriaged issues, and the plans of the maintainer's own lister.
func TestStatusListsWhatWaitsForTheMaintainer(t *testing.T) {
	e := newLandEnv(t)
	e.withStatusGH()
	e.queue("rej", "fix(kb): rej\n", "2026-01-01T09:00:00Z", map[string]string{"r.txt": "r\n"})
	r := e.upGit("rev-parse", "ready/rej")
	e.upGit("update-ref", "refs/heads/failed/rej", e.upGit("commit-tree", r+"^{tree}", "-p", r, "-m", "lander: a conflict with main"))
	e.upGit("update-ref", "-d", "refs/heads/ready/rej")
	e.queue("a", "fix(kb): a\n", "2026-01-01T10:00:00Z", map[string]string{"a2.txt": "a\n"})
	e.queueChild("b", "a", "2026-01-01T11:00:00Z", map[string]string{"b.txt": "b\n"})
	// A worktree not queued whose diff touches a supply-chain path (a test file of an invariant area is not a production path: not counted; an AGENTS.md of one matches two rows and counts once).
	e.git("checkout", "-q", "-b", "feat/unread", "main")
	e.commitFiles("wip", map[string]string{"scripts/x.sh": "#!/bin/sh\n", "internal/kb/k.go": "package kb\n", "internal/mcpserver/a_test.go": "package mcpserver\n", "internal/mcpserver/AGENTS.md": "# a\n"})
	e.git("checkout", "-q", "main")
	e.git("worktree", "add", "-q", filepath.Join(e.tmp, "wt-unread"), "feat/unread")

	out := e.status("STATUS_PLANS=echo plan-one")
	for _, want := range []string{
		"rejected:\n  rej: lander: a conflict with main\n",
		"queue:\n",
		"  a: fix(kb): a [pre-check: ok]\n    b: fix(kb): b (after a)\n",
		"lander: not running",
		"main:\n  ci.yml: failure\n  ci-red #9 main is red\n",
		"triage:\n  #3 an untriaged idea\n",
		"export:\n  #4 an idea\n",
		"trend: 24h opened 0, closed 0\n",
		"plans:\n  plan-one\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the status lacks %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "to read:\n  unread: 2 file(s)\n") {
		t.Errorf("a worktree touching a standing-no path is to read, with the count:\n%s", out)
	}
	// Sections with no line are not printed.
	e2 := newLandEnv(t)
	e2.write(filepath.Join(e2.bin, "gh"), "#!/bin/sh\nexit 0\n")
	if out := e2.status(); strings.Contains(out, "rejected:") || strings.Contains(out, "to read:") || strings.Contains(out, "plans:") {
		t.Errorf("empty sections are not printed:\n%s", out)
	}
}

// A gh that fails everywhere: the status still exits 0 and says which sections are unavailable.
func TestStatusNeverFailsWhenGitHubDoes(t *testing.T) {
	e := newLandEnv(t)
	e.withStatusGH()
	out := e.status("FAKE_STATUS_GH=down")
	for _, want := range []string{"main: unavailable\n", "triage: unavailable\n", "export: unavailable\n", "trend: unavailable\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("lacks %q:\n%s", want, out)
		}
	}
}

// AC-P3-21: the status never pushes or writes a ref on origin, and opens or edits no issue; its
// fetch updates remote-tracking refs only, so the refs of origin and the tree are what is compared.
func TestStatusWritesNothing(t *testing.T) {
	e := newLandEnv(t)
	e.withStatusGH()
	e.queue("a", "fix(kb): a\n", "2026-01-01T10:00:00Z", map[string]string{"a2.txt": "a\n"})
	snap := func() string { return e.upGit("for-each-ref", "--format=%(refname) %(objectname)") }
	before, tree := snap(), e.git("status", "--porcelain")
	e.status()
	if snap() != before || e.git("status", "--porcelain") != tree {
		t.Errorf("the status changed a ref or the tree")
	}
	if log := e.log(); strings.Contains(log, "issue create") || strings.Contains(log, "issue edit") || strings.Contains(log, " -X ") {
		t.Errorf("the status wrote through gh:\n%s", log)
	}
}

// A row that is not seven fields, or a pattern that is no regular expression, is not skipped:
// the to-read section says it is unavailable.
func TestStatusToReadSaysWhenProfilesCannotBeRead(t *testing.T) {
	for name, row := range map[string]string{"fields": "odd\t*\n", "regexp": "odd\t*\t^(x\t-\t-\t-\tno\n"} {
		e := newLandEnv(t)
		e.withStatusGH()
		tsv := filepath.Join(e.work, "scripts", "profiles.tsv")
		e.write(tsv, readFileAbs(t, tsv)+row)
		e.git("commit", "-qam", "profiles: a row the status cannot read")
		e.git("push", "-q", "origin", "main")
		e.git("checkout", "-q", "-b", "feat/unread", "main")
		e.commitFiles("wip", map[string]string{"scripts/x.sh": "#!/bin/sh\n"})
		e.git("checkout", "-q", "main")
		e.git("worktree", "add", "-q", filepath.Join(e.tmp, "wt"), "feat/unread")
		if out := e.status(); !strings.Contains(out, "to read: unavailable (") {
			t.Errorf("%s: the section says it is unavailable:\n%s", name, out)
		}
	}
}

// The triage section leaves the ideas out: they are not accepted here, they are exported (#2440).
func TestStatusTriageLeavesTheIdeasOut(t *testing.T) {
	st := readFile(t, repoRoot(t), "scripts/status.sh")
	if !strings.Contains(st, `(index("accepted") or index("idea")) | not`) {
		t.Error("scripts/status.sh: the triage section must skip the issues labelled `idea`: they are listed under export:")
	}
}

// TestStatusPrintsTheTrendByFoundBy pins the trend line (#2440): the bug and ci-red issues opened
// and closed in the last 24 hours, by the Found-by value of a trusted body.
func TestStatusPrintsTheTrendByFoundBy(t *testing.T) {
	e := newLandEnv(t)
	e.withStatusGH()
	// The clock is 2026-01-03T00:00:00Z.
	issue := func(label, created, closed, body, assoc string) string {
		c := "null"
		if closed != "" {
			c = `"` + closed + `"`
		}
		return fmt.Sprintf(`{"number":1,"title":"x","labels":[{"name":%q}],"created_at":%q,"closed_at":%s,"body":%q,"user":{"login":"user"},"author_association":%q}`,
			label, created, c, body, assoc)
	}
	issues := "[" + strings.Join([]string{
		issue("bug", "2026-01-02T10:00:00Z", "", "Found-by: rc\r\n", "MEMBER"),
		issue("bug", "2026-01-02T11:00:00Z", "2026-01-02T12:00:00Z", "Found-by: gate", "MEMBER"),
		issue("bug", "2026-01-02T11:00:00Z", "", "x\nFound-by: gate\n", "MEMBER"),
		issue("ci-red", "2026-01-02T11:00:00Z", "", "", "MEMBER"),
		issue("bug", "2026-01-02T11:00:00Z", "", "Found-by: rc", "NONE"),                        // an outsider's line is not read
		issue("bug", "2025-12-01T00:00:00Z", "2026-01-02T01:00:00Z", "Found-by: user", "OWNER"), // old, closed today
		issue("bug", "2025-12-01T00:00:00Z", "2025-12-02T00:00:00Z", "Found-by: rc", "OWNER"),   // only updated today
		issue("idea", "2026-01-02T11:00:00Z", "", "", "MEMBER"),
		`{"number":2,"title":"a PR","labels":[{"name":"bug"}],"created_at":"2026-01-02T11:00:00Z","pull_request":{},"user":{"login":"user"},"author_association":"MEMBER"}`,
	}, ",") + "]"
	out := e.status("FAKE_ISSUES="+issues, "STATUS_NOW=1767398400")
	if want := "trend: 24h opened 5 (gate 2, none 2, rc 1), closed 2 (gate 1, user 1)\n"; !strings.Contains(out, want) {
		t.Errorf("the status lacks %q:\n%s", want, out)
	}
	if log := e.log(); !strings.Contains(log, "issues?state=all&since=2026-01-02T00:00:00Z&") {
		t.Errorf("the trend asks GitHub only for the issues updated in the window:\n%s", log)
	}
}

type statusJSON struct {
	Schema   *int `json:"schema"`
	Queue    *[]struct{ Slug, Title string }
	Rejected *[]struct{ Slug, Reason string }
	Issues   map[string][]struct {
		Number   int
		Class    string
		AgeHours *int `json:"age_hours"`
		Late     bool
		BlocksRC bool     `json:"blocks_rc"`
		FoundBy  []string `json:"found_by"`
		Title    string
	}
	Parked     *[]string
	Unlanded   *[]string
	RCBlockers *[]string `json:"rc_blockers"`
	Lander     *struct {
		Alive bool
		Phase *string
		Since *int
	}
}

func (e *landEnv) statusJSON(extra ...string) (statusJSON, map[string]json.RawMessage) {
	e.t.Helper()
	out, errb, code := e.acScript("status.sh", extra, "--json")
	var st statusJSON
	var keys map[string]json.RawMessage
	if code != 0 || json.Unmarshal([]byte(out), &st) != nil || json.Unmarshal([]byte(out), &keys) != nil {
		e.t.Fatalf("status.sh --json exits %d and must print one JSON object\n%s%s", code, out, errb)
	}
	for _, k := range []string{"schema", "queue", "rejected", "issues", "parked", "unlanded", "rc_blockers", "lander"} {
		if _, ok := keys[k]; !ok {
			e.t.Errorf("the JSON status lacks the key %q:\n%s", k, out)
		}
	}
	if len(keys) != 8 {
		e.t.Errorf("the JSON status has %d keys, the schema has 8:\n%s", len(keys), out)
	}
	return st, keys
}

// TestStatusJSONHasAFixedSchema pins `make status JSON=1` (#2440): the queue, the rejected changes
// with their reason, the issues by state, the parked and unlanded work, what blocks the next
// candidate and the lander, every key present whatever is there.
func TestStatusJSONHasAFixedSchema(t *testing.T) {
	e := newLandEnv(t)
	e.withStatusGH()
	e.queue("rej", "fix(kb): rej\n", "2026-01-01T09:00:00Z", map[string]string{"r.txt": "r\n"})
	r := e.upGit("rev-parse", "ready/rej")
	e.upGit("update-ref", "refs/heads/failed/fix-7", e.upGit("commit-tree", r+"^{tree}", "-p", r, "-m", "lander: a conflict with main"))
	e.upGit("update-ref", "-d", "refs/heads/ready/rej")
	e.queue("a", "fix(kb): a\n", "2026-01-01T10:00:00Z", map[string]string{"a2.txt": "a\n"})
	e.git("push", "-q", "origin", "main:refs/heads/wip/later")
	state := filepath.Join(e.tmp, "lander")
	e.write(filepath.Join(state, "pid"), fmt.Sprint(os.Getpid()))
	e.write(filepath.Join(state, "status.json"), `{"pid":1,"phase":"gate","since":1767390000}`)
	issues := `[
 {"number":3,"title":"found by the candidate","labels":[{"name":"bug"},{"name":"accepted"}],"assignees":[],"created_at":"2026-01-02T15:00:00Z","body":"Found-by: rc","user":{"login":"user"},"author_association":"MEMBER"},
 {"number":7,"title":"rejected fix","labels":[{"name":"bug"},{"name":"accepted"}],"assignees":[],"created_at":"2026-01-02T23:00:00Z","body":"","user":{"login":"user"},"author_association":"MEMBER"},
 {"number":9,"title":"an idea","labels":[{"name":"idea"}],"assignees":[],"user":{"login":"user"},"author_association":"MEMBER"}
]`
	st, _ := e.statusJSON("FAKE_ISSUES="+issues, "STATUS_NOW=1767398400")
	if st.Schema == nil || *st.Schema != 1 {
		t.Errorf("schema: %v, want 1", st.Schema)
	}
	if st.Queue == nil || len(*st.Queue) != 1 || (*st.Queue)[0].Slug != "a" || (*st.Queue)[0].Title != "fix(kb): a" {
		t.Errorf("queue: %+v", st.Queue)
	}
	if st.Rejected == nil || len(*st.Rejected) != 1 || (*st.Rejected)[0].Slug != "fix-7" || (*st.Rejected)[0].Reason != "lander: a conflict with main" {
		t.Errorf("rejected: %+v", st.Rejected)
	}
	for _, s := range []string{"needs-maintainer", "queued", "rejected", "fixing", "external", "untriaged", "open", "idle"} {
		if _, ok := st.Issues[s]; !ok {
			t.Errorf("issues lacks the state %q: %+v", s, st.Issues)
		}
	}
	if i := st.Issues["idle"]; len(i) != 1 || i[0].Number != 3 || i[0].Class != "bug" || i[0].AgeHours == nil || *i[0].AgeHours != 9 || !i[0].Late || !i[0].BlocksRC ||
		strings.Join(i[0].FoundBy, ",") != "rc" || i[0].Title != "found by the candidate" {
		t.Errorf("issues.idle: %+v", i)
	}
	if i := st.Issues["open"]; len(i) != 1 || i[0].Class != "export" || i[0].AgeHours != nil || i[0].FoundBy == nil {
		t.Errorf("issues.open: %+v", i)
	}
	if len(st.Issues["rejected"]) != 1 {
		t.Errorf("issues.rejected: %+v", st.Issues["rejected"])
	}
	if st.Parked == nil || strings.Join(*st.Parked, ",") != "later" {
		t.Errorf("parked: %v", st.Parked)
	}
	if st.Unlanded == nil || !strings.Contains(strings.Join(*st.Unlanded, "\n"), "wip later") {
		t.Errorf("unlanded: %v", st.Unlanded)
	}
	if st.RCBlockers == nil || strings.Join(*st.RCBlockers, ",") != "#3,failed/fix-7" {
		t.Errorf("rc_blockers: %v, want #3 and failed/fix-7", st.RCBlockers)
	}
	if l := st.Lander; l == nil || !l.Alive || l.Phase == nil || *l.Phase != "gate" || l.Since == nil || *l.Since != 1767390000 {
		t.Errorf("lander: %+v", l)
	}
	// It writes nothing, like the text status.
	if log := e.log(); strings.Contains(log, "issue create") || strings.Contains(log, "issue edit") || strings.Contains(log, " -X ") {
		t.Errorf("the JSON status wrote through gh:\n%s", log)
	}
}

// With nothing queued and a GitHub that does not answer, the JSON status is still one object with
// every key: the lists are empty, what could not be read is null, and no lander is `stopped`.
func TestStatusJSONKeepsItsKeysWhenGitHubIsDown(t *testing.T) {
	e := newLandEnv(t)
	e.withStatusGH()
	st, keys := e.statusJSON("FAKE_STATUS_GH=down")
	if string(keys["issues"]) != "null" || string(keys["rc_blockers"]) != "null" {
		t.Errorf("issues and rc_blockers are null when GitHub cannot be read: %s %s", keys["issues"], keys["rc_blockers"])
	}
	if string(keys["queue"]) != "[]" || string(keys["rejected"]) != "[]" || string(keys["parked"]) != "[]" || string(keys["unlanded"]) != "[]" {
		t.Errorf("the empty lists are lists: %v", keys)
	}
	if l := st.Lander; l == nil || l.Alive || l.Phase == nil || *l.Phase != "stopped" || l.Since != nil {
		t.Errorf("lander: %+v", l)
	}
	// A status.json left by a lander that died is not its phase.
	e.write(filepath.Join(e.tmp, "lander", "status.json"), `{"pid":1,"phase":"gate","since":5}`)
	if st, _ := e.statusJSON(); st.Lander == nil || st.Lander.Alive || *st.Lander.Phase != "stopped" || st.Lander.Since != nil {
		t.Errorf("a dead lander is stopped: %+v", st.Lander)
	}
	e.write(filepath.Join(e.tmp, "lander", "status.json"), `{"pid":1,"phase":"stopped","since":5}`)
	if st, keys := e.statusJSON(); st.Lander.Since == nil || *st.Lander.Since != 5 || string(keys["rc_blockers"]) != "[]" || len(st.Issues) != 8 {
		t.Errorf("a lander that stopped says since when, and no issue is eight empty states: %+v %v", st.Lander, keys)
	}
}

// `make status JSON=1` reaches the script as --json.
func TestMakeStatusPassesJSON(t *testing.T) {
	if mk := readFile(t, repoRoot(t), "Makefile"); !strings.Contains(mk, "./scripts/status.sh $(if $(JSON),--json)") {
		t.Error("Makefile: `make status JSON=1` must run scripts/status.sh --json")
	}
}

// The JSON status finds the lander's state where scripts/land.sh keeps it when LAND_STATE is not
// set: the directory land.sh itself creates under XDG_STATE_HOME.
func TestStatusJSONReadsTheLanderWhereLandKeepsIt(t *testing.T) {
	e := newLandEnv(t)
	e.withStatusGH()
	xdg := filepath.Join(e.tmp, "xdg")
	env := []string{"LAND_STATE=", "XDG_STATE_HOME=" + xdg}
	if _, errb, code := e.acScript("land.sh", env, "--status"); code != 0 {
		t.Fatalf("land.sh --status exits %d\n%s", code, errb)
	}
	dirs, err := filepath.Glob(filepath.Join(xdg, "cartographer-lander", "*"))
	if err != nil || len(dirs) != 1 {
		t.Fatalf("land.sh keeps its state in one directory under XDG_STATE_HOME, got %v (%v)", dirs, err)
	}
	e.write(filepath.Join(dirs[0], "pid"), fmt.Sprint(os.Getpid()))
	e.write(filepath.Join(dirs[0], "status.json"), `{"pid":1,"phase":"collecting","since":7}`)
	if st, _ := e.statusJSON(env...); st.Lander == nil || !st.Lander.Alive || st.Lander.Phase == nil || *st.Lander.Phase != "collecting" {
		t.Errorf("the lander of %s is alive and collecting: %+v", dirs[0], st.Lander)
	}
}

// Without a working jq the JSON status cannot build its object: it still prints one, and every
// key but the schema is null, a read that failed, never an empty list that reads as "nothing".
func TestStatusJSONIsAllNullWithoutJq(t *testing.T) {
	e := newLandEnv(t)
	e.withStatusGH()
	e.queue("a", "fix(kb): a\n", "2026-01-01T10:00:00Z", map[string]string{"a2.txt": "a\n"})
	e.write(filepath.Join(e.bin, "jq"), "#!/bin/sh\nexit 127\n")
	_, keys := e.statusJSON("FAKE_STATUS_GH=down")
	for k, v := range keys {
		if want := "null"; k != "schema" && string(v) != want {
			t.Errorf("%s is %s without jq, want null", k, v)
		}
	}
}
