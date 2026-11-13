package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/kbtest"
	"github.com/BeppeTemp/cartographer/internal/mcpserver"
	"github.com/BeppeTemp/cartographer/internal/surveyor"
	"github.com/BeppeTemp/cartographer/internal/surveyorconfig"
)

// A beat and a settings report from the surveyor land in the Atlas payloads, and
// a person's tool call (not the surveyor's) moves the idle signal the beater reads.
func TestSurveyorBeatEndToEnd(t *testing.T) {
	const marker = "MARKER-SECRET-VALUE"
	k := kbtest.New(t)
	k.AuthName = "kb-a"
	multi := mcpserver.NewMultiKBServer("test")
	multi.MountKB("kb-a", func(s *mcpserver.Server) { mcpserver.RegisterKBTools(s, k, mcpserver.Deps{}) })
	multi.EnableWeb(nil)
	ts := auth.NewScopedTokenStore([]auth.ScopedToken{
		{Token: "surveyor-tok", Principal: "surveyor-a", Policy: auth.Policy{Permissions: []auth.Permission{{KB: "kb-a", Write: true}}}},
		{Token: "person-tok", Principal: "person", Policy: auth.Policy{Admin: true}},
	})
	srv := httptest.NewServer(ts.Middleware(multi.Handler()))
	t.Cleanup(srv.Close)

	c := client.New(srv.URL+"/mcp", "surveyor-tok").WithInsecureHTTP(true)
	rep, err := surveyorconfig.Build([]byte("name: docs-night\ntoken_env: SURVEYOR_TOKEN\nkbs: [kb-a]\n"), "docs-night", "docs-night-ab12cd", "1.0", []string{"kb-a"},
		func(string) string { return marker })
	if err != nil {
		t.Fatal(err)
	}
	sb := &slowBeat{BeatAPI: c}
	b := &surveyor.Beater{API: sb, Surveyor: "docs-night-ab12cd", Perimeter: "docs-night", Version: "1.0", KBs: []string{"kb-a"}, Settings: rep, Logf: t.Logf}
	b.Beat(context.Background(), surveyor.State{Run: surveyor.StateWaiting})
	if err := sb.failure(); err != nil {
		t.Fatalf("first heartbeat failed: %v", err)
	}

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
		Components []struct {
			Replicas []struct{ ID, State string } `json:"replicas"`
		} `json:"components"`
	}
	get("person-tok", "/api/ui/v1/kbs/kb-a/components", &ws)
	if len(ws.Components) != 1 || len(ws.Components[0].Replicas) != 1 || ws.Components[0].Replicas[0].ID != "docs-night-ab12cd" || ws.Components[0].Replicas[0].State != "waiting" {
		t.Fatalf("components = %+v", ws)
	}
	var st struct {
		Surveyors []struct{ Perimeter string } `json:"surveyors"`
	}
	get("person-tok", "/api/ui/v1/settings", &st)
	if len(st.Surveyors) != 1 || st.Surveyors[0].Perimeter != "docs-night" {
		t.Fatalf("settings surveyors = %+v", st)
	}

	// No call yet: idle since the server started. A person's call resets it; the surveyor's does not.
	d0, known, _ := b.IdleFor(context.Background(), "kb-a")
	if !known {
		t.Fatal("idle must be known against this server")
	}
	time.Sleep(30 * time.Millisecond)
	wc := client.New(srv.URL+"/mcp", "surveyor-tok").WithInsecureHTTP(true).WithKB("kb-a")
	_, _ = wc.Call("kb_status", map[string]any{})
	d1 := beatAndIdle(t, b, sb, 1)
	if d1 < d0 {
		t.Fatalf("a surveyor call moved the idle signal: %v < %v", d1, d0)
	}
	pc := client.New(srv.URL+"/mcp", "person-tok").WithInsecureHTTP(true).WithKB("kb-a")
	before := time.Now()
	if _, err := pc.Call("kb_status", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	d2 := beatAndIdle(t, b, sb, 2)
	if d2 >= d1 {
		t.Fatalf("a person's call did not reset idle: %v >= %v", d2, d1)
	}
	// The server runs in-process on this clock: the idle time cannot exceed what
	// has passed since the call. Up to a margin: the idle is a difference of wall-clock
	// timestamps that crossed JSON (no monotonic reading left), `el` a monotonic one,
	// and on Windows the two drift apart by microseconds (#2032: 353.3708ms against
	// 353.3686ms). One tick of the default Windows timer, 16 ms, keeps the check meaningful:
	// an idle that was not reset is at least the 30 ms slept above.
	if el := time.Since(before); d2 > el+16*time.Millisecond {
		t.Fatalf("idle after a person's call = %v, more than the %v since the call", d2, el)
	}
}

// slowBeat delays every heartbeat, as a loaded gate does: the answer a test
// reads must not depend on the beat being quick. It also remembers the last
// heartbeat error, because the Beater only logs it and keeps the old answer,
// which would let an idle assertion pass without a fresh answer.
type slowBeat struct {
	surveyor.BeatAPI
	mu  sync.Mutex
	err error
}

func (s *slowBeat) Heartbeat(report any, timeout time.Duration) (*client.HeartbeatAnswer, error) {
	time.Sleep(40 * time.Millisecond)
	a, err := s.BeatAPI.Heartbeat(report, timeout)
	s.mu.Lock()
	s.err = err
	s.mu.Unlock()
	return a, err
}

// failure is the error of the last heartbeat, nil when it succeeded.
func (s *slowBeat) failure() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

// beatAndIdle refreshes the beater's answer synchronously and reads the idle
// time from it: Beat sends at once when the last run's time changes, so n (a
// strictly increasing counter) makes this beat a send, and the answer it reads
// is the one just received, whatever the load. The interval stays the default,
// so the answer is never stale; a failed beat or an unknown idle fails the test.
func beatAndIdle(t *testing.T, b *surveyor.Beater, sb *slowBeat, n int) time.Duration {
	t.Helper()
	b.Beat(context.Background(), surveyor.State{
		Run:     surveyor.StateWaiting,
		LastRun: surveyor.LastRun{At: time.Unix(1_700_000_000+int64(n), 0)},
	})
	if err := sb.failure(); err != nil {
		t.Fatalf("heartbeat %d failed, the idle answer is stale: %v", n, err)
	}
	d, known, _ := b.IdleFor(context.Background(), "kb-a")
	if !known {
		t.Fatal("idle unknown right after a beat")
	}
	return d
}
