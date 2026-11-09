package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/BeppeTemp/cartographer/internal/client"
)

// ToolCaller is the MCP side of the loop; *client.MCPClient satisfies it.
type ToolCaller interface {
	ListTools() ([]client.ToolInfo, error)
	CallTool(name string, args any) (client.ToolOutput, error)
}

// Limits bound a run; a zero or negative value takes the default, never
// "unlimited".
type Limits struct {
	MaxSteps           int
	MaxTotalTokens     int
	MaxOutputTokens    int
	MaxToolResultBytes int
}

// Default limits.
const (
	DefaultMaxSteps           = 30
	DefaultMaxTotalTokens     = 200_000
	DefaultMaxOutputTokens    = 4096
	DefaultMaxToolResultBytes = 32 << 10
)

func (l Limits) withDefaults() Limits {
	if l.MaxSteps <= 0 {
		l.MaxSteps = DefaultMaxSteps
	}
	if l.MaxTotalTokens <= 0 {
		l.MaxTotalTokens = DefaultMaxTotalTokens
	}
	if l.MaxOutputTokens <= 0 {
		l.MaxOutputTokens = DefaultMaxOutputTokens
	}
	if l.MaxToolResultBytes <= 0 {
		l.MaxToolResultBytes = DefaultMaxToolResultBytes
	}
	return l
}

// Task is one run: the instructions are a parameter, nothing is read from the
// KB or from disk (D6, D15).
type Task struct{ System, User string }

// Reason is why a run stopped.
type Reason string

// Stop reasons of a run.
const (
	ReasonDone         Reason = "Done"
	ReasonMaxSteps     Reason = "MaxSteps"
	ReasonBudget       Reason = "Budget"
	ReasonTruncated    Reason = "Truncated"
	ReasonModelStopped Reason = "ModelStopped"
	ReasonCanceled     Reason = "Canceled"
	ReasonAborted      Reason = "Aborted" // a model or transport error; Run returns it too
)

// CallRecord is one made tool call, for the audit line.
type CallRecord struct {
	Tool    string
	IsError bool
	// Uncertain marks a call whose transport failed: it may have reached the server.
	Uncertain bool
}

// Outcome is what a run did.
type Outcome struct {
	Reason    Reason
	RawStop   string // the provider's stop value when Reason is ModelStopped
	Final     string
	Steps     int
	Usage     Usage
	ToolCalls int
	Refused   int // calls the allowlist refused locally (injection attempts)
	Writes    int // made calls to a tool outside the read set
	Calls     []CallRecord
}

// Event is what the optional callback receives: names and counts, never a
// prompt, an argument or a result.
type Event struct {
	Kind    string // "model", "tool", "refused"
	Step    int
	Tool    string
	IsError bool
	Tokens  int
}

// Agent runs a model against an MCP server through an allowlist.
type Agent struct {
	Model   Model
	Tools   ToolCaller
	Allow   *Allowlist
	Limits  Limits
	OnEvent func(Event)
}

// NewAgent builds an Agent; a nil allowlist is the default one.
func NewAgent(m Model, tools ToolCaller, allow *Allowlist, limits Limits) *Agent {
	if allow == nil {
		allow = NewAllowlist()
	}
	return &Agent{Model: m, Tools: tools, Allow: allow, Limits: limits}
}

func (a *Agent) emit(e Event) {
	if a.OnEvent != nil {
		a.OnEvent(e)
	}
}

