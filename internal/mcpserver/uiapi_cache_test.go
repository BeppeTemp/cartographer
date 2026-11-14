package mcpserver

import (
	"github.com/BeppeTemp/cartographer/internal/kb"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/auth"
)

// The four routes that open a KB answer from the body cache: a repeat computes
// nothing, a write recomputes, and the ETag lets the browser skip the body.
func TestUIAPI_BodyCacheHitMissAndInvalidation(t *testing.T) {
	t.Parallel()
	handler, k := perfHandler(t)
	backdateTree(t, k.DataRoot())
	paths := []string{"/kbs/docs/graph", "/kbs/docs/graph/clusters", "/kbs/docs/overview", "/kbs/docs/artifacts"}
	first := map[string]string{}
	for _, p := range paths {
		rr := getUI(t, handler, UIAPIPrefix+p, "whole")
		if rr.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", p, rr.Code, rr.Body.String())
		}
		first[p] = rr.Header().Get("ETag")
		if first[p] == "" || rr.Header().Get("Cache-Control") != "private, no-cache" {
			t.Errorf("%s: ETag %q Cache-Control %q", p, first[p], rr.Header().Get("Cache-Control"))
		}
	}
	computes := uiBodyComputes(k)
	for _, p := range paths {
		rr := getUI(t, handler, UIAPIPrefix+p, "whole")
		if rr.Header().Get("ETag") != first[p] {
			t.Errorf("%s: ETag changed with nothing written", p)
		}
	}
	if n := uiBodyComputes(k); n != computes {
		t.Fatalf("a warm open computed %d bodies", n-computes)
	}

	// A write moves the generation: every route recomputes, with a new ETag.
	page := "---\ntype: Topic\ntitle: Fresh\n---\nWritten after the cache filled.\n"
	if err := os.WriteFile(filepath.Join(k.DataRoot(), "visible", "fresh.md"), []byte(page), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/kbs/docs/graph", "/kbs/docs/overview"} {
		rr := getUI(t, handler, UIAPIPrefix+p, "whole")
		if rr.Header().Get("ETag") == first[p] {
			t.Errorf("%s: ETag survived a write", p)
		}
	}
	if n := uiBodyComputes(k); n <= computes {
		t.Fatal("a write did not invalidate the cache")
	}
}

func TestUIAPI_ETagAnswers304(t *testing.T) {
	t.Parallel()
	handler, k := perfHandler(t)
	backdateTree(t, k.DataRoot())
	url := UIAPIPrefix + "/kbs/docs/graph"
	etag := getUI(t, handler, url, "whole").Header().Get("ETag")
	req := httptest.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer whole")
	req.Header.Set("If-None-Match", etag)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotModified || rr.Body.Len() != 0 || rr.Header().Get("ETag") != etag {
		t.Fatalf("If-None-Match: %d %q etag %q", rr.Code, rr.Body.String(), rr.Header().Get("ETag"))
	}
	req.Header.Set("If-None-Match", `"other"`)
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || rr.Body.Len() == 0 {
		t.Fatalf("a stale validator got %d", rr.Code)
	}
}

// Two principals with different views never share an entry or an ETag, and a
// validator earned by one never opens the other's body (D226, D244).
func TestUIAPI_BodyCacheIsolatesPrincipals(t *testing.T) {
	t.Parallel()
	handler, k := perfHandler(t)
	backdateTree(t, k.DataRoot())
	for _, p := range []string{"/kbs/docs/graph", "/kbs/docs/graph/clusters"} {
		whole := getUI(t, handler, UIAPIPrefix+p, "whole")
		narrow := getUI(t, handler, UIAPIPrefix+p, "narrow")
		if whole.Header().Get("ETag") == narrow.Header().Get("ETag") {
			t.Errorf("%s: both principals got one ETag", p)
		}
		if narrow.Body.String() == whole.Body.String() {
			t.Errorf("%s: the narrowed principal got the whole body", p)
		}
		req := httptest.NewRequest(http.MethodGet, UIAPIPrefix+p, nil)
		req.Header.Set("Authorization", "Bearer narrow")
		req.Header.Set("If-None-Match", whole.Header().Get("ETag"))
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK || rr.Body.String() != narrow.Body.String() {
			t.Errorf("%s: another principal's validator answered %d", p, rr.Code)
		}
		// Served again from the cache, still its own.
		if again := getUI(t, handler, UIAPIPrefix+p, "narrow"); again.Body.String() != narrow.Body.String() {
			t.Errorf("%s: the narrowed body changed on a hit", p)
		}
	}
	// overview and artifacts are cached only for a whole-KB caller.
	before := uiBodyComputes(k)
	getUI(t, handler, UIAPIPrefix+"/kbs/docs/overview", "narrow")
	getUI(t, handler, UIAPIPrefix+"/kbs/docs/overview", "narrow")
	if uiBodyComputes(k) != before {
		t.Error("a narrowed overview went through the shared cache")
	}
}

