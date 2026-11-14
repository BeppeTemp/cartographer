package surveyor_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/surveyor"
	"github.com/BeppeTemp/cartographer/internal/surveyor/surveyortest"
)

func tc(id, name, args string) surveyor.ToolCall {
	return surveyor.ToolCall{ID: id, Name: name, Arguments: json.RawMessage(args)}
}

func use(calls ...surveyor.ToolCall) surveyor.Response {
	return surveyor.Response{ToolCalls: calls, Stop: surveyor.StopToolUse, Usage: surveyor.Usage{InputTokens: 10, OutputTokens: 5}}
}

func end(text string) surveyor.Response {
	return surveyor.Response{Text: text, Stop: surveyor.StopEnd, Usage: surveyor.Usage{InputTokens: 10, OutputTokens: 5}}
}

func run(t *testing.T, f *surveyortest.Fake, tools *surveyortest.FakeTools, lim surveyor.Limits) (surveyor.Outcome, error) {
	t.Helper()
	return surveyor.NewAgent(f, tools, nil, lim).Run(context.Background(), surveyor.Task{System: "sys", User: "go"})
}

func standardTools() *surveyortest.FakeTools {
	return &surveyortest.FakeTools{Tools: surveyortest.Listing("search", "concept_read", "concept_write", "concept_delete", "artifact_write")}
}

func TestRun_DoneInOneStep(t *testing.T) {
	f := &surveyortest.Fake{Turns: []surveyor.Response{end("all done")}}
	out, err := run(t, f, standardTools(), surveyor.Limits{})
	if err != nil || out.Reason != surveyor.ReasonDone || out.Final != "all done" || out.Steps != 1 || out.Usage.Total() != 15 {
		t.Fatalf("%+v %v", out, err)
	}
	req := f.Requests[0]
	if req.System != "sys" || req.MaxOutputTokens != surveyor.DefaultMaxOutputTokens || req.Messages[0].Text != "go" {
		t.Errorf("request = %+v", req)
	}
	// The denied tools are absent from what the model was offered.
	for _, s := range req.Tools {
		if s.Name == "concept_delete" || s.Name == "artifact_write" {
			t.Errorf("%s offered to the model", s.Name)
		}
	}
	if len(req.Tools) != 3 {
		t.Errorf("offered %d tools, want 3", len(req.Tools))
	}
}

func TestRun_ReadThenWriteInOrder(t *testing.T) {
	f := &surveyortest.Fake{Turns: []surveyor.Response{use(tc("a", "concept_read", `{"id":"x"}`), tc("b", "concept_write", `{"id":"x"}`)), end("ok")}}
	tools := standardTools()
	out, err := run(t, f, tools, surveyor.Limits{})
	if err != nil || out.ToolCalls != 2 || out.Writes != 1 || out.Refused != 0 {
		t.Fatalf("%+v %v", out, err)
	}
	if strings.Join(tools.Order, ",") != "concept_read,concept_write" {
		t.Errorf("order = %v", tools.Order)
	}
	if len(out.Calls) != 2 || out.Calls[1].Tool != "concept_write" {
		t.Errorf("calls = %+v", out.Calls)
	}
	// One user message carries both results, after the assistant's calls.
	h := f.Requests[1].Messages
	if len(h) != 3 || len(h[1].ToolCalls) != 2 || len(h[2].ToolResults) != 2 || h[2].ToolResults[1].CallID != "b" {
		t.Errorf("history = %+v", h)
	}
}

func TestRun_DeniedAndUnofferedCallsAreRefusedLocally(t *testing.T) {
	f := &surveyortest.Fake{Turns: []surveyor.Response{
		use(tc("a", "concept_delete", `{}`), tc("b", "artifact_write", `{}`), tc("c", "lint", `{}`), tc("d", "nonsense", `{}`)),
		end("ok"),
	}}
	tools := standardTools() // lint is allowed in general but this token does not list it
	out, err := run(t, f, tools, surveyor.Limits{})
	if err != nil || out.Refused != 4 || out.ToolCalls != 0 {
		t.Fatalf("%+v %v", out, err)
	}
	if len(tools.Calls) != 0 {
		t.Errorf("a refused call reached the server: %v", tools.Calls)
	}
	for _, r := range f.Requests[1].Messages[2].ToolResults {
		if !r.IsError || !strings.Contains(r.Text, "not available") {
			t.Errorf("the model did not see the refusal: %+v", r)
		}
	}
}

