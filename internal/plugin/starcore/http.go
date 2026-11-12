package starcore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/plugin/starcore/internal/seam"
	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"
)

// netState is the network state of one call: its request count and the
// transport settings. A call is one goroutine, but the counters are atomic anyway.
type netState struct {
	cc       *CallCtx
	version  string
	seams    seam.NetSeams
	requests atomic.Int32

	mu         sync.Mutex
	proxyAddrs map[string]bool // proxies the environment chose, which the dial check lets through
}

func newNetState(cc *CallCtx, version string, s *seam.NetSeams) *netState {
	n := &netState{cc: cc, version: version, proxyAddrs: map[string]bool{}}
	if s != nil {
		n.seams = *s
	}
	if n.seams.Lookup == nil {
		n.seams.Lookup = func(ctx context.Context, host string) ([]netip.Addr, error) {
			return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		}
	}
	if n.seams.Proxy == nil {
		n.seams.Proxy = http.ProxyFromEnvironment // the allowlist is on the request host, not on the proxy
	}
	return n
}

func httpModule() *starlarkstruct.Module {
	return &starlarkstruct.Module{Name: "http", Members: starlark.StringDict{
		"request": starlark.NewBuiltin("http.request", httpRequest),
	}}
}

var methodRe = regexp.MustCompile(`^(GET|HEAD|POST|PUT|PATCH|DELETE|OPTIONS)$`)

// forbiddenHeaders are the ones the host owns: routing, framing and hop-by-hop.
var forbiddenHeaders = map[string]bool{
	"Host": true, "Content-Length": true, "Connection": true, "Keep-Alive": true,
	"Proxy-Authenticate": true, "Proxy-Authorization": true, "Proxy-Connection": true,
	"Te": true, "Trailer": true, "Transfer-Encoding": true, "Upgrade": true,
}

// redirectSafeHeaders are the script-set headers that follow a redirect to
// another host; they carry no credential.
var redirectSafeHeaders = map[string]bool{"Accept": true, "Accept-Language": true, "Content-Type": true, "User-Agent": true}

func httpRequest(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var (
		method, rawURL, body string
		headers              starlark.Value = starlark.None
		timeoutV             starlark.Value = starlark.None
	)
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "method", &method, "url", &rawURL, "headers?", &headers, "body?", &body, "timeout?", &timeoutV); err != nil {
		return nil, err
	}
	c, err := CallPhase(thread, b.Name())
	if err != nil {
		return nil, err
	}
	if c.Perms == nil || len(c.Perms.Net) == 0 {
		return nil, DeniedErr("http.request needs permissions.net in the manifest")
	}
	n := c.net
	method = strings.ToUpper(method)
	if !methodRe.MatchString(method) {
		return nil, fmt.Errorf("http.request: method %q is not GET, HEAD, POST, PUT, PATCH, DELETE or OPTIONS", client.Escape(method))
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("http.request: invalid url: %s", client.Escape(err.Error()))
	}
	if err := n.checkURL(u); err != nil {
		return nil, err
	}
	if len(body) > MaxHTTPRequestBytes {
		return nil, LimitErr("http.request: the body is %d bytes, over the %d cap", len(body), MaxHTTPRequestBytes)
	}
	timeout := HTTPTimeout
	switch t := timeoutV.(type) {
	case starlark.NoneType:
	case starlark.Int, starlark.Float:
		f, _ := starlark.AsFloat(t)
		if !(f > 0) || f > 3600 {
			return nil, errors.New("http.request: timeout must be above 0 and at most 3600 seconds")
		}
		timeout = time.Duration(f * float64(time.Second))
	default:
		return nil, fmt.Errorf("http.request: timeout must be a number of seconds, not %s", t.Type())
	}
	if n.requests.Add(1) > MaxHTTPRequests {
		return nil, LimitErr("http.request: more than %d requests in one call", MaxHTTPRequests)
	}

	ctx, cancel := context.WithTimeout(c.Ctx, timeout)
	defer cancel()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), rd)
	if err != nil {
		return nil, fmt.Errorf("http.request: %s", client.Escape(err.Error()))
	}
	req.Header.Set("User-Agent", "cartographer-plugin/"+c.Plugin+"/"+n.version)
	if d, ok := headers.(*starlark.Dict); ok {
		for _, kv := range d.Items() {
			k, ok1 := starlark.AsString(kv[0])
			v, ok2 := starlark.AsString(kv[1])
			if !ok1 || !ok2 {
				return nil, errors.New("http.request: headers must map strings to strings")
			}
			if forbiddenHeaders[http.CanonicalHeaderKey(k)] {
				return nil, DeniedErr("http.request: the %q header is set by the host", k)
			}
			req.Header.Set(k, v)
		}
	} else if headers != starlark.None {
		return nil, fmt.Errorf("http.request: headers must be a dict, not %s", headers.Type())
	}

	tr := n.transport()
	defer tr.CloseIdleConnections()
	hc := &http.Client{Transport: tr, CheckRedirect: n.checkRedirect}
	resp, err := hc.Do(req)
	if err != nil {
		var de *deniedError
		var le *limitError
		switch {
		case errors.As(err, &de):
			return nil, de
		case errors.As(err, &le):
			return nil, le
		}
		return nil, fmt.Errorf("http.request: %s", client.Escape(err.Error()))
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxHTTPResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("http.request: reading the response: %s", client.Escape(err.Error()))
	}
	if len(data) > MaxHTTPResponseBytes {
		return nil, LimitErr("http.request: the response is over the %d byte cap", MaxHTTPResponseBytes)
	}
	if !utf8.Valid(data) {
		return nil, errors.New("http.request: the response body is not valid UTF-8")
	}
	names := make([]string, 0, len(resp.Header))
	for k := range resp.Header {
		names = append(names, k)
	}
	sort.Strings(names)
	hd := starlark.NewDict(len(names))
	for _, k := range names {
		_ = hd.SetKey(starlark.String(strings.ToLower(k)), starlark.String(strings.Join(resp.Header.Values(k), ", ")))
	}
	out := starlark.NewDict(3)
	_ = out.SetKey(starlark.String("status"), starlark.MakeInt(resp.StatusCode))
	_ = out.SetKey(starlark.String("headers"), hd)
	_ = out.SetKey(starlark.String("body"), starlark.String(data))
	return out, nil
}

