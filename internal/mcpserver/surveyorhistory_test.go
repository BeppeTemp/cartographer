package mcpserver

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func histRun(id, task, outcome string, tokens, dur int64) surveyorRun {
	return surveyorRun{RunID: id, Replica: "r1", Task: task, KB: "kb-a", Outcome: outcome, TokensSpent: tokens, DurationMS: dur}
}

func newTestHistory(t *testing.T, now time.Time) (*runHistory, string, *[]string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".cartographer", runHistoryFile)
	h := newRunHistory()
	var logs []string
	var mu sync.Mutex
	h.logf = func(f string, a ...any) { mu.Lock(); logs = append(logs, fmt.Sprintf(f, a...)); mu.Unlock() }
	h.load(path, now)
	return h, path, &logs
}

var histNow = time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)

func histTotal(h *runHistory, kb string) (runs, tokens int64) {
	for _, r := range h.query(kb, 90, histNow).Days {
		runs += r.Runs
		tokens += r.Tokens
	}
	return
}

// AC-surveyor-run-history-1
func TestRunHistoryCountsOnce(t *testing.T) {
	h, path, _ := newTestHistory(t, histNow)
	runs := []surveyorRun{histRun("a-1", "poll", "ok", 10, 100), histRun("a-2", "ingest", "failed", 5, 50), histRun("a-3", "poll", "ok", 1, 1)}
	if n := h.add("per", "rep", runs, histNow); n != 3 {
		t.Fatalf("counted %d, want 3", n)
	}
	if n := h.add("per", "rep", runs, histNow); n != 0 {
		t.Fatalf("repost counted %d, want 0", n)
	}
	// the same ids from another replica are other runs
	if n := h.add("per", "rep2", runs[:1], histNow); n != 1 {
		t.Fatalf("other replica counted %d, want 1", n)
	}
	h2 := newRunHistory()
	h2.load(path, histNow)
	if n := h2.add("per", "rep", runs, histNow); n != 0 {
		t.Fatalf("after load counted %d, want 0", n)
	}
	rows := h2.query("kb-a", 30, histNow).Days
	got := map[string]int64{}
	for _, r := range rows {
		got[r.Task+"/"+r.Outcome] += r.Runs
	}
	if got["poll/ok"] != 3 || got["ingest/failed"] != 1 || len(got) != 2 {
		t.Fatalf("rows: %+v", got)
	}
	if tok, _ := json.Marshal(rows); strings.Contains(string(tok), "a-1") || strings.Contains(string(tok), "rep") {
		t.Fatalf("a run or replica id leaked: %s", tok)
	}
}

// AC-surveyor-run-history-2
func TestRunHistoryRetention(t *testing.T) {
	h, path, _ := newTestHistory(t, histNow)
	// the day is the receipt day: a run counted 100 days ago is dropped on the next add.
	old := histNow.AddDate(0, 0, -100)
	h.add("per", "rep", []surveyorRun{histRun("old", "poll", "ok", 1, 1)}, old)
	edge := histNow.AddDate(0, 0, -runHistoryDays)
	h.add("per", "rep", []surveyorRun{histRun("edge", "poll", "ok", 1, 1)}, edge)
	h.add("per", "rep", []surveyorRun{histRun("new", "poll", "ok", 1, 1)}, histNow)
	v := h.query("kb-a", 90, histNow)
	// the edge day is still held (FirstDay) though outside a 90-day window of rows
	if len(v.Days) != 1 || v.FirstDay != historyDay(edge) {
		t.Fatalf("after add: %+v", v)
	}
	// a day between two receipts: midnight UTC moves the bucket
	h.add("per", "rep", []surveyorRun{histRun("m1", "poll", "ok", 0, 0)}, time.Date(2026, 3, 10, 23, 59, 0, 0, time.UTC))
	h.add("per", "rep", []surveyorRun{histRun("m2", "poll", "ok", 0, 0)}, time.Date(2026, 3, 11, 0, 1, 0, 0, time.UTC))
	days := map[string]bool{}
	for _, r := range h.query("kb-a", 90, histNow.AddDate(0, 0, 1)).Days {
		days[r.Day] = true
	}
	if !days["2026-03-10"] || !days["2026-03-11"] {
		t.Fatalf("days: %v", days)
	}
	// load drops what the clock says is too old
	later := histNow.AddDate(0, 0, 10)
	h2 := newRunHistory()
	h2.load(path, later)
	if f := h2.query("kb-a", 90, later).FirstDay; f != historyDay(histNow.AddDate(0, 0, -runHistoryDays+10)) && f < historyCutoff(later) {
		t.Fatalf("load kept %s, cutoff %s", f, historyCutoff(later))
	}
	// the row cap drops the oldest day whole
	c := newRunHistory()
	c.maxRows = 3
	for d := 0; d < 3; d++ {
		at := histNow.AddDate(0, 0, -2+d)
		c.add("per", "rep", []surveyorRun{histRun(fmt.Sprintf("x%d-a", d), "poll", "ok", 1, 1), histRun(fmt.Sprintf("x%d-b", d), "ingest", "ok", 1, 1)}, at)
	}
	v = c.query("kb-a", 90, histNow)
	if len(v.Days) != 2 || v.FirstDay != historyDay(histNow) {
		t.Fatalf("cap: %+v", v)
	}
}

