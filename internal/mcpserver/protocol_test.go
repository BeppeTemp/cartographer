package mcpserver

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Tests for the single protocol era this server speaks (D128 introduced it
// alongside the handshake era; D130 removed the other one). Everything here
// is about the shape of a 2026-07-28 request and the errors a client that
// does not send one receives.

const metaNewEra = `"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}`

func newEraServer(t *testing.T) *Server {
	t.Helper()
	k := setupTestKB(t)
	s := New("1.2.3")
	RegisterKBTools(s, k, Deps{})
	return s
}

// dispatchReady marks a hand-built Request as having already passed the
// transport-level protocol gate, the way resolveProtocol would have. Tests
// that call dispatch directly (rather than going through Run or the HTTP
// handler) build their requests in Go and would otherwise be rejected for
// naming no protocol version.
func dispatchReady(r *Request) *Request {
	r.protocolVersion = ProtocolVersion20260728
	r.metaResolved = true
	return r
}

// resultMap decodes a response's result into a generic map.// resultMap decodes a response's result into a generic map.
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

func TestDiscover(t *testing.T) {
	s := newEraServer(t)
	msg := `{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{` + metaNewEra + `}}`
	resps := runMCPSequenceRaw(t, s, []string{msg})
	if len(resps) != 1 || resps[0].Error != nil {
		t.Fatalf("server/discover: %+v", resps)
	}
	result := resultMap(t, resps[0])

	versions, _ := result["protocolVersions"].([]interface{})
	if len(versions) != 1 || versions[0] != ProtocolVersion20260728 {
		t.Errorf("protocolVersions = %v, want exactly %q", versions, ProtocolVersion20260728)
	}
	caps, _ := result["capabilities"].(map[string]interface{})
	if _, ok := caps["tools"]; !ok {
		t.Errorf("capabilities = %v, want tools advertised", caps)
	}
	info, _ := result["serverInfo"].(map[string]interface{})
	if info["name"] != "cartographer" || info["version"] != "1.2.3" {
		t.Errorf("serverInfo = %v, want this server's name and version", info)
	}
}

// The reduced capability map (D128/WP1): "resources" was advertised with no
// resources/* method behind it and is gone; "skills" stays, because
// notifyWrap/artifactNotifyWrap really do emit its listChanged notification.
func TestCapabilities_OnlyWhatIsBacked(t *testing.T) {
	s := newEraServer(t)
	msg := `{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{` + metaNewEra + `}}`
	caps, _ := resultMap(t, runMCPSequenceRaw(t, s, []string{msg})[0])["capabilities"].(map[string]interface{})
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

// The methods the handshake era owned are gone (D130): they answer like any
// other method this server does not implement.
func TestRetiredMethods_AreUnknown(t *testing.T) {
	s := newEraServer(t)
	for _, method := range []string{"initialize", "ping"} {
		t.Run(method, func(t *testing.T) {
			msg := `{"jsonrpc":"2.0","id":1,"method":"` + method + `","params":{` + metaNewEra + `}}`
			resps := runMCPSequenceRaw(t, s, []string{msg})
			if len(resps) != 1 || resps[0].Error == nil {
				t.Fatalf("%s: %+v, want an error", method, resps)
			}
			if code := resps[0].Error.Code; code != ErrCodeMethodNotFound {
				t.Errorf("%s: error code = %d, want %d", method, code, ErrCodeMethodNotFound)
			}
		})
	}
}

// Over stdio there are no headers, so a request that names no protocol
// version can only be diagnosed from the body — and the message has to name
// the field the client left out, because it is all its operator will see.
func TestStdio_MissingProtocolMeta(t *testing.T) {
	s := newEraServer(t)
	resps := runMCPSequenceRaw(t, s, []string{`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`})
	if len(resps) != 1 || resps[0].Error == nil {
		t.Fatalf("expected an error response, got %+v", resps)
	}
	if code := resps[0].Error.Code; code != ErrCodeInvalidParams {
		t.Fatalf("error code = %d, want %d", code, ErrCodeInvalidParams)
	}
	if !strings.Contains(resps[0].Error.Message, metaKeyProtocolVersion) {
		t.Errorf("error message %q does not name the missing field", resps[0].Error.Message)
	}
}

// notifications/initialized outlives the handshake that produced it: some
// clients still send it. A notification never gets a response, so it must be
// ignored rather than answered with an error.
func TestNotificationsInitialized_Ignored(t *testing.T) {
	s := newEraServer(t)
	msgs := []string{
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{` + metaNewEra + `}}`,
	}
	resps := runMCPSequenceRaw(t, s, msgs)
	if len(resps) != 1 {
		t.Fatalf("got %d responses, want exactly one (the notification must not produce any)", len(resps))
	}
	if resps[0].Error != nil {
		t.Fatalf("tools/list after the notification: %+v", resps[0].Error)
	}
}

func TestResultEnvelope(t *testing.T) {
	s := newEraServer(t)
	msg := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"kb_status","arguments":{},` + metaNewEra + `}}`
	resps := runMCPSequenceRaw(t, s, []string{msg})
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

func TestToolsListIsCacheable(t *testing.T) {
	s := newEraServer(t)
	msg := `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{` + metaNewEra + `}}`
	result := resultMap(t, runMCPSequenceRaw(t, s, []string{msg})[0])

	if ttl, ok := result["ttlMs"].(float64); !ok || ttl <= 0 {
		t.Errorf("ttlMs = %v, want a positive cache lifetime", result["ttlMs"])
	}
	if result["cacheScope"] != "private" {
		t.Errorf("cacheScope = %v, want \"private\" — the list depends on the caller", result["cacheScope"])
	}
}

func TestUnsupportedVersion(t *testing.T) {
	s := newEraServer(t)
	for _, version := range []string{"2030-01-01", "2024-11-05", "2025-11-25"} {
		t.Run(version, func(t *testing.T) {
			msg := `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"` + version + `"}}}`
			resps := runMCPSequenceRaw(t, s, []string{msg})
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
			if len(data.Supported) != 1 || data.Supported[0] != ProtocolVersion20260728 {
				t.Errorf("error data = %s, want the single supported version", raw)
			}
		})
	}
}

// A broken _meta is an error, not a silent fallback to some other shape.
func TestMalformedMeta_IsInvalidParams(t *testing.T) {
	s := newEraServer(t)
	msg := `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":42}}}`
	resps := runMCPSequenceRaw(t, s, []string{msg})
	if len(resps) != 1 || resps[0].Error == nil || resps[0].Error.Code != ErrCodeInvalidParams {
		t.Fatalf("expected -32602 for a malformed _meta, got %+v", resps)
	}
}

// --- HTTP transport ---

// newEraPost builds a POST with the mirror headers filled in, which each test
// then breaks one at a time.
func newEraPost(body string, headers map[string]string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return req
}

// newMCPPost builds a POST that satisfies the protocol gate: the body gets
// the required _meta if it has none, and the mirror headers are derived from
// the body they must agree with. Tests that exercise the gate itself use
// newEraPost and set (or omit) the headers by hand.
func newMCPPost(target, body string) *http.Request {
	decorated := withTestProtocolMeta(body)
	req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(decorated))
	req.Header.Set("Content-Type", "application/json")
	setMirrorHeaders(req, decorated)
	return req
}

// setMirrorHeaders fills in MCP-Protocol-Version, Mcp-Method and (for
// tools/call) Mcp-Name from the request body.
func setMirrorHeaders(req *http.Request, body string) {
	var envelope struct {
		Method string `json:"method"`
		Params struct {
			Name string `json:"name"`
		} `json:"params"`
	}
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		return
	}
	req.Header.Set("MCP-Protocol-Version", ProtocolVersion20260728)
	req.Header.Set("Mcp-Method", envelope.Method)
	if envelope.Method == "tools/call" && envelope.Params.Name != "" {
		req.Header.Set("Mcp-Name", envelope.Params.Name)
	}
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

// The header stands in for a body that names no version: the two only have to
// agree when both are present.
func TestHTTP_HeaderAloneIsEnough(t *testing.T) {
	rr := serveHTTPReq(t, newEraPost(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`, map[string]string{
		"MCP-Protocol-Version": ProtocolVersion20260728,
		"Mcp-Method":           "tools/list",
	}))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	result := resultMap(t, decodeRPC(t, rr))
	if result["resultType"] != "complete" {
		t.Errorf("resultType = %v, want the result envelope", result["resultType"])
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
		{"version disagrees with _meta", func(h map[string]string) { h["MCP-Protocol-Version"] = "2024-11-05" }},
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

// Over HTTP the missing header is the diagnosis, whether or not the body
// declares a version — this is the error every un-migrated client now hits,
// so it names the header it is missing (D130).
func TestHTTP_MissingProtocolVersionHeader(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{"body declares the version", `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{` + metaNewEra + `}}`},
		{"body declares nothing", `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rr := serveHTTPReq(t, newEraPost(tc.body, map[string]string{"Mcp-Method": "tools/list"}))
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", rr.Code, rr.Body.String())
			}
			resp := decodeRPC(t, rr)
			if resp.Error == nil || resp.Error.Code != ErrCodeHeaderMismatch {
				t.Fatalf("error = %+v, want %d", resp.Error, ErrCodeHeaderMismatch)
			}
			if !strings.Contains(resp.Error.Message, "MCP-Protocol-Version") {
				t.Errorf("error message %q does not name the missing header", resp.Error.Message)
			}
		})
	}
}

