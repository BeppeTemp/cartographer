package worker_test

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
	"github.com/BeppeTemp/cartographer/internal/worker"
	"github.com/BeppeTemp/cartographer/internal/worker/workertest"
)

func tc(id, name, args string) worker.ToolCall {
	return worker.ToolCall{ID: id, Name: name, Arguments: json.RawMessage(args)}
}

func use(calls ...worker.ToolCall) worker.Response {
	return worker.Response{ToolCalls: calls, Stop: worker.StopToolUse, Usage: worker.Usage{InputTokens: 10, OutputTokens: 5}}
}

func end(text string) worker.Response {
	return worker.Response{Text: text, Stop: worker.StopEnd, Usage: worker.Usage{InputTokens: 10, OutputTokens: 5}}
}

func run(t *testing.T, f *workertest.Fake, tools *workertest.FakeTools, lim worker.Limits) (worker.Outcome, error) {
	t.Helper()
	return worker.NewAgent(f, tools, nil, lim).Run(context.Background(), worker.Task{System: "sys", User: "go"})
}

func standardTools() *workertest.FakeTools {
	return &workertest.FakeTools{Tools: workertest.Listing("search", "concept_read", "concept_write", "concept_delete", "artifact_write")}
}

func TestRun_DoneInOneStep(t *testing.T) {
	f := &workertest.Fake{Turns: []worker.Response{end("all done")}}
	out, err := run(t, f, standardTools(), worker.Limits{})
	if err != nil || out.Reason != worker.ReasonDone || out.Final != "all done" || out.Steps != 1 || out.Usage.Total() != 15 {
		t.Fatalf("%+v %v", out, err)
	}
	req := f.Requests[0]
	if req.System != "sys" || req.MaxOutputTokens != worker.DefaultMaxOutputTokens || req.Messages[0].Text != "go" {
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
	f := &workertest.Fake{Turns: []worker.Response{use(tc("a", "concept_read", `{"id":"x"}`), tc("b", "concept_write", `{"id":"x"}`)), end("ok")}}
	tools := standardTools()
	out, err := run(t, f, tools, worker.Limits{})
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
	f := &workertest.Fake{Turns: []worker.Response{
		use(tc("a", "concept_delete", `{}`), tc("b", "artifact_write", `{}`), tc("c", "lint", `{}`), tc("d", "nonsense", `{}`)),
		end("ok"),
	}}
	tools := standardTools() // lint is allowed in general but this token does not list it
	out, err := run(t, f, tools, worker.Limits{})
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
	f := &workertest.Fake{Turns: []worker.Response{use(tc("a", "concept_write", `{}`)), end("ok")}}
	out, err := run(t, f, tools, worker.Limits{})
	r := f.Requests[1].Messages[2].ToolResults[0]
	if err != nil || out.Reason != worker.ReasonDone || !r.IsError || r.Text != "stale write: re-read" || !out.Calls[0].IsError {
		t.Fatalf("%+v %v %+v", out, err, r)
	}
}

func TestRun_TransportErrorAborts(t *testing.T) {
	tools := standardTools()
	boom := &client.RemoteError{Code: client.CodeUnreachable, Message: "down"}
	tools.Errors = map[string]error{"search": boom}
	f := &workertest.Fake{Turns: []worker.Response{use(tc("a", "search", `{}`)), end("never")}}
	out, err := run(t, f, tools, worker.Limits{})
	if !errors.Is(err, boom) || out.Reason != worker.ReasonAborted || len(f.Requests) != 1 {
		t.Fatalf("%+v %v", out, err)
	}
	if out.ToolCalls != 1 || len(out.Calls) != 1 || !out.Calls[0].Uncertain {
		t.Errorf("the attempted call is not recorded: %+v", out)
	}
	// A write that failed in transport still counts as a write.
	tools = standardTools()
	tools.Errors = map[string]error{"concept_write": boom}
	f = &workertest.Fake{Turns: []worker.Response{use(tc("a", "concept_write", `{}`))}}
	if out, _ = run(t, f, tools, worker.Limits{}); out.Writes != 1 {
		t.Errorf("writes = %d, want 1", out.Writes)
	}
}

// Pinned: a typo in an argument (-32602) must not cost the run.
func TestRun_InvalidParamsIsFedBack(t *testing.T) {
	tools := standardTools()
	tools.Errors = map[string]error{"search": &client.RemoteError{Code: client.CodeMCPFailed, RPCCode: -32602, Message: "invalid params: unknown argument(s) qq for tool search"}}
	f := &workertest.Fake{Turns: []worker.Response{use(tc("a", "search", `{"qq":1}`)), end("ok")}}
	out, err := run(t, f, tools, worker.Limits{})
	r := f.Requests[1].Messages[2].ToolResults[0]
	if err != nil || out.Reason != worker.ReasonDone || !r.IsError || !strings.Contains(r.Text, "unknown argument") {
		t.Fatalf("%+v %v %+v", out, err, r)
	}
}

func TestRun_MalformedArguments(t *testing.T) {
	tools := standardTools()
	f := &workertest.Fake{Turns: []worker.Response{use(tc("a", "search", `not json`), tc("b", "search", `[1]`), tc("c", "search", ``)), end("ok")}}
	out, err := run(t, f, tools, worker.Limits{})
	res := f.Requests[1].Messages[2].ToolResults
	if err != nil || !res[0].IsError || !res[1].IsError || res[2].IsError || tools.Calls["search"] != 1 || string(tools.Args["search"]) != "{}" {
		t.Fatalf("%+v %v %+v calls=%v", out, err, res, tools.Calls)
	}
}

func TestRun_StepCap(t *testing.T) {
	turns := []worker.Response{use(tc("a", "search", `{}`)), use(tc("b", "search", `{}`)), use(tc("c", "search", `{}`))}
	f := &workertest.Fake{Turns: turns}
	out, err := run(t, f, standardTools(), worker.Limits{MaxSteps: 2})
	if err != nil || out.Reason != worker.ReasonMaxSteps || out.Steps != 2 || len(f.Requests) != 2 {
		t.Fatalf("%+v %v", out, err)
	}
}

func TestRun_TokenCapBeforeCallAndAfterToolUse(t *testing.T) {
	// After a StopToolUse that crosses the cap: no tool executes.
	tools := standardTools()
	f := &workertest.Fake{Turns: []worker.Response{use(tc("a", "concept_write", `{}`))}}
	out, err := run(t, f, tools, worker.Limits{MaxTotalTokens: 15})
	if err != nil || out.Reason != worker.ReasonBudget || len(tools.Calls) != 0 || out.Usage.Total() != 15 {
		t.Fatalf("after: %+v %v %v", out, err, tools.Calls)
	}
	// Under the cap after the first turn: the tool runs and the loop continues.
	tools = standardTools()
	f = &workertest.Fake{Turns: []worker.Response{use(tc("a", "search", `{}`)), end("done")}}
	out, err = run(t, f, tools, worker.Limits{MaxTotalTokens: 16})
	if err != nil || out.Reason != worker.ReasonDone || tools.Calls["search"] != 1 || len(f.Requests) != 2 {
		t.Fatalf("under: %+v %v", out, err)
	}
	// Cap reached by a StopEnd-less turn that asked for nothing more: the next
	// turn is never requested.
	f = &workertest.Fake{Turns: []worker.Response{use(tc("a", "search", `{}`)), use(tc("b", "search", `{}`)), end("no")}}
	out, err = run(t, f, standardTools(), worker.Limits{MaxTotalTokens: 20})
	if err != nil || out.Reason != worker.ReasonBudget || len(f.Requests) != 2 {
		t.Fatalf("before: %+v %v requests=%d", out, err, len(f.Requests))
	}
}

func TestRun_TruncatedAndOther(t *testing.T) {
	f := &workertest.Fake{Turns: []worker.Response{{Text: "half", Stop: worker.StopMaxTokens}}}
	if out, _ := run(t, f, standardTools(), worker.Limits{}); out.Reason != worker.ReasonTruncated {
		t.Errorf("%+v", out)
	}
	f = &workertest.Fake{Turns: []worker.Response{{Stop: worker.StopOther, RawStop: "refusal"}}}
	if out, _ := run(t, f, standardTools(), worker.Limits{}); out.Reason != worker.ReasonModelStopped || out.RawStop != "refusal" {
		t.Errorf("%+v", out)
	}
	// StopToolUse with no calls is treated as StopOther.
	f = &workertest.Fake{Turns: []worker.Response{{Stop: worker.StopToolUse}}}
	if out, _ := run(t, f, standardTools(), worker.Limits{}); out.Reason != worker.ReasonModelStopped {
		t.Errorf("%+v", out)
	}
}

func TestRun_EmptyOfferedSetErrorsBeforeTheModel(t *testing.T) {
	f := &workertest.Fake{Turns: []worker.Response{end("x")}}
	out, err := run(t, f, &workertest.FakeTools{Tools: workertest.Listing("concept_delete", "unknown")}, worker.Limits{})
	if err == nil || len(f.Requests) != 0 || out.Reason != worker.ReasonAborted {
		t.Fatalf("err=%v requests=%d reason=%q", err, len(f.Requests), out.Reason)
	}
}

func TestRun_ContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f := &workertest.Fake{Turns: []worker.Response{end("x")}}
	out, err := worker.NewAgent(f, standardTools(), nil, worker.Limits{}).Run(ctx, worker.Task{})
	if err != nil || out.Reason != worker.ReasonCanceled || len(f.Requests) != 0 {
		t.Fatalf("%+v %v", out, err)
	}
}

