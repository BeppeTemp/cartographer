package mcpserver

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Tests for D128: the server answers both the handshake era and 2026-07-28,
// deciding which per request, and a handshake-era client sees no difference.

const metaNewEra = `"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}`

func newEraServer(t *testing.T) *Server {
	t.Helper()
	k := setupTestKB(t)
	s := New("1.2.3")
	RegisterKBTools(s, k, Deps{})
	return s
}

// resultMap decodes a response's result into a generic map.
func resultMap(t *testing.T, resp Response) map[string]interface{} {
	t.Helper()
	raw, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	return m
}

func TestDiscover_BothEras(t *testing.T) {
	s := newEraServer(t)

	for _, tc := range []struct {
		name   string
		params string
	}{
		{"handshake era", `{}`},
		{"2026-07-28 era", `{` + metaNewEra + `}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			msg := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"server/discover","params":%s}`, tc.params)
			resps := runMCPSequence(t, s, []string{msg})
			if len(resps) != 1 || resps[0].Error != nil {
				t.Fatalf("server/discover: %+v", resps)
			}
			result := resultMap(t, resps[0])

			versions, _ := result["protocolVersions"].([]interface{})
			if len(versions) != 2 || versions[0] != ProtocolVersion20260728 || versions[1] != SupportedProtocolVersion {
				t.Errorf("protocolVersions = %v, want the two supported versions newest first", versions)
			}
			caps, _ := result["capabilities"].(map[string]interface{})
			if _, ok := caps["tools"]; !ok {
				t.Errorf("capabilities = %v, want tools advertised", caps)
			}
			info, _ := result["serverInfo"].(map[string]interface{})
			if info["name"] != "cartographer" || info["version"] != "1.2.3" {
				t.Errorf("serverInfo = %v, want this server's name and version", info)
			}
		})
	}
}

// The reduced capability map (WP1): "resources" was advertised with no
// resources/* method behind it and is gone; "skills" stays, because
// notifyWrap/artifactNotifyWrap really do emit its listChanged notification.
func TestCapabilities_OnlyWhatIsBacked(t *testing.T) {
	s := newEraServer(t)
	resps := runMCPSequence(t, s, []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
	})
	caps, _ := resultMap(t, resps[0])["capabilities"].(map[string]interface{})
	if _, ok := caps["resources"]; ok {
		t.Error("capabilities still advertise resources, which no handler backs")
	}
	if _, ok := caps["skills"]; !ok {
		t.Error("capabilities dropped skills, which notifyWrap does back")
	}
	if _, ok := caps["tools"]; !ok {
		t.Error("capabilities dropped tools")
	}
}

// The acceptance bar for the whole plan: a handshake-era request produces the
// exact bytes it produced before the new era existed — no resultType, no
// _meta, nothing added.
func TestHandshakeEra_ResponsesUnchanged(t *testing.T) {
	s := newEraServer(t)
	msgs := []string{
		`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"kb_status","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"ping","params":{}}`,
	}
	for i, resp := range runMCPSequence(t, s, msgs) {
		result := resultMap(t, resp)
		if _, ok := result["resultType"]; ok {
			t.Errorf("response %d carries resultType in the handshake era", i)
		}
		if _, ok := result["_meta"]; ok {
			t.Errorf("response %d carries _meta in the handshake era", i)
		}
		if _, ok := result["ttlMs"]; ok {
			t.Errorf("response %d carries ttlMs in the handshake era", i)
		}
	}
}

func TestNewEra_ResultEnvelope(t *testing.T) {
	s := newEraServer(t)
	msg := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"kb_status","arguments":{},` + metaNewEra + `}}`
	resps := runMCPSequence(t, s, []string{msg})
	if len(resps) != 1 || resps[0].Error != nil {
		t.Fatalf("tools/call: %+v", resps)
	}
	result := resultMap(t, resps[0])

	if result["resultType"] != "complete" {
		t.Errorf("resultType = %v, want \"complete\"", result["resultType"])
	}
	meta, _ := result["_meta"].(map[string]interface{})
	info, _ := meta[metaKeyServerInfo].(map[string]interface{})
	if info["name"] != "cartographer" || info["version"] != "1.2.3" {
		t.Errorf("_meta.%s = %v, want the server identity", metaKeyServerInfo, meta[metaKeyServerInfo])
	}
	// The tool's own payload survives the envelope.
	if _, ok := result["content"]; !ok {
		t.Errorf("result lost its content block: %v", result)
	}
}

func TestNewEra_ToolsListIsCacheable(t *testing.T) {
	s := newEraServer(t)
	msg := `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{` + metaNewEra + `}}`
	result := resultMap(t, runMCPSequence(t, s, []string{msg})[0])

	if ttl, ok := result["ttlMs"].(float64); !ok || ttl <= 0 {
		t.Errorf("ttlMs = %v, want a positive cache lifetime", result["ttlMs"])
	}
	if result["cacheScope"] != "private" {
		t.Errorf("cacheScope = %v, want \"private\" — the list depends on the caller", result["cacheScope"])
	}
}

func TestNewEra_UnsupportedVersion(t *testing.T) {
	s := newEraServer(t)
	msg := `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2030-01-01"}}}`
	resps := runMCPSequence(t, s, []string{msg})
	if len(resps) != 1 || resps[0].Error == nil {
		t.Fatalf("expected an error response, got %+v", resps)
	}
	if code := resps[0].Error.Code; code != ErrCodeUnsupportedProtocolVersion {
		t.Fatalf("error code = %d, want %d", code, ErrCodeUnsupportedProtocolVersion)
	}
	raw, _ := json.Marshal(resps[0].Error.Data)
	var data struct {
		Supported []string `json:"supported"`
	}
	json.Unmarshal(raw, &data)
	if len(data.Supported) != 2 || data.Supported[0] != ProtocolVersion20260728 {
		t.Errorf("error data = %s, want the supported version list", raw)
	}
}