// checkURL applies the scheme and host rules to the first request and to every
// redirect target.
func (n *netState) checkURL(u *url.URL) error {
	host := u.Hostname()
	if host == "" {
		return errors.New("http.request: the url has no host")
	}
	if u.User != nil {
		return DeniedErr("http.request: a url with credentials is not allowed")
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
	case "http":
		if !client.IsLoopbackHost(host) {
			return DeniedErr("http.request: plain http is allowed only to a loopback host, not %q", host)
		}
	default:
		return DeniedErr("http.request: the scheme %q is not allowed (use https)", u.Scheme)
	}
	if _, ok := netMatch(host, n.cc.Perms.Net); !ok {
		return DeniedErr("http.request: the host %q is not in permissions.net", host)
	}
	return nil
}

func (n *netState) checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= MaxHTTPRedirects {
		return LimitErr("http.request: stopped after %d redirects", MaxHTTPRedirects)
	}
	if prev := via[len(via)-1]; strings.EqualFold(prev.URL.Scheme, "https") && !strings.EqualFold(req.URL.Scheme, "https") {
		return DeniedErr("http.request: refusing a redirect from https to http (%q)", req.URL.Host)
	}
	if n.requests.Add(1) > MaxHTTPRequests {
		return LimitErr("http.request: more than %d requests in one call (redirects count)", MaxHTTPRequests)
	}
	// Go copies the first request's headers to every hop and strips only
	// Authorization and Cookie when the host changes. Compare with the first
	// request, not the previous hop, so a hop back to the first host after a
	// foreign one cannot restore what was dropped: nothing else the script set
	// follows a change of host.
	if !strings.EqualFold(req.URL.Host, via[0].URL.Host) {
		for k := range req.Header {
			if !redirectSafeHeaders[k] {
				delete(req.Header, k)
			}
		}
	}
	if err := n.checkURL(req.URL); err != nil {
		var de *deniedError
		if errors.As(err, &de) {
			return DeniedErr("http.request: not following a redirect to %q: %s", req.URL.Host, strings.TrimPrefix(de.msg, "http.request: "))
		}
		return err
	}
	return nil
}

// netMatch reports which pattern of the manifest covers host: an exact name, or
// *.suffix for any subdomain of suffix but not suffix itself.
func netMatch(host string, patterns []string) (string, bool) {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if host == "" || strings.HasPrefix(host, ".") || strings.Contains(host, "..") {
		return "", false
	}
	for _, p := range patterns {
		pl := strings.ToLower(p)
		if suffix, ok := strings.CutPrefix(pl, "*"); ok && strings.HasPrefix(suffix, ".") {
			if strings.HasSuffix(host, suffix) && len(host) > len(suffix) {
				return p, true
			}
		} else if host == pl {
			return p, true
		}
	}
	return "", false
}

