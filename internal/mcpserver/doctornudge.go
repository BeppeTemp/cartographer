package mcpserver

import (
	"context"
	"fmt"
	"time"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/lint"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// doctorNudgeWindow is how long the server stays quiet toward one MCP session
// after proposing a kb-doctor session (D299, D358). In memory: a restart
// re-arms it, which costs one extra notice at most.
const doctorNudgeWindow = 24 * time.Hour

type callerClientKey struct{}

// withCallerClient records on ctx the clientInfo name the SDK resolved for
// the request (handshake or _meta), so the nudge can tell the CLI apart.
func withCallerClient(ctx context.Context, req any) context.Context {
	type identified interface {
		ClientInfo() *sdk.Implementation
	}
	if r, ok := req.(identified); ok {
		if info := r.ClientInfo(); info != nil {
			return context.WithValue(ctx, callerClientKey{}, info.Name)
		}
	}
	return ctx
}

type callerSessionKey struct{}

// withCallerSession records the MCP session the request belongs to (the SDK
// session ID: the Mcp-Session-Id on HTTP), so the nudge window is per client
// session and not per process (D358). A transport with no session ID leaves it
// empty, which is one shared bucket: the previous per-process behavior.
func withCallerSession(ctx context.Context, req any) context.Context {
	type sessioned interface {
		GetSession() sdk.Session
	}
	if r, ok := req.(sessioned); ok {
		if ss, ok := r.GetSession().(*sdk.ServerSession); ok && ss != nil {
			return context.WithValue(ctx, callerSessionKey{}, ss.ID())
		}
	}
	return ctx
}

func callerSession(ctx context.Context) string {
	id, _ := ctx.Value(callerSessionKey{}).(string)
	return id
}

func callerClient(ctx context.Context) string {
	name, _ := ctx.Value(callerClientKey{}).(string)
	return name
}

// withDoctorNudge appends one text block proposing a kb-doctor session to a
// successful tool result, at most once per doctorNudgeWindow per MCP session
// (D358: the natural moment is a session start), when the
// KB has debt and its doctor interval has passed (D299). The server only
// proposes: it never starts the session and never writes.
//
// The cheap gates come first and the window is claimed before the lint is
// read, so a KB with no debt costs at most one lint per window on this path.
// The CLI never receives the block: client.Call turns a multi-block answer
// into a JSON array, which every CLI command would fail to decode.
func (s *Server) withDoctorNudge(ctx context.Context, tool string, res ToolResult) ToolResult {
	k, cc := s.kbRef, s.conformance
	if res.IsError || tool == "kb_status" || k == nil || cc == nil || k.DoctorIntervalDays <= 0 {
		return res
	}
	if callerClient(ctx) == client.ClientName {
		return res
	}
	p := auth.PrincipalFromContext(ctx)
	if !p.Policy.Admin && !p.Policy.HasKBAccess(kbName(k), true) {
		return res
	}
	now := s.now()
	lastDoctor := cc.cachedDoctorDate(k)
	if !doctorDue(lastDoctor, k.DoctorIntervalDays, now) {
		return res
	}
	s.mu.Lock()
	sid := callerSession(ctx)
	if at, ok := s.nudgedAt[sid]; ok && now.Sub(at) < doctorNudgeWindow {
		s.mu.Unlock()
		return res
	}
	if s.nudgedAt == nil {
		s.nudgedAt = map[string]time.Time{}
	}
	for id, at := range s.nudgedAt { // sessions come and go: drop the expired
		if now.Sub(at) >= doctorNudgeWindow {
			delete(s.nudgedAt, id)
		}
	}
	s.nudgedAt[sid] = now
	s.mu.Unlock()

	all, err := cc.lintFindings(k)
	if err != nil {
		return res
	}
	vis, _ := uiVisibleFindingsFrom(ctx, k, "", all)
	review := 0
	if items, rerr := visibleReview(ctx, k, cc); rerr == nil {
		review = len(items)
	}
	summary := summarizeConformance(vis, review, lastDoctor, k.DoctorIntervalDays, now)
	if summary["doctor_suggested"] != true {
		return res
	}
	res.Content = append(res.Content, ContentBlock{Type: "text", Text: doctorNudgeText(kbName(k), lastDoctor, summary["findings"].(map[string]int), summary["fixable"].(int), review, k.DoctorAssisted())})
	return res
}

// doctorNudgeText is the notice: counts only, never KB content. Unattended
// (the default, D358) tells the agent to run the session itself; assisted
// keeps the operator in the loop.
func doctorNudgeText(kbName, lastDoctor string, bySev map[string]int, fixable, review int, assisted bool) string {
	if lastDoctor == "" {
		lastDoctor = "never"
	}
	action := "Run a kb-doctor session now (skill kb-doctor, unattended): decide every item yourself under its rules, " +
		"record what only a person can answer as a gap, report at the end."
	if assisted {
		action = "Propose a kb-doctor session to the operator (skill kb-doctor); do not start it without their agreement."
	}
	return fmt.Sprintf("cartographer: KB %s - last kb-doctor session %s; %d warnings, %d fixable, %d review items. %s",
		kbName, lastDoctor, bySev[lint.SevWarning], fixable, review, action)
}
