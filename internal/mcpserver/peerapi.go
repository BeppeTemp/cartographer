package mcpserver

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/peers"
)

// PeerAPIPrefix is the client-facing half of peer messaging (D341): the hooks,
// the relay and the Claude Code channel register sessions and collect their
// mail here. Like /api/usage it is client plumbing, not an agent operation, so
// it is HTTP and not MCP, and it sits behind the same auth chain as /mcp.
// Absent unless the hub is enabled.
const PeerAPIPrefix = "/api/peer/v1"

const maxPeerBody = 64 << 10

// EnablePeers routes PeerAPIPrefix to hub. Called by the HTTP server when
// peers.enabled is on, with the same hub its KB tools were registered with.
func (m *MultiKBServer) EnablePeers(hub *peers.Hub) {
	m.peers = hub
}

func (m *MultiKBServer) handlePeerAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		writeUIError(w, http.StatusMethodNotAllowed, uiCodeMethodNotAllow, "use POST", "")
		return
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, maxPeerBody+1))
	if err != nil || len(data) > maxPeerBody {
		writeUIError(w, http.StatusRequestEntityTooLarge, uiCodeInvalidRequest, "request too large", "")
		return
	}
	principal := auth.PrincipalFromContext(r.Context()).ID
	switch strings.TrimPrefix(r.URL.Path, PeerAPIPrefix) {
	case "/register":
		var s peers.Session
		if err := json.Unmarshal(data, &s); err != nil {
			writeUIError(w, http.StatusBadRequest, uiCodeInvalidRequest, "body must be a session object", "")
			return
		}
		// A session may only join KBs the caller can read: the KB is the
		// boundary of who talks to whom, and joining one is reading its
		// membership. An unknown KB is refused the same way, so the route
		// is no existence oracle.
		for _, name := range s.KBs {
			srv, ok := m.servers[name]
			if !ok || !WholeVisible(r.Context(), srv.kbRef, false) {
				writeUIError(w, http.StatusNotFound, uiCodeNotFound, "unknown kb "+name, "kbs")
				return
			}
		}
		got, err := m.peers.Register(principal, s)
		if err != nil {
			writePeerError(w, err)
			return
		}
		roster := map[string][]peers.Session{}
		for _, name := range got.KBs {
			roster[name] = m.peers.List(name)
		}
		writeUIJSON(w, http.StatusOK, map[string]any{"session": got, "peers": roster})
	case "/list":
		var p struct {
			KB string `json:"kb"`
		}
		if err := json.Unmarshal(data, &p); err != nil {
			writeUIError(w, http.StatusBadRequest, uiCodeInvalidRequest, "body must be {kb}", "")
			return
		}
		srv, ok := m.servers[p.KB]
		if !ok || !WholeVisible(r.Context(), srv.kbRef, false) {
			writeUIError(w, http.StatusNotFound, uiCodeNotFound, "unknown kb "+p.KB, "kb")
			return
		}
		writeUIJSON(w, http.StatusOK, map[string]any{"sessions": m.peers.List(p.KB)})
	case "/leave":
		var p struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(data, &p); err != nil {
			writeUIError(w, http.StatusBadRequest, uiCodeInvalidRequest, "body must be {id}", "")
			return
		}
		if err := m.peers.Leave(principal, p.ID); err != nil {
			writePeerError(w, err)
			return
		}
		writeUIJSON(w, http.StatusOK, map[string]bool{"left": true})
	case "/take", "/wait":
		var p struct {
			IDs     []string `json:"ids"`
			Touch   bool     `json:"touch"`
			Timeout int      `json:"timeout_seconds"`
		}
		if err := json.Unmarshal(data, &p); err != nil {
			writeUIError(w, http.StatusBadRequest, uiCodeInvalidRequest, "body must be {ids, touch, timeout_seconds}", "")
			return
		}
		var got map[string][]peers.Message
		if strings.HasSuffix(r.URL.Path, "/take") {
			got, err = m.peers.Take(principal, p.IDs, p.Touch)
		} else {
			timeout := defaultPeerWait
			if p.Timeout > 0 {
				timeout = min(time.Duration(p.Timeout)*time.Second, maxPeerWait)
			}
			got, err = m.peers.Wait(r.Context(), principal, p.IDs, timeout, p.Touch)
		}
		if err != nil {
			writePeerError(w, err)
			return
		}
		writeUIJSON(w, http.StatusOK, map[string]any{"messages": got})
	default:
		writeUINotFound(w)
	}
}

func writePeerError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, peers.ErrUnknown):
		writeUIError(w, http.StatusNotFound, uiCodeNotFound, err.Error(), "")
	case errors.Is(err, peers.ErrForbidden):
		writeUIError(w, http.StatusForbidden, "forbidden", err.Error(), "")
	case errors.Is(err, peers.ErrHubFull):
		writeUIError(w, http.StatusServiceUnavailable, "unavailable", err.Error(), "")
	default:
		writeUIError(w, http.StatusBadRequest, uiCodeInvalidRequest, err.Error(), "")
	}
}
