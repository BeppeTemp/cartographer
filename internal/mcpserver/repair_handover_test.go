package mcpserver

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/gitx/gitxtest"
)

func repairBeat(id string, over map[string]any) map[string]any {
	b := beat(map[string]any{"surveyor": id, "tasks": []map[string]any{{"kind": "repair", "interval_seconds": 86400}}})
	for k, v := range over {
		b[k] = v
	}
	return b
}

func (f *surveyorFixture) beatOK(t *testing.T, token string, b map[string]any) {
	t.Helper()
	if rr := f.post(t, token, b); rr.Code != http.StatusOK {
		t.Fatalf("beat: %d %s", rr.Code, rr.Body.String())
	}
}

func principalCtx(id string, kbName string, write bool) context.Context {
	return auth.ContextWithPrincipal(context.Background(), auth.Principal{ID: id, Policy: auth.Policy{Permissions: []auth.Permission{{KB: kbName, Write: write}}}})
}

// A declaration covers a KB only with all of: live, accepted for the KB, the
// repair task, and a successful auto repair the server saw.
func TestRepairDelegatesNeedsDeclarationLivenessAndObservation(t *testing.T) {
	t.Parallel()
	f := newSurveyorFixture(t)
	a := f.multi.servers["kb-a"]
	if got := f.multi.RepairDelegates("kb-a"); len(got) != 0 {
		t.Fatalf("nothing declared: %v", got)
	}
	// Declaration alone: the timer keeps running.
	f.beatOK(t, "surveyor-a", repairBeat("docs-night", nil))
	if got := f.multi.RepairDelegates("kb-a"); len(got) != 0 {
		t.Fatalf("declaration without an observed repair covers: %v", got)
	}
	a.noteSurveyorRepair("surveyor-a")
	if got := f.multi.RepairDelegates("kb-a"); len(got) != 1 || got[0] != "docs-night" {
		t.Fatalf("covered = %v", got)
	}
	// The task list is what declares it: a Surveyor without repair covers nothing.
	f.beatOK(t, "surveyor-a", repairBeat("docs-night", map[string]any{"tasks": []map[string]any{{"kind": "doctor"}}}))
	if got := f.multi.RepairDelegates("kb-a"); len(got) != 0 {
		t.Fatalf("no repair task but covered: %v", got)
	}
	// An unknown KB has no delegates.
	if got := f.multi.RepairDelegates("nope"); len(got) != 0 {
		t.Fatalf("unknown KB: %v", got)
	}
}

func TestRepairDelegationEnds(t *testing.T) {
	t.Parallel()
	f := newSurveyorFixture(t)
	a := f.multi.servers["kb-a"]
	f.beatOK(t, "surveyor-a", repairBeat("one", nil))
	f.beatOK(t, "surveyor-ab", repairBeat("two", nil))
	a.noteSurveyorRepair("surveyor-a")
	a.noteSurveyorRepair("surveyor-ab")
	if got := f.multi.RepairDelegates("kb-a"); len(got) != 2 {
		t.Fatalf("both cover: %v", got)
	}
	// Silent for three intervals (60s default): "one" dies, "two" keeps beating.
	f.clock.advance(2 * time.Minute)
	f.beatOK(t, "surveyor-ab", repairBeat("two", nil))
	f.clock.advance(61 * time.Second)
	f.beatOK(t, "surveyor-ab", repairBeat("two", nil))
	if got := f.multi.RepairDelegates("kb-a"); len(got) != 1 || got[0] != "two" {
		t.Fatalf("one died, two still covers: %v", got)
	}
	// A clean stop removes the declaration at once.
	f.beatOK(t, "surveyor-ab", repairBeat("two", map[string]any{"state": "stopped"}))
	if got := f.multi.RepairDelegates("kb-a"); len(got) != 0 {
		t.Fatalf("stopped still covers: %v", got)
	}
}

// The window is the timer's period plus a slack, whatever the declaration says:
// a writer that calls auto once and keeps beating (with a huge declared
// interval) loses the handover after one timer period, and one that repeats the
// call keeps it (the accepted residual: it is a writer and could commit anyway).
func TestRepairDelegationNeedsRecentRepair(t *testing.T) {
	t.Parallel()
	f := newSurveyorFixture(t)
	a := f.multi.servers["kb-a"]
	b := func() { f.beatOK(t, "surveyor-a", repairBeat("one", map[string]any{"interval_seconds": 3600})) }
	b()
	a.noteSurveyorRepair("surveyor-a")
	step := func(d time.Duration) {
		for e := time.Duration(0); e < d; e += 30 * time.Minute {
			f.clock.advance(30 * time.Minute)
			b()
		}
	}
	step(autoRepairInterval)
	if got := f.multi.RepairDelegates("kb-a"); len(got) != 1 {
		t.Fatalf("within the period: %v", got)
	}
	step(2 * time.Hour)
	if got := f.multi.RepairDelegates("kb-a"); len(got) != 0 {
		t.Fatalf("beating alone kept the handover past the period: %v", got)
	}
	a.noteSurveyorRepair("surveyor-a")
	if got := f.multi.RepairDelegates("kb-a"); len(got) != 1 {
		t.Fatalf("a repeated call must hold it: %v", got)
	}
}

