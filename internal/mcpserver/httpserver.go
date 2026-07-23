package mcpserver

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

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
		s.handleNotification(&req)
		w.WriteHeader(http.StatusAccepted)
		return
	}

	resp := s.dispatch(&req)

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
func mcpAccessGuard(kbName string, next http.Handler) http.Handler {
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
		var req Request
		if err := json.Unmarshal(body, &req); err == nil {
			if req.Method != "tools/call" {
				needWrite = false
			} else {
				var params struct {
					Name      string `json:"name"`
					Arguments struct {
						ResolveSecrets bool `json:"resolve_secrets"`
					} `json:"arguments"`
				}
				_ = json.Unmarshal(req.Params, &params) // ignore errors: ToolRequiresWrite("") is fail-closed too
				needWrite = ToolRequiresWrite(params.Name)
				if params.Name == "service_get" && params.Arguments.ResolveSecrets {
					needWrite = true
				}
			}
		}

		if !auth.HasAccess(scopes, kbName, needWrite) {
			auth.Forbidden(w)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	result := map[string]interface{}{
		"status":  "ok",
		"version": s.version,
		"ready":   true, // a single-KB server always has its one KB mounted
	}
	json.NewEncoder(w).Encode(result)
}

// handleReady reports readiness: a single-KB server is always ready (its one
// KB is mounted at construction time), unlike MultiKBServer where 0 KBs
// mounted means not ready.
func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{"ready": true})
}

// ListenAndServe starts the HTTP server on the given address (e.g. ":8080").
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

// MountKB registers a KB with the given name. Creates a dedicated MCP server for it.
func (m *MultiKBServer) MountKB(name string, setupFn func(s *Server)) {
	srv := New(m.version)
	setupFn(srv)
	m.servers[name] = srv
	m.kbs = append(m.kbs, KBInfo{Name: name, Status: "normal"})
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
				for name, srv := range m.servers {
					srv := srv
					mcpAccessGuard(name, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						srv.handleMCP(w, r)
					})).ServeHTTP(w, r)
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

// serveKB routes r to the named KB's MCP handler (through the per-KB access
// guard), or responds 404 "unknown kb" if no KB with that name is mounted.
func (m *MultiKBServer) serveKB(w http.ResponseWriter, r *http.Request, kbName string) {
	srv, ok := m.servers[kbName]
	if !ok {
		http.Error(w, fmt.Sprintf("unknown kb %q", kbName), http.StatusNotFound)
		return
	}
	mcpAccessGuard(kbName, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		srv.handleMCP(w, r)
	})).ServeHTTP(w, r)
}
