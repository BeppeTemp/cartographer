package mcpserver

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/kb"
)

// usageHandler mounts the UI fixture KB ("docs") with one skill and one
// agent. Tokens: "whole" writes the whole KB, "reader" reads it, "narrow"
// sees one Map only.
func usageHandler(t *testing.T) (http.Handler, *kb.KB) { return usageHandlerWeb(t, true) }

func usageHandlerWeb(t *testing.T, web bool) (http.Handler, *kb.KB) {
	t.Helper()
	k := uiFixtureKB(t, "docs")
	k.UsageStaleDays = 42
	for rel, body := range map[string]string{
		"skills/ops-tool/SKILL.md":  "---\nname: ops-tool\ndescription: Ops things\n---\n# Ops\n",
		"skills/idle-tool/SKILL.md": "---\nname: idle-tool\ndescription: Idle things\n---\n# Idle\n",
		"agents/helper.md":          "---\nname: helper\ndescription: Helps\n---\nHelp.\n",
	} {
		full := filepath.Join(k.Root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ts := auth.NewScopedTokenStore([]auth.ScopedToken{
		{Token: "whole", Policy: auth.Policy{Permissions: []auth.Permission{{KB: "docs", Write: true}}}},
		{Token: "reader", Policy: auth.Policy{Permissions: []auth.Permission{{KB: "docs"}}}},
		{Token: "narrow", Policy: auth.Policy{Permissions: []auth.Permission{{KB: "docs", Maps: []string{"visible"}, Write: true}}}},
	})
	multi := NewMultiKBServer("test")
	multi.MountKB("docs", func(s *Server) { RegisterKBTools(s, k, Deps{}) })
	if web {
		multi.EnableWeb(nil)
	}
	return ts.Middleware(multi.Handler()), k
}

func postUsage(t *testing.T, h http.Handler, query, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, UsagePath+query, bytes.NewReader(raw))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func usageRow(name, kind, provider string, ago time.Duration, count int) map[string]any {
	return map[string]any{"name": name, "kind": kind, "provider": provider, "source": "kb:docs",
		"last_used": time.Now().Add(-ago).UTC().Format(time.RFC3339), "count": count}
}

func TestUsageEndpoint_StoresData(t *testing.T) {
	h, k := usageHandler(t)
	rr := postUsage(t, h, "?kb=docs", "whole", []map[string]any{usageRow("ops-tool", "skill", "claude", time.Hour, 3)})
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	got, err := k.LoadUsage()
	if err != nil || len(got) != 1 || got[0].Name != "ops-tool" || got[0].Count != 3 || got[0].Provider != "claude" {
		t.Fatalf("store = %+v, %v", got, err)
	}
	// Local state, never committed: the file sits under .cartographer/.
	if _, err := os.Stat(filepath.Join(k.Root, ".cartographer", "usage.json")); err != nil {
		t.Errorf("usage.json: %v", err)
	}
}

// The usage route is client metadata, not part of the UI: it works with the
// web UI switched off.
func TestUsageEndpoint_WorksWithoutWeb(t *testing.T) {
	h, k := usageHandlerWeb(t, false)
	if rr := postUsage(t, h, "", "whole", []map[string]any{usageRow("ops-tool", "skill", "claude", time.Hour, 1)}); rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	if got, _ := k.LoadUsage(); len(got) != 1 {
		t.Fatalf("store = %+v", got)
	}
}

func TestUsageEndpoint_MergesNewerTimestamp(t *testing.T) {
	h, k := usageHandler(t)
	postUsage(t, h, "?kb=docs", "whole", []map[string]any{usageRow("ops-tool", "skill", "claude", 48*time.Hour, 5)})
	postUsage(t, h, "?kb=docs", "whole", []map[string]any{usageRow("ops-tool", "skill", "claude", time.Hour, 2)})
	// An older report arriving late (another machine) does not win.
	postUsage(t, h, "?kb=docs", "whole", []map[string]any{usageRow("ops-tool", "skill", "claude", 96*time.Hour, 9)})
	got, _ := k.LoadUsage()
	if len(got) != 1 || got[0].Count != 2 || time.Since(got[0].LastUsed) > 2*time.Hour {
		t.Fatalf("store = %+v, want the newer sighting with its own count", got)
	}
}

func TestUsageEndpoint_UnknownKB(t *testing.T) {
	h, _ := usageHandler(t)
	if rr := postUsage(t, h, "?kb=nope", "whole", []map[string]any{}); rr.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404", rr.Code)
	}
	// A token that may not write the whole KB gets the same answer.
	for _, tok := range []string{"reader", "narrow"} {
		if rr := postUsage(t, h, "?kb=docs", tok, []map[string]any{usageRow("ops-tool", "skill", "claude", time.Hour, 1)}); rr.Code != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", tok, rr.Code)
		}
	}
}

func TestUsageEndpoint_AuthRequired(t *testing.T) {
	h, k := usageHandler(t)
	if rr := postUsage(t, h, "?kb=docs", "", []map[string]any{usageRow("ops-tool", "skill", "claude", time.Hour, 1)}); rr.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401", rr.Code)
	}
	if got, _ := k.LoadUsage(); len(got) != 0 {
		t.Fatalf("an unauthenticated report was stored: %+v", got)
	}
}

