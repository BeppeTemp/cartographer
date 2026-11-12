package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/plugin/core"
	"github.com/BeppeTemp/cartographer/internal/surveyor"
)

const surveyorCanary = "canary-secret-9c1e"

func fastRetry(total time.Duration) retryPolicy {
	return retryPolicy{total: total, first: time.Millisecond, max: 2 * time.Millisecond,
		sleep: func(context.Context, time.Duration) bool { time.Sleep(time.Millisecond); return true }}
}

// fakeServer serves /health like the real one (public path, anonymous callers get
// 200 with no kbs) and answers server/discover with 401 unless the token is right.
func fakeServer(t *testing.T, kbs []string, bootstrapping *atomic.Int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authed := r.Header.Get("Authorization") == "Bearer "+surveyorCanary+"-token"
		if r.URL.Path == "/health" {
			resp := map[string]any{"status": "ok", "ready": true}
			if bootstrapping != nil && bootstrapping.Add(-1) >= 0 {
				resp["bootstrapping"], resp["ready"] = true, false
			} else if authed && kbs != nil {
				list := []map[string]string{}
				for _, k := range kbs {
					list = append(list, map[string]string{"name": k})
				}
				resp["kbs"] = list
			}
			json.NewEncoder(w).Encode(resp)
			return
		}
		if !authed {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func surveyorCfg(t *testing.T, url string, token string) string {
	t.Helper()
	t.Setenv("SURVEYOR_TOKEN", token)
	t.Setenv("CLOUD_API_KEY", surveyorCanary+"-key")
	body := `name: docs-night
server_url: ` + url + `/mcp
token_env: SURVEYOR_TOKEN
kbs: [kb-a, work-kb]
models:
  - name: cloud
    provider: anthropic
    model: some-model
    api_key_env: CLOUD_API_KEY
    budget: {daily_tokens: 1000}
schedule:
  window: {start: "23:00", end: "06:00", timezone: UTC}
tasks:
  - kind: noop
    model: cloud
`
	p := filepath.Join(t.TempDir(), "surveyor.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func noop() *surveyor.Kinds { return surveyor.NewKinds(surveyor.Kind{Name: "noop"}) }

func startup(t *testing.T, srv *httptest.Server, token string, retry retryPolicy) (*surveyorEnv, error, string) {
	t.Helper()
	var out strings.Builder
	env, err := surveyorStartup(context.Background(), surveyorCfg(t, srv.URL, token), noop(), retry, &out)
	return env, err, out.String()
}

func wantExit(t *testing.T, err error, code int) string {
	t.Helper()
	se, ok := err.(*startError)
	if !ok || se.code != code {
		t.Fatalf("err = %v, want exit %d", err, code)
	}
	if strings.Contains(err.Error(), surveyorCanary) {
		t.Fatalf("error leaks a secret: %v", err)
	}
	return err.Error()
}

func TestSurveyorStartupOK(t *testing.T) {
	srv := fakeServer(t, []string{"kb-a", "work-kb", "other"}, nil)
	env, err, out := startup(t, srv, surveyorCanary+"-token", fastRetry(time.Second))
	if err != nil || env == nil || len(env.models) != 1 {
		t.Fatalf("env=%v err=%v", env, err)
	}
	if strings.Contains(out, surveyorCanary) {
		t.Fatal("output leaks a secret")
	}
}

func TestSurveyorStartupBootstrappingThenReady(t *testing.T) {
	var n atomic.Int32
	n.Store(2)
	srv := fakeServer(t, []string{"kb-a", "work-kb"}, &n)
	if _, err, _ := startup(t, srv, surveyorCanary+"-token", fastRetry(time.Second)); err != nil {
		t.Fatal(err)
	}
}

func TestSurveyorStartupUnreachableExits1(t *testing.T) {
	srv := fakeServer(t, nil, nil)
	url := srv.URL
	srv.Close()
	p := surveyorCfg(t, url, surveyorCanary+"-token")
	_, err := surveyorStartup(context.Background(), p, noop(), fastRetry(20*time.Millisecond), &strings.Builder{})
	if msg := wantExit(t, err, 1); !strings.Contains(msg, url) {
		t.Fatalf("message does not name the URL: %s", msg)
	}
}

func TestSurveyorStartupWrongTokenExits2NamingTheVariable(t *testing.T) {
	srv := fakeServer(t, []string{"kb-a", "work-kb"}, nil)
	_, err, _ := startup(t, srv, surveyorCanary+"-wrong", fastRetry(time.Second))
	if msg := wantExit(t, err, 2); !strings.Contains(msg, "SURVEYOR_TOKEN") {
		t.Fatalf("message does not name the variable: %s", msg)
	}
}

func TestSurveyorStartupNoKBsListed(t *testing.T) {
	srv := fakeServer(t, nil, nil)
	_, err, _ := startup(t, srv, surveyorCanary+"-token", fastRetry(time.Second))
	if msg := wantExit(t, err, 2); !strings.Contains(msg, "SURVEYOR_TOKEN") {
		t.Fatal(msg)
	}
}

func TestSurveyorStartupKBNotVisible(t *testing.T) {
	srv := fakeServer(t, []string{"kb-a"}, nil)
	_, err, _ := startup(t, srv, surveyorCanary+"-token", fastRetry(time.Second))
	msg := wantExit(t, err, 2)
	if !strings.Contains(msg, "work-kb") || strings.Contains(msg, "kb-a,") || !strings.Contains(msg, "SURVEYOR_TOKEN") {
		t.Fatalf("message = %s", msg)
	}
}

func TestSurveyorBadConfigExits2(t *testing.T) {
	p := filepath.Join(t.TempDir(), "w.yaml")
	os.WriteFile(p, []byte("bogus: 1\n"), 0o600)
	_, err := surveyorStartup(context.Background(), p, noop(), fastRetry(time.Second), &strings.Builder{})
	wantExit(t, err, 2)
}

func TestSurveyorCommandWithoutConfigPrintsUsageExit2(t *testing.T) {
	if got := cmdSurveyor(nil); got != 2 {
		t.Fatalf("exit = %d, want 2", got)
	}
}

func TestSurveyorDispatch(t *testing.T) {
	old := surveyorFn
	defer func() { surveyorFn = old }()
	var got []string
	surveyorFn = func(a []string) int { got = a; return 7 }
	if code := run([]string{"surveyor", "--config", "x"}); code != 7 || len(got) != 2 {
		t.Fatalf("code=%d args=%v", code, got)
	}
}

func TestSurveyorStartupRetriesA5xx(t *testing.T) {
	var n atomic.Int32
	kbs := []string{"kb-a", "work-kb"}
	inner := fakeServer(t, kbs, nil)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" && n.Add(1) <= 2 {
			http.Error(w, "bad gateway", http.StatusBadGateway)
			return
		}
		inner.Config.Handler.ServeHTTP(w, r)
	}))
	t.Cleanup(proxy.Close)
	if _, err, _ := startup(t, proxy, surveyorCanary+"-token", fastRetry(time.Second)); err != nil {
		t.Fatalf("a 502 during a rollout must be retried: %v", err)
	}
	if n.Load() < 3 {
		t.Fatalf("health called %d times", n.Load())
	}
}

