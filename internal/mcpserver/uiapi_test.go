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
	"github.com/BeppeTemp/cartographer/internal/kb"
)

// uiFixtureKB builds a KB with a visible Map, a hidden Map, a backlink pair, a
// broken link and an expanded concept — everything the non-disclosure
// assertions need.
func uiFixtureKB(t *testing.T, authName string) *kb.KB {
	t.Helper()
	k := setupTestKB(t)
	k.AuthName = authName
	if err := k.CreateMap("visible", "Visible", "map", nil, ""); err != nil {
		t.Fatal(err)
	}
	if err := k.CreateMap("hidden", "Hidden", "map", nil, ""); err != nil {
		t.Fatal(err)
	}
	write := func(rel, content string) {
		t.Helper()
		full := filepath.Join(k.DataRoot(), rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("visible/alpha.md", "---\ntype: Runbook\ntitle: Alpha\nstatus: active\n---\nLinks [beta](beta.md), [secret](../hidden/secret.md) and [gone](nope.md).\n")
	write("visible/beta.md", "---\ntype: Note\ntitle: Beta\n---\nLinks back to [alpha](alpha.md).\n")
	write("visible/owner/index.md", "---\ntype: Note\ntitle: Owner\n---\nExpanded.\n")
	write("hidden/secret.md", "---\ntype: Runbook\ntitle: Secret\n---\nLinks [alpha](../visible/alpha.md).\n")
	return k
}

// newUIHandler mounts one KB under authName and wraps it in the token store,
// exactly as serve.go does.
func newUIHandler(t *testing.T, ts *auth.TokenStore, authName string) http.Handler {
	t.Helper()
	multi := NewMultiKBServer("test")
	k := uiFixtureKB(t, authName)
	multi.MountKB(authName, func(s *Server) { RegisterKBTools(s, k, Deps{}) })
	// No static bundle: these tests are about the JSON API, which is useful
	// and testable without one.
	multi.EnableWeb(nil)
	return ts.Middleware(multi.Handler())
}

func getUI(t *testing.T, handler http.Handler, path, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

func decodeUI(t *testing.T, rr *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var out map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("response is not JSON (%d): %s", rr.Code, rr.Body.String())
	}
	return out
}

func TestUIAPI_OpenModeServesEveryRoute(t *testing.T) {
	handler := newUIHandler(t, auth.NewTokenStore(nil), "docs")
	for _, path := range []string{
		UIAPIPrefix + "/kbs",
		UIAPIPrefix + "/kbs/docs/overview",
		UIAPIPrefix + "/kbs/docs/graph",
		UIAPIPrefix + "/kbs/docs/graph?scope=visible&limit=10",
		UIAPIPrefix + "/kbs/docs/concept?id=visible/alpha",
		UIAPIPrefix + "/kbs/docs/lint",
	} {
		rr := getUI(t, handler, path, "")
		if rr.Code != http.StatusOK {
			t.Errorf("%s: status %d, want 200: %s", path, rr.Code, rr.Body.String())
		}
		if got := rr.Header().Get("Content-Type"); got != "application/json" {
			t.Errorf("%s: Content-Type %q", path, got)
		}
		if got := rr.Header().Get("Cache-Control"); got != "no-store" {
			t.Errorf("%s: Cache-Control %q, want no-store", path, got)
		}
		if got := rr.Header().Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("%s: X-Content-Type-Options %q", path, got)
		}
		if got := rr.Header().Get("Referrer-Policy"); got != "no-referrer" {
			t.Errorf("%s: Referrer-Policy %q", path, got)
		}
	}
}

func TestUIAPI_RequiresABearerTokenWhenAuthIsOn(t *testing.T) {
	ts := auth.NewScopedTokenStore([]auth.ScopedToken{
		{Token: "read-tok", Scopes: []auth.KBScope{{KB: "docs", Write: false}}},
	})
	handler := newUIHandler(t, ts, "docs")

	if rr := getUI(t, handler, UIAPIPrefix+"/kbs", ""); rr.Code != http.StatusUnauthorized {
		t.Errorf("no token: status %d, want 401", rr.Code)
	}
	if rr := getUI(t, handler, UIAPIPrefix+"/kbs", "wrong"); rr.Code != http.StatusUnauthorized {
		t.Errorf("invalid token: status %d, want 401", rr.Code)
	}
	if rr := getUI(t, handler, UIAPIPrefix+"/kbs", "read-tok"); rr.Code != http.StatusOK {
		t.Errorf("read token: status %d, want 200: %s", rr.Code, rr.Body.String())
	}
}

func TestUIAPI_IsReadOnly(t *testing.T) {
	handler := newUIHandler(t, auth.NewTokenStore(nil), "docs")
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		req := httptest.NewRequest(method, UIAPIPrefix+"/kbs", nil)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: status %d, want 405", method, rr.Code)
		}
		if got := rr.Header().Get("Allow"); got != "GET, HEAD" {
			t.Errorf("%s: Allow %q, want %q", method, got, "GET, HEAD")
		}
	}
}

