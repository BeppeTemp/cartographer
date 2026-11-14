package mcpserver

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/kb"
)

// The encoded bodies of the four routes the Atlas reads to open a KB
// (/graph, /graph/clusters, /overview, /artifacts) are kept per KB, so a warm
// open costs a map lookup instead of a ranking, a lint stamp and an encode
// (D2077).
//
// Trap: the key holds the caller's visible-set fingerprint, never the
// principal's name or token: two principals with different rules never share
// an entry, two with the same view do (D226, D244). overview and artifacts
// depend on more than the visible ids (collections, lint findings, the
// artifact files), so they are served from the cache only to a caller that
// sees the whole KB; everyone else computes, as before.

// uiBodyCacheMax bounds the entries one KB keeps: a handful of principals and
// scopes, not one per request.
const uiBodyCacheMax = 64

// uiBodyCacheBytes bounds the bodies one KB keeps, in bytes: a graph body at
// the largest limit is several MB, so a count alone would let a caller pin
// 64 of them.
const uiBodyCacheBytes = 48 << 20

// uiCachedParams is what each cached route reads from its query: the key is
// built from these alone, and a request carrying any other parameter is
// answered without the cache, so no variation a handler ignores can add an
// entry.
var uiCachedParams = map[string][]string{
	"graph":     {"scope", "limit"},
	"clusters":  {"scope", "unfold", "focus"},
	"overview":  nil,
	"artifacts": nil,
}

const uiWholeFingerprint = "whole"

// uiBodyEntry is one cached response: valid while stamp is the KB's stamp.
type uiBodyEntry struct {
	stamp string
	body  []byte
	etag  string
}

type uiBodyCache struct {
	mu       sync.Mutex
	entries  map[string]*uiBodyEntry
	bytes    int
	computes atomic.Int64
}

var uiBodyCaches sync.Map // *kb.KB -> *uiBodyCache

func uiBodyCacheOf(k *kb.KB) *uiBodyCache {
	if c, ok := uiBodyCaches.Load(k); ok {
		return c.(*uiBodyCache)
	}
	c, _ := uiBodyCaches.LoadOrStore(k, &uiBodyCache{entries: map[string]*uiBodyEntry{}})
	return c.(*uiBodyCache)
}

// UIBodyComputes reports how many responses of this KB the cache had to
// compute rather than serve: a test asserts a warm open computes none.
func uiBodyComputes(k *kb.KB) int64 { return uiBodyCacheOf(k).computes.Load() }

// uiGraphStamp is the stamp of the graph routes: the graph generation, read
// with a strict validation like every reader but the revision poll (D241), so
// an edit made out of band is seen by the next request.
func uiGraphStamp(k *kb.KB) (string, error) { return uiStampAt(k, 0) }

// uiStampAt is uiGraphStamp trusting a validation younger than maxAge.
func uiStampAt(k *kb.KB, maxAge time.Duration) (string, error) {
	gen, err := k.GraphGenerationWithin(maxAge)
	if err != nil {
		return "", err
	}
	return uiRevisionBoot + "-" + strconv.FormatUint(gen, 10), nil
}

// uiWholeStamp is the stamp of the routes that also read the files around the
// concepts (collections' metadata, artifacts, usage): the graph generation and
// the lint inputs stamp, which holds every other file (D294).
func uiWholeStamp(k *kb.KB) (string, error) {
	g, err := uiGraphStamp(k)
	if err != nil {
		return "", err
	}
	return g + "|" + lintInputsStamp(k), nil
}

// uiVisibleFingerprint names the set of concepts the caller sees.
func uiVisibleFingerprint(ctx context.Context, k *kb.KB) (string, error) {
	if WholeVisible(ctx, k, false) {
		return uiWholeFingerprint, nil
	}
	catalog, err := k.ConceptCatalog()
	if err != nil {
		return "", err
	}
	ids := make([]string, 0, len(catalog))
	for _, c := range catalog {
		if Visible(ctx, k, string(c.ID)) {
			ids = append(ids, string(c.ID))
		}
	}
	sort.Strings(ids)
	h := sha256.New()
	for _, id := range ids {
		h.Write([]byte(id))
		h.Write([]byte{0})
	}
	return "set:" + hex.EncodeToString(h.Sum(nil)), nil
}

