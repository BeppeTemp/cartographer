package mcpserver

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// #1279: a message the server cannot take as a request is answered with a
// JSON-RPC error body and the code that names the failure, not plain text.
func TestMCPProtocolErrorsAreJSONRPC(t *testing.T) {
	t.Parallel()
	h := openHandler(newEraServer(t))
	for _, tc := range []struct {
		name, body string
		code       int
		id         string
	}{
		{"malformed JSON", `{bad`, ErrCodeParseError, "null"},
		{"empty body", ``, ErrCodeParseError, "null"},
		{"empty batch", `[]`, ErrCodeInvalidRequest, "null"},
		{"wrong version", `{"jsonrpc":"1.0","id":1,"method":"tools/list"}`, ErrCodeInvalidRequest, "null"},
		{"object id", `{"jsonrpc":"2.0","id":{"a":1},"method":"tools/list"}`, ErrCodeInvalidRequest, "null"},
		{"not an object", `"x"`, ErrCodeInvalidRequest, "null"},
		{"unknown method", `{"jsonrpc":"2.0","id":7,"method":"nope/x"}`, ErrCodeMethodNotFound, "7"},
	} {
		rr := postMCP(t, h, tc.body, nil)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", tc.name, rr.Code)
		}
		if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			t.Errorf("%s: content type %q, want JSON", tc.name, ct)
		}
		var resp struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      json.RawMessage `json:"id"`
			Error   *RPCError       `json:"error"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil || resp.Error == nil {
			t.Errorf("%s: body %q is not a JSON-RPC error", tc.name, rr.Body.String())
			continue
		}
		if resp.JSONRPC != "2.0" || resp.Error.Code != tc.code || string(resp.ID) != tc.id {
			t.Errorf("%s: got %s code %d id %s, want code %d id %s", tc.name, resp.JSONRPC, resp.Error.Code, resp.ID, tc.code, tc.id)
		}
	}
}

// A valid call is untouched by the rewriting.
func TestMCPValidRequestPassesThroughTheProtocolErrorWriter(t *testing.T) {
	t.Parallel()
	rr := postMCP(t, openHandler(newEraServer(t)), `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, nil)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"tools"`) {
		t.Fatalf("status %d body %.200s", rr.Code, rr.Body.String())
	}
}

// #1279: an argument the tool's schema does not declare is -32602 naming the
// key, so a typo such as if_mach cannot silently drop the check it asked for.
func TestMCPUnknownToolArgumentIsInvalidParams(t *testing.T) {
	t.Parallel()
	h := openHandler(newEraServer(t))
	rr := postMCP(t, h, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"concept_write","arguments":{"id":"x/y","if_mach":"abc"}}}`, nil)
	var resp struct {
		Error *RPCError `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil || resp.Error == nil {
		t.Fatalf("body %q is not a JSON-RPC error", rr.Body.String())
	}
	if resp.Error.Code != -32602 || !strings.Contains(resp.Error.Message, "if_mach") {
		t.Errorf("error = %+v, want -32602 naming if_mach", resp.Error)
	}
	ok := postMCP(t, h, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"work_list","arguments":{"limit":1}}}`, nil)
	if strings.Contains(ok.Body.String(), `"error":{`) {
		t.Errorf("a declared argument was refused: %.200s", ok.Body.String())
	}
}
