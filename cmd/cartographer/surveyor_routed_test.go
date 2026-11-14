package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/kbtest"
	"github.com/BeppeTemp/cartographer/internal/mcpserver"
	"github.com/BeppeTemp/cartographer/internal/surveyor"
)

const routedPerimeterToken = "routed-perimeter-tok"

// routedAuthServer is a real multi-KB server with auth on and the routed mount
// enabled, serving kb-a and work-kb (#2088).
func routedAuthServer(t *testing.T) *httptest.Server {
	t.Helper()
	multi := mcpserver.NewMultiKBServer("test")
	for _, name := range []string{"kb-a", "work-kb"} {
		k := kbtest.New(t)
		k.AuthName = name
		multi.MountKB(name, func(s *mcpserver.Server) { mcpserver.RegisterKBTools(s, k, mcpserver.Deps{}) })
	}
	if err := multi.EnableRoutedMount("test", nil); err != nil {
		t.Fatal(err)
	}
	ts := auth.NewScopedTokenStore([]auth.ScopedToken{{Token: routedPerimeterToken, Principal: "surveyor-a", Policy: auth.Policy{Permissions: []auth.Permission{
		{KB: "kb-a", Write: true}, {KB: "work-kb", Write: true}}}}})
	srv := httptest.NewServer(ts.Middleware(multi.Handler()))
	t.Cleanup(srv.Close)
	return srv
}

// A Surveyor with two KBs starts against a routed server with auth on whether
// server_url names /mcp or /mcp/routed, and reaches each KB through the routed
// endpoint (#2088).
func TestSurveyorStartupOnARoutedServer(t *testing.T) {
	srv := routedAuthServer(t)
	for _, path := range []string{"/mcp", "/mcp/routed", "/mcp/routed/"} {
		t.Run(path, func(t *testing.T) {
			t.Setenv("SURVEYOR_TOKEN", routedPerimeterToken)
			t.Setenv("CLOUD_API_KEY", surveyorCanary+"-key")
			cfgPath := surveyorCfg(t, srv.URL, routedPerimeterToken)
			raw, _ := os.ReadFile(cfgPath)
			body := strings.Replace(string(raw), srv.URL+"/mcp", srv.URL+path, 1)
			body = "allow_insecure_http: true\n" + body
			if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			env, err := surveyorStartup(context.Background(), cfgPath, noop(), fastRetry(time.Second), &strings.Builder{})
			if err != nil || env == nil {
				t.Fatalf("env=%v err=%v", env, err)
			}
			if !env.routed {
				t.Fatal("the Surveyor did not move to the routed endpoint")
			}
			for _, kb := range []string{"kb-a", "work-kb"} {
				c := env.kbClient(kb)
				tools, err := c.ListTools()
				if err != nil || len(tools) == 0 {
					t.Fatalf("%s: tools %d, err %v", kb, len(tools), err)
				}
				for _, tl := range tools {
					if strings.Contains(string(tl.InputSchema), `"kb"`) && strings.Contains(string(tl.InputSchema), `"properties":{"kb"`) {
						t.Fatalf("%s: tool %s still shows the kb argument to the model", kb, tl.Name)
					}
				}
				// The surveyor loop hands a model's call over as raw JSON.
				out, err := c.CallTool("kb_status", json.RawMessage(`{}`))
				if err != nil || out.IsError || !strings.Contains(out.Text, kb) {
					t.Fatalf("%s: kb_status = %+v, %v", kb, out, err)
				}
			}
		})
	}
}

// A server_url that carries ?kb= keeps the one-KB addressing of the per-KB endpoint.
func TestRoutedPerimeterURLKeepsAPinnedKB(t *testing.T) {
	if _, pinned := routedPerimeterURL("http://h/mcp?kb=kb-a", "/mcp/routed", []string{"kb-a"}); !pinned {
		t.Fatal("?kb= was not kept")
	}
	if got, _ := routedPerimeterURL("https://h/prefix/mcp", "/mcp/routed", []string{"kb-a"}); got != "https://h/prefix/mcp/routed?kbs=kb-a" {
		t.Fatalf("prefixed: %q", got)
	}
	if got, _ := routedPerimeterURL("https://h/prefix/mcp/routed", "/prefix/mcp/routed", []string{"kb-a"}); got != "https://h/prefix/mcp/routed?kbs=kb-a" {
		t.Fatalf("prefix already advertised: %q", got)
	}
	got, pinned := routedPerimeterURL("http://h/mcp", "/mcp/routed", []string{"kb-a", "work-kb"})
	if pinned || got != "http://h/mcp/routed?kbs=kb-a%2Cwork-kb" {
		t.Fatalf("got %q pinned %v", got, pinned)
	}
}

