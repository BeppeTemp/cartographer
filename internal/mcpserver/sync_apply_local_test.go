package mcpserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/BeppeTemp/cartographer/internal/auth"
)

func syncApplyBundle() fstest.MapFS {
	return fstest.MapFS{
		"bundled/kb-create/SKILL.md": &fstest.MapFile{
			Data: []byte("---\nname: kb-create\ndescription: Guide KB creation\nversion: \"1.0\"\n---\nBody.\n"),
		},
	}
}

// sync_apply writes on the server host at a caller-named path, so only the
// local operator runs it: never a token, admin included, dry_run included.
func TestSyncApply_RefusesEveryTokenKind(t *testing.T) {
	t.Parallel()
	tokens := auth.NewScopedTokenStore([]auth.ScopedToken{
		{Token: "admin-tok", Policy: auth.Policy{Admin: true}},
		{Token: "whole-tok", Policy: auth.Policy{Permissions: []auth.Permission{{KB: "kb-x", Write: true}}}},
		{Token: "scoped-tok", Policy: auth.Policy{Permissions: []auth.Permission{{KB: "kb-x", Write: true, Maps: []string{"manutenzione"}}}}},
		// A token named like the local principal gains nothing from the name.
		{Token: "named-tok", Principal: "local-admin", Policy: auth.Policy{Admin: true}},
	})
	multi := NewMultiKBServer("test")
	k := setupNamedTestKB(t, "kb-x")
	multi.MountKB("kb-x", func(s *Server) { RegisterKBTools(s, k, Deps{BundleFS: syncApplyBundle()}) })
	if err := multi.EnableRoutedMount("test", nil); err != nil {
		t.Fatal(err)
	}
	handler := tokens.Middleware(multi.Handler())

	for _, tok := range []string{"admin-tok", "whole-tok", "scoped-tok", "named-tok"} {
		for _, dry := range []bool{false, true} {
			base := t.TempDir()
			args := map[string]any{"base_dir": base, "dry_run": dry}

			// Per-KB endpoint.
			rr := doMCP(handler, "kb-x", tok, toolCallBodyWithArgs("sync_apply", args))
			res := decodeToolResult(t, unmarshalMCPResponse(t, rr))
			if !res.IsError || !strings.Contains(res.Content[0].Text, denyLocalOnly) {
				t.Errorf("per-KB %s dry=%v: %+v", tok, dry, res)
			}

			// Routed mount, read-write and read-only view.
			for _, path := range []string{RoutedMountPath, RoutedMountPath + "?access=read"} {
				body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call",
					"params": map[string]any{"name": "sync_apply", "arguments": map[string]any{"kb": "kb-x", "base_dir": base, "dry_run": dry}}})
				rr := routedPostAs(t, handler, path, string(body), tok)
				res := decodeToolResult(t, unmarshalMCPResponse(t, rr))
				if !res.IsError || res.Content[0].Text == "" || strings.Contains(res.Content[0].Text, "new_revision") {
					t.Errorf("routed %s %s dry=%v: %+v", path, tok, dry, res)
				}
			}
			entries, _ := os.ReadDir(base)
			if len(entries) != 0 {
				t.Errorf("%s dry=%v: refused call left files in base_dir: %v", tok, dry, entries)
			}
		}
	}
}

func TestSyncApply_LocalOperatorAllowed(t *testing.T) {
	t.Parallel()
	k := setupTestKB(t)
	base := t.TempDir()
	s := New("1.0.0")
	RegisterKBTools(s, k, Deps{BundleFS: syncApplyBundle()})
	resps := runMCPSequence(t, s, []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"sync_apply","arguments":{"base_dir":%q}}}`, base),
	})
	if tr := decodeToolResult(t, resps[1]); tr.IsError {
		t.Fatalf("local operator refused: %+v", tr)
	}
	if _, err := os.Stat(filepath.Join(base, ".claude", "skills", "kb-create", "SKILL.md")); err != nil {
		t.Errorf("skill not materialized: %v", err)
	}
}

// Auth off over HTTP serves the local operator; the guard must let it through.
func TestSyncApply_AuthDisabledHTTPIsLocal(t *testing.T) {
	t.Parallel()
	k := setupTestKB(t)
	multi := NewMultiKBServer("test")
	multi.MountKB("kb-x", func(s *Server) { RegisterKBTools(s, k, Deps{BundleFS: syncApplyBundle()}) })
	handler := auth.NewTokenStore(nil).Middleware(multi.Handler())
	base := t.TempDir()
	rr := doMCP(handler, "kb-x", "", toolCallBodyWithArgs("sync_apply", map[string]any{"base_dir": base}))
	if res := decodeToolResult(t, unmarshalMCPResponse(t, rr)); res.IsError {
		t.Fatalf("auth-off HTTP refused: %+v", res)
	}
}

func routedPostAs(t *testing.T, handler http.Handler, path, body, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}
