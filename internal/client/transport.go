package client

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
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
		// https -> http is refused even to loopback and even with the opt-in:
		// a secure endpoint never legitimately sends the client back to cleartext.
		if len(via) > 0 && strings.EqualFold(via[len(via)-1].URL.Scheme, "https") && !strings.EqualFold(req.URL.Scheme, "https") {
			return fmt.Errorf("refusing redirect from %s to %s: https to http downgrade", via[len(via)-1].URL.Redacted(), req.URL.Redacted())
		}
		return CheckServerURL(req.URL.String(), allowInsecure)
	}
}

// serverCA is the extra trust of the configured server (server.ca_file, WP6):
// the system roots plus one private CA file, honoured for one host:port only.
//
// The registry is keyed by canonical host:port, so the configs of several
// directories (the main one and a workspace's) can each register their server
// and none clears another's entry.
var serverCA struct {
	sync.RWMutex
	byHost map[string]serverTrust
}

type serverTrust struct {
	pool *x509.CertPool
	rt   http.RoundTripper
	// owner is the config file that registered the entry (SetServerCAFrom),
	// "" when the caller did not say.
	owner string
}

// CAFileError is a CA file that cannot be used: unreadable, or holding no PEM
// certificate. It is typed and does not unwrap to its cause on purpose: the
// cause of an unreadable file is os.ErrNotExist, and callers that read
// errors.Is(err, os.ErrNotExist) as "no .cartographer.yaml, first connect"
// (LoadOrDefault, loadConfigForWrite) would take a missing CA file for a
// missing config and write defaults over the user's settings.
type CAFileError struct {
	// Source is where the path came from: "server.ca_file" or
	// "CARTOGRAPHER_CA_FILE". Empty when the caller of SetServerCA did not say.
	Source string
	Path   string
	Reason string
}

func (e *CAFileError) Error() string {
	src := e.Source
	if src == "" {
		src = "server.ca_file"
	}
	return fmt.Sprintf("client: the CA file %s (%s) cannot be used: %s", e.Path, src, e.Reason)
}

// canonicalHost is the lower-case host:port of u, the port defaulted from the
// scheme (443 for https, 80 for http), so "example.com" and "example.com:443"
// name the same endpoint.
func canonicalHost(u *url.URL) string {
	port := u.Port()
	if port == "" {
		port = "80"
		if strings.EqualFold(u.Scheme, "https") {
			port = "443"
		}
	}
	return net.JoinHostPort(strings.ToLower(u.Hostname()), port)
}

// SetServerCA registers path, a PEM file, as an extra root for requests to the
// host:port of serverURL, replacing only that host's entry. The system roots
// stay trusted and no other host trusts the file. An empty path removes the
// entry of serverURL's host; an empty path and URL clear the registry. A file
// that cannot be read or holds no certificate is a *CAFileError, never a
// silent fallback.
func SetServerCA(path, serverURL string) error {
	return SetServerCAFrom("", path, serverURL)
}

// ReleaseServerCA removes the entry of serverURL's host only when owner
// registered it (SetServerCAFrom): the config that registered a CA file is the
// one whose removal of the key withdraws it, another directory's config without
// one leaves it alone.
func ReleaseServerCA(owner, serverURL string) {
	u, err := url.Parse(serverURL)
	if err != nil || u.Host == "" {
		return
	}
	serverCA.Lock()
	defer serverCA.Unlock()
	h := canonicalHost(u)
	if tr, ok := serverCA.byHost[h]; ok && tr.owner == owner {
		delete(serverCA.byHost, h)
	}
}

// SetServerCAFrom is SetServerCA recording owner, the config file that asked,
// for ReleaseServerCA.
func SetServerCAFrom(owner, path, serverURL string) error {
	serverCA.Lock()
	defer serverCA.Unlock()
	if path == "" {
		if serverURL == "" {
			serverCA.byHost = nil
		} else if u, err := url.Parse(serverURL); err == nil && u.Host != "" {
			delete(serverCA.byHost, canonicalHost(u))
		}
		return nil
	}
	pem, err := os.ReadFile(path)
	if err != nil {
		// %v, not %w: see CAFileError.
		return &CAFileError{Path: path, Reason: fmt.Sprintf("%v", err)}
	}
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if !pool.AppendCertsFromPEM(pem) {
		return &CAFileError{Path: path, Reason: "it holds no PEM certificate"}
	}
	u, err := url.Parse(serverURL)
	if err != nil || u.Host == "" {
		return fmt.Errorf("client: invalid server URL %q", serverURL)
	}
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	if serverCA.byHost == nil {
		serverCA.byHost = map[string]serverTrust{}
	}
	serverCA.byHost[canonicalHost(u)] = serverTrust{pool: pool, rt: t, owner: owner}
	return nil
}

// ServerRoots returns the pool to verify host (host or host:port, https) with,
// or nil when the system roots apply: mcpprobe's own dial path uses it.
func ServerRoots(host string) *x509.CertPool {
	u, err := url.Parse("https://" + host)
	if err != nil || u.Host == "" {
		return nil
	}
	serverCA.RLock()
	defer serverCA.RUnlock()
	return serverCA.byHost[canonicalHost(u)].pool
}

// scopedTransport sends the registered server host through the transport that
// trusts its CA and every other host through http.DefaultTransport.
type scopedTransport struct{}

func (scopedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	serverCA.RLock()
	tr, ok := serverCA.byHost[canonicalHost(req.URL)]
	serverCA.RUnlock()
	if ok {
		return tr.rt.RoundTrip(req)
	}
	return http.DefaultTransport.RoundTrip(req)
}

// ScopedTransport is the transport of HTTPClient: for a caller that builds its
// own http.Client (headers, redirect rule) and must still trust the server CA.
func ScopedTransport() http.RoundTripper { return scopedTransport{} }

// HTTPClient returns a copy of base whose redirects obey RedirectPolicy. A base
// with no Transport also trusts the server's CA file for that host (SetServerCA).
func HTTPClient(base *http.Client, allowInsecure bool) *http.Client {
	hc := *base
	hc.CheckRedirect = RedirectPolicy(allowInsecure)
	if hc.Transport == nil {
		hc.Transport = scopedTransport{}
	}
	return &hc
}