// literalGranted reports whether host is listed in the manifest as an IP
// literal or localhost: the only way a script may reach a non-public address.
// (The manifest grammar refuses both today, so this holds for tests and for a
// future grammar; the dial check must not depend on that.)
func (n *netState) literalGranted(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if _, err := netip.ParseAddr(host); err != nil && host != "localhost" {
		return false
	}
	for _, p := range n.cc.Perms.Net {
		if strings.EqualFold(p, host) {
			return true
		}
	}
	return false
}

var forbiddenPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"), // carrier-grade NAT
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"), // benchmarking
}

// forbiddenAddr is an address a name must not lead to: where the host's own
// services, the local network and cloud metadata live. An IPv6 address that
// embeds an IPv4 one (NAT64 64:ff9b::/96, 6to4 2002::/16) is judged by it.
func forbiddenAddr(a netip.Addr) bool {
	a = a.Unmap()
	if a.Is6() {
		b := a.As16()
		switch {
		case netip.MustParsePrefix("64:ff9b::/96").Contains(a):
			return forbiddenAddr(netip.AddrFrom4([4]byte(b[12:16])))
		case b[0] == 0x20 && b[1] == 0x02:
			return forbiddenAddr(netip.AddrFrom4([4]byte(b[2:6])))
		}
	}
	if a.IsLoopback() || a.IsPrivate() || a.IsLinkLocalUnicast() || a.IsLinkLocalMulticast() ||
		a.IsInterfaceLocalMulticast() || a.IsMulticast() || a.IsUnspecified() {
		return true
	}
	for _, p := range forbiddenPrefixes {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

func (n *netState) transport() *http.Transport {
	return &http.Transport{
		Proxy: func(r *http.Request) (*url.URL, error) {
			p, err := n.seams.Proxy(r)
			if p != nil && err == nil {
				// The proxy resolves the target, so the dial never sees its
				// address: resolve it here and refuse what a direct dial would.
				// The proxy may still resolve differently; its answer is its own.
				if err := n.checkTarget(r.Context(), r.URL.Hostname()); err != nil {
					return nil, err
				}
				n.mu.Lock()
				n.proxyAddrs[proxyAddr(p)] = true
				n.mu.Unlock()
			}
			return p, err
		},
		DialContext:           n.dial,
		TLSClientConfig:       n.seams.TLS,
		ForceAttemptHTTP2:     true,
		DisableKeepAlives:     true,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 0,
	}
}

// checkTarget refuses a host that leads to a forbidden address, as dial does.
func (n *netState) checkTarget(ctx context.Context, host string) error {
	if n.literalGranted(host) {
		return nil
	}
	addrs, err := n.resolve(ctx, host)
	if err != nil {
		return err
	}
	for _, a := range addrs {
		if forbiddenAddr(a) {
			return DeniedErr("http.request: %q resolves to a loopback, private or link-local address, which a plugin may not reach", host)
		}
	}
	return nil
}

func (n *netState) resolve(ctx context.Context, host string) ([]netip.Addr, error) {
	if a, err := netip.ParseAddr(host); err == nil {
		return []netip.Addr{a}, nil
	}
	return n.seams.Lookup(ctx, host)
}

func proxyAddr(p *url.URL) string {
	port := p.Port()
	if port == "" {
		switch p.Scheme {
		case "https":
			port = "443"
		case "socks5", "socks5h":
			port = "1080"
		default:
			port = "80"
		}
	}
	return net.JoinHostPort(p.Hostname(), port)
}

// dial resolves the name itself, refuses forbidden addresses unless the manifest
// lists that very literal, and connects to the checked address, so a DNS answer
// cannot change between the check and the connect.
func (n *netState) dial(ctx context.Context, network, addr string) (net.Conn, error) {
	var d net.Dialer
	n.mu.Lock()
	viaProxy := n.proxyAddrs[addr]
	n.mu.Unlock()
	if viaProxy {
		return d.DialContext(ctx, network, addr)
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	addrs, err := n.resolve(ctx, host)
	if err != nil {
		return nil, err
	}
	exempt := n.literalGranted(host)
	var last error
	for _, a := range addrs {
		if !exempt && forbiddenAddr(a) {
			last = DeniedErr("http.request: %q resolves to a loopback, private or link-local address, which a plugin may not reach", host)
			continue
		}
		conn, err := d.DialContext(ctx, network, net.JoinHostPort(a.String(), port))
		if err == nil {
			return conn, nil
		}
		last = err
	}
	if last == nil {
		last = fmt.Errorf("no address for %q", host)
	}
	return nil, last
}
