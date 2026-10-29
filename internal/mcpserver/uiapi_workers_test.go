package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/auth"
)

// workerFixture mounts kb-a and kb-b. Tokens: "worker-a" writes kb-a,
// "worker-ab" writes both, "reader" reads kb-a, "person" writes kb-a.
type workerFixture struct {
	multi *MultiKBServer
	h     http.Handler
	clock *testClock
}

type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *testClock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func newWorkerFixture(t *testing.T) *workerFixture {
	t.Helper()
	ka, kbb := uiFixtureKB(t, "kb-a"), uiFixtureKB(t, "kb-b")
	ts := auth.NewScopedTokenStore([]auth.ScopedToken{
		{Token: "worker-a", Principal: "worker-a", Policy: auth.Policy{Permissions: []auth.Permission{{KB: "kb-a", Write: true}}}},
		{Token: "worker-ab", Principal: "worker-ab", Policy: auth.Policy{Permissions: []auth.Permission{{KB: "kb-a", Write: true}, {KB: "kb-b", Write: true}}}},
		{Token: "reader", Principal: "reader", Policy: auth.Policy{Permissions: []auth.Permission{{KB: "kb-a"}}}},
		{Token: "person", Principal: "person", Policy: auth.Policy{Permissions: []auth.Permission{{KB: "kb-a", Write: true}}}},
	})
	multi := NewMultiKBServer("test")
	multi.MountKB("kb-a", func(s *Server) { RegisterKBTools(s, ka, Deps{}) })
	multi.MountKB("kb-b", func(s *Server) { RegisterKBTools(s, kbb, Deps{}) })
	clock := &testClock{t: time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC)}
	multi.workers.now = clock.now
	for _, s := range multi.servers {
		s.now = clock.now
	}
	return &workerFixture{multi: multi, h: ts.Middleware(multi.Handler()), clock: clock}
}

