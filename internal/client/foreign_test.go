package client_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/client"
)

// A listener that answers 200 {"status":"ok"} to everything (no version, so
// not a Cartographer server) never sees the token, from any request the client
// makes: the check sits where the header is set, not in the callers.
func TestForeignListenerNeverReceivesTheToken(t *testing.T) {
	var mu sync.Mutex
	var auth []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		auth = append(auth, r.Header.Get("Authorization"))
		mu.Unlock()
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer srv.Close()
	c := client.New(srv.URL+"/mcp", "sekret").WithKB("kb-a")

	ops := map[string]func() error{
		"Call":        func() error { _, err := c.Call("kb_status", map[string]any{}); return err },
		"ListTools":   func() error { _, err := c.ListTools(); return err },
		"Ping":        func() error { return c.Ping(time.Second) },
		"ReportUsage": func() error { return c.ReportUsage("kb-a", []int{}, time.Second) },
		"ReportClientSettings": func() error {
			return c.ReportClientSettings(map[string]any{"machine": "m"}, time.Second)
		},
		"Heartbeat": func() error { _, err := c.Heartbeat(map[string]any{"protocol": 1}, time.Second); return err },
	}
	for name, op := range ops {
		if err := op(); !errors.Is(err, client.ErrNotCartographerServer) {
			t.Errorf("%s: err = %v, want ErrNotCartographerServer", name, err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	for i, a := range auth {
		if a != "" {
			t.Errorf("request %d carried Authorization %q", i, a)
		}
	}
}

// Neither a body that merely says bootstrapping nor a /health that redirects
// to another host verifies the configured URL: no request carries the token.
func TestVerifyRefusesForgedBootstrapAndForeignRedirect(t *testing.T) {
	var mu sync.Mutex
	var auth []string
	record := func(r *http.Request) {
		mu.Lock()
		auth = append(auth, r.Header.Get("Authorization"))
		mu.Unlock()
	}
	real := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		record(r)
		_, _ = w.Write([]byte(`{"status":"ok","version":"v1"}`))
	}))
	defer real.Close()

	cases := map[string]http.HandlerFunc{
		"forged bootstrapping": func(w http.ResponseWriter, r *http.Request) {
			record(r)
			_, _ = w.Write([]byte(`{"bootstrapping":true}`))
		},
		"redirect to another host": func(w http.ResponseWriter, r *http.Request) {
			record(r)
			if r.URL.Path == "/health" {
				http.Redirect(w, r, "http://localhost:"+real.URL[strings.LastIndex(real.URL, ":")+1:]+"/health", http.StatusFound)
			}
		},
	}
	for name, h := range cases {
		srv := httptest.NewServer(h)
		c := client.New(srv.URL+"/mcp", "sekret").WithInsecureHTTP(true)
		if _, err := c.Call("kb_status", map[string]any{}); !errors.Is(err, client.ErrNotCartographerServer) {
			t.Errorf("%s: err = %v, want ErrNotCartographerServer", name, err)
		}
		if err := client.VerifyServer(srv.URL+"/mcp", true, nil); !errors.Is(err, client.ErrNotCartographerServer) {
			t.Errorf("%s: VerifyServer = %v", name, err)
		}
		srv.Close()
	}
	mu.Lock()
	defer mu.Unlock()
	for i, a := range auth {
		if a != "" {
			t.Errorf("request %d carried Authorization %q", i, a)
		}
	}
}

// A real bootstrapping answer (status, version, bootstrapping) still verifies.
func TestVerifyAcceptsARealBootstrappingServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"ok","version":"v1","bootstrapping":true}`))
	}))
	defer srv.Close()
	if err := client.VerifyServer(srv.URL+"/mcp", true, nil); err != nil {
		t.Fatal(err)
	}
}
