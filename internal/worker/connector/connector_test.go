package connector

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestMain re-executes the test binary as a stdio MCP server: it runs on every
// OS without an interpreter. HELPER_ENV_PROBE makes the "env" tool report the
// environment it was started with.
func TestMain(m *testing.M) {
	if os.Getenv("CONNECTOR_TEST_HELPER") == "1" {
		srv := helperServer(nil)
		if err := srv.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

type noArgs struct{}

type pageArgs struct {
	ID string `json:"id"`
}

// helperServer has three tools: read (allowed in tests), write and env (never).
func helperServer(calls *atomic.Int32) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: "fake", Version: "1"}, nil)
	count := func() {
		if calls != nil {
			calls.Add(1)
		}
	}
	mcp.AddTool(srv, &mcp.Tool{Name: "read", Description: strings.Repeat("d", 400)}, func(ctx context.Context, r *mcp.CallToolRequest, in pageArgs) (*mcp.CallToolResult, any, error) {
		count()
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "page " + in.ID}}}, nil, nil
	})
	mcp.AddTool(srv, &mcp.Tool{Name: "write", Description: "mutates"}, func(ctx context.Context, r *mcp.CallToolRequest, in noArgs) (*mcp.CallToolResult, any, error) {
		count()
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "written"}}}, nil, nil
	})
	mcp.AddTool(srv, &mcp.Tool{Name: "env", Description: "environment"}, func(ctx context.Context, r *mcp.CallToolRequest, in noArgs) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: strings.Join(os.Environ(), "\n")}}}, nil, nil
	})
	return srv
}

func stdioCfg(t *testing.T, tools ...string) Config {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return Config{Name: "wiki", Transport: TransportStdio, Command: exe, Tools: tools,
		Env: map[string]string{"CONNECTOR_TEST_HELPER": "HELPER_FLAG"}, CallTimeout: 20 * time.Second, MaxResultBytes: 1 << 10}
}

func names(ts []Tool) []string {
	var out []string
	for _, t := range ts {
		out = append(out, t.Name)
	}
	return out
}

