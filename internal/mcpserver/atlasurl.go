package mcpserver

import (
	"context"
	"net"
	"net/http"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/webui"
)

// atlasKBURL is kb_status's ui_url (#1945): the Atlas deep link to this
// server's KB, "" when the server that mounted it serves no Atlas (stdio,
// web.enabled off, no bundle, a bind to a specific address) or when the caller
// is not on the server's own machine. A remote caller learns neither the port
// nor that the host is loopback-bound, and 127.0.0.1 is no link to it.
func (s *Server) atlasKBURL(ctx context.Context) string {
	if s == nil || s.atlasURL == nil || !isLocalCaller(ctx) {
		return ""
	}
	base := s.atlasURL()
	if base == "" {
		return ""
	}
	if name := s.PolicyKB(); name != "" {
		return webui.KBURL(base, name)
	}
	return base
}

// localCallerKey marks a request that reached the server from its own machine.
// TRAP: it is set from the HTTP request by Handler, never from anything the
// client sends in a tool argument; the key is unexported, so no request body
// can carry it.
type localCallerKey struct{}

// forwardingHeaders are the request headers a reverse proxy adds. A proxy on
// the server's own machine connects from loopback, so its remote users look
// local; any of these says the peer is not the caller.
var forwardingHeaders = []string{"Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto", "X-Real-IP", "Via"}

// withLocalCaller marks r when its TCP peer is a loopback address, no
// forwarding header is present and the caller did not come in through a
// trusted proxy's token.
//
// TRAP: it reads the principal from the request context, so it must run inside
// the authentication middleware (serve wraps MultiKBServer.Handler in it); a
// principal on a proxy token (ProxyToken, and Via when it named a person) says
// the TCP peer is the proxy and not the caller, whatever headers the proxy
// forwarded, and a proxy relaying anonymous traffic on its own token leaves Via
// empty.
func withLocalCaller(r *http.Request) *http.Request {
	if !isLocalRequest(r) {
		return r
	}
	return r.WithContext(context.WithValue(r.Context(), localCallerKey{}, true))
}

func isLocalRequest(r *http.Request) bool {
	if p := auth.PrincipalFromContext(r.Context()); p.Via != "" || p.ProxyToken {
		return false
	}
	for _, h := range forwardingHeaders {
		if len(r.Header.Values(h)) > 0 {
			return false
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if i := strings.IndexByte(host, '%'); i >= 0 { // IPv6 zone
		host = host[:i]
	}
	return client.IsLoopbackHost(host)
}

// isLocalCaller reports whether the request under ctx came from this machine
// (false under stdio, where no HTTP request exists).
func isLocalCaller(ctx context.Context) bool {
	v, _ := ctx.Value(localCallerKey{}).(bool)
	return v
}
