package mcpserver

import (
	"net/http"
	"net/url"
	"strings"
)

// APICORS is the CORS allow-list of the public read API. It acts only on paths
// under APIPrefix and only on a request that carries an Origin different from
// the server's own (the same-origin rule of OriginGuard is untouched, D906):
//
//   - an origin that equals, after canonicalOrigin, an entry of allowed gets
//     Access-Control-Allow-Origin with that origin, Vary: Origin and the
//     exposed headers; a genuine preflight (OPTIONS with Origin and
//     Access-Control-Request-Method) for GET, HEAD or OPTIONS is answered 204
//     here, from the list alone, without reading a KB;
//   - any other origin gets no CORS header at all (the browser blocks the
//     read), and a genuine preflight from it, or one asking for another
//     method, is a 403 with the API error envelope.
//
// Never "*", never Access-Control-Allow-Credentials: the API takes a bearer
// token in a header, not a cookie, so credentialed mode is not needed.
//
// TRAP: it must sit OUTSIDE authentication. A preflight carries no
// Authorization header by design, so behind the token middleware it would be a
// 401 and the browser would never send the real request. Answering it here also
// keeps auth free of a CORS exemption. /mcp and /api/ui/v1 are never touched:
// they are not under APIPrefix, which keeps the D906 invariant (no allow-list
// for the MCP endpoint or the state-changing routes).
//
// An entry that is not a canonical origin never matches (the config validator
// stops the start before that; this is the second lock).
func APICORS(allowed []string, next http.Handler) http.Handler {
	set := make(map[string]bool, len(allowed))
	for _, o := range allowed {
		if c, ok := canonicalOrigin(o); ok {
			set[c] = true
		}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != APIPrefix && !strings.HasPrefix(r.URL.Path, APIPrefix+"/") {
			next.ServeHTTP(w, r)
			return
		}
		// Every /api/v1 answer varies on Origin, also one with no Origin or a
		// same-origin one: a shared cache must not serve that ACAO-less copy to
		// a listed origin.
		w.Header().Add("Vary", "Origin")
		origin := r.Header.Get("Origin")
		if origin == "" || originAllowed(origin, r.Host) {
			next.ServeHTTP(w, r)
			return
		}
		c, ok := canonicalOrigin(origin)
		listed := ok && set[c]
		preflight := r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != ""
		if preflight {
			if !listed || !corsMethod(r.Header.Get("Access-Control-Request-Method")) {
				writeUIError(w, http.StatusForbidden, "origin_not_allowed", "origin or method not allowed for cross-origin reads", "")
				return
			}
			h := w.Header()
			h.Add("Vary", "Access-Control-Request-Method")
			h.Add("Vary", "Access-Control-Request-Headers")
			h.Set("Access-Control-Allow-Origin", origin)
			h.Set("Access-Control-Allow-Methods", "GET, HEAD, OPTIONS")
			h.Set("Access-Control-Allow-Headers", "Authorization, Last-Event-ID, Content-Type")
			h.Set("Access-Control-Max-Age", "600")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if listed {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Expose-Headers", "Retry-After, Content-Type")
		}
		next.ServeHTTP(w, r)
	})
}

func corsMethod(m string) bool {
	switch strings.ToUpper(strings.TrimSpace(m)) {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	return false
}

// canonicalOrigin normalises an origin for comparison: scheme and host
// lowercased, the default port of the scheme dropped, so http://x:80 and
// http://X are one origin. It refuses (false) anything that is not exactly
// scheme://host[:port] with http or https: a path, query, fragment, userinfo,
// a wildcard or a trailing slash.
func canonicalOrigin(v string) (string, bool) {
	v = strings.TrimSpace(v)
	if v == "" || strings.Contains(v, "*") || strings.HasSuffix(v, "/") {
		return "", false
	}
	u, err := url.Parse(v)
	if err != nil || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return "", false
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", false
	}
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if (scheme == "http" && port == "80") || (scheme == "https" && port == "443") {
		port = ""
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if port != "" {
		host += ":" + port
	}
	return scheme + "://" + host, true
}
