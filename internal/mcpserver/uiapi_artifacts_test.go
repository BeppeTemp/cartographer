package mcpserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// artifactUIHandler mounts the UI fixture KB with one artifact of each kind,
// a binary and an oversized skill file, an invalid skill and an MCP descriptor
// outside the allowlist. Tokens: "whole" reads the whole KB, "narrow" only the
// "visible" Map.
func artifactUIHandler(t *testing.T) http.Handler {
	t.Helper()
	k := uiFixtureKB(t, "docs")
	write := func(rel string, data []byte, mode os.FileMode) {
		t.Helper()
		full := filepath.Join(k.Root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, data, mode); err != nil {
			t.Fatal(err)
		}
	}
	write("skills/review/SKILL.md", []byte("---\nname: review\ndescription: Reviews a change\n---\n# Review\n"), 0o644)
	write("skills/review/logo.bin", []byte{0xff, 0xfe, 0x00, 0x01}, 0o644)
	write("skills/review/big.md", []byte(strings.Repeat("a", artifactMaxFileSize+1)), 0o644)
	write("skills/broken/SKILL.md", []byte("---\nname: other\ndescription: wrong name\n---\n"), 0o644)
	write("agents/triage.md", []byte("---\nname: triage\ndescription: Sorts incoming issues\n---\nBody\n"), 0o644)
	write("hooks/guard/hook.json", []byte(`{"event":"SessionStart"}`), 0o644)
	write("hooks/guard/run.sh", []byte("#!/bin/sh\n"), 0o755)
	write("mcp/tools.json", []byte(`{"type":"stdio","command":"tool"}`), 0o644)
	write("mcp/denied.json", []byte(`{"type":"stdio","command":"secret-tool"}`), 0o644)
	write("instructions.md", []byte("# House rules\n"), 0o644)
	write("templates/runbook.md", []byte("---\ntype: Runbook\ntitle: Runbook template\n---\n# {{title}}\n"), 0o644)

	ts := auth.NewScopedTokenStore([]auth.ScopedToken{
		{Token: "whole", Policy: auth.Policy{Permissions: []auth.Permission{{KB: "docs"}}}},
		{Token: "narrow", Policy: auth.Policy{Permissions: []auth.Permission{{KB: "docs", Maps: []string{"visible"}}}}},
	})
	multi := NewMultiKBServer("test")
	allow := []provisioning.MCPAllowlistEntry{{Name: "tools", Transport: "stdio", Target: "tool"}}
	multi.MountKB("docs", func(s *Server) { RegisterKBTools(s, k, Deps{MCPAllowlist: allow}) })
	multi.EnableWeb(nil)
	return ts.Middleware(multi.Handler())
}

