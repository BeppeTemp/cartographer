package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/embed"
	"github.com/BeppeTemp/cartographer/internal/okf"
	"github.com/BeppeTemp/cartographer/internal/sqlindex"
)

func restrictedContext(policy auth.Policy) context.Context {
	return auth.ContextWithPrincipal(context.Background(), auth.Principal{ID: "reader", Policy: policy})
}

func TestPolicyExactReadDoesNotDiscloseForbiddenConcept(t *testing.T) {
	k := setupTestKB(t)
	k.AuthName = "docs"
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	ctx := restrictedContext(auth.Policy{Permissions: []auth.Permission{{KB: "docs", Maps: []string{"other"}}}})
	req := &Request{ID: json.RawMessage(`1`), Method: "tools/call", Params: json.RawMessage(`{"name":"concept_read","arguments":{"id":"manutenzione/test-runbook"}}`)}
	resp := s.dispatch(ctx, req)
	b, _ := json.Marshal(resp)
	if strings.Contains(string(b), "manutenzione/test-runbook") || !strings.Contains(string(b), genericNotFound) {
		t.Fatalf("forbidden exact read disclosed resource: %s", b)
	}
}

func TestPolicySearchFiltersBeforeLimit(t *testing.T) {
	k := setupTestKB(t)
	k.AuthName = "docs"
	hidden := filepath.Join(k.DataRoot(), "hidden")
	if err := os.MkdirAll(hidden, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hidden, "secret.md"), []byte("---\ntype: Runbook\ntitle: Secret\n---\nrunbook keyword\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	ctx := restrictedContext(auth.Policy{Permissions: []auth.Permission{{KB: "docs", Maps: []string{"manutenzione"}}}})
	result, err := s.Tools()["search"].Handler(ctx, json.RawMessage(`{"query":"runbook","limit":1}`))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(result)
	if strings.Contains(string(b), "hidden/secret") || !strings.Contains(string(b), "manutenzione/test-runbook") {
		t.Fatalf("visibility/limit failure: %s", b)
	}
}

func TestPolicySQLiteSearchFiltersBeforeLimit(t *testing.T) {
	k := setupTestKB(t)
	k.AuthName = "docs"
	if err := os.MkdirAll(filepath.Join(k.DataRoot(), "hidden"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(k.DataRoot(), "hidden", "secret.md"), []byte("---\ntype: Runbook\ntitle: Secret\n---\nrunbook keyword\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ix, err := sqlindex.Open(filepath.Join(t.TempDir(), "search.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()
	for _, id := range []string{"manutenzione/test-runbook", "hidden/secret"} {
		data, err := k.ReadConcept(okf.ConceptID(id))
		if err != nil {
			t.Fatal(err)
		}
		if err := ix.Upsert(id, data.ContentHash, data.Content); err != nil {
			t.Fatal(err)
		}
	}
	s := New("test")
	RegisterKBTools(s, k, Deps{SQLIndex: ix})
	ctx := restrictedContext(auth.Policy{Permissions: []auth.Permission{{KB: "docs", Maps: []string{"manutenzione"}}}})
	result, err := s.Tools()["search"].Handler(ctx, json.RawMessage(`{"query":"runbook","limit":1}`))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(result)
	if strings.Contains(string(b), "hidden/secret") || !strings.Contains(string(b), "manutenzione/test-runbook") || strings.Contains(string(b), `"count": 2`) {
		t.Fatalf("SQLite visibility/limit/count failure: %s", b)
	}
}

func TestPolicySemanticAndHybridSearchFilterBeforeLimit(t *testing.T) {
	k := setupTestKB(t)
	k.AuthName = "docs"
	if err := os.MkdirAll(filepath.Join(k.DataRoot(), "hidden"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(k.DataRoot(), "hidden", "secret.md"), []byte("---\ntype: Secret\ntitle: Hidden title\n---\nneedle\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	idx, meta := buildIndex(k)
	live := newLiveIndex(idx, meta)
	store := embed.NewStore()
	store.Add("hidden/secret", embed.Vector{1, 0})
	store.Add("manutenzione/test-runbook", embed.Vector{0.8, 0.2})
	tool := toolSearch(k, live, Deps{Embedder: testEmbedder{}, VecStore: store})
	ctx := restrictedContext(auth.Policy{Permissions: []auth.Permission{{KB: "docs", Maps: []string{"manutenzione"}}}})
	for _, mode := range []string{"semantic", "hybrid"} {
		result, err := tool.Handler(ctx, json.RawMessage(`{"query":"needle","mode":"`+mode+`","limit":1}`))
		if err != nil {
			t.Fatal(err)
		}
		body, _ := json.Marshal(result)
		if strings.Contains(string(body), "hidden/secret") || strings.Contains(string(body), "Hidden title") || !strings.Contains(string(body), "manutenzione/test-runbook") || strings.Contains(string(body), `"count": 2`) {
			t.Fatalf("%s visibility/limit/count failure: %s", mode, body)
		}
	}
}

func TestPolicyWriteRequiresProposedTypePermission(t *testing.T) {
	k := setupTestKB(t)
	k.AuthName = "docs"
	policy := auth.Policy{Permissions: []auth.Permission{{KB: "docs", Write: true, Maps: []string{"manutenzione"}, Types: []string{"Runbook"}}}}
	err := authorizeTool(policy, k, "docs", "concept_write", json.RawMessage(`{"id":"manutenzione/test-runbook","frontmatter":{"type":"Secret"}}`))
	if err == nil {
		t.Fatal("type escalation was allowed")
	}
}

func TestResourceClassInventory(t *testing.T) {
	k := setupTestKB(t)
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	for name, tool := range s.Tools() {
		if tool.ResourceClass == "" {
			t.Fatalf("registered tool %q has no resource class", name)
		}
		if resourceClassForTool(s.StripToolPrefix(name)) != tool.ResourceClass {
			t.Fatalf("tool %q resource class %q diverges from resolver inventory", name, tool.ResourceClass)
		}
	}
}

func TestMissingPrincipalFailsClosedEverywhere(t *testing.T) {
	k := setupTestKB(t)
	k.AuthName = "docs"
	s := New("test")
	RegisterKBTools(s, k, Deps{})

	if Visible(context.Background(), k, "manutenzione/test-runbook") ||
		VisibleCollection(context.Background(), k, "manutenzione", "map") ||
		WholeVisible(context.Background(), k, false) {
		t.Fatal("missing principal acquired visibility")
	}
	call := &Request{ID: json.RawMessage(`1`), Method: "tools/call", Params: json.RawMessage(`{"name":"concept_read","arguments":{"id":"manutenzione/test-runbook"}}`)}
	if result := decodeToolResult(t, s.dispatch(context.Background(), call)); !result.IsError || result.Content[0].Text != genericNotFound {
		t.Fatalf("missing principal dispatch = %+v, want generic non-disclosure", result)
	}
	if err := New("test").authorize(context.Background(), "anything", json.RawMessage(`{}`)); err == nil {
		t.Fatal("nil authorizer permitted a missing principal")
	}
	if err := New("test").authorize(authLocalContext(), "anything", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("explicit local admin denied by nil authorizer: %v", err)
	}
	// The transport paths must carry the same rule: a bare HTTP/stdin context
	// has no authority, while Server.Run supplies its explicit local principal.
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"concept_read","arguments":{"id":"manutenzione/test-runbook"}}}`
	for name, handler := range map[string]http.Handler{
		"bare HTTP":          s.HTTPHandler(),
		"auth-disabled HTTP": auth.NewTokenStore(nil).Middleware(s.HTTPHandler()),
	} {
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body)))
		var httpResp Response
		if err := json.Unmarshal(rr.Body.Bytes(), &httpResp); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		result := decodeToolResult(t, httpResp)
		if (name == "bare HTTP" && (!result.IsError || result.Content[0].Text != genericNotFound)) || (name != "bare HTTP" && result.IsError) {
			t.Fatalf("%s = %+v", name, result)
		}
	}
	var out bytes.Buffer
	if err := s.RunContext(context.Background(), strings.NewReader(body+"\n"), &out); err != nil {
		t.Fatal(err)
	}
	var stdio Response
	if err := json.Unmarshal(out.Bytes(), &stdio); err != nil {
		t.Fatal(err)
	}
	if result := decodeToolResult(t, stdio); !result.IsError || result.Content[0].Text != genericNotFound {
		t.Fatalf("missing stdio context = %+v", result)
	}
	out.Reset()
	if err := s.Run(strings.NewReader(body+"\n"), &out); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(out.Bytes(), &stdio); err != nil {
		t.Fatal(err)
	}
	if result := decodeToolResult(t, stdio); result.IsError {
		t.Fatalf("explicit local stdio principal = %+v", result)
	}
}
