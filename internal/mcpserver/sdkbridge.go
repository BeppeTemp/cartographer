package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/audit"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/BeppeTemp/cartographer/internal/auth"
)

// This file is the seam between Cartographer's tool registry and the official
// MCP SDK (D168). Everything above it — Tool, ToolResult, RegisterKBTools and
// the ~7k lines of tool implementations — is unchanged; everything the
// protocol itself dictates (JSON-RPC framing, the era envelope, header
// mirroring, version negotiation, tools/list paging) now comes from the SDK
// instead of from protocol.go.
//
// The registry stays the source of truth rather than registering tools
// directly on an sdk.Server: Cartographer's authorization, audit and client
// roster are keyed on the tool name, the agent
// profile hides tools from tools/list while keeping them callable, and an
// unknown tool has a deliberately informative message (D151). None of those
// are protocol concerns, so they stay on this side of the seam and are applied
// by the wrappers below.

// sdkImplementation is the name/version pair the SDK reports as serverInfo.
// The name is the display name when one is set (D102: "cartographer:<kb>" on a
// multi-KB server), otherwise the historical bare "cartographer".
func (s *Server) sdkImplementation() *sdk.Implementation {
	s.mu.Lock()
	defer s.mu.Unlock()
	name := "cartographer"
	if s.displayName != "" {
		name = s.displayName
	}
	return &sdk.Implementation{Name: name, Version: s.version}
}

// readOnlyInstructions is the initialize instructions of a read-only routed
// session: one sentence, so the agent knows why it has no write tool.
const readOnlyInstructions = "This session is read-only: the folder it was started in is bound to no KB, so only read tools are available. " +
	"Bind the folder with `cartographer workspace bind <provider> <path> --kb <name>` to write."

// readMinInstructions is the initialize instructions of an ?access=read-min
// connection (D1184). It names no CLI command: a generic client never gets one.
const readMinInstructions = "This connection is read-only: only the minimal reading tools are listed, " +
	"every other read tool (graph_path, lint, kb_status, work_list, artifact_list...) can still be called by name, " +
	"and every write is refused with read_only_profile."

func (s *Server) sessionInstructions() string {
	if s.readMinSession {
		return readMinInstructions
	}
	if s.readOnlySession {
		return readOnlyInstructions
	}
	return ""
}

// SDKServer returns the SDK server for this Server, building it on first use.
//
// It is built once and memoised because the SDK infers its advertised
// capabilities from the tools present at construction, and because tools are
// registered during mount, before any transport is served. Registering a tool
// after the first request would therefore not be reflected — which is exactly
// the lifecycle Cartographer already has (RegisterKBTools runs at mount).
func (s *Server) SDKServer() *sdk.Server {
	s.sdkOnce.Do(func() { s.sdkSrv = &sdkServerHandle{srv: s.buildSDKServer()} })
	return s.sdkSrv.srv
}

func (s *Server) buildSDKServer() *sdk.Server {
	srv := sdk.NewServer(s.sdkImplementation(), &sdk.ServerOptions{
		// Cartographer implements no optional capability: no prompts, no
		// resources, no completion, and logging has never been served. The SDK
		// defaults to advertising {"logging":{}} "for historical reasons", so
		// an explicit empty value is what keeps the advertised set honest —
		// D151's rule that a capability is advertised only where it is
		// implemented.
		Capabilities: &sdk.ServerCapabilities{},
		// No Instructions here: the SDK server is memoised, so build-time text
		// would be stale and the same for every caller. guideMiddleware sets
		// them per handshake (D1097).
	})

	s.mu.Lock()
	names := append([]string(nil), s.toolsOrd...)
	s.mu.Unlock()

	for _, name := range names {
		s.mu.Lock()
		t := s.tools[name]
		s.mu.Unlock()
		if t == nil {
			continue
		}
		schema := t.InputSchema
		if schema == nil {
			schema = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		var annotations *sdk.ToolAnnotations
		if t.ReadOnly {
			annotations = &sdk.ToolAnnotations{ReadOnlyHint: true}
		}
		srv.AddTool(&sdk.Tool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: schema,
			Annotations: annotations,
		}, s.sdkToolHandler(t.Name))
	}

	// Order matters: the SDK applies receiving middleware outermost-first, so
	// the gate runs before the roster records a request it may reject, and the
	// unknown-tool answer is produced before the SDK's own routing rejects the
	// name with a bare protocol error.
	srv.AddReceivingMiddleware(
		s.metadataGateMiddleware(),
		s.rosterMiddleware(),
		s.unknownToolMiddleware(),
		s.agentProfileMiddleware(),
		s.guideMiddleware(),
	)
	return srv
}

