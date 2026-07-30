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
