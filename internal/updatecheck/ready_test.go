package updatecheck

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// channelWorld is one fake of everything a channel check can touch: the
// release list (with assets), the tap's cask and the image registry.
type channelWorld struct {
	srv      *httptest.Server
	releases string
	cask     string
	caskCode int
	caskURL  string // overrides the fake cask URL
	imageTag string // the one tag the registry has ("" = none)
	tokenOK  bool
	hits     map[string]*atomic.Int32
}

func newChannelWorld(t *testing.T, releases string) *channelWorld {
	t.Helper()
	w := &channelWorld{releases: releases, caskCode: 200, tokenOK: true, hits: map[string]*atomic.Int32{}}
	for _, k := range []string{"releases", "cask", "token", "manifest"} {
		w.hits[k] = &atomic.Int32{}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/releases", func(rw http.ResponseWriter, r *http.Request) {
		w.hits["releases"].Add(1)
		rw.Write([]byte(w.releases))
	})
	mux.HandleFunc("/cask.rb", func(rw http.ResponseWriter, r *http.Request) {
		w.hits["cask"].Add(1)
		if r.Header.Get("Authorization") != "" {
			t.Errorf("the cask request carried a token")
		}
		rw.WriteHeader(w.caskCode)
		rw.Write([]byte(w.cask))
	})
	mux.HandleFunc("/token", func(rw http.ResponseWriter, r *http.Request) {
		w.hits["token"].Add(1)
		if !w.tokenOK {
			rw.WriteHeader(http.StatusForbidden)
			return
		}
		if r.URL.Query().Get("scope") != "repository:beppetemp/cartographer:pull" {
			t.Errorf("token scope = %q", r.URL.Query().Get("scope"))
		}
		rw.Write([]byte(`{"token":"anon"}`))
	})
	mux.HandleFunc("/v2/beppetemp/cartographer/manifests/", func(rw http.ResponseWriter, r *http.Request) {
		w.hits["manifest"].Add(1)
		if r.Method != http.MethodHead || r.Header.Get("Authorization") != "Bearer anon" {
			t.Errorf("manifest request %s auth=%q", r.Method, r.Header.Get("Authorization"))
		}
		if filepath.Base(r.URL.Path) == w.imageTag {
			return
		}
		rw.WriteHeader(http.StatusNotFound)
	})
	w.srv = httptest.NewServer(mux)
	t.Cleanup(w.srv.Close)
	return w
}

func (w *channelWorld) check(t *testing.T, current string, ch Channel, goos, goarch string) (Result, error, string) {
	t.Helper()
	cache := filepath.Join(t.TempDir(), CacheFileName)
	caskURL := w.srv.URL + "/cask.rb"
	if w.caskURL != "" {
		caskURL = w.caskURL
	}
	res, err := Check(context.Background(), current, Options{
		CacheFile: cache, APIURL: w.srv.URL + "/api", CaskURL: caskURL, RegistryURL: w.srv.URL,
		Channel: ch, GOOS: goos, GOARCH: goarch,
		Now:    func() time.Time { return time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC) },
		Getenv: func(k string) string { return map[string]string{"GITHUB_TOKEN": "tok"}[k] },
	})
	return res, err, cache
}

func notCached(t *testing.T, cache string) {
	t.Helper()
	if _, ok := readCache(cache); ok {
		t.Fatal("a release the channel cannot install was cached")
	}
}

const bareRelease = `[{"tag_name":"v0.28.0","assets":[]}]`

func TestHomebrewWaitsForTheCask(t *testing.T) {
	w := newChannelWorld(t, bareRelease)
	w.cask = "cask \"cartographer\" do\n  version \"0.27.0\"\n  sha256 \"x\"\nend\n"
	res, err, cache := w.check(t, "v0.27.0", ChannelHomebrew, "darwin", "arm64")
	if res.Available || !errors.Is(err, ErrNotInstallable) {
		t.Fatalf("release published, cask not yet updated: %+v err=%v", res, err)
	}
	notCached(t, cache)

	w.cask = "cask \"cartographer\" do\n  version \"0.28.0\"\nend\n"
	res, err, cache = w.check(t, "v0.27.0", ChannelHomebrew, "darwin", "arm64")
	if err != nil || !res.Available || res.Latest != "v0.28.0" {
		t.Fatalf("cask updated: %+v err=%v", res, err)
	}
	if e, ok := readCache(cache); !ok || e.Latest != "v0.28.0" {
		t.Fatalf("cache = %+v", e)
	}
}

