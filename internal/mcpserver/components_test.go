package mcpserver

import (
	"bytes"
	"context"
	"log"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/componentproto"
	"github.com/BeppeTemp/cartographer/internal/config"
)

func compFixture(t *testing.T, specs ...config.ComponentSpec) *surveyorFixture {
	t.Helper()
	f := newSurveyorFixture(t)
	f.multi.components.now = f.clock.now
	f.multi.SetComponents(specs)
	return f
}

func spec(name string, min int, kbs ...string) config.ComponentSpec {
	return config.ComponentSpec{Kind: componentproto.KindSurveyor, Name: name, KBs: kbs, MinReplicas: min}
}

func (f *surveyorFixture) rows(t *testing.T, kb string) map[string]ComponentRow {
	t.Helper()
	rows, ok := f.multi.ComponentStatus(adminCtx, kb)
	if !ok {
		t.Fatalf("%s not visible", kb)
	}
	out := map[string]ComponentRow{}
	for _, r := range rows {
		out[r.Name] = r
	}
	return out
}

func (f *surveyorFixture) compBeat(t *testing.T, token string, over map[string]any) {
	t.Helper()
	if rr := f.post(t, token, beat(over)); rr.Code != http.StatusOK {
		t.Fatalf("beat: %d %s", rr.Code, rr.Body.String())
	}
}

func TestComponents_StatesAndGrace(t *testing.T) {
	f := compFixture(t, spec("team-a", 1, "kb-a"))
	if got := f.rows(t, "kb-a")["team-a"]; got.State != componentproto.StateStarting || got.LastSeenAt != nil || got.Live != 0 {
		t.Fatalf("at start: %+v", got)
	}
	f.clock.advance(componentGrace + time.Second)
	if got := f.rows(t, "kb-a")["team-a"]; got.State != componentproto.StateSilent || got.LastSeenAt != nil {
		t.Fatalf("after grace, never seen: %+v", got)
	}
	f.compBeat(t, "surveyor-a", nil)
	got := f.rows(t, "kb-a")["team-a"]
	if got.State != componentproto.StateUp || got.Live != 1 || got.LastSeenAt == nil {
		t.Fatalf("after a beat: %+v", got)
	}
	seen := *got.LastSeenAt
	// The beat goes stale (3 intervals of 60 s): silent again, last_seen kept.
	f.clock.advance(4 * time.Minute)
	got = f.rows(t, "kb-a")["team-a"]
	if got.State != componentproto.StateSilent || got.LastSeenAt == nil || !got.LastSeenAt.Equal(seen) {
		t.Fatalf("after silence: %+v", got)
	}
}

func TestComponents_BeatInsideGraceThenSilentIsNotStarting(t *testing.T) {
	f := compFixture(t, spec("team-a", 1, "kb-a"))
	f.compBeat(t, "surveyor-a", nil)
	f.clock.advance(4 * time.Minute) // past the beat, still inside no grace: grace is 3 min
	if got := f.rows(t, "kb-a")["team-a"]; got.State != componentproto.StateSilent {
		t.Fatalf("seen once then silent must be silent: %+v", got)
	}
}

func TestComponents_MinReplicasAndTwoPrincipals(t *testing.T) {
	f := compFixture(t, spec("team-a", 2, "kb-a"))
	f.compBeat(t, "surveyor-a", map[string]any{"surveyor": "s-1"})
	if got := f.rows(t, "kb-a")["team-a"]; got.State != componentproto.StateDegraded || got.Live != 1 {
		t.Fatalf("one of two: %+v", got)
	}
	// A second Surveyor of another principal on the same name.
	f.compBeat(t, "surveyor-ab", map[string]any{"surveyor": "s-1"})
	if got := f.rows(t, "kb-a")["team-a"]; got.State != componentproto.StateUp || got.Live != 2 {
		t.Fatalf("two principals: %+v", got)
	}
}

func TestComponents_Undeclared(t *testing.T) {
	f := compFixture(t, spec("team-a", 1, "kb-a"))
	f.compBeat(t, "surveyor-a", map[string]any{"perimeter": "other"})
	rows := f.rows(t, "kb-a")
	if rows["other"].State != componentproto.StateUndeclared || rows["other"].Expected || rows["other"].Live != 1 {
		t.Fatalf("undeclared: %+v", rows["other"])
	}
	if _, ok := f.multi.components.lastSeen[componentKey{kind: "surveyor", name: "other", kb: "kb-a"}]; ok {
		t.Fatal("an undeclared name must not be remembered")
	}
	// An expected entry on another KB gives no row here.
	f2 := compFixture(t, spec("team-b", 1, "kb-b"))
	if _, ok := f2.rows(t, "kb-a")["team-b"]; ok {
		t.Fatal("a component of kb-b has no row on kb-a")
	}
}

