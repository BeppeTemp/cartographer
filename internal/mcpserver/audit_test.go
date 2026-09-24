package mcpserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/audit"
	"github.com/BeppeTemp/cartographer/internal/auth"
)

func auditServer(t *testing.T, opts audit.Options) (*Server, *audit.Log, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	l, err := audit.OpenWithOptions(path, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	s := New("test")
	s.SetAuditLog(l)
	s.SetKBName("docs")
	s.SetTransport("http")
	return s, l, path
}

func auditEntries(t *testing.T, path string) []audit.Entry {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []audit.Entry
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var e audit.Entry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("bad audit line %q: %v", line, err)
		}
		out = append(out, e)
	}
	return out
}

func callTool(t *testing.T, s *Server, name, args string) ToolResult {
	t.Helper()
	return s.callTool(authLocalContext(), name, json.RawMessage(args))
}

// TestAuditRecordsAttemptAndCompletionPair is the core contract: an audited
// call leaves an attempt event before the tool runs and a completion event
// after it, both attributed to the same KB and transport.
func TestAuditRecordsAttemptAndCompletionPair(t *testing.T) {
	s, _, path := auditServer(t, audit.Options{})
	s.RegisterTool(Tool{
		Name: "ok_tool", ReadOnly: true,
		InputSchema: json.RawMessage(`{"type":"object"}`),
		Handler: func(requestContext, json.RawMessage) (ToolResult, error) {
			return textResult("fine"), nil
		},
	})

	if res := callTool(t, s, "ok_tool", `{}`); res.IsError {
		t.Fatalf("tool returned an error: %+v", res.Content)
	}

	entries := auditEntries(t, path)
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want an attempt+completion pair: %+v", len(entries), entries)
	}
	if entries[0].Phase != audit.PhaseAttempt {
		t.Errorf("first entry phase = %q, want %q", entries[0].Phase, audit.PhaseAttempt)
	}
	if entries[1].Phase != audit.PhaseCompletion || entries[1].Outcome != audit.OutcomeSuccess {
		t.Errorf("second entry = %q/%q, want completion/success", entries[1].Phase, entries[1].Outcome)
	}
	for i, e := range entries {
		if e.Tool != "ok_tool" {
			t.Errorf("entry %d tool = %q", i, e.Tool)
		}
	}
}

// TestAuditRecordsDenialAttemptAndCompletionPair is the audit-of-denial
// contract (D132, issue #122): handleToolsCall returns as soon as s.authorize
// fails, before beginAuditCall would otherwise run — so a scope/permission
// denial must still leave an attempt+completion pair, with outcome
// unauthorized, via auditDenied, or a denied call would be invisible in the
// log.
func TestAuditRecordsDenialAttemptAndCompletionPair(t *testing.T) {
	s, _, path := auditServer(t, audit.Options{})
	called := false
	s.RegisterTool(Tool{
		Name: "guarded_tool", ReadOnly: true,
		InputSchema: json.RawMessage(`{"type":"object"}`),
		Handler: func(requestContext, json.RawMessage) (ToolResult, error) {
			called = true
			return textResult("should never run"), nil
		},
	})

	// A non-admin principal with no authorizer installed is fail-closed
	// (Server.authorize), the same shape a real scope denial takes.
	ctx := restrictedContext(auth.Policy{})
	tr := s.callTool(ctx, "guarded_tool", json.RawMessage(`{}`))
	if !tr.IsError {
		t.Fatalf("denied call did not return an error result: %+v", tr)
	}
	if called {
		t.Fatal("denied call reached the tool handler")
	}

	entries := auditEntries(t, path)
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want a denial attempt+completion pair: %+v", len(entries), entries)
	}
	if entries[0].Phase != audit.PhaseAttempt {
		t.Errorf("first entry phase = %q, want %q", entries[0].Phase, audit.PhaseAttempt)
	}
	if entries[1].Phase != audit.PhaseCompletion || entries[1].Outcome != audit.OutcomeUnauthorized {
		t.Errorf("second entry = %q/%q, want completion/%q", entries[1].Phase, entries[1].Outcome, audit.OutcomeUnauthorized)
	}
	for i, e := range entries {
		if e.Tool != "guarded_tool" {
			t.Errorf("entry %d tool = %q, want %q", i, e.Tool, "guarded_tool")
		}
	}
}

