// Package mcpserver implements an MCP (Model Context Protocol) server over stdio,
// based on newline-delimited JSON-RPC 2.0, with no external dependencies.
package mcpserver

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// SupportedProtocolVersion is the handshake-era protocol version, the one
// initialize reports when the client names none. It is no longer the only
// version served — see SupportedProtocolVersions.
const SupportedProtocolVersion = "2024-11-05"

// ProtocolVersion20260728 is the revision that removed the initialize
// handshake, sessions and SSE resumability, and made server/discover and the
// mirrored HTTP headers mandatory.
const ProtocolVersion20260728 = "2026-07-28"

// SupportedProtocolVersions lists every version this server answers, newest
// first. Both eras are served at once (D128): the era is decided per request,
// so a client that still speaks the handshake gets byte-identical responses to
// the ones it got before 2026-07-28 existed.
var SupportedProtocolVersions = []string{ProtocolVersion20260728, SupportedProtocolVersion}

// protocolEra is which of the two protocol generations a single request
// belongs to. It is never stored: a stateless server decides it from the
// request in hand and forgets it with the response.
type protocolEra int

const (
	// eraHandshake is the initialize/ping generation, everything up to and
	// including 2025-11-25.
	eraHandshake protocolEra = iota
	// era20260728 is the 2026-07-28 revision.
	era20260728
)

// metaKeyProtocolVersion and friends are the reserved _meta keys the
// 2026-07-28 revision defines. They carry a slash, so they can never collide
// with an application key (the spec forbids the io.modelcontextprotocol
// prefix to everyone else).
const (
	metaKeyProtocolVersion = "io.modelcontextprotocol/protocolVersion"
	metaKeyServerInfo      = "io.modelcontextprotocol/serverInfo"
)

