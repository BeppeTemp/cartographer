package mcpserver

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/config"
	"github.com/BeppeTemp/cartographer/internal/testkbs"
)

type historyAnswer struct {
	RetentionDays int          `json:"retention_days"`
	Persisted     bool         `json:"persisted"`
	FirstDay      *string      `json:"first_day"`
	Now           time.Time    `json:"now"`
	Days          []historyRow `json:"days"`
}

func (f *surveyorFixture) history(t *testing.T, token, kb, query string) historyAnswer {
	t.Helper()
	rr := f.get(token, "/kbs/"+kb+"/surveyor-history"+query)
	if rr.Code != 200 {
		t.Fatalf("history %d %s", rr.Code, rr.Body)
	}
	var a historyAnswer
	if err := json.Unmarshal(rr.Body.Bytes(), &a); err != nil {
		t.Fatal(err)
	}
	return a
}

func (a historyAnswer) total() (runs, tokens int64) {
	for _, r := range a.Days {
		runs += r.Runs
		tokens += r.Tokens
	}
	return
}

// historyFixture is a kindFixture whose history lives in dataDir ("" = memory).
func historyFixture(t *testing.T, dataDir string) *surveyorFixture {
	t.Helper()
	f := kindFixture(t)
	f.multi.SetRunHistory(dataDir)
	f.beatOK(t, "surv", beat(nil))
	return f
}

// AC-surveyor-run-history-6
func TestSurveyorHistoryCountsOnceAcrossRestart(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	f := historyFixture(t, dir)
	var runs []map[string]any
	for i := 0; i < 3; i++ {
		runs = append(runs, runOf(f, i, "kb-a"))
	}
	if rr := f.postRuns("surv", runsBodyOf(runs)); rr.Code != 200 {
		t.Fatalf("%d %s", rr.Code, rr.Body)
	}
	a := f.history(t, "reader", "kb-a", "")
	if n, tok := a.total(); n != 3 || tok != 126 || !a.Persisted || a.RetentionDays != 90 || a.FirstDay == nil || *a.FirstDay != "2026-01-10" {
		t.Fatalf("after one post: %+v", a)
	}
	f.postRuns("surv", runsBodyOf(runs)) // the same post again
	if n, _ := f.history(t, "reader", "kb-a", "").total(); n != 3 {
		t.Fatalf("a repeat post counted: %d", n)
	}
	if _, err := os.Stat(config.SurveyorHistoryPath(dir)); err != nil {
		t.Fatalf("no file written: %v", err)
	}
	// A restart on the same data dir: the Surveyor re-posts its runs on runs_wanted.
	g := historyFixture(t, dir)
	if n, _ := g.history(t, "reader", "kb-a", "").total(); n != 3 {
		t.Fatalf("restart forgot the counts: %d", n)
	}
	g.postRuns("surv", runsBodyOf(runs))
	g.postRuns("surv", runsBodyOf([]map[string]any{runOf(g, 9, "kb-a")}))
	if n, _ := g.history(t, "reader", "kb-a", "").total(); n != 4 {
		t.Fatalf("the re-post after a restart doubled the counts: %d", n)
	}
}

// AC-surveyor-run-history-7
func TestSurveyorHistoryOwnKBRowsOnly(t *testing.T) {
	t.Parallel()
	f := historyFixture(t, t.TempDir())
	both := []auth.Permission{{KB: "kb-a", Write: true}, {KB: "kb-b", Write: true}}
	ts := auth.NewScopedTokenStore([]auth.ScopedToken{
		{Token: "surv", Principal: "surv", Kind: auth.KindSurveyor, Policy: auth.Policy{Permissions: []auth.Permission{{KB: "kb-a", Write: true}}}},
		{Token: "survab", Principal: "survab", Kind: auth.KindSurveyor, Policy: auth.Policy{Permissions: both}},
		{Token: "reader", Principal: "reader", Policy: auth.Policy{Permissions: []auth.Permission{{KB: "kb-a"}}}},
		{Token: "readerb", Principal: "readerb", Policy: auth.Policy{Permissions: []auth.Permission{{KB: "kb-b"}}}},
	})
	f.h = ts.Middleware(f.multi.Handler())
	f.beatOK(t, "survab", beat(map[string]any{"kbs": []string{"kb-a", "kb-b"}}))
	// "surv" cannot write kb-b: that run is neither kept nor counted.
	f.postRuns("surv", runsBodyOf([]map[string]any{runOf(f, 1, "kb-a"), runOf(f, 2, "kb-b")}))
	// "survab" writes both.
	f.postRuns("survab", runsBodyOf([]map[string]any{runOf(f, 3, "kb-b")}))
	a := f.history(t, "reader", "kb-a", "")
	if n, _ := a.total(); n != 1 {
		t.Fatalf("kb-a rows: %+v", a)
	}
	if b := f.history(t, "readerb", "kb-b", ""); func() int64 { n, _ := b.total(); return n }() != 1 {
		t.Fatalf("kb-b rows: %+v", b)
	}
	// kb-b was seen first by the Surveyor that could not count it: the first day
	// of kb-a never comes from kb-b's rows (and the other way round).
	raw := f.get("reader", "/kbs/kb-a/surveyor-history").Body.String()
	if strings.Contains(raw, "kb-b") || strings.Contains(raw, "survab") || strings.Contains(raw, "w-1") || strings.Contains(raw, "r-00") {
		t.Fatalf("the answer names another KB, a principal, a replica or a run: %s", raw)
	}
	if rr := f.get("reader", "/kbs/kb-b/surveyor-history"); rr.Code != 404 {
		t.Fatalf("a reader of kb-a reads kb-b: %d", rr.Code)
	}
}

