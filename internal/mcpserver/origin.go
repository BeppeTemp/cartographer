package mcpserver

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
)

// OriginGuard rejects requests to the MCP endpoint whose Origin header names a
// site other than the one serving it. Validating Origin has been a MUST since
// the 2025-03-26 revision (D128): without it any page the user visits can
// script a request to a Cartographer bound to localhost or to a private
// address, and DNS rebinding turns that into full KB access.
//
// The rule is fixed (D906), applied to /mcp and /mcp/<kb> only:
//
//   - no Origin header — a non-browser client (an MCP client, curl, a probe):
//     allowed, because the header is what a browser adds and only a browser
//     can be tricked into sending one;
//   - an Origin header: allowed only when its host and port are the request's
//     own Host, compared on the authority alone and case-insensitively, so a
//     TLS-terminating proxy does not have to be told which scheme it
//     terminated, and an Atlas behind an SSO proxy, which serves the page and
//     the API from one origin, keeps working (D308).
//
// There is no list of other origins to allow: a page on another site has no
// business scripting this server, and the setting that widened the rule was a
// way to turn the protection off. The same-origin rule is not by itself a
// rebinding defence: a rebound name arrives with Origin and Host both naming
// the attacker, so they match; authentication is what stops that.
//
// The guard runs before authentication: a caller who cannot pass this check
// should not get as far as having its token read.
func OriginGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isMCPPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		origin := r.Header.Get("Origin")
		if !originAllowed(origin, r.Host) {
			writeOriginRejected(w)
			return
		}
		if origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Add("Vary", "Origin")
		}
		next.ServeHTTP(w, r)
	})
}

func isMCPPath(p string) bool {
	return p == "/mcp" || strings.HasPrefix(p, "/mcp/")
}

// originAllowed implements the rule documented on OriginGuard. host is the
// request's Host header (authority, possibly with a port).
func originAllowed(origin, host string) bool {
	if origin == "" {
		return true
	}
	if host == "" {
		return false
	}
	u, err := url.Parse(normalizeOrigin(origin))
	if err != nil || u.Host == "" {
		return false
	}
	return strings.EqualFold(u.Host, host)
}

func normalizeOrigin(v string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(v), "/"))
}

// writeOriginRejected answers a refused origin with 403 and a JSON-RPC error
// carrying no id: the request was refused before it was parsed, so there is no
// id to correlate it with.
func writeOriginRejected(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	_ = json.NewEncoder(w).Encode(errorResponse(nil, ErrCodeInvalidRequest, "origin not allowed"))
}
