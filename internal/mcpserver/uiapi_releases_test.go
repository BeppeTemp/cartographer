package mcpserver

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/updatecheck"
)

// releasesFixture mounts a server whose release source is fn (nil leaves it
// unset, the "disabled" case). clock drives the cache.
func releasesFixture(t *testing.T, version, latest, channel string, fn ReleasesSource, clock func() time.Time) http.Handler {
	t.Helper()
	multi := NewMultiKBServer(version)
	if latest != "" {
		multi.SetLatestVersionSource(func() string { return latest })
	}
	multi.SetSettings(nil, channel)
	multi.SetReleasesSource(fn)
	multi.releases.now = clock
	k := uiFixtureKB(t, "docs")
	multi.MountKB("docs", func(s *Server) { RegisterKBTools(s, k, Deps{}) })
	multi.EnableWeb(nil)
	return auth.NewTokenStore(nil).Middleware(multi.Handler())
}

func twoReleases() []updatecheck.Release {
	return []updatecheck.Release{
		{Version: "0.17.0", Body: "### Features\n\n* new", URL: "https://example.com/r/0.17.0", PublishedAt: time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)},
		{Version: "0.16.1", Name: "0.16.1", Body: "fix", URL: "https://example.com/r/0.16.1", Truncated: true, Prerelease: true},
	}
}

func getReleases(t *testing.T, h http.Handler) map[string]any {
	t.Helper()
	rr := getUI(t, h, UIAPIPrefix+"/releases", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body)
	}
	return decodeUI(t, rr)
}

func TestUIAPI_ReleasesDisabledWithoutASource(t *testing.T) {
	t.Parallel()
	h := releasesFixture(t, "0.16.1", "", "", nil, nil)
	body := getReleases(t, h)
	if body["unavailable"] != "disabled" || body["releases_url"] != updatecheck.ReleasesURL || body["current"] != "0.16.1" {
		t.Errorf("body = %v", body)
	}
	if rows, ok := body["releases"].([]any); !ok || len(rows) != 0 {
		t.Errorf("releases = %v, want an empty list", body["releases"])
	}
}

func TestUIAPI_ReleasesMarksCurrentAndNamesTheUpgrade(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	src := func(_ context.Context, etag string) (updatecheck.ReleasesResult, error) {
		calls.Add(1)
		if etag != "" {
			t.Errorf("first fetch sent ETag %q", etag)
		}
		return updatecheck.ReleasesResult{Releases: twoReleases(), ETag: `"e1"`}, nil
	}
	// The server's version may carry a leading v or not; the release list never does.
	for _, version := range []string{"0.16.1", "v0.16.1"} {
		h := releasesFixture(t, version, "v0.17.0", "homebrew", src, nil)
		body := getReleases(t, h)
		if body["latest"] != "v0.17.0" || body["channel"] != "homebrew" || body["upgrade_command"] != "brew upgrade --cask beppetemp/tap/cartographer" {
			t.Errorf("%s: upgrade facts = %v", version, body)
		}
		if _, has := body["unavailable"]; has {
			t.Errorf("%s: unavailable = %v", version, body["unavailable"])
		}
		rows := body["releases"].([]any)
		if len(rows) != 2 {
			t.Fatalf("%s: rows = %v", version, rows)
		}
		first, second := rows[0].(map[string]any), rows[1].(map[string]any)
		if first["current"] != false || second["current"] != true {
			t.Errorf("%s: current flags = %v / %v", version, first["current"], second["current"])
		}
		if first["version"] != "0.17.0" || first["body"] == "" || first["published_at"] == nil || first["url"] == "" {
			t.Errorf("%s: row = %v", version, first)
		}
		if second["truncated"] != true || second["prerelease"] != true || first["truncated"] != false {
			t.Errorf("%s: flags = %v / %v", version, first, second)
		}
	}
}

