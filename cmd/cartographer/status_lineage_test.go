package main

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
)

// statusOf runs the status snapshot of e's configuration and renders it both
// ways, as `status` prints them.
func statusOf(t *testing.T, e *lineageEnv) (snap statusSnapshot, text, js string) {
	t.Helper()
	cfg, err := clientconfig.Load(e.dir)
	if err != nil {
		t.Fatal(err)
	}
	snap = snapshotForConfig(e.dir, cfg, false)
	text = withStdout(t, func() { renderStatus("table", snap, 0) })
	js = withStdout(t, func() { renderStatus("json", snap, 0) })
	return snap, text, js
}

func claudeStatus(t *testing.T, s statusSnapshot) providerStatus {
	t.Helper()
	for _, p := range s.Providers {
		if p.Name == "claude" {
			return p
		}
	}
	t.Fatal("no claude provider in the status")
	return providerStatus{}
}

// AC-lineage-27: status names each lineage KB with the KB it came from, in
// text and JSON.
func TestStatusLineageLines(t *testing.T) {
	e, _ := workspaceLineageEnv(t)
	if _, _, err := e.syncOut(t, syncOptions{}); err != nil {
		t.Fatal(err)
	}
	snap, text, js := statusOf(t, e)
	var found bool
	for _, w := range claudeStatus(t, snap).Workspaces {
		if reflect.DeepEqual(w.Lineage["core-kb"], []string{"client-kb"}) {
			found = true
		}
	}
	if !found {
		t.Fatalf("no workspace carries core-kb from client-kb: %+v", claudeStatus(t, snap).Workspaces)
	}
	if !strings.Contains(text, "lineage: core-kb (from client-kb)") {
		t.Errorf("text lacks the lineage line:\n%s", text)
	}
	if !strings.Contains(js, `"lineage":{"core-kb":["client-kb"]}`) {
		t.Errorf("json lacks the lineage:\n%s", js)
	}
}

// AC-lineage-28: an unresolved relation is reported with its reason sentence by
// status and doctor, and nothing tells an unreadable target from an unmounted one.
func TestStatusDoctorUnresolvedReason(t *testing.T) {
	e, _ := workspaceLineageEnv(t)
	rel := func(name string) *contract.Relations {
		r := closureOf("core-kb")
		r.Unresolved = []contract.RelationUnresolved{{Name: name, Reason: "not_mounted"}}
		return r
	}
	e.srv.set(func(s *lineageServer) { s.relations["client-kb"] = rel("absent-kb") })
	if _, _, err := e.syncOut(t, syncOptions{}); err != nil {
		t.Fatal(err)
	}
	_, textAbsent, _ := statusOf(t, e)
	const want = "relation client-kb -> absent-kb not followed: no mounted KB readable with this token matches"
	if !strings.Contains(textAbsent, want) {
		t.Errorf("status lacks %q:\n%s", want, textAbsent)
	}
	cfg, err := clientconfig.Load(e.dir)
	if err != nil {
		t.Fatal(err)
	}
	fs := checkLineage(e.dir, cfg)
	if len(fs) != 1 || fs[0].Check != "lineage" || !strings.Contains(fs[0].Message, want) || !strings.Contains(fs[0].Fix, "mount it on the server") {
		t.Fatalf("doctor findings = %+v", fs)
	}
	if strings.Contains(strings.ToLower(fs[0].Message+fs[0].Fix), "exist") {
		t.Errorf("a finding says the KB exists: %+v", fs[0])
	}
	// An unreadable target is sent as the same reason: the output is the same.
	e.srv.set(func(s *lineageServer) { s.relations["client-kb"] = rel("hidden-kb") })
	if _, _, err := e.syncOut(t, syncOptions{}); err != nil {
		t.Fatal(err)
	}
	_, textHidden, _ := statusOf(t, e)
	if strings.ReplaceAll(textHidden, "hidden-kb", "absent-kb") != textAbsent {
		t.Errorf("an unreadable target reads differently from an unmounted one:\n%s\n--\n%s", textHidden, textAbsent)
	}
	if got := lineageReasonText("x\x1b[31m"); strings.ContainsRune(got, 0x1b) {
		t.Errorf("an unknown reason is printed raw: %q", got)
	}
}

// AC-lineage-29: a relation that appeared on the server after the last sync is
// drift naming the KB and the fix; a server without `relations` never is.
func TestStatusLineageDrift(t *testing.T) {
	e, _ := workspaceLineageEnv(t)
	e.srv.set(func(s *lineageServer) {
		s.mounted = append(s.mounted, "extra-kb")
		s.relations["extra-kb"] = closureOf()
	})
	if _, _, err := e.syncOut(t, syncOptions{}); err != nil {
		t.Fatal(err)
	}
	if p := claudeStatus(t, firstOf(statusOf(t, e))); p.State != "in_sync" {
		t.Fatalf("after sync: state %q, reasons %v", p.State, p.DriftReason)
	}
	e.srv.set(func(s *lineageServer) { s.relations["client-kb"] = closureOf("core-kb", "extra-kb") })
	snap, text, _ := statusOf(t, e)
	p := claudeStatus(t, snap)
	if p.State != "drift" || !strings.Contains(strings.Join(p.DriftReason, "\n"), "lineage changed (extra-kb would be bound in ") || !strings.Contains(text, "run `cartographer sync`") {
		t.Errorf("no drift for a relation that appeared: state %q reasons %v", p.State, p.DriftReason)
	}
	e.srv.set(func(s *lineageServer) { s.relations["client-kb"] = closureOf() })
	snap, _, _ = statusOf(t, e)
	if r := strings.Join(claudeStatus(t, snap).DriftReason, "\n"); !strings.Contains(r, "core-kb would be unbound in ") {
		t.Errorf("no drift for a relation that went: %q", r)
	}
	e.srv.set(func(s *lineageServer) { s.relations["client-kb"] = nil })
	if p := claudeStatus(t, firstOf(statusOf(t, e))); p.State != "in_sync" {
		t.Errorf("a server that sends no relations is drift: %v", p.DriftReason)
	}
	e.srv.set(func(s *lineageServer) { s.relations["client-kb"] = closureOf("core-kb", "extra-kb") })
	if _, _, err := e.syncOut(t, syncOptions{}); err != nil {
		t.Fatal(err)
	}
	if p := claudeStatus(t, firstOf(statusOf(t, e))); p.State != "in_sync" {
		t.Errorf("after the sync that applies it: state %q, reasons %v", p.State, p.DriftReason)
	}
}

