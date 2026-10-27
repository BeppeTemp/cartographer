package updatecheck

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// A fake GitHub release list: pages by ?page=N, an optional ETag on page 1.
// Like the Check tests, nothing here touches the real network.
type fakeReleaseList struct {
	srv  *httptest.Server
	mu   sync.Mutex
	got  []string // the request URIs, in order
	auth []string
	ifNM []string
}

func newFakeReleaseList(t *testing.T, code int, etag string, pages ...[]apiRelease) *fakeReleaseList {
	t.Helper()
	f := &fakeReleaseList{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.got = append(f.got, r.URL.RequestURI())
		f.auth = append(f.auth, r.Header.Get("Authorization"))
		f.ifNM = append(f.ifNM, r.Header.Get("If-None-Match"))
		f.mu.Unlock()
		if r.URL.Path != "/releases" || r.URL.Query().Get("per_page") != "100" {
			t.Errorf("unexpected request %s", r.URL)
		}
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if page == 1 && etag != "" {
			if r.Header.Get("If-None-Match") == etag {
				w.WriteHeader(http.StatusNotModified)
				return
			}
			w.Header().Set("ETag", etag)
		}
		if code != http.StatusOK {
			w.WriteHeader(code)
			return
		}
		var body []apiRelease
		if page >= 1 && page <= len(pages) {
			body = pages[page-1]
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func releasesOpts(f *fakeReleaseList, env map[string]string) Options {
	return Options{APIURL: f.srv.URL, Getenv: func(k string) string { return env[k] }}
}

func rel(tag string) apiRelease {
	return apiRelease{
		TagName:     tag,
		Name:        tag,
		Body:        "### Features\n\n* thing in " + tag,
		HTMLURL:     "https://example.com/releases/" + tag,
		PublishedAt: time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC),
	}
}

func fullPage(prefix string) []apiRelease {
	page := make([]apiRelease, ReleasesPerPage)
	for i := range page {
		page[i] = rel("v" + prefix + "." + strconv.Itoa(i) + ".0")
	}
	return page
}

func TestReleasesFollowsPagesWhileFull(t *testing.T) {
	f := newFakeReleaseList(t, http.StatusOK, "", fullPage("2"), []apiRelease{rel("v1.0.0")})
	res, err := Releases(context.Background(), releasesOpts(f, nil), ReleasesRequest{Current: "v1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Releases) != ReleasesPerPage+1 {
		t.Fatalf("%d releases, want %d", len(res.Releases), ReleasesPerPage+1)
	}
	if res.Releases[0].Version != "2.0.0" || res.Releases[ReleasesPerPage].Version != "1.0.0" {
		t.Errorf("order not kept: first %q, last %q", res.Releases[0].Version, res.Releases[ReleasesPerPage].Version)
	}
	if len(f.got) != 2 || !strings.HasSuffix(f.got[1], "page=2") {
		t.Errorf("requests = %v, want pages 1 and 2 only (the second is short)", f.got)
	}
}

func TestReleasesStopsAtThreePages(t *testing.T) {
	f := newFakeReleaseList(t, http.StatusOK, "", fullPage("4"), fullPage("3"), fullPage("2"), fullPage("1"))
	res, err := Releases(context.Background(), releasesOpts(f, nil), ReleasesRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(f.got) != ReleasesMaxPages || len(res.Releases) != ReleasesMaxPages*ReleasesPerPage {
		t.Errorf("%d requests, %d releases; want %d and %d", len(f.got), len(res.Releases), ReleasesMaxPages, ReleasesMaxPages*ReleasesPerPage)
	}
}

func TestReleasesDropsDraftsCandidatesAndStripsV(t *testing.T) {
	draft := rel("v0.9.0")
	draft.Draft = true
	rc := rel("v0.8.0-rc.1")
	rc.Prerelease = true
	// A stable 0.x release carries GitHub's flag too (D82, D211): the flag is carried, never the criterion.
	pre := rel("v0.8.0")
	pre.Prerelease = true
	f := newFakeReleaseList(t, http.StatusOK, "", []apiRelease{rel("v1.0.0"), draft, rc, pre, rel("0.7.0")})
	res, err := Releases(context.Background(), releasesOpts(f, nil), ReleasesRequest{})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range res.Releases {
		got = append(got, r.Version)
	}
	if strings.Join(got, " ") != "1.0.0 0.8.0 0.7.0" {
		t.Fatalf("versions = %v, want the draft and the candidate dropped and the leading v stripped", got)
	}
	if !res.Releases[1].Prerelease || res.Releases[0].Prerelease {
		t.Error("prerelease flag not carried")
	}
	if res.Releases[0].URL != "https://example.com/releases/v1.0.0" || res.Releases[0].PublishedAt.IsZero() {
		t.Errorf("url/date not carried: %+v", res.Releases[0])
	}
}

func TestReleasesDropsTagsThatAreNotVersions(t *testing.T) {
	f := newFakeReleaseList(t, http.StatusOK, "", []apiRelease{
		rel("v1.0.0"), rel("v9.9.9-$(id)"), rel("nightly"), rel("v0.9.0+build"), rel("v0.8.0"),
	})
	res, err := Releases(context.Background(), releasesOpts(f, nil), ReleasesRequest{})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range res.Releases {
		got = append(got, r.Version)
	}
	if strings.Join(got, " ") != "1.0.0 0.8.0" {
		t.Fatalf("versions = %v, want only the version tags", got)
	}
}

func TestReleasesTruncatesLongBodyOnARuneBoundary(t *testing.T) {
	long := rel("v1.0.0")
	// "é" is two bytes: an odd cut point lands inside one.
	long.Body = strings.Repeat("é", MaxBodyBytes)
	short := rel("v0.9.0")
	f := newFakeReleaseList(t, http.StatusOK, "", []apiRelease{long, short})
	res, err := Releases(context.Background(), releasesOpts(f, nil), ReleasesRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Releases[0].Truncated || len(res.Releases[0].Body) > MaxBodyBytes {
		t.Fatalf("body %d bytes, truncated=%v; want <= %d and flagged", len(res.Releases[0].Body), res.Releases[0].Truncated, MaxBodyBytes)
	}
	if strings.ContainsRune(res.Releases[0].Body, '�') {
		t.Error("the cut split a UTF-8 rune")
	}
	if res.Releases[1].Truncated {
		t.Error("a short body must not be flagged")
	}
}

func TestReleasesNotModifiedWithTheSentETag(t *testing.T) {
	f := newFakeReleaseList(t, http.StatusOK, `"v1"`, []apiRelease{rel("v1.0.0")})
	first, err := Releases(context.Background(), releasesOpts(f, nil), ReleasesRequest{})
	if err != nil || first.ETag != `"v1"` || first.NotModified || len(first.Releases) != 1 {
		t.Fatalf("first = %+v, err %v", first, err)
	}
	second, err := Releases(context.Background(), releasesOpts(f, nil), ReleasesRequest{ETag: first.ETag})
	if err != nil {
		t.Fatal(err)
	}
	if !second.NotModified || second.ETag != `"v1"` || len(second.Releases) != 0 {
		t.Errorf("second = %+v, want NotModified with the ETag kept and no list", second)
	}
	if got := f.ifNM[len(f.ifNM)-1]; got != `"v1"` {
		t.Errorf("If-None-Match = %q", got)
	}
}

func TestReleasesETagOnlyOnTheFirstPage(t *testing.T) {
	// A 200 on page 1 (the tag changed) followed by page 2: only page 1 may
	// carry If-None-Match, or page 2 would answer 304 with no body.
	f := newFakeReleaseList(t, http.StatusOK, `"v2"`, fullPage("2"), []apiRelease{rel("v1.0.0")})
	if _, err := Releases(context.Background(), releasesOpts(f, nil), ReleasesRequest{ETag: `"v1"`}); err != nil {
		t.Fatal(err)
	}
	if len(f.ifNM) != 2 || f.ifNM[0] != `"v1"` || f.ifNM[1] != "" {
		t.Errorf("If-None-Match per page = %q", f.ifNM)
	}
}

func TestReleasesErrors(t *testing.T) {
	for _, code := range []int{http.StatusInternalServerError, http.StatusForbidden} {
		f := newFakeReleaseList(t, code, "")
		if _, err := Releases(context.Background(), releasesOpts(f, nil), ReleasesRequest{}); err == nil || !strings.Contains(err.Error(), strconv.Itoa(code)) {
			t.Errorf("HTTP %d: err = %v, want one naming the status", code, err)
		}
	}

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("not json")) }))
	t.Cleanup(bad.Close)
	if _, err := Releases(context.Background(), Options{APIURL: bad.URL, Getenv: func(string) string { return "" }}, ReleasesRequest{}); err == nil {
		t.Error("an undecodable page must be an error")
	}

	// A second page failing fails the call: no silently shortened history.
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") == "2" {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_ = json.NewEncoder(w).Encode(fullPage("2"))
	}))
	t.Cleanup(second.Close)
	if _, err := Releases(context.Background(), Options{APIURL: second.URL, Getenv: func(string) string { return "" }}, ReleasesRequest{}); err == nil {
		t.Error("a failing second page must be an error")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f := newFakeReleaseList(t, http.StatusOK, "", []apiRelease{rel("v1.0.0")})
	if _, err := Releases(ctx, releasesOpts(f, nil), ReleasesRequest{}); err == nil {
		t.Error("a cancelled context must be an error")
	}
}