// Run executes the task. The loop is bounded by the step and token caps; a
// model or transport error aborts it and is returned with the partial Outcome
// (tokens already spent still count for the caller's budget).
func (a *Agent) Run(ctx context.Context, task Task) (Outcome, error) {
	lim := a.Limits.withDefaults()
	var out Outcome
	listed, err := a.Tools.ListTools()
	if err != nil {
		out.Reason = ReasonAborted
		return out, err
	}
	specs := a.Allow.Filter(listed)
	if len(specs) == 0 {
		out.Reason = ReasonAborted
		return out, errors.New("worker: no tool is both allowed and offered by the server; check the token and the allowlist")
	}
	offered := make(map[string]bool, len(specs))
	for _, s := range specs {
		offered[s.Name] = true
	}
	msgs := []Message{{Role: RoleUser, Text: task.User}}

	for {
		switch {
		case ctx.Err() != nil:
			out.Reason = ReasonCanceled
			return out, nil
		case out.Steps >= lim.MaxSteps:
			out.Reason = ReasonMaxSteps
			return out, nil
		case out.Usage.Total() >= lim.MaxTotalTokens:
			out.Reason = ReasonBudget
			return out, nil
		}
		resp, err := a.Model.Complete(ctx, Request{System: task.System, Messages: msgs, Tools: specs, MaxOutputTokens: lim.MaxOutputTokens})
		if err != nil {
			out.Reason = ReasonAborted
			return out, err
		}
		out.Steps++
		out.Usage.InputTokens += resp.Usage.InputTokens
		out.Usage.OutputTokens += resp.Usage.OutputTokens
		out.Usage.Estimated = out.Usage.Estimated || resp.Usage.Estimated
		a.emit(Event{Kind: "model", Step: out.Steps, Tokens: resp.Usage.Total()})

		stop := resp.Stop
		if stop == StopToolUse && len(resp.ToolCalls) == 0 {
			stop = StopOther
		}
		switch stop {
		case StopEnd:
			out.Reason, out.Final = ReasonDone, resp.Text
			return out, nil
		case StopMaxTokens:
			out.Reason = ReasonTruncated
			return out, nil
		case StopToolUse:
			if out.Usage.Total() >= lim.MaxTotalTokens {
				out.Reason = ReasonBudget // over budget: no more writes
				return out, nil
			}
			results := make([]ToolResult, 0, len(resp.ToolCalls))
			for _, c := range resp.ToolCalls {
				if ctx.Err() != nil { // a cancelled worker makes no further write
					out.Reason = ReasonCanceled
					return out, nil
				}
				res, err := a.execute(c, offered, lim, &out)
				if err != nil {
					out.Reason = ReasonAborted
					return out, err
				}
				results = append(results, res)
			}
			msgs = append(msgs,
				Message{Role: RoleAssistant, Text: resp.Text, ToolCalls: resp.ToolCalls},
				Message{Role: RoleUser, ToolResults: results})
		default:
			out.Reason, out.RawStop = ReasonModelStopped, resp.RawStop
			return out, nil
		}
	}
}

// invalidParams is the JSON-RPC code the server answers for an undeclared or
// malformed argument.
const invalidParams = -32602

// execute runs one call; only a transport failure comes back as an error.
func (a *Agent) execute(c ToolCall, offered map[string]bool, lim Limits, out *Outcome) (ToolResult, error) {
	fail := func(text string) ToolResult { return ToolResult{CallID: c.ID, Text: text, IsError: true} }
	if !offered[c.Name] || !a.Allow.Permits(c.Name) {
		out.Refused++
		a.emit(Event{Kind: "refused", Step: out.Steps, Tool: c.Name, IsError: true})
		return fail("tool is not available"), nil
	}
	args := bytes.TrimSpace(c.Arguments)
	if len(args) == 0 || string(args) == "null" {
		args = []byte("{}")
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(args, &obj) != nil || obj == nil {
		return fail("arguments must be a JSON object"), nil
	}
	if a.Allow.RefusesArgs(c.Name, args) {
		out.Refused++
		a.emit(Event{Kind: "refused", Step: out.Steps, Tool: c.Name, IsError: true})
		return fail("tool is not available"), nil
	}
	res, err := a.Tools.CallTool(c.Name, json.RawMessage(args))
	if err != nil {
		var re *client.RemoteError
		if !errors.As(err, &re) || re.RPCCode != invalidParams {
			// The request may have reached the server before the transport
			// failed: count it, as uncertain, so a write is never under-reported.
			out.ToolCalls++
			if !isRead(c.Name) {
				out.Writes++
			}
			out.Calls = append(out.Calls, CallRecord{Tool: c.Name, IsError: true, Uncertain: true})
			return ToolResult{}, err
		}
		res = client.ToolOutput{IsError: true, Text: re.Message}
	}
	out.ToolCalls++
	if !isRead(c.Name) {
		out.Writes++
	}
	out.Calls = append(out.Calls, CallRecord{Tool: c.Name, IsError: res.IsError})
	a.emit(Event{Kind: "tool", Step: out.Steps, Tool: c.Name, IsError: res.IsError})
	return ToolResult{CallID: c.ID, Text: truncateBytes(res.Text, lim.MaxToolResultBytes), IsError: res.IsError}, nil
}

// truncateBytes cuts s at max bytes on a rune boundary and says how much went.
func truncateBytes(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return fmt.Sprintf("%s\n[truncated %d bytes]", s[:cut], len(s)-cut)
}
