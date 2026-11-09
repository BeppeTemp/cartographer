package recipe

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// Page is what GetFunc returns for one URL.
type Page struct {
	Body               []byte
	ETag, LastModified string
}

// GetFunc fetches one page. The worker supplies the fetch the unit executor
// uses (size cap, redirects within the host, fetch.allow_private); the recipe
// adds the scheme and host checks in front of it.
type GetFunc func(ctx context.Context, url string) (Page, error)

// httpPages lists a page set: a sitemap or an HTML index, one level, no crawl.
type httpPages struct {
	text     string
	hosts    []string
	insecure bool
	get      GetFunc
}

func (h *httpPages) Name() string         { return "http-pages" }
func (h *httpPages) Shape() string        { return ShapeFeed }
func (h *httpPages) Instructions() string { return h.text }

var hrefRE = regexp.MustCompile(`(?is)\bhref\s*=\s*["']([^"']+)["']`)

// check refuses a URL the recipe may not read before anything is fetched:
// https (http only with fetch.allow_insecure_http), no credential, and a host on
// fetch.allow_hosts. The fragment is dropped.
func (h *httpPages) check(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	switch {
	case err != nil || u.Host == "" || u.Hostname() == "":
		return nil, fail(CodeBadLocator, "not an http(s) URL")
	case u.Scheme != "https" && !(u.Scheme == "http" && h.insecure):
		return nil, fail(CodeBadLocator, "only https is read (fetch.allow_insecure_http allows http)")
	case u.User != nil:
		return nil, fail(CodeBadLocator, "a URL with credentials is refused: use an exec recipe or an MCP connector")
	case !HostAllowed(u.Hostname(), h.hosts):
		return nil, fail(CodeHostNotAllowed, "%s is not in fetch.allow_hosts", strings.ToLower(u.Hostname()))
	}
	u.Fragment = ""
	return u, nil
}

// links extracts the references of a sitemap (<loc>) or an HTML index (href).
func links(body []byte) ([]string, error) {
	s := string(body)
	switch {
	case strings.Contains(s, "<sitemapindex"):
		return nil, fail(CodeListFailed, "a sitemap index is not followed: name one sitemap")
	case strings.Contains(s, "<urlset"):
		var m struct {
			URLs []struct {
				Loc string `xml:"loc"`
			} `xml:"url"`
		}
		if err := xml.Unmarshal(body, &m); err != nil {
			return nil, fail(CodeListFailed, "the sitemap is not valid XML")
		}
		var out []string
		for _, u := range m.URLs {
			out = append(out, strings.TrimSpace(u.Loc))
		}
		return out, nil
	}
	var out []string
	for _, m := range hrefRE.FindAllStringSubmatch(s, -1) {
		out = append(out, m[1])
	}
	return out, nil
}

func (h *httpPages) List(ctx context.Context, req Request) (Listing, error) {
	base, err := h.check(req.Locator) // before any request
	if err != nil {
		return Listing{}, err
	}
	idx, err := h.get(ctx, base.String())
	if err != nil {
		return Listing{}, fail(CodeListFailed, "index: %s", oneLine(err.Error()))
	}
	refs, err := links(idx.Body)
	if err != nil {
		return Listing{}, err
	}
	seen := map[string]bool{base.String(): true}
	var urls []string
	for _, ref := range refs {
		r, perr := base.Parse(ref)
		if perr != nil {
			continue
		}
		// One level, the index's own host and port only; the host list and the
		// scheme rule apply to every link, not only to the locator.
		u, cerr := h.check(r.String())
		if cerr != nil || u.Host != base.Host || seen[u.String()] || !included(req.Include, u.String()) {
			continue
		}
		seen[u.String()] = true
		urls = append(urls, u.String())
	}
	max := req.MaxEntries
	if max <= 0 {
		max = DefaultMaxEntries
	}
	if len(urls) > max {
		return Listing{}, fail(CodeListFailed, "index lists %d matching links, max_entries is %d", len(urls), max)
	}
	sort.Strings(urls)
	digest := sha256.New()
	var entries []Entry
	for _, raw := range urls {
		p, err := h.get(ctx, raw)
		if err != nil {
			return Listing{}, fail(CodeListFailed, "%s: %s", raw, oneLine(err.Error()))
		}
		rev := p.ETag
		if rev == "" {
			rev = p.LastModified
		}
		if rev == "" {
			sum := sha256.Sum256(p.Body)
			rev = "sha256:" + hex.EncodeToString(sum[:8])
		}
		u, _ := url.Parse(raw)
		key := u.EscapedPath()
		if key == "" {
			key = "/"
		}
		digest.Write([]byte(raw + "|" + rev + "\n"))
		// Every matching page is emitted each poll as added: the unit ID, which
		// carries the revision, tells new from known, so no per-entry state is
		// kept. A digest cannot say which page went, so none is ever removed.
		entries = append(entries, Entry{Key: key, Title: key, Revision: rev, Change: Added, Locator: raw})
	}
	next := hex.EncodeToString(digest.Sum(nil))
	if next == req.Cursor {
		return Listing{NextCursor: next}, nil // nothing changed since the last poll
	}
	return Listing{Entries: entries, NextCursor: next}, nil
}

// included: feed_include is a list of URL prefixes (a trailing * is accepted);
// none means every same-host link.
func included(globs []string, u string) bool {
	if len(globs) == 0 {
		return true
	}
	for _, g := range globs {
		if strings.HasPrefix(u, strings.TrimSuffix(g, "*")) {
			return true
		}
	}
	return false
}