// sdkToolHandler wraps one registered tool as an SDK tool handler, reproducing
// the authorization, audit and error semantics tools/call had before the SDK
// carried the protocol.
//
// Every failure below returns a *result* with IsError set, never a Go error: a
// Go error out of an SDK ToolHandler becomes a JSON-RPC protocol error, which
// is a different thing on the wire and would change what every existing client
// sees for an ordinary denial or a failing tool.
func (s *Server) sdkToolHandler(name string) sdk.ToolHandler {
	return func(ctx context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		ctx = withCallerAuthor(withCallerSession(withCallerClient(ctx, req), req), req)
		// The unknown-argument check runs inside the shared path, after the
		// unknown-tool answer and the authorization decision (#1279): a caller
		// that may not use the tool gets the denial, not a schema probe.
		result, err := s.callToolChecked(ctx, name, json.RawMessage(req.Params.Arguments), s.unknownArgumentsError)
		if err != nil {
			return nil, err
		}
		return sdkResult(result), nil
	}
}

// unknownArgumentsError refuses, as JSON-RPC -32602 invalid params, a call that
// carries a top-level argument its tool's input schema does not declare (#1279):
// a typo such as `if_mach` would otherwise drop the check it was meant to
// request without a word. A schema that allows additional properties, or that
// declares no properties object, is not enforced; free-form nested objects are
// not entered.
func (s *Server) unknownArgumentsError(tool *Tool, args json.RawMessage) error {
	if tool == nil || len(args) == 0 {
		return nil
	}
	name := tool.Name
	schema := tool.InputSchema
	if schema == nil {
		schema = json.RawMessage(`{"type":"object","properties":{}}`)
	}
	var decl struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Additional json.RawMessage            `json:"additionalProperties"`
	}
	if json.Unmarshal(schema, &decl) != nil || decl.Properties == nil || string(decl.Additional) == "true" {
		return nil
	}
	var given map[string]json.RawMessage
	if json.Unmarshal(args, &given) != nil {
		return nil // not an object: the handler reports it
	}
	var unknown []string
	for k := range given {
		if _, ok := decl.Properties[k]; !ok && !(tool.AcceptsKB && k == routedKBArgument) && !slices.Contains(tool.HiddenArguments, k) {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	sort.Strings(unknown)
	return &jsonrpc.Error{
		Code:    jsonrpc.CodeInvalidParams,
		Message: fmt.Sprintf("invalid params: unknown argument(s) %s for tool %s", strings.Join(unknown, ", "), name),
	}
}

// callTool performs one tools/call end to end: the unknown-tool answer, the
// authorization decision, the audit pair, and the tool itself. It is the whole
// of what Cartographer does with a tool call, with nothing about the wire in
// it, so the SDK handler, the unknown-tool middleware and the tests that used
// to reach the old dispatch entry point all share one path.
//
// It never returns a Go error: every failure is a result with IsError set. A
// Go error out of an SDK ToolHandler becomes a JSON-RPC protocol error, which
// is a different thing on the wire and would change what every existing client
// sees for an ordinary denial or a failing tool.
func (s *Server) callTool(ctx context.Context, name string, args json.RawMessage) ToolResult {
	result, _ := s.callToolChecked(ctx, name, args, nil)
	return result
}

// callToolChecked is callTool with an optional pre-dispatch check, run once
// the tool is known and the call is authorized, before the audit pair opens.
// A check error is audited as a denial (D119/D132) and returned as the Go
// error, which the SDK handler turns into a JSON-RPC protocol error; it is the
// only Go error this path produces.
func (s *Server) callToolChecked(ctx context.Context, name string, args json.RawMessage, check func(*Tool, json.RawMessage) error) (ToolResult, error) {
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}

	principal := auth.PrincipalFromContext(ctx).ID

	s.mu.Lock()
	tool := s.tools[name]
	s.mu.Unlock()

	if tool == nil {
		// D151: a client sees one message for "not registered", "hidden by the
		// agent profile" and "absent from this build", and only the server can
		// tell them apart. Arguments are deliberately not resolved or recorded
		// for a name this server has no allow-list entry for.
		msg := s.legacyToolMessage(ctx, name)
		if msg == "" {
			msg = "tool not found: " + name
		}
		result := errorResult(msg)
		call, rejected, ok := s.beginAuditCall(principal, name, false, nil)
		if !ok {
			return rejected, nil
		}
		call.end(s, audit.OutcomeUnknownTool, result)
		return result, nil
	}

	if !exactArgumentKeys(args) {
		// Refused before authorization: see exactArgumentKeys.
		s.auditDenied(principal, name, args)
		return errorResult(deny(denyBadArguments).Error()), nil
	}

	if err := s.authorize(ctx, name, args); err != nil {
		// D119/D132: the denial is audited at the point the decision happens,
		// not where the call would have been dispatched.
		s.auditDenied(principal, name, args)
		return errorResult(err.Error()), nil
	}

	if check != nil {
		if err := check(tool, args); err != nil {
			s.auditDenied(principal, name, args)
			return ToolResult{}, err
		}
	}

	resources := extractResources(name, args)
	call, rejected, ok := s.beginAuditCall(principal, name, tool.ReadOnly, resources)
	if !ok {
		// Required mode, attempt-phase append failed: reject before the tool
		// ever runs — see beginAuditCall.
		return rejected, nil
	}

	result, err := tool.Handler(ctx, args)
	call.end(s, classifyOutcome(result, err), result)
	if err != nil {
		return errorResult("internal error: " + err.Error()), nil
	}
	return s.withDoctorNudge(ctx, name, result), nil
}