// TestAuditBestEffortDoesNotBlockTheCall is the availability guarantee: when
// the sink is broken, the MCP call must still succeed in best_effort mode.
func TestAuditBestEffortDoesNotBlockTheCall(t *testing.T) {
	s, _, _ := auditServer(t, audit.Options{Mode: "best_effort"})
	s.RegisterTool(Tool{
		Name: "ok_tool", ReadOnly: true,
		InputSchema: json.RawMessage(`{"type":"object"}`),
		Handler: func(requestContext, json.RawMessage) (ToolResult, error) {
			return textResult("fine"), nil
		},
	})
	restore := audit.FailAppendsForTest()
	defer restore()

	res := callTool(t, s, "ok_tool", `{}`)
	if res.IsError {
		t.Fatalf("best_effort audit failure blocked the call: %+v", res.Content)
	}
}

// TestAuditRequiredRejectsBeforeTheToolRuns is the compliance guarantee: in
// required mode a failed attempt append must reject the call without the tool
// ever executing, so the log can never miss an operation that happened.
func TestAuditRequiredRejectsBeforeTheToolRuns(t *testing.T) {
	s, _, _ := auditServer(t, audit.Options{Mode: "required"})
	ran := false
	s.RegisterTool(Tool{
		Name: "side_effect", ReadOnly: false,
		InputSchema: json.RawMessage(`{"type":"object"}`),
		Handler: func(requestContext, json.RawMessage) (ToolResult, error) {
			ran = true
			return textResult("done"), nil
		},
	})
	restore := audit.FailAppendsForTest()
	defer restore()

	res := callTool(t, s, "side_effect", `{}`)
	if !res.IsError {
		t.Fatal("required mode accepted a call it could not audit")
	}
	if ran {
		t.Fatal("the tool ran despite the audit rejection — the log would be missing a real operation")
	}
}

// TestAuditUnknownToolRecordsOutcomeWithoutArguments keeps arguments of an
// unregistered tool out of the log while still recording the attempt.
func TestAuditUnknownToolRecordsOutcomeWithoutArguments(t *testing.T) {
	s, _, path := auditServer(t, audit.Options{})
	callTool(t, s, "no_such_tool", `{"secret":"s3cr3t"}`)

	entries := auditEntries(t, path)
	if len(entries) == 0 {
		t.Fatal("unknown tool was not audited")
	}
	last := entries[len(entries)-1]
	if last.Outcome != audit.OutcomeUnknownTool {
		t.Errorf("outcome = %q, want %q", last.Outcome, audit.OutcomeUnknownTool)
	}
	raw, _ := json.Marshal(entries)
	if strings.Contains(string(raw), "s3cr3t") {
		t.Fatal("arguments of an unknown tool leaked into the audit log")
	}
}

// TestAuditDisabledLeavesDispatchUnchanged pins the pre-D119 default: with no
// sink attached nothing is recorded and nothing fails.
func TestAuditDisabledLeavesDispatchUnchanged(t *testing.T) {
	s := New("test")
	s.RegisterTool(Tool{
		Name: "ok_tool", ReadOnly: true,
		InputSchema: json.RawMessage(`{"type":"object"}`),
		Handler: func(requestContext, json.RawMessage) (ToolResult, error) {
			return textResult("fine"), nil
		},
	})
	if res := callTool(t, s, "ok_tool", `{}`); res.IsError {
		t.Fatalf("call failed with auditing off: %+v", res.Content)
	}
}

