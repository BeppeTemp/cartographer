package worker

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/client"
)

// ledgerCaller is a fake MCP server for log_tail and log_append: per KB a list
// of entries (newest first) it renders like the real log_tail does.
type ledgerCaller struct {
	mu      sync.Mutex
	entries []string // first body lines, newest first
	missing bool     // no log.md
	tailErr error
	failApp int // log_append failures left
	appends []map[string]any
	tails   []int
}

func (l *ledgerCaller) ListTools() ([]client.ToolInfo, error) { return nil, nil }

func (l *ledgerCaller) CallTool(name string, args any) (client.ToolOutput, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	a := args.(map[string]any)
	switch name {
	case "log_tail":
		if l.tailErr != nil {
			return client.ToolOutput{}, l.tailErr
		}
		if l.missing {
			return client.ToolOutput{IsError: true, Text: "log_tail: concept not found: log.md"}, nil
		}
		n := a["n"].(int)
		l.tails = append(l.tails, n)
		if len(l.entries) == 0 {
			return client.ToolOutput{Text: `{"entries": 0, "note": "no entries"}`}, nil
		}
		var sb strings.Builder
		for i, e := range l.entries {
			if i >= n {
				break
			}
			fmt.Fprintf(&sb, "## 2026-10-29T10:00:00Z\n\n%s\n\n", e)
		}
		return client.ToolOutput{Text: sb.String()}, nil
	case "log_append":
		if l.failApp > 0 {
			l.failApp--
			return client.ToolOutput{IsError: true, Text: "boom"}, nil
		}
		l.appends = append(l.appends, a)
		return client.ToolOutput{Text: "ok"}, nil
	}
	return client.ToolOutput{}, errors.New("unexpected tool " + name)
}

var (
	testNow  = time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	profiles = []Profile{
		{Name: "cloud", DailyTokens: 1000, RunTokens: 400},
		{Name: "local", DailyTokens: 100000, RunTokens: 100000},
	}
)

func newTestBudget(kbs map[string]*ledgerCaller) *Budget {
	b := NewBudget("docs-night", profiles, nil, func(kb string) ToolCaller { return kbs[kb] })
	b.Logf = func(string, ...any) {}
	return b
}

func TestBudgetSumsPerProfileAcrossKBs(t *testing.T) {
	kbs := map[string]*ledgerCaller{
		"kb-a": {entries: []string{
			"[worker/docs-night] run r1 day=2026-10-29 tokens.cloud=300 tokens.local=5000 units=2 commits=2 stopped=done — ok tokens.cloud=9999",
			"[worker/docs-night] run r0 day=2026-10-28 tokens.cloud=900 units=1 commits=1 stopped=done", // yesterday
			"[worker/docs-night] run r9 day=2026-10-29 tokens.gone=777 units=1 commits=1 stopped=done",  // unknown profile
		}},
		"work-kb": {entries: []string{"[worker/docs-night] run r2 day=2026-10-29 tokens.cloud=100 units=1 commits=0 stopped=budget"}},
	}
	b := newTestBudget(kbs)
	if err := b.Begin([]string{"kb-a", "work-kb"}, testNow); err != nil {
		t.Fatal(err)
	}
	if got := b.Remaining("cloud"); got != 600 {
		t.Errorf("cloud remaining = %d, want 600 (summary text must not count)", got)
	}
	if got := b.Remaining("local"); got != 95000 {
		t.Errorf("local remaining = %d", got)
	}
}

func TestBudgetGrantAndExhaustedProfile(t *testing.T) {
	kbs := map[string]*ledgerCaller{"kb-a": {entries: []string{"[worker/docs-night] run r1 day=2026-10-29 tokens.cloud=700 units=1 commits=1 stopped=done"}}}
	b := newTestBudget(kbs)
	if err := b.Begin([]string{"kb-a"}, testNow); err != nil {
		t.Fatal(err)
	}
	if g := b.Grant("cloud"); g != 300 { // min(300 remaining, 400 run)
		t.Fatalf("grant = %d, want 300", g)
	}
	b.Charge("kb-a", "cloud", 300)
	if g := b.Grant("cloud"); g > 0 {
		t.Fatalf("exhausted profile still granted %d", g)
	}
	if g := b.Grant("local"); g != 100000 {
		t.Fatalf("local grant = %d: a spent cloud budget must not stop it", g)
	}
	if g := b.Grant("nope"); g != 0 {
		t.Fatal("unknown profile granted")
	}
}