// initialize pins its own era: a client that decorates the handshake with
// 2026-07-28 _meta still gets the handshake answer it knows how to read.
func TestInitialize_KeepsHandshakeShape(t *testing.T) {
	s := newEraServer(t)
	msg := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05",` + metaNewEra + `}}`
	result := resultMap(t, runMCPSequence(t, s, []string{msg})[0])
	if _, ok := result["resultType"]; ok {
		t.Errorf("initialize answered with the new envelope: %v", result)
	}
	if result["protocolVersion"] != "2024-11-05" {
		t.Errorf("protocolVersion = %v, want the version the client asked for", result["protocolVersion"])
	}
}

// A broken _meta is an error, not a silent downgrade to the handshake era.
func TestMalformedMeta_IsInvalidParams(t *testing.T) {
	s := newEraServer(t)
	msg := `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":42}}}`
	resps := runMCPSequence(t, s, []string{msg})
	if len(resps) != 1 || resps[0].Error == nil || resps[0].Error.Code != ErrCodeInvalidParams {
		t.Fatalf("expected -32602 for a malformed _meta, got %+v", resps)
	}
}

// --- HTTP transport (WP3) ---

// newEraPost builds a 2026-07-28-era POST with the mirror headers filled in,
// which each test then breaks one at a time.
func newEraPost(body string, headers map[string]string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return req
}

func serveHTTPReq(t *testing.T, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	s := newEraServer(t)
	rr := httptest.NewRecorder()
	s.HTTPHandler().ServeHTTP(rr, req)
	return rr
}

func decodeRPC(t *testing.T, rr *httptest.ResponseRecorder) Response {
	t.Helper()
	var resp Response
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response %q: %v", rr.Body.String(), err)
	}
	return resp
}

func TestHTTP_MirrorHeadersAgree(t *testing.T) {
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"kb_status","arguments":{},` + metaNewEra + `}}`
	rr := serveHTTPReq(t, newEraPost(body, map[string]string{
		"MCP-Protocol-Version": ProtocolVersion20260728,
		"Mcp-Method":           "tools/call",
		"Mcp-Name":             "kb_status",
	}))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
}

// Either marker alone identifies the era (D128 decision 2): a body with no
// _meta but the header present is a new-era request, and gets the new-era
// envelope back.
func TestHTTP_HeaderAloneSelectsNewEra(t *testing.T) {
	rr := serveHTTPReq(t, newEraPost(toolsListBody, map[string]string{
		"MCP-Protocol-Version": ProtocolVersion20260728,
		"Mcp-Method":           "tools/list",
	}))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	result := resultMap(t, decodeRPC(t, rr))
	if result["resultType"] != "complete" {
		t.Errorf("resultType = %v, want the new-era envelope", result["resultType"])
	}
}

func TestHTTP_MirrorHeaderMismatches(t *testing.T) {
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"kb_status","arguments":{},` + metaNewEra + `}}`
	base := map[string]string{
		"MCP-Protocol-Version": ProtocolVersion20260728,
		"Mcp-Method":           "tools/call",
		"Mcp-Name":             "kb_status",
	}

	for _, tc := range []struct {
		name   string
		mutate func(h map[string]string)
	}{
		{"version disagrees with _meta", func(h map[string]string) { h["MCP-Protocol-Version"] = SupportedProtocolVersion }},
		{"method disagrees with body", func(h map[string]string) { h["Mcp-Method"] = "tools/list" }},
		{"name disagrees with params", func(h map[string]string) { h["Mcp-Name"] = "concept_read" }},
		{"method header missing", func(h map[string]string) { delete(h, "Mcp-Method") }},
		{"name header missing", func(h map[string]string) { delete(h, "Mcp-Name") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			headers := map[string]string{}
			for k, v := range base {
				headers[k] = v
			}
			tc.mutate(headers)
			rr := serveHTTPReq(t, newEraPost(body, headers))
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", rr.Code, rr.Body.String())
			}
			if resp := decodeRPC(t, rr); resp.Error == nil || resp.Error.Code != ErrCodeHeaderMismatch {
				t.Fatalf("error = %+v, want %d", resp.Error, ErrCodeHeaderMismatch)
			}
		})
	}
}

