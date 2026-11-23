package landertest

import (
	"path/filepath"
	"strings"
	"testing"
)

// A maintainer's change that touches the CLI, a client, an installer or a bundled skill is tried on a
// built binary before it is queued (scripts/pr-triage.sh USAGE_PATHS); make ready records the trial.

func usageLine(out string) string {
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "Usage-test: ") {
			return l
		}
	}
	return ""
}

// AC-P4-18: the triage of a change under cmd/cartographer/ prints `Usage-test: required` with the
// paths; one touching only internal/kb/, or only the tests of the CLI, does not; a contributor's
// change is not concerned.
func TestPRTriageRequiresAUsageTest(t *testing.T) {
	e := newLandEnv(t)
	for _, tc := range []struct {
		name, perm string
		files      map[string]string
		want       string // the whole line, "" for none
	}{
		{"the CLI", "ADMIN", map[string]string{"cmd/cartographer/x.go": "package main\n"}, "Usage-test: required — cmd/cartographer/x.go"},
		{"a client and a bundled skill", "ADMIN", map[string]string{"internal/configurator/c.go": "package c\n", "internal/skillbundle/bundled/s/SKILL.md": "# s\n"},
			"Usage-test: required — internal/configurator/c.go internal/skillbundle/bundled/s/SKILL.md"},
		{"provisioning and an installer", "ADMIN", map[string]string{"internal/provisioning/p.go": "package p\n", "install.sh": "#!/bin/sh\n", "install.ps1": "x\n"},
			"Usage-test: required — install.ps1 install.sh internal/provisioning/p.go"},
		{"only the kb package", "ADMIN", map[string]string{"internal/kb/x.go": "package kb\n"}, ""},
		{"the tools package is left out", "ADMIN", map[string]string{"internal/mcpserver/x.go": "package mcpserver\n"}, ""},
		{"only the tests of the CLI", "ADMIN", map[string]string{"cmd/cartographer/x_test.go": "package main\n"}, ""},
		{"a contributor", "READ", map[string]string{"cmd/cartographer/x.go": "package main\n"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e.git("checkout", "-q", "-B", "feat/ut", "main")
			e.commitFiles("wip", tc.files)
			if got := usageLine(e.triageAs(tc.perm, "feat(client): x\n")); got != tc.want {
				t.Errorf("Usage-test line %q, want %q", got, tc.want)
			}
		})
	}
}

// AC-P4-19: make ready without USAGE on such a change exits non-zero and names the waiver form; with
// USAGE it queues and the commit carries `Usage-test:`; a malformed record is refused.
// AC-P4-20: a change outside the list queues as before, and a record given anyway is written.
func TestReadyRecordsTheUsageTest(t *testing.T) {
	e := newLandEnv(t)
	ready := func(extra ...string) (string, int) {
		m := filepath.Join(e.tmp, "msg")
		e.write(m, "feat(client): x\n\nWhy.\n\nUsage-test: waived: a copied line the script drops\n")
		out, errb, code := e.acScript("ready.sh", append([]string{"MSG=" + m, "READY_NO_LANDER=1", "REVIEW=full, 1 rounds"}, extra...))
		return out + errb, code
	}
	e.git("checkout", "-q", "-b", "feat/usage")
	e.commitFiles("wip", map[string]string{"cmd/cartographer/x.go": "package main\n"})
	if out, code := ready(); code == 0 || !strings.Contains(out, "cmd/cartographer/x.go") || !strings.Contains(out, `USAGE="waived: <reason>"`) || e.refs("ready/") != "" {
		t.Errorf("a change on the list is refused without USAGE, naming its paths and the waiver form (exit %d)\n%s", code, out)
	}
	for _, bad := range []string{"fine", "pass", "waived:", "pass, x entries", "pass, 2 entries\nReview: light, 1 round"} {
		if out, code := ready("USAGE=" + bad); code == 0 || e.refs("ready/") != "" {
			t.Errorf("USAGE=%q is refused (exit %d)\n%s", bad, code, out)
		}
	}
	if out, code := ready("USAGE=pass, 3 entries"); code != 0 {
		t.Fatalf("a pass queues\n%s", out)
	}
	msg := e.upGit("log", "-1", "--format=%B", "ready/usage")
	if strings.Count(msg, "Usage-test: ") != 1 || !strings.Contains(msg, "Usage-test: pass, 3 entries") {
		t.Errorf("the queued commit carries the record once, the message's own copy dropped:\n%s", msg)
	}
	if out, code := ready("USAGE=waived: an installer text, tried by hand elsewhere"); code != 0 {
		t.Fatalf("a waiver queues\n%s", out)
	}
	if msg := e.upGit("log", "-1", "--format=%B", "ready/usage"); !strings.Contains(msg, "Usage-test: waived: an installer text, tried by hand elsewhere") {
		t.Errorf("a waiver is written as given:\n%s", msg)
	}

	// Off the list: no record needed, and one given anyway is written.
	e.git("checkout", "-q", "-B", "feat/plain", "origin/main")
	e.commitFiles("wip", map[string]string{"internal/kb/x.go": "package kb\n"})
	if out, code := ready(); code != 0 || strings.Contains(e.upGit("log", "-1", "--format=%B", "ready/plain"), "Usage-test:") {
		t.Errorf("a change off the list queues without a record, and carries none (exit %d)\n%s", code, out)
	}
	if out, code := ready("USAGE=pass, 1 entry"); code != 0 || !strings.Contains(e.upGit("log", "-1", "--format=%B", "ready/plain"), "Usage-test: pass, 1 entry") {
		t.Errorf("a record given off the list is written (exit %d)\n%s", code, out)
	}
}

