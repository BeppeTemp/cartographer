package mcpserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/kb"
)

// /health carries a KB's push state to the humans' surfaces once the push
// failed, and only for the KBs the caller reads.
func TestHealth_CarriesPushStateScopedToTheCaller(t *testing.T) {
	t.Parallel()
	ka, bare := setupGitKBWithRemote(t)
	kw := setupTestKB(t)
	ka.SetDefaultIdentity("Ada Example", "ada@example.test")
	if err := ka.SyncOut(); err != nil {
		t.Fatalf("seed SyncOut: %v", err)
	}
	// The remote now refuses every push.
	hook := "#!/bin/sh\necho 'remote: push declined by policy' >&2\nexit 1\n"
	if err := os.MkdirAll(filepath.Join(bare, "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bare, "hooks", "pre-receive"), []byte(hook), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := ka.WriteFileAtomic("data/a.md", []byte("a\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := ka.CommitOpAs("write a", "", ""); err != nil {
		t.Fatal(err)
	}

	multi := NewMultiKBServer("test")
	for name, k := range map[string]*kb.KB{"kb-a": ka, "work-kb": kw} {
		k := k
		multi.MountKB(name, func(s *Server) { RegisterKBTools(s, k, Deps{}) })
	}
	store := auth.NewScopedTokenStore([]auth.ScopedToken{
		{Token: tokAdmin, Policy: auth.Policy{Admin: true}},
		{Token: tokRO, Scopes: []auth.KBScope{{KB: "kb-a"}}},
		{Token: tokNone, Scopes: []auth.KBScope{{KB: "work-kb"}}},
		{Token: tokMapRule, Policy: auth.Policy{Permissions: []auth.Permission{{KB: "kb-a", Maps: []string{"manutenzione"}}}}},
	})
	h := store.Middleware(multi.Handler())
	multi.pushHealth.ttl = time.Nanosecond // this test watches the state change

	pushOf := func(body map[string]any, name string) map[string]any {
		for _, e := range body["kbs"].([]any) {
			m := e.(map[string]any)
			if m["name"] == name {
				p, _ := m["push"].(map[string]any)
				return p
			}
		}
		t.Fatalf("kb %q not in /health: %v", name, body)
		return nil
	}

	// Committed and not yet pushed, young: quiet.
	if _, body := healthAs(t, h, tokAdmin); pushOf(body, "kb-a") != nil {
		t.Errorf("a fresh unpushed commit is reported: %v", pushOf(body, "kb-a"))
	}

	if err := ka.SyncOut(); err == nil {
		t.Fatal("the refusing remote accepted the push")
	}
	_, admin := healthAs(t, h, tokAdmin)
	p := pushOf(admin, "kb-a")
	if p == nil || p["push_state"] != "failed" || p["unpushed_commits"] != float64(1) || p["remedy"] == "" {
		t.Fatalf("kb-a push = %v, want failed, 1 unpushed, a remedy", p)
	}
	if pushOf(admin, "work-kb") != nil {
		t.Errorf("a KB without a remote reports a push state: %v", pushOf(admin, "work-kb"))
	}

	// A principal that reads work-kb only learns nothing about kb-a.
	raw, other := healthAs(t, h, tokNone)
	if pushOf(other, "work-kb") != nil {
		t.Error("work-kb has no push state to show")
	}
	if got := healthKBNames(t, other); len(got) != 1 || got[0] != "work-kb" {
		t.Errorf("kbs = %v, want [work-kb]", got)
	}
	if containsAny(raw, "kb-a", "push_state", "declined") {
		t.Errorf("/health leaks another KB's push state: %s", raw)
	}
	// An anonymous probe sees no KB at all.
	if raw, _ := healthAs(t, h, ""); containsAny(raw, "push_state", "kb-a") {
		t.Errorf("anonymous /health carries push state: %s", raw)
	}
	// A token narrowed to a map reads kb-a on /health but not its replication
	// (sync_status needs the whole KB).
	rawSel, sel := healthAs(t, h, tokMapRule)
	if got := healthKBNames(t, sel); len(got) != 1 || got[0] != "kb-a" {
		t.Fatalf("selector token kbs = %v, want [kb-a]", got)
	}
	if pushOf(sel, "kb-a") != nil || strings.Contains(rawSel, "push") {
		t.Errorf("a selector-restricted token sees the push state: %s", rawSel)
	}
	_, ro := healthAs(t, h, tokRO)
	if pushOf(ro, "kb-a") == nil {
		t.Error("a token that reads kb-a does not get its push state")
	}
}

func containsAny(s string, subs ...string) bool {
	for _, x := range subs {
		if strings.Contains(s, x) {
			return true
		}
	}
	return false
}

// Polling /health must not multiply git calls: a reading is reused for the TTL.
func TestPushHealthCache_ReusesAReadingWithinTheTTL(t *testing.T) {
	t.Parallel()
	k := setupTestKB(t)
	var c pushHealthCache
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	c.get("kb-a", k, now)
	c.get("kb-a", k, now.Add(pushHealthTTL-time.Second))
	if c.reads != 1 {
		t.Errorf("reads within the TTL = %d, want 1", c.reads)
	}
	c.get("kb-b", k, now)
	if c.reads != 2 {
		t.Errorf("a second KB shares the first one's reading: reads = %d, want 2", c.reads)
	}
	c.get("kb-a", k, now.Add(pushHealthTTL))
	if c.reads != 3 {
		t.Errorf("reads after the TTL = %d, want 3", c.reads)
	}
}
