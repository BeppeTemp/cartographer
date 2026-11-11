package mcpserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/auth"
)

// newAtlasSurveyorFixture adds a narrowed token ("narrow": kb-a, map visible only) to the
// surveyor fixture, which has "surveyor-ab" for kb-a and kb-b.
func newAtlasSurveyorFixture(t *testing.T) *surveyorFixture {
	t.Helper()
	f := newSurveyorFixture(t)
	f.multi.EnableWeb(nil)
	ts := auth.NewScopedTokenStore([]auth.ScopedToken{
		{Token: "surveyor-a", Principal: "surveyor-a", Policy: auth.Policy{Permissions: []auth.Permission{{KB: "kb-a", Write: true}}}},
		{Token: "surveyor-ab", Principal: "surveyor-ab", Policy: auth.Policy{Permissions: []auth.Permission{{KB: "kb-a", Write: true}, {KB: "kb-b", Write: true}}}},
		{Token: "reader", Principal: "reader", Policy: auth.Policy{Permissions: []auth.Permission{{KB: "kb-a"}}}},
		{Token: "narrow", Principal: "narrow", Policy: auth.Policy{Permissions: []auth.Permission{{KB: "kb-a", Maps: []string{"visible"}}}}},
		{Token: "other", Principal: "other", Policy: auth.Policy{Permissions: []auth.Permission{{KB: "kb-b"}}}},
	})
	f.h = ts.Middleware(f.multi.Handler())
	return f
}

func (f *surveyorFixture) get(token, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, UIAPIPrefix+path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	f.h.ServeHTTP(rr, req)
	return rr
}