func (f *workerFixture) post(t *testing.T, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, ok := body.([]byte)
	if !ok {
		var err error
		if raw, err = json.Marshal(body); err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(http.MethodPost, WorkersPath, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rr := httptest.NewRecorder()
	f.h.ServeHTTP(rr, req)
	return rr
}

func beat(over map[string]any) map[string]any {
	b := map[string]any{"worker": "w-1", "perimeter": "team-a", "version": "1.0", "kbs": []string{"kb-a"}, "state": "idle"}
	for k, v := range over {
		b[k] = v
	}
	return b
}

func (f *workerFixture) callAs(principal string, tool string) {
	ctx := auth.ContextWithPrincipal(context.Background(), auth.Principal{ID: principal, Policy: auth.Policy{Admin: true}})
	f.multi.servers["kb-a"].callTool(ctx, tool, json.RawMessage(`{}`))
}

func TestWorkers_AcceptedBeatIsStoredAndAnswered(t *testing.T) {
	t.Parallel()
	f := newWorkerFixture(t)
	rr := f.post(t, "worker-a", beat(map[string]any{"kbs": []string{"kb-a", "kb-a", "kb-b", "nope"}, "interval_seconds": 5}))
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	var ans struct {
		Accepted      []string                  `json:"accepted"`
		Kbs           map[string]map[string]any `json:"kbs"`
		ActivityKnown bool                      `json:"activity_known"`
		StartedAt     string                    `json:"server_started_at"`
		Now           string                    `json:"now"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &ans); err != nil {
		t.Fatal(err)
	}
	if len(ans.Accepted) != 1 || ans.Accepted[0] != "kb-a" || !ans.ActivityKnown || ans.StartedAt == "" || ans.Now == "" {
		t.Fatalf("answer = %+v", ans)
	}
	// No call yet: null, and no idle_seconds.
	if v, ok := ans.Kbs["kb-a"]["last_activity_at"]; !ok || v != nil {
		t.Fatalf("last_activity_at = %v, want null", ans.Kbs["kb-a"])
	}
	if _, ok := ans.Kbs["kb-a"]["idle_seconds"]; ok {
		t.Fatal("idle_seconds present with a null last_activity_at")
	}
	live := f.multi.workers.live("kb-a")
	if len(live) != 1 || live[0].IntervalSeconds != minWorkerInterval {
		t.Fatalf("store = %+v (interval must clamp to %d)", live, minWorkerInterval)
	}
	if len(f.multi.workers.live("kb-b")) != 0 {
		t.Fatal("a KB the token cannot write was declared")
	}
}

func TestWorkers_StaleExpiresAndStoppedRemoves(t *testing.T) {
	t.Parallel()
	f := newWorkerFixture(t)
	f.post(t, "worker-a", beat(map[string]any{"interval_seconds": 60}))
	f.clock.advance(179 * time.Second)
	if len(f.multi.workers.live("kb-a")) != 1 {
		t.Fatal("declaration should be live before 3 intervals")
	}
	f.clock.advance(2 * time.Second)
	if len(f.multi.workers.live("kb-a")) != 0 || f.multi.workers.hasLive("worker-a") {
		t.Fatal("stale declaration still live")
	}
	f.post(t, "worker-a", beat(nil))
	if rr := f.post(t, "worker-a", beat(map[string]any{"state": "stopped"})); rr.Code != http.StatusOK {
		t.Fatalf("stopped: %d", rr.Code)
	}
	if f.multi.workers.hasLive("worker-a") {
		t.Fatal("stopped left a declaration")
	}
}

func TestWorkers_TimeBoundsAndSanitising(t *testing.T) {
	t.Parallel()
	f := newWorkerFixture(t)
	now := f.clock.now()
	f.post(t, "worker-a", beat(map[string]any{
		"version":        "1.0\n\x00" + strings.Repeat("x", 100),
		"last_run":       map[string]any{"at": now.Add(72 * time.Hour), "units_done": -3, "tokens_spent": int64(1) << 50, "commits": 2},
		"next_window_at": now.Add(9 * 24 * time.Hour),
	}))
	d := f.multi.workers.live("kb-a")[0]
	if d.LastRun == nil || !d.LastRun.At.Equal(now) || d.LastRun.UnitsDone != 0 || d.LastRun.TokensSpent != workerMaxCount || d.LastRun.Commits != 2 {
		t.Fatalf("last_run = %+v", d.LastRun)
	}
	if d.NextWindowAt != nil {
		t.Fatal("a window 9 days ahead must be omitted")
	}
	if strings.ContainsAny(d.Version, "\n\x00") || len(d.Version) > 32 {
		t.Fatalf("version = %q", d.Version)
	}
	f.post(t, "worker-a", beat(map[string]any{"next_window_at": now.Add(-time.Hour)}))
	if f.multi.workers.live("kb-a")[0].NextWindowAt != nil {
		t.Fatal("a past window must be omitted")
	}
	f.post(t, "worker-a", beat(map[string]any{"next_window_at": now.Add(3 * 24 * time.Hour)}))
	if f.multi.workers.live("kb-a")[0].NextWindowAt == nil {
		t.Fatal("a window in range must be kept")
	}
}

func TestWorkers_Budgets(t *testing.T) {
	t.Parallel()
	f := newWorkerFixture(t)
	rows := func(n int) []map[string]any {
		out := []map[string]any{}
		for i := 0; i < n; i++ {
			out = append(out, map[string]any{"profile": "p" + string(rune('a'+i)), "tier": "small", "spent": 5, "limit": 10})
		}
		return out
	}
	if rr := f.post(t, "worker-a", beat(map[string]any{"budgets": rows(2)})); rr.Code != http.StatusOK {
		t.Fatalf("two profiles: %d", rr.Code)
	}
	if got := f.multi.workers.live("kb-a")[0].Budgets; len(got) != 2 || got[1].Profile != "pb" || got[0].Spent != 5 {
		t.Fatalf("budgets = %+v", got)
	}
	if rr := f.post(t, "worker-a", beat(map[string]any{"budgets": rows(9)})); rr.Code != http.StatusBadRequest {
		t.Fatalf("nine profiles: %d, want 400", rr.Code)
	}
	if rr := f.post(t, "worker-a", beat(map[string]any{"budgets": []map[string]any{{"profile": "p", "tier": "huge"}}})); rr.Code != http.StatusBadRequest {
		t.Fatalf("bad tier: %d, want 400", rr.Code)
	}
}

func TestWorkers_Rejections(t *testing.T) {
	t.Parallel()
	f := newWorkerFixture(t)
	for name, body := range map[string]any{
		"bad worker id": beat(map[string]any{"worker": "Bad Id"}),
		"no perimeter":  beat(map[string]any{"perimeter": ""}),
		"no kbs":        beat(map[string]any{"kbs": []string{}}),
		"too many kbs":  beat(map[string]any{"kbs": make([]string, 33)}),
		"bad state":     beat(map[string]any{"state": "napping"}),
		"not an object": []byte(`[1]`),
		"not json":      []byte(`{`),
	} {
		if rr := f.post(t, "worker-a", body); rr.Code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", name, rr.Code)
		}
	}
	if len(f.multi.workers.live("kb-a")) != 0 {
		t.Fatal("a refused beat was stored")
	}
	// Oversize.
	if rr := f.post(t, "worker-a", beat(map[string]any{"version": strings.Repeat("v", maxWorkerBody)})); rr.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversize: %d, want 413", rr.Code)
	}
	// Wrong content type.
	req := httptest.NewRequest(http.MethodPost, WorkersPath, strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer worker-a")
	req.Header.Set("Content-Type", "text/plain")
	rr := httptest.NewRecorder()
	f.h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnsupportedMediaType {
		t.Errorf("content type: %d, want 415", rr.Code)
	}
	// Wrong method.
	req = httptest.NewRequest(http.MethodGet, WorkersPath, nil)
	req.Header.Set("Authorization", "Bearer worker-a")
	rr = httptest.NewRecorder()
	f.h.ServeHTTP(rr, req)
	if rr.Code != http.StatusMethodNotAllowed || rr.Header().Get("Allow") != "POST" {
		t.Errorf("method: %d allow %q", rr.Code, rr.Header().Get("Allow"))
	}
	// No token.
	if rr := f.post(t, "", beat(nil)); rr.Code != http.StatusUnauthorized {
		t.Errorf("no token: %d, want 401", rr.Code)
	}
	// Cross-origin.
	raw, _ := json.Marshal(beat(nil))
	req = httptest.NewRequest(http.MethodPost, WorkersPath, bytes.NewReader(raw))
	req.Header.Set("Authorization", "Bearer worker-a")
	req.Header.Set("Content-Type", "application/json")
	req.Host = "127.0.0.1:39273"
	req.Header.Set("Origin", "https://evil.example.com")
	rr = httptest.NewRecorder()
	OriginGuard(f.h).ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("cross-origin: %d, want 403", rr.Code)
	}
}

func TestWorkers_NoWriteAccessIs404AndStoresNothing(t *testing.T) {
	t.Parallel()
	f := newWorkerFixture(t)
	if rr := f.post(t, "reader", beat(nil)); rr.Code != http.StatusNotFound {
		t.Errorf("read-only token: %d, want 404", rr.Code)
	}
	if rr := f.post(t, "worker-a", beat(map[string]any{"kbs": []string{"kb-b"}})); rr.Code != http.StatusNotFound {
		t.Errorf("KB outside the grant: %d, want 404", rr.Code)
	}
	if rr := f.post(t, "worker-a", beat(map[string]any{"kbs": []string{"nope"}})); rr.Code != http.StatusNotFound {
		t.Errorf("unknown KB: %d, want 404", rr.Code)
	}
	if len(f.multi.workers.live("kb-a"))+len(f.multi.workers.live("kb-b")) != 0 {
		t.Fatal("something was stored")
	}
}

func TestWorkers_EvictionBounds(t *testing.T) {
	t.Parallel()
	f := newWorkerFixture(t)
	for i := 0; i < maxWorkersPerPrincipal+4; i++ {
		f.clock.advance(time.Second)
		f.post(t, "worker-a", beat(map[string]any{"worker": "w-" + string(rune('a'+i))}))
	}
	live := f.multi.workers.live("kb-a")
	if len(live) != maxWorkersPerPrincipal {
		t.Fatalf("per-principal bound: %d live", len(live))
	}
	for _, d := range live {
		if d.Worker == "w-a" {
			t.Fatal("the oldest was not the one evicted")
		}
	}
	var s workerStore
	s.now = f.clock.now
	for i := 0; i < maxWorkersTotal+10; i++ {
		f.clock.advance(time.Millisecond)
		s.put(workerDecl{Principal: "p" + string(rune('A'+i%26)) + string(rune('a'+i/26)), Worker: "w", KBs: []string{"kb-a"}, IntervalSeconds: 60, ReceivedAt: f.clock.now()})
	}
	if n := s.totalLocked(); n != maxWorkersTotal {
		t.Fatalf("total bound: %d", n)
	}
}

func TestWorkers_AuthOffIsActivityUnknown(t *testing.T) {
	t.Parallel()
	multi := NewMultiKBServer("test")
	k := uiFixtureKB(t, "kb-a")
	multi.MountKB("kb-a", func(s *Server) { RegisterKBTools(s, k, Deps{}) })
	raw, _ := json.Marshal(beat(nil))
	req := httptest.NewRequest(http.MethodPost, WorkersPath, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	auth.NewTokenStore(nil).Middleware(multi.Handler()).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	var ans map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &ans)
	if ans["activity_known"] != false {
		t.Fatalf("activity_known = %v, want false", ans["activity_known"])
	}
}

// The idle signal (WP3): which calls move last_activity_at.
func TestActivity_WhichCallsCount(t *testing.T) {
	t.Parallel()
	f := newWorkerFixture(t)
	srv := f.multi.servers["kb-a"]
	if _, ok := srv.lastActivity(); ok {
		t.Fatal("activity before any call")
	}

	// A worker with a live declaration does not move it.
	f.post(t, "worker-a", beat(nil))
	f.callAs("worker-a", "kb_status")
	if _, ok := srv.lastActivity(); ok {
		t.Fatal("a live worker's call counted")
	}

	// sync_pull is not activity, even by a person.
	f.callAs("person", "sync_pull")
	if _, ok := srv.lastActivity(); ok {
		t.Fatal("sync_pull counted")
	}

	// A denied call is not activity.
	ctx := auth.ContextWithPrincipal(context.Background(), auth.Principal{ID: "reader", Policy: auth.Policy{Permissions: []auth.Permission{{KB: "kb-a"}}}})
	srv.callTool(ctx, "map_create", json.RawMessage(`{"name":"x"}`))
	if _, ok := srv.lastActivity(); ok {
		t.Fatal("a denied call counted")
	}

	// kb_status by a person does, and the heartbeat reports it.
	f.clock.advance(time.Minute)
	f.callAs("person", "kb_status")
	at, ok := srv.lastActivity()
	if !ok || !at.Equal(f.clock.now()) {
		t.Fatalf("activity = %v %v", at, ok)
	}
	f.clock.advance(90 * time.Second)
	rr := f.post(t, "worker-a", beat(nil))
	var ans struct {
		Kbs map[string]struct {
			At   string `json:"last_activity_at"`
			Idle int    `json:"idle_seconds"`
		} `json:"kbs"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &ans)
	if ans.Kbs["kb-a"].Idle != 90 || ans.Kbs["kb-a"].At == "" {
		t.Fatalf("answer = %s", rr.Body.String())
	}

	// A stale declaration stops excluding: the worker counts as a person.
	f.clock.advance(10 * time.Minute)
	f.callAs("worker-a", "kb_status")
	if got, _ := srv.lastActivity(); !got.Equal(f.clock.now()) {
		t.Fatal("a stale worker's call did not count")
	}

	// The other KB is untouched.
	if _, ok := f.multi.servers["kb-b"].lastActivity(); ok {
		t.Fatal("kb-b moved")
	}
}
