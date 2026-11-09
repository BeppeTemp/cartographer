package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/kbtest"
	"github.com/BeppeTemp/cartographer/internal/mcpserver"
	"github.com/BeppeTemp/cartographer/internal/worker"
	"github.com/BeppeTemp/cartographer/internal/workerconfig"
)

// A beat and a settings report from the worker land in the Atlas payloads, and
// a person's tool call (not the worker's) moves the idle signal the beater reads.
func TestWorkerBeatEndToEnd(t *testing.T) {
	const marker = "MARKER-SECRET-VALUE"
	k := kbtest.New(t)
	k.AuthName = "kb-a"
	multi := mcpserver.NewMultiKBServer("test")
	multi.MountKB("kb-a", func(s *mcpserver.Server) { mcpserver.RegisterKBTools(s, k, mcpserver.Deps{}) })
	multi.EnableWeb(nil)
	ts := auth.NewScopedTokenStore([]auth.ScopedToken{
		{Token: "worker-tok", Principal: "worker-a", Policy: auth.Policy{Permissions: []auth.Permission{{KB: "kb-a", Write: true}}}},
		{Token: "person-tok", Principal: "person", Policy: auth.Policy{Admin: true}},
	})
	srv := httptest.NewServer(ts.Middleware(multi.Handler()))
	t.Cleanup(srv.Close)

	c := client.New(srv.URL+"/mcp", "worker-tok").WithInsecureHTTP(true)
	rep, err := workerconfig.Build([]byte("name: docs-night\ntoken_env: WORKER_TOKEN\nkbs: [kb-a]\n"), "docs-night", "docs-night-ab12cd", "1.0", []string{"kb-a"},
		func(string) string { return marker })
	if err != nil {
		t.Fatal(err)
	}
	b := &worker.Beater{API: c, Worker: "docs-night-ab12cd", Perimeter: "docs-night", Version: "1.0", KBs: []string{"kb-a"}, Settings: rep}
	b.Beat(context.Background(), worker.State{Run: worker.StateWaiting})

	get := func(token, path string, out any) {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, srv.URL+path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var raw strings.Builder
		buf := make([]byte, 4096)
		for {
			n, e := resp.Body.Read(buf)
			raw.Write(buf[:n])
			if e != nil {
				break
			}
		}
		if resp.StatusCode != 200 {
			t.Fatalf("GET %s = %d %s", path, resp.StatusCode, raw.String())
		}
		if strings.Contains(raw.String(), marker) {
			t.Fatalf("GET %s carries the secret marker", path)
		}
		if err := json.Unmarshal([]byte(raw.String()), out); err != nil {
			t.Fatal(err)
		}
	}
	var ws struct {
		Workers []struct{ Worker, State string } `json:"workers"`
	}
	get("person-tok", "/api/ui/v1/kbs/kb-a/workers", &ws)
	if len(ws.Workers) != 1 || ws.Workers[0].Worker != "docs-night-ab12cd" || ws.Workers[0].State != "waiting" {
		t.Fatalf("workers = %+v", ws)
	}
	var st struct {
		Workers []struct{ Perimeter string } `json:"workers"`
	}
	get("person-tok", "/api/ui/v1/settings", &st)
	if len(st.Workers) != 1 || st.Workers[0].Perimeter != "docs-night" {
		t.Fatalf("settings workers = %+v", st)
	}

	// No call yet: idle since the server started. A person's call resets it; the worker's does not.
	d0, known, _ := b.IdleFor(context.Background(), "kb-a")
	if !known {
		t.Fatal("idle must be known against this server")
	}
	time.Sleep(30 * time.Millisecond)
	wc := client.New(srv.URL+"/mcp", "worker-tok").WithInsecureHTTP(true).WithKB("kb-a")
	_, _ = wc.Call("kb_status", map[string]any{})
	b.Beat(context.Background(), worker.State{Run: worker.StateWaiting}) // kept only
	if d, _ := sendAndIdle(b); d < d0 {
		t.Fatalf("a worker call moved the idle signal: %v < %v", d, d0)
	}
	pc := client.New(srv.URL+"/mcp", "person-tok").WithInsecureHTTP(true).WithKB("kb-a")
	if _, err := pc.Call("kb_status", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if d, _ := sendAndIdle(b); d >= d0 {
		t.Fatalf("a person's call did not reset idle: %v >= %v", d, d0)
	}
}

// sendAndIdle forces a beat through the public surface (Beat with stopped would
// remove the declaration, so a fresh snapshot is sent by the next tick; here a
// short interval goroutine is the real path).
func sendAndIdle(b *worker.Beater) (time.Duration, bool) {
	b.Interval = 10 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b.Start(ctx)
	time.Sleep(80 * time.Millisecond)
	d, known, _ := b.IdleFor(context.Background(), "kb-a")
	return d, known
}
