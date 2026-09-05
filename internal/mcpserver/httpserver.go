package mcpserver

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/audit"
	"github.com/BeppeTemp/cartographer/internal/auth"
)

// HTTPHandler returns an http.Handler that serves MCP over Streamable HTTP.
// POST /mcp accepts a JSON-RPC 2.0 request and returns a JSON response.
// GET /mcp opens an SSE stream (optional, not yet implemented).
// GET /health returns 200 OK with a JSON status body (liveness).
// GET /ready returns readiness (single-KB server is always ready).
func (s *Server) HTTPHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", s.handleMCP)
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/ready", s.handleReady)
	mux.HandleFunc("/clients", s.handleClients)
	return mux
}

func (s *Server) handleMCP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		s.handleMCPPost(w, r)
	default:
		// POST is the whole transport (D128). GET used to answer 501 as a
		// placeholder for the SSE stream, and DELETE would have ended a
		// session; 2026-07-28 removed both the stream and sessions, and
		// requires 405 for either.
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleMCPPost(w http.ResponseWriter, r *http.Request) {
	ct := r.Header.Get("Content-Type")
	if ct != "" && ct != "application/json" {
		http.Error(w, "content-type must be application/json", http.StatusUnsupportedMediaType)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 2*1024*1024))
	if err != nil {
		http.Error(w, "read error", http.StatusBadRequest)
		return
	}

	var req Request
	if err := json.Unmarshal(body, &req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		resp := errorResponse(nil, ErrCodeParseError, "parse error: "+err.Error())
		json.NewEncoder(w).Encode(resp)
		return
	}

	if req.JSONRPC != "2.0" {
		if req.isNotification() {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		resp := errorResponse(req.ID, ErrCodeInvalidRequest, "jsonrpc must be '2.0'")
		json.NewEncoder(w).Encode(resp)
		return
	}

	if req.isNotification() {
		s.handleNotification(r.Context(), &req)
		w.WriteHeader(http.StatusAccepted)
		return
	}

	// Mcp-Session-Id and Last-Event-ID may arrive from a client that still
	// believes in sessions and resumable streams. Both are ignored, on purpose
	// and by doing nothing: this server has never had per-connection state and
	// 2026-07-28 removed the concept, so there is nothing to look up and
	// nothing to echo back.

	if err := req.resolveEra(r.Header.Get("MCP-Protocol-Version")); err != nil {
		writeRPC(w, http.StatusBadRequest, errorResponse(req.ID, ErrCodeInvalidParams, err.Error()))
		return
	}
	if req.era == era20260728 {
		if failure := validateMirrorHeaders(r, &req); failure != "" {
			writeRPC(w, http.StatusBadRequest, errorResponse(req.ID, ErrCodeHeaderMismatch, failure))
			return
		}
	}

	resp := s.dispatch(r.Context(), &req)
	writeRPC(w, statusForResponse(req.era, resp), resp)
}

// writeRPC writes a JSON-RPC response with the given HTTP status.
func writeRPC(w http.ResponseWriter, status int, resp Response) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(resp)
}

// statusForResponse maps a JSON-RPC error onto the HTTP status the 2026-07-28
// revision requires for it. A handshake-era response is always HTTP 200,
// errors included: that is what the era's clients expect, and
// internal/client/client.go treats any other status as an opaque transport
// failure.
func statusForResponse(era protocolEra, resp Response) int {
	if era != era20260728 || resp.Error == nil {
		return http.StatusOK
	}
	switch resp.Error.Code {
	case ErrCodeMethodNotFound:
		return http.StatusNotFound
	case ErrCodeUnsupportedProtocolVersion, ErrCodeHeaderMismatch:
		return http.StatusBadRequest
	}
	return http.StatusOK
}