// A release URL comes from the network and becomes a link in the Atlas: only
// https leaves the server, anything else is sent as an empty url (D862).
func TestUIAPI_ReleasesKeepOnlyHTTPSURLs(t *testing.T) {
	t.Parallel()
	urls := []string{"https://example.com/r/1", "HTTPS://example.com/r/2", "javascript:alert(1)", "http://example.com/r/4", "data:text/html,x", "//example.com/r/6", ""}
	src := func(context.Context, string) (updatecheck.ReleasesResult, error) {
		var list []updatecheck.Release
		for i, u := range urls {
			list = append(list, updatecheck.Release{Version: "0.1." + string(rune('0'+i)), URL: u})
		}
		return updatecheck.ReleasesResult{Releases: list}, nil
	}
	body := getReleases(t, releasesFixture(t, "0.1.0", "", "", src, nil))
	rows := body["releases"].([]any)
	if len(rows) != len(urls) {
		t.Fatalf("rows = %d, want %d", len(rows), len(urls))
	}
	want := []string{urls[0], urls[1], "", "", "", "", ""}
	for i, r := range rows {
		if got := r.(map[string]any)["url"]; got != want[i] {
			t.Errorf("url of %q = %v, want %q", urls[i], got, want[i])
		}
	}
	if body["releases_url"] != updatecheck.ReleasesURL {
		t.Errorf("releases_url = %v", body["releases_url"])
	}
}

func TestUIAPI_ReleasesNoUpgradeBlockWithoutACommandOrANewerRelease(t *testing.T) {
	t.Parallel()
	src := func(context.Context, string) (updatecheck.ReleasesResult, error) {
		return updatecheck.ReleasesResult{Releases: twoReleases()}, nil
	}
	// A container install has no upgrade command; a server on the newest
	// release has no latest at all.
	for name, c := range map[string]struct{ latest, channel string }{
		"container": {"v0.17.0", "container"},
		"unknown":   {"v0.17.0", ""},
		"current":   {"", "homebrew"},
	} {
		body := getReleases(t, releasesFixture(t, "0.16.1", c.latest, c.channel, src, nil))
		if _, has := body["upgrade_command"]; has {
			t.Errorf("%s: upgrade_command = %v", name, body["upgrade_command"])
		}
	}
}

func TestUIAPI_ReleasesCachedForAnHour(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	src := func(_ context.Context, etag string) (updatecheck.ReleasesResult, error) {
		n := calls.Add(1)
		if n == 2 {
			// The hour is up: the ETag from the first answer comes back, and a
			// 304 keeps the list without re-sending it.
			if etag != `"e1"` {
				t.Errorf("refresh sent ETag %q, want the first answer's", etag)
			}
			return updatecheck.ReleasesResult{ETag: etag, NotModified: true}, nil
		}
		return updatecheck.ReleasesResult{Releases: twoReleases(), ETag: `"e1"`}, nil
	}
	h := releasesFixture(t, "0.16.1", "", "", src, func() time.Time { return now })

	getReleases(t, h)
	now = now.Add(59 * time.Minute)
	getReleases(t, h)
	if calls.Load() != 1 {
		t.Fatalf("source called %d times inside the TTL, want 1", calls.Load())
	}
	now = now.Add(2 * time.Minute)
	body := getReleases(t, h)
	if calls.Load() != 2 || len(body["releases"].([]any)) != 2 {
		t.Fatalf("after the TTL: %d calls, body %v; a 304 must keep the list", calls.Load(), body)
	}
	if _, stale := body["stale"]; stale {
		t.Error("a 304 is a successful refresh, not stale")
	}
	// The 304 restarted the hour.
	now = now.Add(30 * time.Minute)
	getReleases(t, h)
	if calls.Load() != 2 {
		t.Errorf("source called %d times, the 304 should have restarted the TTL", calls.Load())
	}
}

