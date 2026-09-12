package mcpserver

import (
	"encoding/json"
	"fmt"
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

// handleMCP serves one KB's MCP endpoint through the official SDK (D168).
// POST remains the whole transport: the SDK's stateless mode answers GET and
// DELETE with 405, which is what 2026-07-28 requires now that neither the
// event stream nor sessions exist.
func (s *Server) handleMCP(w http.ResponseWriter, r *http.Request) {
	normalizeStreamableAccept(r)
	s.sdkHTTPHandler().ServeHTTP(w, r)
}

// normalizeStreamableAccept ensures POST requests carry an Accept header that
// satisfies the Go MCP SDK's streamable HTTP validator (streamableAccepts).
// Cartographer operates with JSONResponse: true (stateless, zero SSE streams on
// POST), but the SDK strictly requires both application/json and text/event-stream.
// Real-world clients (such as Google Antigravity) omit text/event-stream on client
// notifications (notifications/roots/list_changed), which would otherwise cause
// an unnecessary 400 Bad Request.
func normalizeStreamableAccept(r *http.Request) {
	if r.Method != http.MethodPost {
		return
	}
	accepts := r.Header.Values("Accept")
	if len(accepts) == 0 {
		r.Header.Set("Accept", "application/json, text/event-stream")
		return
	}
	hasJSON, hasStream := false, false
	for _, val := range accepts {
		for _, part := range strings.Split(val, ",") {
			base, _, _ := strings.Cut(strings.TrimSpace(part), ";")
			switch strings.ToLower(strings.TrimSpace(base)) {
			case "application/json", "application/*":
				hasJSON = true
			case "text/event-stream", "text/*":
				hasStream = true
			case "*/*":
				hasJSON = true
				hasStream = true
			}
		}
	}
	if !hasJSON {
		r.Header.Add("Accept", "application/json")
	}
	if !hasStream {
		r.Header.Add("Accept", "text/event-stream")
	}
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

// auditGate is the one rule both the single-KB and multi-KB readiness paths
// apply (D119/D176): no sink contributes nothing, an unhealthy required-mode
// sink makes the server not ready. Shared because two implementations of one
// readiness rule is how they drifted apart in the first place.
func auditGate(st *audit.State) bool { return st == nil || st.Ready }

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	// A single-KB server always has its one KB mounted; only the audit gate
	// can make it not ready.
	st := s.auditState()
	result := map[string]interface{}{
		"status":  "ok",
		"version": s.version,
	}
	if st != nil {
		result["audit"] = st
	}
	result["ready"] = auditGate(st)
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
	st := s.auditState()
	ready := auditGate(st)
	var auditInfo interface{}
	if st != nil {
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
	// Audit is this KB's audit-sink state when one is attached (D119/D176).
	// Omitted when there is no sink. Reported per KB rather than folded into a
	// single boolean so an operator knows where to look.
	Audit interface{} `json:"audit,omitempty"`
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
	// routed is the optional single endpoint advertising the union of the
	// mounted KBs' tools once, with the KB as a tool argument (D187). Nil —
	// the default — means only the per-KB endpoints are served. See
	// routedmount.go.
	routed *Server
	// routedNames are the KB names the routed mount serves, in deterministic
	// order, used to decide whether `kb` may be omitted and to name them in
	// the error when it may not.
	routedNames []string
}

// readiness folds every mounted KB's audit state into one verdict, and returns
// the per-KB view alongside it (D176).
//
// Readiness gates on a required-mode audit sink (D119) so an operator — or a
// readinessProbe — notices before the next required-mode call is rejected. The
// single-KB handlers did that; this one did not, and serveHTTP builds a
// MultiKBServer unconditionally, so on the HTTP path the audit-aware code was
// unreachable and a server that would refuse every write reported itself ready.
//
// Any degraded KB makes the process not ready: a probe must pick one answer,
// and the conservative one is the only safe choice.
func (m *MultiKBServer) readiness() (ready bool, kbs []KBInfo, degraded []string) {
	ready = len(m.servers) > 0
	kbs = make([]KBInfo, len(m.kbs))
	copy(kbs, m.kbs)
	for i := range kbs {
		srv, ok := m.servers[kbs[i].Name]
		if !ok {
			continue
		}
		st := srv.auditState()
		if st == nil {
			continue
		}
		kbs[i].Audit = st
		if !auditGate(st) {
			ready = false
			degraded = append(degraded, kbs[i].Name)
		}
	}
	return ready, kbs, degraded
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
			// Liveness: always 200 with status "ok", even when not ready.
			// auth.isPublicPath exempts this path, and probes depend on the
			// shape. Only `ready` reflects the audit gate (D176).
			w.Header().Set("Content-Type", "application/json")
			ready, kbs, _ := m.readiness()
			result := map[string]interface{}{
				"status":  "ok",
				"version": m.version,
				"kbs":     kbs,
				"ready":   ready,
			}
			// D187: the client needs to know the mount topology before it
			// writes a single MCP entry, and /health is the probe it already
			// performs first. Emitted only when routing is on, so an older
			// client and a per-KB server stay byte-identical.
			if m.routed != nil {
				result["mount_mode"] = "routed"
				result["routed_path"] = RoutedMountPath
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
			ready, _, degraded := m.readiness()
			if !ready {
				w.WriteHeader(http.StatusServiceUnavailable)
				body := map[string]interface{}{"ready": false, "kbs": len(m.servers)}
				if len(degraded) > 0 {
					// Name them: a single boolean tells an operator that
					// something is wrong and nothing about where to look.
					body["degraded"] = degraded
				}
				json.NewEncoder(w).Encode(body)
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

		// D187: the routed mount. The KB travels in the tool arguments here, so
		// a ?kb= on this URL is a second channel for the same choice — refused
		// rather than silently preferred, the same rule /mcp/<name> already
		// applies to a conflicting ?kb=. The guard is on m.routed, not on the
		// path alone: with no routed mount enabled the path falls through to
		// /mcp/<name>, so a KB that happens to be named "routed" keeps its own
		// endpoint instead of being shadowed by a mode nobody turned on.
		case r.URL.Path == RoutedMountPath && m.routed != nil:
			if r.URL.Query().Get("kb") != "" {
				http.Error(w, "conflicting kb selection: the routed mount takes the KB as a tool argument, not as a query parameter", http.StatusBadRequest)
				return
			}
			m.routed.handleMCP(w, r)
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