func TestRunHistoryRingAndReplicaBounds(t *testing.T) {
	h := newRunHistory()
	for i := 0; i < runHistorySeenIDs+10; i++ {
		h.add("per", "rep", []surveyorRun{histRun(fmt.Sprintf("id-%d", i), "poll", "ok", 0, 0)}, histNow)
	}
	if n := len(h.seen["per/rep"].ids); n != runHistorySeenIDs {
		t.Fatalf("ring %d", n)
	}
	// a run just evicted from the ring would count again; one within it does not
	if h.add("per", "rep", []surveyorRun{histRun("id-0", "poll", "ok", 0, 0)}, histNow) != 1 {
		t.Fatal("evicted id should count again")
	}
	if h.add("per", "rep", []surveyorRun{histRun("id-73", "poll", "ok", 0, 0)}, histNow) != 0 {
		t.Fatal("recent id counted twice")
	}
	for i := 0; i < runHistoryReplicas+20; i++ {
		h.add("per", fmt.Sprintf("rep-%d", i), []surveyorRun{histRun("z", "poll", "ok", 0, 0)}, histNow.Add(time.Duration(i)*time.Second))
	}
	if len(h.seen) > runHistoryReplicas {
		t.Fatalf("replicas %d", len(h.seen))
	}
	if _, ok := h.seen["per/rep-0"]; ok {
		t.Fatal("oldest replica kept")
	}
	if _, ok := h.seen["per/rep-"+fmt.Sprint(runHistoryReplicas+19)]; !ok {
		t.Fatal("newest replica evicted")
	}
}

// AC-surveyor-run-history-4
func TestRunHistoryBadFileAndSymlink(t *testing.T) {
	for name, content := range map[string]string{"malformed": "{not json", "version2": `{"version":2,"days":{},"seen":{}}`} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, runHistoryFile)
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			h := newRunHistory()
			h.logf = func(string, ...any) {}
			h.load(path, histNow)
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("file not moved aside: %v", err)
			}
			m, _ := filepath.Glob(path + ".bad-*")
			if len(m) != 1 {
				t.Fatalf("aside: %v", m)
			}
			if b, _ := os.ReadFile(m[0]); string(b) != content {
				t.Fatalf("aside content changed")
			}
			if v := h.query("kb-a", 30, histNow); len(v.Days) != 0 || v.FirstDay != "" {
				t.Fatalf("not empty: %+v", v)
			}
		})
	}
	t.Run("symlink", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("symlinks need privileges on windows")
		}
		dir := t.TempDir()
		target := filepath.Join(dir, "victim")
		const keep = `{"version":1,"days":{},"seen":{}}`
		if err := os.WriteFile(target, []byte(keep), 0o600); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, "state", runHistoryFile)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, path); err != nil {
			t.Fatal(err)
		}
		h := newRunHistory()
		var logged int
		h.logf = func(string, ...any) { logged++ }
		h.load(path, histNow)
		h.add("per", "rep", []surveyorRun{histRun("a", "poll", "ok", 1, 1)}, histNow)
		if b, _ := os.ReadFile(target); string(b) != keep {
			t.Fatalf("written through the symlink: %q", b)
		}
		if fi, _ := os.Lstat(path); fi.Mode()&os.ModeSymlink == 0 {
			t.Fatal("symlink replaced")
		}
		if logged != 1 {
			t.Fatalf("logged %d", logged)
		}
		if runs, _ := histTotal(h, "kb-a"); runs != 1 {
			t.Fatal("memory lost")
		}
	})
}

