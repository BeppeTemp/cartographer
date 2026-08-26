package mcpserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/auth"
)

func policyFixture(t *testing.T) (*Server, requestContext) {
	t.Helper()
	k := setupTestKB(t)
	k.AuthName = "docs"
	if err := k.CreateMap("visible", "Visible", "map", nil, ""); err != nil {
		t.Fatal(err)
	}
	if err := k.CreateMap("hidden", "Hidden", "journal", nil, ""); err != nil {
		t.Fatal(err)
	}
	write := func(id, typ, title string) {
		path := filepath.Join(k.DataRoot(), id+".md")
		if err := os.WriteFile(path, []byte("---\ntype: "+typ+"\ntitle: "+title+"\n---\nneedle body\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("visible/runbook", "Runbook", "Visible title")
	write("visible/service", "Service", "Visible service")
	write("hidden/secret", "Runbook", "Hidden title")
	write("hidden/contradiction", "Contradiction", "Hidden contradiction")
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	ctx := restrictedContext(auth.Policy{Permissions: []auth.Permission{{KB: "docs", Maps: []string{"visible"}}}})
	return s, ctx
}

func toolText(t *testing.T, s *Server, ctx requestContext, name, args string) string {
	t.Helper()
	result, err := s.Tools()[name].Handler(ctx, json.RawMessage(args))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("%s returned error: %+v", name, result.Content)
	}
	var b strings.Builder
	for _, block := range result.Content {
		b.WriteString(block.Text)
	}
	return b.String()
}

func TestPolicyRetrievalFiltersMixedVisibility(t *testing.T) {
	s, ctx := policyFixture(t)
	for name, args := range map[string]string{
		"search":               `{"query":"needle","limit":10}`,
		"concept_list":         `{}`,
		"map_list":             `{}`,
		"atlas_overview":       `{}`,
		"service_list":         `{}`,
		"contradiction_report": `{}`,
	} {
		text := toolText(t, s, ctx, name, args)
		if strings.Contains(text, "hidden/") || strings.Contains(text, "Hidden") {
			t.Fatalf("%s leaked hidden resource: %s", name, text)
		}
	}
}

func TestPolicyExactMissingAndForbiddenAreIndistinguishable(t *testing.T) {
	s, ctx := policyFixture(t)
	for _, id := range []string{"hidden/secret", "visible/missing"} {
		resp := s.dispatch(ctx, dispatchReady(&Request{ID: json.RawMessage(`1`), Method: "tools/call", Params: json.RawMessage(`{"name":"concept_read","arguments":{"id":"` + id + `"}}`)}))
		result := decodeToolResult(t, resp)
		if !result.IsError || len(result.Content) == 0 || result.Content[0].Text != genericNotFound {
			t.Fatalf("%s = %+v; want generic not found", id, result)
		}
	}
}
