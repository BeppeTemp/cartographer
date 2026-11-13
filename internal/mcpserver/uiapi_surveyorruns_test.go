package mcpserver

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/componentproto"
)

func (f *surveyorFixture) postRuns(token string, body any) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, componentproto.RunsPath, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	f.h.ServeHTTP(rr, req)
	return rr
}

func runsBodyOf(runs []map[string]any, alarms ...map[string]any) map[string]any {
	return map[string]any{"protocol": 1, "id": "w-1", "perimeter": "team-a", "runs": runs, "alarms": alarms}
}

func runOf(f *surveyorFixture, i int, kb string) map[string]any {
	return map[string]any{"run_id": fmt.Sprintf("r-%03d", i), "task": "poll", "kb": kb, "started_at": f.clock.now().Add(time.Duration(i) * time.Second),
		"duration_ms": 1500, "outcome": "ok", "tokens_spent": 42}
}

type runsAnswer struct {
	Components []struct {
		Runs   []surveyorRun   `json:"runs"`
		Alarms []surveyorAlarm `json:"alarms"`
	} `json:"components"`
}

func (f *surveyorFixture) runsView(t *testing.T) runsAnswer {
	t.Helper()
	rr := f.get("reader", "/kbs/kb-a/components")
	if rr.Code != 200 {
		t.Fatalf("components %d %s", rr.Code, rr.Body)
	}
	var a runsAnswer
	if err := json.Unmarshal(rr.Body.Bytes(), &a); err != nil {
		t.Fatal(err)
	}
	return a
}

func TestRuns_StoredBoundedDeduplicatedAndShown(t *testing.T) {
	t.Parallel()
	f := kindFixture(t)
	f.beatOK(t, "surv", beat(nil))
	var runs []map[string]any
	for i := 0; i < 20; i++ {
		runs = append(runs, runOf(f, i, "kb-a"))
	}
	rr := f.postRuns("surv", runsBodyOf(runs))
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"received":20`) || !strings.Contains(rr.Body.String(), `"last_run_id":"r-019"`) {
		t.Fatalf("%d %s", rr.Code, rr.Body)
	}
	// A re-post is harmless; a 21st run evicts the oldest.
	f.postRuns("surv", runsBodyOf(runs[:5]))
	f.postRuns("surv", runsBodyOf([]map[string]any{runOf(f, 20, "kb-a")}))
	v := f.runsView(t)
	got := v.Components[0].Runs
	if len(got) != 20 || got[0].RunID != "r-020" || got[19].RunID != "r-001" {
		t.Fatalf("runs = %d first %s last %s", len(got), got[0].RunID, got[len(got)-1].RunID)
	}
	if got[0].TokensSpent != 42 || got[0].Replica != "w-1" || got[0].ReceivedAt.IsZero() {
		t.Fatalf("run = %+v", got[0])
	}
}

func TestRuns_UnwritableOrOtherKBIsDroppedAndAgesAreTheServers(t *testing.T) {
	t.Parallel()
	f := kindFixture(t)
	f.beatOK(t, "surv", beat(nil))
	far := runOf(f, 1, "kb-a")
	far["started_at"] = f.clock.now().Add(400 * 24 * time.Hour) // a skewed clock
	rr := f.postRuns("surv", runsBodyOf([]map[string]any{far, runOf(f, 2, "kb-b"), runOf(f, 3, "nope")}))
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"received":1`) {
		t.Fatalf("%d %s", rr.Code, rr.Body)
	}
	r := f.runsView(t).Components[0].Runs[0]
	if r.StartedAt.After(f.clock.now()) || !r.ReceivedAt.Equal(f.clock.now()) {
		t.Fatalf("started %v received %v: a skewed clock must not make a run look recent", r.StartedAt, r.ReceivedAt)
	}
}

func TestRuns_AlarmsReportedAndComputedAndExpireWithTheDeclaration(t *testing.T) {
	t.Parallel()
	f := kindFixture(t)
	f.beatOK(t, "surv", beat(map[string]any{"protocol": 2}))
	al := map[string]any{"code": "feed_failing", "kb": "kb-a", "source": "poll", "since": f.clock.now(), "detail": "boom"}
	if rr := f.postRuns("surv", runsBodyOf(nil, al)); rr.Code != 200 {
		t.Fatalf("%d %s", rr.Code, rr.Body)
	}
	codes := func() string {
		var out []string
		for _, a := range f.runsView(t).Components[0].Alarms {
			out = append(out, a.Origin+":"+a.Code)
		}
		return strings.Join(out, ",")
	}
	if got := codes(); got != "reported:feed_failing,server:protocol_skew" {
		t.Fatalf("alarms = %s", got)
	}
	f.clock.advance(5 * time.Minute) // the declaration goes stale: reported alarms go with it
	if f.multi.surveyors.hasRuns("surv", "team-a") {
		t.Fatal("runs outlive the declaration")
	}
}

