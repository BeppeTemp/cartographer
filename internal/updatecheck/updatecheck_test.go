package updatecheck

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Every test points the cache at t.TempDir() and the API at an httptest
// server: none touches the real network or the real user cache directory.

type fakeGitHub struct {
	srv   *httptest.Server
	hits  atomic.Int32
	code  int
	body  string
	etag  string
	gotIf atomic.Value
	auth  atomic.Value
}

func newFakeGitHub(t *testing.T, code int, body, etag string) *fakeGitHub {
	t.Helper()
	f := &fakeGitHub{code: code, body: body, etag: etag}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.hits.Add(1)
		if r.URL.Path != "/releases" || r.URL.Query().Get("per_page") != "10" {
			t.Errorf("unexpected request %s", r.URL)
		}
		f.gotIf.Store(r.Header.Get("If-None-Match"))
		f.auth.Store(r.Header.Get("Authorization"))
		if f.etag != "" && r.Header.Get("If-None-Match") == f.etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		if f.etag != "" {
			w.Header().Set("ETag", f.etag)
		}
		w.WriteHeader(f.code)
		w.Write([]byte(f.body))
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func opts(t *testing.T, f *fakeGitHub, cache string, now time.Time) Options {
	env := map[string]string{}
	return Options{
		CacheFile: cache,
		APIURL:    f.srv.URL,
		Now:       func() time.Time { return now },
		Getenv:    func(k string) string { return env[k] },
	}
}

func TestCheckFreshFetchWritesCache(t *testing.T) {
	f := newFakeGitHub(t, 200, `[{"tag_name":"v0.17.0"},{"tag_name":"v0.16.9"}]`, `"abc"`)
	cache := filepath.Join(t.TempDir(), "sub", CacheFileName)
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	res, err := Check(context.Background(), "v0.16.1", opts(t, f, cache, now))
	if err != nil {
		t.Fatal(err)
	}
	if !res.Available || res.Latest != "v0.17.0" || res.Kind != KindMinor {
		t.Fatalf("result = %+v", res)
	}
	e, ok := readCache(cache)
	if !ok || e.Latest != "v0.17.0" || e.ETag != `"abc"` || !e.CheckedAt.Equal(now) {
		t.Fatalf("cache = %+v ok=%v", e, ok)
	}
}

func TestCheckTTLBoundary(t *testing.T) {
	f := newFakeGitHub(t, 200, `[{"tag_name":"v0.17.0"}]`, "")
	cache := filepath.Join(t.TempDir(), CacheFileName)
	t0 := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	writeCache(cache, cacheEntry{CheckedAt: t0, Latest: "v0.16.2"})

	// Just inside the TTL: answered from the cache, no request.
	res, _ := Check(context.Background(), "v0.16.1", opts(t, f, cache, t0.Add(TTL-time.Second)))
	if f.hits.Load() != 0 || res.Latest != "v0.16.2" || res.Kind != KindPatch {
		t.Fatalf("warm cache hit the network (%d) or answered %+v", f.hits.Load(), res)
	}
	// At the TTL: refreshed.
	res, _ = Check(context.Background(), "v0.16.1", opts(t, f, cache, t0.Add(TTL)))
	if f.hits.Load() != 1 || res.Latest != "v0.17.0" {
		t.Fatalf("stale cache not refreshed (%d): %+v", f.hits.Load(), res)
	}
}

func TestCheckForceIgnoresTTLAndCacheOnlyNeverFetches(t *testing.T) {
	f := newFakeGitHub(t, 200, `[{"tag_name":"v0.17.0"}]`, "")
	cache := filepath.Join(t.TempDir(), CacheFileName)
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	writeCache(cache, cacheEntry{CheckedAt: now.Add(-48 * time.Hour), Latest: "v0.16.2"})

	o := opts(t, f, cache, now)
	o.CacheOnly = true
	if res, _ := Check(context.Background(), "v0.16.1", o); f.hits.Load() != 0 || res.Latest != "v0.16.2" {
		t.Fatalf("cache-only fetched (%d) or answered %+v", f.hits.Load(), res)
	}
	writeCache(cache, cacheEntry{CheckedAt: now, Latest: "v0.16.2"})
	o = opts(t, f, cache, now)
	o.Force = true
	if res, _ := Check(context.Background(), "v0.16.1", o); f.hits.Load() != 1 || res.Latest != "v0.17.0" {
		t.Fatalf("forced check did not refresh (%d): %+v", f.hits.Load(), res)
	}
}

func TestCheck304RefreshesTimestampOnly(t *testing.T) {
	f := newFakeGitHub(t, 200, `[{"tag_name":"v9.9.9"}]`, `"e1"`)
	cache := filepath.Join(t.TempDir(), CacheFileName)
	t0 := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	writeCache(cache, cacheEntry{CheckedAt: t0, Latest: "v0.16.2", ETag: `"e1"`})
	now := t0.Add(30 * time.Hour)
	res, err := Check(context.Background(), "v0.16.1", opts(t, f, cache, now))
	if err != nil {
		t.Fatal(err)
	}
	if got := f.gotIf.Load(); got != `"e1"` {
		t.Fatalf("If-None-Match = %v", got)
	}
	if res.Latest != "v0.16.2" {
		t.Fatalf("304 changed the tag: %+v", res)
	}
	if e, _ := readCache(cache); !e.CheckedAt.Equal(now) || e.Latest != "v0.16.2" {
		t.Fatalf("304 did not refresh checked_at: %+v", e)
	}
}

func TestCheck500WithAndWithoutStaleCache(t *testing.T) {
	f := newFakeGitHub(t, 500, `boom`, "")
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

	empty := filepath.Join(t.TempDir(), CacheFileName)
	res, err := Check(context.Background(), "v0.16.1", opts(t, f, empty, now))
	if err == nil || res.Available || res.Latest != "" {
		t.Fatalf("500 without cache: %+v %v", res, err)
	}
	if _, err := os.Stat(empty); !os.IsNotExist(err) {
		t.Fatal("a failed fetch wrote a cache")
	}

	stale := filepath.Join(t.TempDir(), CacheFileName)
	writeCache(stale, cacheEntry{CheckedAt: now.Add(-72 * time.Hour), Latest: "v0.16.3"})
	res, err = Check(context.Background(), "v0.16.1", opts(t, f, stale, now))
	if err == nil || !res.Available || res.Latest != "v0.16.3" {
		t.Fatalf("500 with stale cache: %+v %v", res, err)
	}
}

func TestCheckMalformedBodyAndCorruptCache(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	for _, body := range []string{`{"not":"a list"}`, `[]`, `[{"tag_name":""}]`, `<html>`} {
		f := newFakeGitHub(t, 200, body, "")
		cache := filepath.Join(t.TempDir(), CacheFileName)
		if err := os.WriteFile(cache, []byte("{corrupt"), 0o644); err != nil {
			t.Fatal(err)
		}
		res, err := Check(context.Background(), "v0.16.1", opts(t, f, cache, now))
		if err == nil || res.Available {
			t.Errorf("body %q: %+v %v", body, res, err)
		}
		if f.hits.Load() != 1 {
			t.Errorf("a corrupt cache must be treated as absent (hits=%d)", f.hits.Load())
		}
	}
}

func TestCheckSendsGitHubToken(t *testing.T) {
	f := newFakeGitHub(t, 200, `[{"tag_name":"v0.17.0"}]`, "")
	o := opts(t, f, filepath.Join(t.TempDir(), CacheFileName), time.Now())
	o.Getenv = func(k string) string {
		if k == "GITHUB_TOKEN" {
			return "tok"
		}
		return ""
	}
	if _, err := Check(context.Background(), "v0.16.1", o); err != nil {
		t.Fatal(err)
	}
	if got := f.auth.Load(); got != "Bearer tok" {
		t.Fatalf("Authorization = %v", got)
	}
}

func TestCheckDevAndOptOutNeverFetch(t *testing.T) {
	f := newFakeGitHub(t, 200, `[{"tag_name":"v0.17.0"}]`, "")
	cache := filepath.Join(t.TempDir(), CacheFileName)
	for _, cur := range []string{"dev", ""} {
		if res, err := Check(context.Background(), cur, opts(t, f, cache, time.Now())); err != nil || res.Available {
			t.Errorf("%q: %+v %v", cur, res, err)
		}
	}
	o := opts(t, f, cache, time.Now())
	o.Getenv = func(k string) string {
		if k == EnvDisable {
			return "1"
		}
		return ""
	}
	if _, err := Check(context.Background(), "v0.16.1", o); !errors.Is(err, ErrDisabled) {
		t.Errorf("env opt-out: %v", err)
	}
	o = opts(t, f, cache, time.Now())
	o.Disabled, o.Force = true, true
	if _, err := Check(context.Background(), "v0.16.1", o); !errors.Is(err, ErrDisabled) {
		t.Errorf("config opt-out (forced): %v", err)
	}
	if f.hits.Load() != 0 {
		t.Fatalf("dev/opt-out reached the network %d time(s)", f.hits.Load())
	}
}

func TestCompare(t *testing.T) {
	cases := []struct {
		current, latest string
		avail           bool
		kind            string
	}{
		{"v0.16.1", "v0.17.0", true, KindMinor},
		{"v0.16.1", "v0.16.2", true, KindPatch},
		{"0.16.1", "v1.0.0", true, KindMajor},
		{"v0.99.0", "v1.0.0-rc.1", true, KindMajor},
		{"v1.0.0-rc.1", "v0.99.0", false, ""},
		{"v1.0.0-rc.1", "v1.0.0", true, KindMinor},
		{"v1.0.0-rc.2", "v1.0.0-rc.10", true, KindMinor},
		{"v0.16.1", "v0.16.1", false, ""},
		{"v0.17.0", "v0.16.9", false, ""},
		{"v0.16.1", "garbage", false, ""},
		{"dev", "v0.16.1", false, ""},
		// A `git describe` build after a tag is newer than that tag (#1171).
		{"v0.25.1-110-gabc1234", "v0.25.1", false, ""},
		{"v0.25.1-110-gabc1234-dirty", "v0.25.1", false, ""},
		{"v0.25.1-110-gabc1234", "v0.25.2", true, KindPatch},
		{"v0.25.1-110-gabc1234", "v0.26.0", true, KindMinor},
		{"v1.0.0-rc.1-5-gabc1234", "v1.0.0-rc.1", false, ""},
		{"v1.0.0-rc.1-5-gabc1234", "v1.0.0-rc.2", true, KindMinor},
		// What a release candidate binary reports (D1192): up to date while the
		// newest stable is older, a minor update once its release is out.
		{"v0.28.0-rc.20261111", "v0.27.0", false, ""},
		{"v0.28.0-rc.20261111", "v0.28.0", true, KindMinor},
	}
	for _, c := range cases {
		avail, kind := Compare(c.current, c.latest)
		if avail != c.avail || kind != c.kind {
			t.Errorf("Compare(%q, %q) = %v %q, want %v %q", c.current, c.latest, avail, kind, c.avail, c.kind)
		}
	}
}

// D320: a fresh cache that only knows the running version is re-checked, so a
// release published inside the TTL window is not hidden for the rest of it.
func TestForceRefreshOnVersionBypassesTTL(t *testing.T) {
	f := newFakeGitHub(t, 200, `[{"tag_name":"v0.17.0"}]`, "")
	cache := filepath.Join(t.TempDir(), CacheFileName)
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	writeCache(cache, cacheEntry{CheckedAt: now, Latest: "v0.16.1"})

	o := opts(t, f, cache, now)
	o.ForceRefreshOnVersion = "v0.16.1"
	res, _ := Check(context.Background(), "v0.16.1", o)
	if f.hits.Load() != 1 || res.Latest != "v0.17.0" || !res.Available {
		t.Fatalf("same-version cache not refreshed (%d hits): %+v", f.hits.Load(), res)
	}

	// CacheOnly still never fetches, whatever the version.
	writeCache(cache, cacheEntry{CheckedAt: now, Latest: "v0.16.1"})
	o.CacheOnly = true
	if _, _ = Check(context.Background(), "v0.16.1", o); f.hits.Load() != 1 {
		t.Fatalf("cache-only fetched: %d hits", f.hits.Load())
	}
}

func TestForceRefreshNotTriggeredWhenDifferent(t *testing.T) {
	f := newFakeGitHub(t, 200, `[{"tag_name":"v0.18.0"}]`, "")
	cache := filepath.Join(t.TempDir(), CacheFileName)
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	writeCache(cache, cacheEntry{CheckedAt: now, Latest: "v0.17.0"})

	o := opts(t, f, cache, now)
	o.ForceRefreshOnVersion = "v0.16.1"
	res, _ := Check(context.Background(), "v0.16.1", o)
	if f.hits.Load() != 0 || res.Latest != "v0.17.0" {
		t.Fatalf("a cache naming a newer release must be trusted (%d hits): %+v", f.hits.Load(), res)
	}
}

// A tag from the network ends up in an upgrade command the user runs
// (Channel.UpgradeCommand): the shapes that would run something are refused at
// the source, so no caller ever sees them as Latest (D862).
func TestValidTag(t *testing.T) {
	for _, tag := range []string{"v0.17.0", "0.17.0", "v1.0.0-rc.1", "v10.20.30-beta-2.x", "v1.2.3-0"} {
		if !ValidTag(tag) {
			t.Errorf("ValidTag(%q) = false, want true", tag)
		}
	}
	for _, tag := range []string{
		"", "dev", "v1", "v1.2", "v1.2.3.4", "v1.2.3-", "v1.2.3+build",
		"v9.9.9-$(id)", "v9.9.9-`id`", "v9.9.9; rm -rf ~", "v9.9.9-a b", "v9.9.9\n", "v9.9.9-é", "v9.9.9|sh", "v9.9.9&&id",
	} {
		if ValidTag(tag) {
			t.Errorf("ValidTag(%q) = true, want false", tag)
		}
	}
}

func TestCheckRefusesAHostileTag(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	f := newFakeGitHub(t, 200, `[{"tag_name":"v9.9.9-$(id)"}]`, "")
	cache := filepath.Join(t.TempDir(), CacheFileName)
	res, err := Check(context.Background(), "v0.16.1", opts(t, f, cache, now))
	if err == nil || res.Available || res.Latest != "" {
		t.Fatalf("a hostile tag must produce no latest and no update: %+v %v", res, err)
	}
	if _, statErr := os.Stat(cache); statErr == nil {
		t.Error("a hostile tag must not be cached")
	}
}

func TestCheckIgnoresAHostileTagInTheCache(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	f := newFakeGitHub(t, 200, `[{"tag_name":"v0.17.0"}]`, "")
	cache := filepath.Join(t.TempDir(), CacheFileName)
	hostile := `{"checked_at":"` + now.Add(-time.Minute).Format(time.RFC3339) + `","latest":"v9.9.9-$(id)"}`
	if err := os.WriteFile(cache, []byte(hostile), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := Check(context.Background(), "v0.16.1", opts(t, f, cache, now))
	if err != nil || res.Latest != "v0.17.0" || f.hits.Load() != 1 {
		t.Fatalf("a hostile cached tag must count as no cache: %+v %v hits=%d", res, err, f.hits.Load())
	}
}

// A token with write access lists drafts first, and a stray tag can sit at the
// head: the check answers with the newest release that is public and has a
// version tag, and caches it, instead of going dark (D862).
func TestCheckSkipsDraftsAndTagsThatAreNotVersions(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	f := newFakeGitHub(t, 200, `[{"tag_name":"v9.9.9","draft":true},{"tag_name":"v8.8.8-$(id)"},{"tag_name":"v1.2.3"},{"tag_name":"v1.2.2"}]`, `"e1"`)
	cache := filepath.Join(t.TempDir(), CacheFileName)
	res, err := Check(context.Background(), "v1.0.0", opts(t, f, cache, now))
	if err != nil || res.Latest != "v1.2.3" || !res.Available {
		t.Fatalf("Check = %+v, %v; want latest v1.2.3", res, err)
	}
	e, ok := readCache(cache)
	if !ok || e.Latest != "v1.2.3" || e.ETag != `"e1"` {
		t.Errorf("cache = %+v ok=%v, want v1.2.3 with its ETag", e, ok)
	}
	// Nothing usable in the list is still an error and writes no cache.
	g := newFakeGitHub(t, 200, `[{"tag_name":"v9.9.9","draft":true},{"tag_name":"nightly"}]`, "")
	cache2 := filepath.Join(t.TempDir(), CacheFileName)
	if res, err := Check(context.Background(), "v1.0.0", opts(t, g, cache2, now)); err == nil || res.Latest != "" {
		t.Errorf("a list with no usable release must be an error: %+v %v", res, err)
	}
	if _, statErr := os.Stat(cache2); statErr == nil {
		t.Error("no usable release must not be cached")
	}
}

func TestHasPrereleaseSuffix(t *testing.T) {
	for _, tag := range []string{"v0.28.0-rc.20261111", "v1.0.0-rc.1", "v1.0.0-beta.2", "1.2.3-0"} {
		if !HasPrereleaseSuffix(tag) {
			t.Errorf("HasPrereleaseSuffix(%q) = false, want true", tag)
		}
	}
	for _, tag := range []string{"v0.27.0", "0.27.0", "v10.20.30", "", "nightly", "v9.9.9-$(id)"} {
		if HasPrereleaseSuffix(tag) {
			t.Errorf("HasPrereleaseSuffix(%q) = true, want false", tag)
		}
	}
}

// A release candidate is a GitHub pre-release with a suffixed tag (D1192): the
// check never offers it, judged by the tag and never by the `prerelease` flag,
// which every stable 0.x release carries (D82, D211).
func TestCheckSkipsReleaseCandidates(t *testing.T) {
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	f := newFakeGitHub(t, 200, `[{"tag_name":"v0.28.0-rc.20261111","prerelease":true},{"tag_name":"v0.27.0","prerelease":true}]`, "")
	res, err := Check(context.Background(), "v0.26.0", opts(t, f, filepath.Join(t.TempDir(), CacheFileName), now))
	if err != nil || res.Latest != "v0.27.0" || !res.Available {
		t.Fatalf("Check = %+v, %v; want latest v0.27.0, the candidate skipped and the flagged stable kept", res, err)
	}
	// A candidate binary is not offered its own candidate, nor another one.
	res, err = Check(context.Background(), "v0.28.0-rc.20261111", opts(t, f, filepath.Join(t.TempDir(), CacheFileName), now))
	if err != nil || res.Available {
		t.Errorf("Check from a candidate = %+v, %v; want up to date against v0.27.0", res, err)
	}
	g := newFakeGitHub(t, 200, `[{"tag_name":"v0.28.0-rc.20261111"},{"tag_name":"v0.28.0-rc.20261110"}]`, "")
	cache := filepath.Join(t.TempDir(), CacheFileName)
	if res, err := Check(context.Background(), "v0.27.0", opts(t, g, cache, now)); err == nil || res.Latest != "" ||
		!strings.Contains(err.Error(), "no release with a version tag") {
		t.Errorf("a list of candidates only must be the no-release error: %+v %v", res, err)
	}
	// A cached candidate (none is ever written) counts as no cache.
	writeCache(cache, cacheEntry{CheckedAt: now, Latest: "v0.28.0-rc.20261111"})
	if _, ok := readCache(cache); ok {
		t.Error("a cached candidate must be rejected")
	}
}
