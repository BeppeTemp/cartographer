package mcpserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/auth"
)

func TestPolicyWriteClassesDenyOutsideWholeOrBoundary(t *testing.T) {
	k := setupTestKB(t)
	k.AuthName = "docs"
	policy := auth.Policy{Permissions: []auth.Permission{{KB: "docs", Write: true, Maps: []string{"manutenzione"}, Types: []string{"Runbook"}}}}
	cases := map[string]string{
		"map_create":       `{"name":"other"}`,
		"map_delete":       `{"name":"manutenzione"}`,
		"log_append":       `{"entry":"leak"}`,
		"snapshot":         `{}`,
		"artifact_write":   `{"path":"instructions.md","content":"x"}`,
		"secret_set":       `{"path":"secrets/x.sops.yaml","key":"x","value":"y"}`,
		"conflict_resolve": `{"contradiction_id":"manutenzione/test-runbook","resolution":"x"}`,
		"sync_apply":       `{}`,
		"concept_move":     `{"source_id":"manutenzione/test-runbook","target_id":"other/moved"}`,
		"supersede":        `{"source_id":"manutenzione/test-runbook","target_id":"other/replacement"}`,
		"concept_write":    `{"id":"manutenzione/new","frontmatter":{"type":"Secret"},"body":"x"}`,
		"concept_patch":    `{"id":"manutenzione/test-runbook","frontmatter":{"type":"Secret"},"if_match":"x","old_string":"x","new_string":"y"}`,
		"index_patch":      `{"path":"other","if_match":"x","old_string":"x","new_string":"y"}`,
		"concept_batch":    `{"operations":[{"op":"write","id":"other/new","frontmatter":{"type":"Runbook"},"body":"x"}]}`,
	}
	for tool, args := range cases {
		if err := authorizeTool(policy, k, "docs", tool, json.RawMessage(args)); err == nil {
			t.Errorf("%s unexpectedly authorized", tool)
		}
	}
}