// AC-surveyor-run-history-8
func TestSurveyorHistoryNotFoundAndBadDays(t *testing.T) {
	t.Parallel()
	f := historyFixture(t, t.TempDir())
	unknown := f.get("reader", "/kbs/nope/surveyor-history")
	if rr := f.get("other", "/kbs/kb-a/surveyor-history"); rr.Code != 404 || rr.Body.String() != unknown.Body.String() {
		t.Fatalf("a caller that does not see the KB: %d %s (unknown route: %d %s)", rr.Code, rr.Body, unknown.Code, unknown.Body)
	}
	for _, q := range []string{"?days=0", "?days=x", "?days=91", "?days=-1"} {
		rr := f.get("reader", "/kbs/kb-a/surveyor-history"+q)
		if rr.Code != 400 || !strings.Contains(rr.Body.String(), `"days"`) {
			t.Fatalf("%s: %d %s", q, rr.Code, rr.Body)
		}
	}
	if rr := f.get("reader", "/kbs/kb-a/surveyor-history?days=90"); rr.Code != 200 {
		t.Fatalf("days=90: %d", rr.Code)
	}
}

// AC-surveyor-run-history-9
func TestSurveyorHistoryWithoutDataDir(t *testing.T) {
	t.Parallel()
	f := historyFixture(t, "")
	f.postRuns("surv", runsBodyOf([]map[string]any{runOf(f, 1, "kb-a")}))
	a := f.history(t, "reader", "kb-a", "")
	if n, _ := a.total(); n != 1 || a.Persisted {
		t.Fatalf("%+v", a)
	}
	empty := f.history(t, "reader", "kb-a", "?days=1")
	if len(empty.Days) != 1 {
		t.Fatalf("%+v", empty)
	}
	g := kindFixture(t) // no SetRunHistory at all
	if a := g.history(t, "reader", "kb-a", ""); a.Persisted || a.FirstDay != nil || a.Days == nil || len(a.Days) != 0 {
		t.Fatalf("empty answer: %+v", a)
	}
}

// AC-surveyor-run-history-10
func TestRunsRouteUnchangedByHistoryFailure(t *testing.T) {
	t.Parallel()
	post := func(dataDir string, breakIt bool) string {
		f := historyFixture(t, dataDir)
		if breakIt {
			// After the load: every flush now fails (the state directory cannot be made).
			if err := os.WriteFile(filepath.Join(dataDir, ".cartographer"), []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		rr := f.postRuns("surv", runsBodyOf([]map[string]any{runOf(f, 1, "kb-a"), runOf(f, 2, "kb-a")}))
		if rr.Code != 200 {
			t.Fatalf("%d %s", rr.Code, rr.Body)
		}
		return rr.Body.String()
	}
	without := post("", false)
	bad := t.TempDir()
	if with := post(bad, true); with != without {
		t.Fatalf("the answer changed because the history could not be written:\n%s\n%s", with, without)
	}
}

func TestSurveyorHistoryConcurrentPosts(t *testing.T) {
	t.Parallel()
	f := historyFixture(t, t.TempDir())
	type who struct{ per, rep string }
	var ws []who
	for _, per := range []string{"team-a", "team-b"} {
		for _, rep := range []string{"w-1", "w-2", "w-3"} {
			rep := per + "-" + rep // a declaration is keyed by principal and replica id
			ws = append(ws, who{per, rep})
			f.beatOK(t, "surv", beat(map[string]any{"surveyor": rep, "perimeter": per}))
		}
	}
	var wg sync.WaitGroup
	for _, w := range ws {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 5; i++ {
				run := map[string]any{"run_id": fmt.Sprintf("%s-%d", w.rep, i), "task": "poll", "kb": "kb-a",
					"started_at": f.clock.now(), "duration_ms": 10, "outcome": "ok", "tokens_spent": 1}
				f.postRuns("surv", map[string]any{"protocol": 1, "id": w.rep, "perimeter": w.per, "runs": []map[string]any{run, run}})
			}
		}()
	}
	wg.Wait()
	if n, _ := f.history(t, "reader", "kb-a", "").total(); n != int64(len(ws)*5) {
		t.Fatalf("total = %d, want %d", n, len(ws)*5)
	}
}

// AC-surveyor-run-history-14
func TestSeededHistoryLoads(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	b, err := testkbs.SurveyorHistoryFile(now)
	if err != nil {
		t.Fatal(err)
	}
	var f historyFile
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	want := 0
	for _, rows := range f.Days {
		want += len(rows)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, ".cartographer", runHistoryFile)
	if err := testkbs.WriteSurveyorHistory(dir, now); err != nil {
		t.Fatal(err)
	}
	h := newRunHistory()
	var logs []string
	h.logf = func(format string, a ...any) { logs = append(logs, fmt.Sprintf(format, a...)) }
	h.load(path, now)
	if len(logs) != 0 || h.path == "" {
		t.Fatalf("the seed was refused: %v", logs)
	}
	if ents, _ := filepath.Glob(path + ".bad-*"); len(ents) != 0 {
		t.Fatalf("renamed aside: %v", ents)
	}
	days := map[string]bool{}
	for k := range h.rows {
		days[k.day] = true
		if k.day == historyDay(now) {
			t.Fatalf("a seeded row is dated today: %v", k)
		}
	}
	if len(h.rows) != want || len(days) != testkbs.SurveyorHistorySeedDays {
		t.Fatalf("rows %d (want %d), days %d", len(h.rows), want, len(days))
	}
	// An existing file is never overwritten.
	if err := os.WriteFile(path, []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := testkbs.WriteSurveyorHistory(dir, now); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != "mine" {
		t.Fatalf("overwritten: %q", got)
	}
}
