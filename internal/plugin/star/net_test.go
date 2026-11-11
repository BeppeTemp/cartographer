package star

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/plugin/core"
)

// netCall runs body as a tool with the given permissions and returns its result.
func netCall(t *testing.T, perms *core.Permissions, seams *netSeams, body string) (Result, error) {
	t.Helper()
	src := "def go(a):\n    " + strings.ReplaceAll(strings.TrimSpace(body), "\n", "\n    ") + "\ntool(\"go\", go, \"go\")\n"
	r := toolReq("go", `{}`)
	r.Permissions = perms
	r.Version = "1.2.3"
	return mustLoad(t, src).call(context.Background(), r, callOpts{net: seams})
}

func netPerms(hosts ...string) *core.Permissions { return &core.Permissions{Net: hosts} }

func decode(t *testing.T, res Result) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(res.Text), &m); err != nil {
		t.Fatalf("result %q: %v", res.Text, err)
	}
	return m
}

func TestNetMatch(t *testing.T) {
	for _, c := range []struct {
		host string
		pats []string
		want bool
	}{
		{"api.example.com", []string{"api.example.com"}, true},
		{"API.Example.com.", []string{"api.example.com"}, true},
		{"api.example.com", []string{"example.com"}, false},
		{"a.example.com", []string{"*.example.com"}, true},
		{"a.b.example.com", []string{"*.example.com"}, true},
		{"example.com", []string{"*.example.com"}, false},
		{"evilexample.com", []string{"*.example.com"}, false},
		{".example.com", []string{"*.example.com"}, false},
		{"x..example.com", []string{"*.example.com"}, false},
		{"example.com.evil.org", []string{"*.example.com"}, false},
	} {
		if _, got := netMatch(c.host, c.pats); got != c.want {
			t.Errorf("netMatch(%q, %v) = %v", c.host, c.pats, got)
		}
	}
}

func TestForbiddenAddr(t *testing.T) {
	for addr, want := range map[string]bool{
		"127.0.0.1": true, "::1": true, "10.1.2.3": true, "192.168.0.1": true, "172.16.0.1": true,
		"169.254.169.254": true, "fe80::1": true, "0.0.0.0": true, "224.0.0.1": true, "100.64.0.1": true,
		"::ffff:127.0.0.1": true, "fc00::1": true,
		"0.1.2.3": true, "192.0.0.8": true, "198.18.0.1": true, "198.19.255.1": true,
		"64:ff9b::7f00:1": true, "64:ff9b::a9fe:a9fe": true, "64:ff9b::5db8:d822": false,
		"2002:7f00:1::": true, "2002:a9fe:a9fe::1": true, "2002:5db8:d822::1": false,
		"198.20.0.1":    false,
		"93.184.216.34": false, "2606:4700::1111": false,
	} {
		if got := forbiddenAddr(netip.MustParseAddr(addr)); got != want {
			t.Errorf("forbiddenAddr(%s) = %v", addr, got)
		}
	}
}

func TestNetAbsentWithoutPermission(t *testing.T) {
	for _, perms := range []*core.Permissions{nil, {}, {KB: "read"}} {
		_, err := netCall(t, perms, nil, `return http.request("GET", "https://example.com/")`)
		if ce := callErr(t, err, Denied); !strings.Contains(ce.Msg, "permissions.net") {
			t.Errorf("message %q", ce.Msg)
		}
		_, err = netCall(t, perms, nil, `return env.get("TOKEN")`)
		if ce := callErr(t, err, Denied); !strings.Contains(ce.Msg, "permissions.env") {
			t.Errorf("message %q", ce.Msg)
		}
	}
}

func TestEnvGet(t *testing.T) {
	look := func(name string) (string, bool) {
		return map[string]string{"TOKEN": "s3", "OTHER": "x"}[name], name == "TOKEN" || name == "OTHER"
	}
	run := func(body string) (Result, error) {
		p := mustLoad(t, "def go(a):\n    "+body+"\ntool(\"go\", go, \"go\")\n")
		r := toolReq("go", `{}`)
		r.Permissions = &core.Permissions{Env: []string{"TOKEN", "UNSET_ONE"}}
		r.Host = Host{LookupEnv: look}
		return p.Call(context.Background(), r)
	}
	if res, err := run(`return env.get("TOKEN")`); err != nil || res.Text != "s3" {
		t.Errorf("listed: %q %v", res.Text, err)
	}
	if res, err := run(`return env.get("UNSET_ONE") == None`); err != nil || res.Text != "true" {
		t.Errorf("unset: %q %v", res.Text, err)
	}
	// OTHER is set but not listed: its value is never reached.
	_, err := run(`return env.get("OTHER")`)
	if ce := callErr(t, err, Denied); !strings.Contains(ce.Msg, "permissions.env") {
		t.Errorf("message %q", ce.Msg)
	}
	// There is no way to list the environment.
	if _, err := run(`return env.list()`); err == nil {
		t.Error("env.list exists")
	}
}

