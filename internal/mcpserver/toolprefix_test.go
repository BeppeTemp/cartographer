package mcpserver

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/auth"
)

// TestRegisterTool_NoPrefix verifies the byte-identical no-prefix path: a
// tool registered on a server with no tool-name prefix keeps its bare name.
func TestRegisterTool_NoPrefix(t *testing.T) {
	s := New("test")
	s.RegisterTool(Tool{Name: "atlas_overview"})
	if _, ok := s.Tools()["atlas_overview"]; !ok {
		t.Fatalf("expected unprefixed tool name %q to be registered; got %v", "atlas_overview", s.Tools())
	}
}

// TestRegisterTool_WithPrefix verifies RegisterTool rewrites the tool name to
// "<prefix>__<name>" once SetToolNamePrefix has been called (D102).
func TestRegisterTool_WithPrefix(t *testing.T) {
	s := New("test")
	s.SetToolNamePrefix("aiteam")
	s.RegisterTool(Tool{Name: "atlas_overview"})

	if _, ok := s.Tools()["atlas_overview"]; ok {
		t.Fatalf("bare tool name %q must not be registered when a prefix is set", "atlas_overview")
	}
	if _, ok := s.Tools()["aiteam__atlas_overview"]; !ok {
		t.Fatalf("expected prefixed tool name %q to be registered; got %v", "aiteam__atlas_overview", s.Tools())
	}
}

// TestServer_ToolRequiresWrite_Prefixed verifies that ToolRequiresWrite
// (the Server method, which strips this server's tool-name prefix) still
// classifies prefixed tool names correctly against the pre-prefix name
// tables — the security-critical path flagged by the issue.
func TestServer_ToolRequiresWrite_Prefixed(t *testing.T) {
	s := New("test")
	s.SetToolNamePrefix("aiteam")

	if s.ToolRequiresWrite("aiteam__atlas_overview") {
		t.Error("aiteam__atlas_overview (read-only tool, prefixed) misclassified as requiring write")
	}
	if !s.ToolRequiresWrite("aiteam__concept_write") {
		t.Error("aiteam__concept_write (write tool, prefixed) misclassified as read-only")
	}
	// Unknown names remain fail-closed (require write) whether or not they
	// carry the server's prefix.
	if !s.ToolRequiresWrite("aiteam__no_such_tool") {
		t.Error("unknown prefixed tool name should fail-closed to write-required")
	}
}

// TestServer_ToolsProfile_Prefixed verifies the "agent" tools-profile filter
// (ToolAdvanced) still hides advanced tools under their prefixed name, and
// (D123) still exposes the four canonical governance descriptors — with
// readOnlyHint:true — under their configured prefix.
func TestServer_ToolsProfile_Prefixed(t *testing.T) {
	k := setupTestKB(t)
	s := New("test")
	s.SetToolNamePrefix("aiteam")
	RegisterKBTools(s, k, Deps{})
	s.SetToolsProfile("agent")

	resps := runMCPSequence(t, s, []string{toolsListBody})
	if len(resps) != 1 {
		t.Fatalf("expected 1 response, got %d", len(resps))
	}
	names := toolNamesFromToolsList(t, resps[0])

	if _, ok := names["aiteam__reindex"]; ok {
		t.Error("aiteam__reindex is an advanced tool and must be hidden under the agent profile, even prefixed")
	}
	if _, ok := names["aiteam__atlas_overview"]; !ok {
		t.Error("aiteam__atlas_overview must be visible under the agent profile")
	}

	readOnlyHints := toolReadOnlyHintsFromToolsList(t, resps[0])
	for _, name := range []string{"aiteam__validate", "aiteam__lint", "aiteam__gate_check", "aiteam__kb_status"} {
		hint, ok := readOnlyHints[name]
		if !ok {
			t.Errorf("%s must be visible under the agent profile, even prefixed", name)
			continue
		}
		if !hint {
			t.Errorf("%s must advertise readOnlyHint:true under the agent profile", name)
		}
	}
}

// toolNamesFromToolsList decodes a tools/list Response into a name set.
func toolNamesFromToolsList(t *testing.T, resp Response) map[string]bool {
	t.Helper()
	resultBytes, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	var result struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(resultBytes, &result); err != nil {
		t.Fatalf("unmarshal tools/list result: %v", err)
	}
	out := make(map[string]bool, len(result.Tools))
	for _, tl := range result.Tools {
		out[tl.Name] = true
	}
	return out
}