// sdkResult converts Cartographer's ToolResult into the SDK's.
func sdkResult(r ToolResult) *sdk.CallToolResult {
	content := make([]sdk.Content, 0, len(r.Content))
	for _, block := range r.Content {
		content = append(content, &sdk.TextContent{Text: block.Text})
	}
	return &sdk.CallToolResult{Content: content, IsError: r.IsError}
}

// unknownToolMiddleware answers tools/call for a name this server did not
// register, instead of letting the SDK reject it with a bare "unknown tool"
// protocol error.
//
// The distinction is D151's: a client sees one message for "not registered",
// "hidden by the agent profile" and "absent from this build", and only the
// server can tell them apart — which is how a documented capability once
// stayed unfound. The answer is a result with IsError, not a protocol error,
// because that is what every other tool-level failure returns; a caller that
// branches on isError should not have to also branch on transport errors to
// learn it named the wrong tool.
func (s *Server) unknownToolMiddleware() sdk.Middleware {
	return func(next sdk.MethodHandler) sdk.MethodHandler {
		return func(ctx context.Context, method string, req sdk.Request) (sdk.Result, error) {
			if method != "tools/call" {
				return next(ctx, method, req)
			}
			params, ok := req.GetParams().(*sdk.CallToolParamsRaw)
			if !ok || params == nil {
				return next(ctx, method, req)
			}
			s.mu.Lock()
			_, known := s.tools[params.Name]
			s.mu.Unlock()
			if known {
				return next(ctx, method, req)
			}
			return sdkResult(s.callTool(ctx, params.Name, params.Arguments)), nil
		}
	}
}

