package mcpserver

import (
	"strings"
	"testing"
	"time"
)

func TestClosestLineHint_LongFirstLineGivesNoHint(t *testing.T) {
	t.Parallel()
	body := strings.Repeat(strings.Repeat("x", 400)+"\n", 500)
	old := strings.Repeat("x", 10000)
	start := time.Now()
	if got := closestLineHint(body, old); got != "" {
		t.Fatalf("want no hint for a first line over the limit, got %q", got)
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("took %v", d)
	}
}

func TestClosestLineHint_NearLineWithinLimitStillFound(t *testing.T) {
	t.Parallel()
	body := "alpha\nthe quick brown fox jumps over the lazy dog\nomega\n"
	got := closestLineHint(body, "the quick brown fox jumps over the lazy dgo")
	if !strings.Contains(got, "closest line 2") {
		t.Fatalf("want line 2, got %q", got)
	}
}

func TestClosestLineHint_LengthDifferenceBeyondLimitSkipped(t *testing.T) {
	t.Parallel()
	if got := closestLineHint(strings.Repeat("a", 100), "ab"); got != "" {
		t.Fatalf("got %q", got)
	}
}