func tlsSeams(srv *httptest.Server) *netSeams {
	return &netSeams{tls: srv.Client().Transport.(*http.Transport).TLSClientConfig}
}

func TestHTTPSAllowedHost(t *testing.T) {
	var ua, hdr, method, body atomic.Value
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ua.Store(r.Header.Get("User-Agent"))
		hdr.Store(r.Header.Get("X-Thing"))
		method.Store(r.Method)
		b := new(strings.Builder)
		buf := make([]byte, 64)
		for n, _ := r.Body.Read(buf); n > 0; n, _ = r.Body.Read(buf) {
			b.Write(buf[:n])
		}
		body.Store(b.String())
		w.Header().Add("Set-Cookie", "a=1")
		w.Header().Add("Set-Cookie", "b=2")
		w.Header().Set("X-Reply", "yes")
		w.WriteHeader(201)
		fmt.Fprint(w, "héllo")
	}))
	defer srv.Close()
	res, err := netCall(t, netPerms("127.0.0.1"), tlsSeams(srv),
		fmt.Sprintf(`return http.request("post", %q, headers={"X-Thing": "v"}, body="payload")`, srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	m := decode(t, res)
	if m["status"] != float64(201) || m["body"] != "héllo" {
		t.Errorf("result %v", m)
	}
	h := m["headers"].(map[string]any)
	if h["set-cookie"] != "a=1, b=2" || h["x-reply"] != "yes" {
		t.Errorf("headers %v", h)
	}
	if ua.Load() != "cartographer-plugin/acme/1.2.3" || hdr.Load() != "v" || method.Load() != "POST" || body.Load() != "payload" {
		t.Errorf("server saw %v %v %v %v", ua.Load(), hdr.Load(), method.Load(), body.Load())
	}
}

func TestHTTPSchemeAndHostRules(t *testing.T) {
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "plain") }))
	defer plain.Close()
	// Plain http to a loopback host that is granted.
	if res, err := netCall(t, netPerms("127.0.0.1"), nil, fmt.Sprintf(`return http.request("GET", %q)["body"]`, plain.URL)); err != nil || res.Text != "plain" {
		t.Errorf("loopback http: %q %v", res.Text, err)
	}
	for name, c := range map[string]struct {
		perms *core.Permissions
		url   string
	}{
		"http elsewhere":       {netPerms("example.com"), "http://example.com/"},
		"host not listed":      {netPerms("example.com"), "https://other.example.org/"},
		"bare suffix":          {netPerms("*.example.com"), "https://example.com/"},
		"other scheme":         {netPerms("example.com"), "ftp://example.com/"},
		"credentials in url":   {netPerms("example.com"), "https://u:p@example.com/"},
		"loopback not granted": {netPerms("example.com"), plain.URL},
	} {
		_, err := netCall(t, c.perms, nil, fmt.Sprintf(`return http.request("GET", %q)`, c.url))
		if err == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		callErr(t, err, Denied)
	}
}

func TestHTTPDialRefusesPrivateAddresses(t *testing.T) {
	for _, ip := range []string{"127.0.0.1", "10.0.0.5", "169.254.169.254", "::1"} {
		seams := &netSeams{lookup: func(context.Context, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr(ip)}, nil
		}}
		_, err := netCall(t, netPerms("*.example.com"), seams, `return http.request("GET", "https://api.example.com/")`)
		if ce := callErr(t, err, Denied); !strings.Contains(ce.Msg, "api.example.com") {
			t.Errorf("%s: %q", ip, ce.Msg)
		}
	}
}

