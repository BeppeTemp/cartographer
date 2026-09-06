package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/BeppeTemp/cartographer/internal/audit"
	"github.com/BeppeTemp/cartographer/internal/auth"
)

// requestContext is an alias kept package-local so every tool implementation
// receives the transport request context without importing context itself.
type requestContext = context.Context

// Tool describes an MCP tool registered in the server.
type Tool struct {
	Name        string
	Description string
	// ResourceClass declares the authorization shape of this tool. It is
	// assigned from the exhaustive registry inventory before registration;
	// unknown tools are rejected rather than inheriting a permissive default.
	ResourceClass string
	// ReadOnly marks tools that never mutate KB content (safe under a read-only
	// scope token). See ToolRequiresWrite and the readOnlyTools golden test.
	ReadOnly bool
	// InputSchema is the JSON Schema for the "arguments" parameter.
	InputSchema json.RawMessage
	// Handler receives the request context and raw parameters (JSON object)
	// and returns a result and application error. Application errors go in
	// the ToolResult (isError:true), not as Go errors.
	Handler func(context.Context, json.RawMessage) (ToolResult, error)
}

// Server is the MCP stdio server.
type Server struct {
	version  string
	tools    map[string]*Tool
	toolsOrd []string // maintains registration order for tools/list
	// agentProfile hides advanced tools (advancedToolNames, D65) from
	// tools/list; they remain callable via tools/call. Zero value = full list,
	// so New() keeps its historical behavior; `serve` sets it from
	// config.ToolsProfile (default "agent").
	agentProfile bool
	// toolPrefix, when non-empty, is prepended (as "<toolPrefix>__") to
	// every tool name at RegisterTool time (D102: opt-in per-KB tool-name
	// namespacing for MCP clients with a flat tool namespace, e.g. Kiro).
	// Zero value = unprefixed, byte-identical to pre-D102 behaviour. Set via
	// SetToolNamePrefix, before RegisterKBTools/setupFn runs — see
	// MultiKBServer.MountKBWithPrefix.
	toolPrefix string
	// displayName, when non-empty, overrides the "cartographer" literal
	// reported as serverInfo.name by initialize (D102). Set via
	// SetDisplayName.
	displayName string
	// policyKB is the mounted logical KB name used by authorization rules
	// (SetPolicyKB); it lets the authorizer resolve the right KB even before
	// RegisterKBTools sets kb.KB.AuthName.
	policyKB string
	// authorizer is installed during KB tool registration (installPolicy). It
	// is a closure over immutable registration dependencies, never mutable
	// request-global state.
	authorizer func(context.Context, string, json.RawMessage) error
	// auditLog, if set (SetAuditLog), records an attempt+completion event pair
	// for every tools/call dispatched through this server (D119). Nil (the
	// default) means auditing is off — byte-identical to pre-D119 behaviour.
	auditLog *audit.Log
	// kbName identifies the KB this server serves, recorded on every audit
	// event. Set via SetKBName at mount time.
	kbName string
	// transport is the fixed transport ("stdio" or "http") this Server
	// instance dispatches over, recorded on every audit event. Set via
	// SetTransport at mount time.
	transport string
	// roster tracks per-client request counts, keyed by (clientName,
	// clientVersion, protocolVersion, era). Guarded by mu.
	roster *clientRoster
	// now returns the current time; defaulted to time.Now in New, overridable
	// in tests for determinism.
	now func() time.Time
	mu  sync.Mutex

	// sdkOnce/sdkSrv memoise the official-SDK server this Server is served
	// through (D168). Built on first use, from the tools registered at mount.
	sdkOnce sync.Once
	sdkSrv  *sdkServerHandle

	sdkHTTPOnce sync.Once
	sdkHTTP     http.Handler
}

// authLocalContext returns a context carrying an explicit local-admin
// principal, used for auth-disabled HTTP and trusted stdio transports so
// handlers never infer authority from a missing context value.
func authLocalContext() context.Context {
	return auth.ContextWithPrincipal(context.Background(), auth.LocalAdminPrincipal())
}

// SetAuthorizer installs the transport-neutral policy gate used by both HTTP
// and stdio dispatch. A nil authorizer is fail-closed for non-admin callers.
func (s *Server) SetAuthorizer(fn func(context.Context, string, json.RawMessage) error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.authorizer = fn
}

// authorize resolves the transport-neutral policy decision for tool
// (already prefix-stripped) against args. name=="" gates protocol metadata
// calls (initialize/ping/tools/list): any resolved principal, even a
// restricted one, is enough as long as it can reach the KB in some way. A
// missing principal (no authorizer installed and no admin context) is fail
// closed rather than treated as full access.
func (s *Server) authorize(ctx context.Context, name string, args json.RawMessage) error {
	s.mu.Lock()
	fn := s.authorizer
	s.mu.Unlock()
	if fn == nil {
		if auth.PrincipalFromContext(ctx).Policy.Admin {
			return nil
		}
		return fmt.Errorf("forbidden")
	}
	return fn(ctx, name, args)
}

// New creates a new Server with the given version.
func New(version string) *Server {
	return &Server{
		version: version,
		tools:   make(map[string]*Tool),
		roster:  newClientRoster(),
		now:     time.Now,
	}
}

// SetToolsProfile selects which tools tools/list advertises: "agent" hides
// the advancedToolNames set, anything else ("full", "") advertises everything.
// tools/call is unaffected — hidden tools stay callable by name.
func (s *Server) SetToolsProfile(profile string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.agentProfile = profile == "agent"
}