// metadataGateMiddleware is the fail-closed check that used to sit at the top
// of dispatchMethod: protocol metadata is not resource access, but a caller
// with no resolvable principal must still be denied rather than implicitly
// treated as having full access.
//
// tools/call is deliberately not gated here — it carries its own per-tool
// authorization in sdkToolHandler, where the tool name and arguments are
// available to the policy.
func (s *Server) metadataGateMiddleware() sdk.Middleware {
	return func(next sdk.MethodHandler) sdk.MethodHandler {
		return func(ctx context.Context, method string, req sdk.Request) (sdk.Result, error) {
			if isMetadataMethod(method) {
				if err := s.authorize(ctx, "", json.RawMessage(`{}`)); err != nil {
					// A denial is a JSON-RPC error here, where the old
					// hand-written dispatch returned a success envelope
					// carrying isError. isError is a tools/call concept: the
					// result types of tools/list and server/discover have no
					// place to put it, and a client that reads the envelope
					// literally saw a *successful* empty tool list where it
					// should have seen a refusal. A coded error is the shape
					// the spec has for this.
					return nil, &jsonrpc.Error{Code: jsonrpc.CodeInvalidRequest, Message: err.Error()}
				}
			}
			return next(ctx, method, req)
		}
	}
}

// rosterMiddleware records one request per dispatched call in the client
// roster (D132), keyed by the identity the SDK resolved from the request.
func (s *Server) rosterMiddleware() sdk.Middleware {
	return func(next sdk.MethodHandler) sdk.MethodHandler {
		return func(ctx context.Context, method string, req sdk.Request) (sdk.Result, error) {
			// Notifications are not requests: they get no response and the
			// roster counts what clients ask of this server. Counting them
			// would also double every session, since the initialized
			// notification immediately follows initialize.
			if !strings.HasPrefix(method, "notifications/") {
				s.recordSDKRequest(method, req)
			}
			return next(ctx, method, req)
		}
	}
}

// guideMiddleware sets the instructions of an initialize and of a
// server/discover result (D1097): the operating guide of this connection, or
// what a provisioned one gets (sessionInstructions). The text is edited on the
// result, which the SDK builds fresh per call, rather than at build time: the
// SDK server is memoised and its views are shared by every URL that resolves to
// the same set, while the guide depends on the caller and on files read at each
// handshake. The discover result varies by caller, so it is not public.
func (s *Server) guideMiddleware() sdk.Middleware {
	return func(next sdk.MethodHandler) sdk.MethodHandler {
		return func(ctx context.Context, method string, req sdk.Request) (sdk.Result, error) {
			res, err := next(ctx, method, req)
			if err != nil {
				return res, err
			}
			switch r := res.(type) {
			case *sdk.InitializeResult:
				r.Instructions = s.handshakeInstructions(ctx)
			case *sdk.DiscoverResult:
				r.Instructions = s.handshakeInstructions(ctx)
				r.CacheScope = "private"
			}
			return res, nil
		}
	}
}

// agentProfileMiddleware hides advanced tools from tools/list while leaving
// them callable through tools/call (D65). The SDK lists every tool added to
// it, so the filter is applied to the result rather than at registration —
// registering them is what keeps them callable. A read-only routed session
// (readOnlySession) or a token view that writes nothing (tokenReadOnly) also
// drops every write tool from the list; those stay
// registered so a call is refused with a reason, not with "tool not found".
func (s *Server) agentProfileMiddleware() sdk.Middleware {
	return func(next sdk.MethodHandler) sdk.MethodHandler {
		return func(ctx context.Context, method string, req sdk.Request) (sdk.Result, error) {
			res, err := next(ctx, method, req)
			if err != nil || method != "tools/list" {
				return res, err
			}
			s.mu.Lock()
			hide := s.agentProfile
			readOnly := s.readOnlySession || s.tokenReadOnly
			readMin := s.readMinSession
			s.mu.Unlock()
			if !hide && !readOnly && !readMin {
				return res, nil
			}
			list, ok := res.(*sdk.ListToolsResult)
			if !ok {
				return res, nil
			}
			if readMin {
				// The allowlist wins over the agent profile; order is readMinTools'.
				byName := make(map[string]*sdk.Tool, len(list.Tools))
				for _, t := range list.Tools {
					byName[t.Name] = t
				}
				kept := make([]*sdk.Tool, 0, len(readMinTools))
				for _, n := range readMinTools {
					if t, ok := byName[n]; ok {
						kept = append(kept, t)
					}
				}
				list.Tools = kept
				return list, nil
			}
			kept := make([]*sdk.Tool, 0, len(list.Tools))
			for _, t := range list.Tools {
				if hide && ToolAdvanced(t.Name) {
					continue
				}
				// A read-only session lists only what it may call; the call
				// itself is refused by the routed authorizer (denyUnboundFolder).
				if readOnly && ToolRequiresWrite(t.Name) {
					continue
				}
				kept = append(kept, t)
			}
			list.Tools = kept
			return list, nil
		}
	}
}