func TestRunHistoryHandEditedRowsDropped(t *testing.T) {
	path := filepath.Join(t.TempDir(), runHistoryFile)
	good := `{"kb":"kb-a","perimeter":"per","task":"poll","outcome":"ok","runs":2,"tokens":3,"duration_ms":4}`
	bad := []string{
		`{"kb":"kb-a","perimeter":"per","task":"poll","outcome":"<b>x</b>","runs":1}`,
		`{"kb":"kb-a","perimeter":"Per Haps","task":"poll","outcome":"ok","runs":1}`,
		`{"kb":"kb-a","perimeter":"per","task":"po ll","outcome":"ok","runs":1}`,
		`{"kb":"","perimeter":"per","task":"poll","outcome":"ok","runs":1}`,
		`{"kb":"kb-a","perimeter":"per","task":"poll","outcome":"ok","runs":-1}`,
	}
	day := historyDay(histNow)
	body := fmt.Sprintf(`{"version":1,"days":{%q:[%s,%s],"junk":[%s]},"seen":{"bad key":{"at":%q,"ids":["x"]}}}`,
		day, good, strings.Join(bad, ","), good, histNow.Format(time.RFC3339))
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	h := newRunHistory()
	h.load(path, histNow)
	v := h.query("kb-a", 30, histNow)
	if len(v.Days) != 1 || v.Days[0].Runs != 2 || len(h.seen) != 0 {
		t.Fatalf("view %+v seen %v", v, h.seen)
	}
}

// AC-surveyor-run-history-3
func TestRunHistoryConcurrentAdd(t *testing.T) {
	h, path, _ := newTestHistory(t, histNow)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 10; i++ {
				id := fmt.Sprintf("g%d-%d", g, i)
				h.add("per", fmt.Sprintf("rep%d", g), []surveyorRun{histRun(id, "poll", "ok", 2, 3), histRun(id, "poll", "ok", 2, 3)}, histNow)
			}
		}()
	}
	wg.Wait()
	runs, tokens := histTotal(h, "kb-a")
	if runs != 80 || tokens != 160 {
		t.Fatalf("totals %d/%d", runs, tokens)
	}
	h2 := newRunHistory()
	h2.load(path, histNow)
	if a, b := h.query("kb-a", 90, histNow), h2.query("kb-a", 90, histNow); fmt.Sprint(a) != fmt.Sprint(b) {
		t.Fatalf("file differs from memory:\n%v\n%v", a, b)
	}
	if len(h2.seen) != 8 {
		t.Fatalf("seen %d", len(h2.seen))
	}
}

// AC-surveyor-run-history-5
func TestRunHistoryFlushFailureRetried(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a directory the process cannot write")
	}
	root := t.TempDir()
	dir := filepath.Join(root, ".cartographer")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, runHistoryFile)
	h := newRunHistory()
	var logs []string
	h.logf = func(f string, a ...any) { logs = append(logs, fmt.Sprintf(f, a...)) }
	h.load(path, histNow)
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o755) })
	h.add("per", "rep", []surveyorRun{histRun("a", "poll", "ok", 1, 1)}, histNow)
	h.add("per", "rep", []surveyorRun{histRun("b", "poll", "ok", 1, 1)}, histNow)
	if runs, _ := histTotal(h, "kb-a"); runs != 2 {
		t.Fatalf("memory lost: %d", runs)
	}
	if len(logs) != 1 {
		t.Fatalf("logs: %v", logs)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// nothing new to count, but the pending change is written
	h.add("per", "rep", []surveyorRun{histRun("b", "poll", "ok", 1, 1)}, histNow)
	h2 := newRunHistory()
	h2.load(path, histNow)
	if runs, _ := histTotal(h2, "kb-a"); runs != 2 {
		t.Fatalf("file has %d, want 2", runs)
	}
}

func TestRunHistoryFlushesNeverGoBackwards(t *testing.T) {
	h, path, _ := newTestHistory(t, histNow)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 5; i++ {
				h.add("per", fmt.Sprintf("r%d", g), []surveyorRun{histRun(fmt.Sprintf("i%d", i), "poll", "ok", 0, 0)}, histNow)
				h.flush()
			}
		}()
	}
	wg.Wait()
	h2 := newRunHistory()
	h2.load(path, histNow)
	if runs, _ := histTotal(h2, "kb-a"); runs != 40 {
		t.Fatalf("file has %d, want 40", runs)
	}
}

