package mcpserver

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
)

// OriginGuard rejects requests to the MCP endpoint whose Origin header names a
// site the operator has not allowed. Validating Origin has been a MUST since
// the 2025-03-26 revision (D128): without it any page the user visits can
// script a request to a Cartographer bound to localhost or to a private
// address, and DNS rebinding turns that into full KB access.
//
// The rule, applied to /mcp and /mcp/<kb> only:
//
//   - no Origin header — a non-browser client (an MCP client, curl, a probe):
//     allowed, because the header is what a browser adds and only a browser
//     can be tricked into sending one;
//   - allowed is non-empty: the origin must appear in it verbatim (scheme,
//     host and port, compared case-insensitively, a trailing "/" ignored);
//     "*" in the list disables the check and restores the pre-D128 behaviour;
//   - allowed is empty (the default): only the request's own Host is accepted,
//     compared on the authority alone so a TLS-terminating proxy does not have
//     to be told which scheme it terminated.
//
// The empty-list default keeps a same-machine browser working without config,
// but it is not by itself a rebinding defence: a rebound name arrives with
// Origin and Host both naming the attacker, so they match. An operator who
// exposes the server to browsers should list the origins explicitly.
//
// The guard runs before authentication: a caller who cannot pass this check
// should not get as far as having its token read.
func OriginGuard(allowed []string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isMCPPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		origin := r.Header.Get("Origin")
		if !originAllowed(allowed, origin, r.Host) {
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
func originAllowed(allowed []string, origin, host string) bool {
	if origin == "" {
		return true
	}
	want := normalizeOrigin(origin)
	if len(allowed) > 0 {
		for _, a := range allowed {
			if a == "*" {
				return true
			}
			if normalizeOrigin(a) == want {
				return true
			}
		}
		return false
	}
	if host == "" {
		return false
	}
	u, err := url.Parse(want)
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