// validateMirrorHeaders checks the headers 2026-07-28 requires on every POST
// against the body they mirror, and returns a description of the first
// disagreement, or "" when they all agree.
//
// The point of the check is that the headers exist for intermediaries that
// route or rate-limit without parsing a JSON-RPC body — which means a client
// could describe itself one way to the proxy and another way to the server.
// The body remains the only thing this server acts on for authorization,
// which happens later in dispatch (the authorizer installed by
// installPolicy, internal/mcpserver/policy.go); the headers are validated
// against it here and then discarded.
//
// Header names are matched case-insensitively (net/http canonicalises them);
// values are compared exactly.
func validateMirrorHeaders(r *http.Request, req *Request) string {
	version := r.Header.Get("MCP-Protocol-Version")
	switch {
	case version == "":
		return "missing required header MCP-Protocol-Version"
	case req.protocolVersion != "" && version != req.protocolVersion:
		return "MCP-Protocol-Version does not match _meta." + metaKeyProtocolVersion
	}

	method := r.Header.Get("Mcp-Method")
	switch {
	case method == "":
		return "missing required header Mcp-Method"
	case method != req.Method:
		return "Mcp-Method does not match the request method"
	}

	if req.Method != "tools/call" {
		return ""
	}
	var params struct {
		Name string `json:"name"`
	}
	_ = json.Unmarshal(req.Params, &params)
	name := r.Header.Get("Mcp-Name")
	switch {
	case name == "":
		return "missing required header Mcp-Name"
	case decodeMcpName(name) != params.Name:
		return "Mcp-Name does not match params.name"
	}
	return ""
}

// decodeMcpName unwraps the "=?base64?<payload>?=" form a client uses when the
// name does not fit in a header field (a non-ASCII tool name, say). Anything
// else, and anything that fails to decode, is returned unchanged — a mangled
// value then fails the comparison it was decoded for, which is the right
// outcome anyway.
func decodeMcpName(v string) string {
	const prefix, suffix = "=?base64?", "?="
	if !strings.HasPrefix(v, prefix) || !strings.HasSuffix(v, suffix) {
		return v
	}
	payload := v[len(prefix) : len(v)-len(suffix)]
	decoded, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return v
	}
	return string(decoded)
}

// auditState returns this Server's attached audit sink health (D119), or nil
// if no sink is attached (SetAuditLog never called) — the pre-D119 default.
func (s *Server) auditState() *audit.State {
	s.mu.Lock()
	l := s.auditLog
	s.mu.Unlock()
	if l == nil {
		return nil
	}
	st := l.State()
	return &st
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	ready := true // a single-KB server always has its one KB mounted
	result := map[string]interface{}{
		"status":  "ok",
		"version": s.version,
	}
	if st := s.auditState(); st != nil {
		result["audit"] = st
		ready = ready && st.Ready
	}
	result["ready"] = ready
	json.NewEncoder(w).Encode(result)
}

// handleReady reports readiness: a single-KB server is always ready (its one
// KB is mounted at construction time), unlike MultiKBServer where 0 KBs
// mounted means not ready — UNLESS an attached audit sink in required mode is
// unhealthy (D119: readiness gates on the sink so an operator, or a
// readinessProbe, notices before the next required-mode call is rejected).
func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ready := true
	var auditInfo interface{}
	if st := s.auditState(); st != nil {
		ready = st.Ready
		auditInfo = st
	}
	w.Header().Set("Content-Type", "application/json")
	if !ready {
		w.WriteHeader(http.StatusServiceUnavailable)
	} else {
		w.WriteHeader(http.StatusOK)
	}
	body := map[string]interface{}{"ready": ready}
	if auditInfo != nil {
		body["audit"] = auditInfo
	}
	json.NewEncoder(w).Encode(body)
}

