package mcpserver

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/BeppeTemp/cartographer/internal/audit"
)

// This file is the single seam (D119) that turns every dispatched tools/call
// into an attempt+completion pair of internal/audit events: attempt before
// authorization/execution, completion afterward, with the reduced argument
// set from auditResourceFields. It never touches internal/auth: principal
// attribution is derived here, directly from the request's bearer token,
// solely as an opaque, irreversible hash — the raw token itself is never
// logged. See docs/transport-auth.md and docs/deployment.md for the
// operator-facing description of this boundary.

// auditResourceFields is the golden, per-tool allow-list of argument field
// names that may be copied into an audit event's Resources map. Only
// identifiers belong here (concept ID, map/journal name, artifact kind/name,
// asset path, scope prefix, service ID, ...) — never frontmatter/body,
// patch/edit values, secret names or values, or free-text query strings. A
// tool with no entry (or an empty list) never records any argument; adding a
// new write tool without an entry here is safe by default (no leakage), just
// less specific in the audit trail (TestAuditResourceFieldsNeverLeakSecrets
// guards the leakage side; there is deliberately no "must cover every tool"
// test, since silence is always safe here).
var auditResourceFields = map[string][]string{
	"concept_read":         {"id"},
	"concept_write":        {"id"},
	"concept_new":          {"id", "template"},
	"concept_patch":        {"id"},
	"concept_expand":       {"id"},
	"concept_delete":       {"id"},
	"concept_move":         {"source_id", "target_id"},
	"supersede":            {"source_id", "target_id"},
	"map_create":           {"name", "kind"},
	"map_delete":           {"map"},
	"log_append":           {"path"},
	"graph_neighbors":      {"id"},
	"concept_list":         {"scope"},
	"contradiction_report": {"scope"},
	"asset_read":           {"concept_id", "path"},
	"asset_list":           {"concept_id"},
	"asset_write":          {"concept_id", "path"},
	"asset_delete":         {"concept_id", "path"},
	// The artifact tools address their target by path only (see
	// tools_artifact.go: "required": ["path", ...]); "kind"/"name" were never
	// arguments of theirs, so those entries recorded nothing. artifact_list
	// takes no arguments at all, so it has no entry (silence is safe here).
	"artifact_read":   {"path"},
	"artifact_write":  {"path"},
	"artifact_delete": {"path"},
	"service_get":     {"service_id"},
	// secret_resolve's "names" and secret_set's "key" are secret field
	// names, not resource identifiers — deliberately excluded.
	// "reveal" is recorded: the argument name is not secret, and the decision
	// to print a credential is exactly what an audit trail is for (D158).
	"secret_resolve":       {"concept_id", "reveal"},
	"secret_set":           {"path"},
	"skill_install":        {"name"},
	"conflict_resolve":     {"contradiction_id"},
	"git_conflict_resolve": {"concept_id"},
}

