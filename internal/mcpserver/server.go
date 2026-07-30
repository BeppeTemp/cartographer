package mcpserver

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"

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

// toolDescriptor is the representation exported to the client for tools/list.
type toolDescriptor struct {
	Name        string           `json:"name"`
	Description string           `json:"description"`
	InputSchema json.RawMessage  `json:"inputSchema"`
	Annotations *toolAnnotations `json:"annotations,omitempty"`
}

// toolAnnotations carries the MCP tool annotations exposed in tools/list
// (spec: https://modelcontextprotocol.io/ — "annotations"). Only ReadOnlyHint
// is populated today, derived from Tool.ReadOnly (kept in sync with
// readOnlyToolNames by TestReadOnlyToolsGolden).
type toolAnnotations struct {
	ReadOnlyHint bool `json:"readOnlyHint"`
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
	mu        sync.Mutex

	writeMu sync.Mutex    // serializes writes to the shared encoder
	enc     *json.Encoder // active stdio encoder; nil when not in Run (e.g. HTTP)
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
	scanner := bufio.NewScanner(reader)
	// Increase buffer for large messages.
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	enc := json.NewEncoder(writer)
	enc.SetEscapeHTML(false)

	// Store the encoder so Notify can push notifications on the same stream.
	s.writeMu.Lock()
	s.enc = enc
	s.writeMu.Unlock()
	defer func() {
		s.writeMu.Lock()
		s.enc = nil
		s.writeMu.Unlock()
	}()

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}

		var req Request
		if err := json.Unmarshal(line, &req); err != nil {
			resp := errorResponse(nil, ErrCodeParseError, "parse error: "+err.Error())
			s.writeMu.Lock()
			enc.Encode(resp)
			s.writeMu.Unlock()
			continue
		}

		if req.JSONRPC != "2.0" {
			if !req.isNotification() {
				resp := errorResponse(req.ID, ErrCodeInvalidRequest, "jsonrpc must be '2.0'")
				s.writeMu.Lock()
				enc.Encode(resp)
				s.writeMu.Unlock()
			}
			continue
		}

		// Notifications do not receive a response.
		if req.isNotification() {
			s.handleNotification(ctx, &req)
			continue
		}

		// IMPORTANT: do not hold writeMu across dispatch — Notify is called
		// within the dispatch goroutine and acquires writeMu itself.
		// Stdio is a single, already-trusted local caller: the context carries
		// the local-admin principal, which is what audit events are attributed
		// to (see docs/transport-auth.md).
		resp := s.dispatch(ctx, &req)
		s.writeMu.Lock()
		enc.Encode(resp)
		s.writeMu.Unlock()
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scanner: %w", err)
	}
	return nil
}

// handleNotification handles messages without an id (no response expected).
func (s *Server) handleNotification(_ context.Context, req *Request) {
	// notifications/initialized: no-op
	// other notification methods: silently ignored
}

// Notify writes a JSON-RPC notification to the active stdio encoder. It is a no-op
// when no stdio encoder is set (e.g. the HTTP transport), which gracefully degrades
// push (Layer 3) to the pull-based Layers 1-2.
func (s *Server) Notify(method string, params any) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.enc == nil {
		return nil
	}
	msg := map[string]any{"jsonrpc": "2.0", "method": method, "params": params}
	return s.enc.Encode(msg)
}

// dispatch routes the request to the appropriate method. initialize, ping and
// tools/list are protocol metadata, not resource access, but a caller with no
// resolvable principal must still be denied (fail-closed) rather than
// implicitly treated as full access.
func (s *Server) dispatch(ctx context.Context, req *Request) Response {
	if req.Method == "initialize" || req.Method == "ping" || req.Method == "tools/list" {
		if err := s.authorize(ctx, "", json.RawMessage(`{}`)); err != nil {
			return successResponse(req.ID, errorResult(err.Error()))
		}
	}
	switch req.Method {
	case "initialize":
		return s.handleInitialize(req)
	case "ping":
		return successResponse(req.ID, map[string]interface{}{})
	case "tools/list":
		return s.handleToolsList(req)
	case "tools/call":
		// The principal attributed to an audit event (D119) is read from the
		// context rather than passed alongside it: since D118 the authenticated
		// principal already travels there, and a second channel could disagree
		// with the one authorization actually used.
		return s.handleToolsCall(ctx, req)
	default:
		return errorResponse(req.ID, ErrCodeMethodNotFound, "method not found: "+req.Method)
	}
}