func (s *Server) handleClients(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	stats, overflow := s.ClientStats()
	by := s.PolicyKB()
	rows := make([]ClientStat, 0, len(stats))
	for _, st := range stats {
		st.KB = by
		rows = append(rows, st)
	}
	result := map[string]interface{}{
		"clients": rows,
	}
	if overflow > 0 {
		result["overflow"] = overflow
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// KBInfo holds metadata about a mounted KB for kb_list responses.
type KBInfo struct {
	Name   string `json:"name"`
	Root   string `json:"root"`
	Status string `json:"status"` // "normal", "syncing", "needs-resolution"
	// ToolPrefix is the effective tool-name prefix (D102) this KB's tools were
	// registered under, e.g. "ai_team" for tools named "ai_team__concept_read".
	// Empty (omitted) for an unprefixed KB — the same shape a pre-D120 client
	// already tolerates (see client.HealthKB). Set once, at mount time, from
	// the exact sanitised value MountKBWithPrefix passed to
	// Server.SetToolNamePrefix (D120): clients discover it here rather than
	// re-deriving config.ResolveToolPrefix themselves.
	ToolPrefix string `json:"tool_prefix,omitempty"`
	// Capabilities is what this KB is allowed to do, keyed by gate name, with
	// the configuration key that controls each one (D151). Advertised here so
	// `cartographer doctor` can report a capability that is off without a
	// session: a client otherwise has no way to ask.
	Capabilities map[string]KBCapability `json:"capabilities,omitempty"`
}

// KBCapability is one gate's state plus the configuration key controlling it.
type KBCapability struct {
	State   string `json:"state"`
	Setting string `json:"setting"`
}

// MultiKBServer wraps multiple KB instances served by a single HTTP server.
type MultiKBServer struct {
	servers map[string]*Server // one MCP server per KB
	kbs     []KBInfo
	version string
}

// NewMultiKBServer creates a multi-KB server.
func NewMultiKBServer(version string) *MultiKBServer {
	return &MultiKBServer{
		servers: make(map[string]*Server),
		version: version,
	}
}

// MountKB registers a KB with the given name, tool names unprefixed. Creates
// a dedicated MCP server for it.
func (m *MultiKBServer) MountKB(name string, setupFn func(s *Server)) {
	// prefix == "" never fails MountKBWithPrefix's validation (no tool name
	// grows), so the error is unreachable here.
	_ = m.MountKBWithPrefix(name, "", setupFn)
}

// maxToolNameLen is the conservative per-tool-name budget (D102) enforced
// once a tool-name prefix is set: Kiro (and MCP clients generally) may
// reject or exclude a tool whose name is too long, and some clients add
// their own "@server/" prefix on top — 48 leaves room for that.
const maxToolNameLen = 48

// MountKBWithPrefix mounts a KB whose tool names are all rewritten to
// "<prefix>__<tool>" (D102: opt-in per-KB tool-name namespacing for MCP
// clients with a flat tool namespace, e.g. Kiro CLI — Claude Code, Codex and
// OpenCode already namespace tools per server and need no prefix). An empty
// prefix leaves tool names unchanged — the default, byte-identical to
// pre-D102 behaviour.
//
// prefix is assumed already sanitised and shape-validated (see
// config.ResolveToolPrefix): this only enforces the tool-name length budget
// (maxToolNameLen), which needs the KB's actual registered tool names and so
// can only be checked after setupFn runs. On a budget violation the KB is
// not mounted and an error naming the KB and the offending tool is
// returned.
func (m *MultiKBServer) MountKBWithPrefix(name, prefix string, setupFn func(s *Server)) error {
	srv := New(m.version)
	srv.SetPolicyKB(name)
	if prefix != "" {
		srv.SetToolNamePrefix(prefix)
	}
	setupFn(srv)
	if prefix != "" {
		for _, toolName := range srv.toolsOrd {
			if len(toolName) > maxToolNameLen {
				return fmt.Errorf("KB %q: tool name %q (%d chars) exceeds the %d-char budget after applying tool_prefix %q; use a shorter prefix",
					name, toolName, len(toolName), maxToolNameLen, prefix)
			}
		}
	}
	m.servers[name] = srv
	m.kbs = append(m.kbs, KBInfo{Name: name, Status: "normal", ToolPrefix: prefix})
	return nil
}

// SetKBCapabilities records a mounted KB's capability map for /health. Called
// after MountKBWithPrefix by the caller that owns the *kb.KB, so the mount
// signature stays unchanged (D151).
func (m *MultiKBServer) SetKBCapabilities(name string, caps map[string]KBCapability) {
	for i := range m.kbs {
		if m.kbs[i].Name == name {
			m.kbs[i].Capabilities = caps
			return
		}
	}
}

// resourceBaseURL reconstructs this server's own externally-visible base URL
// (scheme://host, no path) from one request, for RFC 9728's self-describing
// "resource" and "authorization_servers" fields (D132): cartographer
// validates its own static bearer tokens rather than delegating to a
// separate OAuth authorization server (docs/transport-auth.md), so there is
// no distinct issuer to plumb through config — the server names itself in
// both fields. scheme is inferred from X-Forwarded-Proto (set by a
// TLS-terminating reverse proxy) or, failing that, from whether the
// connection itself is TLS; r.Host already carries the port, if any.
// X-Forwarded-Proto is client-controlled when no proxy overwrites it, so only
// the two schemes this server can actually be reached on are honoured, and
// only the first hop of a proxy chain ("https, http") is read.
func resourceBaseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
		first := strings.TrimSpace(strings.Split(proto, ",")[0])
		if first == "http" || first == "https" {
			scheme = first
		}
	}
	return scheme + "://" + r.Host
}