func TestHTTPRedirects(t *testing.T) {
	var auth, seen atomic.Value
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth.Store(r.Header.Get("Authorization"))
		seen.Store(r.Header.Clone())
		fmt.Fprint(w, "landed")
	}))
	defer other.Close()
	otherURL := strings.Replace(other.URL, "127.0.0.1", "localhost", 1)
	var hops atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cross":
			http.Redirect(w, r, otherURL, http.StatusFound)
		case "/chain/0":
			fmt.Fprint(w, "end")
		case "/chain/9", "/chain/8", "/chain/7", "/chain/6", "/chain/5", "/chain/4", "/chain/3", "/chain/2", "/chain/1":
			n := int(r.URL.Path[len(r.URL.Path)-1] - '0')
			http.Redirect(w, r, fmt.Sprintf("/chain/%d", n-1), http.StatusFound)
		case "/loop":
			hops.Add(1)
			http.Redirect(w, r, "/loop", http.StatusFound)
		}
	}))
	defer srv.Close()
	get := func(perms *core.Permissions, path string) (Result, error) {
		return netCall(t, perms, nil, fmt.Sprintf(`return http.request("GET", %q, headers={"Authorization": "Bearer t"})["body"]`, srv.URL+path))
	}
	// Inside the list: followed, and the credential does not follow to another host.
	if res, err := get(netPerms("127.0.0.1", "localhost"), "/cross"); err != nil || res.Text != "landed" {
		t.Errorf("inside: %q %v", res.Text, err)
	}
	if auth.Load() != "" {
		t.Errorf("Authorization crossed hosts: %v", auth.Load())
	}
	// Only a small safe set of headers follows a change of host.
	if _, err := netCall(t, netPerms("127.0.0.1", "localhost"), nil, fmt.Sprintf(
		`return http.request("GET", %q, headers={"X-Api-Key": "k", "X-Auth-Token": "t", "Accept": "text/plain"})["body"]`, srv.URL+"/cross")); err != nil {
		t.Fatal(err)
	}
	if got := seen.Load().(http.Header); got.Get("X-Api-Key") != "" || got.Get("X-Auth-Token") != "" || got.Get("Accept") != "text/plain" {
		t.Errorf("headers after a cross-host redirect: %v", got)
	}
	// Outside the list: not followed, and the error names the host.
	_, err := get(netPerms("127.0.0.1"), "/cross")
	if ce := callErr(t, err, Denied); !strings.Contains(ce.Msg, "localhost") {
		t.Errorf("outside: %q", ce.Msg)
	}
	// Every hop counts against the cap of requests: a chain of 10 requests, 5 times.
	chain := func(n int) error {
		_, err := netCall(t, netPerms("127.0.0.1"), nil, fmt.Sprintf("for i in range(%d):\n    http.request(\"GET\", %q)", n, srv.URL+"/chain/9"))
		return err
	}
	if err := chain(MaxHTTPRequests / 10); err != nil {
		t.Errorf("at the cap: %v", err)
	}
	callErr(t, chain(MaxHTTPRequests/10+1), LimitExceeded)
	// Over the bound.
	_, err = get(netPerms("127.0.0.1"), "/loop")
	callErr(t, err, LimitExceeded)
	if hops.Load() > MaxHTTPRedirects+1 {
		t.Errorf("%d hops", hops.Load())
	}
}

func TestHTTPCaps(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/big":
			w.Write(make([]byte, MaxHTTPResponseBytes+1))
		case "/bin":
			w.Write([]byte{0xff, 0xfe})
		default:
			fmt.Fprint(w, "ok")
		}
	}))
	defer srv.Close()
	perms := netPerms("127.0.0.1")
	_, err := netCall(t, perms, nil, fmt.Sprintf(`return http.request("GET", %q)`, srv.URL+"/big"))
	callErr(t, err, LimitExceeded)
	_, err = netCall(t, perms, nil, fmt.Sprintf(`return http.request("GET", %q)`, srv.URL+"/bin"))
	if ce := callErr(t, err, Failed); !strings.Contains(ce.Msg, "UTF-8") {
		t.Errorf("%q", ce.Msg)
	}
	_, err = netCall(t, perms, nil, fmt.Sprintf(`return http.request("POST", %q, body="x" * %d)`, srv.URL, MaxHTTPRequestBytes+1))
	callErr(t, err, LimitExceeded)
	_, err = netCall(t, perms, nil, fmt.Sprintf("for i in range(%d):\n    http.request(\"GET\", %q)", MaxHTTPRequests+1, srv.URL))
	if ce := callErr(t, err, LimitExceeded); !strings.Contains(ce.Msg, "requests") {
		t.Errorf("%q", ce.Msg)
	}
	// Exactly the cap is fine.
	if _, err := netCall(t, perms, nil, fmt.Sprintf("for i in range(%d):\n    http.request(\"GET\", %q)", MaxHTTPRequests, srv.URL)); err != nil {
		t.Errorf("at the cap: %v", err)
	}
}