func TestStdioOffersOnlyTheAllowlist(t *testing.T) {
	t.Setenv("HELPER_FLAG", "1")
	t.Setenv("MODEL_API_KEY", "provider-key-value")
	ctx := context.Background()
	s := Open(ctx, []Config{stdioCfg(t, "read", "env")}, nil)
	defer s.Close()
	ts, err := s.Tools(ctx, "wiki")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(names(ts), ","); got != "wiki__read,wiki__env" && got != "wiki__env,wiki__read" {
		t.Fatalf("offered %v", names(ts))
	}
	for _, tl := range ts {
		if len(tl.Description) > maxDescriptionBytes {
			t.Errorf("%s description is %d bytes", tl.Name, len(tl.Description))
		}
	}
	res, err := s.Call(ctx, "wiki__read", map[string]any{"id": "7"})
	if err != nil || res.Text != "page 7" || res.IsError {
		t.Fatalf("read = %+v, %v", res, err)
	}
	if _, err := s.Call(ctx, "wiki__write", nil); err == nil {
		t.Error("a tool outside the allowlist was called")
	}
	if _, err := s.Call(ctx, "other__read", nil); err == nil {
		t.Error("a call to an undeclared connector was accepted")
	}
	// The child env holds only the listed variable plus the base set.
	env, err := s.Call(ctx, "wiki__env", nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(env.Text, "provider-key-value") || strings.Contains(env.Text, "MODEL_API_KEY") {
		t.Errorf("an unlisted variable reached the connector process:\n%s", env.Text)
	}
	if !strings.Contains(env.Text, "CONNECTOR_TEST_HELPER=1") {
		t.Errorf("the listed variable did not arrive:\n%s", env.Text)
	}
}

func TestUnsetVariableRefusesNamingIt(t *testing.T) {
	cfg := stdioCfg(t, "read")
	cfg.Env = map[string]string{"X": "NOT_SET_ANYWHERE"}
	s := Open(context.Background(), []Config{cfg}, nil)
	defer s.Close()
	_, err := s.Tools(context.Background(), "wiki")
	if err == nil || !strings.Contains(err.Error(), "NOT_SET_ANYWHERE") {
		t.Fatalf("err = %v", err)
	}
}

func TestConnectorThatCannotStartIsUnavailableNotFatal(t *testing.T) {
	bad := Config{Name: "gone", Transport: TransportStdio, Command: "/nonexistent/connector-binary", Tools: []string{"x"}, CallTimeout: 5 * time.Second, MaxResultBytes: 100}
	s := Open(context.Background(), []Config{bad}, nil)
	defer s.Close()
	if _, err := s.Tools(context.Background(), "gone"); err == nil {
		t.Fatal("a missing binary started")
	}
	if _, err := s.Call(context.Background(), "gone__x", nil); err == nil {
		t.Fatal("a call on an unavailable connector succeeded")
	}
	if _, err := s.Tools(context.Background(), "undeclared"); err == nil {
		t.Fatal("an undeclared connector started")
	}
}

func TestHTTPConnectorSendsCredentialsAndRefusesForgedCalls(t *testing.T) {
	var calls atomic.Int32
	var auth, hdr atomic.Value
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return helperServer(&calls) }, nil)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth.Store(r.Header.Get("Authorization"))
		hdr.Store(r.Header.Get("X-Api-Key"))
		handler.ServeHTTP(w, r)
	}))
	defer ts.Close()
	t.Setenv("WIKI_TOKEN", "bearer-value")
	t.Setenv("WIKI_KEY", "key-value")
	cfg := Config{Name: "wiki", Transport: TransportHTTP, URL: ts.URL, AllowInsecureHTTP: true, TokenEnv: "WIKI_TOKEN",
		Headers: map[string]string{"X-Api-Key": "WIKI_KEY"}, Tools: []string{"read"}, CallTimeout: 20 * time.Second, MaxResultBytes: 4}
	ctx := context.Background()
	s := Open(ctx, []Config{cfg}, nil)
	defer s.Close()
	tl, err := s.Tools(ctx, "wiki")
	if err != nil || len(tl) != 1 || tl[0].Name != "wiki__read" {
		t.Fatalf("tools = %v, %v", tl, err)
	}
	if auth.Load() != "Bearer bearer-value" || hdr.Load() != "key-value" {
		t.Errorf("credentials not sent: %v %v", auth.Load(), hdr.Load())
	}
	before := calls.Load()
	for _, forged := range []string{"wiki__write", "wiki__env", "wiki__", "wiki__nope"} {
		if _, err := s.Call(ctx, forged, nil); err == nil {
			t.Errorf("forged call %s accepted", forged)
		}
	}
	if calls.Load() != before {
		t.Errorf("a refused call reached the server")
	}
	res, err := s.Call(ctx, "wiki__read", map[string]any{"id": "123456"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(res.Text, "page") || !strings.HasSuffix(res.Text, "[truncated]") {
		t.Errorf("result not cut to max_result_bytes: %q", res.Text)
	}
}

func TestHTTPUnsetTokenEnvRefusesNamingIt(t *testing.T) {
	cfg := Config{Name: "wiki", Transport: TransportHTTP, URL: "https://wiki.example.com/mcp", TokenEnv: "UNSET_TOKEN_VAR", Tools: []string{"read"}, CallTimeout: time.Second, MaxResultBytes: 10}
	s := Open(context.Background(), []Config{cfg}, nil)
	_, err := s.Tools(context.Background(), "wiki")
	if err == nil || !strings.Contains(err.Error(), "UNSET_TOKEN_VAR") {
		t.Fatalf("err = %v", err)
	}
}

func TestPlainHTTPToARemoteHostIsRefused(t *testing.T) {
	cfg := Config{Name: "wiki", Transport: TransportHTTP, URL: "http://wiki.example.com/mcp", Tools: []string{"read"}, CallTimeout: time.Second, MaxResultBytes: 10}
	s := Open(context.Background(), []Config{cfg}, nil)
	if _, err := s.Tools(context.Background(), "wiki"); err == nil {
		t.Fatal("plain http to a remote host was accepted")
	}
}

// A redirect never carries the credentials to another host: the second server
// sees no token and the redirect itself is refused.
func TestHTTPRedirectToAnotherHostLeaksNothing(t *testing.T) {
	var otherAuth, otherKey atomic.Value
	var otherHits atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		otherHits.Add(1)
		otherAuth.Store(r.Header.Get("Authorization"))
		otherKey.Store(r.Header.Get("X-Api-Key"))
	}))
	defer other.Close()
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL, http.StatusTemporaryRedirect)
	}))
	defer first.Close()
	t.Setenv("WIKI_TOKEN", "bearer-value")
	t.Setenv("WIKI_KEY", "key-value")
	cfg := Config{Name: "wiki", Transport: TransportHTTP, URL: first.URL, AllowInsecureHTTP: true, TokenEnv: "WIKI_TOKEN",
		Headers: map[string]string{"X-Api-Key": "WIKI_KEY"}, Tools: []string{"read"}, CallTimeout: 5 * time.Second, MaxResultBytes: 100}
	s := Open(context.Background(), []Config{cfg}, nil)
	defer s.Close()
	if _, err := s.Tools(context.Background(), "wiki"); err == nil {
		t.Fatal("a redirect to another host was followed")
	}
	if a, _ := otherAuth.Load().(string); a != "" {
		t.Errorf("the other host saw Authorization %q", a)
	}
	if k, _ := otherKey.Load().(string); k != "" {
		t.Errorf("the other host saw X-Api-Key %q", k)
	}
	if otherHits.Load() != 0 {
		t.Errorf("the other host was reached %d times", otherHits.Load())
	}
}

// The result is cut while it is read, not after: a result far over the cap
// still comes back at the cap.
func TestResultCapAppliesWhileAppending(t *testing.T) {
	t.Setenv("HELPER_FLAG", "1")
	cfg := stdioCfg(t, "env")
	cfg.MaxResultBytes = 50
	ctx := context.Background()
	s := Open(ctx, []Config{cfg}, nil)
	defer s.Close()
	res, err := s.Call(ctx, "wiki__env", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Text) > 50+len("\n[truncated]") || !strings.HasSuffix(res.Text, "[truncated]") {
		t.Errorf("len %d: %q", len(res.Text), res.Text)
	}
}
