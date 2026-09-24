// Package updatecheck answers whether a newer Cartographer release exists and
// how this binary is upgraded (D254): the latest tag from the GitHub release
// list, cached for 24 hours and failing silent, and the install channel the
// running binary came from.
//
// It never prints. Every failure degrades to "no update known", because the
// main caller runs inside an agent's session-start hook, where an error line
// or a wait on the network would be worse than no answer.
package updatecheck

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	// DefaultAPIURL is the repository's GitHub API base. The release list is
	// read from <base>/releases?per_page=1, not /releases/latest: the latter
	// skips pre-releases, and every 0.x release is one (D82, D211) — the same
	// endpoint install.sh uses.
	DefaultAPIURL = "https://api.github.com/repos/BeppeTemp/cartographer"
	// ReleasesURL is the human-facing release list, named when no upgrade
	// command can be given.
	ReleasesURL = "https://github.com/BeppeTemp/cartographer/releases"

	// TTL is how long a cached answer is trusted without asking GitHub.
	TTL = 24 * time.Hour
	// Timeout bounds the one request a check may make.
	Timeout = 3 * time.Second

	// EnvDisable turns every check off when set to 1 (or true).
	EnvDisable = "CARTOGRAPHER_NO_UPDATE_CHECK"
	// EnvAPIURL overrides DefaultAPIURL. For tests only: it exists so an
	// end-to-end run can point a real binary at a local fake, and is not a
	// supported way to follow a fork.
	EnvAPIURL = "CARTOGRAPHER_UPDATE_API_URL"

	// CacheFileName is the client cache file inside CacheDir.
	CacheFileName = "update-check.json"
)

// Kinds of update, by the first semver component that differs.
const (
	KindPatch = "patch"
	KindMinor = "minor"
	KindMajor = "major"
)

// Result is the answer to "is there a newer release". Latest is the newest
// tag known, empty when none is; Available is true only when it is newer than
// Current.
type Result struct {
	Current   string    `json:"current"`
	Latest    string    `json:"latest,omitempty"`
	Available bool      `json:"available"`
	Kind      string    `json:"kind,omitempty"`
	CheckedAt time.Time `json:"checked_at,omitempty"`
}

// Options tunes one Check. The zero value is the default hook behaviour:
// answer from a fresh cache, refresh a stale one.
type Options struct {
	// CacheFile is the cache path. Empty means CacheDir()/CacheFileName; a
	// cache directory that cannot be resolved means no cache at all.
	CacheFile string
	// Force ignores the TTL (not the opt-out): `update check` refreshes.
	Force bool
	// CacheOnly never touches the network: `status`, `doctor` and the
	// dashboard must not add latency, and read whatever the cache holds.
	CacheOnly bool
	// Disabled is the client configuration's `update.check: false`.
	Disabled bool

	// Injection points for tests; nil means the real thing.
	Now        func() time.Time
	HTTPClient *http.Client
	APIURL     string
	Getenv     func(string) string
}

// cacheEntry is the on-disk cache.
type cacheEntry struct {
	CheckedAt time.Time `json:"checked_at"`
	Latest    string    `json:"latest"`
	ETag      string    `json:"etag,omitempty"`
}

// ErrDisabled is returned when the check is turned off, so a caller that
// prints (`update check`) can say why nothing was checked.
var ErrDisabled = errors.New("update check disabled")

// CacheDir is <os.UserCacheDir()>/cartographer.
func CacheDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "cartographer"), nil
}

