package worker

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/workerconfig"
)

type stubAPI struct {
	mu       sync.Mutex
	beats    []map[string]any
	posts    int
	answer   *client.HeartbeatAnswer
	beatErr  error
	postErr  error
	settings any
}

func (s *stubAPI) Heartbeat(report any, _ time.Duration) (*client.HeartbeatAnswer, error) {
	raw, _ := json.Marshal(report)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.beats = append(s.beats, m)
	if s.beatErr != nil {
		return nil, s.beatErr
	}
	a := *s.answer
	return &a, nil
}

func (s *stubAPI) ReportWorkerSettings(report any, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.posts++
	s.settings = report
	return s.postErr
}

func (s *stubAPI) counts() (int, int) { s.mu.Lock(); defer s.mu.Unlock(); return len(s.beats), s.posts }

func newBeater(api *stubAPI, now *time.Time) *Beater {
	return &Beater{API: api, Worker: "docs-night-ab12cd", Perimeter: "docs-night", Version: "1.0", KBs: []string{"kb-a"},
		Tiers: map[string]string{"small-a": "small", "big-a": "large"}, Settings: &workerconfig.Report{Perimeter: "docs-night"},
		Now: func() time.Time { return *now }}
}

func TestBeaterBodyShapeAndOneBudgetRowPerProfile(t *testing.T) {
	now := time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC)
	api := &stubAPI{answer: &client.HeartbeatAnswer{}}
	b := newBeater(api, &now)
	b.Beat(context.Background(), State{Run: StateWaiting, NextWindowStart: now.Add(time.Hour), LastRun: LastRun{At: now.Add(-time.Hour), Units: 3, Commits: 2},
		Budgets: []ProfileBudget{{Profile: "small-a", SpentToday: 10, Daily: 100}, {Profile: "big-a", SpentToday: 1, Daily: 50}}})
	got := api.beats[0]
	if got["worker"] != "docs-night-ab12cd" || got["perimeter"] != "docs-night" || got["state"] != "waiting" || got["interval_seconds"] != float64(60) {
		t.Fatalf("body = %v", got)
	}
	rows := got["budgets"].([]any)
	if len(rows) != 2 || rows[0].(map[string]any)["tier"] != "small" || rows[1].(map[string]any)["tier"] != "large" || rows[0].(map[string]any)["limit"] != float64(100) {
		t.Fatalf("budgets = %v", rows)
	}
	if lr := got["last_run"].(map[string]any); lr["units_done"] != float64(3) || lr["commits"] != float64(2) {
		t.Fatalf("last_run = %v", lr)
	}
	if got["next_window_at"] == nil {
		t.Fatal("next_window_at missing")
	}
}

func TestBeaterStoppedIsSentAndCarriesNoSettings(t *testing.T) {
	now := time.Now()
	api := &stubAPI{answer: &client.HeartbeatAnswer{}}
	b := newBeater(api, &now)
	b.Beat(context.Background(), State{Run: StateWaiting})
	b.Beat(context.Background(), State{Run: StateWaiting}) // kept, not sent: the goroutine sends
	b.Beat(context.Background(), State{Run: StateStopped})
	n, posts := api.counts()
	if n != 2 || api.beats[1]["state"] != "stopped" {
		t.Fatalf("beats = %d %v, want first and stopped", n, api.beats)
	}
	if posts != 1 {
		t.Fatalf("settings posted %d times, want once (at start)", posts)
	}
}

func TestBeaterSettingsWantedPostsAgain(t *testing.T) {
	now := time.Now()
	api := &stubAPI{answer: &client.HeartbeatAnswer{}}
	b := newBeater(api, &now)
	b.Beat(context.Background(), State{Run: StateIdle})
	api.mu.Lock()
	api.answer.SettingsWanted = true
	api.mu.Unlock()
	b.send()
	if _, posts := api.counts(); posts != 2 {
		t.Fatalf("posts = %d, want 2 (start and settings_wanted)", posts)
	}
	api.mu.Lock()
	api.answer.SettingsWanted = false
	api.mu.Unlock()
	b.send()
	if _, posts := api.counts(); posts != 2 {
		t.Fatalf("posts = %d, want still 2", posts)
	}
}

func TestBeaterFailuresAreIgnored(t *testing.T) {
	now := time.Now()
	api := &stubAPI{answer: &client.HeartbeatAnswer{}, beatErr: &client.RemoteError{HTTPStatus: 404, Code: client.CodeHTTPFailed}}
	var logs []string
	b := newBeater(api, &now)
	b.Logf = func(f string, a ...any) { logs = append(logs, f) }
	b.Beat(context.Background(), State{Run: StateIdle}) // must not panic or block
	if d, known, err := b.IdleFor(context.Background(), "kb-a"); known || err != nil || d != 0 {
		t.Fatalf("idle after a 404 = %v %v %v, want unknown", d, known, err)
	}
	if _, posts := api.counts(); posts != 0 || len(logs) == 0 {
		t.Fatalf("posts %d logs %v: a failed beat posts no settings and is logged", posts, logs)
	}
}

func TestBeaterIdleFromTheAnswer(t *testing.T) {
	now := time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC)
	last := now.Add(-10 * time.Minute)
	api := &stubAPI{answer: &client.HeartbeatAnswer{ActivityKnown: true, Now: now, ServerStartedAt: now.Add(-time.Hour)}}
	api.answer.KBs = map[string]struct {
		LastActivityAt *time.Time `json:"last_activity_at"`
	}{"kb-a": {LastActivityAt: &last}, "kb-n": {}}
	b := newBeater(api, &now)
	b.Beat(context.Background(), State{Run: StateWaiting})
	ctx := context.Background()
	if d, known, _ := b.IdleFor(ctx, "kb-a"); !known || d != 10*time.Minute {
		t.Fatalf("idle = %v %v, want 10m", d, known)
	}
	now = now.Add(2 * time.Minute) // local time since the answer is added
	if d, _, _ := b.IdleFor(ctx, "kb-a"); d != 12*time.Minute {
		t.Fatalf("idle = %v, want 12m", d)
	}
	if d, known, _ := b.IdleFor(ctx, "kb-n"); !known || d != 62*time.Minute {
		t.Fatalf("null activity = %v %v, want idle since the server started (62m)", d, known)
	}
	if _, known, _ := b.IdleFor(ctx, "kb-x"); known {
		t.Fatal("a KB the answer omits must be unknown")
	}
	now = now.Add(4 * time.Minute) // 6m old... then past three intervals
	now = now.Add(3 * time.Minute)
	if _, known, _ := b.IdleFor(ctx, "kb-a"); known {
		t.Fatal("an answer older than three intervals must be unknown")
	}
	api.answer.ActivityKnown = false
	b.send()
	if _, known, _ := b.IdleFor(ctx, "kb-a"); known {
		t.Fatal("activity_known false must be unknown")
	}
}

func TestBeaterNeverLogsAToken(t *testing.T) {
	now := time.Now()
	api := &stubAPI{answer: &client.HeartbeatAnswer{}}
	b := newBeater(api, &now)
	b.Beat(context.Background(), State{Run: StateIdle})
	raw, _ := json.Marshal(api.beats)
	if strings.Contains(strings.ToLower(string(raw)), "token") {
		t.Fatalf("beat carries a token field: %s", raw)
	}
}
