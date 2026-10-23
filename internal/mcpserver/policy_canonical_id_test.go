package mcpserver

import (
	"context"
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

// Every other tool that takes a concept id refuses a non-canonical one too,
// including the ids inside concept_transfer moves and the archive destination.
func TestPolicyRefusesNonCanonicalIDsInEveryIDTool(t *testing.T) {
	t.Parallel()
	k := setupTestKB(t)
	k.AuthName = "docs"
	policy := auth.Policy{Permissions: []auth.Permission{{KB: "docs", Write: true, Maps: []string{"manutenzione"}}}}
	whole := auth.Policy{Permissions: []auth.Permission{{KB: "docs", Write: true}}}
	ids := []string{"other/../manutenzione/x", "manutenzione/../other/x", "./manutenzione/x", "manutenzione//x", "manutenzione/x/", `manutenzione\x`, "manutenzione/./x"}
	for _, id := range ids {
		cases := map[string]string{
			"concept_new":      fmt.Sprintf(`{"id":%q}`, id),
			"concept_patch":    fmt.Sprintf(`{"id":%q,"old_string":"a","new_string":"b","if_match":"x"}`, id),
			"concept_expand":   fmt.Sprintf(`{"id":%q}`, id),
			"concept_collapse": fmt.Sprintf(`{"id":%q}`, id),
			"concept_history":  fmt.Sprintf(`{"id":%q}`, id),
			"graph_neighbors":  fmt.Sprintf(`{"id":%q}`, id),
			"concept_archive":  fmt.Sprintf(`{"id":%q}`, id),
			"concept_transfer": fmt.Sprintf(`{"moves":[{"source_id":%q,"target_id":"manutenzione/y"}],"target_kb":"kb-b"}`, id),
		}
		for name, args := range cases {
			tool := name
			for pname, p := range map[string]auth.Policy{"scoped": policy, "whole": whole} {
				if err := authorizeTool(p, k, "docs", tool, json.RawMessage(args)); err == nil {
					t.Errorf("%s (%s policy) with id %q was authorized", name, pname, id)
				}
			}
		}
	}
	// The target half of a transfer refuses a non-canonical target id.
	ctx := auth.ContextWithPrincipal(context.Background(), auth.Principal{ID: "t", Policy: whole})
	for _, id := range ids {
		moves := []conceptMoveEntry{{SourceID: "manutenzione/test-runbook", TargetID: id}}
		if err := authorizeTransferTarget(ctx, k, k, "docs", moves); err == nil {
			t.Errorf("concept_transfer target id %q was authorized", id)
		}
	}
	// The archive destination is checked as well.
	for _, to := range []string{"../other", "archive/../other", `a\b`} {
		args := fmt.Sprintf(`{"id":"manutenzione/test-runbook","to":%q}`, to)
		if err := authorizeTool(whole, k, "docs", "concept_archive", json.RawMessage(args)); err == nil {
			t.Errorf("concept_archive to %q was authorized", to)
		}
	}
}

// index_patch: the authorizer and the handler clean the path the same way, so
// the resource that is authorized is the one that is patched.
func TestPolicyIndexPatchActsOnTheCleanedPath(t *testing.T) {
	t.Parallel()
	k := setupTestKB(t)
	k.AuthName = "docs"
	policy := auth.Policy{Permissions: []auth.Permission{{KB: "docs", Write: true, Maps: []string{"manutenzione"}}}}
	for path, want := range map[string]bool{
		"manutenzione":          true,
		"./manutenzione":        true,
		"manutenzione/":         true,
		`manutenzione\`:         true,
		"other/../manutenzione": true,
		"manutenzione/../other": false,
		"manutenzione/../":      false, // cleans to the root: whole-KB grant needed
		"other":                 false,
	} {
		args := fmt.Sprintf(`{"path":%q,"old_string":"a","new_string":"b","if_match":"x"}`, path)
		err := authorizeTool(policy, k, "docs", "index_patch", json.RawMessage(args))
		if (err == nil) != want {
			t.Errorf("index_patch path %q: authorized=%v, want %v", path, err == nil, want)
		}
	}
	if err := os.WriteFile(filepath.Join(k.DataRoot(), "index.md"), []byte("# Root\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The handler resolves the same cleaned path as the authorizer.
	a, _, errA := k.IndexHash("")
	b, _, errB := k.IndexHash("other/..")
	if errA != nil || errB != nil || a != b {
		t.Fatalf("IndexHash disagrees on the cleaned path: %v %v", errA, errB)
	}
}
