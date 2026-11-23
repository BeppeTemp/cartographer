package landertest

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// triageAs runs scripts/pr-triage.sh in the branch mode with body as the message, as a role
// (FAKE_PERM: ADMIN is a maintainer, READ a contributor), and returns its output.
func (e *landEnv) triageAs(perm, body string, extra ...string) string {
	e.t.Helper()
	out, errb, code := e.acScript("pr-triage.sh", append([]string{"TRIAGE_BODY=" + body, "FAKE_PERM=" + perm}, extra...))
	if code != 0 {
		e.t.Fatalf("pr-triage.sh: exit %d\n%s%s", code, out, errb)
	}
	return out
}

func tierLine(out string) string {
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "Tier: ") {
			return l
		}
	}
	return "(no Tier line)"
}

// AC-P7-6: the triage prints `Tier: fast` for a maintainer's change of 51 to 150 production lines
// that matches no profile path row (so no invariant area and no supply-chain file), touches nothing
// under docs/decisions/ and holds no binary file and no gitlink; `light`, `standard` or `full` as
// before when any condition fails, one fixture per condition.
func TestPRTriageFastLane(t *testing.T) {
	e := newLandEnv(t)
	lines := func(n int) string { return strings.Repeat("x\n", n) }
	dec := "docs/" + "decisions/"
	for _, tc := range []struct {
		name, perm string
		files      map[string]string
		want       string // the prefix of the Tier line
	}{
		{"a change of sixty lines", "ADMIN", map[string]string{"internal/kb/x.go": lines(60)}, "Tier: fast"},
		{"exactly the cap", "ADMIN", map[string]string{"internal/kb/x.go": lines(150)}, "Tier: fast"},
		{"one line over the cap", "ADMIN", map[string]string{"internal/kb/x.go": lines(151)}, "Tier: standard"},
		{"fifty lines are light", "ADMIN", map[string]string{"internal/kb/x.go": lines(50)}, "Tier: light"},
		{"a contributor", "READ", map[string]string{"internal/kb/x.go": lines(60)}, "Tier: standard"},
		{"a pending decision", "ADMIN", map[string]string{"internal/kb/x.go": lines(60), dec + "D-new-x.md": "# D-new-x\n"}, "Tier: standard"},
		{"an edited numbered decision", "ADMIN", map[string]string{"internal/kb/x.go": lines(60), dec + "D1-x.md": "# D1\n"}, "Tier: standard"},
		{"a binary file in a production path", "ADMIN", map[string]string{"internal/kb/x.go": lines(60), "internal/kb/blob.bin": "\x00\x01\x02\x00"}, "Tier: standard"},
		{"an invariant area", "ADMIN", map[string]string{"internal/mcpserver/x.go": lines(60)}, "Tier: full"},
		{"a script", "ADMIN", map[string]string{"scripts/new.sh": lines(60)}, "Tier: full"},
		{"an agent-steering file", "ADMIN", map[string]string{".claude/skills/cartographer-develop-live/SKILL.md": lines(60)}, "Tier: full"},
		{"a workflow", "ADMIN", map[string]string{".github/workflows/x.yml": lines(60)}, "Tier: full"},
		{"the Makefile", "ADMIN", map[string]string{"Makefile": lines(60)}, "Tier: full"},
		{"gitattributes", "ADMIN", map[string]string{".gitattributes": lines(60)}, "Tier: full"},
		{"the quarantine list", "ADMIN", map[string]string{"test/quarantine.tsv": lines(60)}, "Tier: full"},
		{"an embedded prompt", "ADMIN", map[string]string{"internal/surveyor/poll/instructions.md": lines(60)}, "Tier: full"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e.git("checkout", "-q", "-B", "feat/fl", "main")
			e.commitFiles("wip", tc.files)
			out := e.triageAs(tc.perm, "feat(dev): x\n")
			if got := tierLine(out); !strings.HasPrefix(got, tc.want) {
				t.Errorf("%s, want %s\n%s", got, tc.want, out)
			}
		})
	}
}

// A gitlink (a submodule entry) keeps a change out of the fast lane: its numstat row is a line of
// no file and its content is not in the tree.
func TestPRTriageFastLaneRefusesAGitlink(t *testing.T) {
	e := newLandEnv(t)
	e.git("checkout", "-q", "-B", "feat/gl", "main")
	e.write(filepath.Join(e.work, "internal", "kb", "x.go"), strings.Repeat("x\n", 60))
	e.git("add", ".")
	e.git("update-index", "--add", "--cacheinfo", "160000,"+strings.TrimSpace(e.git("rev-parse", "HEAD"))+",internal/kb/sub")
	e.commitAt("wip", "2026-01-02T00:00:00Z")
	if got := tierLine(e.triageAs("ADMIN", "feat(dev): x\n")); !strings.HasPrefix(got, "Tier: standard") {
		t.Errorf("a gitlink is not the fast lane: %s", got)
	}
}