func TestUsageEndpoint_RejectsBadInput(t *testing.T) {
	h, k := usageHandler(t)
	req := httptest.NewRequest(http.MethodPost, UsagePath+"?kb=docs", bytes.NewReader([]byte(`{"not":"an array"}`)))
	req.Header.Set("Authorization", "Bearer whole")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status %d, want 400", rr.Code)
	}
	get := httptest.NewRequest(http.MethodGet, UsagePath+"?kb=docs", nil)
	get.Header.Set("Authorization", "Bearer whole")
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, get)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET: status %d, want 405", rr.Code)
	}
	// An unknown kind and a far-future clock are cleaned, not stored as is.
	future := usageRow("ops-tool", "skill", "claude", -400*24*time.Hour, 1)
	if rr := postUsage(t, h, "?kb=docs", "whole", []map[string]any{usageRow("x", "hook", "claude", time.Hour, 1), future}); rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	got, _ := k.LoadUsage()
	if len(got) != 1 || got[0].LastUsed.After(time.Now().Add(time.Minute)) {
		t.Fatalf("store = %+v, want the hook dropped and the future clock clamped", got)
	}
}

func TestUIAPI_ArtifactsIncludesLastUsed(t *testing.T) {
	h, _ := usageHandler(t)
	postUsage(t, h, "?kb=docs", "whole", []map[string]any{usageRow("ops-tool", "skill", "claude", 3*24*time.Hour, 4)})
	rr := getUI(t, h, UIAPIPrefix+"/kbs/docs/artifacts", "whole")
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	var body struct {
		Artifacts []uiArtifact `json:"artifacts"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	seen := map[string]uiArtifact{}
	for _, a := range body.Artifacts {
		seen[a.Kind+"/"+a.Name] = a
	}
	ops := seen["skill/ops-tool"]
	if ops.LastUsed == nil || ops.LastUsedProvider == nil || *ops.LastUsedProvider != "claude" || ops.LastUsedDaysAgo == nil || *ops.LastUsedDaysAgo != 3 {
		t.Errorf("ops-tool = %+v", ops)
	}
	idle := seen["skill/idle-tool"]
	if idle.LastUsed != nil || idle.LastUsedProvider != nil || idle.LastUsedDaysAgo != nil {
		t.Errorf("idle-tool = %+v, want null usage", idle)
	}
	// null on the wire, not absent: the panel tells "never" from "unknown field".
	var raw struct {
		Artifacts []map[string]json.RawMessage `json:"artifacts"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &raw)
	for _, a := range raw.Artifacts {
		if string(a["name"]) == `"idle-tool"` && string(a["last_used"]) != "null" {
			t.Errorf("idle-tool last_used = %s, want null", a["last_used"])
		}
	}
	// The detail route carries it too.
	rr = getUI(t, h, UIAPIPrefix+"/kbs/docs/artifact?kind=skill&name=ops-tool", "whole")
	var one uiArtifact
	_ = json.Unmarshal(rr.Body.Bytes(), &one)
	if one.LastUsed == nil {
		t.Errorf("detail route lost last_used: %s", rr.Body.String())
	}
}

func TestKBStatus_IncludesUsage(t *testing.T) {
	k := setupTestKB(t)
	k.AuthName = "docs"
	k.UsageStaleDays = 42
	for rel, body := range map[string]string{
		"skills/ops-tool/SKILL.md":   "---\nname: ops-tool\ndescription: Ops\n---\n# Ops\n",
		"skills/old-tool/SKILL.md":   "---\nname: old-tool\ndescription: Old\n---\n# Old\n",
		"skills/never-tool/SKILL.md": "---\nname: never-tool\ndescription: Never\n---\n# Never\n",
	} {
		full := filepath.Join(k.Root, rel)
		_ = os.MkdirAll(filepath.Dir(full), 0o755)
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	status := func() map[string]any {
		t.Helper()
		var m struct {
			Usage map[string]any `json:"usage"`
		}
		if err := json.Unmarshal([]byte(callOK(t, s, "kb_status", `{}`)), &m); err != nil {
			t.Fatal(err)
		}
		return m.Usage
	}
	if u := status(); u["scanner_enabled"] != false || u["no_data"] != true || u["no_report_since"] != processStartedAt {
		t.Fatalf("no reports: usage = %v", u)
	}
	if _, err := k.MergeUsage([]kb.UsageEntry{
		{Name: "ops-tool", Kind: "skill", Provider: "claude", LastUsed: time.Now().Add(-time.Hour), Count: 2},
		{Name: "old-tool", Kind: "skill", Provider: "claude", LastUsed: time.Now().Add(-60 * 24 * time.Hour), Count: 1},
	}); err != nil {
		t.Fatal(err)
	}
	u := status()
	if u["scanner_enabled"] != true || u["artifacts_active"] != float64(1) || u["artifacts_stale"] != float64(1) ||
		u["artifacts_never_used"] != float64(1) || u["stale_threshold_days"] != float64(42) {
		t.Fatalf("usage = %v", u)
	}
	if got := u["unsupported_providers"].([]any); len(got) != 4 {
		t.Errorf("unsupported_providers = %v", got)
	}
}