// handleInitialize handles the initial MCP negotiation.
func (s *Server) handleInitialize(req *Request) Response {
	// Extract the protocol version requested by the client.
	var params struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if len(req.Params) > 0 {
		json.Unmarshal(req.Params, &params)
	}

	// Negotiation: use the requested version if provided, otherwise ours.
	negotiated := SupportedProtocolVersion
	if params.ProtocolVersion != "" {
		// In M1 we accept any version provided by the client (echo).
		// Future: validate the version and downgrade if necessary.
		negotiated = params.ProtocolVersion
	}

	s.mu.Lock()
	name := "cartographer"
	if s.displayName != "" {
		name = s.displayName
	}
	s.mu.Unlock()

	result := map[string]interface{}{
		"protocolVersion": negotiated,
		"capabilities": map[string]interface{}{
			"tools":     map[string]interface{}{},
			"resources": map[string]interface{}{},
			"skills":    map[string]interface{}{"listChanged": true},
		},
		"serverInfo": map[string]interface{}{
			"name":    name,
			"version": s.version,
		},
	}
	return successResponse(req.ID, result)
}

// handleToolsList responds with the list of registered tools.
func (s *Server) handleToolsList(req *Request) Response {
	s.mu.Lock()
	defer s.mu.Unlock()

	descriptors := make([]toolDescriptor, 0, len(s.toolsOrd))
	for _, name := range s.toolsOrd {
		if s.agentProfile && ToolAdvanced(s.stripToolPrefixLocked(name)) {
			continue
		}
		t := s.tools[name]
		schema := t.InputSchema
		if schema == nil {
			schema = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		var annotations *toolAnnotations
		if t.ReadOnly {
			annotations = &toolAnnotations{ReadOnlyHint: true}
		}
		descriptors = append(descriptors, toolDescriptor{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: schema,
			Annotations: annotations,
		})
	}
	return successResponse(req.ID, map[string]interface{}{"tools": descriptors})
}

// handleToolsCall routes the call to the correct tool. Authorization runs
// before the handler for every call — a denial never reaches tool logic — and
// an attempt+completion audit event pair is recorded around the dispatch when
// an audit sink is attached (SetAuditLog, D119; see audit.go).
func (s *Server) handleToolsCall(ctx context.Context, req *Request) Response {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return errorResponse(req.ID, ErrCodeInvalidParams, "invalid params: "+err.Error())
	}

	s.mu.Lock()
	tool, ok := s.tools[params.Name]
	s.mu.Unlock()

	args := params.Arguments
	if args == nil {
		args = json.RawMessage(`{}`)
	}

	canonicalName := s.StripToolPrefix(params.Name)
	externalName := ""
	if canonicalName != params.Name {
		externalName = params.Name
	}
	principal := auth.PrincipalFromContext(ctx).ID

	if err := s.authorize(ctx, canonicalName, args); err != nil {
		return successResponse(req.ID, errorResult(err.Error()))
	}

	if !ok {
		// Unknown tool: never resolve/record arguments for a tool this server
		// has no allow-list entry for; the outcome itself names the case.
		result := errorResult("tool not found: " + params.Name)
		call, rejected, ok := s.beginAuditCall(principal, canonicalName, externalName, false, nil)
		if !ok {
			return successResponse(req.ID, rejected)
		}
		call.end(s, audit.OutcomeUnknownTool, result)
		return successResponse(req.ID, result)
	}

	resources := extractResources(canonicalName, args)
	call, rejected, ok := s.beginAuditCall(principal, canonicalName, externalName, tool.ReadOnly, resources)
	if !ok {
		// Required mode, attempt-phase append failed: reject before the tool
		// ever runs — see beginAuditCall.
		return successResponse(req.ID, rejected)
	}

	result, err := tool.Handler(ctx, args)
	call.end(s, classifyOutcome(result, err), result)
	if err != nil {
		// Internal server error (not an application error): return isError in the result.
		return successResponse(req.ID, errorResult("internal error: "+err.Error()))
	}
	return successResponse(req.ID, result)
}