func TestUIAPI_ReleasesFailedRefreshServesTheOldList(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	var fail atomic.Bool
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	src := func(context.Context, string) (updatecheck.ReleasesResult, error) {
		calls.Add(1)
		if fail.Load() {
			return updatecheck.ReleasesResult{}, errors.New("HTTP 500")
		}
		return updatecheck.ReleasesResult{Releases: twoReleases()}, nil
	}
	h := releasesFixture(t, "0.16.1", "", "", src, func() time.Time { return now })

	getReleases(t, h)
	fail.Store(true)
	now = now.Add(2 * time.Hour)
	body := getReleases(t, h)
	if body["stale"] != true || len(body["releases"].([]any)) != 2 {
		t.Fatalf("failed refresh: body %v, want the old list with stale: true", body)
	}
	if _, has := body["unavailable"]; has {
		t.Errorf("unavailable = %v with a list to show", body["unavailable"])
	}
	// The failure is remembered for a minute: the sheet does not wait out the
	// fetch timeout on every open.
	before := calls.Load()
	now = now.Add(30 * time.Second)
	if body := getReleases(t, h); body["stale"] != true {
		t.Errorf("within the retry wait: %v", body)
	}
	if calls.Load() != before {
		t.Errorf("source called again %d time(s) inside the retry wait", calls.Load()-before)
	}
	// After it, and once GitHub answers again, the list is fresh.
	fail.Store(false)
	now = now.Add(2 * time.Minute)
	if body := getReleases(t, h); body["stale"] != nil {
		t.Errorf("recovered: %v", body)
	}
}

func TestUIAPI_ReleasesFirstFailureIsUnreachable(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	src := func(context.Context, string) (updatecheck.ReleasesResult, error) {
		calls.Add(1)
		return updatecheck.ReleasesResult{}, errors.New("dial tcp: no route")
	}
	h := releasesFixture(t, "0.16.1", "", "", src, func() time.Time { return now })
	body := getReleases(t, h)
	if body["unavailable"] != "unreachable" || body["releases_url"] != updatecheck.ReleasesURL {
		t.Errorf("body = %v", body)
	}
	if len(body["releases"].([]any)) != 0 || body["stale"] != nil {
		t.Errorf("nothing to show, nothing stale: %v", body)
	}
	getReleases(t, h)
	if calls.Load() != 1 {
		t.Errorf("source called %d times inside the retry wait, want 1", calls.Load())
	}
}

func TestUIAPI_ReleasesConcurrentOpensFetchOnce(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	release := make(chan struct{})
	started := make(chan struct{}, 8)
	src := func(context.Context, string) (updatecheck.ReleasesResult, error) {
		calls.Add(1)
		started <- struct{}{}
		<-release
		return updatecheck.ReleasesResult{Releases: twoReleases()}, nil
	}
	h := releasesFixture(t, "0.16.1", "", "", src, nil)
	done := make(chan map[string]any, 4)
	for i := 0; i < 4; i++ {
		go func() { done <- getReleases(t, h) }()
	}
	<-started
	time.Sleep(50 * time.Millisecond) // let the other callers reach the flight
	close(release)
	for i := 0; i < 4; i++ {
		if body := <-done; len(body["releases"].([]any)) != 2 {
			t.Errorf("body = %v", body)
		}
	}
	if calls.Load() != 1 {
		t.Errorf("source called %d times for 4 concurrent opens, want 1", calls.Load())
	}
}

func TestUIAPI_ReleasesIsReadOnlyAndAuthenticated(t *testing.T) {
	t.Parallel()
	src := func(context.Context, string) (updatecheck.ReleasesResult, error) {
		t.Error("the source must not run for a rejected request")
		return updatecheck.ReleasesResult{}, nil
	}
	multi := NewMultiKBServer("0.16.1")
	multi.SetReleasesSource(src)
	k := uiFixtureKB(t, "docs")
	multi.MountKB("docs", func(s *Server) { RegisterKBTools(s, k, Deps{}) })
	multi.EnableWeb(nil)
	ts := auth.NewTokenStore([]string{"tok"})
	h := ts.Middleware(multi.Handler())
	if rr := getUI(t, h, UIAPIPrefix+"/releases", ""); rr.Code != http.StatusUnauthorized {
		t.Errorf("no token: status %d, want 401", rr.Code)
	}
}
