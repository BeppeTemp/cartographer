package mcpserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/lint"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

func sampleAt(at time.Time, total int) lintSample {
	return lintSample{At: at.UTC().Format(time.RFC3339), Total: total,
		BySeverity: map[string]int{"warning": total}, ByHandler: map[string]int{"doctor": total}}
}

// One sample per UTC day, the latest of the day wins; older than 90 days goes.
func TestLintHistoryOnePerDayAndRetention(t *testing.T) {
	k := cacheTestKB(t)
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	for _, s := range []lintSample{
		sampleAt(now.Add(-100*24*time.Hour), 9), // beyond retention
		sampleAt(now.Add(-2*24*time.Hour), 7),
		sampleAt(now.Add(-time.Hour), 5),
		sampleAt(now, 3), // same day as the previous: replaces it
	} {
		at, _ := time.Parse(time.RFC3339, s.At)
		if err := recordLintSample(k, s, at); err != nil {
			t.Fatal(err)
		}
	}
	got := readLintHistory(k)
	if len(got) != 2 || got[0].Total != 7 || got[1].Total != 3 {
		t.Fatalf("history = %+v", got)
	}
	// A new write prunes what aged out meanwhile.
	later := now.Add(89 * 24 * time.Hour)
	if err := recordLintSample(k, sampleAt(later, 1), later); err != nil {
		t.Fatal(err)
	}
	if got := readLintHistory(k); len(got) != 2 || got[0].Total != 3 || got[1].Total != 1 {
		t.Fatalf("after prune = %+v", got)
	}
}

// The whole-KB lint recompute records a sample, split by severity and handler.
func TestConformanceCacheRecordsLintSample(t *testing.T) {
	k := cacheTestKB(t)
	now := time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)
	cc := &conformanceCache{clock: func() time.Time { return now }}
	findings, err := cc.lintFindings(k)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cc.lintFindings(k); err != nil { // warm hit: no second sample
		t.Fatal(err)
	}
	h := readLintHistory(k)
	if len(h) != 1 || h[0].Total != len(findings) {
		t.Fatalf("history = %+v for %d findings", h, len(findings))
	}
	sev, hand := 0, 0
	for _, n := range h[0].BySeverity {
		sev += n
	}
	for _, n := range h[0].ByHandler {
		hand += n
	}
	if sev != h[0].Total || hand != h[0].Total {
		t.Fatalf("splits do not add up: %+v", h[0])
	}
	// The next day's recompute adds a sample.
	now = now.Add(24 * time.Hour)
	fm, _ := okf.ParseFrontmatter("")
	fm.Set("type", "Note")
	fm.Set("title", "More")
	if _, err := k.WriteConcept("arch/more", fm, "# More\n", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := cc.lintFindings(k); err != nil {
		t.Fatal(err)
	}
	if h := readLintHistory(k); len(h) != 2 {
		t.Fatalf("history after next day = %+v", h)
	}
}

func TestNewLintSampleHandlers(t *testing.T) {
	k := cacheTestKB(t)
	fs := []lint.Finding{{Check: "x", Severity: "warning"}, {Check: "y", Severity: "info"}}
	s := newLintSample(k, fs, time.Now())
	if s.Total != 2 || s.BySeverity["warning"] != 1 || s.BySeverity["info"] != 1 || s.ByHandler["doctor"] != 2 {
		t.Fatalf("sample = %+v", s)
	}
}

func TestLintTrend(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	old := sampleAt(now.Add(-20*24*time.Hour), 300)
	a := sampleAt(now.Add(-6*24*time.Hour), 188)
	b := sampleAt(now.Add(-3*24*time.Hour), 90)
	c := sampleAt(now, 40)
	if lintTrend([]lintSample{old, a}, now) != nil {
		t.Fatal("one sample in the window must give no trend")
	}
	tr := lintTrend([]lintSample{old, a, b, c}, now)
	if tr == nil || tr["delta"] != -148 || tr["samples"] != 3 || tr["first"].(lintSample).Total != 188 {
		t.Fatalf("trend = %+v", tr)
	}
}

// kb_status carries the trend for a whole-KB caller and never for a narrowed one.
func TestKBStatusLintTrend(t *testing.T) {
	k, s := repairKB(t, 1)
	now := time.Now().UTC()
	for _, x := range []lintSample{sampleAt(now.Add(-5*24*time.Hour), 188), sampleAt(now.Add(-2*24*time.Hour), 40)} {
		at, _ := time.Parse(time.RFC3339, x.At)
		if err := recordLintSample(k, x, at); err != nil {
			t.Fatal(err)
		}
	}
	read := func(ctx requestContext) (map[string]any, ToolResult) {
		res := s.callTool(ctx, "kb_status", json.RawMessage(`{}`))
		var out map[string]any
		if !res.IsError {
			if err := json.Unmarshal([]byte(res.Content[0].Text), &out); err != nil {
				t.Fatal(err)
			}
		}
		return out, res
	}
	st, _ := read(authLocalContext())
	tr, _ := st["lint_trend"].(map[string]any)
	if tr == nil {
		t.Fatal("no lint_trend for a whole-KB caller")
	}
	// The status call's own lint adds today's sample after the seeded ones.
	first, last := tr["first"].(map[string]any), tr["last"].(map[string]any)
	if first["total"].(float64) != 188 || tr["delta"].(float64) != last["total"].(float64)-188 {
		t.Fatalf("lint_trend = %v", tr)
	}
	// A narrowed principal gets no trend: kb_status refuses it outright.
	narrow := asPrincipal(auth.Policy{Permissions: []auth.Permission{{KB: kbName(k), Maps: []string{"ops"}}}})
	if st, res := read(narrow); st["lint_trend"] != nil {
		t.Fatalf("a narrowed principal sees the lint trend: %+v", res)
	}
}

// The Health summary carries the history as an array, even when empty.
func TestUIAPI_MaintenanceSummaryLintHistory(t *testing.T) {
	handler := maintenanceHandler(t, auth.NewTokenStore(nil))
	body := decodeUI(t, getUI(t, handler, UIAPIPrefix+"/kbs/docs/maintenance/summary", ""))
	if h, ok := body["lint_history"].([]any); !ok || h == nil {
		t.Fatalf("lint_history = %v", body["lint_history"])
	}
}

// The history never invalidates the lint cache it is written from, but the
// rest of .cartographer does: usage.json is an input of artifact_unused.
func TestLintHistoryOutsideTheStampUsageInside(t *testing.T) {
	k := cacheTestKB(t)
	before := lintInputsStamp(k)
	now := time.Now()
	if err := recordLintSample(k, sampleAt(now, 1), now); err != nil {
		t.Fatal(err)
	}
	if got := lintInputsStamp(k); got != before {
		t.Fatal("writing the lint history changed the lint inputs stamp")
	}
	usage := filepath.Join(k.Root, ".cartographer", "usage.json")
	if err := os.WriteFile(usage, []byte("[]"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := lintInputsStamp(k); got == before {
		t.Fatal("writing usage.json left the lint inputs stamp unchanged")
	}
}