// The start log says which declared private-network hosts this Surveyor grants
// and which stay refused; a plugin that declares none adds no line.
func TestPrintPrivateGrant(t *testing.T) {
	p := &core.Plugin{Manifest: core.Manifest{Name: "acme-wiki", Components: map[core.Side]core.Component{
		core.SideSurveyor: {Permissions: &core.Permissions{Net: []string{"*.example.com"}, NetPrivate: []string{"wiki.example.com", "docs.example.com"}}},
	}}}
	var b strings.Builder
	printPrivateGrant(&b, p, []string{"wiki.example.com"})
	want := "surveyor: plugin acme-wiki may reach private-network hosts: wiki.example.com\n" +
		"surveyor: plugin acme-wiki private-network hosts not granted by this Surveyor: docs.example.com\n"
	if b.String() != want {
		t.Errorf("got %q", b.String())
	}
	b.Reset()
	printPrivateGrant(&b, p, nil)
	if b.String() != "surveyor: plugin acme-wiki private-network hosts not granted by this Surveyor: wiki.example.com, docs.example.com\n" {
		t.Errorf("no grant: %q", b.String())
	}
	b.Reset()
	printPrivateGrant(&b, &core.Plugin{Manifest: core.Manifest{Name: "plain"}}, nil)
	if b.String() != "" {
		t.Errorf("no net_private: %q", b.String())
	}
}