// SetToolNamePrefix sets the opt-in per-KB tool-name prefix (D102): every
// tool registered afterwards via RegisterTool is renamed
// "<prefix>__<name>". Must be called before the tools are registered
// (RegisterKBTools/setupFn) — it does not rename tools already registered.
// Empty (the default) leaves tool names unchanged.
func (s *Server) SetToolNamePrefix(prefix string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.toolPrefix = prefix
}

// ToolNamePrefix returns the tool-name prefix set by SetToolNamePrefix, or
// "" when tools are registered unprefixed.
func (s *Server) ToolNamePrefix() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.toolPrefix
}

// SetDisplayName overrides the serverInfo.name reported by initialize
// (D102). Empty (the default) keeps the historical "cartographer".
func (s *Server) SetDisplayName(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.displayName = name
}

// SetPolicyKB sets the mounted logical KB name used by authorization rules.
func (s *Server) SetPolicyKB(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.policyKB = name
}

// PolicyKB returns the mounted logical KB name set by SetPolicyKB.
func (s *Server) PolicyKB() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.policyKB
}

// SetAuditLog attaches the operational audit sink (D119): every subsequent
// tools/call dispatched through this Server records an attempt event before
// the tool runs and a completion event afterward (internal/mcpserver/audit.go).
// nil (the default, unchanged from pre-D119) leaves auditing off.
func (s *Server) SetAuditLog(l *audit.Log) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.auditLog = l
}

// SetKBName records the KB name (config.KBSpec-resolved) attributed to every
// audit event this Server emits (D119).
func (s *Server) SetKBName(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.kbName = name
}

// SetTransport records the fixed transport ("stdio" or "http") attributed to
// every audit event this Server emits (D119). A Server instance only ever
// dispatches over one transport for its whole lifetime.
func (s *Server) SetTransport(transport string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.transport = transport
}

// ClientStats returns a snapshot of the client roster: a slice of ClientStat
// rows sorted deterministically by (ClientName, ClientVersion, ProtocolVersion,
// Era), plus the overflow counter. The returned slice is a copy — callers may
// inspect it without holding s.mu.
func (s *Server) ClientStats() ([]ClientStat, int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.roster.stats()
}

// stripToolPrefixLocked removes this server's tool-name prefix (see
// SetToolNamePrefix) from name, if name carries it; otherwise returns name
// unchanged. Callers must already hold s.mu (it does not lock itself, to
// stay reentrant-safe when called from handleToolsList).
func (s *Server) stripToolPrefixLocked(name string) string {
	if s.toolPrefix == "" {
		return name
	}
	p := s.toolPrefix + "__"
	if strings.HasPrefix(name, p) {
		return name[len(p):]
	}
	return name
}

// StripToolPrefix removes this server's tool-name prefix (SetToolNamePrefix,
// D102) from name, if name carries it; otherwise returns name unchanged. The
// transport-neutral policy resolver (installPolicy) always receives this
// canonical name.
func (s *Server) StripToolPrefix(name string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stripToolPrefixLocked(name)
}

// ToolRequiresWrite reports whether name — which may carry this server's
// tool-name prefix (SetToolNamePrefix) — requires write access to the KB.
// It strips the prefix, if any, then delegates to the package-level
// ToolRequiresWrite classification.
func (s *Server) ToolRequiresWrite(name string) bool {
	return ToolRequiresWrite(s.StripToolPrefix(name))
}

// Tools returns a snapshot of all registered tools, keyed by name (for
// introspection/tests, e.g. the ReadOnly golden test).
func (s *Server) Tools() map[string]Tool {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]Tool, len(s.tools))
	for name, t := range s.tools {
		out[name] = *t
	}
	return out
}

// RegisterTool registers an MCP tool. Overwrites if the same name is already
// registered. If a tool-name prefix is set (SetToolNamePrefix, D102), t.Name
// is rewritten to "<prefix>__<name>" here — the single injection point that
// covers every tool, including conditionally-registered ones
// (skill_install, sync_*, artifact_*), without touching individual toolXxx
// constructors.
func (s *Server) RegisterTool(t Tool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.toolPrefix != "" {
		t.Name = s.toolPrefix + "__" + t.Name
	}
	if _, exists := s.tools[t.Name]; !exists {
		s.toolsOrd = append(s.toolsOrd, t.Name)
	}
	s.tools[t.Name] = &t
}

// Run starts the read/write loop on reader/writer under an explicit
// local-admin principal (trusted stdio transport).
// Blocks until EOF on reader or a fatal I/O error.
// Diagnostic logs (if needed) must go to stderr, not to the writer.
func (s *Server) Run(reader io.Reader, writer io.Writer) error {
	return s.RunContext(authLocalContext(), reader, writer)
}

// RunContext starts the read/write loop on reader/writer under ctx, the
// given request context (which must carry the caller's principal —
// PrincipalFromContext returns the zero value, fail-closed, otherwise).
func (s *Server) RunContext(ctx context.Context, reader io.Reader, writer io.Writer) error {
	return s.sdkStdioRun(ctx, reader, writer)
}

// isMetadataMethod reports whether the method is protocol metadata rather than
// resource access.
func isMetadataMethod(m string) bool {
	switch m {
	case "initialize", "ping", "tools/list", "server/discover":
		return true
	}
	return false
}
