package mcpserver

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
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
	return mux
}

func (s *Server) handleMCP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		s.handleMCPPost(w, r)
	case http.MethodGet:
		http.Error(w, "SSE stream not yet implemented", http.StatusNotImplemented)
	default:
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

	resp := s.dispatch(r.Context(), &req)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(resp)
}

// mcpAccessGuard wraps next (an /mcp handler for a single KB) with per-KB,
// per-tool r/rw scope enforcement. Scopes come from the request context
// (auth.ScopesFromContext, populated by TokenStore.Middleware from the
// token's configured scopes); a nil/empty scope list means full access
// (admin token) and the guard passes through unconditionally.
//
// To decide whether the request needs write access it peeks the JSON-RPC
// body: any method other than "tools/call" (initialize, tools/list, ping,
// ...) is treated as read-only; "tools/call" needs write iff
// ToolRequiresWrite(params.name) — fail-closed, so an unparsable body or an
// unknown tool name requires write. The body is always restored on r.Body
// (via io.NopCloser over the buffered bytes) so the wrapped handler, which
// reads it again from scratch, sees the exact original bytes.
//
// Special case (M4, D47): service_get is classified ReadOnly (it only reads
// frontmatter+body by default), but with arguments.resolve_secrets==true it
// decrypts and returns the service's secrets — access to secrets requires at
// least the same privilege as a write. The tool-name classification in
// ToolRequiresWrite can't see arguments, so this per-argument override lives
// here, at the one place that already parses the JSON-RPC body.
//
// srv is the KB's own *Server: its ToolRequiresWrite method strips this
// server's tool-name prefix (D102), if any, before classifying — so a
// prefixed write tool is still correctly rejected for a read-only scope.
//
// D119: a denial of an actual "tools/call" request is recorded as one
// attempt+completion audit event pair (outcome=unauthorized) via
// srv.auditDenied, since this is the one place that decides the denial —
// dispatch (and its own audit wrapping in handleToolsCall) never runs for a
// request rejected here. Denials of any other JSON-RPC method (e.g. a
// wrong-KB scoped token hitting tools/list) are not tool calls and are not
// audited, matching handleToolsCall's own scope.
func mcpAccessGuard(kbName string, srv *Server, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scopes := auth.ScopesFromContext(r.Context())
		if len(scopes) == 0 {
			next.ServeHTTP(w, r)
			return
		}

		body, err := io.ReadAll(io.LimitReader(r.Body, 2<<20))
		if err != nil {
			http.Error(w, "read error", http.StatusBadRequest)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))

		needWrite := true // fail-closed: an unparsable request requires write access
		isToolCall := false
		var toolName string
		var toolArgs json.RawMessage
		var req Request
		if err := json.Unmarshal(body, &req); err == nil {
			if req.Method != "tools/call" {
				needWrite = false
			} else {
				isToolCall = true
				var params struct {
					Name      string          `json:"name"`
					Arguments json.RawMessage `json:"arguments"`
				}
				_ = json.Unmarshal(req.Params, &params) // ignore errors: ToolRequiresWrite("") is fail-closed too
				toolName, toolArgs = params.Name, params.Arguments
				var resolve struct {
					ResolveSecrets bool `json:"resolve_secrets"`
				}
				_ = json.Unmarshal(params.Arguments, &resolve)
				needWrite = srv.ToolRequiresWrite(toolName)
				if srv.StripToolPrefix(toolName) == "service_get" && resolve.ResolveSecrets {
					needWrite = true
				}
			}
		}

		if !auth.HasAccess(scopes, kbName, needWrite) {
			if isToolCall {
				srv.auditDenied(r, toolName, toolArgs)
			}
			auth.Forbidden(w)
			return
		}
		next.ServeHTTP(w, r)
	})
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

// ListenAndServe starts the HTTP server on the given address (e.g. ":39273").
func (s *Server) ListenAndServe(addr string) error {
	handler := s.HTTPHandler()
	log.Printf("MCP HTTP server listening on %s", addr)
	return http.ListenAndServe(addr, handler)
}

// ListenAndServeWithHandler starts the HTTP server with a custom handler wrapper
// (e.g. for adding auth middleware).
func (s *Server) ListenAndServeWithHandler(addr string, wrap func(http.Handler) http.Handler) error {
	handler := wrap(s.HTTPHandler())
	log.Printf("MCP HTTP server listening on %s", addr)
	return http.ListenAndServe(addr, handler)
}

// ListenAndServeHandler starts an HTTP server with the given handler.
func ListenAndServeHandler(addr string, handler http.Handler) error {
	return http.ListenAndServe(addr, handler)
}

// WellKnownHandler returns a handler for /.well-known/oauth-protected-resource.
func WellKnownHandler(metadataJSON []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(metadataJSON)
	}
}

// FullHTTPHandler returns a combined handler with /mcp, /health, and /.well-known endpoints.
func (s *Server) FullHTTPHandler(oauthMetadata []byte) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", s.handleMCP)
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/ready", s.handleReady)
	if oauthMetadata != nil {
		mux.HandleFunc("/.well-known/oauth-protected-resource", WellKnownHandler(oauthMetadata))
	}

	// CORS headers for browser-based clients.
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		mux.ServeHTTP(w, r)
	})
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

// Handler returns the HTTP handler that routes MCP requests to the correct
// KB server. Three ways to select a KB:
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

		case r.URL.Path == "/ready":
			w.Header().Set("Content-Type", "application/json")
			if len(m.servers) == 0 {
				w.WriteHeader(http.StatusServiceUnavailable)
				json.NewEncoder(w).Encode(map[string]interface{}{"ready": false, "kbs": 0})
				return
			}
			json.NewEncoder(w).Encode(map[string]interface{}{"ready": true})
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
