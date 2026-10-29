package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// DefaultAnthropicBase is the public Messages API host.
const DefaultAnthropicBase = "https://api.anthropic.com"

// Anthropic is a Model over the Anthropic Messages API (non-streaming).
type Anthropic struct{ t *transport }

// NewAnthropic builds the client; an empty baseURL means DefaultAnthropicBase.
// The key is never printed: String and GoString redact it.
func NewAnthropic(baseURL, apiKey, model string, opts ...Option) (*Anthropic, error) {
	if baseURL == "" {
		baseURL = DefaultAnthropicBase
	}
	t, err := newTransport(baseURL, apiKey, model, opts)
	if err != nil {
		return nil, err
	}
	return &Anthropic{t: t}, nil
}

// String redacts the key.
func (a Anthropic) String() string { return a.GoString() }

// GoString redacts the key.
func (a Anthropic) GoString() string {
	if a.t == nil {
		return "worker.Anthropic{}"
	}
	return fmt.Sprintf("worker.Anthropic{base: %q, model: %q, key: [redacted]}", a.t.baseURL, a.t.model)
}

type anthBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   string          `json:"content,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
}

type anthMessage struct {
	Role    string      `json:"role"`
	Content []anthBlock `json:"content"`
}

type anthTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type anthRequest struct {
	Model     string        `json:"model"`
	MaxTokens int           `json:"max_tokens"`
	System    string        `json:"system,omitempty"`
	Messages  []anthMessage `json:"messages"`
	Tools     []anthTool    `json:"tools,omitempty"`
}

type anthResponse struct {
	Content []struct {
		Type  string          `json:"type"`
		Text  string          `json:"text"`
		ID    string          `json:"id"`
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input"`
	} `json:"content"`
	StopReason string `json:"stop_reason"`
	Usage      struct {
		Input         int `json:"input_tokens"`
		CacheCreation int `json:"cache_creation_input_tokens"`
		CacheRead     int `json:"cache_read_input_tokens"`
		Output        int `json:"output_tokens"`
	} `json:"usage"`
}

// objectOr returns raw when it is a JSON object, else "{}": a provider rejects
// a tool_use whose input is not one, and the model's own bad arguments are
// already answered by the loop.
func objectOr(raw json.RawMessage) json.RawMessage {
	trimmed := strings.TrimSpace(string(raw))
	if strings.HasPrefix(trimmed, "{") && json.Valid(raw) {
		return raw
	}
	return json.RawMessage("{}")
}

func schemaOr(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || !json.Valid(raw) {
		return json.RawMessage(`{"type":"object"}`)
	}
	return raw
}

// Complete implements Model.
func (a *Anthropic) Complete(ctx context.Context, req Request) (Response, error) {
	wire := anthRequest{Model: a.t.model, MaxTokens: req.MaxOutputTokens, System: req.System}
	if wire.MaxTokens <= 0 {
		wire.MaxTokens = 4096
	}
	for _, m := range req.Messages {
		am := anthMessage{Role: m.Role}
		for _, r := range m.ToolResults {
			am.Content = append(am.Content, anthBlock{Type: "tool_result", ToolUseID: r.CallID, Content: r.Text, IsError: r.IsError})
		}
		if m.Text != "" {
			am.Content = append(am.Content, anthBlock{Type: "text", Text: m.Text})
		}
		for _, c := range m.ToolCalls {
			am.Content = append(am.Content, anthBlock{Type: "tool_use", ID: c.ID, Name: c.Name, Input: objectOr(c.Arguments)})
		}
		if len(am.Content) == 0 {
			continue // content: null is a final 400
		}
		wire.Messages = append(wire.Messages, am)
	}
	for _, ts := range req.Tools {
		wire.Tools = append(wire.Tools, anthTool{Name: ts.Name, Description: ts.Description, InputSchema: schemaOr(ts.InputSchema)})
	}
	body, err := json.Marshal(wire)
	if err != nil {
		return Response{}, fmt.Errorf("worker: encode model request: %w", err)
	}
	data, err := a.t.post(ctx, "/v1/messages", map[string]string{
		"x-api-key":         a.t.key,
		"anthropic-version": "2023-06-01",
	}, body)
	if err != nil {
		return Response{}, err
	}
	var resp anthResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return Response{}, fmt.Errorf("worker: model response is not the expected JSON: %w", err)
	}
	out := Response{RawStop: resp.StopReason}
	var text []string
	for _, b := range resp.Content {
		switch b.Type {
		case "text":
			text = append(text, b.Text)
		case "tool_use":
			out.ToolCalls = append(out.ToolCalls, ToolCall{ID: b.ID, Name: b.Name, Arguments: b.Input})
		}
	}
	out.Text = strings.Join(text, "")
	switch resp.StopReason {
	case "end_turn", "stop_sequence":
		out.Stop = StopEnd
	case "tool_use":
		out.Stop = StopToolUse
	case "max_tokens":
		out.Stop = StopMaxTokens
	default:
		out.Stop = StopOther
	}
	// Calls win over a stop reason that says the answer ended.
	if out.Stop == StopEnd && len(out.ToolCalls) > 0 {
		out.Stop = StopToolUse
	}
	out.Usage = Usage{
		InputTokens:  resp.Usage.Input + resp.Usage.CacheCreation + resp.Usage.CacheRead,
		OutputTokens: resp.Usage.Output,
	}
	if out.Usage.Total() == 0 {
		// A gateway that omits usage must not make the budget unlimited.
		out.Usage = Usage{InputTokens: estimateTokens(len(body)), OutputTokens: estimateTokens(len(data)), Estimated: true}
	}
	return out, nil
}