func TestRun_ToolErrorIsFedBackAndRunContinues(t *testing.T) {
	tools := standardTools()
	tools.Outputs = map[string]client.ToolOutput{"concept_write": {IsError: true, Text: "stale write: re-read"}}
	f := &surveyortest.Fake{Turns: []surveyor.Response{use(tc("a", "concept_write", `{}`)), end("ok")}}
	out, err := run(t, f, tools, surveyor.Limits{})
	r := f.Requests[1].Messages[2].ToolResults[0]
	if err != nil || out.Reason != surveyor.ReasonDone || !r.IsError || r.Text != "stale write: re-read" || !out.Calls[0].IsError {
		t.Fatalf("%+v %v %+v", out, err, r)
	}
}

func TestRun_TransportErrorAborts(t *testing.T) {
	tools := standardTools()
	boom := &client.RemoteError{Code: client.CodeUnreachable, Message: "down"}
	tools.Errors = map[string]error{"search": boom}
	f := &surveyortest.Fake{Turns: []surveyor.Response{use(tc("a", "search", `{}`)), end("never")}}
	out, err := run(t, f, tools, surveyor.Limits{})
	if !errors.Is(err, boom) || out.Reason != surveyor.ReasonAborted || len(f.Requests) != 1 {
		t.Fatalf("%+v %v", out, err)
	}
	if out.ToolCalls != 1 || len(out.Calls) != 1 || !out.Calls[0].Uncertain {
		t.Errorf("the attempted call is not recorded: %+v", out)
	}
	// A write that failed in transport still counts as a write.
	tools = standardTools()
	tools.Errors = map[string]error{"concept_write": boom}
	f = &surveyortest.Fake{Turns: []surveyor.Response{use(tc("a", "concept_write", `{}`))}}
	if out, _ = run(t, f, tools, surveyor.Limits{}); out.Writes != 1 {
		t.Errorf("writes = %d, want 1", out.Writes)
	}
}

// Pinned: a typo in an argument (-32602) must not cost the run.
func TestRun_InvalidParamsIsFedBack(t *testing.T) {
	tools := standardTools()
	tools.Errors = map[string]error{"search": &client.RemoteError{Code: client.CodeMCPFailed, RPCCode: -32602, Message: "invalid params: unknown argument(s) qq for tool search"}}
	f := &surveyortest.Fake{Turns: []surveyor.Response{use(tc("a", "search", `{"qq":1}`)), end("ok")}}
	out, err := run(t, f, tools, surveyor.Limits{})
	r := f.Requests[1].Messages[2].ToolResults[0]
	if err != nil || out.Reason != surveyor.ReasonDone || !r.IsError || !strings.Contains(r.Text, "unknown argument") {
		t.Fatalf("%+v %v %+v", out, err, r)
	}
}

func TestRun_MalformedArguments(t *testing.T) {
	tools := standardTools()
	f := &surveyortest.Fake{Turns: []surveyor.Response{use(tc("a", "search", `not json`), tc("b", "search", `[1]`), tc("c", "search", ``)), end("ok")}}
	out, err := run(t, f, tools, surveyor.Limits{})
	res := f.Requests[1].Messages[2].ToolResults
	if err != nil || !res[0].IsError || !res[1].IsError || res[2].IsError || tools.Calls["search"] != 1 || string(tools.Args["search"]) != "{}" {
		t.Fatalf("%+v %v %+v calls=%v", out, err, res, tools.Calls)
	}
}