func TestBudgetRunTokensBoundsTheRun(t *testing.T) {
	b := newTestBudget(map[string]*ledgerCaller{"kb-a": {}})
	b.Begin([]string{"kb-a"}, testNow)
	b.Charge("kb-a", "cloud", 150)
	if g := b.Grant("cloud"); g != 250 { // min(1000, 400) - 150
		t.Fatalf("grant = %d, want 250", g)
	}
}

func TestBudgetLedgerEdgeCases(t *testing.T) {
	for name, kb := range map[string]*ledgerCaller{
		"empty note":  {},
		"missing log": {missing: true},
	} {
		b := newTestBudget(map[string]*ledgerCaller{"kb-a": kb})
		if err := b.Begin([]string{"kb-a"}, testNow); err != nil {
			t.Errorf("%s: %v", name, err)
		} else if b.Remaining("cloud") != 1000 {
			t.Errorf("%s: spent should be zero", name)
		}
	}
	b := newTestBudget(map[string]*ledgerCaller{"kb-a": {tailErr: errors.New("down")}})
	if err := b.Begin([]string{"kb-a"}, testNow); err == nil {
		t.Error("any other log_tail failure must skip the run")
	}
	bad := &ledgerCaller{entries: []string{"[worker/docs-night] run r1 tokens.cloud=5 stopped=done", "[worker/other] run r2 day=2026-10-29 tokens.cloud=5"}}
	b = newTestBudget(map[string]*ledgerCaller{"kb-a": bad})
	var warned int
	b.Logf = func(string, ...any) { warned++ }
	if err := b.Begin([]string{"kb-a"}, testNow); err != nil || warned != 1 || b.Remaining("cloud") != 1000 {
		t.Errorf("malformed entries: err=%v warned=%d", err, warned)
	}
}

func TestBudgetDoublesTheReadThenFailsClosed(t *testing.T) {
	today := "[worker/docs-night] run r day=2026-10-29 tokens.cloud=1 stopped=done"
	var many []string
	for i := 0; i < 250; i++ {
		many = append(many, today)
	}
	kb := &ledgerCaller{entries: many}
	b := newTestBudget(map[string]*ledgerCaller{"kb-a": kb})
	if err := b.Begin([]string{"kb-a"}, testNow); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(kb.tails) != "[100 200 400]" || b.Remaining("cloud") != 750 {
		t.Fatalf("tails=%v remaining=%d", kb.tails, b.Remaining("cloud"))
	}
	for len(many) < ledgerCap+1 {
		many = append(many, today)
	}
	kb = &ledgerCaller{entries: many}
	b = newTestBudget(map[string]*ledgerCaller{"kb-a": kb})
	if err := b.Begin([]string{"kb-a"}, testNow); !errors.Is(err, ErrLedgerTooLong) {
		t.Fatalf("err = %v, want ErrLedgerTooLong", err)
	}
}

func TestBudgetDayFollowsTheWindowZone(t *testing.T) {
	rome, _ := time.LoadLocation("Europe/Rome")
	b := NewBudget("x", profiles, rome, nil)
	if d := b.Day(time.Date(2026, 10, 29, 23, 30, 0, 0, time.UTC)); d != "2026-10-30" {
		t.Fatalf("day = %s", d)
	}
}