// Request represents an incoming JSON-RPC 2.0 message.
// id is json.RawMessage because it can be a string, number, or null (notifications).
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`

	// era and protocolVersion are derived from the request, never read off the
	// wire as fields — see resolveEra. eraResolved guards against dispatch
	// re-deriving what the HTTP layer already established from the headers.
	era             protocolEra
	protocolVersion string
	eraResolved     bool
}

// resolveEra decides which era this request belongs to and records the
// protocol version it names, per D128 decision (2): the request is
// 2026-07-28-era iff params._meta carries the protocol version, or the
// transport reported one (the MCP-Protocol-Version header over HTTP).
// headerVersion is "" for stdio and for an HTTP request without the header.
//
// A _meta block that is present but unparsable is an error rather than a
// silent downgrade to the handshake era: a client that gets _meta wrong should
// hear about it instead of being served a shape it did not ask for.
func (r *Request) resolveEra(headerVersion string) error {
	r.eraResolved = true
	r.era, r.protocolVersion = eraHandshake, ""

	var params struct {
		Meta map[string]json.RawMessage `json:"_meta"`
	}
	if len(r.Params) > 0 && !isJSONNull(r.Params) {
		if err := json.Unmarshal(r.Params, &params); err != nil {
			// params may legitimately not be an object (or not carry _meta at
			// all); only a malformed _meta is fatal, and that is checked below.
			params.Meta = nil
			if bodyHasMetaKey(r.Params) {
				return fmt.Errorf("malformed _meta: %w", err)
			}
		}
	}
	if raw, ok := params.Meta[metaKeyProtocolVersion]; ok {
		var v string
		if err := json.Unmarshal(raw, &v); err != nil {
			return fmt.Errorf("malformed _meta.%s: must be a string", metaKeyProtocolVersion)
		}
		r.era, r.protocolVersion = era20260728, v
	}
	if headerVersion != "" {
		r.era = era20260728
		if r.protocolVersion == "" {
			r.protocolVersion = headerVersion
		}
	}
	return nil
}

// bodyHasMetaKey reports whether the raw params mention a "_meta" key at all,
// used to tell "params is not an object" from "params carries a broken _meta".
func bodyHasMetaKey(params json.RawMessage) bool {
	return bytes.Contains(params, []byte(`"_meta"`))
}

func isJSONNull(raw json.RawMessage) bool {
	return string(bytes.TrimSpace(raw)) == "null"
}

// IsProtocolVersionSupported reports whether v is one of the versions this
// server serves.
func IsProtocolVersionSupported(v string) bool {
	for _, s := range SupportedProtocolVersions {
		if s == v {
			return true
		}
	}
	return false
}

// serverCapabilities is the capability map reported by both initialize and
// server/discover. It lists only what a handler actually backs: tools, and the
// skills list-changed notification that skill_install and artifact_write emit
// over stdio (notifyWrap/artifactNotifyWrap). "resources" used to be advertised
// here with no resources/* method behind it — a client acting on it got
// -32601, so it was removed with D128/WP1.
func serverCapabilities() map[string]interface{} {
	return map[string]interface{}{
		"tools":  map[string]interface{}{},
		"skills": map[string]interface{}{"listChanged": true},
	}
}

// isNotification returns true if the message is a notification (id absent or null).
func (r *Request) isNotification() bool {
	return len(r.ID) == 0 || string(r.ID) == "null"
}

// Response represents an outgoing JSON-RPC 2.0 message.
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
	ErrCodeParseError     = -32700
	ErrCodeInvalidRequest = -32600
	ErrCodeMethodNotFound = -32601
	ErrCodeInvalidParams  = -32602
	ErrCodeInternal       = -32603
)

// Error codes the 2026-07-28 revision adds (D128). They are only ever produced
// for a request that identified itself as that era.
const (
	// ErrCodeHeaderMismatch reports an HTTP header that contradicts the body it
	// mirrors, or a required mirror header that is missing.
	ErrCodeHeaderMismatch = -32020
	// ErrCodeUnsupportedProtocolVersion reports a protocol version this server
	// does not serve; its data carries the list of versions it does.
	ErrCodeUnsupportedProtocolVersion = -32022
)

// errorResponse builds a JSON-RPC error Response.
func errorResponse(id json.RawMessage, code int, msg string) Response {
	return Response{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &RPCError{Code: code, Message: msg},
	}
}

// errorResponseData builds a JSON-RPC error Response carrying a data payload.
func errorResponseData(id json.RawMessage, code int, msg string, data interface{}) Response {
	return Response{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &RPCError{Code: code, Message: msg, Data: data},
	}
}

// unsupportedVersionResponse is the -32022 answer, its data naming every
// version the server does serve so the client can pick one without guessing.
func unsupportedVersionResponse(id json.RawMessage, requested string) Response {
	return errorResponseData(id, ErrCodeUnsupportedProtocolVersion,
		"unsupported protocol version: "+requested,
		map[string]interface{}{"supported": SupportedProtocolVersions})
}

// withEra applies the 2026-07-28 result envelope to a response, and is the one
// place that does: every successful result of that era carries
// resultType:"complete" and a _meta block naming the server. A handshake-era
// response passes through untouched — that byte-identity is the acceptance bar
// for D128 — and so does an error, which has no result to wrap.
//
// The result is re-encoded through a generic map, so a handler can return a
// struct (ToolResult) or a map without knowing about eras. Fields tagged
// json:"-" (ToolResult.CommitSHA) drop out here exactly as they would at
// transport time; they are consumed before this point, by the audit pair in
// handleToolsCall.
func (r Response) withEra(era protocolEra, serverInfo map[string]interface{}) Response {
	if era != era20260728 || r.Error != nil || r.Result == nil {
		return r
	}
	raw, err := json.Marshal(r.Result)
	if err != nil {
		return errorResponse(r.ID, ErrCodeInternal, "encode result: "+err.Error())
	}
	var envelope map[string]interface{}
	if err := json.Unmarshal(raw, &envelope); err != nil || envelope == nil {
		// A non-object result cannot carry the envelope's fields. No handler
		// returns one today; keep the result rather than invent a wrapper.
		return r
	}
	envelope["resultType"] = "complete"
	meta, _ := envelope["_meta"].(map[string]interface{})
	if meta == nil {
		meta = map[string]interface{}{}
	}
	meta[metaKeyServerInfo] = serverInfo
	envelope["_meta"] = meta
	r.Result = envelope
	return r
}

// successResponse builds a JSON-RPC success Response.
func successResponse(id json.RawMessage, result interface{}) Response {
	return Response{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
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
	// audit completion event in handleToolsCall.
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
