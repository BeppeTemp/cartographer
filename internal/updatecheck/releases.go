package updatecheck

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
	"unicode/utf8"
)

// The release history (D862): every release with its changelog, read from the
// same GitHub list the update check uses, on demand and in full. Unlike
// Check it keeps no cache of its own and never touches the disk: the caller
// (the Atlas route) owns the cache, so a CLI that wanted the list could not
// be surprised by one.
//
// Releases does not look at the update-check switch (Options.Disabled,
// EnvDisable, a dev build): the one place that decides whether this server
// talks to GitHub at all is the wiring in cmd/cartographer, and a second,
// quieter gate here would let the two drift.

const (
	// ReleasesTimeout bounds each page request, not the whole call: a history
	// of three pages can take up to ReleasesMaxPages x ReleasesTimeout. A page
	// is up to a few MiB of changelog, not the single tag Check asks for.
	ReleasesTimeout = 10 * time.Second
	// ReleasesPerPage is GitHub's maximum page size.
	ReleasesPerPage = 100
	// ReleasesMaxPages caps the history at 300 releases: the bound that keeps
	// a repository with a very long history from turning one open into an
	// unbounded crawl.
	ReleasesMaxPages = 3
	// MaxResponseBytes caps what is read from one page response; a longer
	// body fails to decode and the call fails, rather than buffering it.
	MaxResponseBytes = 4 << 20
	// MaxBodyBytes cuts one release's changelog; the cut release is flagged
	// Truncated and the UI links to the full text on GitHub.
	MaxBodyBytes = 64 << 10
)

// Release is one published release. Version is the tag without its leading
// "v", the form the server reports its own version in.
type Release struct {
	Version     string    `json:"version"`
	Name        string    `json:"name,omitempty"`
	Body        string    `json:"body"`
	URL         string    `json:"url"`
	PublishedAt time.Time `json:"published_at"`
	Prerelease  bool      `json:"prerelease"`
	// Truncated is true when Body was cut at MaxBodyBytes.
	Truncated bool `json:"truncated,omitempty"`
}

// ReleasesRequest is what a caller passes to Releases.
type ReleasesRequest struct {
	// ETag is the one a previous result returned: sent as If-None-Match on
	// the first page, a 304 there means the list is unchanged.
	ETag string
	// Current is the running version, for the User-Agent only.
	Current string
}

// ReleasesResult is the answer. NotModified means the sent ETag still holds
// and Releases is empty: the caller keeps the list it has.
type ReleasesResult struct {
	Releases    []Release
	ETag        string
	NotModified bool
}

type apiRelease struct {
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	Body        string    `json:"body"`
	HTMLURL     string    `json:"html_url"`
	PublishedAt time.Time `json:"published_at"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
}

// Releases reads the release list, newest first (the order GitHub returns),
// following pages while one is full, up to ReleasesMaxPages. Drafts and
// releases whose tag is not a version (ValidTag) are dropped. It asks the same base URL and sends the same headers as Check,
// including GITHUB_TOKEN when set (read through opts.Getenv).
//
// Any non-2xx status other than 304, an undecodable page and a timeout are
// errors; no partial list is returned for them.
func Releases(ctx context.Context, opts Options, req ReleasesRequest) (ReleasesResult, error) {
	getenv := opts.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	base := opts.APIURL
	if base == "" {
		base = getenv(EnvAPIURL)
	}
	if base == "" {
		base = DefaultAPIURL
	}
	base = strings.TrimRight(base, "/")
	hc := opts.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: ReleasesTimeout}
	}

	var out ReleasesResult
	for page := 1; page <= ReleasesMaxPages; page++ {
		raw, etag, notModified, err := releasesPage(ctx, hc, base, page, req, getenv)
		if err != nil {
			return ReleasesResult{}, err
		}
		if notModified {
			return ReleasesResult{ETag: req.ETag, NotModified: true}, nil
		}
		if page == 1 {
			out.ETag = etag
		}
		for _, r := range raw {
			tag := strings.TrimSpace(r.TagName)
			if r.Draft || !stableTag(tag) { // a candidate is not a release (D1192)
				continue
			}
			body, cut := truncateBody(r.Body, MaxBodyBytes)
			out.Releases = append(out.Releases, Release{
				Version:     strings.TrimPrefix(tag, "v"),
				Name:        r.Name,
				Body:        body,
				URL:         r.HTMLURL,
				PublishedAt: r.PublishedAt,
				Prerelease:  r.Prerelease,
				Truncated:   cut,
			})
		}
		if len(raw) < ReleasesPerPage {
			break
		}
	}
	return out, nil
}

// releasesPage fetches one page. Only the first carries If-None-Match: a 304
// on it stands for the whole list, because a new release lands on page 1.
func releasesPage(ctx context.Context, hc *http.Client, base string, page int, req ReleasesRequest, getenv func(string) string) ([]apiRelease, string, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, ReleasesTimeout)
	defer cancel()
	url := fmt.Sprintf("%s/releases?per_page=%d&page=%d", base, ReleasesPerPage, page)
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", false, err
	}
	r.Header.Set("Accept", "application/vnd.github+json")
	r.Header.Set("User-Agent", "cartographer/"+req.Current)
	if page == 1 && req.ETag != "" {
		r.Header.Set("If-None-Match", req.ETag)
	}
	if tok := getenv("GITHUB_TOKEN"); tok != "" {
		r.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := hc.Do(r)
	if err != nil {
		return nil, "", false, err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotModified && page == 1 && req.ETag != "":
		return nil, "", true, nil
	case resp.StatusCode != http.StatusOK:
		return nil, "", false, fmt.Errorf("release history: HTTP %d", resp.StatusCode)
	}
	var raw []apiRelease
	if err := json.NewDecoder(io.LimitReader(resp.Body, MaxResponseBytes)).Decode(&raw); err != nil {
		return nil, "", false, fmt.Errorf("release history: %w", err)
	}
	return raw, resp.Header.Get("ETag"), false, nil
}

// truncateBody cuts s to at most max bytes without splitting a UTF-8 rune.
func truncateBody(s string, max int) (string, bool) {
	if len(s) <= max {
		return s, false
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut], true
}
