package mcpserver

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/BeppeTemp/cartographer/internal/updatecheck"
)

// The release history behind the version in the Atlas (D862). The browser
// cannot reach GitHub (the CSP is connect-src 'self'), so the server fetches
// the list and keeps it for an hour. The source is nil unless the update check
// is on (cmd/cartographer decides): a server whose operator turned off its one
// outbound call must not make a second one.

const (
	// releasesTTL is how long a good list is served without asking GitHub:
	// 60 unauthenticated requests per hour per IP is GitHub's limit, and one
	// refresh an hour stays far below it.
	releasesTTL = time.Hour
	// releasesRetry is the wait after a failed refresh. Without it every open
	// of the sheet on a server that cannot reach GitHub would wait out the
	// fetch timeout again, and a rate-limited one would keep asking.
	releasesRetry = time.Minute
)

// ReleasesSource reads the release list. etag is the one the previous result
// returned ("" the first time); a result with NotModified keeps that list.
type ReleasesSource func(ctx context.Context, etag string) (updatecheck.ReleasesResult, error)

// SetReleasesSource installs the release-history source of GET /releases.
// Nil (the default) answers unavailable: "disabled" and calls nothing.
func (m *MultiKBServer) SetReleasesSource(fn ReleasesSource) { m.releasesSource = fn }

// releasesCache holds the last good list, in memory only.
type releasesCache struct {
	mu       sync.Mutex
	flight   singleflight.Group // one fetch at a time: the sheet and a reload may ask together
	list     []updatecheck.Release
	etag     string
	have     bool
	fetched  time.Time // when the list was last confirmed good
	failedAt time.Time // when the last refresh failed; zero after a success
	now      func() time.Time
}

func (c *releasesCache) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

// get returns the list, refreshing it when it is older than the TTL. stale is
// true when a refresh failed and the last good list is served instead; with
// none, the error is returned.
func (c *releasesCache) get(ctx context.Context, src ReleasesSource) (list []updatecheck.Release, stale bool, err error) {
	c.mu.Lock()
	now := c.clock()
	if c.have && now.Sub(c.fetched) < releasesTTL {
		list = c.list
		c.mu.Unlock()
		return list, false, nil
	}
	// A refresh failed a moment ago: do not ask again yet.
	if !c.failedAt.IsZero() && now.Sub(c.failedAt) < releasesRetry {
		have, list := c.have, c.list
		c.mu.Unlock()
		if have {
			return list, true, nil
		}
		return nil, false, errReleasesUnreachable
	}
	c.mu.Unlock()

	// The fetch outlives this request: another caller may be waiting on the
	// same flight, and the source bounds itself with its own timeouts.
	ch := c.flight.DoChan("releases", func() (any, error) {
		c.mu.Lock()
		// A caller that arrived just after another's flight ended finds the
		// list already fresh.
		if c.have && c.clock().Sub(c.fetched) < releasesTTL {
			c.mu.Unlock()
			return nil, nil
		}
		etag := c.etag
		c.mu.Unlock()
		res, err := src(context.WithoutCancel(ctx), etag)
		c.mu.Lock()
		defer c.mu.Unlock()
		if err != nil {
			c.failedAt = c.clock()
			return nil, err
		}
		if !res.NotModified || !c.have {
			c.list, c.etag = res.Releases, res.ETag
		}
		c.have, c.fetched, c.failedAt = true, c.clock(), time.Time{}
		return nil, nil
	})
	select {
	case <-ctx.Done():
		return nil, false, ctx.Err()
	case r := <-ch:
		c.mu.Lock()
		defer c.mu.Unlock()
		if r.Err != nil {
			if c.have {
				return c.list, true, nil
			}
			return nil, false, errReleasesUnreachable
		}
		return c.list, false, nil
	}
}

var errReleasesUnreachable = errors.New("release list unreachable")

// httpsOnly returns u when it is an https URL and "" otherwise. A release URL
// comes from the network and ends up as a link in the Atlas: a javascript: or
// data: URL must not leave the server as one (an empty url means no link).
func httpsOnly(u string) string {
	if len(u) >= len("https://") && strings.EqualFold(u[:len("https://")], "https://") {
		return u
	}
	return ""
}

// GET /releases — every release, newest first, with its changelog; the one
// running marked; and, when a newer one is known and the install channel
// names a command, how to upgrade. The same for every caller who can open the
// Atlas: nothing in it is KB data.
func (m *MultiKBServer) uiReleases(w http.ResponseWriter, r *http.Request) {
	current := strings.TrimPrefix(m.version, "v")
	payload := map[string]any{
		"current":      m.version,
		"releases":     []any{},
		"releases_url": updatecheck.ReleasesURL,
	}
	if m.latestVersion != nil {
		if latest := m.latestVersion(); latest != "" {
			payload["latest"] = latest
			if m.updateChannel != "" {
				payload["channel"] = m.updateChannel
				if cmd := updatecheck.Channel(m.updateChannel).UpgradeCommand(latest); cmd != "" {
					payload["upgrade_command"] = cmd
				}
			}
		}
	}
	if m.releasesSource == nil {
		payload["unavailable"] = "disabled"
		writeUIJSON(w, http.StatusOK, payload)
		return
	}
	list, stale, err := m.releases.get(r.Context(), m.releasesSource)
	if err != nil {
		if r.Context().Err() != nil {
			return // the caller went away
		}
		payload["unavailable"] = "unreachable"
		writeUIJSON(w, http.StatusOK, payload)
		return
	}
	type row struct {
		Version     string    `json:"version"`
		Name        string    `json:"name,omitempty"`
		URL         string    `json:"url"`
		PublishedAt time.Time `json:"published_at"`
		Body        string    `json:"body"`
		Truncated   bool      `json:"truncated"`
		Prerelease  bool      `json:"prerelease"`
		Current     bool      `json:"current"`
	}
	rows := make([]row, 0, len(list))
	for _, rel := range list {
		rows = append(rows, row{
			Version: rel.Version, Name: rel.Name, URL: httpsOnly(rel.URL), PublishedAt: rel.PublishedAt,
			Body: rel.Body, Truncated: rel.Truncated, Prerelease: rel.Prerelease,
			Current: current != "" && rel.Version == current,
		})
	}
	payload["releases"] = rows
	if stale {
		payload["stale"] = true
	}
	writeUIJSON(w, http.StatusOK, payload)
}
