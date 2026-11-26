package main

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// transversalEnv is the workspace-scope lineage environment plus kb-a, a KB
// bound to no folder that the person declared transversal.
func transversalEnv(t *testing.T) *lineageEnv {
	t.Helper()
	e, _ := workspaceLineageEnv(t)
	e.srv.set(func(s *lineageServer) {
		s.mounted = append(s.mounted, "kb-a")
		s.instructions, s.agents = true, true
		s.relations["kb-a"] = closureOf("core-kb") // read by nothing: a transversal KB is no seed
	})
	e.cfg.KnownKBs = e.srv.mounted
	e.cfg.SetKBTransversal("kb-a", true)
	if err := clientconfig.Save(e.dir, e.cfg); err != nil {
		t.Fatal(err)
	}
	return e
}

// globalManaged is the managed entries of the claude global projection.
func (e *lineageEnv) globalManaged(t *testing.T) []provisioning.ManagedFile {
	t.Helper()
	lf, err := provisioning.ReadLockFile(lockFilePath(e.dir))
	if err != nil {
		t.Fatal(err)
	}
	return lf.Providers["claude"].Managed
}

// AC-transv-10: a transversal KB bound to nowhere is pulled in the same round
// as every other KB: once per sync, the manifest build reusing the prefetch.
func TestTransversalKBPulledOnce(t *testing.T) {
	e := transversalEnv(t)
	for i := 0; i < 2; i++ {
		e.srv.set(func(s *lineageServer) { s.pulls = map[string]int{} })
		if _, _, err := e.syncOut(t, syncOptions{}); err != nil {
			t.Fatal(err)
		}
		e.srv.set(func(s *lineageServer) {
			for _, kb := range []string{"kb-a", "client-kb", "core-kb"} {
				if s.pulls[kb] != 1 {
					t.Errorf("sync %d pulled %s %d times, want once", i+1, kb, s.pulls[kb])
				}
			}
		})
	}
}

// AC-transv-11: the bundle-only projection holds the transversal KB's
// instructions and no other artifact of it; a server that does not identify
// its KBs adds nothing.
func TestBundleOnlyCarriesInstructionsOnly(t *testing.T) {
	e := transversalEnv(t)
	if _, _, err := e.syncOut(t, syncOptions{}); err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, mf := range e.globalManaged(t) {
		if mf.Source == "kb:kb-a" {
			kinds = append(kinds, mf.Kind)
		}
	}
	if len(kinds) != 1 || kinds[0] != "instructions" {
		t.Fatalf("kb-a entries of the global projection = %v, want its instructions only", kinds)
	}
	if !e.skillMissing("kb-a") {
		t.Error("a transversal KB's skill must not be installed")
	}
	if _, err := os.Stat(e.dir + "/.claude/agents/agent-kb-a.md"); err == nil {
		t.Error("a transversal KB's agent must not be installed")
	}
	if !strings.Contains(e.instructions(t), "section of kb-a") {
		t.Error("the instructions block does not carry kb-a's region")
	}

	// A server that names no KB: nothing of any KB is added.
	cs := candidateSet{HasBare: true}
	if got := cs.forProjection(e.cfg, syncProjection{Provider: "claude", BundleOnly: true, KBs: []string{"kb-a"}}); len(got) != 0 {
		t.Errorf("bare server: bundle-only projection holds %v", got)
	}
}

func (e *lineageEnv) skillMissing(kb string) bool { return !e.skill(kb) }