// A file name with a quote, a backslash or a newline is listed as it is (git -z): the row of the
// invariant area matches it, whatever the name holds.
func TestPRTriageReadsAnUnusualName(t *testing.T) {
	e := newLandEnv(t)
	e.git("checkout", "-q", "-B", "feat/odd", "main")
	e.commitFiles("wip", map[string]string{"internal/mcpserver/a\"b\\c\nd.go": "package mcpserver\n"})
	if got := tierLine(e.triageAs("ADMIN", "chore(mcp): x\n")); !strings.HasPrefix(got, "Tier: full — security: internal/mcpserver/a") {
		t.Errorf("a quote, a backslash and a newline in a name: %s", got)
	}
}

// The lander judges the review record of a queued change by main's triage: a change that
// computes full tier cannot land with a lighter record, and one recorded at its tier or above lands.
func TestLanderRejectsAReviewRecordLowerThanTheComputedTier(t *testing.T) {
	e := newLandEnv(t)
	e.queue("low", "feat(dev): low\n\nWhy.\n\nReview: fast, 1 rounds\n", "2026-01-01T10:00:00Z",
		map[string]string{"scripts/low.sh": "#!/bin/sh\n"})
	out, ok := e.land()
	if !ok || e.refs("failed/") != "low" || !strings.Contains(out, "lower than the tier main's triage computes (full)") {
		t.Fatalf("a full-tier change recorded as fast is rejected (ok=%v)\n%s", ok, out)
	}
	if reason := e.upGit("log", "-1", "--format=%s", "failed/low"); !strings.Contains(reason, "recorded review tier (fast)") {
		t.Errorf("the failed ref says why: %s", reason)
	}
	e.queue("ok", "feat(dev): ok\n\nWhy.\n\nReview: full, 2 rounds\n", "2026-01-01T11:00:00Z",
		map[string]string{"scripts/ok.sh": "#!/bin/sh\n"})
	if out, ok := e.land(); !ok || e.refs("ready/") != "" {
		t.Errorf("a record at the computed tier lands (ok=%v)\n%s", ok, out)
	}
	e.queue("odd", "feat(dev): odd\n\nWhy.\n\nReview: heavy, 1 rounds\n", "2026-01-01T12:00:00Z", map[string]string{"odd.txt": "o\n"})
	if out, _ := e.land(); !strings.Contains(out, "none of light, fast, standard, full") {
		t.Errorf("a record that names no tier is refused\n%s", out)
	}
}

// A rename keeps its new path in the list the triage reads: a file moved into
// .github/workflows/ is a workflow, not the `old => new` text numstat prints with renames.
func TestPRTriageReadsTheNewPathOfARename(t *testing.T) {
	e := newLandEnv(t)
	e.git("checkout", "-q", "-B", "feat/rn", "main")
	body := strings.Repeat("name: x\n", 20)
	base := e.commitFiles("add", map[string]string{"tools/ci.yml": body})
	e.git("mv", "tools/ci.yml", ".github/workflows/moved.yml")
	e.commitAt("move", "2026-01-03T00:00:00Z")
	out := e.triageAs("ADMIN", "feat(dev): x\n", "TRIAGE_BASE="+base)
	if got := tierLine(out); !strings.HasPrefix(got, "Tier: full — process: .github/workflows/moved.yml") {
		t.Errorf("a rename into the workflows is a core file by its new path: %s\n%s", got, out)
	}
}

// A binary file stays in the list: numstat prints `-` for its sizes, and its path still counts for
// the profiles (a binary in an invariant area is a change there).
func TestPRTriageKeepsABinaryFile(t *testing.T) {
	e := newLandEnv(t)
	e.git("checkout", "-q", "-B", "feat/bin", "main")
	e.commitFiles("wip", map[string]string{"internal/mcpserver/blob.bin": "\x00\x01\x02\x00"})
	out := e.triageAs("ADMIN", "chore(mcp): x\n")
	if got := tierLine(out); !strings.HasPrefix(got, "Tier: full — security: internal/mcpserver/blob.bin") {
		t.Errorf("a binary file of an invariant area is read: %s\n%s", got, out)
	}
}

