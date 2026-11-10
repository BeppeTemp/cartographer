package mcpserver

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/componentproto"
)

func rawJSONPost(h http.Handler, path, token string, body any) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// Every constant of componentproto is routed, and the dispatcher names no path
// of the family that is not a constant (a new route goes through componentproto).
func TestComponentRoutes_TableMatchesComponentproto(t *testing.T) {
	t.Parallel()
	f := newSurveyorFixture(t)
	for _, p := range componentproto.Paths() {
		req := httptest.NewRequest(http.MethodGet, p, nil)
		req.Header.Set("Authorization", "Bearer surveyor-a")
		rr := httptest.NewRecorder()
		f.h.ServeHTTP(rr, req)
		if rr.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: GET status %d, want 405 (routed)", p, rr.Code)
		}
	}
	req := httptest.NewRequest(http.MethodGet, componentproto.Prefix+"nothing", nil)
	req.Header.Set("Authorization", "Bearer surveyor-a")
	rr := httptest.NewRecorder()
	f.h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("unknown path of the family: %d, want 404", rr.Code)
	}
	files, _ := filepath.Glob("*.go")
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, _ := os.ReadFile(name)
		if strings.Contains(string(src), `"/api/component/v1/`) {
			t.Errorf("%s spells a component path as a literal: use componentproto", name)
		}
	}
}

func TestComponentKinds_MatchTokenKinds(t *testing.T) {
	t.Parallel()
	want := map[string]bool{string(auth.KindHuman): true, string(auth.KindClient): true, string(auth.KindSurveyor): true}
	got := componentproto.Kinds()
	if len(got) != len(want) {
		t.Fatalf("kinds %v vs auth %v", got, want)
	}
	for _, k := range got {
		if !want[k] {
			t.Errorf("componentproto kind %q is not an auth kind", k)
		}
	}
}

// Every path of the family is documented where the route table lives.
func TestComponentPathsAreDocumented(t *testing.T) {
	t.Parallel()
	doc, err := os.ReadFile(filepath.Join("..", "..", "docs", "control-plane.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range append(componentproto.Paths(), componentproto.LegacyUsagePath, componentproto.LegacyClientSettingsPath) {
		if !strings.Contains(string(doc), p) {
			t.Errorf("docs/control-plane.md does not name %s", p)
		}
	}
}

func TestHeartbeat_ProtocolWindow(t *testing.T) {
	t.Parallel()
	f := newSurveyorFixture(t)
	for _, c := range []struct {
		protocol int
		code     int
		skew     string
	}{
		{componentproto.Protocol - 1, http.StatusBadRequest, ""}, // 0 is "not negotiated": required here
		{componentproto.Protocol, http.StatusOK, ""},
		{componentproto.Protocol + 1, http.StatusOK, componentproto.SkewAhead},
		{componentproto.Protocol + 2, http.StatusUpgradeRequired, ""},
		{componentproto.Protocol + 3, http.StatusUpgradeRequired, ""},
	} {
		rr := f.post(t, "surveyor-a", beat(map[string]any{"protocol": c.protocol}))
		if rr.Code != c.code {
			t.Errorf("protocol %d: status %d, want %d (%s)", c.protocol, rr.Code, c.code, rr.Body)
			continue
		}
		switch c.code {
		case http.StatusOK:
			live := f.multi.surveyors.live("kb-a")
			if len(live) != 1 || live[0].Protocol != c.protocol || live[0].ProtocolSkew != c.skew {
				t.Errorf("protocol %d: stored %+v, want skew %q", c.protocol, live, c.skew)
			}
			var ans map[string]any
			_ = json.Unmarshal(rr.Body.Bytes(), &ans)
			if ans["protocol"] != float64(componentproto.Protocol) || ans["min_protocol"] != float64(componentproto.MinProtocol) {
				t.Errorf("answer = %v", ans)
			}
		case http.StatusUpgradeRequired:
			var ans struct {
				Error struct {
					Code        string `json:"code"`
					Protocol    int    `json:"protocol"`
					MinProtocol int    `json:"min_protocol"`
				} `json:"error"`
			}
			_ = json.Unmarshal(rr.Body.Bytes(), &ans)
			if ans.Error.Code != componentproto.CodeProtocolUnsupported || ans.Error.Protocol != componentproto.Protocol || ans.Error.MinProtocol != componentproto.MinProtocol {
				t.Errorf("426 body = %s", rr.Body)
			}
		}
		f.multi.surveyors.remove("surveyor-a", "w-1")
	}
	// The field is required, not defaulted.
	b := beat(nil)
	delete(b, "protocol")
	if rr := f.post(t, "surveyor-a", b); rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), `"field":"protocol"`) {
		t.Errorf("no protocol: %d %s", rr.Code, rr.Body)
	}
}