// A Surveyor that lists repair but can write only kb-a does not cover kb-b,
// even when the server saw it repair there.
func TestRepairDelegationFollowsTheKBFilter(t *testing.T) {
	t.Parallel()
	f := newSurveyorFixture(t)
	f.beatOK(t, "surveyor-a", repairBeat("one", map[string]any{"kbs": []string{"kb-a", "kb-b"}}))
	f.multi.servers["kb-b"].noteSurveyorRepair("surveyor-a")
	if got := f.multi.RepairDelegates("kb-b"); len(got) != 0 {
		t.Fatalf("kb-b covered without write access: %v", got)
	}
}

// The observation is the server's own record of an authenticated, successful
// call: a read-only token is refused and records nothing, a writer records.
func TestKBRepairAutoRecordsOnlyASuccessfulWriter(t *testing.T) {
	t.Parallel()
	f := newSurveyorFixture(t)
	a := f.multi.servers["kb-a"]
	args := []byte(`{"check":"auto","dry_run":false}`)
	if res := a.callTool(principalCtx("reader", "kb-a", false), "kb_repair", args); !res.IsError {
		t.Fatalf("read-only token ran a repair: %+v", res)
	}
	if _, ok := a.surveyorRepairAt("reader"); ok {
		t.Fatal("a refused call was recorded")
	}
	if res := a.callTool(principalCtx("surveyor-a", "kb-a", true), "kb_repair", []byte(`{"check":"auto"}`)); !res.IsError {
		t.Fatal("auto without dry_run false must be refused")
	}
	if _, ok := a.surveyorRepairAt("surveyor-a"); ok {
		t.Fatal("a refusal was recorded")
	}
	if res := a.callTool(principalCtx("surveyor-a", "kb-a", true), "kb_repair", args); res.IsError {
		t.Fatalf("writer refused: %s", res.Content[0].Text)
	}
	if at, ok := a.surveyorRepairAt("surveyor-a"); !ok || !at.Equal(f.clock.now()) {
		t.Fatalf("not recorded: %v %v", at, ok)
	}
	// Any other kb_repair check records nothing.
	if res := a.callTool(principalCtx("person", "kb-a", true), "kb_repair", []byte(`{"check":"*"}`)); res.IsError {
		t.Fatalf("preview: %s", res.Content[0].Text)
	}
	if _, ok := a.surveyorRepairAt("person"); ok {
		t.Fatal("a preview was recorded")
	}
}

func TestSurveyorRepairObservationsAreBounded(t *testing.T) {
	t.Parallel()
	f := newSurveyorFixture(t)
	a := f.multi.servers["kb-a"]
	for i := 0; i < 3*maxSurveyorsTotal; i++ {
		f.clock.advance(time.Second)
		a.noteSurveyorRepair("p" + strings.Repeat("x", i%7) + string(rune('a'+i%26)) + time.Duration(i).String())
	}
	if n := len(a.surveyorRepairs); n > maxSurveyorsTotal {
		t.Fatalf("%d observations kept", n)
	}
}

// The loop skips a run while a surveyor owns the KB, re-checks quickly, and
// runs as soon as the answer turns empty.
func TestAutoRepairLoopPausesWhileDelegated(t *testing.T) {
	t.Parallel()
	k, s := autoRepairKB(t)
	var delegated atomic.Bool
	delegated.Store(true)
	s.SetRepairDelegation(func() []string {
		if delegated.Load() {
			return []string{"docs-night"}
		}
		return nil
	})
	s.autoRepairRecheckFor = 20 * time.Millisecond
	before := commitCount(t, k)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.autoRepairLoop(ctx, time.Hour, 5*time.Millisecond); close(done) }()
	time.Sleep(300 * time.Millisecond)
	if commitCount(t, k) != before {
		t.Fatal("the server repaired a KB a surveyor owns")
	}
	if _, ok := lastAutoRepairRun(k); ok {
		t.Fatal("a delegated tick wrote a run log line")
	}
	delegated.Store(false)
	deadline := time.Now().Add(gitxtest.Deadline(10 * time.Second))
	for {
		if run, ok := lastAutoRepairRun(k); ok && len(run.Checks) == 1 && run.Checks[0].Applied == 3 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the server never resumed")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
}

// The Atlas summary names the surveyors that own the repair.
func TestMaintenanceSummaryShowsTheDelegate(t *testing.T) {
	t.Parallel()
	f := newSurveyorFixture(t)
	a := f.multi.servers["kb-a"]
	a.SetRepairDelegation(func() []string { return f.multi.RepairDelegates("kb-a") })
	f.multi.EnableWeb(nil)
	get := func() map[string]any {
		rr := getUI(t, f.h, UIAPIPrefix+"/kbs/kb-a/maintenance/summary", "person")
		if rr.Code != http.StatusOK {
			t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
		}
		return decodeUI(t, rr)["auto_repair"].(map[string]any)
	}
	if d := get()["delegated_to"].([]any); len(d) != 0 {
		t.Fatalf("delegated_to = %v", d)
	}
	f.beatOK(t, "surveyor-a", repairBeat("docs-night", nil))
	a.noteSurveyorRepair("surveyor-a")
	if d := get()["delegated_to"].([]any); len(d) != 1 || d[0] != "docs-night" {
		t.Fatalf("delegated_to = %v", d)
	}
}