// A Ping failure that is not a 401/403 is not reported as a rejected token.
func TestSurveyorStartupPingErrorIsNotATokenRejection(t *testing.T) {
	for _, tc := range []struct {
		status int
		token  bool
	}{{http.StatusBadRequest, false}, {http.StatusBadGateway, false}, {http.StatusForbidden, true}} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/health" {
				json.NewEncoder(w).Encode(map[string]any{"status": "ok", "version": "t", "ready": true,
					"kbs": []map[string]string{{"name": "kb-a"}, {"name": "work-kb"}}})
				return
			}
			http.Error(w, "kb parameter required", tc.status)
		}))
		_, err, _ := startup(t, srv, surveyorCanary+"-token", fastRetry(time.Second))
		srv.Close()
		msg := wantExit(t, err, 2)
		if got := strings.Contains(msg, "did not accept the token"); got != tc.token {
			t.Fatalf("status %d: token rejection = %v: %s", tc.status, got, msg)
		}
		if tc.status == http.StatusBadRequest && !strings.Contains(msg, "?kb=<name>") {
			t.Fatalf("a 400 kb parameter required names no URL: %s", msg)
		}
	}
}

// A 400 on a routed server names the routed URL to use.
func TestSurveyorStartupKBRequiredNamesTheRoutedURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			json.NewEncoder(w).Encode(map[string]any{"status": "ok", "version": "t", "ready": true,
				"mount_mode": "routed", "routed_path": "/mcp/routed",
				"kbs": []map[string]string{{"name": "kb-a"}, {"name": "work-kb"}}})
			return
		}
		http.Error(w, "kb parameter required", http.StatusBadRequest)
	}))
	defer srv.Close()
	// ?kb= pins one KB, so the Surveyor stays on the per-KB endpoint and the 400 is reported.
	cfgPath := surveyorCfg(t, srv.URL, surveyorCanary+"-token")
	raw, _ := os.ReadFile(cfgPath)
	if err := os.WriteFile(cfgPath, []byte(strings.Replace(string(raw), "/mcp\n", "/mcp?kb=zzz\n", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := surveyorStartup(context.Background(), cfgPath, noop(), fastRetry(time.Second), &strings.Builder{})
	msg := wantExit(t, err, 2)
	if !strings.Contains(msg, "/mcp/routed?kbs=kb-a%2Cwork-kb") || strings.Contains(msg, "did not accept the token") {
		t.Fatalf("message = %s", msg)
	}
}

// `mcp exec` on a routed server with auth on: the client built on the routed
// path reaches /health at the server base, so the token is sent (#2088).
func TestMCPExecDepsReachARoutedServerWithAuth(t *testing.T) {
	srv := routedAuthServer(t)
	t.Setenv("ROUTED_EXEC_TOKEN", routedPerimeterToken)
	c := client.New(srv.URL+mcpserver.RoutedMountPath, routedPerimeterToken).WithInsecureHTTP(true).WithTokenEnv("ROUTED_EXEC_TOKEN")
	d := cliMCPExecDeps{c: c, cfg: &clientconfig.Config{ServerURL: srv.URL + "/mcp"}}
	out, err := d.Call(context.Background(), "kb_status", map[string]any{"kb": "work-kb"})
	if err != nil || !strings.Contains(out, "work-kb") {
		t.Fatalf("out %q, err %v", out, err)
	}
}

// `surveyor source test` addresses a KB of a routed server through the routed
// endpoint, and the start-up check names a perimeter the token does not see.
func TestSurveyorSourceTestClientAndUnknownPerimeterOnARoutedServer(t *testing.T) {
	srv := routedAuthServer(t)
	t.Setenv("SURVEYOR_TOKEN", routedPerimeterToken)
	t.Setenv("CLOUD_API_KEY", surveyorCanary+"-key")
	cfgPath := surveyorCfg(t, srv.URL, routedPerimeterToken)
	raw, _ := os.ReadFile(cfgPath)
	if err := os.WriteFile(cfgPath, []byte("allow_insecure_http: true\n"+string(raw)), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := surveyor.LoadConfig(cfgPath, noop())
	if err != nil {
		t.Fatal(err)
	}
	c := surveyorKBClient(client.New(cfg.ServerURL, cfg.Token()).WithInsecureHTTP(true).WithTokenEnv(cfg.TokenEnv), cfg, "work-kb")
	if err := c.Ping(startupCallTimeout); err != nil {
		t.Fatal(err)
	}
	if out, err := c.CallTool("kb_status", json.RawMessage(`{}`)); err != nil || !strings.Contains(out.Text, "work-kb") {
		t.Fatalf("out %+v err %v", out, err)
	}

	ghost := strings.Replace(string(raw), "kbs: [kb-a, work-kb]", "kbs: [ghost]", 1)
	if err := os.WriteFile(cfgPath, []byte("allow_insecure_http: true\n"+ghost), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = surveyorStartup(context.Background(), cfgPath, noop(), fastRetry(time.Second), &strings.Builder{})
	if msg := wantExit(t, err, 2); !strings.Contains(msg, "does not see") || !strings.Contains(msg, "ghost") {
		t.Fatalf("message = %s", msg)
	}
}