// A non-ASCII name is listed as it is: git C-quotes it by default ("internal/mcpserver/\303\250.go"),
// and no path pattern matches a quoted name. The triage and make status read it unquoted.
func TestTriageAndStatusReadANonASCIIName(t *testing.T) {
	e := newLandEnv(t)
	e.withStatusGH()
	name := "è.go"
	e.git("checkout", "-q", "-B", "feat/utf", "main")
	e.commitFiles("wip", map[string]string{"internal/mcpserver/" + name: "package mcpserver\n"})
	out := e.triageAs("ADMIN", "chore(mcp): x\n")
	if got := tierLine(out); !strings.HasPrefix(got, "Tier: full — security: internal/mcpserver/"+name) {
		t.Errorf("a non-ASCII name of an invariant area is read as it is: %s\n%s", got, out)
	}
	e.git("checkout", "-q", "-B", "feat/utf2", "main")
	e.commitFiles("wip", map[string]string{"scripts/\u00e8.sh": "#!/bin/sh\n"})
	e.git("checkout", "-q", "main")
	e.git("worktree", "add", "-q", filepath.Join(e.tmp, "wt-utf"), "feat/utf2")
	if st := e.status(); !strings.Contains(st, "to read:\n  utf2: 1 file(s)\n") {
		t.Errorf("make status counts a non-ASCII name of a standing-no path:\n%s", st)
	}
}

// A pure move adds no line: the size counts what git's rename pairing says changed, so moving a
// file does not push a change over the cap, while both paths still count for the profiles.
func TestPRTriageDoesNotCountAPureMoveAsAdditions(t *testing.T) {
	e := newLandEnv(t)
	e.git("checkout", "-q", "-B", "feat/mv", "main")
	base := e.commitFiles("add", map[string]string{"internal/kb/a.go": strings.Repeat("x\n", 100)})
	if err := os.MkdirAll(filepath.Join(e.work, "internal", "kb2"), 0o755); err != nil {
		t.Fatal(err)
	}
	e.git("mv", "internal/kb/a.go", "internal/kb2/a.go")
	e.commitAt("move", "2026-01-03T00:00:00Z")
	out := e.triageAs("ADMIN", "refactor(kb): x\n", "TRIAGE_BASE="+base)
	if !strings.Contains(out, "Size: production +0/-0") {
		t.Errorf("a pure move adds no line:\n%s", out)
	}
}

// A file whose lines cannot be counted (a binary file, or one a .gitattributes marks `-diff`:
// numstat prints `-`) counts as over the size cap: it is neither light nor fast, and it BLOCKs
// unless the message carries a `Size exception:`.
func TestPRTriageCountsAnUncountableFileAsOverTheCap(t *testing.T) {
	e := newLandEnv(t)
	for name, files := range map[string]map[string]string{
		"a binary":      {"internal/kb/blob.bin": "\x00\x01\x02\x00"},
		"-diff by attr": {"internal/kb/sub/.gitattributes": "*.go -diff\n", "internal/kb/sub/x.go": strings.Repeat("x\n", 400)},
	} {
		t.Run(name, func(t *testing.T) {
			e.git("checkout", "-q", "-B", "feat/unc", "main")
			e.commitFiles("wip", files)
			out := e.triageAs("ADMIN", "feat(kb): x\n")
			if !strings.Contains(out, "BLOCK: over the size cap") {
				t.Errorf("an uncountable file is over the cap:\n%s", out)
			}
			if got := tierLine(out); strings.HasPrefix(got, "Tier: light") || strings.HasPrefix(got, "Tier: fast") {
				t.Errorf("never light or fast: %s", got)
			}
			if out := e.triageAs("ADMIN", "feat(kb): x\n\nSize exception: a binary asset\n"); strings.Contains(out, "BLOCK") {
				t.Errorf("a Size exception line accepts it:\n%s", out)
			}
		})
	}
}

// Git's rename sizes apply only inside one kind: a test file moved into production is production
// lines in full.
func TestPRTriageCountsAMoveAcrossKindsInFull(t *testing.T) {
	e := newLandEnv(t)
	e.git("checkout", "-q", "-B", "feat/xk", "main")
	base := e.commitFiles("add", map[string]string{"internal/kb/impl_test.go": strings.Repeat("x\n", 400)})
	e.git("mv", "internal/kb/impl_test.go", "internal/kb/impl.go")
	e.commitAt("move", "2026-01-03T00:00:00Z")
	out := e.triageAs("ADMIN", "refactor(kb): x\n", "TRIAGE_BASE="+base)
	if !strings.Contains(out, "Size: production +400/") {
		t.Errorf("a test file moved into production is 400 production lines:\n%s", out)
	}
}