// recordedResponse captures what a handler writes.
type recordedResponse struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (r *recordedResponse) Header() http.Header { return r.header }
func (r *recordedResponse) WriteHeader(s int) {
	if r.status == 0 {
		r.status = s
	}
}
func (r *recordedResponse) Write(b []byte) (int, error) {
	r.WriteHeader(http.StatusOK)
	return r.body.Write(b)
}

// serveUICached answers a route from the per-KB body cache. inner is the
// route's handler, run only on a miss; a response that is not a 200 is never
// kept. The answer carries an ETag, and a matching If-None-Match gets 304.
func serveUICached(w http.ResponseWriter, r *http.Request, k *kb.KB, route string, wholeOnly bool,
	stamp func(*kb.KB) (string, error), inner func(http.ResponseWriter, *http.Request)) {
	fp, err := uiVisibleFingerprint(r.Context(), k)
	if err != nil || (wholeOnly && fp != uiWholeFingerprint) {
		inner(w, r)
		return
	}
	st, err := stamp(k)
	if err != nil {
		inner(w, r)
		return
	}
	query, ok := canonicalQuery(r.URL.Query(), uiCachedParams[route])
	if !ok {
		inner(w, r)
		return
	}
	c := uiBodyCacheOf(k)
	key := route + "?" + query + "#" + fp

	c.mu.Lock()
	e := c.entries[key]
	c.mu.Unlock()
	if e == nil || e.stamp != st {
		rec := &recordedResponse{header: http.Header{}}
		c.computes.Add(1)
		inner(rec, r)
		if rec.status != http.StatusOK {
			// An error or a refusal: pass it on untouched, keep nothing.
			for name, vals := range rec.header {
				w.Header()[name] = vals
			}
			w.WriteHeader(rec.status)
			_, _ = w.Write(rec.body.Bytes())
			return
		}
		sum := sha256.Sum256([]byte(st + "\x00" + key))
		e = &uiBodyEntry{stamp: st, body: rec.body.Bytes(), etag: `W/"` + hex.EncodeToString(sum[:12]) + `"`}
		c.mu.Lock()
		if old := c.entries[key]; old != nil {
			c.bytes -= len(old.body)
		}
		if len(c.entries) >= uiBodyCacheMax || c.bytes+len(e.body) > uiBodyCacheBytes {
			c.entries = map[string]*uiBodyEntry{}
			c.bytes = 0
		}
		if len(e.body) <= uiBodyCacheBytes {
			c.entries[key] = e
			c.bytes += len(e.body)
		}
		c.mu.Unlock()
	}

	writeUIHeaders(w)
	h := w.Header()
	// The body is the caller's projection: private, and always revalidated, so
	// the browser's copy is never used without asking (no-store would forbid
	// the conditional request altogether).
	h.Set("Cache-Control", "private, no-cache")
	h.Set("ETag", e.etag)
	if etagMatches(r.Header.Get("If-None-Match"), e.etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(e.body)
}

func etagMatches(header, etag string) bool {
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimPrefix(strings.TrimSpace(part), "W/")
		if part == "*" || part == strings.TrimPrefix(etag, "W/") {
			return true
		}
	}
	return false
}

// canonicalQuery encodes the parameters a route reads, in a stable order; ok
// is false when the query holds any other one.
func canonicalQuery(q url.Values, allowed []string) (string, bool) {
	kept := url.Values{}
	for name, vals := range q {
		if !slices.Contains(allowed, name) {
			return "", false
		}
		kept[name] = vals
	}
	return kept.Encode(), true
}

// WarmUI computes, for each mounted KB, the bodies the Atlas asks for first,
// as the whole-KB caller: the first open of a KB then answers from the cache.
// Called once after the KBs are mounted, in the background; a write
// invalidates lazily (the next read recomputes), never ahead of a request.
func (m *MultiKBServer) WarmUI() {
	for name, srv := range m.servers {
		if srv.kbRef == nil {
			continue
		}
		go m.warmKB(name, srv)
	}
}

