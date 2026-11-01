// Package worker is the model client and the allowlisted agent loop a worker
// runs against a KB's MCP server (D9): library only, nothing reaches it from the binary yet.
//
// A worker is a headless MCP client of the public HTTP contract plus its own
// loop calling the model API directly: the Anthropic Messages API and
// OpenAI-compatible chat completions, over the standard library. Everything the
// model returns is untrusted, and the model API key never appears in an error,
// a log line or a printed struct.
package worker

import (
	"context"
	"encoding/json"
)

// Model is one non-streaming completion call against a model API.
type Model interface {
	Complete(ctx context.Context, req Request) (Response, error)
}

// Request is the whole conversation sent to the model.
type Request struct {
	System          string
	Messages        []Message
	Tools           []ToolSpec
	MaxOutputTokens int
}

// Message is one turn. An assistant message carries Text and ToolCalls, a user
// message Text or ToolResults.
type Message struct {
	Role        string // "user" or "assistant"
	Text        string
	ToolCalls   []ToolCall
	ToolResults []ToolResult
}

// Roles of a Message.
const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
)

// ToolCall is a tool use requested by the model.
type ToolCall struct {
	ID        string
	Name      string
	Arguments json.RawMessage
}

// ToolResult answers one ToolCall.
type ToolResult struct {
	CallID  string
	Text    string
	IsError bool
}

// ToolSpec is a tool offered to the model; InputSchema is the server's own
// JSON schema, passed through untouched.
type ToolSpec struct {
	Name        string
	Description string
	InputSchema json.RawMessage
}

// Response is the model's answer to one Request.
type Response struct {
	Text      string
	ToolCalls []ToolCall
	Stop      StopReason
	// RawStop is the provider's own stop value, kept for logs.
	RawStop string
	Usage   Usage
}

// StopReason is the provider-neutral reason a completion ended.
type StopReason int

// Normalized stop reasons.
const (
	StopEnd       StopReason = iota // natural end of the answer
	StopToolUse                     // the model asks for tool calls
	StopMaxTokens                   // output truncated
	StopOther                       // refusal, content filter, pause, anything unknown
)

func (s StopReason) String() string {
	switch s {
	case StopEnd:
		return "end"
	case StopToolUse:
		return "tool_use"
	case StopMaxTokens:
		return "max_tokens"
	default:
		return "other"
	}
}

// Usage is what one call cost. Estimated is set when the provider reported
// nothing and the count is bytes/4: a local endpoint without usage must not
// make a token budget unlimited.
type Usage struct {
	InputTokens  int
	OutputTokens int
	Estimated    bool
}

// Total is the sum a daily budget adds up.
func (u Usage) Total() int { return u.InputTokens + u.OutputTokens }
