package mcpserver

import (
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/auth"
)

// routedAtlasHandler is newRoutedTestHandler with the Atlas surface set as
// serve sets it: web reports whether EnableWeb mounted a bundle, addr is the
// listen address the Atlas URL is derived from.
func routedAtlasHandler(t *testing.T, web bool, addr string, names ...string) http.Handler {
	t.Helper()
	multi := NewMultiKBServer("test")
	for _, name := range names {
		k := setupNamedTestKB(t, name)
		multi.MountKB(name, func(s *Server) { RegisterKBTools(s, k, Deps{}) })
	}
	if err := multi.EnableRoutedMount("test", nil); err != nil {
		t.Fatalf("EnableRoutedMount: %v", err)
	}
	if web {
		multi.EnableWeb(http.NotFoundHandler())
	}
	multi.SetAtlasAddr(addr)
	return fromPeer(routedTokenStore(names...).Middleware(multi.Handler()), "127.0.0.1:50000")
}

// fromPeer sets the TCP peer of every request (httptest's default is a
// documentation address, which is not local) and, in pairs, headers a proxy
// would add.
func fromPeer(h http.Handler, remoteAddr string, headers ...string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.RemoteAddr = remoteAddr
		for i := 0; i+1 < len(headers); i += 2 {
			r.Header.Set(headers[i], headers[i+1])
		}
		h.ServeHTTP(w, r)
	})
}

// #1945: kb_status names the Atlas deep link of the KB it reports on, only
// when the HTTP server serves the Atlas.
func TestKBStatus_UIURLOnlyWhenTheAtlasIsServed(t *testing.T) {
	t.Parallel()
	served := routedAtlasHandler(t, true, "0.0.0.0:39273", "kb-a", "work-kb")
	for _, name := range []string{"kb-a", "work-kb"} {
		obj, _ := routedKBStatusConnection(t, served, RoutedMountPath, name)
		if want := "http://127.0.0.1:39273/ui/?kb=" + name; obj["ui_url"] != want {
			t.Errorf("%s: ui_url = %v, want %s", name, obj["ui_url"], want)
		}
	}
	// web.enabled off: EnableWeb is never called, the address alone says nothing.
	off := routedAtlasHandler(t, false, "0.0.0.0:39273", "kb-a")
	if obj, _ := routedKBStatusConnection(t, off, RoutedMountPath, "kb-a"); obj["ui_url"] != nil {
		t.Errorf("web off: ui_url = %v, want absent", obj["ui_url"])
	}
}

// The bind address is not the caller's to learn: only a loopback or wildcard
// bind yields a ui_url, a specific other address yields none.
func TestKBStatus_UIURLOnlyForALoopbackBind(t *testing.T) {
	t.Parallel()
	cases := []struct{ addr, want string }{
		{"127.0.0.1:39273", "http://127.0.0.1:39273/ui/?kb=kb-a"},
		{"[::1]:39273", "http://[::1]:39273/ui/?kb=kb-a"},
		{"localhost:39273", "http://localhost:39273/ui/?kb=kb-a"},
		{":39273", "http://127.0.0.1:39273/ui/?kb=kb-a"},
		{"0.0.0.0:39273", "http://127.0.0.1:39273/ui/?kb=kb-a"},
		{"[::]:39273", "http://127.0.0.1:39273/ui/?kb=kb-a"},
		{"10.0.0.5:39273", ""},
		{"[2001:db8::5]:39273", ""},
		{"server.example.com:39273", ""},
	}
	for _, c := range cases {
		h := routedAtlasHandler(t, true, c.addr, "kb-a")
		obj, _ := routedKBStatusConnection(t, h, RoutedMountPath, "kb-a")
		if c.want == "" {
			if _, ok := obj["ui_url"]; ok {
				t.Errorf("bind %s: ui_url = %v, want absent", c.addr, obj["ui_url"])
			}
			raw, _ := json.Marshal(obj)
			if host, _, _ := net.SplitHostPort(c.addr); strings.Contains(string(raw), host) {
				t.Errorf("bind %s: the answer names the bind address", c.addr)
			}
			continue
		}
		if obj["ui_url"] != c.want {
			t.Errorf("bind %s: ui_url = %v, want %s", c.addr, obj["ui_url"], c.want)
		}
	}
}

