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

	"github.com/BeppeTemp/cartographer/internal/worker"
)

const workerCanary = "canary-secret-9c1e"

func fastRetry(total time.Duration) retryPolicy {
	return retryPolicy{total: total, first: time.Millisecond, max: 2 * time.Millisecond,
		sleep: func(context.Context, time.Duration) bool { time.Sleep(time.Millisecond); return true }}
}

// fakeServer serves /health like the real one (public path, anonymous callers get
// 200 with no kbs) and answers server/discover with 401 unless the token is right.
func fakeServer(t *testing.T, kbs []string, bootstrapping *atomic.Int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authed := r.Header.Get("Authorization") == "Bearer "+workerCanary+"-token"
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

func workerCfg(t *testing.T, url string, token string) string {
	t.Helper()
	t.Setenv("WORKER_TOKEN", token)
	t.Setenv("CLOUD_API_KEY", workerCanary+"-key")
	body := `name: docs-night
server_url: ` + url + `/mcp
token_env: WORKER_TOKEN
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
	p := filepath.Join(t.TempDir(), "worker.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func noop() *worker.Kinds { return worker.NewKinds(worker.Kind{Name: "noop"}) }

func startup(t *testing.T, srv *httptest.Server, token string, retry retryPolicy) (*workerEnv, error, string) {
	t.Helper()
	var out strings.Builder
	env, err := workerStartup(context.Background(), workerCfg(t, srv.URL, token), noop(), retry, &out)
	return env, err, out.String()
}

func wantExit(t *testing.T, err error, code int) string {
	t.Helper()
	se, ok := err.(*startError)
	if !ok || se.code != code {
		t.Fatalf("err = %v, want exit %d", err, code)
	}
	if strings.Contains(err.Error(), workerCanary) {
		t.Fatalf("error leaks a secret: %v", err)
	}
	return err.Error()
}

func TestWorkerStartupOK(t *testing.T) {
	srv := fakeServer(t, []string{"kb-a", "work-kb", "other"}, nil)
	env, err, out := startup(t, srv, workerCanary+"-token", fastRetry(time.Second))
	if err != nil || env == nil || len(env.models) != 1 {
		t.Fatalf("env=%v err=%v", env, err)
	}
	if strings.Contains(out, workerCanary) {
		t.Fatal("output leaks a secret")
	}
}

func TestWorkerStartupBootstrappingThenReady(t *testing.T) {
	var n atomic.Int32
	n.Store(2)
	srv := fakeServer(t, []string{"kb-a", "work-kb"}, &n)
	if _, err, _ := startup(t, srv, workerCanary+"-token", fastRetry(time.Second)); err != nil {
		t.Fatal(err)
	}
}

func TestWorkerStartupUnreachableExits1(t *testing.T) {
	srv := fakeServer(t, nil, nil)
	url := srv.URL
	srv.Close()
	p := workerCfg(t, url, workerCanary+"-token")
	_, err := workerStartup(context.Background(), p, noop(), fastRetry(20*time.Millisecond), &strings.Builder{})
	if msg := wantExit(t, err, 1); !strings.Contains(msg, url) {
		t.Fatalf("message does not name the URL: %s", msg)
	}
}

func TestWorkerStartupWrongTokenExits2NamingTheVariable(t *testing.T) {
	srv := fakeServer(t, []string{"kb-a", "work-kb"}, nil)
	_, err, _ := startup(t, srv, workerCanary+"-wrong", fastRetry(time.Second))
	if msg := wantExit(t, err, 2); !strings.Contains(msg, "WORKER_TOKEN") {
		t.Fatalf("message does not name the variable: %s", msg)
	}
}

func TestWorkerStartupNoKBsListed(t *testing.T) {
	srv := fakeServer(t, nil, nil)
	_, err, _ := startup(t, srv, workerCanary+"-token", fastRetry(time.Second))
	if msg := wantExit(t, err, 2); !strings.Contains(msg, "WORKER_TOKEN") {
		t.Fatal(msg)
	}
}

func TestWorkerStartupKBNotVisible(t *testing.T) {
	srv := fakeServer(t, []string{"kb-a"}, nil)
	_, err, _ := startup(t, srv, workerCanary+"-token", fastRetry(time.Second))
	msg := wantExit(t, err, 2)
	if !strings.Contains(msg, "work-kb") || strings.Contains(msg, "kb-a,") || !strings.Contains(msg, "WORKER_TOKEN") {
		t.Fatalf("message = %s", msg)
	}
}

func TestWorkerBadConfigExits2(t *testing.T) {
	p := filepath.Join(t.TempDir(), "w.yaml")
	os.WriteFile(p, []byte("bogus: 1\n"), 0o600)
	_, err := workerStartup(context.Background(), p, noop(), fastRetry(time.Second), &strings.Builder{})
	wantExit(t, err, 2)
}

func TestWorkerCommandWithoutConfigPrintsUsageExit2(t *testing.T) {
	if got := cmdWorker(nil); got != 2 {
		t.Fatalf("exit = %d, want 2", got)
	}
}

func TestWorkerDispatch(t *testing.T) {
	old := workerFn
	defer func() { workerFn = old }()
	var got []string
	workerFn = func(a []string) int { got = a; return 7 }
	if code := run([]string{"worker", "--config", "x"}); code != 7 || len(got) != 2 {
		t.Fatalf("code=%d args=%v", code, got)
	}
}
