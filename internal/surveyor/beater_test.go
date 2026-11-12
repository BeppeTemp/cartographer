package surveyor

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/componentproto"
	"github.com/BeppeTemp/cartographer/internal/surveyor/recipe"
	"github.com/BeppeTemp/cartographer/internal/surveyorconfig"
)

type stubAPI struct {
	mu       sync.Mutex
	beats    []map[string]any
	posts    int
	answer   *client.HeartbeatAnswer
	beatErr  error
	postErr  error
	settings any
	runPosts []map[string]any
	runsErr  error
	onRuns   func()
}

func (s *stubAPI) ReportRuns(report any, _ time.Duration) (*client.RunsAnswer, error) {
	raw, _ := json.Marshal(report)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.onRuns != nil {
		s.onRuns()
	}
	if s.runsErr != nil {
		return nil, s.runsErr
	}
	s.runPosts = append(s.runPosts, m)
	return &client.RunsAnswer{Received: len(m["runs"].([]any))}, nil
}

func (s *stubAPI) runCounts() (out []int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.runPosts {
		out = append(out, len(p["runs"].([]any)))
	}
	return out
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

func (s *stubAPI) ReportSurveyorSettings(report any, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.posts++
	s.settings = report
	return s.postErr
}

func (s *stubAPI) counts() (int, int) { s.mu.Lock(); defer s.mu.Unlock(); return len(s.beats), s.posts }

func newBeater(api *stubAPI, now *time.Time) *Beater {
	return &Beater{API: api, Surveyor: "docs-night-ab12cd", Perimeter: "docs-night", Version: "1.0", KBs: []string{"kb-a"},
		Tiers: map[string]string{"small-a": "small", "big-a": "large"}, Settings: &surveyorconfig.Report{Perimeter: "docs-night"},
		Now: func() time.Time { return *now }}
}

func TestBeaterBodyShapeAndOneBudgetRowPerProfile(t *testing.T) {
	now := time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC)
	api := &stubAPI{answer: &client.HeartbeatAnswer{}}
	b := newBeater(api, &now)
	b.Beat(context.Background(), State{Run: StateWaiting, NextWindowStart: now.Add(time.Hour), LastRun: LastRun{At: now.Add(-time.Hour), Units: 3, Commits: 2},
		Budgets: []ProfileBudget{{Profile: "small-a", SpentToday: 10, Daily: 100}, {Profile: "big-a", SpentToday: 1, Daily: 50}}})
	got := api.beats[0]
	if got["surveyor"] != "docs-night-ab12cd" || got["perimeter"] != "docs-night" || got["state"] != "waiting" || got["interval_seconds"] != float64(60) {
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

// A 4xx on the settings report (an older server, a protocol skew, a restart,
// no writable KB) may pass: the post backs off for runsBackoffBeats beats, is
// tried again, and one 200 puts the report where it belongs. The refusal is
// logged once per status.
func TestBeaterRefusedSettingsBackOffThenRepost(t *testing.T) {
	now := time.Now()
	api := &stubAPI{answer: &client.HeartbeatAnswer{SettingsWanted: true}, postErr: &client.RemoteError{HTTPStatus: 404, Code: client.CodeHTTPFailed}}
	var logs []string
	b := newBeater(api, &now)
	b.Logf = func(f string, a ...any) { logs = append(logs, fmt.Sprintf(f, a...)) }
	b.Beat(context.Background(), State{Run: StateIdle}) // refused
	for i := 0; i < runsBackoffBeats; i++ {
		b.send()
	}
	if _, posts := api.counts(); posts != 1 {
		t.Fatalf("posts = %d during the back-off, want 1", posts)
	}
	b.send() // back-off over: refused again, same status, not logged again
	if _, posts := api.counts(); posts != 2 {
		t.Fatalf("posts = %d, want a retry after the back-off", posts)
	}
	api.mu.Lock()
	api.postErr = nil // the server was upgraded or restarted
	api.mu.Unlock()
	for i := 0; i < runsBackoffBeats; i++ {
		b.send()
	}
	b.send() // accepted
	if _, posts := api.counts(); posts != 3 {
		t.Fatalf("posts = %d, want the report reposted once the route answers", posts)
	}
	n := 0
	for _, l := range logs {
		if strings.Contains(l, "settings report refused") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("refusal logged %d times, want once: %v", n, logs)
	}
	// Accepted, and nothing wanted: no further post, and the back-off is clear.
	api.mu.Lock()
	api.answer = &client.HeartbeatAnswer{}
	api.mu.Unlock()
	b.send()
	if _, posts := api.counts(); posts != 3 {
		t.Fatalf("posts = %d after an accepted report, want still 3", posts)
	}
}

func TestBeaterSendsItsProtocol(t *testing.T) {
	now := time.Now()
	api := &stubAPI{answer: &client.HeartbeatAnswer{Protocol: 1, MinProtocol: 1}}
	b := newBeater(api, &now)
	b.Beat(context.Background(), State{Run: StateIdle})
	if api.beats[0]["protocol"] != float64(componentproto.Protocol) {
		t.Fatalf("body = %v", api.beats[0])
	}
}

// A 404 is an older server and a 426 a server that refuses this protocol: both
// leave the activity unknown, neither stops the beat, and the 426 line is
// logged once per distinct (protocol, min_protocol) pair.
func TestBeater404And426(t *testing.T) {
	now := time.Now()
	api := &stubAPI{answer: &client.HeartbeatAnswer{ActivityKnown: true, KBs: map[string]struct {
		LastActivityAt *time.Time `json:"last_activity_at"`
	}{"kb-a": {}}, ServerStartedAt: now, Now: now}}
	var logs []string
	b := newBeater(api, &now)
	b.Logf = func(f string, a ...any) { logs = append(logs, fmt.Sprintf(f, a...)) }
	b.Beat(context.Background(), State{Run: StateIdle})
	if _, known, _ := b.IdleFor(context.Background(), "kb-a"); !known {
		t.Fatal("idle unknown after a good answer")
	}
	api.mu.Lock()
	api.beatErr = &client.RemoteError{HTTPStatus: 404}
	api.mu.Unlock()
	b.send()
	if _, known, _ := b.IdleFor(context.Background(), "kb-a"); known {
		t.Fatal("idle known after a 404")
	}
	api.mu.Lock()
	api.beatErr = &client.RemoteError{HTTPStatus: 426, ServerProtocol: 4, ServerMinProtocol: 3}
	api.mu.Unlock()
	refusals := func() (n int) {
		for _, l := range logs {
			if strings.Contains(l, "refuses component protocol") {
				n++
			}
		}
		return n
	}
	b.send()
	b.send()
	if n := refusals(); n != 1 {
		t.Fatalf("426 logged %d lines for the same pair, want 1: %v", n, logs)
	}
	api.mu.Lock()
	api.beatErr = &client.RemoteError{HTTPStatus: 426, ServerProtocol: 5, ServerMinProtocol: 4}
	api.mu.Unlock()
	b.send()
	if refusals() != 2 {
		t.Fatal("a different pair must be logged again")
	}
	if n, _ := api.counts(); n < 5 {
		t.Fatalf("the beat stopped: %d", n)
	}
}

// An answer more than one protocol below ours is treated as an older server.
func TestBeaterAnswerFarBelowIsAnOlderServer(t *testing.T) {
	now := time.Now()
	api := &stubAPI{answer: &client.HeartbeatAnswer{ActivityKnown: true, Protocol: componentproto.Protocol - 5, ServerStartedAt: now, Now: now}}
	b := newBeater(api, &now)
	b.Beat(context.Background(), State{Run: StateIdle})
	if _, known, _ := b.IdleFor(context.Background(), "kb-a"); known {
		t.Fatal("idle known against a server two protocols below")
	}
}

// The heartbeat's task names are the closed set of componentproto.
func TestDefaultKindsAreTheComponentTaskKinds(t *testing.T) {
	var names []string
	for _, k := range DefaultKinds().list {
		names = append(names, k.Name)
	}
	sort.Strings(names)
	if !slices.Equal(names, componentproto.TaskKinds()) {
		t.Fatalf("kinds %v, componentproto %v", names, componentproto.TaskKinds())
	}
}

func finishedRun(i int, outcome string) Run {
	return Run{ID: fmt.Sprintf("r-%d", i), Task: "poll", KB: "kb-a", Started: time.Date(2026, 1, 10, 12, 0, i, 0, time.UTC),
		Duration: time.Second, Outcome: outcome, Tokens: 10}
}

func TestBeaterRunsAreBatchedAckedAndRefilled(t *testing.T) {
	now := time.Now()
	api := &stubAPI{answer: &client.HeartbeatAnswer{}}
	b := newBeater(api, &now)
	b.Beat(context.Background(), State{Run: StateIdle})
	if got := api.runCounts(); len(got) != 0 {
		t.Fatalf("nothing finished yet, posts %v", got)
	}
	for i := 0; i < 25; i++ {
		b.Finished(finishedRun(i, componentproto.OutcomeOK))
	}
	b.send()
	if got := api.runCounts(); len(got) != 1 || got[0] != 20 {
		t.Fatalf("posts %v, want one of the newest 20", got)
	}
	b.send() // acknowledged: nothing to send
	if got := api.runCounts(); len(got) != 1 {
		t.Fatalf("an acknowledged run was sent again: %v", got)
	}
	b.Finished(finishedRun(25, componentproto.OutcomeOK))
	b.send()
	if got := api.runCounts(); len(got) != 2 || got[1] != 1 {
		t.Fatalf("posts %v, want the one new run", got)
	}
	api.mu.Lock()
	api.answer.RunsWanted = true
	api.mu.Unlock()
	b.send()
	if got := api.runCounts(); len(got) != 3 || got[2] != 20 {
		t.Fatalf("posts %v, want the whole history after runs_wanted", got)
	}
}

func TestBeaterFailedRunsPostKeepsTheQueueAndDoesNotFailTheBeat(t *testing.T) {
	now := time.Now()
	api := &stubAPI{answer: &client.HeartbeatAnswer{}, runsErr: fmt.Errorf("boom")}
	b := newBeater(api, &now)
	b.Beat(context.Background(), State{Run: StateIdle})
	b.Finished(finishedRun(1, componentproto.OutcomeOK))
	b.send()
	api.mu.Lock()
	api.runsErr = nil
	api.mu.Unlock()
	b.send()
	if got := api.runCounts(); len(got) != 1 || got[0] != 1 {
		t.Fatalf("posts %v, want the run kept and sent once the route answers", got)
	}
}

func TestBeaterAlarmsOpenAndClearWithTheRunsOutcome(t *testing.T) {
	now := time.Now()
	api := &stubAPI{answer: &client.HeartbeatAnswer{}}
	b := newBeater(api, &now)
	b.Beat(context.Background(), State{Run: StateIdle})
	bad := finishedRun(1, componentproto.OutcomeFailed)
	bad.Alarm = componentproto.AlarmFeedFailing
	b.Finished(bad)
	b.send()
	if al := api.runPosts[0]["alarms"].([]any); len(al) != 1 || al[0].(map[string]any)["code"] != "feed_failing" || al[0].(map[string]any)["source"] != "poll" {
		t.Fatalf("alarms = %v", api.runPosts[0]["alarms"])
	}
	b.Finished(finishedRun(2, componentproto.OutcomeOK))
	b.send()
	if al := api.runPosts[1]["alarms"].([]any); len(al) != 0 {
		t.Fatalf("alarm not cleared by an ok run: %v", al)
	}
}

func TestBeaterBeatCarriesTokensSpent(t *testing.T) {
	now := time.Now()
	api := &stubAPI{answer: &client.HeartbeatAnswer{}}
	b := newBeater(api, &now)
	b.Beat(context.Background(), State{Run: StateIdle, LastRun: LastRun{At: now, Units: 1, Tokens: 456}})
	if lr := api.beats[0]["last_run"].(map[string]any); lr["tokens_spent"] != float64(456) {
		t.Fatalf("last_run = %v", lr)
	}
}

func TestBeaterRefusedRunsRouteBacksOffThenResumes(t *testing.T) {
	now := time.Now()
	api := &stubAPI{answer: &client.HeartbeatAnswer{}, runsErr: &client.RemoteError{HTTPStatus: 404}}
	b := newBeater(api, &now)
	var logs []string
	b.Logf = func(f string, a ...any) { logs = append(logs, fmt.Sprintf(f, a...)) }
	b.Beat(context.Background(), State{Run: StateIdle})
	b.Finished(finishedRun(1, componentproto.OutcomeOK))
	calls := 0
	api.mu.Lock()
	api.onRuns = func() { calls++ }
	api.mu.Unlock()
	b.send() // refused
	for i := 0; i < runsBackoffBeats; i++ {
		b.send()
	}
	if calls != 1 {
		t.Fatalf("runs route called %d times during the back-off, want 1", calls)
	}
	b.send() // back-off over: refused again, same status, not logged again
	if calls != 2 {
		t.Fatalf("calls = %d, want a retry after the back-off", calls)
	}
	api.mu.Lock()
	api.runsErr = nil
	api.mu.Unlock()
	for i := 0; i < runsBackoffBeats; i++ {
		b.send()
	}
	b.send() // success: the history resumes
	if got := api.runCounts(); len(got) != 1 || got[0] != 1 {
		t.Fatalf("posts %v, want the queued run sent once the route answers", got)
	}
	n := 0
	for _, l := range logs {
		if strings.Contains(l, "runs report") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("logged %d times, want once: %v", n, logs)
	}
}

// The sources change with the last check of a recipe: a new check posts the
// report again, the age of an old one does not, and each post carries the
// sources as they are then.
func TestBeaterPostsAgainWhenAChecksGenerationChanges(t *testing.T) {
	now := time.Now()
	api := &stubAPI{answer: &client.HeartbeatAnswer{}}
	b := newBeater(api, &now)
	cfg := &Config{Checks: NewCheckLog(), ExecRecipes: []recipe.ExecConfig{{Name: "wiki-lister"}}}
	b.Sources = func() ([]surveyorconfig.Source, uint64) {
		gen := cfg.Checks.Generation()
		return cfg.Sources(now), gen
	}
	b.Beat(context.Background(), State{Run: StateIdle})
	if _, posts := api.counts(); posts != 1 {
		t.Fatalf("posts = %d, want 1 (start)", posts)
	}
	now = now.Add(time.Hour) // an hour older, no new check
	b.send()
	if _, posts := api.counts(); posts != 1 {
		t.Fatalf("posts = %d: the age of a check is no change", posts)
	}
	cfg.Checks.Record("wiki-lister", nil, now)
	b.send()
	if _, posts := api.counts(); posts != 2 {
		t.Fatalf("posts = %d, want 2 (a check ran)", posts)
	}
	b.send()
	if _, posts := api.counts(); posts != 2 {
		t.Fatalf("posts = %d, want still 2", posts)
	}
	api.mu.Lock()
	rep := api.settings.(*surveyorconfig.Report)
	api.mu.Unlock()
	var got string
	for _, s := range rep.Sources {
		if s.Name == "wiki-lister" {
			got = s.Check.Outcome
		}
	}
	if got != surveyorconfig.CheckOK {
		t.Errorf("the second report carries %q for wiki-lister, want ok: %+v", got, rep.Sources)
	}
	if b.Settings.Sources != nil && len(b.Settings.Sources) != 0 {
		t.Error("the beater wrote into the caller's report instead of a copy")
	}
}