func TestRun_StepCap(t *testing.T) {
	turns := []surveyor.Response{use(tc("a", "search", `{}`)), use(tc("b", "search", `{}`)), use(tc("c", "search", `{}`))}
	f := &surveyortest.Fake{Turns: turns}
	out, err := run(t, f, standardTools(), surveyor.Limits{MaxSteps: 2})
	if err != nil || out.Reason != surveyor.ReasonMaxSteps || out.Steps != 2 || len(f.Requests) != 2 {
		t.Fatalf("%+v %v", out, err)
	}
}

func TestRun_TokenCapBeforeCallAndAfterToolUse(t *testing.T) {
	// After a StopToolUse that crosses the cap: no tool executes.
	tools := standardTools()
	f := &surveyortest.Fake{Turns: []surveyor.Response{use(tc("a", "concept_write", `{}`))}}
	out, err := run(t, f, tools, surveyor.Limits{MaxTotalTokens: 15})
	if err != nil || out.Reason != surveyor.ReasonBudget || len(tools.Calls) != 0 || out.Usage.Total() != 15 {
		t.Fatalf("after: %+v %v %v", out, err, tools.Calls)
	}
	// Under the cap after the first turn: the tool runs and the loop continues.
	tools = standardTools()
	f = &surveyortest.Fake{Turns: []surveyor.Response{use(tc("a", "search", `{}`)), end("done")}}
	out, err = run(t, f, tools, surveyor.Limits{MaxTotalTokens: 16})
	if err != nil || out.Reason != surveyor.ReasonDone || tools.Calls["search"] != 1 || len(f.Requests) != 2 {
		t.Fatalf("under: %+v %v", out, err)
	}
	// Cap reached by a StopEnd-less turn that asked for nothing more: the next
	// turn is never requested.
	f = &surveyortest.Fake{Turns: []surveyor.Response{use(tc("a", "search", `{}`)), use(tc("b", "search", `{}`)), end("no")}}
	out, err = run(t, f, standardTools(), surveyor.Limits{MaxTotalTokens: 20})
	if err != nil || out.Reason != surveyor.ReasonBudget || len(f.Requests) != 2 {
		t.Fatalf("before: %+v %v requests=%d", out, err, len(f.Requests))
	}
}

func TestRun_TruncatedAndOther(t *testing.T) {
	half := surveyor.Response{Text: "half", Stop: surveyor.StopMaxTokens}
	f := &surveyortest.Fake{Turns: []surveyor.Response{half, half}}
	if out, _ := run(t, f, standardTools(), surveyor.Limits{}); out.Reason != surveyor.ReasonTruncated {
		t.Errorf("%+v", out)
	}
	f = &surveyortest.Fake{Turns: []surveyor.Response{{Stop: surveyor.StopOther, RawStop: "refusal"}}}
	if out, _ := run(t, f, standardTools(), surveyor.Limits{}); out.Reason != surveyor.ReasonModelStopped || out.RawStop != "refusal" {
		t.Errorf("%+v", out)
	}
	// StopToolUse with no calls is treated as StopOther.
	f = &surveyortest.Fake{Turns: []surveyor.Response{{Stop: surveyor.StopToolUse}}}
	if out, _ := run(t, f, standardTools(), surveyor.Limits{}); out.Reason != surveyor.ReasonModelStopped {
		t.Errorf("%+v", out)
	}
}

func TestRun_EmptyOfferedSetErrorsBeforeTheModel(t *testing.T) {
	f := &surveyortest.Fake{Turns: []surveyor.Response{end("x")}}
	out, err := run(t, f, &surveyortest.FakeTools{Tools: surveyortest.Listing("concept_delete", "unknown")}, surveyor.Limits{})
	if err == nil || len(f.Requests) != 0 || out.Reason != surveyor.ReasonAborted {
		t.Fatalf("err=%v requests=%d reason=%q", err, len(f.Requests), out.Reason)
	}
}

func TestRun_ContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f := &surveyortest.Fake{Turns: []surveyor.Response{end("x")}}
	out, err := surveyor.NewAgent(f, standardTools(), nil, surveyor.Limits{}).Run(ctx, surveyor.Task{})
	if err != nil || out.Reason != surveyor.ReasonCanceled || len(f.Requests) != 0 {
		t.Fatalf("%+v %v", out, err)
	}
}

