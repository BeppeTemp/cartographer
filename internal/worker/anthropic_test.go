package worker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAnthropic_RequestShape(t *testing.T) {
	var got map[string]any
	var hdr http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hdr = r.Header.Clone()
		if r.URL.Path != "/v1/messages" {
			t.Errorf("path = %s", r.URL.Path)
		}
		got = decode(t, r)
		_, _ = w.Write([]byte(`{"content":[],"stop_reason":"end_turn","usage":{"input_tokens":1,"cache_creation_input_tokens":2,"cache_read_input_tokens":3,"output_tokens":4}}`))
	}))
	defer srv.Close()
	m, _ := NewAnthropic(srv.URL, canary, "model-x")
	resp, err := m.Complete(context.Background(), Request{
		System: "be brief", MaxOutputTokens: 77,
		Tools: []ToolSpec{{Name: "search", Description: "d", InputSchema: []byte(`{"type":"object","properties":{"q":{"type":"string"}}}`)}},
		Messages: []Message{
			{Role: RoleUser, Text: "go"},
			{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "t1", Name: "search", Arguments: []byte(`{"q":"x"}`)}}},
			{Role: RoleUser, ToolResults: []ToolResult{{CallID: "t1", Text: "boom", IsError: true}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if hdr.Get("x-api-key") != canary || hdr.Get("anthropic-version") != "2023-06-01" || hdr.Get("Content-Type") != "application/json" {
		t.Errorf("headers = %v", hdr)
	}
	system, _ := got["system"].([]any)
	if len(system) != 1 || got["max_tokens"] != float64(77) || got["model"] != "model-x" {
		t.Errorf("body = %v", got)
	}
	if block := system[0].(map[string]any); block["text"] != "be brief" || block["cache_control"] == nil {
		t.Errorf("system = %v", system)
	}
	tool := got["tools"].([]any)[0].(map[string]any)
	if tool["name"] != "search" || tool["input_schema"].(map[string]any)["type"] != "object" {
		t.Errorf("tool = %v", tool)
	}
	msgs := got["messages"].([]any)
	use := msgs[1].(map[string]any)["content"].([]any)[0].(map[string]any)
	if use["type"] != "tool_use" || use["id"] != "t1" || use["input"].(map[string]any)["q"] != "x" {
		t.Errorf("tool_use = %v", use)
	}
	res := msgs[2].(map[string]any)["content"].([]any)[0].(map[string]any)
	if res["type"] != "tool_result" || res["tool_use_id"] != "t1" || res["is_error"] != true || res["content"] != "boom" {
		t.Errorf("tool_result = %v", res)
	}
	if res["cache_control"] == nil {
		t.Errorf("no cache breakpoint on the last block: %v", res)
	}
	if use["cache_control"] != nil {
		t.Errorf("cache breakpoint before the last block: %v", use)
	}
	if resp.Usage.InputTokens != 6 || resp.Usage.OutputTokens != 4 {
		t.Errorf("usage = %+v (cache tokens count)", resp.Usage)
	}
}

func TestAnthropic_StopTable(t *testing.T) {
	for raw, want := range map[string]StopReason{
		"end_turn": StopEnd, "stop_sequence": StopEnd, "tool_use": StopToolUse,
		"max_tokens": StopMaxTokens, "refusal": StopOther, "pause_turn": StopOther, "weird": StopOther,
	} {
		srv := serve(t, 200, `{"content":[],"stop_reason":"`+raw+`","usage":{}}`)
		m, _ := NewAnthropic(srv.URL, "", "m")
		resp, err := m.Complete(context.Background(), Request{})
		srv.Close()
		if err != nil || resp.Stop != want || resp.RawStop != raw {
			t.Errorf("%s: %+v %v", raw, resp, err)
		}
	}
}

func TestAnthropic_DefaultBase(t *testing.T) {
	m, err := NewAnthropic("", "k", "m")
	if err != nil || m.t.baseURL != DefaultAnthropicBase {
		t.Fatalf("%v %v", m, err)
	}
}

func TestAnthropic_EmptyMessageSkipped(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = decode(t, r)
		_, _ = w.Write([]byte(`{"content":[],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer srv.Close()
	m, _ := NewAnthropic(srv.URL, "", "m")
	_, err := m.Complete(context.Background(), Request{Messages: []Message{{Role: RoleUser, Text: "a"}, {Role: RoleAssistant}}})
	if err != nil {
		t.Fatal(err)
	}
	if n := len(got["messages"].([]any)); n != 1 {
		t.Errorf("messages sent = %d, want the empty one skipped", n)
	}
}

func TestAnthropic_ToolUseWithEndTurnIsToolUse(t *testing.T) {
	for _, raw := range []string{"end_turn", "stop_sequence"} {
		srv := serve(t, 200, `{"content":[{"type":"tool_use","id":"a","name":"search","input":{}}],"stop_reason":"`+raw+`","usage":{"input_tokens":1,"output_tokens":1}}`)
		m, _ := NewAnthropic(srv.URL, "", "m")
		resp, err := m.Complete(context.Background(), Request{})
		srv.Close()
		if err != nil || resp.Stop != StopToolUse || len(resp.ToolCalls) != 1 {
			t.Errorf("%s: %+v %v", raw, resp, err)
		}
	}
}

func TestAnthropic_MissingUsageIsEstimated(t *testing.T) {
	body := `{"content":[{"type":"text","text":"hello world!"}],"stop_reason":"end_turn"}`
	srv := serve(t, 200, body)
	defer srv.Close()
	m, _ := NewAnthropic(srv.URL, "", "m")
	resp, err := m.Complete(context.Background(), Request{Messages: []Message{{Role: RoleUser, Text: "hi"}}})
	if err != nil || !resp.Usage.Estimated || resp.Usage.InputTokens == 0 || resp.Usage.OutputTokens != estimateTokens(len(body)) {
		t.Errorf("%+v %v", resp.Usage, err)
	}
}