// sdkHTTPHandler serves this Server over Streamable HTTP.
//
// Stateless and JSONResponse together reproduce the transport Cartographer has
// always offered and documents: every POST is self-contained, no session id is
// read or issued, and a request gets one complete JSON-RPC response rather
// than an event stream. They are not tuning knobs — a session-bearing or
// streaming server would be a different product contract (docs/transport-auth.md
// §Stateless behavior).
func (s *Server) sdkHTTPHandler() http.Handler {
	s.sdkHTTPOnce.Do(func() {
		s.sdkHTTP = sdk.NewStreamableHTTPHandler(
			func(*http.Request) *sdk.Server { return s.SDKServer() },
			&sdk.StreamableHTTPOptions{Stateless: true, JSONResponse: true},
		)
	})
	return s.sdkHTTP
}

// sdkStdioRun serves this Server over stdio until the peer disconnects or ctx
// is cancelled. The reader and writer are adapted to the ReadCloser/WriteCloser
// the SDK transport wants; closing them is the caller's business, so the
// adapters' Close is a no-op.
func (s *Server) sdkStdioRun(ctx context.Context, reader io.Reader, writer io.Writer) error {
	reader, writer = guardStdio(reader, writer)
	return s.SDKServer().Run(ctx, &sdk.IOTransport{
		Reader: nopReadCloser{reader},
		Writer: nopWriteCloser{writer},
	})
}

type nopReadCloser struct{ io.Reader }

func (nopReadCloser) Close() error { return nil }

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

// recordSDKRequest records one client-roster row (D132) for a dispatched
// request. Identity comes from what the SDK resolved: ClientInfo from the
// handshake or from the request's _meta, and the negotiated protocol version.
// A request that carries no identity is recorded as "unknown" rather than
// dropped — the roster's purpose is to show what is actually talking to this
// server, and an anonymous client is exactly the case an operator needs to
// see before retiring an era.
func (s *Server) recordSDKRequest(method string, req sdk.Request) {
	name, version := "unknown", "unknown"
	protocol := ""
	apply := func(info *sdk.Implementation) {
		if info == nil {
			return
		}
		if info.Name != "" {
			name = info.Name
		}
		if info.Version != "" {
			version = info.Version
		}
	}

	type identified interface {
		ClientInfo() *sdk.Implementation
		ProtocolVersion() string
	}
	if r, ok := req.(identified); ok {
		apply(r.ClientInfo())
		protocol = r.ProtocolVersion()
	}

	// initialize is the one request whose identity is not yet on the session:
	// the SDK adopts clientInfo as a *result* of handling it, and this
	// middleware runs before that. Reading the params directly is what keeps a
	// client from being filed as "unknown" on the very request that introduces
	// it — which would put a spurious anonymous row in front of every real one.
	if p, ok := req.GetParams().(*sdk.InitializeParams); ok && p != nil {
		apply(p.ClientInfo)
		if protocol == "" {
			protocol = p.ProtocolVersion
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.roster.record(name, version, protocol, eraForProtocol(protocol), s.now())
}

// eraForProtocol labels a roster row with the era its protocol version belongs
// to. The label is what makes the roster answer the only question it exists to
// answer — whether anything still speaks the old era (issue #118) — now that
// the SDK, not Cartographer, decides how each request is framed.
func eraForProtocol(protocolVersion string) protocolEra {
	if protocolVersion == ProtocolVersion20260728 {
		return era20260728
	}
	return eraHandshake
}

// sdkServerHandle boxes the memoised SDK server so Server's zero value stays
// usable (a nil handle simply means "not built yet").
type sdkServerHandle struct{ srv *sdk.Server }