func TestPolicyResolvesActualWriteShapes(t *testing.T) {
	k := setupTestKB(t)
	k.AuthName = "docs"
	policy := auth.Policy{Permissions: []auth.Permission{{KB: "docs", Write: true, Maps: []string{"manutenzione"}, Types: []string{"Runbook"}}}}
	if err := os.MkdirAll(filepath.Join(k.Root, "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(k.Root, "templates", "runbook.md"), []byte("---\ntype: Runbook\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	allowed := map[string]string{
		"concept_new":   `{"id":"manutenzione/new","template":"runbook"}`,
		"concept_patch": `{"id":"manutenzione/test-runbook","if_match":"x","old_string":"x","new_string":"y","frontmatter":{"type":"Runbook"}}`,
		"concept_move":  `{"moves":[{"source_id":"manutenzione/test-runbook","target_id":"manutenzione/moved"}],"rewrite_links":false}`,
	}
	for tool, args := range allowed {
		if err := authorizeTool(policy, k, "docs", tool, json.RawMessage(args)); err != nil {
			t.Errorf("%s denied valid JSON shape: %v", tool, err)
		}
	}
	for tool, args := range map[string]string{
		"concept_new":   `{"id":"manutenzione/new","template":"missing"}`,
		"concept_patch": `{"id":"manutenzione/test-runbook","if_match":"x","old_string":"x","new_string":"y","frontmatter":{"type":"Secret"}}`,
		"concept_move":  `{"source_id":"manutenzione/test-runbook","target_id":"manutenzione/moved"}`,
	} {
		if err := authorizeTool(policy, k, "docs", tool, json.RawMessage(args)); err == nil {
			t.Errorf("%s permitted unauthorized actual JSON shape", tool)
		}
	}
	if err := authorizeTool(policy, k, "docs", "concept_move", json.RawMessage(`{"moves":[{"source_id":"manutenzione/test-runbook","target_id":"manutenzione/moved"},{"source_id":"manutenzione/test-runbook","target_id":"other/moved"}],"rewrite_links":false}`)); err == nil {
		t.Error("concept_move batch permitted a cross-boundary destination")
	}
}

func TestAuthorizationUsesCurrentFrontmatterAfterChange(t *testing.T) {
	k := setupTestKB(t)
	k.AuthName = "docs"
	policy := auth.Policy{Permissions: []auth.Permission{{KB: "docs", Write: true, Maps: []string{"manutenzione"}, Types: []string{"Runbook"}}}}
	args := json.RawMessage(`{"id":"manutenzione/test-runbook","if_match":"x","old_string":"x","new_string":"y"}`)
	if err := authorizeTool(policy, k, "docs", "concept_patch", args); err != nil {
		t.Fatalf("initial type denied: %v", err)
	}
	path := filepath.Join(k.DataRoot(), "manutenzione", "test-runbook.md")
	if err := os.WriteFile(path, []byte("---\ntype: Secret\ntitle: changed\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := authorizeTool(policy, k, "docs", "concept_patch", args); err == nil {
		t.Fatal("authorization used stale frontmatter classification")
	}
}

// TestPolicyIndexPatch_ScopedToMapNotWholeKB verifies index_patch's bounded
// resource authorization (D122 WP3): a policy scoped to one Map can patch
// that Map's curated index but not another Map's, and — since the root index
// is not scoped to any single Map/Journal — not the root index either, even
// though the same policy already grants a write inside that Map.
func TestPolicyIndexPatch_ScopedToMapNotWholeKB(t *testing.T) {
	k := setupTestKB(t)
	k.AuthName = "docs"
	policy := auth.Policy{Permissions: []auth.Permission{{KB: "docs", Write: true, Maps: []string{"manutenzione"}}}}

	if err := authorizeTool(policy, k, "docs", "index_patch", json.RawMessage(`{"path":"manutenzione","if_match":"x","old_string":"x","new_string":"y"}`)); err != nil {
		t.Errorf("in-scope map index_patch denied: %v", err)
	}
	if err := authorizeTool(policy, k, "docs", "index_patch", json.RawMessage(`{"path":"other","if_match":"x","old_string":"x","new_string":"y"}`)); err == nil {
		t.Error("out-of-scope map index_patch was authorized")
	}
	for _, rootArgs := range []string{
		`{"if_match":"x","old_string":"x","new_string":"y"}`,            // 'path' omitted
		`{"path":".","if_match":"x","old_string":"x","new_string":"y"}`, // explicit "."
	} {
		if err := authorizeTool(policy, k, "docs", "index_patch", json.RawMessage(rootArgs)); err == nil {
			t.Errorf("root index_patch (%s) authorized under a Map-scoped (not whole-KB) policy", rootArgs)
		}
	}

	wholePolicy := auth.Policy{Permissions: []auth.Permission{{KB: "docs", Write: true}}}
	if err := authorizeTool(wholePolicy, k, "docs", "index_patch", json.RawMessage(`{"if_match":"x","old_string":"x","new_string":"y"}`)); err != nil {
		t.Errorf("root index_patch denied under a whole-KB policy: %v", err)
	}
}

// TestPolicyConceptBatch_MixedAuthorizationDeniesWholeBatch verifies
// concept_batch's authorization (D125 WP3): every id in "operations" must
// individually pass allowedID, and one denied id — even the last one —
// rejects the whole batch, mirroring concept_move's non-disclosure
// guarantee for its own multi-resource "moves" batch.
func TestPolicyConceptBatch_MixedAuthorizationDeniesWholeBatch(t *testing.T) {
	k := setupTestKB(t)
	k.AuthName = "docs"
	policy := auth.Policy{Permissions: []auth.Permission{{KB: "docs", Write: true, Maps: []string{"manutenzione"}, Types: []string{"Runbook"}}}}

	inScope := `{"operations":[{"op":"write","id":"manutenzione/a","frontmatter":{"type":"Runbook"},"body":"x"}]}`
	if err := authorizeTool(policy, k, "docs", "concept_batch", json.RawMessage(inScope)); err != nil {
		t.Errorf("fully in-scope batch denied: %v", err)
	}

	mixed := `{"operations":[` +
		`{"op":"write","id":"manutenzione/a","frontmatter":{"type":"Runbook"},"body":"x"},` +
		`{"op":"write","id":"other/b","frontmatter":{"type":"Runbook"},"body":"x"}` +
		`]}`
	if err := authorizeTool(policy, k, "docs", "concept_batch", json.RawMessage(mixed)); err == nil {
		t.Error("batch with one out-of-scope id was authorized")
	}

	if err := authorizeTool(policy, k, "docs", "concept_batch", json.RawMessage(`{"operations":[]}`)); err == nil {
		t.Error("empty operations batch was authorized")
	}
}

func TestPolicyAllowsBoundedWriteAndRejectsTypeChange(t *testing.T) {
	k := setupTestKB(t)
	k.AuthName = "docs"
	policy := auth.Policy{Permissions: []auth.Permission{{KB: "docs", Write: true, Maps: []string{"manutenzione"}, Types: []string{"Runbook"}}}}
	if err := authorizeTool(policy, k, "docs", "concept_write", json.RawMessage(`{"id":"manutenzione/new","frontmatter":{"type":"Runbook"},"body":"x"}`)); err != nil {
		t.Fatalf("bounded create denied: %v", err)
	}
	if err := authorizeTool(policy, k, "docs", "concept_write", json.RawMessage(`{"id":"manutenzione/test-runbook","frontmatter":{"type":"Secret"},"body":"x"}`)); err == nil {
		t.Fatal("type change authorized")
	}
}
