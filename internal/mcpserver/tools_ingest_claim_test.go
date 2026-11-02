package mcpserver

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/kbtest"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// D1201: the claim protocol of an IngestUnit is documented behavior of
// work_list and concept_patch, pinned here. These tests pin lint.Now, so none
// of them runs in parallel.

func unitPage(status, extra string) string {
	return "---\ntype: IngestUnit\ntitle: U\nsource: sources/s\ntargets: [m/a]\nextract: facts\nverify: check\nexecutor: any\nstatus: " + status + "\n" + extra + "---\n# U\n"
}

func claimServer(t *testing.T, pages map[string]string) (*Server, *kb.KB) {
	t.Helper()
	k := kbtest.New(t)
	k.AuthName = "docs"
	writeKBFile(t, k, "units/_map.md", "---\ntype: Map\ntitle: Units\n---\n")
	for id, c := range pages {
		writeKBFile(t, k, "units/"+id+".md", c)
	}
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	return s, k
}

func unitHash(t *testing.T, k *kb.KB, id string) string {
	t.Helper()
	d, err := k.ReadConcept(okf.ConceptID(id))
	if err != nil {
		t.Fatal(err)
	}
	return d.ContentHash
}

const unitQueue = `{"where":["type=IngestUnit"],"fields":["status","executor","review_after","claimed_by","source","plan"]}`

func TestIngestClaimIsCompareAndSwap(t *testing.T) {
	pinToday(t, time.Date(2031, 2, 3, 12, 0, 0, 0, time.UTC))
	s, k := claimServer(t, map[string]string{"u1": unitPage("open", "")})
	h := unitHash(t, k, "units/u1")
	patch := func(who string) ToolResult {
		b, _ := json.Marshal(map[string]interface{}{"id": "units/u1", "if_match": h, "frontmatter": map[string]interface{}{
			"status": "in-progress", "claimed_by": who, "claimed_at": "2031-02-03T12:00:00Z", "review_after": "2031-02-03"}})
		return callTool(t, s, "concept_patch", string(b))
	}
	if r := patch("worker-a"); r.IsError {
		t.Fatalf("first claim: %s", r.Content[0].Text)
	}
	r := patch("worker-b")
	if !r.IsError || !strings.Contains(r.Content[0].Text, "stale_write") {
		t.Fatalf("second claim = %+v", r)
	}
	d, _ := k.ReadConcept("units/u1")
	fm, _ := okf.ParseFrontmatter(d.FrontmatterRaw)
	if fmString(fm, "claimed_by") != "worker-a" {
		t.Fatalf("claimed_by = %q", fmString(fm, "claimed_by"))
	}
}

func TestIngestQueueAndLeaseExpiry(t *testing.T) {
	pinToday(t, time.Date(2031, 2, 3, 12, 0, 0, 0, time.UTC))
	s, _ := claimServer(t, map[string]string{
		"open":    unitPage("open", ""),
		"expired": unitPage("in-progress", "claimed_by: w\nreview_after: 2031-02-02\n"),
		"leased":  unitPage("in-progress", "claimed_by: w\nreview_after: 2031-02-03\n"),
		"done":    unitPage("done", ""),
		"skipped": unitPage("skipped", ""),
	})
	r := workCall(t, s, unitQueue)
	got := map[string]string{}
	for _, e := range r.Entries {
		if !e.OpenPhase {
			t.Errorf("%s: open_phase false", e.ID)
		}
		// Pins finding 1: an in-progress unit is open by its status, so a
		// passed lease is never a "review" entry (lint/work.go).
		if e.Reason != "" {
			t.Errorf("%s: reason %q, want empty", e.ID, e.Reason)
		}
		got[e.ID] = e.Fields["review_after"]
	}
	if len(got) != 3 || got["units/open"] != "" || got["units/expired"] != "2031-02-02" || got["units/leased"] != "2031-02-03" {
		t.Fatalf("queue = %v", got)
	}
	if _, ok := got["units/done"]; ok {
		t.Error("done unit listed")
	}
	// draft is never queued unless a map names it in open_statuses.
	s2, _ := claimServer(t, map[string]string{"d": unitPage("draft", "")})
	if r := workCall(t, s2, unitQueue); len(r.Entries) != 0 {
		t.Errorf("draft listed: %s", workIDs(r))
	}
}