// Fail closed on the role: a permission the triage could not read (gh failed) still prints the line for a
// change on the list, and make ready refuses to queue when its own read of the permission fails.
func TestUsageTestFailsClosedOnAnUnreadRole(t *testing.T) {
	e := newLandEnv(t)
	e.git("checkout", "-q", "-B", "feat/unread", "main")
	e.commitFiles("wip", map[string]string{"cmd/cartographer/x.go": "package main\n"})
	// gh repo view fails on every call (the fake's counter is shared, so a large number).
	if got := usageLine(e.triageAs("ADMIN", "feat(client): x\n", "FAKE_PERM_FAILS=999")); got != "Usage-test: required — cmd/cartographer/x.go" {
		t.Errorf("an unread role must not hide the usage requirement: %q", got)
	}
	m := filepath.Join(e.tmp, "msg")
	e.write(m, "feat(client): x\n\nWhy.\n")
	out, errb, code := e.acScript("ready.sh", []string{"MSG=" + m, "READY_NO_LANDER=1", "REVIEW=full, 1 rounds", "USAGE=pass, 1 entry", "FAKE_PERM_FAILS=999"})
	if code == 0 || e.refs("ready/") != "" {
		t.Errorf("make ready with an unreadable permission refuses to queue (exit %d)\n%s%s", code, out, errb)
	}
}

// The lander re-runs main's triage on a queued change, as it does for the review tier: a change on the
// usage list with no Usage-test record is rejected, one with a pass or a waiver lands, one off the list
// needs none.
func TestLanderRejectsAChangeOnTheUsageListWithoutARecord(t *testing.T) {
	e := newLandEnv(t)
	e.queue("bare", "feat(client): bare\n\nWhy.\n", "2026-01-01T10:00:00Z", map[string]string{"cmd/cartographer/bare.go": "package main\n"})
	out, ok := e.land()
	if !ok || e.refs("failed/") != "bare" || !strings.Contains(out, "carries no Usage-test record") || !strings.Contains(out, "cmd/cartographer/bare.go") {
		t.Fatalf("a listed change with no record is rejected, naming its paths (ok=%v)\n%s", ok, out)
	}
	e.queue("tested", "feat(client): tested\n\nWhy.\n\nUsage-test: pass, 2 entries\n", "2026-01-01T11:00:00Z", map[string]string{"cmd/cartographer/tested.go": "package main\n"})
	e.queue("waived", "feat(client): waived\n\nWhy.\n\nUsage-test: waived: an installer text\n", "2026-01-01T12:00:00Z", map[string]string{"install.sh": "#!/bin/sh\n"})
	e.queue("plain", "feat(kb): plain\n\nWhy.\n", "2026-01-01T13:00:00Z", map[string]string{"internal/kb/plain.go": "package kb\n"})
	if out, ok := e.land(); !ok || e.refs("ready/") != "" || strings.Contains(e.refs("failed/"), "tested") || strings.Contains(e.refs("failed/"), "waived") || strings.Contains(e.refs("failed/"), "plain") {
		t.Errorf("a pass, a waiver and a change off the list land (ok=%v failed=%q)\n%s", ok, e.refs("failed/"), out)
	}
}