func TestUIAPI_ArtifactsListsWhatTheKBShips(t *testing.T) {
	handler := artifactUIHandler(t)
	rr := getUI(t, handler, UIAPIPrefix+"/kbs/docs/artifacts", "whole")
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	var body struct {
		Artifacts []uiArtifact   `json:"artifacts"`
		Counts    map[string]int `json:"counts"`
		Issues    []string       `json:"issues"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	var names []string
	byName := map[string]uiArtifact{}
	for _, a := range body.Artifacts {
		names = append(names, a.Kind+"/"+a.Name)
		byName[a.Kind+"/"+a.Name] = a
		for _, f := range a.Files {
			if f.Content != nil || f.Binary || f.Truncated {
				t.Errorf("%s: the list route must not carry file content", f.Path)
			}
		}
	}
	want := "agent/triage hook/guard instructions/instructions mcp/tools skill/review template/runbook"
	if got := strings.Join(names, " "); got != want {
		t.Fatalf("artifacts = %s, want %s (bundled skills, the invalid skill and the denied descriptor excluded)", got, want)
	}
	if body.Counts["skill"] != 1 || body.Counts["template"] != 1 {
		t.Errorf("counts = %v", body.Counts)
	}
	if len(body.Issues) != 1 || !strings.Contains(body.Issues[0], "skills/broken") || strings.HasPrefix(body.Issues[0], "KB ") {
		t.Errorf("issues = %q, want one about skills/broken without the internal KB key", body.Issues)
	}
	for _, issue := range body.Issues {
		if strings.Contains(issue, "secret-tool") || strings.Contains(issue, "denied") {
			t.Errorf("an issue names the descriptor outside the allowlist: %q", issue)
		}
	}

	skill := byName["skill/review"]
	if skill.Description != "Reviews a change" || skill.ContentHash == "" || skill.Signed == nil || *skill.Signed {
		t.Errorf("skill metadata = %+v", skill)
	}
	if byName["agent/triage"].Description != "Sorts incoming issues" {
		t.Errorf("agent description = %q", byName["agent/triage"].Description)
	}
	if byName["template/runbook"].Description != "Runbook template" || byName["template/runbook"].Signed != nil {
		t.Errorf("template = %+v", byName["template/runbook"])
	}
	// instructions.md reaches clients inside the generated block (D61);
	// a template never leaves the KB.
	if got := byName["instructions/instructions"]; len(got.Clients) == 0 || got.Signed != nil {
		t.Errorf("instructions = %+v, want clients and no signature", got)
	}
	if got := byName["template/runbook"]; len(got.Clients) != 0 {
		t.Errorf("template clients = %+v, want none", got.Clients)
	}
	hookClients := map[string]bool{}
	for _, c := range byName["hook/guard"].Clients {
		hookClients[c.ID] = true
		if c.Name == "" {
			t.Errorf("client %q has no display name", c.ID)
		}
	}
	if !hookClients[string(configurator.ProviderClaudeCode)] || hookClients[string(configurator.ProviderKiro)] {
		t.Errorf("hook clients = %v", hookClients)
	}
}

func TestUIAPI_ArtifactCarriesBoundedTextContent(t *testing.T) {
	handler := artifactUIHandler(t)
	rr := getUI(t, handler, UIAPIPrefix+"/kbs/docs/artifact?kind=skill&name=review", "whole")
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	var a uiArtifact
	if err := json.Unmarshal(rr.Body.Bytes(), &a); err != nil {
		t.Fatal(err)
	}
	files := map[string]uiArtifactFile{}
	for _, f := range a.Files {
		files[f.Path] = f
	}
	if f := files["skills/review/SKILL.md"]; f.Content == nil || !strings.Contains(*f.Content, "# Review") {
		t.Errorf("SKILL.md = %+v", f)
	}
	if f := files["skills/review/logo.bin"]; !f.Binary || f.Content != nil || f.Size != 4 {
		t.Errorf("binary file = %+v", f)
	}
	if f := files["skills/review/big.md"]; !f.Truncated || f.Content != nil || f.Size != artifactMaxFileSize+1 {
		t.Errorf("oversized file = %+v", f)
	}
}

func TestUIAPI_ArtifactsAreWholeKBResources(t *testing.T) {
	handler := artifactUIHandler(t)
	for _, path := range []string{"/kbs/docs/artifacts", "/kbs/docs/artifact?kind=skill&name=review"} {
		if rr := getUI(t, handler, UIAPIPrefix+path, "narrow"); rr.Code != http.StatusNotFound {
			t.Errorf("narrow %s: status %d, want 404", path, rr.Code)
		}
	}
	flag := func(token string) interface{} {
		kbs := decodeUI(t, getUI(t, handler, UIAPIPrefix+"/kbs", token))["kbs"].([]interface{})
		return kbs[0].(map[string]interface{})["artifacts"]
	}
	if got := flag("narrow"); got != false {
		t.Errorf("narrow artifacts flag = %v, want false", got)
	}
	if got := flag("whole"); got != true {
		t.Errorf("whole artifacts flag = %v, want true", got)
	}
}

func TestUIAPI_ArtifactRequestErrors(t *testing.T) {
	handler := artifactUIHandler(t)
	for _, tc := range []struct {
		query, field string
		status       int
	}{
		{"name=review", "kind", http.StatusBadRequest},
		{"kind=widget&name=review", "kind", http.StatusBadRequest},
		{"kind=skill", "name", http.StatusBadRequest},
		{"kind=skill&name=nope", "", http.StatusNotFound},
		{"kind=mcp&name=denied", "", http.StatusNotFound},
		{"kind=skill&name=broken", "", http.StatusNotFound},
	} {
		rr := getUI(t, handler, UIAPIPrefix+"/kbs/docs/artifact?"+tc.query, "whole")
		if rr.Code != tc.status {
			t.Errorf("%s: status %d, want %d", tc.query, rr.Code, tc.status)
			continue
		}
		if tc.field != "" {
			if got := decodeUI(t, rr)["error"].(map[string]interface{})["field"]; got != tc.field {
				t.Errorf("%s: field %v, want %s", tc.query, got, tc.field)
			}
		}
	}
	req := httptest.NewRequest(http.MethodPost, UIAPIPrefix+"/kbs/docs/artifacts", nil)
	req.Header.Set("Authorization", "Bearer whole")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST: status %d, want 405", rr.Code)
	}
}

func TestUIAPI_ArtifactsRefuseASymlinkedTree(t *testing.T) {
	k := uiFixtureKB(t, "docs")
	if err := os.MkdirAll(filepath.Join(k.Root, "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(k.Root, "skills", "linked")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	multi := NewMultiKBServer("test")
	multi.MountKB("docs", func(s *Server) { RegisterKBTools(s, k, Deps{}) })
	multi.EnableWeb(nil)
	handler := auth.NewTokenStore(nil).Middleware(multi.Handler())
	rr := getUI(t, handler, UIAPIPrefix+"/kbs/docs/artifacts", "")
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500: %s", rr.Code, rr.Body.String())
	}
	if code := decodeUI(t, rr)["error"].(map[string]interface{})["code"]; code != uiCodeInternal {
		t.Errorf("error code = %v", code)
	}
}