// extractResources returns the redacted Resources map for one tool call:
// only the string-valued fields named in auditResourceFields[tool] that are
// actually present in args. Returns nil if the tool has no allow-list entry,
// the entry is empty, or none of its fields are present — the safe default
// for any tool (including every read-only/introspection tool, and any tool
// added without an explicit entry here).
func extractResources(tool string, args json.RawMessage) map[string]string {
	fields := auditResourceFields[tool]
	if len(fields) == 0 || len(args) == 0 {
		return nil
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(args, &raw); err != nil {
		return nil
	}
	var out map[string]string
	for _, f := range fields {
		v, ok := raw[f]
		if !ok {
			continue
		}
		var s string
		if err := json.Unmarshal(v, &s); err != nil || s == "" {
			continue
		}
		if out == nil {
			out = make(map[string]string, len(fields))
		}
		out[f] = s
	}
	return out
}

// classifyOutcome maps a tool call's result onto the audit outcome taxonomy
// (internal/audit.Outcome*). Unauthenticated/unauthorized/cancelled are
// produced by other callers (auditDenied, called from handleToolsCall's
// authorize-failure path, for unauthorized; the other two are not reachable
// from this iteration's call sites — see docs/testing.md).
func classifyOutcome(result ToolResult, err error) string {
	switch {
	case err != nil:
		return audit.OutcomeInternalErr
	case result.IsError:
		return audit.OutcomeApplicationErr
	default:
		return audit.OutcomeSuccess
	}
}

// auditCall correlates one tool call's attempt and completion audit events.
// recorded is false when there is nothing to pair a completion with — either
// no audit sink is attached, or the attempt itself failed to append in
// best_effort mode (see beginAuditCall) — in which case end is a no-op: a
// completion event with no corresponding on-disk attempt would otherwise be
// reported as chain corruption by audit.VerifyFiles/VerifyAudit.
type auditCall struct {
	requestID string
	principal string
	tool      string
	external  string
	readOnly  bool
	resources map[string]string
	start     time.Time
	recorded  bool
}

// beginAuditCall emits the attempt-phase event for one tool call, if an audit
// sink is attached. Three outcomes:
//   - no sink attached: (call, ToolResult{}, true) — call.recorded is false,
//     end is later a no-op.
//   - sink attached, append fails, best_effort mode: (call, ToolResult{},
//     true) — the call proceeds un-audited (backward compatible default).
//   - sink attached, append fails, required mode: (call, rejected, false) —
//     the caller must return rejected to the client WITHOUT ever invoking the
//     tool handler (fail closed) and must not call end.
func (s *Server) beginAuditCall(principal, tool, external string, readOnly bool, resources map[string]string) (call auditCall, rejected ToolResult, ok bool) {
	s.mu.Lock()
	log, kbName, transport := s.auditLog, s.kbName, s.transport
	s.mu.Unlock()

	call = auditCall{
		requestID: newAuditRequestID(), principal: principal, tool: tool, external: external,
		readOnly: readOnly, resources: resources, start: time.Now(),
	}
	if log == nil {
		return call, ToolResult{}, true
	}
	_, err := log.AppendEvent(audit.Entry{
		RequestID: call.requestID, PrincipalID: principal, Transport: transport, KB: kbName,
		Tool: tool, ExternalTool: external, ReadOnly: readOnly, Resources: resources,
		Phase: audit.PhaseAttempt,
	})
	if err != nil {
		if log.Required() {
			return call, errorResult("audit log unavailable: call rejected (required mode): " + err.Error()), false
		}
		return call, ToolResult{}, true
	}
	call.recorded = true
	return call, ToolResult{}, true
}

// end emits the paired completion event, if the attempt was itself durably
// recorded (see auditCall.recorded). result.CommitSHA (set by gitWrap on a
// successful write, under the per-KB git lock — never a later racy HEAD
// query) is carried onto the completion event's CommitSHA field.
//
// A failure to append the completion is deliberately not retried or surfaced
// to the caller here: the tool call already ran (and, for a write, already
// committed) and cannot be rolled back or silently repeated. It instead
// degrades the sink's health (audit.Log.State), which is what makes the
// NEXT call's attempt-phase append fail in required mode — see
// beginAuditCall and docs/deployment.md's readiness/recovery description.
func (c auditCall) end(s *Server, outcome string, result ToolResult) {
	if !c.recorded {
		return
	}
	s.mu.Lock()
	log, kbName, transport := s.auditLog, s.kbName, s.transport
	s.mu.Unlock()
	if log == nil {
		return
	}
	_, _ = log.AppendEvent(audit.Entry{
		RequestID: c.requestID, PrincipalID: c.principal, Transport: transport, KB: kbName,
		Tool: c.tool, ExternalTool: c.external, ReadOnly: c.readOnly, Resources: c.resources,
		Phase: audit.PhaseCompletion, Outcome: outcome,
		DurationMs: time.Since(c.start).Milliseconds(), CommitSHA: result.CommitSHA,
	})
}

// auditDenied records an authorization denial (handleToolsCall's
// s.authorize failure path, server.go) as one attempt+completion pair with
// outcome=unauthorized. This is the only point that can audit that denial:
// dispatch never reaches the tool, so beginAuditCall/end never runs for it.
// principal is read from the request context by the caller (D118) rather
// than derived here, so this function has no *http.Request dependency and
// audits identically over HTTP and stdio. It never blocks or changes the
// caller's already-decided error response — if the sink itself is
// unavailable, the denial is simply not recorded (best_effort) rather than
// failing the already-decided rejection a second time.
func (s *Server) auditDenied(principal, toolName string, rawArgs json.RawMessage) {
	s.mu.Lock()
	log, kbName, transport := s.auditLog, s.kbName, s.transport
	s.mu.Unlock()
	if log == nil {
		return
	}

	canonical := s.StripToolPrefix(toolName)
	external := ""
	if canonical != toolName {
		external = toolName
	}
	readOnly := false
	if t, ok := s.Tools()[toolName]; ok {
		readOnly = t.ReadOnly
	}
	resources := extractResources(canonical, rawArgs)
	requestID := newAuditRequestID()
	start := time.Now()

	_, err := log.AppendEvent(audit.Entry{
		RequestID: requestID, PrincipalID: principal, Transport: transport, KB: kbName,
		Tool: canonical, ExternalTool: external, ReadOnly: readOnly, Resources: resources,
		Phase: audit.PhaseAttempt,
	})
	if err != nil {
		// Sink unhealthy: nothing safe to record for this denial (a
		// completion here would pair with an attempt that never landed on
		// disk). The denial response is unaffected either way — the call was
		// already going to be denied.
		return
	}
	_, _ = log.AppendEvent(audit.Entry{
		RequestID: requestID, PrincipalID: principal, Transport: transport, KB: kbName,
		Tool: canonical, ExternalTool: external, ReadOnly: readOnly, Resources: resources,
		Phase: audit.PhaseCompletion, Outcome: audit.OutcomeUnauthorized,
		DurationMs: time.Since(start).Milliseconds(),
	})
}

// newAuditRequestID returns a random 32-hex-char correlation ID pairing one
// tool call's attempt and completion audit events.
func newAuditRequestID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand failure is effectively unreachable on any supported
		// platform; degrade to a low-collision-risk fallback rather than
		// panicking mid-dispatch.
		return hex.EncodeToString([]byte(time.Now().Format(time.RFC3339Nano)))
	}
	return hex.EncodeToString(b)
}