// TestClassifyOutcomeDistinguishesApplicationFromInternalErrors keeps the two
// failure kinds separable in a compliance review.
func TestClassifyOutcomeDistinguishesApplicationFromInternalErrors(t *testing.T) {
	if got := classifyOutcome(textResult("ok"), nil); got != audit.OutcomeSuccess {
		t.Errorf("success = %q", got)
	}
	if got := classifyOutcome(errorResult("nope"), nil); got != audit.OutcomeApplicationErr {
		t.Errorf("application error = %q", got)
	}
	if got := classifyOutcome(ToolResult{}, os.ErrClosed); got != audit.OutcomeInternalErr {
		t.Errorf("internal error = %q", got)
	}
}

// The flags that decide whether a credential is decrypted and printed are
// booleans; a string-only filter dropped them silently, so D158's promise that
// reveal "is recorded in the audit trail" was never kept (D261).
func TestExtractResourcesRecordsAllowListedBooleans(t *testing.T) {
	t.Run("secret_resolve reveal is recorded, names are not", func(t *testing.T) {
		got := extractResources("secret_resolve", json.RawMessage(`{"concept_id":"svc","names":["TOKEN"],"reveal":true}`))
		if got["reveal"] != "true" {
			t.Errorf("Resources[reveal] = %q, want \"true\" (all: %v)", got["reveal"], got)
		}
		if got["concept_id"] != "svc" {
			t.Errorf("Resources[concept_id] = %q, want \"svc\"", got["concept_id"])
		}
		if _, ok := got["names"]; ok {
			t.Errorf("secret names reached the audit: %v", got)
		}
	})

	t.Run("reveal false is recorded as false", func(t *testing.T) {
		got := extractResources("secret_resolve", json.RawMessage(`{"concept_id":"svc","reveal":false}`))
		if got["reveal"] != "false" {
			t.Errorf("Resources[reveal] = %q, want \"false\"", got["reveal"])
		}
	})

	t.Run("service_get records resolve_secrets and reveal", func(t *testing.T) {
		got := extractResources("service_get", json.RawMessage(`{"service_id":"svc","resolve_secrets":true,"reveal":true}`))
		if got["resolve_secrets"] != "true" || got["reveal"] != "true" || got["service_id"] != "svc" {
			t.Errorf("Resources = %v, want service_id, resolve_secrets and reveal", got)
		}
	})

	t.Run("a boolean outside the allow-list stays absent", func(t *testing.T) {
		got := extractResources("service_get", json.RawMessage(`{"service_id":"svc","verbose":true}`))
		if _, ok := got["verbose"]; ok {
			t.Errorf("a non-allow-listed boolean reached the audit: %v", got)
		}
	})

	t.Run("other JSON types stay dropped", func(t *testing.T) {
		got := extractResources("service_get", json.RawMessage(`{"service_id":"svc","resolve_secrets":1,"reveal":null}`))
		if len(got) != 1 || got["service_id"] != "svc" {
			t.Errorf("Resources = %v, want only service_id", got)
		}
	})
}

// End to end through the dispatcher: the audit entry of a revealing
// secret_resolve call carries reveal=true (and never the requested names).
func TestAuditRecordsSecretResolveReveal(t *testing.T) {
	s, _, path := auditServer(t, audit.Options{})
	s.RegisterTool(Tool{
		Name:        "secret_resolve",
		InputSchema: json.RawMessage(`{"type":"object"}`),
		Handler: func(requestContext, json.RawMessage) (ToolResult, error) {
			return textResult("TOKEN=<redacted>"), nil
		},
	})
	callTool(t, s, "secret_resolve", `{"concept_id":"svc","names":["TOKEN"],"reveal":true}`)
	entries := auditEntries(t, path)
	if len(entries) == 0 {
		t.Fatal("no audit entries")
	}
	for _, e := range entries {
		if e.Resources["reveal"] != "true" {
			t.Errorf("entry %+v: Resources[reveal] = %q, want \"true\"", e, e.Resources["reveal"])
		}
		if _, ok := e.Resources["names"]; ok {
			t.Errorf("entry %+v carries secret names", e)
		}
	}
}