// The KB list carries what the Atlas needs to pick flat or clustered before
// any graph request, per principal.
func TestUIAPI_KBListCarriesConceptTotal(t *testing.T) {
	t.Parallel()
	handler, _ := perfHandler(t)
	total := func(token string) float64 {
		rows := decodeUI(t, getUI(t, handler, UIAPIPrefix+"/kbs", token))["kbs"].([]interface{})
		row := rows[0].(map[string]interface{})
		if row["cluster_threshold"] == nil {
			t.Fatalf("%s: no cluster_threshold: %v", token, row)
		}
		return row["concepts_total"].(float64)
	}
	overview := func(token string) float64 {
		body := decodeUI(t, getUI(t, handler, UIAPIPrefix+"/kbs/docs/overview", token))
		return body["concepts"].(map[string]interface{})["total"].(float64)
	}
	for _, token := range []string{"whole", "narrow"} {
		if got, want := total(token), overview(token); got != want {
			t.Errorf("%s: list says %v concepts, overview %v", token, got, want)
		}
	}
	if total("whole") <= total("narrow") {
		t.Error("a narrowed principal's count is not smaller")
	}
}

// The warm path recomputes nothing: after WarmUI the first open of a KB, as a
// whole-KB caller, is a cache hit.
func TestUIAPI_WarmUIFillsTheCache(t *testing.T) {
	t.Parallel()
	k := uiFixtureKB(t, "docs")
	backdateTree(t, k.DataRoot())
	multi := NewMultiKBServer("test")
	multi.MountKB("docs", func(s *Server) { RegisterKBTools(s, k, Deps{}) })
	multi.EnableWeb(nil)
	ts := auth.NewScopedTokenStore([]auth.ScopedToken{
		{Token: "whole", Policy: auth.Policy{Permissions: []auth.Permission{{KB: "docs"}}}},
	})
	handler := ts.Middleware(multi.Handler())
	multi.warmKB("docs", multi.servers["docs"])
	computes := uiBodyComputes(k)
	if computes == 0 {
		t.Fatal("the warm-up computed nothing")
	}
	// The flat graph exactly as the Atlas asks for it (web/src/api/client.ts GRAPH_ALL_NODES).
	graph := "/kbs/docs/graph?limit=" + strconv.Itoa(kb.MaxGraphNodeLimit)
	for _, p := range []string{graph, "/kbs/docs/overview", "/kbs/docs/artifacts"} {
		rr := getUI(t, handler, UIAPIPrefix+p, "whole")
		if rr.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", p, rr.Code, rr.Body.String())
		}
	}
	if n := uiBodyComputes(k); n != computes {
		t.Errorf("the first open recomputed %d bodies after the warm-up", n-computes)
	}
}

// cacheControlOK accepts what the UI API sends: no-store, or, for the cached
// bodies (D-new-atlas-graph-preload), private and revalidated on every use.
func cacheControlOK(v string) bool { return v == "no-store" || v == "private, no-cache" }

// A parameter a route does not read never adds an entry: such a request is
// answered without the cache, so varying it cannot pin bodies in memory.
func TestUIAPI_BodyCacheKeysOnlyTheParametersARouteReads(t *testing.T) {
	t.Parallel()
	handler, k := perfHandler(t)
	backdateTree(t, k.DataRoot())
	getUI(t, handler, UIAPIPrefix+"/kbs/docs/graph?limit=50", "whole")
	cache := uiBodyCacheOf(k)
	cache.mu.Lock()
	before := len(cache.entries)
	cache.mu.Unlock()
	for _, q := range []string{"?limit=50&x=1", "?limit=50&x=2", "?y=3"} {
		if rr := getUI(t, handler, UIAPIPrefix+"/kbs/docs/graph"+q, "whole"); rr.Code != http.StatusOK {
			t.Fatalf("%s: %d", q, rr.Code)
		}
	}
	cache.mu.Lock()
	after, bytes := len(cache.entries), cache.bytes
	cache.mu.Unlock()
	if after != before {
		t.Errorf("unread parameters added %d cache entries", after-before)
	}
	if bytes <= 0 || bytes > uiBodyCacheBytes {
		t.Errorf("cache bytes %d outside (0, %d]", bytes, uiBodyCacheBytes)
	}
}