func TestComponents_EmptyRegistryIsToday(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(testLogSink{}) })
	f := compFixture(t)
	f.compBeat(t, "surveyor-a", nil)
	f.clock.advance(time.Hour)
	if rows := f.rows(t, "kb-a"); len(rows) != 0 {
		t.Fatalf("empty registry gave rows: %+v", rows)
	}
	if strings.Contains(buf.String(), "component ") {
		t.Fatalf("empty registry logged: %s", buf.String())
	}
}

type testLogSink struct{}

func (testLogSink) Write(p []byte) (int, error) { return len(p), nil }

func TestComponents_RestartIsStartingAgain(t *testing.T) {
	f := compFixture(t, spec("team-a", 1, "kb-a"))
	f.clock.advance(time.Hour)
	if got := f.rows(t, "kb-a")["team-a"]; got.State != componentproto.StateSilent {
		t.Fatalf("%+v", got)
	}
	f.multi.SetComponents([]config.ComponentSpec{spec("team-a", 1, "kb-a")}) // a new process
	if got := f.rows(t, "kb-a")["team-a"]; got.State != componentproto.StateStarting {
		t.Fatalf("restart: %+v", got)
	}
}

func TestComponents_NoOracleForAKBNotSeenWhole(t *testing.T) {
	f := compFixture(t, spec("team-a", 1, "kb-a"))
	// "reader" reads only kb-a, a restricted view: not the whole KB for a
	// write-scoped caller check is WholeVisible's; an unmounted KB is the same
	// answer as a hidden one.
	if _, ok := f.multi.ComponentStatus(adminCtx, "nope"); ok {
		t.Fatal("an unmounted KB must not be visible")
	}
	ctx := auth.ContextWithPrincipal(context.Background(), auth.Principal{ID: "x", Policy: auth.Policy{Permissions: []auth.Permission{{KB: "kb-b"}}}})
	if rows, ok := f.multi.ComponentStatus(ctx, "kb-a"); ok || rows != nil {
		t.Fatalf("a caller without kb-a got %v %v", rows, ok)
	}
}

func TestComponents_TransitionsLoggedOnce(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(testLogSink{}) })
	f := compFixture(t, spec("team-a", 1, "kb-a"))
	f.clock.advance(componentGrace + time.Second)
	f.rows(t, "kb-a")
	f.rows(t, "kb-a")
	if n := strings.Count(buf.String(), "is silent"); n != 1 {
		t.Fatalf("silent logged %d times: %s", n, buf.String())
	}
	f.compBeat(t, "surveyor-a", nil)
	f.rows(t, "kb-a")
	f.rows(t, "kb-a")
	if n := strings.Count(buf.String(), "recovered"); n != 1 {
		t.Fatalf("recovery logged %d times: %s", n, buf.String())
	}
}

// The perimeter pattern of the heartbeat and the registry's name pattern are
// one rule written twice (config cannot import mcpserver).
func TestComponents_NamePatternMatchesPerimeter(t *testing.T) {
	if config.ComponentNamePattern() != perimeterRe.String() {
		t.Fatalf("config %s != perimeter %s", config.ComponentNamePattern(), perimeterRe)
	}
	if config.MaxComponentReplicas != maxSurveyorsPerPrincipal {
		t.Fatal("min_replicas cap must be the per-principal cap")
	}
}

// A beat counts only for the KBs its principal holds: "surveyor-a" writes kb-a
// only, so a beat naming the expected name and kb-b says nothing about kb-b, and
// a beat for kb-b by a principal of kb-b does not mark kb-a seen.
func TestComponents_BeatCountsOnlyForAcceptedKBs(t *testing.T) {
	f := compFixture(t, spec("team-a", 1, "kb-a", "kb-b"))
	f.clock.advance(componentGrace + time.Second)
	f.compBeat(t, "surveyor-a", map[string]any{"kbs": []string{"kb-a", "kb-b"}})
	a, b := f.rows(t, "kb-a")["team-a"], f.rows(t, "kb-b")["team-a"]
	if a.State != componentproto.StateUp || a.LastSeenAt == nil {
		t.Fatalf("kb-a: %+v", a)
	}
	if b.State != componentproto.StateSilent || b.LastSeenAt != nil || b.Live != 0 {
		t.Fatalf("kb-b must not be vouched for by a principal that cannot write it: %+v", b)
	}
	// A KB the entry does not name is not remembered either.
	f2 := compFixture(t, spec("team-a", 1, "kb-a"))
	f2.compBeat(t, "surveyor-ab", map[string]any{"kbs": []string{"kb-b"}})
	if got := f2.rows(t, "kb-a")["team-a"]; got.LastSeenAt != nil || got.Live != 0 {
		t.Fatalf("a beat for kb-b marked kb-a seen: %+v", got)
	}
}
