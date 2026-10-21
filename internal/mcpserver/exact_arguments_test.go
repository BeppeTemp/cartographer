package mcpserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/auth"
)

func scopedServer(t *testing.T) (*Server, context.Context, string) {
	t.Helper()
	k := setupTestKB(t)
	k.AuthName = "docs"
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	dir := filepath.Join(k.DataRoot(), "other")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(dir, "y.md")
	if err := os.WriteFile(victim, []byte("---\ntype: Runbook\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := auth.Principal{ID: "scoped", Policy: auth.Policy{Permissions: []auth.Permission{{KB: "docs", Write: true, Maps: []string{"manutenzione"}}}}}
	return s, auth.ContextWithPrincipal(context.Background(), p), victim
}

// Tool arguments are matched by their exact name, and an id field that is not
// the one the tool reads is refused.
func TestToolArgumentsAreMatchedByExactName(t *testing.T) {
	t.Parallel()
	s, ctx, victim := scopedServer(t)
	before, _ := os.ReadFile(victim)
	in := "manutenzione/test-runbook"
	for name, c := range map[string][2]string{
		"upper-case duplicate":  {"concept_delete", `{"id":"` + in + `","ID":"other/y"}`},
		"upper-case alone":      {"concept_read", `{"ID":"other/y"}`},
		"asset duplicate":       {"asset_write", `{"concept_id":"` + in + `","Concept_ID":"other/y","path":"a.txt","content":"x"}`},
		"merge shadow id":       {"concept_merge", `{"id":"` + in + `","satellite_id":"other/y/z","if_match":"x"}`},
		"merge foreign parent":  {"concept_merge", `{"satellite_id":"other/y/z","if_match":"x"}`},
		"batch element mixed":   {"concept_batch", `{"operations":[{"op":"delete","Id":"other/y"}]}`},
		"batch element foreign": {"concept_batch", `{"operations":[{"op":"delete","id":"other/y"}]}`},
	} {
		res := s.callTool(ctx, c[0], json.RawMessage(c[1]))
		if !res.IsError {
			t.Errorf("%s: %s was not refused", name, c[0])
		}
	}
	after, err := os.ReadFile(victim)
	if err != nil || string(after) != string(before) {
		t.Fatalf("file in the other map changed: %v", err)
	}
	if res := s.callTool(ctx, "concept_read", json.RawMessage(`{"id":"`+in+`"}`)); res.IsError {
		t.Errorf("ordinary read refused: %+v", res)
	}
}

// Every tool schema spells its top-level properties in lower case, which is
// what the exact-name check relies on.
func TestToolSchemasUseLowerCaseProperties(t *testing.T) {
	t.Parallel()
	k := setupTestKB(t)
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	for name, tool := range s.Tools() {
		var schema struct {
			Properties map[string]json.RawMessage `json:"properties"`
		}
		if json.Unmarshal(tool.InputSchema, &schema) != nil {
			continue
		}
		for prop := range schema.Properties {
			if strings.Trim(prop, "abcdefghijklmnopqrstuvwxyz0123456789_") != "" {
				t.Errorf("%s: property %q is not lower-case ASCII", name, prop)
			}
		}
	}
}

// encoding/json folds U+017F onto "s" and the Kelvin sign onto "k": a key
// carrying either is lower case and would still reach an ASCII field.
func TestExactKeysRefuseNonASCII(t *testing.T) {
	t.Parallel()
	for _, key := range []string{"ſatellite_id", "Kind", "idé"} {
		if exactKeys(map[string]json.RawMessage{key: json.RawMessage(`"x"`)}) {
			t.Errorf("%q accepted", key)
		}
	}
	if !exactKeys(map[string]json.RawMessage{"satellite_id": nil, "if_match": nil}) {
		t.Error("ordinary keys refused")
	}
}