func TestRunHistorySeenIsPerKB(t *testing.T) {
	h := newRunHistory()
	b := histRun("same", "poll", "ok", 1, 1)
	b.KB = "kb-b"
	if h.add("per", "rep", []surveyorRun{histRun("same", "poll", "ok", 1, 1)}, histNow) != 1 || h.add("per", "rep", []surveyorRun{b}, histNow) != 1 {
		t.Fatal("a run id counted for kb-a suppressed the same id for kb-b")
	}
	if h.add("per", "rep", []surveyorRun{b}, histNow) != 0 {
		t.Fatal("repeat counted")
	}
}

func TestRunHistoryUnreadableFileKept(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a file the process cannot read")
	}
	path := filepath.Join(t.TempDir(), runHistoryFile)
	const body = `{"version":1,"days":{},"seen":{}}`
	if err := os.WriteFile(path, []byte(body), 0o000); err != nil {
		t.Fatal(err)
	}
	h := newRunHistory()
	h.logf = func(string, ...any) {}
	h.load(path, histNow)
	h.add("per", "rep", []surveyorRun{histRun("a", "poll", "ok", 1, 1)}, histNow)
	os.Chmod(path, 0o600)
	if b, _ := os.ReadFile(path); string(b) != body {
		t.Fatalf("file touched: %q", b)
	}
	if m, _ := filepath.Glob(path + ".bad-*"); len(m) != 0 || h.persisted() {
		t.Fatalf("renamed aside %v or still persisted", m)
	}
	if runs, _ := histTotal(h, "kb-a"); runs != 1 {
		t.Fatal("memory lost")
	}
}

func TestRunHistoryQueryScopesKBAndWindow(t *testing.T) {
	h := newRunHistory()
	b := histRun("b1", "poll", "ok", 1, 1)
	b.KB = "kb-b"
	h.add("per", "rep", []surveyorRun{histRun("a1", "poll", "ok", 1, 1), b}, histNow.AddDate(0, 0, -5))
	h.add("per", "rep", []surveyorRun{histRun("a2", "poll", "ok", 1, 1)}, histNow)
	for _, r := range h.query("kb-a", 30, histNow).Days {
		if r.KB != "" {
			t.Fatalf("kb in row: %+v", r)
		}
	}
	if v := h.query("kb-a", 30, histNow); len(v.Days) != 2 || v.Persisted {
		t.Fatalf("%+v", v)
	}
	if v := h.query("kb-a", 3, histNow); len(v.Days) != 1 || v.FirstDay != historyDay(histNow.AddDate(0, 0, -5)) {
		t.Fatalf("%+v", v)
	}
	if v := h.query("kb-a", 0, histNow); len(v.Days) != 1 {
		t.Fatalf("clamp low: %+v", v)
	}
}

func TestRunHistoryFirstDayIsOfTheQueriedKB(t *testing.T) {
	h := newRunHistory()
	b := histRun("b1", "poll", "ok", 1, 1)
	b.KB = "kb-b"
	h.add("per", "rep", []surveyorRun{b}, histNow.AddDate(0, 0, -20))
	h.add("per", "rep", []surveyorRun{histRun("a1", "poll", "ok", 1, 1)}, histNow)
	if f := h.query("kb-a", 30, histNow).FirstDay; f != historyDay(histNow) {
		t.Fatalf("first day leaked another KB's: %s", f)
	}
}

func TestRunHistoryBadFileNotMovedIsNotOverwritten(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a directory the process cannot write")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, runHistoryFile)
	const body = "{not json"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o755) })
	h := newRunHistory()
	h.logf = func(string, ...any) {}
	h.load(path, histNow)
	h.add("per", "rep", []surveyorRun{histRun("a", "poll", "ok", 1, 1)}, histNow)
	os.Chmod(dir, 0o755)
	if b, _ := os.ReadFile(path); string(b) != body || h.persisted() {
		t.Fatalf("file overwritten or still persisted: %q", b)
	}
	if runs, _ := histTotal(h, "kb-a"); runs != 1 {
		t.Fatal("memory lost")
	}
}