// Handler returns the HTTP handler that routes MCP requests to the correct
// KB server, plus /health, /ready, /clients and the RFC 9728
// well-known/oauth-protected-resource metadata endpoint (D132; auth.go's
// isPublicPath exempts the same path from authentication). Three ways to
// select a KB:
//   - bare /mcp: auto-routes when exactly one KB is mounted;
//   - /mcp?kb=<name>: explicit selection by query parameter;
//   - /mcp/<name>: explicit selection by path.
//
// /mcp/<name> and ?kb= may not disagree: if both are present and name the
// same KB, path wins as the explicit route; if they differ, the request is
// rejected with 400 (conflicting kb selection) rather than silently
// preferring one over the other.
func (m *MultiKBServer) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/health":
			w.Header().Set("Content-Type", "application/json")
			result := map[string]interface{}{
				"status":  "ok",
				"version": m.version,
				"kbs":     m.kbs,
				"ready":   len(m.servers) > 0,
			}
			json.NewEncoder(w).Encode(result)
			return

		case r.URL.Path == auth.WellKnownProtectedResourcePath:
			if r.Method != http.MethodGet {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			base := resourceBaseURL(r)
			w.Write(auth.ProtectedResourceMetadata(base, base))
			return

		case r.URL.Path == "/ready":
			w.Header().Set("Content-Type", "application/json")
			if len(m.servers) == 0 {
				w.WriteHeader(http.StatusServiceUnavailable)
				json.NewEncoder(w).Encode(map[string]interface{}{"ready": false, "kbs": 0})
				return
			}
			json.NewEncoder(w).Encode(map[string]interface{}{"ready": true})
			return

		case r.URL.Path == "/clients":
			if r.Method != http.MethodGet {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			// Collect KB names and sort them for deterministic output.
			names := make([]string, 0, len(m.servers))
			for name := range m.servers {
				names = append(names, name)
			}
			sort.Strings(names)
			var overflow int64
			allRows := make([]ClientStat, 0)
			for _, name := range names {
				srv := m.servers[name]
				stats, o := srv.ClientStats()
				overflow += o
				for _, st := range stats {
					st.KB = name
					allRows = append(allRows, st)
				}
			}
			result := map[string]interface{}{"clients": allRows}
			if overflow > 0 {
				result["overflow"] = overflow
			}
			json.NewEncoder(w).Encode(result)
			return

		case r.URL.Path == "/mcp":
			kbName := r.URL.Query().Get("kb")

			// Single-KB mode: if only one KB is mounted, use it as default.
			if kbName == "" && len(m.servers) == 1 {
				for _, srv := range m.servers {
					srv := srv
					srv.handleMCP(w, r)
					return
				}
			}

			if kbName == "" {
				http.Error(w, "kb parameter required", http.StatusBadRequest)
				return
			}
			m.serveKB(w, r, kbName)
			return

		case strings.HasPrefix(r.URL.Path, "/mcp/"):
			pathName := strings.TrimPrefix(r.URL.Path, "/mcp/")
			if queryName := r.URL.Query().Get("kb"); queryName != "" && queryName != pathName {
				http.Error(w, "conflicting kb selection", http.StatusBadRequest)
				return
			}
			m.serveKB(w, r, pathName)
			return

		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	})
}

// serveKB routes r to the named KB's MCP handler, or responds 404 "unknown
// kb" if no KB with that name is mounted. Per-tool/per-resource
// authorization happens centrally in Server.dispatch (installPolicy), not
// here.
func (m *MultiKBServer) serveKB(w http.ResponseWriter, r *http.Request, kbName string) {
	srv, ok := m.servers[kbName]
	if !ok {
		http.Error(w, fmt.Sprintf("unknown kb %q", kbName), http.StatusNotFound)
		return
	}
	srv.handleMCP(w, r)
}
