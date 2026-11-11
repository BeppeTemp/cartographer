package connector

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestAlias(t *testing.T) {
	for in, want := range map[string]string{"wiki": "wiki", "acme-wiki/wiki": "acme-wiki--wiki"} {
		if got := Alias(in); got != want {
			t.Errorf("Alias(%q) = %q", in, got)
		}
	}
	if err := CheckAliases([]Config{{Name: "a--b"}, {Name: "c"}}); err != nil {
		t.Fatal(err)
	}
	if err := CheckAliases([]Config{{Name: "a--b"}, {Name: "a/b"}}); err == nil {
		t.Fatal("a--b and a/b offer the same prefix and were accepted")
	}
}

func TestPluginConnectorIsOfferedAndRoutedByItsAlias(t *testing.T) {
	var calls atomic.Int32
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return helperServer(&calls) }, nil)
	ts := httptest.NewServer(handler)
	defer ts.Close()
	cfg := Config{Name: "acme-wiki/wiki", Transport: TransportHTTP, URL: ts.URL, AllowInsecureHTTP: true,
		Tools: []string{"read"}, CallTimeout: 20 * time.Second, MaxResultBytes: 100}
	ctx := context.Background()
	s := Open(ctx, []Config{cfg}, nil)
	defer s.Close()
	if !s.Has("acme-wiki/wiki") || s.Has("acme-wiki--wiki") {
		t.Fatal("Has answers by the qualified name only")
	}
	tl, err := s.Tools(ctx, "acme-wiki/wiki")
	if err != nil || len(tl) != 1 || tl[0].Name != "acme-wiki--wiki__read" {
		t.Fatalf("tools = %v, %v", tl, err)
	}
	res, err := s.Call(ctx, "acme-wiki--wiki__read", map[string]any{"id": "7"})
	if err != nil || res.Text != "page 7" {
		t.Fatalf("call = %+v, %v", res, err)
	}
	before := calls.Load()
	for _, forged := range []string{"acme-wiki/wiki__read", "acme-wiki--wiki__write", "wiki__read", "acme-wiki__read"} {
		if _, err := s.Call(ctx, forged, nil); err == nil {
			t.Errorf("forged call %s accepted", forged)
		}
	}
	if calls.Load() != before {
		t.Error("a refused call reached the server")
	}
}

func TestOpenRefusesConnectorsWithOneAlias(t *testing.T) {
	var calls atomic.Int32
	ts := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return helperServer(&calls) }, nil))
	defer ts.Close()
	mk := func(name string) Config {
		return Config{Name: name, Transport: TransportHTTP, URL: ts.URL, AllowInsecureHTTP: true,
			Tools: []string{"read"}, CallTimeout: 20 * time.Second, MaxResultBytes: 100}
	}
	ctx := context.Background()
	s := Open(ctx, []Config{mk("a--b"), mk("a/b")}, nil)
	defer s.Close()
	for _, name := range []string{"a--b", "a/b"} {
		if _, err := s.Tools(ctx, name); err == nil {
			t.Errorf("Tools(%s) started a connector of a colliding set", name)
		}
	}
	if _, err := s.Call(ctx, "a--b__read", nil); err == nil {
		t.Error("Call routed in a colliding set")
	}
	if calls.Load() != 0 {
		t.Error("a server was reached")
	}
}

func TestToolWhoseOfferedNameIsOverTheCapIsNotOffered(t *testing.T) {
	var calls atomic.Int32
	ts := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		srv := helperServer(&calls)
		mcp.AddTool(srv, &mcp.Tool{Name: strings.Repeat("t", 60), Description: "long"}, func(context.Context, *mcp.CallToolRequest, noArgs) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{}, nil, nil
		})
		return srv
	}, nil))
	defer ts.Close()
	cfg := Config{Name: "acme-wiki/wiki", Transport: TransportHTTP, URL: ts.URL, AllowInsecureHTTP: true,
		Tools: []string{"read", strings.Repeat("t", 60)}, CallTimeout: 20 * time.Second, MaxResultBytes: 100}
	var logged []string
	s := Open(context.Background(), []Config{cfg}, func(f string, a ...any) { logged = append(logged, fmt.Sprintf(f, a...)) })
	defer s.Close()
	tl, err := s.Tools(context.Background(), "acme-wiki/wiki")
	if err != nil || len(tl) != 1 || tl[0].Name != "acme-wiki--wiki__read" {
		t.Fatalf("tools = %v, %v", tl, err)
	}
	if len(logged) != 1 || !strings.Contains(logged[0], "over 64") {
		t.Fatalf("log = %v", logged)
	}
	if _, err := s.Call(context.Background(), "acme-wiki--wiki__"+strings.Repeat("t", 60), nil); err == nil {
		t.Error("a tool that was not offered was called")
	}
}
