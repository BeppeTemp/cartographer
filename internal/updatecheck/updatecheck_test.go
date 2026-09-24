package updatecheck

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
		if r.URL.Path != "/releases" || r.URL.Query().Get("per_page") != "1" {
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
	}
	for _, c := range cases {
		avail, kind := Compare(c.current, c.latest)
		if avail != c.avail || kind != c.kind {
			t.Errorf("Compare(%q, %q) = %v %q, want %v %q", c.current, c.latest, avail, kind, c.avail, c.kind)
		}
	}
}
