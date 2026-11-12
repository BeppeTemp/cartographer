package webui

import (
	"fmt"
	"net"
	"net/url"

	"github.com/BeppeTemp/cartographer/internal/client"
)

// URL is the browser address of the Atlas served on a server http address
// ("127.0.0.1:39273", ":39273", "0.0.0.0:9090", "[::]:9090"). A bare port and
// a wildcard bind become 127.0.0.1: a server can listen on a wildcard, but a
// browser cannot open one. "" when addr is empty or unparseable. Shared by
// `cartographer service status` (the ui: line), the tray and kb_status, so
// the three print the same address.
func URL(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return ""
	}
	if wildcardHost(host) {
		host = "127.0.0.1"
	}
	return fmt.Sprintf("http://%s%s", net.JoinHostPort(host, port), MountPath)
}

// LoopbackURL is URL for the one address kb_status may report (#1945): a
// loopback bind (client.IsLoopbackHost: 127.0.0.0/8, ::1, localhost) or a wildcard one, which only the
// machine itself reaches as 127.0.0.1. A bind to a specific other address is
// "": naming it would tell every authenticated caller where the server listens,
// and the operator, not the server, knows the address users reach it by.
func LoopbackURL(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return ""
	}
	if !wildcardHost(host) && !client.IsLoopbackHost(host) {
		return ""
	}
	return URL(addr)
}

// wildcardHost reports a bind to every interface: no host, 0.0.0.0, ::.
func wildcardHost(host string) bool {
	if host == "" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsUnspecified()
}

// KBURL is the Atlas URL that opens one KB (the UI's ?kb= view state). The
// name is only ever a query value, escaped here.
func KBURL(uiURL, kb string) string {
	return uiURL + "?" + url.Values{"kb": {kb}}.Encode()
}