func TestUIAPI_UnknownKBAndConceptReturn404NotForbidden(t *testing.T) {
	handler := newUIHandler(t, auth.NewTokenStore(nil), "docs")
	for _, path := range []string{
		UIAPIPrefix + "/kbs/nope/overview",
		UIAPIPrefix + "/kbs/docs/nope",
		UIAPIPrefix + "/kbs/docs/concept?id=visible/does-not-exist",
		UIAPIPrefix + "/unknown",
	} {
		rr := getUI(t, handler, path, "")
		if rr.Code != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", path, rr.Code)
		}
		body := decodeUI(t, rr)
		errObj, ok := body["error"].(map[string]interface{})
		if !ok || errObj["code"] != uiCodeNotFound {
			t.Errorf("%s: envelope = %v", path, body)
		}
	}
}

func TestUIAPI_MalformedParameters(t *testing.T) {
	handler := newUIHandler(t, auth.NewTokenStore(nil), "docs")
	for _, tc := range []struct{ path, field string }{
		{UIAPIPrefix + "/kbs/docs/graph?limit=abc", "limit"},
		{UIAPIPrefix + "/kbs/docs/graph?scope=a/b", "scope"},
		{UIAPIPrefix + "/kbs/docs/concept", "id"},
		{UIAPIPrefix + "/kbs/docs/lint?severity_min=loud", "severity_min"},
	} {
		rr := getUI(t, handler, tc.path, "")
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("%s: status %d, want 400: %s", tc.path, rr.Code, rr.Body.String())
		}
		errObj := decodeUI(t, rr)["error"].(map[string]interface{})
		if errObj["code"] != uiCodeInvalidRequest || errObj["field"] != tc.field {
			t.Errorf("%s: envelope = %v, want field %q", tc.path, errObj, tc.field)
		}
	}
	// The severity error names the valid values instead of leaving the caller
	// to guess them.
	rr := getUI(t, handler, UIAPIPrefix+"/kbs/docs/lint?severity_min=loud", "")
	if msg := decodeUI(t, rr)["error"].(map[string]interface{})["message"].(string); !strings.Contains(msg, "warning") {
		t.Errorf("severity error should list the valid severities, got %q", msg)
	}
}

func TestUIAPI_GraphClampsAndScopes(t *testing.T) {
	handler := newUIHandler(t, auth.NewTokenStore(nil), "docs")

	body := decodeUI(t, getUI(t, handler, UIAPIPrefix+"/kbs/docs/graph?limit=999999", ""))
	if got := body["limit"].(float64); int(got) != kb.MaxGraphNodeLimit {
		t.Errorf("limit above the maximum should clamp to %d, got %v", kb.MaxGraphNodeLimit, got)
	}

	body = decodeUI(t, getUI(t, handler, UIAPIPrefix+"/kbs/docs/graph?scope=visible", ""))
	for _, raw := range body["nodes"].([]interface{}) {
		node := raw.(map[string]interface{})
		if node["collection"] != "visible" {
			t.Errorf("scope=visible returned %v", node["id"])
		}
	}
	// The broken target is reported, never promoted to a node.
	broken := body["broken"].([]interface{})
	if len(broken) != 1 || broken[0].(map[string]interface{})["target"] != "visible/nope" {
		t.Errorf("broken targets = %v", broken)
	}
}