func TestBudgetCloseWritesOneEntryPerTouchedKB(t *testing.T) {
	a, w, idle := &ledgerCaller{}, &ledgerCaller{failApp: 1}, &ledgerCaller{}
	b := newTestBudget(map[string]*ledgerCaller{"kb-a": a, "work-kb": w, "idle-kb": idle})
	b.Begin([]string{"kb-a", "work-kb", "idle-kb"}, testNow)
	b.Charge("kb-a", "cloud", 120)
	b.Charge("kb-a", "local", 30)
	b.Note("kb-a", 2, 1)
	b.Touch("work-kb")
	if failed := b.Close(StoppedDone, "did two\nthings"); len(failed) != 0 {
		t.Fatalf("failed = %v", failed)
	}
	if len(idle.appends) != 0 || len(w.appends) != 1 {
		t.Fatalf("idle=%d work=%d (a failed append is retried once)", len(idle.appends), len(w.appends))
	}
	got := a.appends[0]
	want := fmt.Sprintf("run %s day=2026-10-29 tokens.cloud=120 tokens.local=30 units=2 commits=1 stopped=done — did two things", b.RunID())
	if got["entry"] != want || got["path"] != "worker/docs-night" {
		t.Fatalf("append = %v\nwant entry %s", got, want)
	}
	if e := w.appends[0]["entry"].(string); strings.Contains(e, "tokens.") {
		t.Fatalf("a profile that spent nothing must not appear: %s", e)
	}
	// What Close writes, Begin reads back (with the server's prefix).
	next := newTestBudget(map[string]*ledgerCaller{"kb-a": {entries: []string{"[worker/docs-night] " + got["entry"].(string)}}})
	next.Begin([]string{"kb-a"}, testNow)
	if next.Remaining("cloud") != 880 || next.Remaining("local") != 99970 {
		t.Fatalf("round trip: %d %d", next.Remaining("cloud"), next.Remaining("local"))
	}
}

func TestBudgetCloseLogsAfterTwoFailures(t *testing.T) {
	kb := &ledgerCaller{failApp: 5}
	b := newTestBudget(map[string]*ledgerCaller{"kb-a": kb})
	var logged []string
	b.Logf = func(f string, a ...any) { logged = append(logged, fmt.Sprintf(f, a...)) }
	b.Begin([]string{"kb-a"}, testNow)
	b.Touch("kb-a")
	if failed := b.Close(StoppedError, ""); len(failed) != 1 || len(logged) != 1 {
		t.Fatalf("failed=%v logged=%v", failed, logged)
	}
}

func TestBudgetEntryHasNoSecret(t *testing.T) {
	t.Setenv("CLOUD_API_KEY", cfgCanary+"-key")
	kb := &ledgerCaller{}
	b := newTestBudget(map[string]*ledgerCaller{"kb-a": kb})
	b.Begin([]string{"kb-a"}, testNow)
	b.Charge("kb-a", "cloud", 1)
	b.Close(StoppedDone, "summary")
	if strings.Contains(fmt.Sprint(kb.appends), cfgCanary) {
		t.Fatal("entry leaks a secret")
	}
}

func TestBudgetForgedFiguresFailClosed(t *testing.T) {
	huge := "tokens.cloud=9223372036854775807"
	kbs := map[string]*ledgerCaller{
		"kb-a": {entries: []string{
			"[worker/docs-night] run r1 day=2026-10-29 " + huge + " " + huge + " stopped=done",
			"[worker/docs-night] run r2 day=2026-10-29 " + huge + " stopped=done",
			"[worker/docs-night] run r3 day=2026-10-29 tokens.cloud=99999999999999999999 stopped=done",
		}},
		"work-kb": {entries: []string{"[worker/docs-night] run r4 day=2026-10-29 " + huge + " stopped=done"}},
	}
	b := newTestBudget(kbs)
	if err := b.Begin([]string{"kb-a", "work-kb"}, testNow); err != nil {
		t.Fatal(err)
	}
	if r, g := b.Remaining("cloud"), b.Grant("cloud"); r != 0 || g > 0 {
		t.Fatalf("poisoned ledger read as free: remaining=%d grant=%d", r, g)
	}
	if b.Grant("local") <= 0 {
		t.Fatal("another profile must be unaffected")
	}
}

func TestBudgetOldestJudgedByDateNotPosition(t *testing.T) {
	// The last entry of a full answer is not necessarily the oldest (a legacy
	// directory log is returned before the root entries): a yesterday entry
	// anywhere in it ends the read.
	today := "[worker/docs-night] run t day=2026-10-29 tokens.cloud=1 stopped=done"
	old := "[worker/docs-night] run o day=2026-10-28 tokens.cloud=1 stopped=done"
	var entries []string
	for i := 0; i < ledgerStart; i++ {
		entries = append(entries, today)
	}
	entries[10] = old
	kb := &ledgerCaller{entries: entries}
	b := newTestBudget(map[string]*ledgerCaller{"kb-a": kb})
	if err := b.Begin([]string{"kb-a"}, testNow); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(kb.tails) != "[100]" {
		t.Fatalf("tails = %v: a dated-before-today entry should stop the read", kb.tails)
	}
}