func TestRuns_RefillAskedByTheHeartbeat(t *testing.T) {
	t.Parallel()
	f := kindFixture(t)
	want := func(w bool) {
		t.Helper()
		rr := f.post(t, "surv", beat(nil))
		if !strings.Contains(rr.Body.String(), fmt.Sprintf(`"runs_wanted":%v`, w)) {
			t.Fatalf("runs_wanted != %v: %s", w, rr.Body)
		}
	}
	want(true)
	f.postRuns("surv", runsBodyOf([]map[string]any{runOf(f, 1, "kb-a")}))
	want(false)
}

func TestRuns_PerimeterBookIsBounded(t *testing.T) {
	t.Parallel()
	s := &surveyorStore{}
	now := time.Now()
	for i := 0; i <= maxRunBooks; i++ {
		per := fmt.Sprintf("p-%d", i)
		if s.by == nil {
			s.by = map[string]map[string]surveyorDecl{"p": {}}
		}
		s.by["p"][per] = surveyorDecl{Principal: "p", Surveyor: per, Perimeter: per, IntervalSeconds: 3600, ReceivedAt: now}
		s.putRuns("p", per, []surveyorRun{{RunID: "x", KB: "kb-a"}}, nil, nil, now.Add(time.Duration(i)*time.Second))
	}
	if n := s.booksLocked(); n != maxRunBooks {
		t.Fatalf("books = %d", n)
	}
	if _, ok := s.runs["p"]["p-0"]; ok {
		t.Fatal("the oldest perimeter was not evicted")
	}
}

func TestRuns_KindMatrixAndValidation(t *testing.T) {
	t.Parallel()
	f := kindFixture(t)
	f.beatOK(t, "surv", beat(nil))
	b := runsBodyOf([]map[string]any{runOf(f, 1, "kb-a")})
	for token, want := range map[string]int{"surv": 200, "cli": 403, "hum": 404, "unset": 404} {
		// hum and unset are let through the kind check, then find no live declaration of their own.
		if rr := f.postRuns(token, b); rr.Code != want {
			t.Errorf("%s: %d, want %d: %s", token, rr.Code, want, rr.Body)
		}
	}
	bad := map[string]map[string]any{
		"protocol": {"protocol": 9, "id": "w-1", "perimeter": "team-a"},
		"outcome":  runsBodyOf([]map[string]any{{"run_id": "a", "task": "poll", "kb": "kb-a", "outcome": "weird"}}),
		"code":     runsBodyOf(nil, map[string]any{"code": "silent"}),
	}
	for name, body := range bad {
		if rr := f.postRuns("surv", body); rr.Code != 400 && rr.Code != 426 {
			t.Errorf("%s: %d", name, rr.Code)
		}
	}
	if rr := f.postRuns("surv", map[string]any{"protocol": 1, "id": "w-1", "perimeter": "team-a", "runs": make([]map[string]any, 21)}); rr.Code != 400 {
		t.Errorf("21 runs: %d", rr.Code)
	}
}

func TestRuns_KBlessAlarmIsShownWithoutTextAndReplicaMustBeDeclared(t *testing.T) {
	t.Parallel()
	f := kindFixture(t)
	f.beatOK(t, "surv", beat(nil))
	al := map[string]any{"code": "auth_refused", "since": f.clock.now(), "source": "poll", "detail": "free text"}
	if rr := f.postRuns("surv", runsBodyOf(nil, al)); rr.Code != 200 {
		t.Fatalf("%d %s", rr.Code, rr.Body)
	}
	var found bool
	for _, a := range f.runsView(t).Components[0].Alarms {
		if a.Code == "auth_refused" {
			found = true
			if a.Source != "" || a.Detail != "" || a.Since == nil {
				t.Fatalf("kb-less alarm = %+v, want code and time only", a)
			}
		}
	}
	if !found {
		t.Fatal("kb-less alarm not shown")
	}
	// A replica id the principal never declared is the 404 of no declaration.
	other := runsBodyOf([]map[string]any{runOf(f, 1, "kb-a")})
	other["id"] = "not-mine"
	if rr := f.postRuns("surv", other); rr.Code != 404 {
		t.Fatalf("undeclared replica: %d", rr.Code)
	}
}
