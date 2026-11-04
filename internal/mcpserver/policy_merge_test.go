package mcpserver

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/auth"
)

// concept_merge redirects links across the whole KB, so it needs the
// whole-KB write grant; a role bounded to maps, journals or types is refused
// even when it may write both the satellite and its parent.
func TestPolicyConceptMergeNeedsWholeKBWrite(t *testing.T) {
	t.Parallel()
	k := setupTestKB(t)
	k.AuthName = "docs"
	args := json.RawMessage(`{"satellite_id":"m/c/child","if_match":"x"}`)

	for name, policy := range map[string]auth.Policy{
		"map":  {Permissions: []auth.Permission{{KB: "docs", Write: true, Maps: []string{"m"}}}},
		"type": {Permissions: []auth.Permission{{KB: "docs", Write: true, Types: []string{"Note"}}}},
	} {
		err := authorizeTool(policy, k, "docs", "concept_merge", args)
		if err == nil || !strings.Contains(err.Error(), denyNeedsWholeKB) {
			t.Errorf("%s-scoped merge = %v, want %s", name, err, denyNeedsWholeKB)
		}
	}
	if err := authorizeTool(auth.Policy{Permissions: []auth.Permission{{KB: "docs", Write: true}}}, k, "docs", "concept_merge", args); err != nil {
		t.Errorf("whole-KB writer refused: %v", err)
	}
	// A token with no grant on the KB learns nothing about it.
	err := authorizeTool(auth.Policy{Permissions: []auth.Permission{{KB: "other", Write: true}}}, k, "docs", "concept_merge", args)
	if err == nil || !strings.Contains(err.Error(), genericNotFound) {
		t.Errorf("foreign token = %v, want %s", err, genericNotFound)
	}
}

// A scoped caller is refused before anything is rewritten, and a whole-KB
// caller still gets every rewritten page; the response filter is the second
// line behind the grant.
func TestConceptMergeResponseListsOnlyVisibleRewrites(t *testing.T) {
	setup := func(t *testing.T) (*Server, string) {
		k, s := d160Fixture(t)
		k.AuthName = "docs"
		d160Write(t, k, "m/_map.md", "---\ntype: Map\nkind: map\ntitle: M\n---\n# M\n")
		d160Write(t, k, "m/index.md", "---\ntype: Index\ntitle: M\n---\n- [c](c.md)\n")
		d160Write(t, k, "m/c/index.md", "---\ntype: Note\ntitle: C\n---\n# C\n")
		d160Write(t, k, "m/c/child.md", "---\ntype: Note\ntitle: Child\n---\nBody.\n")
		d160Write(t, k, "m/near.md", "---\ntype: Note\ntitle: Near\n---\nSee [[m/c/child]].\n")
		d160Write(t, k, "hid/_map.md", "---\ntype: Map\nkind: map\ntitle: Hid\n---\n# Hid\n")
		d160Write(t, k, "hid/far.md", "---\ntype: Note\ntitle: Far\n---\nSee [[m/c/child]].\n")
		child, err := k.ReadConcept("m/c/child")
		if err != nil {
			t.Fatal(err)
		}
		return s, child.ContentHash
	}
	call := func(t *testing.T, ctx requestContext, s *Server, hash string) string {
		args, _ := json.Marshal(map[string]string{"satellite_id": "m/c/child", "if_match": hash})
		res, err := s.Tools()["concept_merge"].Handler(ctx, args)
		if err != nil || res.IsError {
			t.Fatalf("concept_merge: %+v err=%v", res, err)
		}
		return res.Content[0].Text
	}

	t.Run("scoped", func(t *testing.T) {
		t.Parallel()
		s, hash := setup(t)
		ctx := restrictedContext(auth.Policy{Permissions: []auth.Permission{{KB: "docs", Write: true, Maps: []string{"m"}}}})
		args, _ := json.Marshal(map[string]string{"satellite_id": "m/c/child", "if_match": hash})
		res, _ := s.Tools()["concept_merge"].Handler(ctx, args)
		if !res.IsError || !strings.Contains(res.Content[0].Text, denyNeedsWholeKB) || strings.Contains(res.Content[0].Text, "hid/far") {
			t.Errorf("scoped merge = %+v, want a needs_whole_kb refusal", res)
		}
	})
	t.Run("whole", func(t *testing.T) {
		t.Parallel()
		s, hash := setup(t)
		out := call(t, authLocalContext(), s, hash)
		if !strings.Contains(out, "hid/far") || !strings.Contains(out, "m/near") {
			t.Errorf("rewritten = %s, want both", out)
		}
	})
}

func TestConceptMergeResponseFilterHidesInvisibleIDs(t *testing.T) {
	t.Parallel()
	k := setupTestKB(t)
	k.AuthName = "docs"
	ctx := restrictedContext(auth.Policy{Permissions: []auth.Permission{{KB: "docs", Write: true, Maps: []string{"m"}}}})
	got := newCallerView(ctx, k).filterRewritten([]rewrittenConcept{{ID: "m/near"}, {ID: "hid/far"}})
	if len(got) != 1 || got[0].ID != "m/near" {
		t.Errorf("filtered = %+v, want only m/near", got)
	}
}