func (m *MultiKBServer) warmKB(_ string, srv *Server) {
	k := srv.kbRef
	ctx := auth.ContextWithPrincipal(withOperator(context.Background()), auth.Principal{ID: "warm", Policy: auth.Policy{Admin: true}})
	get := func(target string, h func(http.ResponseWriter, *http.Request)) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err != nil {
			return
		}
		h(&recordedResponse{header: http.Header{}}, req)
	}
	catalog, err := k.ConceptCatalog()
	if err != nil {
		return
	}
	// The same queries the Atlas sends, so the keys match (web/src/api/client.ts:
	// the flat graph asks for every node, the clusters nothing).
	if len(catalog) > kb.ClusterThreshold {
		get("/", func(w http.ResponseWriter, r *http.Request) { uiGraphClustersCached(w, r, k) })
	} else {
		get("/?limit="+strconv.Itoa(kb.MaxGraphNodeLimit), func(w http.ResponseWriter, r *http.Request) { uiGraphCached(w, r, k) })
	}
	get("/", func(w http.ResponseWriter, r *http.Request) { m.uiOverviewCached(w, r, srv) })
	get("/", func(w http.ResponseWriter, r *http.Request) { m.uiArtifactsCached(w, r, srv) })
}

func uiGraphCached(w http.ResponseWriter, r *http.Request, k *kb.KB) {
	serveUICached(w, r, k, "graph", false, uiGraphStamp, func(w http.ResponseWriter, r *http.Request) { uiGraph(w, r, k) })
}

func uiGraphClustersCached(w http.ResponseWriter, r *http.Request, k *kb.KB) {
	serveUICached(w, r, k, "clusters", false, uiGraphStamp, func(w http.ResponseWriter, r *http.Request) { uiGraphClusters(w, r, k) })
}

func (m *MultiKBServer) uiOverviewCached(w http.ResponseWriter, r *http.Request, srv *Server) {
	serveUICached(w, r, srv.kbRef, "overview", true, uiWholeStamp, func(w http.ResponseWriter, r *http.Request) { m.uiOverview(w, r, srv) })
}

func (m *MultiKBServer) uiArtifactsCached(w http.ResponseWriter, r *http.Request, srv *Server) {
	serveUICached(w, r, srv.kbRef, "artifacts", true, uiWholeStamp, func(w http.ResponseWriter, r *http.Request) { m.uiArtifacts(w, r, srv) })
}

// uiVisibleTotal is the number of concepts the caller sees in k, the figure
// the KB list carries so the Atlas picks flat or clustered before it asks for
// any graph. It rides the same stamp as the graph bodies, so a list reload
// walks nothing while the KB is unchanged.
func uiVisibleTotal(ctx context.Context, k *kb.KB) (int, bool) {
	// The count only chooses flat or clustered: it trusts a recent validation,
	// as the revision poll does.
	st, err := uiStampAt(k, uiRevisionMaxAge)
	if err != nil {
		return 0, false
	}
	fp := uiWholeFingerprint
	if !WholeVisible(ctx, k, false) {
		// A narrowed caller pays one filtered walk to name its view; the
		// result is cached under that view.
		if fp, err = uiVisibleFingerprint(ctx, k); err != nil {
			return 0, false
		}
	}
	c := uiBodyCacheOf(k)
	key := "total#" + fp
	c.mu.Lock()
	e := c.entries[key]
	c.mu.Unlock()
	if e != nil && e.stamp == st {
		n, _ := strconv.Atoi(string(e.body))
		return n, true
	}
	catalog, err := k.ConceptCatalog()
	if err != nil {
		return 0, false
	}
	n := 0
	for _, cc := range catalog {
		if Visible(ctx, k, string(cc.ID)) {
			n++
		}
	}
	c.mu.Lock()
	if old := c.entries[key]; old != nil {
		c.bytes -= len(old.body)
	}
	if len(c.entries) >= uiBodyCacheMax {
		// Both together, as serveUICached does: a byte count left over from the
		// dropped bodies would make the next real body trip the byte cap.
		c.entries = map[string]*uiBodyEntry{}
		c.bytes = 0
	}
	body := []byte(strconv.Itoa(n))
	c.entries[key] = &uiBodyEntry{stamp: st, body: body}
	c.bytes += len(body)
	c.mu.Unlock()
	return n, true
}
