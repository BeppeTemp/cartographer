package feed

import (
	"errors"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)

func entry(fields map[string]any) Entry {
	return Entry{ID: "ledger/docs-feed", Fields: fields, ContentHash: "h0"}
}

func TestParseGood(t *testing.T) {
	f, err := Parse(entry(map[string]any{
		"locator": "https://git.example.com/docs.git", "connector": "git", "recipe": "git-docs",
		KeyInterval: "6h", KeyCheckedAt: "2026-10-30T06:00:00Z", KeyInclude: []string{"docs/**/*.md"}, KeyCursor: "abc123",
	}), 24*time.Hour, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if f.Interval != 6*time.Hour || f.Cursor != "abc123" || f.Recipe != "git-docs" || len(f.Include) != 1 || f.ContentHash != "h0" {
		t.Errorf("feed = %+v", f)
	}
	if !f.Due(t0) {
		t.Error("due exactly at checked_at + interval")
	}
	if f.Due(t0.Add(-time.Second)) {
		t.Error("due one second early")
	}
}

func TestParseDefaultsAndNeverChecked(t *testing.T) {
	f, err := Parse(entry(map[string]any{"recipe": "git-docs"}), 24*time.Hour, time.Hour)
	if err != nil || f.Interval != 24*time.Hour || !f.Due(t0) {
		t.Fatalf("feed = %+v, %v", f, err)
	}
}

func TestParseErrorsAreBadFieldNotPanics(t *testing.T) {
	for name, fields := range map[string]map[string]any{
		"interval text":      {KeyInterval: "often"},
		"interval type":      {KeyInterval: []string{"1h"}},
		"interval below min": {KeyInterval: "10m"},
		"date":               {KeyCheckedAt: "yesterday"},
		"date type":          {KeyCheckedAt: []string{"x"}},
		"include type":       {KeyInclude: 3},
		"include empty glob": {KeyInclude: []string{"docs/**", " "}},
		"cursor too long":    {KeyCursor: strings.Repeat("x", MaxCursorBytes+1)},
		"cursor type":        {KeyCursor: []string{"x"}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Parse(entry(fields), 24*time.Hour, time.Hour)
			var fe *Error
			if !errors.As(err, &fe) || fe.Code != CodeBadField {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestBuildersAlwaysCarryIfMatch(t *testing.T) {
	f := Feed{ID: "ledger/docs-feed", ContentHash: "h0"}
	lease, err1 := f.Lease(t0)
	adv, err2 := f.Advance("h1", "cursor-2", t0)
	fail, err3 := f.Fail("h1", "list_failed", "boom\nsecond line", t0)
	if err1 != nil || err2 != nil || err3 != nil {
		t.Fatal(err1, err2, err3)
	}
	for name, a := range map[string]map[string]any{"lease": lease, "advance": adv, "fail": fail} {
		if a["if_match"] == "" || a["if_match"] == nil || a["id"] != "ledger/docs-feed" {
			t.Errorf("%s: %v", name, a)
		}
		if _, ok := a["frontmatter"].(map[string]any)[KeyCheckedAt]; !ok {
			t.Errorf("%s: no %s", name, KeyCheckedAt)
		}
	}
	if lease["if_match"] != "h0" || adv["if_match"] != "h1" {
		t.Errorf("hashes: %v %v", lease["if_match"], adv["if_match"])
	}
	if _, ok := lease["frontmatter"].(map[string]any)[KeyCursor]; ok {
		t.Error("the lease moved the cursor")
	}
	if u, _ := adv["unset"].([]string); len(u) != 1 || u[0] != KeyError {
		t.Errorf("advance unset = %v", adv["unset"])
	}
	if _, ok := fail["frontmatter"].(map[string]any)[KeyCursor]; ok {
		t.Error("fail moved the cursor")
	}
	e := fail["frontmatter"].(map[string]any)[KeyError].(string)
	if e != "list_failed: boom second line" {
		t.Errorf("feed_error = %q", e)
	}
}

func TestFailDetailIsCutOnARuneBoundary(t *testing.T) {
	long := strings.Repeat("é", 400)
	a, _ := Feed{ID: "x"}.Fail("h", "list_failed", long, t0)
	e := a["frontmatter"].(map[string]any)[KeyError].(string)
	if len(e) > MaxErrorBytes || !strings.HasPrefix(e, "list_failed: é") {
		t.Errorf("len %d: %q", len(e), e)
	}
}

func TestOutcome(t *testing.T) {
	if Outcome("") != nil {
		t.Error("success")
	}
	if !errors.Is(Outcome("stale_write: if_match does not match"), ErrLeaseLost) {
		t.Error("stale is not ErrLeaseLost")
	}
	if err := Outcome("not_found: x"); err == nil || errors.Is(err, ErrLeaseLost) {
		t.Errorf("other = %v", err)
	}
}

func TestQueryFieldsFitTheConceptListCap(t *testing.T) {
	if len(QueryFields) > 8 {
		t.Errorf("%d fields, concept_list takes 8", len(QueryFields))
	}
}

func TestWritesRefuseAnEmptyIfMatchAndAnOverlongCursor(t *testing.T) {
	f := Feed{ID: "x"} // no ContentHash: a Lease has nothing to compare against
	if _, err := f.Lease(t0); err == nil {
		t.Error("Lease without a hash")
	}
	if _, err := f.Advance("", "c", t0); err == nil {
		t.Error("Advance with an empty if_match")
	}
	if _, err := f.Fail("", "list_failed", "x", t0); err == nil {
		t.Error("Fail with an empty if_match")
	}
	if _, err := f.Advance("h", strings.Repeat("x", MaxCursorBytes+1), t0); err == nil {
		t.Error("Advance with an over-long cursor")
	}
	if _, err := f.Advance("h", strings.Repeat("x", MaxCursorBytes), t0); err != nil {
		t.Errorf("a cursor at the limit: %v", err)
	}
}

func TestParseIntervalMustBePositiveWhateverTheMinimum(t *testing.T) {
	for _, v := range []string{"0s", "-1h"} {
		if _, err := Parse(entry(map[string]any{KeyInterval: v}), time.Hour, 0); err == nil {
			t.Errorf("feed_interval %s accepted", v)
		}
	}
}