// The unreleased 0.29-dev routes are gone, with no alias.
func TestSurveyorPreviewRoutesAreGone(t *testing.T) {
	t.Parallel()
	f := newSurveyorFixture(t)
	for _, p := range []string{"/api/surveyors", "/api/surveyor-settings", "/api/workers", "/api/worker-settings"} {
		if rr := rawJSONPost(f.h, p, "surveyor-a", beat(nil)); rr.Code != http.StatusNotFound {
			t.Errorf("%s: %d, want 404", p, rr.Code)
		}
	}
}

func TestSettings_ComponentAndProtocol(t *testing.T) {
	t.Parallel()
	f := newSurveyorFixture(t)
	post := func(body map[string]any) int {
		return rawJSONPost(f.h, componentproto.SettingsPath, "surveyor-a", body).Code
	}
	if c := post(report(nil)); c != http.StatusOK {
		t.Errorf("surveyor report: %d", c)
	}
	if c := post(map[string]any{"component": "client", "protocol": 1, "machine": "laptop"}); c != http.StatusOK {
		t.Errorf("client report: %d", c)
	}
	if c := post(map[string]any{"component": "client", "protocol": 1}); c != http.StatusBadRequest {
		t.Errorf("client report without machine: %d", c)
	}
	if c := post(report(map[string]any{"component": "human"})); c != http.StatusBadRequest {
		t.Errorf("unknown component: %d", c)
	}
	if c := post(report(map[string]any{"protocol": componentproto.Protocol + 2})); c != http.StatusUpgradeRequired {
		t.Errorf("protocol N+2: %d", c)
	}
	if c := post(report(map[string]any{"protocol": componentproto.Protocol + 1})); c != http.StatusOK {
		t.Errorf("protocol N+1: %d", c)
	}
	r := report(nil)
	delete(r, "protocol")
	if c := post(r); c != http.StatusBadRequest {
		t.Errorf("no protocol: %d", c)
	}
}

func TestUsage_ProtocolOnTheNewRouteAndLegacyAlias(t *testing.T) {
	t.Parallel()
	h, _ := usageHandler(t)
	rows := []map[string]any{usageRow("ops-tool", "skill", "claude", time.Hour, 3)}
	for _, c := range []struct {
		path string
		code int
	}{
		{componentproto.UsagePath + "?kb=docs&protocol=1", http.StatusOK},
		{componentproto.UsagePath + "?kb=docs&protocol=2", http.StatusOK},
		{componentproto.UsagePath + "?kb=docs&protocol=3", http.StatusUpgradeRequired},
		{componentproto.UsagePath + "?kb=docs", http.StatusBadRequest},
		{componentproto.UsagePath + "?kb=docs&protocol=x", http.StatusBadRequest},
		// The 0.28 alias negotiates nothing and answers as it always did.
		{componentproto.LegacyUsagePath + "?kb=docs", http.StatusOK},
	} {
		if rr := rawJSONPost(h, c.path, "whole", rows); rr.Code != c.code {
			t.Errorf("%s: %d, want %d (%s)", c.path, rr.Code, c.code, rr.Body)
		}
	}
	rr := rawJSONPost(h, componentproto.LegacyUsagePath+"?kb=docs", "whole", rows)
	var ans map[string]int
	_ = json.Unmarshal(rr.Body.Bytes(), &ans)
	if ans["received"] != 1 || len(ans) != 2 {
		t.Errorf("legacy usage answer = %s, want the 0.28 {received, merged}", rr.Body)
	}
}

func TestHealth_NamesTheComponentProtocol(t *testing.T) {
	t.Parallel()
	f := newSurveyorFixture(t)
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rr := httptest.NewRecorder()
	f.h.ServeHTTP(rr, req)
	var h map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &h)
	if h["component_protocol"] != float64(componentproto.Protocol) || h["component_min_protocol"] != float64(componentproto.MinProtocol) {
		t.Errorf("health = %v", h)
	}
}