func TestIngestLeaseIsDayGranular(t *testing.T) {
	// The rule is judged by the same clock work_list uses (lint.Now); the
	// helper above reads time.Now, so the day boundary is checked with the
	// lint side: review_after today suspends, yesterday is overdue.
	for _, c := range []struct {
		name string
		now  time.Time
		want bool
	}{
		{"23:59 on the lease day", time.Date(2031, 2, 3, 23, 59, 0, 0, time.UTC), false},
		{"00:01 the day after", time.Date(2031, 2, 4, 0, 1, 0, 0, time.UTC), true},
	} {
		t.Run(c.name, func(t *testing.T) {
			pinToday(t, c.now)
			s, _ := claimServer(t, map[string]string{"u": unitPage("in-progress", "claimed_by: w\nreview_after: 2031-02-03\n")})
			r := workCall(t, s, unitQueue)
			if len(r.Entries) != 1 {
				t.Fatalf("entries = %s", workIDs(r))
			}
			ra := r.Entries[0].Fields["review_after"]
			day, _ := time.Parse("2006-01-02", ra)
			expired := day.Before(c.now.UTC().Truncate(24 * time.Hour))
			if expired != c.want {
				t.Fatalf("expired = %v, want %v", expired, c.want)
			}
		})
	}
}

func TestIngestReclaimUsesCurrentHash(t *testing.T) {
	pinToday(t, time.Date(2031, 2, 5, 9, 0, 0, 0, time.UTC))
	s, k := claimServer(t, map[string]string{"u": unitPage("in-progress", "claimed_by: old\nclaimed_at: 2031-02-03T09:00:00Z\nreview_after: 2031-02-03\n")})
	pre := unitHash(t, k, "units/u")
	// Another executor renews before our claim: the pre-expiry hash is stale.
	renew, _ := json.Marshal(map[string]interface{}{"id": "units/u", "if_match": pre, "frontmatter": map[string]interface{}{"review_after": "2031-02-04"}})
	if r := callTool(t, s, "concept_patch", string(renew)); r.IsError {
		t.Fatal(r.Content[0].Text)
	}
	claim := func(h string) ToolResult {
		b, _ := json.Marshal(map[string]interface{}{"id": "units/u", "if_match": h, "frontmatter": map[string]interface{}{
			"status": "in-progress", "claimed_by": "new", "claimed_at": "2031-02-05T09:00:00Z", "review_after": "2031-02-05"}})
		return callTool(t, s, "concept_patch", string(b))
	}
	if r := claim(pre); !r.IsError || !strings.Contains(r.Content[0].Text, "stale_write") {
		t.Fatalf("pre-expiry hash = %+v", r)
	}
	if r := claim(unitHash(t, k, "units/u")); r.IsError {
		t.Fatalf("re-claim: %s", r.Content[0].Text)
	}
	d, _ := k.ReadConcept("units/u")
	fm, _ := okf.ParseFrontmatter(d.FrontmatterRaw)
	if fmString(fm, "claimed_by") != "new" {
		t.Fatalf("claimed_by = %q", fmString(fm, "claimed_by"))
	}
}

func TestIngestDoneUnsetsLease(t *testing.T) {
	pinToday(t, time.Date(2031, 2, 5, 9, 0, 0, 0, time.UTC))
	s, k := claimServer(t, map[string]string{"u": unitPage("in-progress", "claimed_by: w\nreview_after: 2031-02-01\n")})
	b, _ := json.Marshal(map[string]interface{}{"id": "units/u", "if_match": unitHash(t, k, "units/u"),
		"frontmatter": map[string]interface{}{"status": "done"}, "unset": []string{"review_after"}})
	if r := callTool(t, s, "concept_patch", string(b)); r.IsError {
		t.Fatal(r.Content[0].Text)
	}
	if r := workCall(t, s, unitQueue); len(r.Entries) != 0 {
		t.Fatalf("done unit listed: %s", workIDs(r))
	}
}