// A cancelled surveyor stops before the next call of the same turn.
func TestRun_CancelBetweenCallsOfOneTurn(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	tools := &cancelTools{FakeTools: *standardTools(), cancel: cancel}
	f := &surveyortest.Fake{Turns: []surveyor.Response{use(tc("a", "search", `{}`), tc("b", "concept_write", `{}`))}}
	out, err := surveyor.NewAgent(f, tools, nil, surveyor.Limits{}).Run(ctx, surveyor.Task{})
	if err != nil || out.Reason != surveyor.ReasonCanceled || tools.Calls["concept_write"] != 0 || out.ToolCalls != 1 {
		t.Fatalf("%+v %v %v", out, err, tools.Calls)
	}
}

// cancelTools cancels the context once its first call is made.
type cancelTools struct {
	surveyortest.FakeTools
	cancel context.CancelFunc
}

func (c *cancelTools) CallTool(name string, args any) (client.ToolOutput, error) {
	defer c.cancel()
	return c.FakeTools.CallTool(name, args)
}

func TestRun_OversizedResultTruncatedOnRuneBoundary(t *testing.T) {
	tools := standardTools()
	tools.Outputs = map[string]client.ToolOutput{"search": {Text: strings.Repeat("é", 100)}} // 200 bytes
	f := &surveyortest.Fake{Turns: []surveyor.Response{use(tc("a", "search", `{}`)), end("ok")}}
	_, err := run(t, f, tools, surveyor.Limits{MaxToolResultBytes: 11}) // 11 falls inside a rune
	got := f.Requests[1].Messages[2].ToolResults[0].Text
	if err != nil || !strings.HasPrefix(got, strings.Repeat("é", 5)+"\n[truncated 190 bytes]") {
		t.Fatalf("%q %v", got, err)
	}
}

func TestRun_EventsCarryNoContent(t *testing.T) {
	var events []surveyor.Event
	f := &surveyortest.Fake{Turns: []surveyor.Response{use(tc("a", "search", `{"q":"secret-arg"}`)), end("ok")}}
	a := surveyor.NewAgent(f, standardTools(), nil, surveyor.Limits{})
	a.OnEvent = func(e surveyor.Event) { events = append(events, e) }
	if _, err := a.Run(context.Background(), surveyor.Task{User: "secret-prompt"}); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(events)
	if len(events) != 3 || strings.Contains(string(b), "secret") {
		t.Errorf("events = %s", b)
	}
}

// End to end: the real MCP client against an httptest MCP server and the
// Anthropic client against an httptest model server.
func TestRun_EndToEnd(t *testing.T) {
	var deleted atomic.Int32
	mcp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			_, _ = w.Write([]byte(`{"status":"ok","version":"test"}`))
			return
		}
		var req struct {
			Method string `json:"method"`
			Params struct {
				Name string `json:"name"`
			} `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		var result any
		switch req.Method {
		case "tools/list":
			result = map[string]any{"tools": []map[string]any{
				{"name": "search", "description": "s", "inputSchema": map[string]any{"type": "object"}},
				{"name": "concept_delete", "description": "d", "inputSchema": map[string]any{"type": "object"}},
			}}
		default:
			if req.Params.Name == "concept_delete" {
				deleted.Add(1)
			}
			result = map[string]any{"content": []map[string]any{{"type": "text", "text": "found 3"}}}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": result})
	}))
	defer mcp.Close()

	var turn atomic.Int32
	var lastReq map[string]any
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&lastReq)
		if turn.Add(1) == 1 {
			_, _ = w.Write([]byte(`{"content":[{"type":"tool_use","id":"t1","name":"concept_delete","input":{}},{"type":"tool_use","id":"t2","name":"search","input":{"q":"x"}}],"stop_reason":"tool_use","usage":{"input_tokens":20,"output_tokens":5}}`))
			return
		}
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"finished"}],"stop_reason":"end_turn","usage":{"input_tokens":30,"output_tokens":5}}`))
	}))
	defer model.Close()

	m, err := surveyor.NewAnthropic(model.URL, "sk-test", "m")
	if err != nil {
		t.Fatal(err)
	}
	out, err := surveyor.NewAgent(m, client.New(mcp.URL, "tok"), nil, surveyor.Limits{}).Run(context.Background(), surveyor.Task{System: "s", User: "u"})
	if err != nil || out.Reason != surveyor.ReasonDone || out.Final != "finished" || out.Refused != 1 || out.ToolCalls != 1 || out.Usage.Total() != 60 {
		t.Fatalf("%+v %v", out, err)
	}
	if deleted.Load() != 0 {
		t.Error("concept_delete reached the MCP server")
	}
	tools := lastReq["tools"].([]any)
	if len(tools) != 1 || tools[0].(map[string]any)["name"] != "search" {
		t.Errorf("model was offered %v", tools)
	}
}