// A client on an earlier revision sends no mirror headers at all. It is
// rejected, and the rejection names what is missing rather than failing
// somewhere deeper.
func TestHTTP_UnmigratedClientIsRejected(t *testing.T) {
	rr := serveHTTPReq(t, newEraPost(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`, nil))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
	resp := decodeRPC(t, rr)
	if resp.Error == nil || resp.Error.Code != ErrCodeHeaderMismatch {
		t.Fatalf("error = %+v, want %d", resp.Error, ErrCodeHeaderMismatch)
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

// An unknown method is 404 unconditionally, which is the signal the spec's
// backward-compatibility section tells a client to read — and it is what an
// initialize now receives.
func TestHTTP_UnknownMethodIs404(t *testing.T) {
	for _, method := range []string{"nope/nope", "initialize", "ping"} {
		t.Run(method, func(t *testing.T) {
			body := `{"jsonrpc":"2.0","id":1,"method":"` + method + `","params":{` + metaNewEra + `}}`
			rr := serveHTTPReq(t, newEraPost(body, map[string]string{
				"MCP-Protocol-Version": ProtocolVersion20260728,
				"Mcp-Method":           method,
			}))
			if rr.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404; body=%s", rr.Code, rr.Body.String())
			}
			if resp := decodeRPC(t, rr); resp.Error == nil || resp.Error.Code != ErrCodeMethodNotFound {
				t.Fatalf("error = %+v, want %d", resp.Error, ErrCodeMethodNotFound)
			}
		})
	}
}

// A stray notifications/initialized over HTTP is accepted and does nothing.
func TestHTTP_NotificationsInitializedIs202(t *testing.T) {
	rr := serveHTTPReq(t, newEraPost(`{"jsonrpc":"2.0","method":"notifications/initialized"}`, nil))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", rr.Code, rr.Body.String())
	}
	if body := strings.TrimSpace(rr.Body.String()); body != "" {
		t.Errorf("body = %q, want no response to a notification", body)
	}
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
	rr := serveHTTPReq(t, newEraPost(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{`+metaNewEra+`}}`, map[string]string{
		"MCP-Protocol-Version": ProtocolVersion20260728,
		"Mcp-Method":           "tools/list",
		"Mcp-Session-Id":       "some-session",
		"Last-Event-ID":        "42",
	}))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want the request served normally; body=%s", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("Mcp-Session-Id"); got != "" {
		t.Errorf("Mcp-Session-Id = %q, want no session header in the response", got)
	}
}