// make ready refuses a queue without the review record, naming its form; the lander rejects a
// queued change that carries none.
func TestReadyRequiresAReviewAndTheLanderRejectsNone(t *testing.T) {
	e := newLandEnv(t)
	e.git("checkout", "-q", "-b", "feat/norev")
	e.write(filepath.Join(e.work, "x.txt"), "x\n")
	e.git("add", "x.txt")
	e.git("commit", "-q", "-m", "wip")
	m := filepath.Join(e.tmp, "msg")
	e.write(m, "feat(kb): x\n")
	for _, rv := range []string{"", "REVIEW=soon", "REVIEW=full"} {
		out, errb, code := e.acScript("ready.sh", []string{"MSG=" + m, "READY_NO_LANDER=1", rv})
		if code == 0 || !strings.Contains(errb, `REVIEW="<tier>, <n> rounds" is required`) || e.refs("ready/") != "" {
			t.Errorf("%q: ready must refuse (exit %d)\n%s%s", rv, code, out, errb)
		}
	}
	e.git("checkout", "-q", "-B", "work-norev", "origin/main")
	e.write(filepath.Join(e.work, "y.txt"), "y\n")
	e.git("add", "y.txt")
	e.git("commit", "-q", "-m", "feat(kb): y")
	e.git("push", "-q", "--force", "origin", "HEAD:refs/heads/ready/norev")
	e.git("checkout", "-q", "main")
	out, ok := e.land()
	if !ok || e.refs("failed/") != "norev" || !strings.Contains(out, "carries no Review record") {
		t.Errorf("a queued change with no Review is rejected (ok=%v)\n%s", ok, out)
	}
}

// A triage that fails to answer (here main's script exits 1) rejects nothing: the lander stops,
// naming it, and the change stays queued for the next pass.
func TestLanderLeavesAChangeQueuedWhenTheTriageFails(t *testing.T) {
	e := newLandEnv(t)
	e.write(filepath.Join(e.work, "scripts", "pr-triage.sh"), "#!/bin/sh\nexit 1\n")
	e.git("commit", "-qam", "main: a triage that fails")
	e.git("push", "-q", "origin", "main")
	e.queue("a", "feat(kb): a\n", "2026-01-01T10:00:00Z", map[string]string{"qa-triage.txt": "a\n"})
	out, ok := e.land()
	if ok || e.refs("ready/") != "a" || e.refs("failed/") != "" || !strings.Contains(out, "could not compute the review tier") || !strings.Contains(out, "git push origin --delete refs/heads/ready/a") {
		t.Errorf("the change stays queued, not rejected (ok=%v)\n%s", ok, out)
	}
}

// manyFiles is n test files with long names: a path list big enough that a producer piped into a
// quiet grep is cut off by SIGPIPE, which pipefail reads as "no match".
func manyFiles(n int) map[string]string {
	files := map[string]string{}
	for i := 0; i < n; i++ {
		files[fmt.Sprintf("internal/kb/%s%d_test.go", strings.Repeat("long", 30), i)] = "package kb\n"
	}
	return files
}

// A long path list cannot hide a decision from the fast lane, nor a reviewer definition from the
// Evals BLOCK, and a path with a byte that is no UTF-8 is triaged without error.
func TestPRTriageReadsALongPathListWholeAndAnyByte(t *testing.T) {
	e := newLandEnv(t)
	dec := "docs/" + "decisions/"
	e.git("checkout", "-q", "-B", "feat/many", "main")
	files := manyFiles(900)
	files["internal/kb/x.go"] = strings.Repeat("x\n", 60)
	files[dec+"D-new-x.md"] = "# D-new-x\n"
	e.commitFiles("wip", files)
	if got := tierLine(e.triageAs("ADMIN", "feat(kb): x\n")); !strings.HasPrefix(got, "Tier: standard") {
		t.Errorf("a decision among 900 files keeps a change out of the fast lane: %s", got)
	}
	e.git("checkout", "-q", "-B", "feat/many2", "main")
	files = manyFiles(900)
	files[".claude/agents/cartographer-contract-reviewer.md"] = "x\n"
	e.commitFiles("wip", files)
	if out := e.triageAs("ADMIN", "feat(dev): x\n"); !strings.Contains(out, "BLOCK: a reviewer definition changed") {
		t.Errorf("a reviewer definition among 900 files still needs its Evals line:\n%s", out)
	}
	e.git("checkout", "-q", "-B", "feat/odd8", "main")
	odd := filepath.Join(e.work, "internal", "kb", "\xffx.go")
	if err := os.MkdirAll(filepath.Dir(odd), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(odd, []byte(strings.Repeat("x\n", 60)), 0o644); err != nil {
		t.Skipf("this file system cannot hold a name that is not UTF-8: %v", err)
	}
	e.git("add", ".")
	e.commitAt("wip", "2026-01-02T00:00:00Z")
	if got := tierLine(e.triageAs("ADMIN", "feat(kb): x\n")); !strings.HasPrefix(got, "Tier: fast") {
		t.Errorf("a non-UTF-8 name is triaged as any other: %s", got)
	}
}