func TestReleasesAuthorizationOnlyWithAToken(t *testing.T) {
	f := newFakeReleaseList(t, http.StatusOK, "", []apiRelease{rel("v1.0.0")})
	if _, err := Releases(context.Background(), releasesOpts(f, nil), ReleasesRequest{}); err != nil {
		t.Fatal(err)
	}
	if f.auth[0] != "" {
		t.Errorf("Authorization = %q without GITHUB_TOKEN", f.auth[0])
	}
	if _, err := Releases(context.Background(), releasesOpts(f, map[string]string{"GITHUB_TOKEN": "tok"}), ReleasesRequest{}); err != nil {
		t.Fatal(err)
	}
	if f.auth[1] != "Bearer tok" {
		t.Errorf("Authorization = %q with GITHUB_TOKEN", f.auth[1])
	}
}

func TestReleasesAPIURLFromEnv(t *testing.T) {
	f := newFakeReleaseList(t, http.StatusOK, "", []apiRelease{rel("v1.0.0")})
	env := map[string]string{EnvAPIURL: f.srv.URL}
	res, err := Releases(context.Background(), Options{Getenv: func(k string) string { return env[k] }}, ReleasesRequest{})
	if err != nil || len(res.Releases) != 1 {
		t.Fatalf("res = %+v, err %v: the EnvAPIURL override must be honoured as in Check", res, err)
	}
}

// The Atlas history lists releases only, never a candidate at the head (D1192).
func TestReleasesSkipsACandidateAtTheHead(t *testing.T) {
	rc := rel("v0.28.0-rc.20261111")
	rc.Prerelease = true
	f := newFakeReleaseList(t, http.StatusOK, "", []apiRelease{rc, rel("v0.27.0")})
	res, err := Releases(context.Background(), releasesOpts(f, nil), ReleasesRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Releases) != 1 || res.Releases[0].Version != "0.27.0" {
		t.Errorf("releases = %+v, want only 0.27.0", res.Releases)
	}
}