// AC-transv-12: a transversal KB's relations are not stored as lineage, and it
// is no seed of anything.
func TestTransversalKBNeverASeed(t *testing.T) {
	e := transversalEnv(t)
	if _, _, err := e.syncOut(t, syncOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.lineage()["kb-a"]; ok {
		t.Errorf("a transversal KB's relations were stored: %+v", e.lineage())
	}
	cfg, err := clientconfig.Load(e.dir)
	if err != nil {
		t.Fatal(err)
	}
	if kbs, _ := cfg.BoundOwnKBs("claude"); strings.Join(kbs, ",") != "client-kb" {
		t.Errorf("the flag changed the provider's binding: %v", kbs)
	}
	if seeds := lineageSeedsOf(cfg, nil); seeds["kb-a"] {
		t.Error("kb-a is a lineage seed")
	}
	if _, candidates := derivationScope(cfg, []string{"claude"}); !strings.Contains(strings.Join(candidates, ","), "kb-a") {
		t.Errorf("kb-a must be a pull candidate: %v", candidates)
	}
}

// AC-transv-21: a transversal KB in skipped_kbs is held, not pulled: the
// provider's bundle and plugin updates go on, its region and lock entry stay
// byte for byte, and status reports no drift.
func TestTransversalKBInSkippedKBsIsHeld(t *testing.T) {
	e := transversalEnv(t)
	setHome(t, e.dir) // `status` reads the client directory from HOME
	if _, _, err := e.syncOut(t, syncOptions{}); err != nil {
		t.Fatal(err)
	}
	region := e.instructions(t)
	if !strings.Contains(region, "section of kb-a") {
		t.Fatal("setup: no region of kb-a")
	}
	entries := entriesOf(e.globalManaged(t), "kb:kb-a")
	e.srv.set(func(s *lineageServer) {
		s.mounted = []string{"client-kb", "core-kb"}
		s.skipped = []string{"kb-a"}
		s.pulls = map[string]int{}
	})
	out, _, err := e.syncOut(t, syncOptions{})
	if err != nil {
		t.Fatalf("a held transversal KB must not fail the sync: %v", err)
	}
	e.srv.set(func(s *lineageServer) {
		if s.pulls["kb-a"] != 0 {
			t.Errorf("a held KB was pulled %d times", s.pulls["kb-a"])
		}
	})
	if !strings.Contains(out, "held kb-a") {
		t.Errorf("no `held kb-a` line:\n%s", out)
	}
	if got := e.instructions(t); !strings.Contains(got, "section of kb-a") || got != region {
		t.Errorf("the held region changed:\n%s", got)
	}
	if got := entriesOf(e.globalManaged(t), "kb:kb-a"); got != entries || got == "" {
		t.Errorf("held lock entries = %q, want %q", got, entries)
	}
	// status judges the projection as sync applied it: no drift while held.
	statusState := func() string {
		out := withStdout(t, func() { cmdStatus([]string{"--output", "json"}) })
		var st statusSnapshot
		if err := json.Unmarshal([]byte(out), &st); err != nil {
			t.Fatalf("status json: %v\n%s", err, out)
		}
		return st.State
	}
	if st := statusState(); st != "in_sync" {
		t.Errorf("a held transversal KB reads as drift: %s", st)
	}
	// Not listed at all: the region and the entry leave.
	e.srv.set(func(s *lineageServer) { s.skipped = nil })
	if _, _, err := e.syncOut(t, syncOptions{}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(e.instructions(t), "section of kb-a") || entriesOf(e.globalManaged(t), "kb:kb-a") != "" {
		t.Error("a KB in neither list must leave")
	}
}

func entriesOf(managed []provisioning.ManagedFile, source string) string {
	var sb strings.Builder
	for _, mf := range managed {
		if mf.Source == source {
			sb.WriteString(mf.Kind + ":" + mf.Name + ":" + mf.Path + ":" + mf.ContentHash + "\n")
		}
	}
	return sb.String()
}

// Config.HeldKBs is runtime state both sync and status set: the two must leave
// the same transversal KBs in the pull set and the projection. sync sets
// KnownKBs to the live list; status filters the persisted one to it.
func TestTransversalKBsAgreeBetweenSyncAndStatusWhenHeld(t *testing.T) {
	newCfg := func() *clientconfig.Config {
		cfg := &clientconfig.Config{KnownKBs: []string{"kb-a", "kb-b", "kb-c"}}
		if err := cfg.SetWorkspaceScope("claude", clientconfig.ScopeWorkspace); err != nil {
			t.Fatal(err)
		}
		cfg.SetKBTransversal("kb-a", true)
		cfg.SetKBTransversal("kb-b", true)
		return cfg
	}
	live := []string{"kb-b", "kb-c"}
	skipped := []client.SkippedKB{{Name: "kb-a", Reason: "clone failed"}}
	previous := []string{"kb-a", "kb-b", "kb-c"}

	// sync: the live list is KnownKBs, HeldKBs the names heldKBs returns.
	syncCfg := newCfg()
	syncCfg.HeldKBs = heldNames(heldKBs(previous, live, skipped, declaredTransversal(syncCfg, []string{"claude"})))
	syncCfg.KnownKBs = live
	// status: the persisted list filtered to the live one.
	statusCfg := newCfg()
	statusCfg.HeldKBs = heldNames(heldKBs(statusCfg.KnownKBs, live, skipped, declaredTransversal(statusCfg, []string{"claude"})))
	statusCfg.KnownKBs = slices.DeleteFunc(slices.Clone(statusCfg.KnownKBs), func(kb string) bool { return !slices.Contains(live, kb) })

	got1, got2 := syncCfg.TransversalKBs("claude"), statusCfg.TransversalKBs("claude")
	if !slices.Equal(got1, got2) || !slices.Equal(got1, []string{"kb-b"}) {
		t.Errorf("sync %v, status %v, want [kb-b] for both", got1, got2)
	}
	if h1, h2 := syncCfg.HeldTransversalKBs("claude"), statusCfg.HeldTransversalKBs("claude"); !slices.Equal(h1, h2) || !slices.Equal(h1, []string{"kb-a"}) {
		t.Errorf("held: sync %v, status %v, want [kb-a] for both", h1, h2)
	}
}