// DisabledByEnv reports whether EnvDisable turns the check off.
func DisabledByEnv(getenv func(string) string) bool {
	if getenv == nil {
		getenv = os.Getenv
	}
	switch strings.ToLower(strings.TrimSpace(getenv(EnvDisable))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// Check answers whether a release newer than current exists. The returned
// Result is always usable; the error only explains a degraded answer (a
// failed refresh answered from a stale cache, or no answer at all) and is
// never something a hook should print.
func Check(ctx context.Context, current string, opts Options) (Result, error) {
	res := Result{Current: current}
	// A dev build never checks: it is not on any release line.
	if current == "" || current == "dev" {
		return res, nil
	}
	getenv := opts.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	if opts.Disabled || DisabledByEnv(getenv) {
		return res, ErrDisabled
	}
	now := time.Now
	if opts.Now != nil {
		now = opts.Now
	}

	cachePath := opts.CacheFile
	if cachePath == "" {
		if dir, err := CacheDir(); err == nil {
			cachePath = filepath.Join(dir, CacheFileName)
		}
	}
	cached, haveCache := readCache(cachePath)

	fresh := haveCache && !cached.CheckedAt.After(now()) && now().Sub(cached.CheckedAt) < TTL
	if opts.CacheOnly || (fresh && !opts.Force) {
		if !haveCache {
			return res, nil
		}
		return fill(res, cached), nil
	}

	entry, err := fetch(ctx, current, cached, haveCache, opts, getenv, now)
	if err != nil {
		if haveCache {
			return fill(res, cached), err
		}
		return res, err
	}
	writeCache(cachePath, entry)
	return fill(res, entry), nil
}

func fill(res Result, e cacheEntry) Result {
	res.Latest = e.Latest
	res.CheckedAt = e.CheckedAt
	res.Available, res.Kind = Compare(res.Current, e.Latest)
	return res
}

// fetch performs the one request. A 304 keeps the cached tag and refreshes
// only its timestamp.
func fetch(ctx context.Context, current string, cached cacheEntry, haveCache bool, opts Options, getenv func(string) string, now func() time.Time) (cacheEntry, error) {
	base := opts.APIURL
	if base == "" {
		base = getenv(EnvAPIURL)
	}
	if base == "" {
		base = DefaultAPIURL
	}
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+"/releases?per_page=1", nil)
	if err != nil {
		return cacheEntry{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "cartographer/"+current)
	if haveCache && cached.ETag != "" && cached.Latest != "" {
		req.Header.Set("If-None-Match", cached.ETag)
	}
	if tok := getenv("GITHUB_TOKEN"); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	hc := opts.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: Timeout}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return cacheEntry{}, err
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusNotModified && haveCache && cached.Latest != "":
		cached.CheckedAt = now().UTC()
		return cached, nil
	case resp.StatusCode != http.StatusOK:
		return cacheEntry{}, fmt.Errorf("release list: HTTP %d", resp.StatusCode)
	}
	var releases []struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return cacheEntry{}, fmt.Errorf("release list: %w", err)
	}
	if len(releases) == 0 || strings.TrimSpace(releases[0].TagName) == "" {
		return cacheEntry{}, errors.New("release list: no tag")
	}
	return cacheEntry{CheckedAt: now().UTC(), Latest: strings.TrimSpace(releases[0].TagName), ETag: resp.Header.Get("ETag")}, nil
}

// readCache treats a missing, unreadable or corrupt cache as absent.
func readCache(path string) (cacheEntry, bool) {
	if path == "" {
		return cacheEntry{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return cacheEntry{}, false
	}
	var e cacheEntry
	if err := json.Unmarshal(data, &e); err != nil || e.Latest == "" || e.CheckedAt.IsZero() {
		return cacheEntry{}, false
	}
	return e, true
}

// writeCache replaces the cache atomically (temp + rename). Best effort: a
// cache that cannot be written only means the next check asks again.
func writeCache(path string, e cacheEntry) {
	if path == "" {
		return
	}
	data, err := json.Marshal(e)
	if err != nil {
		return
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return
	}
	name := tmp.Name()
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if werr != nil || cerr != nil {
		os.Remove(name)
		return
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
	}
}

// Compare reports whether latest is newer than current and, if so, which
// semver component differs. Either side unparseable, or latest not newer,
// means not available: a local build ahead of the last release is never
// "behind". A difference in the pre-release suffix alone (v1.0.0-rc.1 →
// v1.0.0) counts as minor, so an automatic patch never takes it.
func Compare(current, latest string) (bool, string) {
	c, ok1 := parse(current)
	l, ok2 := parse(latest)
	if !ok1 || !ok2 || compare(l, c) <= 0 {
		return false, ""
	}
	switch {
	case l.core[0] != c.core[0]:
		return true, KindMajor
	case l.core[1] != c.core[1]:
		return true, KindMinor
	case l.core[2] != c.core[2]:
		return true, KindPatch
	}
	return true, KindMinor
}

type semver struct {
	core [3]int
	pre  []string
}

func parse(v string) (semver, bool) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexByte(v, '+'); i >= 0 {
		v = v[:i]
	}
	var s semver
	core := v
	if i := strings.IndexByte(v, '-'); i >= 0 {
		core = v[:i]
		s.pre = strings.Split(v[i+1:], ".")
	}
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return semver{}, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return semver{}, false
		}
		s.core[i] = n
	}
	return s, true
}

// compare orders two versions by semver precedence: a pre-release sorts below
// its release, identifiers compare numerically when both are numbers.
func compare(a, b semver) int {
	for i := 0; i < 3; i++ {
		if a.core[i] != b.core[i] {
			if a.core[i] < b.core[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(a.pre) == 0 && len(b.pre) == 0:
		return 0
	case len(a.pre) == 0:
		return 1
	case len(b.pre) == 0:
		return -1
	}
	for i := 0; i < len(a.pre) && i < len(b.pre); i++ {
		if c := compareIdent(a.pre[i], b.pre[i]); c != 0 {
			return c
		}
	}
	switch {
	case len(a.pre) < len(b.pre):
		return -1
	case len(a.pre) > len(b.pre):
		return 1
	}
	return 0
}

func compareIdent(a, b string) int {
	an, aerr := strconv.Atoi(a)
	bn, berr := strconv.Atoi(b)
	switch {
	case aerr == nil && berr == nil:
		switch {
		case an < bn:
			return -1
		case an > bn:
			return 1
		}
		return 0
	case aerr == nil:
		return -1 // numeric identifiers sort below alphanumeric ones
	case berr == nil:
		return 1
	}
	return strings.Compare(a, b)
}