// ui_url is for a caller on the server's own machine: a remote peer, and a
// local proxy relaying a remote one, learn neither the port nor a link they
// cannot open. The same server answers all of them.
func TestKBStatus_UIURLOnlyForALocalCaller(t *testing.T) {
	t.Parallel()
	multi := NewMultiKBServer("test")
	k := setupNamedTestKB(t, "kb-a")
	multi.MountKB("kb-a", func(s *Server) { RegisterKBTools(s, k, Deps{}) })
	if err := multi.EnableRoutedMount("test", nil); err != nil {
		t.Fatalf("EnableRoutedMount: %v", err)
	}
	multi.EnableWeb(http.NotFoundHandler())
	multi.SetAtlasAddr("0.0.0.0:39273")
	base := routedTokenStore("kb-a").Middleware(multi.Handler())
	viaBase := routedTokenStore("kb-a").Middleware(markViaPrincipal(multi.Handler()))
	// A proxy token that forwards anonymous traffic: no identity header, so
	// Via stays empty, but the token is the proxy's (internal/auth/proxy.go).
	proxyTokBase := auth.NewScopedTokenStore([]auth.ScopedToken{{
		Token: routedTestToken, Proxy: true, Scopes: []auth.KBScope{{KB: "kb-a", Write: true}},
	}}).Middleware(multi.Handler())
	want := "http://127.0.0.1:39273/ui/?kb=kb-a"
	cases := []struct {
		name    string
		peer    string
		headers []string
		present bool
	}{
		{"loopback v4", "127.0.0.1:50000", nil, true},
		{"loopback v6", "[::1]:50000", nil, true},
		{"remote", "192.0.2.7:50000", nil, false},
		{"remote v6", "[2001:db8::7]:50000", nil, false},
		{"no peer address", "", nil, false},
		{"loopback proxy, X-Forwarded-For", "127.0.0.1:50000", []string{"X-Forwarded-For", "198.51.100.9"}, false},
		{"loopback proxy, Forwarded", "127.0.0.1:50000", []string{"Forwarded", "for=198.51.100.9"}, false},
		{"loopback proxy, X-Forwarded-Host", "127.0.0.1:50000", []string{"X-Forwarded-Host", "example.com"}, false},
		{"loopback proxy, X-Real-IP", "127.0.0.1:50000", []string{"X-Real-IP", "198.51.100.9"}, false},
		{"loopback proxy, X-Forwarded-Proto", "127.0.0.1:50000", []string{"X-Forwarded-Proto", "https"}, false},
		{"loopback proxy, Via", "127.0.0.1:50000", []string{"Via", "1.1 proxy.example.com"}, false},
		{"loopback, trusted-proxy principal, no header", "127.0.0.1:50000", nil, false},
		{"loopback, proxy token without identity", "127.0.0.1:50000", nil, false},
	}
	for _, c := range cases {
		h := base
		switch {
		case strings.Contains(c.name, "trusted-proxy"):
			h = viaBase
		case strings.Contains(c.name, "proxy token"):
			h = proxyTokBase
		}
		obj, _ := routedKBStatusConnection(t, fromPeer(h, c.peer, c.headers...), RoutedMountPath, "kb-a")
		if !c.present {
			raw, _ := json.Marshal(obj)
			if _, ok := obj["ui_url"]; ok || strings.Contains(string(raw), "39273") {
				t.Errorf("%s: the answer carries ui_url or the port: %v", c.name, obj["ui_url"])
			}
			continue
		}
		if obj["ui_url"] != want {
			t.Errorf("%s: ui_url = %v, want %s", c.name, obj["ui_url"], want)
		}
	}
}

// markViaPrincipal is what a trusted-proxy token does inside the auth
// middleware (internal/auth/proxy.go): the principal is a person the proxy
// named, with Via the id of the proxy token.
func markViaPrincipal(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := auth.PrincipalFromContext(r.Context())
		p.Via = "proxy-token"
		next.ServeHTTP(w, r.WithContext(auth.ContextWithPrincipal(r.Context(), p)))
	})
}

// Under stdio no HTTP server mounted the KB: no Atlas, no field.
func TestKBStatus_NoUIURLUnderStdio(t *testing.T) {
	t.Parallel()
	s := New("test")
	RegisterKBTools(s, setupTestKB(t), Deps{})
	var out map[string]any
	if err := json.Unmarshal([]byte(callOK(t, s, "kb_status", `{}`)), &out); err != nil {
		t.Fatal(err)
	}
	if _, ok := out["ui_url"]; ok {
		t.Errorf("stdio: ui_url = %v, want absent", out["ui_url"])
	}
}

// The entry paragraph a provisioned connection receives names the Atlas and
// where its URL is, so the agent never says there is no graphical view.
func TestEntryInstructions_NameTheAtlas(t *testing.T) {
	t.Parallel()
	for _, want := range []string{"Atlas", "`ui_url`", "`kb_status`"} {
		if !strings.Contains(entryInstructions, want) {
			t.Errorf("entryInstructions does not carry %q", want)
		}
	}
}
