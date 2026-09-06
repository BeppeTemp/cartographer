package mcpserver

import "encoding/json"

// This file used to carry Cartographer's own MCP wire implementation: the
// JSON-RPC envelope, the two protocol eras and the rules for telling them
// apart, header mirroring, version negotiation and the cacheable-result
// fields. All of that now comes from the official SDK (D168), which serves
// every revision from 2024-11-05 to 2026-07-28 and decides per request, so
// keeping a second interpretation of the spec here would only be one more
// thing to keep in step with it.
//
// What remains is what does not belong to the wire: the result shape tools
// return, and the small JSON-RPC vocabulary the pieces of the HTTP stack that
// sit *outside* the SDK still need — the origin guard, which must refuse a
// request before it ever reaches a session.

// ProtocolVersion20260728 is the revision that replaced the initialize
// handshake with per-request metadata. Cartographer no longer implements it —
// the SDK does — but the roster still labels a row by the era its protocol
// version belongs to, which is the evidence for retiring the older one.
const ProtocolVersion20260728 = "2026-07-28"

// protocolEra labels a client by the generation of the protocol it speaks.
type protocolEra int

const (
	// eraHandshake is the initialize/ping generation, everything up to and
	// including 2025-11-25.
	eraHandshake protocolEra = iota
	// era20260728 is the 2026-07-28 revision.
	era20260728
)

// String returns the human-readable era label: "handshake" for the
// initialize/ping generation, "2026-07-28" for the 2026-07-28 revision.
// The zero value (handshake) is the default.
func (e protocolEra) String() string {
	switch e {
	case era20260728:
		return "2026-07-28"
	default:
		return "handshake"
	}
}

// Response is a JSON-RPC 2.0 response. The SDK owns the wire format; this
// declaration is what the origin guard writes and what tests decode.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  interface{}     `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// RPCError represents the JSON-RPC 2.0 error object.
type RPCError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

// Standard JSON-RPC 2.0 error codes.
const (
	ErrCodeInvalidRequest = -32600
	ErrCodeMethodNotFound = -32601
)

// errorResponse builds a JSON-RPC error Response.
func errorResponse(id json.RawMessage, code int, msg string) Response {
	return Response{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &RPCError{Code: code, Message: msg},
	}
}

// ContentBlock is an MCP content block (type=text).
type ContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// ToolResult is the result of an MCP tools/call.
type ToolResult struct {
	Content []ContentBlock `json:"content"`
	IsError bool           `json:"isError,omitempty"`
	// CommitSHA, if set by gitWrap after a successful write commit, is the
	// resulting commit's SHA (captured under the per-KB git lock, never a
	// later racy HEAD query — D119). It is internal call metadata only:
	// excluded from the wire format (json:"-") and consumed solely by the
	// audit completion event in callTool.
	CommitSHA string `json:"-"`
}

// textResult builds a ToolResult with a single text block.
func textResult(text string) ToolResult {
	return ToolResult{
		Content: []ContentBlock{{Type: "text", Text: text}},
	}
}

// errorResult builds a ToolResult that signals an application-level error.
func errorResult(msg string) ToolResult {
	return ToolResult{
		Content: []ContentBlock{{Type: "text", Text: msg}},
		IsError: true,
	}
}
