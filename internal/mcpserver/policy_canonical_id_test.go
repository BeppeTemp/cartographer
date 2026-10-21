package mcpserver

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/auth"
)

// A non-canonical concept id is refused before authorization, in every tool
// argument that names a concept, and the file it would have resolved to is
// left untouched.
func TestPolicyRefusesNonCanonicalIDs(t *testing.T) {
	t.Parallel()
	k := setupTestKB(t)
	k.AuthName = "docs"
	policy := auth.Policy{Permissions: []auth.Permission{{KB: "docs", Write: true, Maps: []string{"manutenzione"}}}}

	other := filepath.Join(k.DataRoot(), "other")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(other, "x.md")
	const original = "---\ntype: Runbook\n---\nbody\n"
	if err := os.WriteFile(victim, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, id := range []string{"manutenzione/../other/x", "manutenzione/../x", "./manutenzione/x", "manutenzione//x", "/manutenzione/x", `manutenzione\x`, "manutenzione/./x"} {
		cases := map[string]string{
			"concept_read":   fmt.Sprintf(`{"id":%q}`, id),
			"concept_write":  fmt.Sprintf(`{"id":%q,"frontmatter":{"type":"Runbook"},"body":"x"}`, id),
			"concept_delete": fmt.Sprintf(`{"id":%q}`, id),
			"concept_move":   fmt.Sprintf(`{"source_id":%q,"target_id":"manutenzione/moved","rewrite_links":false}`, id),
			"concept_move_t": fmt.Sprintf(`{"source_id":"manutenzione/test-runbook","target_id":%q,"rewrite_links":false}`, id),
			"supersede":      fmt.Sprintf(`{"source_id":"manutenzione/test-runbook","target_id":%q}`, id),
			"concept_batch":  fmt.Sprintf(`{"operations":[{"op":"write","id":%q,"frontmatter":{"type":"Runbook"},"body":"x"}]}`, id),
			"asset_read":     fmt.Sprintf(`{"concept_id":%q,"path":"a.png"}`, id),
		}
		for name, args := range cases {
			tool := name
			if name == "concept_move_t" {
				tool = "concept_move"
			}
			if err := authorizeTool(policy, k, "docs", tool, json.RawMessage(args)); err == nil {
				t.Errorf("%s with id %q was authorized", name, id)
			}
		}
	}
	got, err := os.ReadFile(victim)
	if err != nil || string(got) != original {
		t.Fatalf("file in the other map changed: %v %q", err, got)
	}
}

// The id that is authorized is the one the tool reads: a second, differing id
// field is refused, and ordinary calls still pass.
func TestPolicyAuthorizesTheFieldTheToolReads(t *testing.T) {
	t.Parallel()
	k := setupTestKB(t)
	k.AuthName = "docs"
	policy := auth.Policy{Permissions: []auth.Permission{{KB: "docs", Write: true, Maps: []string{"manutenzione"}}}}
	in, out := "manutenzione/test-runbook", "other/y"
	refused := map[string]string{
		"asset_write":  fmt.Sprintf(`{"id":%q,"concept_id":%q,"path":"a.txt","content":"x"}`, in, out),
		"asset_read":   fmt.Sprintf(`{"id":%q,"concept_id":%q,"path":"a.txt"}`, in, out),
		"asset_list":   fmt.Sprintf(`{"id":%q,"concept_id":%q}`, in, out),
		"asset_delete": fmt.Sprintf(`{"id":%q,"concept_id":%q,"path":"a.txt","if_match":"x"}`, in, out),
		"service_get":  fmt.Sprintf(`{"id":%q,"service_id":%q}`, in, out),
		"concept_read": fmt.Sprintf(`{"id":%q,"concept_id":%q}`, in, out),
	}
	for tool, args := range refused {
		if err := authorizeTool(policy, k, "docs", tool, json.RawMessage(args)); err == nil {
			t.Errorf("%s with a shadow id field was authorized", tool)
		}
	}
	for tool, args := range map[string]string{
		"asset_list":   fmt.Sprintf(`{"concept_id":%q}`, in),
		"asset_write":  fmt.Sprintf(`{"concept_id":%q,"path":"a.txt","content":"x"}`, in),
		"concept_read": fmt.Sprintf(`{"id":%q}`, in),
	} {
		if err := authorizeTool(policy, k, "docs", tool, json.RawMessage(args)); err != nil {
			t.Errorf("%s ordinary call denied: %v", tool, err)
		}
	}
}
