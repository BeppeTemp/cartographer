package mcpserver

import (
	"strings"
	"testing"
)

// #1303: a tools/call whose arguments are not an object, and a call to a tool
// the server does not have, are JSON-RPC -32602 naming the tool, never a Go type.
func TestToolsCall_ArgumentsAndNameAreInvalidParams(t *testing.T) {
	t.Parallel()
	k := setupTestKB(t)
	s := New("test")
	RegisterKBTools(s, k, Deps{})

	resps := runMCPSequence(t, s, []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"search","arguments":"str"}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"nonexist","arguments":{}}}`,
	})
	if len(resps) != 3 {
		t.Fatalf("responses = %d, want 3", len(resps))
	}
	if e := resps[1].Error; e == nil || e.Code != -32602 ||
		!strings.Contains(e.Message, "search") || !strings.Contains(e.Message, "must be an object") ||
		strings.Contains(e.Message, "Go value") || strings.Contains(e.Message, "struct") {
		t.Errorf("arguments \"str\": %+v", resps[1])
	}
	if e := resps[2].Error; e == nil || e.Code != -32602 || e.Message != "unknown tool nonexist" {
		t.Errorf("unknown tool: %+v", resps[2])
	}
}