func TestUIAPI_ConceptCarriesBodyOutlineAndNeighbors(t *testing.T) {
	handler := newUIHandler(t, auth.NewTokenStore(nil), "docs")
	body := decodeUI(t, getUI(t, handler, UIAPIPrefix+"/kbs/docs/concept?id=visible/alpha", ""))

	if body["title"] != "Alpha" || body["collection"] != "visible" {
		t.Errorf("metadata = %v / %v", body["title"], body["collection"])
	}
	if !strings.Contains(body["body"].(string), "Links") {
		t.Errorf("body missing: %v", body["body"])
	}
	if body["content_hash"] == "" {
		t.Error("content_hash missing")
	}
	fm := body["frontmatter"].(map[string]interface{})
	if fm["type"] != "Runbook" || fm["status"] != "active" {
		t.Errorf("frontmatter = %v", fm)
	}
	outbound := body["outbound"].([]interface{})
	if len(outbound) != 2 || outbound[0] != "hidden/secret" || outbound[1] != "visible/beta" {
		t.Errorf("outbound = %v", outbound)
	}
	inbound := body["inbound"].([]interface{})
	if len(inbound) != 2 || inbound[0] != "hidden/secret" || inbound[1] != "visible/beta" {
		t.Errorf("inbound = %v", inbound)
	}
	// A link to a missing concept is not a neighbour: it is reported apart, so
	// the UI can explain it instead of rendering a chip that leads nowhere.
	broken := body["broken"].([]interface{})
	if len(broken) != 1 || broken[0] != "visible/nope" {
		t.Errorf("broken = %v", broken)
	}
}

