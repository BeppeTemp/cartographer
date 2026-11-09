package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// OpenAI is a Model over OpenAI-compatible chat completions (non-streaming),
// the format local llama.cpp and LiteLLM endpoints implement.
type OpenAI struct{ t *transport }

// NewOpenAI builds the client. baseURL is required and includes any /v1; an
// empty apiKey sends no Authorization header (local servers take none).
func NewOpenAI(baseURL, apiKey, model string, opts ...Option) (*OpenAI, error) {
	if baseURL == "" {
		return nil, errors.New("worker: OpenAI-compatible endpoint needs a base URL")
	}
	t, err := newTransport(baseURL, apiKey, model, opts)
	if err != nil {
		return nil, err
	}
	return &OpenAI{t: t}, nil
}

// String redacts the key.
func (o OpenAI) String() string { return o.GoString() }

// GoString redacts the key.
func (o OpenAI) GoString() string {
	if o.t == nil {
		return "worker.OpenAI{}"
	}
	return fmt.Sprintf("worker.OpenAI{base: %q, model: %q, key: [redacted]}", o.t.baseURL, o.t.model)
}

type oaiCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type oaiMessage struct {
	Role       string    `json:"role"`
	Content    *string   `json:"content"`
	ToolCalls  []oaiCall `json:"tool_calls,omitempty"`
	ToolCallID string    `json:"tool_call_id,omitempty"`
}

type oaiTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"`
	} `json:"function"`
}

type oaiRequest struct {
	Model     string       `json:"model"`
	MaxTokens int          `json:"max_tokens"`
	Messages  []oaiMessage `json:"messages"`
	Tools     []oaiTool    `json:"tools,omitempty"`
}

type oaiResponse struct {
	Choices []struct {
		Message struct {
			Content   *string   `json:"content"`
			ToolCalls []oaiCall `json:"tool_calls"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		Prompt     int `json:"prompt_tokens"`
		Completion int `json:"completion_tokens"`
	} `json:"usage"`
}

func strp(s string) *string { return &s }

// Complete implements Model.
func (o *OpenAI) Complete(ctx context.Context, req Request) (Response, error) {
	wire := oaiRequest{Model: o.t.model, MaxTokens: req.MaxOutputTokens}
	if wire.MaxTokens <= 0 {
		wire.MaxTokens = 4096
	}
	if req.System != "" {
		wire.Messages = append(wire.Messages, oaiMessage{Role: "system", Content: strp(req.System)})
	}
	for _, m := range req.Messages {
		// Tool results first, in the order of the calls: the format wants them
		// right after the assistant message that made them.
		for _, r := range m.ToolResults {
			text := r.Text
			if r.IsError {
				text = "ERROR: " + text
			}
			wire.Messages = append(wire.Messages, oaiMessage{Role: "tool", ToolCallID: r.CallID, Content: strp(text)})
		}
		if m.Text == "" && len(m.ToolCalls) == 0 {
			continue
		}
		om := oaiMessage{Role: m.Role}
		if m.Text != "" {
			om.Content = strp(m.Text)
		}
		for _, c := range m.ToolCalls {
			oc := oaiCall{ID: c.ID, Type: "function"}
			oc.Function.Name = c.Name
			oc.Function.Arguments = string(c.Arguments)
			if strings.TrimSpace(oc.Function.Arguments) == "" {
				oc.Function.Arguments = "{}"
			}
			om.ToolCalls = append(om.ToolCalls, oc)
		}
		wire.Messages = append(wire.Messages, om)
	}
	for _, ts := range req.Tools {
		ot := oaiTool{Type: "function"}
		ot.Function.Name, ot.Function.Description, ot.Function.Parameters = ts.Name, ts.Description, schemaOr(ts.InputSchema)
		wire.Tools = append(wire.Tools, ot)
	}
	body, err := json.Marshal(wire)
	if err != nil {
		return Response{}, fmt.Errorf("worker: encode model request: %w", err)
	}
	headers := map[string]string{}
	if o.t.key != "" {
		headers["Authorization"] = "Bearer " + o.t.key
	}
	data, err := o.t.post(ctx, "/chat/completions", headers, body)
	if err != nil {
		return Response{}, err
	}
	var resp oaiResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return Response{}, fmt.Errorf("worker: model response is not the expected JSON: %w", err)
	}
	if len(resp.Choices) == 0 {
		return Response{}, errors.New("worker: model response has no choices")
	}
	ch := resp.Choices[0]
	out := Response{RawStop: ch.FinishReason}
	if ch.Message.Content != nil {
		out.Text = *ch.Message.Content
	}
	for _, c := range ch.Message.ToolCalls {
		out.ToolCalls = append(out.ToolCalls, ToolCall{ID: c.ID, Name: c.Function.Name, Arguments: json.RawMessage(c.Function.Arguments)})
	}
	switch ch.FinishReason {
	case "stop":
		out.Stop = StopEnd
	case "tool_calls", "function_call":
		out.Stop = StopToolUse
	case "length":
		out.Stop = StopMaxTokens
	default:
		out.Stop = StopOther
	}
	// Some local servers say "stop" with tool calls present: the calls win.
	if out.Stop == StopEnd && len(out.ToolCalls) > 0 {
		out.Stop = StopToolUse
	}
	out.Usage = Usage{InputTokens: resp.Usage.Prompt, OutputTokens: resp.Usage.Completion}
	if out.Usage.Total() == 0 {
		out.Usage = Usage{InputTokens: estimateTokens(len(body)), OutputTokens: estimateTokens(len(data)), Estimated: true}
	}
	return out, nil
}

// estimateTokens is ceil(bytes/4).
func estimateTokens(n int) int { return (n + 3) / 4 }