// A body that declares the new era without the header that must mirror it is
// still a new-era request, and still a header mismatch.
func TestHTTP_MissingProtocolVersionHeader(t *testing.T) {
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{` + metaNewEra + `}}`
	rr := serveHTTPReq(t, newEraPost(body, map[string]string{"Mcp-Method": "tools/list"}))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
	if resp := decodeRPC(t, rr); resp.Error == nil || resp.Error.Code != ErrCodeHeaderMismatch {
		t.Fatalf("error = %+v, want %d", resp.Error, ErrCodeHeaderMismatch)
	}
}

// The same request without any era marker is a handshake-era request and is
// served as it always was — no header requirements apply to it.
func TestHTTP_HandshakeEraNeedsNoHeaders(t *testing.T) {
	rr := serveHTTPReq(t, newEraPost(toolsListBody, nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
}

// A version header naming anything other than 2026-07-28 leaves the request in
// the handshake era (D133). Sending MCP-Protocol-Version is conformant for a
// client on an earlier revision — the header predates this one — and such a
// client sends none of the mirror headers. Keying the era off the header's
// presence rather than its value rejected those clients with -32020.
func TestHTTP_OldVersionHeaderStaysHandshakeEra(t *testing.T) {
	for _, version := range []string{SupportedProtocolVersion, "2025-06-18", "2025-11-25"} {
		t.Run(version, func(t *testing.T) {
			rr := serveHTTPReq(t, newEraPost(toolsListBody, map[string]string{
				"MCP-Protocol-Version": version,
			}))
			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
			}
			if result := resultMap(t, decodeRPC(t, rr)); result["resultType"] != nil {
				t.Errorf("resultType = %v, want the handshake envelope (absent)", result["resultType"])
			}
		})
	}
}

func TestHTTP_McpNameBase64Sentinel(t *testing.T) {
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"kb_status","arguments":{},` + metaNewEra + `}}`
	encoded := "=?base64?" + base64.StdEncoding.EncodeToString([]byte("kb_status")) + "?="
	rr := serveHTTPReq(t, newEraPost(body, map[string]string{
		"MCP-Protocol-Version": ProtocolVersion20260728,
		"Mcp-Method":           "tools/call",
		"Mcp-Name":             encoded,
	}))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want the sentinel form decoded and accepted; body=%s", rr.Code, rr.Body.String())
	}
}

func TestHTTP_UnknownMethodStatusByEra(t *testing.T) {
	t.Run("new era is 404", func(t *testing.T) {
		body := `{"jsonrpc":"2.0","id":1,"method":"nope/nope","params":{` + metaNewEra + `}}`
		rr := serveHTTPReq(t, newEraPost(body, map[string]string{
			"MCP-Protocol-Version": ProtocolVersion20260728,
			"Mcp-Method":           "nope/nope",
		}))
		if rr.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404; body=%s", rr.Code, rr.Body.String())
		}
		if resp := decodeRPC(t, rr); resp.Error == nil || resp.Error.Code != ErrCodeMethodNotFound {
			t.Fatalf("error = %+v, want %d", resp.Error, ErrCodeMethodNotFound)
		}
	})

	// Handshake-era clients read any non-200 as a transport failure
	// (internal/client/client.go), so the status must stay 200 for them.
	t.Run("handshake era is 200", func(t *testing.T) {
		rr := serveHTTPReq(t, newEraPost(`{"jsonrpc":"2.0","id":1,"method":"nope/nope","params":{}}`, nil))
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
		}
		if resp := decodeRPC(t, rr); resp.Error == nil || resp.Error.Code != ErrCodeMethodNotFound {
			t.Fatalf("error = %+v, want %d", resp.Error, ErrCodeMethodNotFound)
		}
	})
}

func TestHTTP_UnsupportedVersionIs400(t *testing.T) {
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2030-01-01"}}}`
	rr := serveHTTPReq(t, newEraPost(body, map[string]string{
		"MCP-Protocol-Version": "2030-01-01",
		"Mcp-Method":           "tools/list",
	}))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
	if resp := decodeRPC(t, rr); resp.Error == nil || resp.Error.Code != ErrCodeUnsupportedProtocolVersion {
		t.Fatalf("error = %+v, want %d", resp.Error, ErrCodeUnsupportedProtocolVersion)
	}
}

func TestHTTP_GetAndDeleteNotAllowed(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		req := httptest.NewRequest(method, "/mcp", nil)
		rr := serveHTTPReq(t, req)
		if rr.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s /mcp: status = %d, want 405", method, rr.Code)
		}
	}
}

// Sessions are gone from the protocol and were never here: the headers a
// session-minded client sends are ignored, and nothing session-shaped comes
// back. This test exists so a future change cannot quietly reintroduce them.
func TestHTTP_SessionHeadersIgnored(t *testing.T) {
	rr := serveHTTPReq(t, newEraPost(toolsListBody, map[string]string{
		"Mcp-Session-Id": "some-session",
		"Last-Event-ID":  "42",
	}))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want the request served normally; body=%s", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("Mcp-Session-Id"); got != "" {
		t.Errorf("Mcp-Session-Id = %q, want no session header in the response", got)
	}
}
