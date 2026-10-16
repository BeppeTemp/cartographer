package lint

import (
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/okf"
)

// stale_claim and reviewSuspended read the same review_after date. Both judge
// it by UTC day: the review day itself is neither past nor suspended-over, the
// claim goes stale the day after.
func TestReviewAfterDayGranularity(t *testing.T) {
	const today = "2026-10-16"
	plus2 := time.FixedZone("UTC+2", 2*60*60)
	minus5 := time.FixedZone("UTC-5", -5*60*60)

	instants := []struct {
		name string
		now  time.Time
	}{
		{"start of day", time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)},
		{"midday", time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)},
		{"end of day", time.Date(2026, 10, 16, 23, 59, 59, 0, time.UTC)},
		// 00:30 on the 17th at UTC+2 is still the 16th in UTC.
		{"local date ahead of UTC", time.Date(2026, 10, 17, 0, 30, 0, 0, plus2)},
		// 21:00 on the 15th at UTC-5 is already the 16th in UTC.
		{"local date behind UTC", time.Date(2026, 10, 15, 21, 0, 0, 0, minus5)},
	}
	reviews := []struct {
		name      string
		date      string
		wantStale bool
	}{
		{"yesterday", "2026-10-15", true},
		{"today", today, false},
		{"tomorrow", "2026-10-17", false},
	}

	k := tempKB(t)
	prev := Now
	t.Cleanup(func() { Now = prev })

	for _, at := range instants {
		for _, r := range reviews {
			t.Run(at.name+"/"+r.name, func(t *testing.T) {
				Now = func() time.Time { return at.now }
				content := "---\ntype: Note\ntitle: Page\nreview_after: " + r.date + "\n---\n# Page\n"

				if got := hasCheck(CheckConcept(k, "notes/page", content), "notes/page.md", "stale_claim"); got != r.wantStale {
					t.Errorf("stale_claim at %s with review_after %s = %v, want %v", at.now.Format(time.RFC3339), r.date, got, r.wantStale)
				}

				fm, err := okf.ParseFrontmatter("type: Note\nreview_after: " + r.date + "\n")
				if err != nil {
					t.Fatalf("ParseFrontmatter: %v", err)
				}
				if got := reviewSuspended(fm); got != !r.wantStale {
					t.Errorf("reviewSuspended at %s with review_after %s = %v, want %v", at.now.Format(time.RFC3339), r.date, got, !r.wantStale)
				}
			})
		}
	}
}