func TestHomebrewFailuresAnnounceNothingAndCacheNothing(t *testing.T) {
	for name, set := range map[string]func(*channelWorld){
		"cask 500":    func(w *channelWorld) { w.caskCode = 500 },
		"no version":  func(w *channelWorld) { w.cask = "cask \"x\" do\nend\n" },
		"unreachable": func(w *channelWorld) { w.caskURL = "http://127.0.0.1:1/cask.rb" },
	} {
		t.Run(name, func(t *testing.T) {
			w := newChannelWorld(t, bareRelease)
			set(w)
			res, err, cache := w.check(t, "v0.27.0", ChannelHomebrew, "darwin", "arm64")
			if res.Available || !errors.Is(err, ErrNotInstallable) {
				t.Fatalf("%+v err=%v", res, err)
			}
			notCached(t, cache)
		})
	}
}

func TestInstallShWaitsForThePlatformAsset(t *testing.T) {
	cases := []struct {
		name, assets, goos, goarch string
		want                       bool
	}{
		{"no assets yet", `[]`, "linux", "amd64", false},
		{"other platform only", `[{"name":"cartographer-darwin-arm64"},{"name":"sha256sums.txt"}]`, "linux", "amd64", false},
		{"binary but no checksums", `[{"name":"cartographer-linux-amd64"}]`, "linux", "amd64", false},
		{"unix ready", `[{"name":"cartographer-linux-amd64"},{"name":"sha256sums.txt"}]`, "linux", "amd64", true},
		{"windows needs the zip", `[{"name":"cartographer-windows-amd64"},{"name":"sha256sums.txt"}]`, "windows", "amd64", false},
		{"windows ready", `[{"name":"cartographer-windows-amd64.zip"},{"name":"sha256sums.txt"}]`, "windows", "amd64", true},
	}
	for _, c := range cases {
		for _, ch := range []Channel{ChannelInstallSh, ChannelInstallPS1} {
			t.Run(c.name+"/"+string(ch), func(t *testing.T) {
				w := newChannelWorld(t, `[{"tag_name":"v0.28.0","assets":`+c.assets+`}]`)
				res, err, cache := w.check(t, "v0.27.0", ch, c.goos, c.goarch)
				if res.Available != c.want || (err == nil) != c.want {
					t.Fatalf("%+v err=%v", res, err)
				}
				if !c.want {
					notCached(t, cache)
				}
				if h := w.hits["cask"].Load() + w.hits["token"].Load(); h != 0 {
					t.Fatalf("install.sh needs no extra request, made %d", h)
				}
			})
		}
	}
}

func TestContainerWaitsForTheImageTag(t *testing.T) {
	w := newChannelWorld(t, bareRelease)
	res, err, cache := w.check(t, "v0.27.0", ChannelContainer, "linux", "amd64")
	if res.Available || !errors.Is(err, ErrNotInstallable) {
		t.Fatalf("image not pushed: %+v err=%v", res, err)
	}
	notCached(t, cache)

	w.imageTag = "v0.28.0"
	res, err, _ = w.check(t, "v0.27.0", ChannelContainer, "linux", "amd64")
	if err != nil || !res.Available {
		t.Fatalf("image pushed: %+v err=%v", res, err)
	}

	w.tokenOK = false
	res, err, cache = w.check(t, "v0.27.0", ChannelContainer, "linux", "amd64")
	if res.Available || !errors.Is(err, ErrNotInstallable) {
		t.Fatalf("registry refusing the token must stay silent: %+v err=%v", res, err)
	}
	notCached(t, cache)
}

func TestGoInstallAndUnknownAreUnchanged(t *testing.T) {
	for _, ch := range []Channel{ChannelGoInstall, ChannelUnknown, ""} {
		w := newChannelWorld(t, bareRelease)
		res, err, _ := w.check(t, "v0.27.0", ch, "linux", "amd64")
		if err != nil || !res.Available {
			t.Fatalf("%q: %+v err=%v", ch, res, err)
		}
		if h := w.hits["cask"].Load() + w.hits["token"].Load(); h != 0 {
			t.Fatalf("%q made %d extra requests", ch, h)
		}
	}
}

