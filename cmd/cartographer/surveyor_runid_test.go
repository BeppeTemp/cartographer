package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/componentproto"
	"github.com/BeppeTemp/cartographer/internal/surveyor"
)

// AC-surveyor-run-commit-1: the id a task's writes carry in
// Cartographer-Surveyor-Run is the run_id the same task later reports to the
// runs route; calls made outside a run (the unit listing) carry none.
func TestSurveyorRunIDOnTheWireEqualsTheReportedOne(t *testing.T) {
	var mu sync.Mutex
	var toolRuns []string // run header of each tools/call
	var reported []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/health":
			w.Write([]byte(`{"status":"ok","version":"1.0.0"}`))
		case r.URL.Path == componentproto.RunsPath:
			var b struct {
				Runs []struct {
					RunID string `json:"run_id"`
				} `json:"runs"`
			}
			_ = json.Unmarshal(body, &b)
			mu.Lock()
			for _, x := range b.Runs {
				reported = append(reported, x.RunID)
			}
			mu.Unlock()
			w.Write([]byte(`{}`))
		case strings.HasPrefix(r.URL.Path, "/api/"):
			w.Write([]byte(`{"runs_wanted":true}`))
		default:
			if r.Header.Get("Mcp-Method") == "tools/call" {
				mu.Lock()
				toolRuns = append(toolRuns, r.Header.Get(client.SurveyorRunHeader))
				mu.Unlock()
			}
			if strings.Contains(string(body), `"kb_repair"`) { // a failing task is reported
				w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"isError":true,"content":[{"type":"text","text":"boom"}]}}`))
				return
			}
			w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"{}"}]}}`))
		}
	}))
	defer srv.Close()

	t.Setenv("SURVEYOR_TOKEN", "tok")
	path := filepath.Join(t.TempDir(), "surveyor.yaml")
	yaml := "name: repair-night\nserver_url: " + srv.URL + "\ntoken_env: SURVEYOR_TOKEN\nkbs: [kb-a]\n" +
		"schedule:\n  window: {start: \"00:00\", end: \"23:59\", timezone: UTC}\ntasks:\n  - kind: repair\n"
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := surveyor.LoadConfig(path, surveyor.DefaultKinds())
	if err != nil {
		t.Fatal(err)
	}
	env := &surveyorEnv{cfg: cfg, client: client.New(srv.URL+"/mcp", "tok").WithInsecureHTTP(true)}
	old := surveyorBeatInterval
	surveyorBeatInterval = 20 * time.Millisecond
	t.Cleanup(func() { surveyorBeatInterval = old })
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() { // stop once a beat has carried the run
		for ctx.Err() == nil {
			mu.Lock()
			n := len(reported)
			mu.Unlock()
			if n > 0 {
				cancel()
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()
	surveyorRunFn(ctx, env, false)

	mu.Lock()
	defer mu.Unlock()
	var withRun []string
	for _, id := range toolRuns {
		if id != "" {
			withRun = append(withRun, id)
		}
	}
	if len(withRun) == 0 || len(reported) == 0 {
		t.Fatalf("no run seen: header ids %v, reported %v", toolRuns, reported)
	}
	for _, id := range withRun {
		if id != reported[0] {
			t.Errorf("writes carry %q, the run reported %q", id, reported[0])
		}
	}
}
