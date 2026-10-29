package worker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOpenAI_RequestShape(t *testing.T) {
	var got map[string]any
	var hdr http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hdr = r.Header.Clone()
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path = %s", r.URL.Path)
		}
		got = decode(t, r)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":4}}`))
	}))
	defer srv.Close()
	m, _ := NewOpenAI(srv.URL+"/v1", canary, "model-x")
	_, err := m.Complete(context.Background(), Request{
		System: "be brief",
		Tools:  []ToolSpec{{Name: "search", Description: "d", InputSchema: []byte(`{"type":"object"}`)}},
		Messages: []Message{
			{Role: RoleUser, Text: "go"},
			{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "t1", Name: "search", Arguments: []byte(`{"q":"x"}`)}, {ID: "t2", Name: "search", Arguments: []byte(`{}`)}}},
			{Role: RoleUser, ToolResults: []ToolResult{{CallID: "t1", Text: "fine"}, {CallID: "t2", Text: "boom", IsError: true}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if hdr.Get("Authorization") != "Bearer "+canary {
		t.Errorf("auth = %q", hdr.Get("Authorization"))
	}
	msgs := got["messages"].([]any)
	if first := msgs[0].(map[string]any); first["role"] != "system" || first["content"] != "be brief" {
		t.Errorf("system = %v", first)
	}
	calls := msgs[2].(map[string]any)["tool_calls"].([]any)
	if fn := calls[0].(map[string]any)["function"].(map[string]any); fn["arguments"] != `{"q":"x"}` {
		t.Errorf("arguments must be a string: %v", fn)
	}
	t1, t2 := msgs[3].(map[string]any), msgs[4].(map[string]any)
	if t1["role"] != "tool" || t1["tool_call_id"] != "t1" || t1["content"] != "fine" || t2["tool_call_id"] != "t2" || t2["content"] != "ERROR: boom" {
		t.Errorf("tool messages = %v %v", t1, t2)
	}
	if tool := got["tools"].([]any)[0].(map[string]any); tool["type"] != "function" || tool["function"].(map[string]any)["name"] != "search" {
		t.Errorf("tool = %v", tool)
	}
}

func TestOpenAI_NoKeyNoAuthHeader(t *testing.T) {
	var hdr http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hdr = r.Header.Clone()
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}))
	defer srv.Close()
	m, _ := NewOpenAI(srv.URL, "", "m")
	if _, err := m.Complete(context.Background(), Request{}); err != nil {
		t.Fatal(err)
	}
	if _, ok := hdr["Authorization"]; ok {
		t.Errorf("Authorization sent without a key: %v", hdr)
	}
}

func TestOpenAI_BaseURLRequired(t *testing.T) {
	if _, err := NewOpenAI("", "k", "m"); err == nil {
		t.Fatal("empty base accepted")
	}
}

// Trap: some local servers answer finish_reason "stop" with tool calls present.
func TestOpenAI_StopWithToolCallsIsToolUse(t *testing.T) {
	srv := serve(t, 200, `{"choices":[{"message":{"content":null,"tool_calls":[{"id":"a","type":"function","function":{"name":"search","arguments":"not json"}}]},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`)
	defer srv.Close()
	m, _ := NewOpenAI(srv.URL, "", "m")
	resp, err := m.Complete(context.Background(), Request{})
	if err != nil || resp.Stop != StopToolUse || len(resp.ToolCalls) != 1 {
		t.Fatalf("%+v %v", resp, err)
	}
	// Arguments that are not JSON pass through: the loop refuses them.
	if string(resp.ToolCalls[0].Arguments) != "not json" {
		t.Errorf("arguments = %q", resp.ToolCalls[0].Arguments)
	}
}

// Trap: a local endpoint without usage must not make the budget unlimited.
func TestOpenAI_MissingUsageIsEstimated(t *testing.T) {
	for _, body := range []string{
		`{"choices":[{"message":{"content":"hello world!"},"finish_reason":"stop"}]}`,
		`{"choices":[{"message":{"content":"hello world!"},"finish_reason":"stop"}],"usage":{"prompt_tokens":0,"completion_tokens":0}}`,
	} {
		srv := serve(t, 200, body)
		m, _ := NewOpenAI(srv.URL, "", "m")
		resp, err := m.Complete(context.Background(), Request{Messages: []Message{{Role: RoleUser, Text: "hi"}}})
		srv.Close()
		if err != nil || !resp.Usage.Estimated || resp.Usage.InputTokens == 0 || resp.Usage.OutputTokens != estimateTokens(len(body)) {
			t.Errorf("%+v %v", resp.Usage, err)
		}
	}
}

func TestOpenAI_StopTable(t *testing.T) {
	for raw, want := range map[string]StopReason{
		"stop": StopEnd, "tool_calls": StopToolUse, "function_call": StopToolUse,
		"length": StopMaxTokens, "content_filter": StopOther, "": StopOther,
	} {
		srv := serve(t, 200, `{"choices":[{"message":{"content":"x"},"finish_reason":"`+raw+`"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`)
		m, _ := NewOpenAI(srv.URL, "", "m")
		resp, err := m.Complete(context.Background(), Request{})
		srv.Close()
		if err != nil || resp.Stop != want {
			t.Errorf("%q: %+v %v", raw, resp, err)
		}
	}
}
