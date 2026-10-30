package mcpserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/auth"
)

// newAtlasWorkerFixture adds a narrowed token ("narrow": kb-a, map visible only) to the
// worker fixture, which has "worker-ab" for kb-a and kb-b.
func newAtlasWorkerFixture(t *testing.T) *workerFixture {
	t.Helper()
	f := newWorkerFixture(t)
	f.multi.EnableWeb(nil)
	ts := auth.NewScopedTokenStore([]auth.ScopedToken{
		{Token: "worker-a", Principal: "worker-a", Policy: auth.Policy{Permissions: []auth.Permission{{KB: "kb-a", Write: true}}}},
		{Token: "worker-ab", Principal: "worker-ab", Policy: auth.Policy{Permissions: []auth.Permission{{KB: "kb-a", Write: true}, {KB: "kb-b", Write: true}}}},
		{Token: "reader", Principal: "reader", Policy: auth.Policy{Permissions: []auth.Permission{{KB: "kb-a"}}}},
		{Token: "narrow", Principal: "narrow", Policy: auth.Policy{Permissions: []auth.Permission{{KB: "kb-a", Maps: []string{"visible"}}}}},
		{Token: "other", Principal: "other", Policy: auth.Policy{Permissions: []auth.Permission{{KB: "kb-b"}}}},
	})
	f.h = ts.Middleware(f.multi.Handler())
	return f
}

func (f *workerFixture) get(token, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, UIAPIPrefix+path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	f.h.ServeHTTP(rr, req)
	return rr
}

func workerNames(t *testing.T, rr *httptest.ResponseRecorder) []string {
	t.Helper()
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	var ans struct {
		Workers []struct {
			Worker string `json:"worker"`
		} `json:"workers"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &ans); err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, w := range ans.Workers {
		out = append(out, w.Worker)
	}
	return out
}

func TestUIWorkers_ShowsLiveWorkersOfTheKB(t *testing.T) {
	t.Parallel()
	f := newAtlasWorkerFixture(t)
	if got := workerNames(t, f.get("reader", "/kbs/kb-a/workers")); len(got) != 0 {
		t.Fatalf("no worker declared, got %v", got)
	}
	if rr := f.get("reader", "/kbs/kb-a/workers"); !strings.Contains(rr.Body.String(), `"workers":[]`) {
		t.Fatalf("empty must be [], got %s", rr.Body.String())
	}
	f.post(t, "worker-ab", beat(map[string]any{"worker": "w-2", "kbs": []string{"kb-a", "kb-b"}, "budgets": []map[string]any{{"profile": "cloud", "tier": "large", "spent": 5, "limit": 5}}}))
	f.post(t, "worker-a", beat(map[string]any{"worker": "w-1"}))
	if got := workerNames(t, f.get("reader", "/kbs/kb-a/workers")); strings.Join(got, ",") != "w-1,w-2" {
		t.Fatalf("kb-a workers = %v", got)
	}
	if got := workerNames(t, f.get("other", "/kbs/kb-b/workers")); strings.Join(got, ",") != "w-2" {
		t.Fatalf("a worker declaring two KBs must show on both: %v", got)
	}
	body := f.get("reader", "/kbs/kb-a/workers").Body.String()
	if strings.Contains(body, "worker-a") || strings.Contains(body, "principal") || !strings.Contains(body, `"spent":5`) {
		t.Fatalf("principal leaked or budget missing: %s", body)
	}
	// A stale worker is absent.
	f.clock.advance(time.Hour)
	if got := workerNames(t, f.get("reader", "/kbs/kb-a/workers")); len(got) != 0 {
		t.Fatalf("stale workers shown: %v", got)
	}
}

func TestUIWorkers_Visibility(t *testing.T) {
	t.Parallel()
	f := newAtlasWorkerFixture(t)
	f.post(t, "worker-a", beat(nil))
	for _, c := range []struct{ token, path string }{
		{"narrow", "/kbs/kb-a/workers"}, // does not see the whole KB
		{"other", "/kbs/kb-a/workers"},  // cannot see the KB
		{"reader", "/kbs/nope/workers"},
	} {
		if rr := f.get(c.token, c.path); rr.Code != http.StatusNotFound {
			t.Errorf("%s %s = %d, want 404", c.token, c.path, rr.Code)
		}
	}
	req := httptest.NewRequest(http.MethodPost, UIAPIPrefix+"/kbs/kb-a/workers", nil)
	req.Header.Set("Authorization", "Bearer reader")
	rr := httptest.NewRecorder()
	f.h.ServeHTTP(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST = %d, want 405", rr.Code)
	}
}

func writeConcept(t *testing.T, f *workerFixture, rel, content string) {
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
	f := newAtlasWorkerFixture(t)
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
	f := newAtlasWorkerFixture(t)
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
