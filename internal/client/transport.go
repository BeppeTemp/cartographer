package client

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// InsecureHTTPEnv is the environment variable that opts a run into cleartext
// http:// to a non-loopback host, for one-off and unattended use.
const InsecureHTTPEnv = "CARTOGRAPHER_ALLOW_INSECURE_HTTP"

// IsLoopbackHost reports whether host (no port, no brackets) is the loopback
// interface: "localhost" (case-insensitive, trailing dot ignored) or any IP
// for which net.ParseIP(...).IsLoopback() holds (127.0.0.0/8, ::1, IPv4-mapped).
// It parses the address instead of matching a prefix: "127.0.0.1.example.com"
// is a DNS name, not loopback.
func IsLoopbackHost(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// CheckServerURL is the one gate every client request passes: the bearer token
// must not leave for a non-loopback http:// URL unless allowInsecure is set.
// https is always accepted; any other scheme, or an unparsable URL, is refused.
func CheckServerURL(rawURL string, allowInsecure bool) error {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return fmt.Errorf("client: invalid server URL %q", rawURL)
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
		return nil
	case "http":
		if allowInsecure || IsLoopbackHost(u.Hostname()) {
			return nil
		}
		return fmt.Errorf("refusing %s: plain http:// to a non-loopback host sends the bearer token in clear and the responses are unauthenticated; "+
			"move the server behind https://, or accept the risk explicitly with `cartographer connect --allow-insecure-http` "+
			"(re-run connect with the same flags; it only sets the key) or %s=1; nothing was modified", u.Redacted(), InsecureHTTPEnv)
	default:
		return fmt.Errorf("client: unsupported server URL scheme %q in %s (use https://)", u.Scheme, u.Redacted())
	}
}

// RedirectPolicy is the http.Client CheckRedirect that keeps the gate in force
// across redirects: Go keeps Authorization on a same-host redirect, so an
// https:// endpoint answering 302 to http:// would otherwise leak the token.
// A redirect whose target fails CheckServerURL is refused.
func RedirectPolicy(allowInsecure bool) func(req *http.Request, via []*http.Request) error {
	return func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		return CheckServerURL(req.URL.String(), allowInsecure)
	}
}

// HTTPClient returns a copy of base whose redirects obey RedirectPolicy.
func HTTPClient(base *http.Client, allowInsecure bool) *http.Client {
	hc := *base
	hc.CheckRedirect = RedirectPolicy(allowInsecure)
	return &hc
}