// toolReadOnlyHintsFromToolsList decodes a tools/list Response into a
// name -> annotations.readOnlyHint map.
func toolReadOnlyHintsFromToolsList(t *testing.T, resp Response) map[string]bool {
	t.Helper()
	resultBytes, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	var result struct {
		Tools []struct {
			Name        string `json:"name"`
			Annotations struct {
				ReadOnlyHint bool `json:"readOnlyHint"`
			} `json:"annotations"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(resultBytes, &result); err != nil {
		t.Fatalf("unmarshal tools/list result: %v", err)
	}
	out := make(map[string]bool, len(result.Tools))
	for _, tl := range result.Tools {
		out[tl.Name] = tl.Annotations.ReadOnlyHint
	}
	return out
}

// TestMountKBWithPrefix_TwoKBs_DisjointNames mounts two KBs, one with a
// prefix and one without, and asserts tools/list returns disjoint,
// correctly-prefixed name sets per endpoint.
func TestMountKBWithPrefix_TwoKBs_DisjointNames(t *testing.T) {
	multi := NewMultiKBServer("test")
	kUnprefixed := setupTestKB(t)
	if err := multi.MountKBWithPrefix("personal-kb", "", func(s *Server) {
		RegisterKBTools(s, kUnprefixed, Deps{})
	}); err != nil {
		t.Fatalf("MountKBWithPrefix(personal-kb): %v", err)
	}
	kPrefixed := setupTestKB(t)
	if err := multi.MountKBWithPrefix("ai-team", "aiteam", func(s *Server) {
		RegisterKBTools(s, kPrefixed, Deps{})
	}); err != nil {
		t.Fatalf("MountKBWithPrefix(ai-team): %v", err)
	}
	// Auth-disabled HTTP injects an explicit local-admin principal. Calling the
	// bare multi-KB handler would intentionally be denied as a missing caller.
	handler := auth.NewTokenStore(nil).Middleware(multi.Handler())

	rrPersonal := doMCP(handler, "personal-kb", "", toolsListBody)
	if rrPersonal.Code != http.StatusOK {
		t.Fatalf("personal-kb tools/list: status = %d, body=%s", rrPersonal.Code, rrPersonal.Body.String())
	}
	var respPersonal Response
	if err := json.Unmarshal(rrPersonal.Body.Bytes(), &respPersonal); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	personalNames := toolNamesFromToolsList(t, respPersonal)

	rrAiTeam := doMCP(handler, "ai-team", "", toolsListBody)
	if rrAiTeam.Code != http.StatusOK {
		t.Fatalf("ai-team tools/list: status = %d, body=%s", rrAiTeam.Code, rrAiTeam.Body.String())
	}
	var respAiTeam Response
	if err := json.Unmarshal(rrAiTeam.Body.Bytes(), &respAiTeam); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	aiTeamNames := toolNamesFromToolsList(t, respAiTeam)

	if !personalNames["atlas_overview"] {
		t.Error("personal-kb should expose the bare atlas_overview")
	}
	if aiTeamNames["atlas_overview"] {
		t.Error("ai-team must not expose the bare atlas_overview (it's prefixed)")
	}
	if !aiTeamNames["aiteam__atlas_overview"] {
		t.Error("ai-team should expose aiteam__atlas_overview")
	}
	if personalNames["aiteam__atlas_overview"] {
		t.Error("personal-kb must not expose the ai-team-prefixed name")
	}
	for name := range aiTeamNames {
		if personalNames[name] {
			t.Errorf("tool name %q collides between the two endpoints", name)
		}
	}
}

// TestMountKBWithPrefix_ToolsCall_HitsCorrectKB verifies a prefixed tool
// name executes against its own KB, not the other mounted one.
func TestMountKBWithPrefix_ToolsCall_HitsCorrectKB(t *testing.T) {
	multi := NewMultiKBServer("test")
	kA := setupTestKB(t)
	if err := multi.MountKBWithPrefix("kb-a", "", func(s *Server) {
		RegisterKBTools(s, kA, Deps{})
	}); err != nil {
		t.Fatalf("MountKBWithPrefix(kb-a): %v", err)
	}
	kB := setupTestKB(t)
	if err := multi.MountKBWithPrefix("kb-b", "kbb", func(s *Server) {
		RegisterKBTools(s, kB, Deps{})
	}); err != nil {
		t.Fatalf("MountKBWithPrefix(kb-b): %v", err)
	}
	handler := auth.NewTokenStore(nil).Middleware(multi.Handler())

	rr := doMCP(handler, "kb-b", "", writeToolCallBody("kbb__atlas_overview"))
	if rr.Code != http.StatusOK {
		t.Fatalf("kbb__atlas_overview on kb-b: status = %d, body=%s", rr.Code, rr.Body.String())
	}
	var resp Response
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	tr := decodeToolResult(t, resp)
	if tr.IsError {
		t.Fatalf("kbb__atlas_overview returned isError=true: %v", tr.Content)
	}

	// Calling the prefixed name against the wrong (unprefixed) KB endpoint
	// must fail — that name was never registered there.
	rr = doMCP(handler, "kb-a", "", writeToolCallBody("kbb__atlas_overview"))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	tr = decodeToolResult(t, resp)
	if !tr.IsError || !strings.Contains(tr.Content[0].Text, "not found") {
		t.Fatalf("expected 'tool not found' calling kbb__atlas_overview on kb-a, got: %+v", tr)
	}
}

// TestMountKBWithPrefix_ScopedTokenMatrix verifies a read-only ("kb:<name>:r")
// scope token still rejects a prefixed write tool, and accepts it with rw —
// the security-critical matrix from the issue's test plan.
func TestMountKBWithPrefix_ScopedTokenMatrix(t *testing.T) {
	multi := NewMultiKBServer("test")
	k := setupTestKB(t)
	if err := multi.MountKBWithPrefix("ai-team", "aiteam", func(s *Server) {
		RegisterKBTools(s, k, Deps{})
	}); err != nil {
		t.Fatalf("MountKBWithPrefix: %v", err)
	}
	ts := auth.NewScopedTokenStore([]auth.ScopedToken{
		{Token: "r-tok", Scopes: []auth.KBScope{{KB: "ai-team", Write: false}}},
		{Token: "rw-tok", Scopes: []auth.KBScope{{KB: "ai-team", Write: true}}},
	})
	handler := ts.Middleware(multi.Handler())

	rr := doMCP(handler, "ai-team", "r-tok", writeToolCallBody("aiteam__concept_write"))
	assertMCPForbidden(t, rr)

	rr = doMCP(handler, "ai-team", "r-tok", writeToolCallBody("aiteam__atlas_overview"))
	if rr.Code != http.StatusOK {
		t.Fatalf("prefixed read tool with r scope: status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}

	rr = doMCP(handler, "ai-team", "rw-tok", writeToolCallBody("aiteam__concept_write"))
	if rr.Code != http.StatusOK {
		t.Fatalf("prefixed write tool with rw scope: status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}

	// index_patch (D122 WP3) follows the same prefixed read/write matrix.
	rr = doMCP(handler, "ai-team", "r-tok", writeToolCallBody("aiteam__index_patch"))
	assertMCPForbidden(t, rr)

	rr = doMCP(handler, "ai-team", "rw-tok", writeToolCallBody("aiteam__index_patch"))
	if rr.Code != http.StatusOK {
		t.Fatalf("prefixed index_patch with rw scope: status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
}

// TestMountKBWithPrefix_BudgetExceeded verifies a tool_prefix that pushes a
// resulting tool name past maxToolNameLen fails the mount instead of
// registering an over-budget name.
func TestMountKBWithPrefix_BudgetExceeded(t *testing.T) {
	multi := NewMultiKBServer("test")
	k := setupTestKB(t)
	longPrefix := strings.Repeat("a", maxToolNameLen)
	err := multi.MountKBWithPrefix("kb", longPrefix, func(s *Server) {
		RegisterKBTools(s, k, Deps{})
	})
	if err == nil {
		t.Fatal("expected an error for an over-budget tool name, got nil")
	}
	if !strings.Contains(err.Error(), "kb") {
		t.Errorf("error should name the KB: %v", err)
	}
}

// TestServer_DisplayName_ServerInfo verifies SetDisplayName overrides
// serverInfo.name, and that leaving it unset keeps the historical
// "cartographer" (single-KB path, asserted verbatim elsewhere too).
func TestServer_DisplayName_ServerInfo(t *testing.T) {
	k := setupTestKB(t)
	s := New("test")
	s.SetDisplayName("cartographer:ai-team")
	RegisterKBTools(s, k, Deps{})

	initMsg := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`
	resps := runMCPSequence(t, s, []string{initMsg})
	if len(resps) != 1 {
		t.Fatalf("expected 1 response, got %d", len(resps))
	}
	resultBytes, _ := json.Marshal(resps[0].Result)
	var result map[string]interface{}
	json.Unmarshal(resultBytes, &result)
	info, ok := result["serverInfo"].(map[string]interface{})
	if !ok || info["name"] != "cartographer:ai-team" {
		t.Errorf("initialize: unexpected serverInfo: %v", result["serverInfo"])
	}
}