func TestRun_BatchDeleteIsRefusedLocally(t *testing.T) {
	f := &surveyortest.Fake{Turns: []surveyor.Response{
		use(tc("a", "concept_batch", `{"operations":[{"op":"delete","id":"x"}]}`)), end("ok"),
	}}
	tools := &surveyortest.FakeTools{Tools: surveyortest.Listing("search", "concept_batch")} // offered and allowed by name
	out, err := run(t, f, tools, surveyor.Limits{})
	if err != nil || out.Refused != 1 || tools.Calls["concept_batch"] != 0 {
		t.Fatalf("%+v %v calls=%v", out, err, tools.Calls)
	}
}

func TestRun_ClosingNoticeOnce(t *testing.T) {
	big := use(tc("1", "search", `{}`))
	big.Usage = surveyor.Usage{InputTokens: 800, OutputTokens: 10}
	f := &surveyortest.Fake{Turns: []surveyor.Response{big, big, end("closed")}}
	out, err := run(t, f, standardTools(), surveyor.Limits{MaxContextTokens: 1000})
	if err != nil || out.Reason != surveyor.ReasonDone {
		t.Fatalf("%+v %v", out, err)
	}
	notices := 0
	for _, m := range f.Requests[2].Messages {
		if strings.Contains(m.Text, "close to the limit") {
			notices++
			if len(m.ToolResults) != 1 {
				t.Errorf("notice apart from its results: %+v", m)
			}
		}
	}
	if notices != 1 || out.MaxPromptTokens != 800 {
		t.Errorf("notices = %d, outcome %+v", notices, out)
	}
}

func TestRun_StopsBeforeAPromptOverTheWindow(t *testing.T) {
	big := use(tc("1", "search", `{}`))
	big.Usage = surveyor.Usage{InputTokens: 990, OutputTokens: 20}
	f := &surveyortest.Fake{Turns: []surveyor.Response{big}}
	out, err := run(t, f, standardTools(), surveyor.Limits{MaxContextTokens: 1000})
	if err != nil || out.Reason != surveyor.ReasonBudget || out.Steps != 1 {
		t.Errorf("%+v %v", out, err)
	}
}

func TestRun_TruncatedTurnRetriesOnce(t *testing.T) {
	cut := use(tc("1", "concept_write", `{"incompl`))
	cut.Stop = surveyor.StopMaxTokens
	tools := standardTools()
	f := &surveyortest.Fake{Turns: []surveyor.Response{cut, end("done")}}
	out, err := run(t, f, tools, surveyor.Limits{})
	if err != nil || out.Reason != surveyor.ReasonDone || tools.Calls["concept_write"] != 0 {
		t.Fatalf("%+v %v, calls %v", out, err, tools.Calls)
	}
	f = &surveyortest.Fake{Turns: []surveyor.Response{cut, cut}}
	if out, err = run(t, f, standardTools(), surveyor.Limits{}); err != nil || out.Reason != surveyor.ReasonTruncated {
		t.Errorf("%+v %v", out, err)
	}
}