func surveyorNames(t *testing.T, rr *httptest.ResponseRecorder) []string {
	t.Helper()
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	var ans struct {
		Components []struct {
			Replicas []struct {
				ID string `json:"id"`
			} `json:"replicas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &ans); err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, c := range ans.Components {
		for _, w := range c.Replicas {
			out = append(out, w.ID)
		}
	}
	sort.Strings(out)
	return out
}

func TestUISurveyors_ShowsLiveSurveyorsOfTheKB(t *testing.T) {
	t.Parallel()
	f := newAtlasSurveyorFixture(t)
	if got := surveyorNames(t, f.get("reader", "/kbs/kb-a/components")); len(got) != 0 {
		t.Fatalf("no surveyor declared, got %v", got)
	}
	if rr := f.get("reader", "/kbs/kb-a/components"); !strings.Contains(rr.Body.String(), `"components":[]`) {
		t.Fatalf("empty must be [], got %s", rr.Body.String())
	}
	f.post(t, "surveyor-ab", beat(map[string]any{"surveyor": "w-2", "kbs": []string{"kb-a", "kb-b"}, "budgets": []map[string]any{{"profile": "cloud", "tier": "large", "spent": 5, "limit": 5}}}))
	f.post(t, "surveyor-a", beat(map[string]any{"surveyor": "w-1"}))
	if got := surveyorNames(t, f.get("reader", "/kbs/kb-a/components")); strings.Join(got, ",") != "w-1,w-2" {
		t.Fatalf("kb-a surveyors = %v", got)
	}
	if got := surveyorNames(t, f.get("other", "/kbs/kb-b/components")); strings.Join(got, ",") != "w-2" {
		t.Fatalf("a surveyor declaring two KBs must show on both: %v", got)
	}
	body := f.get("reader", "/kbs/kb-a/components").Body.String()
	if strings.Contains(body, "surveyor-a") || strings.Contains(body, "principal") || !strings.Contains(body, `"spent":5`) {
		t.Fatalf("principal leaked or budget missing: %s", body)
	}
	// A stale surveyor is absent.
	f.clock.advance(time.Hour)
	if got := surveyorNames(t, f.get("reader", "/kbs/kb-a/components")); len(got) != 0 {
		t.Fatalf("stale surveyors shown: %v", got)
	}
}

func TestUISurveyors_Visibility(t *testing.T) {
	t.Parallel()
	f := newAtlasSurveyorFixture(t)
	f.post(t, "surveyor-a", beat(nil))
	for _, c := range []struct{ token, path string }{
		{"narrow", "/kbs/kb-a/components"}, // does not see the whole KB
		{"other", "/kbs/kb-a/components"},  // cannot see the KB
		{"reader", "/kbs/nope/components"},
	} {
		if rr := f.get(c.token, c.path); rr.Code != http.StatusNotFound {
			t.Errorf("%s %s = %d, want 404", c.token, c.path, rr.Code)
		}
	}
	req := httptest.NewRequest(http.MethodPost, UIAPIPrefix+"/kbs/kb-a/components", nil)
	req.Header.Set("Authorization", "Bearer reader")
	rr := httptest.NewRecorder()
	f.h.ServeHTTP(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST = %d, want 405", rr.Code)
	}
}

func writeConcept(t *testing.T, f *surveyorFixture, rel, content string) {
	t.Helper()
	k := f.multi.servers["kb-a"].kbRef
	full := filepath.Join(k.DataRoot(), rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func feedIDs(t *testing.T, rr *httptest.ResponseRecorder) (ids []string, total int, raw string) {
	t.Helper()
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	var ans struct {
		Feeds []struct {
			ID        string `json:"id"`
			FeedError string `json:"feed_error"`
		} `json:"feeds"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &ans); err != nil {
		t.Fatal(err)
	}
	for _, x := range ans.Feeds {
		ids = append(ids, x.ID)
	}
	return ids, ans.Total, rr.Body.String()
}

func TestUIFeeds(t *testing.T) {
	t.Parallel()
	f := newAtlasSurveyorFixture(t)
	ids, total, raw := feedIDs(t, f.get("reader", "/kbs/kb-a/feeds"))
	if len(ids) != 0 || total != 0 || !strings.Contains(raw, `"feeds":[]`) {
		t.Fatalf("no Source: %s", raw)
	}
	src := func(extra string) string { return "---\ntype: Source\ntitle: S\n" + extra + "---\nbody\n" }
	writeConcept(t, f, "visible/ok.md", src("feed_checked_at: 2026-01-01T00:00:00Z\n"))
	writeConcept(t, f, "visible/old.md", src("feed_error: \"timeout\"\nfeed_checked_at: 2026-01-01T00:00:00Z\n"))
	writeConcept(t, f, "visible/new.md", src("feed_error: \"HTTP 500\"\nfeed_checked_at: 2026-01-02T00:00:00Z\nsource_shape: feed\n"))
	writeConcept(t, f, "visible/badtime.md", src("feed_error: \"x\"\nfeed_checked_at: yesterday\n"))
	writeConcept(t, f, "visible/note.md", "---\ntype: Note\ntitle: N\nfeed_error: \"not a source\"\n---\nb\n")
	writeConcept(t, f, "hidden/secret.md", src("feed_error: \"hidden\"\n"))
	ids, total, raw = feedIDs(t, f.get("reader", "/kbs/kb-a/feeds"))
	if got := strings.Join(ids, ","); got != "visible/new,visible/old,hidden/secret,visible/badtime" || total != 4 {
		t.Fatalf("feeds = %v total %d", ids, total)
	}
	if strings.Contains(raw, "yesterday") {
		t.Fatalf("unparsable feed_checked_at passed on: %s", raw)
	}
	// A narrowed caller sees only the maps it sees.
	ids, total, _ = feedIDs(t, f.get("narrow", "/kbs/kb-a/feeds"))
	if got := strings.Join(ids, ","); got != "visible/new,visible/old,visible/badtime" || total != 3 {
		t.Fatalf("narrow feeds = %v total %d", ids, total)
	}
	if rr := f.get("other", "/kbs/kb-a/feeds"); rr.Code != http.StatusNotFound {
		t.Fatalf("invisible KB = %d", rr.Code)
	}
}

func TestUIFeeds_CutsErrorAndCaps(t *testing.T) {
	t.Parallel()
	f := newAtlasSurveyorFixture(t)
	long := strings.Repeat("e", 400)
	for i := 0; i < maxFeedRows+5; i++ {
		writeConcept(t, f, fmt.Sprintf("visible/f%03d.md", i), "---\ntype: Source\ntitle: S\nfeed_error: \""+long+"\"\n---\nb\n")
	}
	rr := f.get("reader", "/kbs/kb-a/feeds")
	var ans struct {
		Feeds []struct {
			FeedError string `json:"feed_error"`
		} `json:"feeds"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &ans); err != nil {
		t.Fatal(err)
	}
	if len(ans.Feeds) != maxFeedRows || ans.Total != maxFeedRows+5 {
		t.Fatalf("len %d total %d", len(ans.Feeds), ans.Total)
	}
	if n := len([]rune(ans.Feeds[0].FeedError)); n != maxFeedErrLen {
		t.Fatalf("error length %d, want %d", n, maxFeedErrLen)
	}
}

func TestUISurveyors_SameIDFromTwoPrincipalsIsOrdered(t *testing.T) {
	t.Parallel()
	f := newAtlasSurveyorFixture(t)
	f.post(t, "surveyor-ab", beat(map[string]any{"surveyor": "w-1", "perimeter": "team-b"}))
	f.post(t, "surveyor-a", beat(map[string]any{"surveyor": "w-1", "perimeter": "team-a"}))
	for i := 0; i < 20; i++ {
		var got []string
		for _, d := range f.multi.surveyors.live("kb-a") {
			got = append(got, d.Perimeter)
		}
		if strings.Join(got, ",") != "team-a,team-b" {
			t.Fatalf("order = %v", got)
		}
	}
}