func TestNoExtraRequestWhenAlreadyCurrent(t *testing.T) {
	w := newChannelWorld(t, bareRelease)
	for _, ch := range []Channel{ChannelHomebrew, ChannelContainer} {
		res, err, _ := w.check(t, "v0.28.0", ch, "darwin", "arm64")
		if err != nil || res.Available || res.Latest != "v0.28.0" {
			t.Fatalf("%s: %+v err=%v", ch, res, err)
		}
	}
	if h := w.hits["cask"].Load() + w.hits["token"].Load() + w.hits["manifest"].Load(); h != 0 {
		t.Fatalf("current binary made %d channel requests", h)
	}
}

// A not-yet-installable release must be re-asked on the next run, not held
// for the TTL: the cache keeps what it had and the next Check goes out again.
func TestNotInstallableIsReaskedNextRun(t *testing.T) {
	w := newChannelWorld(t, bareRelease)
	w.cask = "version \"0.27.0\"\n"
	cache := filepath.Join(t.TempDir(), CacheFileName)
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	o := Options{
		CacheFile: cache, APIURL: w.srv.URL + "/api", CaskURL: w.srv.URL + "/cask.rb", Channel: ChannelHomebrew,
		Now: func() time.Time { return now }, Getenv: func(string) string { return "" },
		ForceRefreshOnVersion: "v0.27.0",
	}
	writeCache(cache, cacheEntry{CheckedAt: now.Add(-time.Hour), Latest: "v0.27.0"})
	if res, _ := Check(context.Background(), "v0.27.0", o); res.Available {
		t.Fatalf("announced early: %+v", res)
	}
	w.cask = "version \"0.28.0\"\n"
	now = now.Add(PendingTTL) // the negative cache has run out
	res, err := Check(context.Background(), "v0.27.0", o)
	if err != nil || !res.Available || res.Latest != "v0.28.0" {
		t.Fatalf("second run: %+v err=%v", res, err)
	}
}

// A release found not installable is not asked about again for PendingTTL
// (the notice runs at every session start), but Force and the end of the
// window ask again, and a success clears the entry.
func TestNotInstallableNegativeCache(t *testing.T) {
	w := newChannelWorld(t, bareRelease)
	w.cask = "version \"0.27.0\"\n"
	cache := filepath.Join(t.TempDir(), CacheFileName)
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	o := Options{
		CacheFile: cache, APIURL: w.srv.URL + "/api", CaskURL: w.srv.URL + "/cask.rb", Channel: ChannelHomebrew,
		Now: func() time.Time { return now }, Getenv: func(string) string { return "" },
	}
	run := func() (Result, error) { return Check(context.Background(), "v0.27.0", o) }
	hits := func() int32 { return w.hits["releases"].Load() + w.hits["cask"].Load() }

	res, err := run()
	if res.Available || res.Pending != "v0.28.0" || !errors.Is(err, ErrNotInstallable) || hits() != 2 {
		t.Fatalf("first: %+v err=%v hits=%d", res, err, hits())
	}
	now = now.Add(PendingTTL - time.Second)
	w.cask = "version \"0.28.0\"\n" // the tap catches up, but the window has not ended
	res, err = run()
	if res.Available || res.Pending != "v0.28.0" || !errors.Is(err, ErrNotInstallable) || hits() != 2 {
		t.Fatalf("inside the window: %+v err=%v hits=%d", res, err, hits())
	}
	o.Force = true
	if res, err = run(); err != nil || !res.Available || hits() != 4 {
		t.Fatalf("force: %+v err=%v hits=%d", res, err, hits())
	}
	if _, ok := readPending(cache+".pending", ChannelHomebrew); ok {
		t.Fatal("a success left the pending entry")
	}
	// Another channel never reads this one's entry.
	writePending(cache+".pending", pendingEntry{Tag: "v0.28.0", Channel: ChannelContainer, At: now})
	if _, ok := readPending(cache+".pending", ChannelHomebrew); ok {
		t.Fatal("pending entry crossed channels")
	}
}