// A cancelled worker stops before the next call of the same turn.
func TestRun_CancelBetweenCallsOfOneTurn(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	tools := &cancelTools{FakeTools: *standardTools(), cancel: cancel}
	f := &workertest.Fake{Turns: []worker.Response{use(tc("a", "search", `{}`), tc("b", "concept_write", `{}`))}}
	out, err := worker.NewAgent(f, tools, nil, worker.Limits{}).Run(ctx, worker.Task{})
	if err != nil || out.Reason != worker.ReasonCanceled || tools.Calls["concept_write"] != 0 || out.ToolCalls != 1 {
		t.Fatalf("%+v %v %v", out, err, tools.Calls)
	}
}

// cancelTools cancels the context once its first call is made.
type cancelTools struct {
	workertest.FakeTools
	cancel context.CancelFunc
}

func (c *cancelTools) CallTool(name string, args any) (client.ToolOutput, error) {
	defer c.cancel()
	return c.FakeTools.CallTool(name, args)
}

func TestRun_OversizedResultTruncatedOnRuneBoundary(t *testing.T) {
	tools := standardTools()
	tools.Outputs = map[string]client.ToolOutput{"search": {Text: strings.Repeat("é", 100)}} // 200 bytes
	f := &workertest.Fake{Turns: []worker.Response{use(tc("a", "search", `{}`)), end("ok")}}
	_, err := run(t, f, tools, worker.Limits{MaxToolResultBytes: 11}) // 11 falls inside a rune
	got := f.Requests[1].Messages[2].ToolResults[0].Text
	if err != nil || !strings.HasPrefix(got, strings.Repeat("é", 5)+"\n[truncated 190 bytes]") {
		t.Fatalf("%q %v", got, err)
	}
}

