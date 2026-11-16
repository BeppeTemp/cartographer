package mcpserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/auth"
)

// #2150: a key that appears in credentials_dir after the server started must
// read as enabled on every surface: kb_status (kbCapabilities), /health (which
// `doctor` reads) and the Atlas KB list. The last two used to serve the map
// computed once at mount.
func TestSecretsStateIsLiveOnEverySurface(t *testing.T) {
	t.Parallel()
	multi := NewMultiKBServer("test")
	k := uiFixtureKB(t, "docs")
	var keyFile atomic.Value
	keyFile.Store("")
	k.SetSopsAgeKeyResolver(func() string { return keyFile.Load().(string) })
	multi.MountKB("docs", func(s *Server) { RegisterKBTools(s, k, Deps{}) })
	multi.EnableWeb(nil)
	multi.capsCache.ttl = time.Nanosecond // each read derives: the TTL has its own test
	h := auth.NewTokenStore(nil).Middleware(multi.Handler())

	state := func() (status, health, atlas string) {
		status = kbCapabilities(k)["secrets"].State
		var hb struct {
			KBs []KBInfo `json:"kbs"`
		}
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/health", nil))
		if err := json.Unmarshal(rr.Body.Bytes(), &hb); err != nil || len(hb.KBs) != 1 {
			t.Fatalf("/health: %v %s", err, rr.Body.String())
		}
		health = hb.KBs[0].Capabilities["secrets"].State
		var ab uiKBList
		rr = getUI(t, h, UIAPIPrefix+"/kbs", "")
		if err := json.Unmarshal(rr.Body.Bytes(), &ab); err != nil || len(ab.KBs) != 1 {
			t.Fatalf("atlas list: %v %s", err, rr.Body.String())
		}
		atlas = ab.KBs[0].Capabilities["secrets"].State
		return
	}

	if s, hl, a := state(); s != "disabled" || hl != "disabled" || a != "disabled" {
		t.Fatalf("before the key: kb_status %q, /health %q, atlas %q; want all disabled", s, hl, a)
	}
	keyFile.Store("/nonexistent/docs.age") // what `kb secrets init` writes, with no restart
	if s, hl, a := state(); s != "enabled" || hl != "enabled" || a != "enabled" {
		t.Errorf("after the key: kb_status %q, /health %q, atlas %q; want all enabled", s, hl, a)
	}
}

// Anonymous /health and /ready, and the routed error, never use the
// capabilities: they must not read the KB's secrets/ files (a poller would
// make the server parse them on every probe). An identified /health and the
// Atlas list derive them at most once per TTL.
func TestCapabilitiesAreDerivedOnlyWhereReturnedAndCached(t *testing.T) {
	t.Parallel()
	multi := NewMultiKBServer("test")
	k := uiFixtureKB(t, "docs")
	multi.MountKB("docs", func(s *Server) { RegisterKBTools(s, k, Deps{}) })
	multi.EnableWeb(nil)
	open := auth.NewTokenStore(nil).Middleware(multi.Handler()) // auth off: the local admin
	reads := func() int { multi.capsCache.mu.Lock(); defer multi.capsCache.mu.Unlock(); return multi.capsCache.reads }

	anon := multi.Handler() // no principal attached: an anonymous probe
	for _, path := range []string{"/health", "/ready"} {
		for i := 0; i < 3; i++ {
			rr := httptest.NewRecorder()
			anon.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, path, nil))
			if rr.Code != http.StatusOK {
				t.Fatalf("%s: %d", path, rr.Code)
			}
		}
	}
	multi.describeRoutedKBs([]string{"docs"})
	if n := reads(); n != 0 {
		t.Fatalf("anonymous /health, /ready and the routed error derived capabilities %d times, want 0", n)
	}

	for i := 0; i < 5; i++ {
		open.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/health", nil))
		getUI(t, open, UIAPIPrefix+"/kbs", "")
	}
	if n := reads(); n != 1 {
		t.Errorf("10 identified reads derived capabilities %d times, want 1 within the TTL", n)
	}
	now := time.Now()
	multi.capsCache.get("docs", k, now.Add(capabilitiesTTL+time.Second))
	if n := reads(); n != 2 {
		t.Errorf("a read after the TTL derived %d times in all, want 2", n)
	}
}