func TestHTTPInputs(t *testing.T) {
	perms := netPerms("example.com")
	for name, body := range map[string]string{
		"method":   `http.request("TRACE", "https://example.com/")`,
		"host hdr": `http.request("GET", "https://example.com/", headers={"host": "x"})`,
		"hop hdr":  `http.request("GET", "https://example.com/", headers={"Transfer-Encoding": "chunked"})`,
	} {
		if _, err := netCall(t, perms, nil, body); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// Text built from the script is escaped before it reaches a terminal.
	_, err := netCall(t, perms, nil, `http.request("GE\x1b[31mT", "https://example.com/")`)
	if ce := callErr(t, err, Failed); strings.ContainsRune(ce.Msg, 0x1b) {
		t.Errorf("unescaped message %q", ce.Msg)
	}
	_, err = netCall(t, perms, nil, `http.request("GET", "https://example.com/\x1b[2J")`)
	if err == nil || strings.ContainsRune(err.Error(), 0x1b) {
		t.Errorf("unescaped error %v", err)
	}
	_, err = netCall(t, netPerms("example.com"), nil, `http.request("GET", "https://evil\x1b[2J.org/")`)
	if err == nil || strings.ContainsRune(err.Error(), 0x1b) {
		t.Errorf("unescaped error %v", err)
	}
}

func TestHTTPTimeout(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)
	start := time.Now()
	_, err := netCall(t, netPerms("127.0.0.1"), nil, fmt.Sprintf(`return http.request("GET", %q, timeout=0.1)`, srv.URL))
	if err == nil || time.Since(start) > 5*time.Second {
		t.Errorf("timeout: %v after %v", err, time.Since(start))
	}
}

// The proxy chosen by the environment is honoured: the connection goes to the
// proxy, which gets a CONNECT for the allowed host, and only the proxy's own
// address passes the dial check unchecked.
func TestHTTPProxy(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	line := make(chan string, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		buf := make([]byte, 512)
		n, _ := c.Read(buf)
		line <- strings.SplitN(string(buf[:n]), "\r\n", 2)[0]
		fmt.Fprint(c, "HTTP/1.1 502 Bad Gateway\r\n\r\n")
	}()
	pu := &url.URL{Scheme: "http", Host: ln.Addr().String()}
	seams := &netSeams{
		proxy: func(*http.Request) (*url.URL, error) { return pu, nil },
		lookup: func(context.Context, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
		},
		tls: &tls.Config{},
	}
	_, err = netCall(t, netPerms("api.example.com"), seams, `return http.request("GET", "https://api.example.com/x")`)
	callErr(t, err, Failed)
	select {
	case l := <-line:
		if l != "CONNECT api.example.com:443 HTTP/1.1" {
			t.Errorf("proxy saw %q", l)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the proxy was not used")
	}
	// Behind a proxy the target's own resolution is checked here.
	for _, ip := range []string{"169.254.169.254", "10.0.0.5", "64:ff9b::a9fe:a9fe"} {
		seams := &netSeams{
			proxy:  func(*http.Request) (*url.URL, error) { return pu, nil },
			lookup: func(context.Context, string) ([]netip.Addr, error) { return []netip.Addr{netip.MustParseAddr(ip)}, nil },
		}
		_, err = netCall(t, netPerms("api.example.com"), seams, `return http.request("GET", "https://api.example.com/x")`)
		if ce := callErr(t, err, Denied); !strings.Contains(ce.Msg, "api.example.com") {
			t.Errorf("%s behind a proxy: %q", ip, ce.Msg)
		}
	}
	// The default is the environment's proxy function.
	if n := newNetState(&callCtx{}, "", nil); n.seams.proxy == nil {
		t.Error("no default proxy")
	}
}