func firstOf(s statusSnapshot, _, _ string) statusSnapshot { return s }

// AC-lineage-30: after a sync that derived a binding and bound a lineage KB,
// status lists both under `last change`, and a later sync that changes nothing
// leaves the record as it was.
func TestStatusLastChange(t *testing.T) {
	e, repo := workspaceLineageEnv(t)
	if _, _, err := e.syncOut(t, syncOptions{}); err != nil {
		t.Fatal(err)
	}
	snap, text, js := statusOf(t, e)
	if snap.LastChange == nil {
		t.Fatal("no last change recorded")
	}
	for _, w := range []string{"last change ", "+ client-kb in " + repo + " (paths.yaml of client-kb)", "+ core-kb in " + repo + " (lineage of client-kb)"} {
		if !strings.Contains(text, w) {
			t.Errorf("text lacks %q:\n%s", w, text)
		}
	}
	var decoded struct {
		LastChange *clientconfig.DerivedChange `json:"last_change"`
	}
	if err := json.Unmarshal([]byte(js), &decoded); err != nil || decoded.LastChange == nil || len(decoded.LastChange.Added) < 2 {
		t.Errorf("json last_change = %+v (%v)", decoded.LastChange, err)
	}
	before, _ := os.ReadFile(derivedFile(t))
	if _, _, err := e.syncOut(t, syncOptions{}); err != nil {
		t.Fatal(err)
	}
	if after, _ := os.ReadFile(derivedFile(t)); string(after) != string(before) {
		t.Errorf("a sync that changed nothing rewrote the record:\n%s\n--\n%s", before, after)
	}
}

func derivedFile(t *testing.T) string {
	t.Helper()
	p, err := clientconfig.DerivedPath()
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// A status snapshot pulls each KB once: the drift check reads the `relations`
// of the pulls that built the manifests, it makes none of its own.
func TestStatusPullsEachKBOnce(t *testing.T) {
	e, _ := workspaceLineageEnv(t)
	if _, _, err := e.syncOut(t, syncOptions{}); err != nil {
		t.Fatal(err)
	}
	e.srv.set(func(s *lineageServer) { s.pulls = map[string]int{} })
	statusOf(t, e)
	e.srv.set(func(s *lineageServer) {
		if len(s.pulls) == 0 {
			t.Error("status pulled nothing")
		}
		for kb, n := range s.pulls {
			if n != 1 {
				t.Errorf("status pulled %q %d times, want once", kb, n)
			}
		}
	})
}

// The drift text names the fix once, in the status line, not in the reason.
func TestStatusLineageDriftNamesFixOnce(t *testing.T) {
	e, _ := workspaceLineageEnv(t)
	e.srv.set(func(s *lineageServer) {
		s.mounted = append(s.mounted, "extra-kb")
		s.relations["extra-kb"] = closureOf()
	})
	if _, _, err := e.syncOut(t, syncOptions{}); err != nil {
		t.Fatal(err)
	}
	e.srv.set(func(s *lineageServer) { s.relations["client-kb"] = closureOf("core-kb", "extra-kb") })
	snap, text, _ := statusOf(t, e)
	if r := strings.Join(claudeStatus(t, snap).DriftReason, "\n"); strings.Contains(r, "cartographer sync") {
		t.Errorf("the reason names the fix: %q", r)
	}
	for _, l := range strings.Split(text, "\n") {
		if n := strings.Count(l, "cartographer sync"); n > 1 {
			t.Errorf("the fix is named %d times in one line, want once: %s", n, l)
		}
	}
}

// One seed without an answer is unknown; the drift of the others is still
// reported. A derived binding (paths.yaml) is compared like a hand-made one.
func TestLineageDriftPerSeedAndDerivedBinding(t *testing.T) {
	e, repo := workspaceLineageEnv(t)
	e.srv.set(func(s *lineageServer) {
		s.mounted = append(s.mounted, "extra-kb", "other-kb")
		s.relations["extra-kb"] = closureOf()
	})
	if _, _, err := e.syncOut(t, syncOptions{}); err != nil {
		t.Fatal(err)
	}
	cfg, err := clientconfig.Load(e.dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Derived().Bindings["claude"]) == 0 {
		t.Fatal("no derived binding after sync")
	}
	// Only client-kb is in rels: the seeds not pulled are unknown, the others are checked.
	cfg.KnownKBs = append(cfg.KnownKBs, "extra-kb")
	rels := map[string]contract.Relations{"client-kb": *closureOf("core-kb", "extra-kb")}
	got := strings.Join(lineageDrift(cfg, "claude", rels), "\n")
	canon, _ := clientconfig.CanonicalWorkspacePath(repo)
	if !strings.Contains(got, "extra-kb would be bound in "+canon) {
		t.Errorf("no drift on the derived binding of %s:\n%s", canon, got)
	}
}
