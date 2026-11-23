package landertest

import (
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
	for _, want := range []string{"main: unavailable\n", "triage: unavailable\n"} {
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
