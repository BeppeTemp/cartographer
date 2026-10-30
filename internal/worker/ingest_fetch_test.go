package worker

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func fetchFrom(t *testing.T, h http.Handler, allow bool) (string, bool) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	out := (&fetcher{locator: srv.URL + "/p", allowPrivate: allow, timeout: 500 * time.Millisecond}).fetch(context.Background())
	return out.Text, out.IsError
}

func TestSourceFetchSuccessWrapsAndStripsHTML(t *testing.T) {
	text, isErr := fetchFrom(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<html><head><title>x</title><style>p{}</style></head><body><script>evil()</script><h1>Title</h1><p>A &amp; B</p></body></html>"))
	}), true)
	if isErr || !strings.HasPrefix(text, fetchBanner) {
		t.Fatalf("text = %q", text)
	}
	body := strings.TrimPrefix(text, fetchBanner+"\n\n")
	if body != "Title\nA & B" {
		t.Fatalf("html text = %q", body)
	}
}

func TestSourceFetchRefusals(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("hello"))
	})
	if text, isErr := fetchFrom(t, ok, false); !isErr || !strings.Contains(text, "private") {
		t.Errorf("loopback must be refused by default: %q %v", text, isErr)
	}
	if text, isErr := fetchFrom(t, ok, true); isErr || !strings.HasSuffix(text, "hello") {
		t.Errorf("loopback with allow_private: %q %v", text, isErr)
	}
	if text, isErr := fetchFrom(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write([]byte("%PDF-1.4"))
	}), true); !isErr || !strings.Contains(text, "not a text source") {
		t.Errorf("binary: %q %v", text, isErr)
	}
	if text, isErr := fetchFrom(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(2 * time.Second)
	}), true); !isErr || !strings.Contains(text, "fetch failed") {
		t.Errorf("timeout: %q %v", text, isErr)
	}
	if text, isErr := fetchFrom(t, http.NotFoundHandler(), true); !isErr || !strings.Contains(text, "404") {
		t.Errorf("404: %q %v", text, isErr)
	}
	// A redirect to another host (same IP, other name) is refused.
	var other *httptest.Server
	other = httptest.NewServer(ok)
	t.Cleanup(other.Close)
	redirect := strings.Replace(other.URL, "127.0.0.1", "localhost", 1)
	if text, isErr := fetchFrom(t, http.RedirectHandler(redirect, http.StatusFound), true); !isErr || !strings.Contains(text, "another host") {
		t.Errorf("cross-host redirect: %q %v", text, isErr)
	}
	// A redirect on the same host is followed.
	mux := http.NewServeMux()
	mux.Handle("/p", http.RedirectHandler("/q", http.StatusFound))
	mux.Handle("/q", ok)
	if text, isErr := fetchFrom(t, mux, true); isErr || !strings.HasSuffix(text, "hello") {
		t.Errorf("same-host redirect: %q %v", text, isErr)
	}
	if out := (&fetcher{locator: "file:///etc/passwd"}).fetch(context.Background()); !out.IsError {
		t.Error("a file locator must be refused")
	}
}

func TestSourceFetchTruncatesAtOneMiB(t *testing.T) {
	text, isErr := fetchFrom(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(strings.Repeat("a", fetchMaxBytes+10)))
	}), true)
	if isErr || !strings.Contains(text, "[truncated: the source continues after byte 1048576]") {
		t.Fatalf("no marker (error=%v, tail=%q)", isErr, text[len(text)-80:])
	}
}

func TestPrivateIPClasses(t *testing.T) {
	for ip, want := range map[string]bool{"127.0.0.1": true, "10.1.2.3": true, "192.168.0.1": true, "169.254.1.1": true,
		"100.64.0.1": true, "0.1.2.3": true, "192.0.0.8": true, "198.19.0.1": true, "198.20.0.1": false,
		"64:ff9b::7f00:1": true, "64:ff9b::5db8:d822": false, "2002:0a00:0001::1": true, "2002:5db8:d822::1": false, "::1": true, "fe80::1": true, "0.0.0.0": true, "93.184.216.34": false, "2606:4700::1": false} {
		if got := privateIP(parseIP(t, ip)); got != want {
			t.Errorf("privateIP(%s) = %v", ip, got)
		}
	}
}

func parseIP(t *testing.T, s string) net.IP {
	t.Helper()
	ip := net.ParseIP(s)
	if ip == nil {
		t.Fatal(s)
	}
	return ip
}

func TestSourceFetchRedirectDowngradeAndPortChange(t *testing.T) {
	a := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(a.Close)
	// Same host, other port.
	if text, isErr := fetchFrom(t, http.RedirectHandler(a.URL+"/x", http.StatusFound), true); !isErr || !strings.Contains(text, "another host or port") {
		t.Errorf("port change: %q %v", text, isErr)
	}
	// https -> http on the same host and port.
	tls := httptest.NewTLSServer(nil)
	t.Cleanup(tls.Close)
	tls.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://"+r.Host+"/x", http.StatusFound)
	})
	f := &fetcher{locator: tls.URL + "/p", allowPrivate: true, timeout: time.Second}
	c := f.client()
	c.Transport.(*http.Transport).TLSClientConfig = tls.Client().Transport.(*http.Transport).TLSClientConfig
	if _, err := c.Get(f.locator); err == nil || !strings.Contains(err.Error(), "https to http") {
		t.Errorf("downgrade: %v", err)
	}
}

func TestSourceFetchStopsWithTheRunContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(100 * time.Millisecond); cancel() }()
	start := time.Now()
	out := (&fetcher{locator: srv.URL, allowPrivate: true, timeout: 10 * time.Second}).fetch(ctx)
	if !out.IsError || time.Since(start) > 3*time.Second {
		t.Fatalf("not cancelled: %+v after %v", out, time.Since(start))
	}
}