// The panel reads both directions from one graph read (D241); a concept that
// links to itself is still not its own neighbour, and a broken link is still
// kept apart after the graph changed on disk.
func TestUIAPI_ConceptNeighborsFollowTheFiles(t *testing.T) {
	multi := NewMultiKBServer("test")
	k := uiFixtureKB(t, "docs")
	multi.MountKB("docs", func(s *Server) { RegisterKBTools(s, k, Deps{}) })
	multi.EnableWeb(nil)
	handler := auth.NewTokenStore(nil).Middleware(multi.Handler())
	path := UIAPIPrefix + "/kbs/docs/concept?id=visible/beta"
	if got := decodeUI(t, getUI(t, handler, path, ""))["outbound"].([]interface{}); len(got) != 1 {
		t.Fatalf("outbound before = %v", got)
	}
	file := filepath.Join(k.DataRoot(), "visible", "beta.md")
	if err := os.WriteFile(file, []byte("---\ntype: Note\ntitle: Beta\n---\nSelf [me](beta.md), [gone](gone.md), [a](alpha.md).\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	body := decodeUI(t, getUI(t, handler, path, ""))
	if got := body["outbound"].([]interface{}); len(got) != 1 || got[0] != "visible/alpha" {
		t.Errorf("outbound = %v, want only visible/alpha", got)
	}
	if got := body["broken"].([]interface{}); len(got) != 1 || got[0] != "visible/gone" {
		t.Errorf("broken = %v", got)
	}
}

// restrictedUIHandler wires a token narrowed to the "visible" Map: the shape a
// UI must never be able to see past.
func restrictedUIHandler(t *testing.T) http.Handler {
	t.Helper()
	ts := auth.NewScopedTokenStore([]auth.ScopedToken{
		{Token: "narrow", Policy: auth.Policy{Permissions: []auth.Permission{
			{KB: "docs", Maps: []string{"visible"}},
		}}},
	})
	return newUIHandler(t, ts, "docs")
}

func TestUIAPI_NarrowedTokenSeesNoHiddenConceptAnywhere(t *testing.T) {
	handler := restrictedUIHandler(t)
	const hidden = "hidden/secret"

	// Not as a node, not as an edge endpoint, not as a broken target.
	graph := decodeUI(t, getUI(t, handler, UIAPIPrefix+"/kbs/docs/graph", "narrow"))
	for _, raw := range graph["nodes"].([]interface{}) {
		if raw.(map[string]interface{})["id"] == hidden {
			t.Fatal("hidden concept returned as a node")
		}
	}
	for _, raw := range graph["edges"].([]interface{}) {
		e := raw.(map[string]interface{})
		if e["source"] == hidden || e["target"] == hidden {
			t.Fatalf("hidden concept returned as an edge endpoint: %v", e)
		}
	}
	for _, raw := range graph["broken"].([]interface{}) {
		if raw.(map[string]interface{})["target"] == hidden {
			t.Fatal("hidden concept disclosed as a broken target")
		}
	}

	// Not in a neighbour list.
	concept := decodeUI(t, getUI(t, handler, UIAPIPrefix+"/kbs/docs/concept?id=visible/alpha", "narrow"))
	for _, list := range []string{"outbound", "inbound"} {
		for _, raw := range concept[list].([]interface{}) {
			if raw == hidden {
				t.Fatalf("hidden concept disclosed in %s", list)
			}
		}
	}

	// Reading it directly is a 404, never a 403: a 403 would confirm it exists.
	rr := getUI(t, handler, UIAPIPrefix+"/kbs/docs/concept?id="+hidden, "narrow")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("hidden concept: status %d, want 404", rr.Code)
	}

	// Not in a count, and not in a collection listing.
	overview := decodeUI(t, getUI(t, handler, UIAPIPrefix+"/kbs/docs/overview", "narrow"))
	for _, raw := range overview["collections"].([]interface{}) {
		if raw.(map[string]interface{})["name"] == "hidden" {
			t.Fatal("hidden collection listed")
		}
	}
	concepts := overview["concepts"].(map[string]interface{})
	if total := int(concepts["total"].(float64)); total != 3 {
		t.Errorf("total must count only visible concepts: got %d, want 3", total)
	}
	byType := concepts["by_type"].(map[string]interface{})
	if got, ok := byType["Runbook"]; ok && int(got.(float64)) != 1 {
		t.Errorf("by_type must exclude the hidden Runbook: got %v", got)
	}
	// Replication facts are for a caller that can see the whole KB.
	if _, ok := overview["git"]; ok {
		t.Error("a narrowed token must not receive the KB's replication facts")
	}

	// Not in a lint finding.
	lintBody := decodeUI(t, getUI(t, handler, UIAPIPrefix+"/kbs/docs/lint", "narrow"))
	for _, raw := range lintBody["findings"].([]interface{}) {
		f := raw.(map[string]interface{})
		if strings.Contains(f["path"].(string), "hidden/") {
			t.Fatalf("lint disclosed a hidden concept: %v", f)
		}
	}
}

func TestUIAPI_NarrowedTokenSeesOnlyItsKB(t *testing.T) {
	ts := auth.NewScopedTokenStore([]auth.ScopedToken{
		{Token: "only-x", Scopes: []auth.KBScope{{KB: "kbx", Write: false}}},
	})
	multi := NewMultiKBServer("test")
	for _, name := range []string{"kbx", "kby"} {
		k := uiFixtureKB(t, name)
		multi.MountKB(name, func(s *Server) { RegisterKBTools(s, k, Deps{}) })
	}
	multi.EnableWeb(nil)
	handler := ts.Middleware(multi.Handler())

	body := decodeUI(t, getUI(t, handler, UIAPIPrefix+"/kbs", "only-x"))
	kbs := body["kbs"].([]interface{})
	if len(kbs) != 1 || kbs[0].(map[string]interface{})["name"] != "kbx" {
		t.Fatalf("kb listing = %v", kbs)
	}
	// The other KB is not merely unlisted: it answers 404.
	if rr := getUI(t, handler, UIAPIPrefix+"/kbs/kby/overview", "only-x"); rr.Code != http.StatusNotFound {
		t.Errorf("invisible KB: status %d, want 404", rr.Code)
	}
}

func TestUIAPI_LintCountsDescribeWhatIsNotShown(t *testing.T) {
	handler := newUIHandler(t, auth.NewTokenStore(nil), "docs")
	all := decodeUI(t, getUI(t, handler, UIAPIPrefix+"/kbs/docs/lint", ""))
	errorsOnly := decodeUI(t, getUI(t, handler, UIAPIPrefix+"/kbs/docs/lint?severity_min=error", ""))

	if errorsOnly["total"] != all["total"] {
		t.Errorf("total must be the unfiltered count: %v vs %v", errorsOnly["total"], all["total"])
	}
	if errorsOnly["count"].(float64) > all["count"].(float64) {
		t.Error("a severity floor cannot return more findings than no floor")
	}
	if errorsOnly["severity_min"] != "error" {
		t.Errorf("severity_min echo = %v", errorsOnly["severity_min"])
	}
	for _, raw := range errorsOnly["findings"].([]interface{}) {
		if sev := raw.(map[string]interface{})["severity"]; sev != "error" {
			t.Errorf("severity floor leaked a %v finding", sev)
		}
	}
	// The broken link in the fixture is reported against its own concept.
	found := false
	for _, raw := range all["findings"].([]interface{}) {
		if raw.(map[string]interface{})["concept"] == "visible/alpha" {
			found = true
		}
	}
	if !found {
		t.Errorf("no finding mapped back to visible/alpha: %v", all["findings"])
	}
}

// The UI API must not shadow any endpoint that existed before it.
func TestUIAPI_DoesNotShadowExistingRoutes(t *testing.T) {
	handler := newUIHandler(t, auth.NewTokenStore(nil), "docs")
	for _, tc := range []struct {
		path     string
		wantJSON string
		wantCode int
	}{
		{"/health", "status", http.StatusOK},
		{"/ready", "ready", http.StatusOK},
		{"/clients", "clients", http.StatusOK},
		{auth.WellKnownProtectedResourcePath, "resource", http.StatusOK},
	} {
		rr := getUI(t, handler, tc.path, "")
		if rr.Code != tc.wantCode {
			t.Errorf("%s: status %d, want %d", tc.path, rr.Code, tc.wantCode)
		}
		if !strings.Contains(rr.Body.String(), tc.wantJSON) {
			t.Errorf("%s: body does not look like its own endpoint: %s", tc.path, rr.Body.String())
		}
	}
	// A KB is reachable through /mcp/<name> exactly as before.
	req := httptest.NewRequest(http.MethodPost, "/mcp/docs", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("/mcp/docs: status %d, want 200: %s", rr.Code, rr.Body.String())
	}
}

// With the UI off, its paths are indistinguishable from any other unknown
// path: web.enabled: false has to restore the previous HTTP surface exactly.
func TestUIAPI_AbsentWhenTheWebSurfaceIsDisabled(t *testing.T) {
	multi := NewMultiKBServer("test")
	k := uiFixtureKB(t, "docs")
	multi.MountKB("docs", func(s *Server) { RegisterKBTools(s, k, Deps{}) })
	handler := auth.NewTokenStore(nil).Middleware(multi.Handler())

	for _, path := range []string{UIAPIPrefix + "/kbs", UIAPIPrefix + "/kbs/docs/overview", "/ui/", "/ui", "/"} {
		rr := getUI(t, handler, path, "")
		if rr.Code != http.StatusNotFound {
			t.Errorf("%s with the UI disabled: status %d, want 404", path, rr.Code)
		}
	}
	// Everything that existed before still answers.
	if rr := getUI(t, handler, "/health", ""); rr.Code != http.StatusOK {
		t.Errorf("/health: status %d, want 200", rr.Code)
	}
}

// TestUIFindingConcept: only a concept file names a concept. A map's own
// index.md, log.md or descriptor is not one, and attributing its finding to a
// concept named after the map sent the Observatory to a node that does not
// exist (D228).
func TestUIFindingConcept(t *testing.T) {
	cases := map[string]string{
		"infra/gateway.md":       "infra/gateway",
		"infra/cluster/index.md": "infra/cluster",
		"infra/cluster/nodes.md": "infra/cluster/nodes",
		`infra\dns.md`:           "infra/dns",
		"infra/index.md":         "",
		"infra/log.md":           "",
		"infra/_map.md":          "",
		"infra/_archive.md":      "",
		"index.md":               "",
		"infra":                  "",
		"infra/cluster/a.png":    "",
	}
	for path, want := range cases {
		if got := uiFindingConcept(path); got != want {
			t.Errorf("uiFindingConcept(%q) = %q, want %q", path, got, want)
		}
	}
}
