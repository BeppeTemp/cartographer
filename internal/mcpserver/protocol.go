// Package mcpserver implements an MCP (Model Context Protocol) server over stdio,
// based on newline-delimited JSON-RPC 2.0, with no external dependencies.
package mcpserver

import (
	"encoding/json"
)

// SupportedProtocolVersion is the MCP protocol version supported by this server.
const SupportedProtocolVersion = "2024-11-05"

// Request represents an incoming JSON-RPC 2.0 message.
// id is json.RawMessage because it can be a string, number, or null (notifications).
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
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
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Standard JSON-RPC 2.0 error codes.
const (
	ErrCodeParseError     = -32700
	ErrCodeInvalidRequest = -32600
	ErrCodeMethodNotFound = -32601
	ErrCodeInvalidParams  = -32602
	ErrCodeInternal       = -32603
)

// errorResponse builds a JSON-RPC error Response.
func errorResponse(id json.RawMessage, code int, msg string) Response {
	return Response{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &RPCError{Code: code, Message: msg},
	}
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