func TestRun_EventsCarryNoContent(t *testing.T) {
	var events []worker.Event
	f := &workertest.Fake{Turns: []worker.Response{use(tc("a", "search", `{"q":"secret-arg"}`)), end("ok")}}
	a := worker.NewAgent(f, standardTools(), nil, worker.Limits{})
	a.OnEvent = func(e worker.Event) { events = append(events, e) }
	if _, err := a.Run(context.Background(), worker.Task{User: "secret-prompt"}); err != nil {
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

	m, err := worker.NewAnthropic(model.URL, "sk-test", "m")
	if err != nil {
		t.Fatal(err)
	}
	out, err := worker.NewAgent(m, client.New(mcp.URL, "tok"), nil, worker.Limits{}).Run(context.Background(), worker.Task{System: "s", User: "u"})
	if err != nil || out.Reason != worker.ReasonDone || out.Final != "finished" || out.Refused != 1 || out.ToolCalls != 1 || out.Usage.Total() != 60 {
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
	f := &workertest.Fake{Turns: []worker.Response{
		use(tc("a", "concept_batch", `{"operations":[{"op":"delete","id":"x"}]}`)), end("ok"),
	}}
	tools := &workertest.FakeTools{Tools: workertest.Listing("search", "concept_batch")} // offered and allowed by name
	out, err := run(t, f, tools, worker.Limits{})
	if err != nil || out.Refused != 1 || tools.Calls["concept_batch"] != 0 {
		t.Fatalf("%+v %v calls=%v", out, err, tools.Calls)
	}
}
