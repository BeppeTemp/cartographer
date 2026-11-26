package mcpserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/kb"
)

// revisionFixture mounts kb-a (links to kb-b), kb-b and kb-c (unrelated) and
// returns a handler with a token that reads all three whole, one that cannot
// read kb-b and one that reads kb-b only in part.
func revisionFixture(t *testing.T) (h http.Handler, a, b, c *kb.KB, rev func(token, name string) string) {
	t.Helper()
	// Not parallel: it narrows the poll's trust window, a package variable.
	setRevisionMaxAge(t, 0)
	a, b, c = uiFixtureKB(t, "kb-a"), uiFixtureKB(t, "kb-b"), uiFixtureKB(t, "kb-c")
	m := kb.NewMounts(map[string]*kb.KB{"kb-a": a, "kb-b": b, "kb-c": c}, []string{"kb-a", "kb-b", "kb-c"}, nil)
	for _, k := range []*kb.KB{a, b, c} {
		k.SetMounts(m)
	}
	putThroughServer(t, a, "visible/linker", "See [[kb:kb-b/visible/alpha]].\n")
	whole := func(names ...string) []auth.Permission {
		var p []auth.Permission
		for _, n := range names {
			p = append(p, auth.Permission{KB: n})
		}
		return p
	}
	ts := auth.NewScopedTokenStore([]auth.ScopedToken{
		{Token: "admin", Policy: auth.Policy{Permissions: whole("kb-a", "kb-b", "kb-c")}},
		{Token: "noB", Policy: auth.Policy{Permissions: whole("kb-a", "kb-c")}},
		{Token: "partB", Policy: auth.Policy{Permissions: append(whole("kb-a", "kb-c"), auth.Permission{KB: "kb-b", Maps: []string{"visible"}})}},
	})
	multi := NewMultiKBServer("test")
	for name, k := range map[string]*kb.KB{"kb-a": a, "kb-b": b, "kb-c": c} {
		multi.MountKB(name, func(s *Server) { RegisterKBTools(s, k, Deps{}) })
	}
	multi.EnableWeb(nil)
	h = ts.Middleware(multi.Handler())
	rev = func(token, name string) string {
		t.Helper()
		return decodeUI(t, getUI(t, h, UIAPIPrefix+"/kbs/"+name+"/revision", token))["revision"].(string)
	}
	return
}

// AC-atlas-kb-map-2
func TestRevisionFollowsLinkedKBs(t *testing.T) {
	_, _, b, c, rev := revisionFixture(t)
	base := rev("admin", "kb-a")
	if !strings.Contains(base, "-x") {
		t.Fatalf("a cross-linked KB has no suffix: %q", base)
	}
	if again := rev("admin", "kb-a"); again != base {
		t.Fatalf("revision moved with nothing changed: %q -> %q", base, again)
	}
	putThroughServer(t, b, "visible/added", "new page\n")
	afterAdd := rev("admin", "kb-a")
	if afterAdd == base {
		t.Fatal("revision of kb-a did not move when linked kb-b gained a page")
	}
	putThroughServer(t, b, "visible/added", "")
	if rev("admin", "kb-a") == afterAdd {
		t.Fatal("revision of kb-a did not move when linked kb-b lost a page")
	}
	// The other direction: kb-b gains a link to kb-a; kb-a is linked from it.
	before := rev("admin", "kb-a")
	putThroughServer(t, b, "visible/back", "Back to [[kb:kb-a/visible/alpha]].\n")
	if rev("admin", "kb-a") == before {
		t.Fatal("revision of kb-a did not move when kb-b gained a link to it")
	}
	// An unrelated KB moves nothing.
	before = rev("admin", "kb-a")
	putThroughServer(t, c, "visible/other", "unrelated\n")
	if rev("admin", "kb-a") != before {
		t.Fatal("revision of kb-a moved for an unrelated KB")
	}
}

// AC-atlas-kb-map-3
func TestRevisionBlindToUnreadableKB(t *testing.T) {
	_, _, b, _, rev := revisionFixture(t)
	for _, tok := range []string{"noB", "partB"} {
		before := rev(tok, "kb-a")
		if strings.Contains(before, "-x") {
			t.Fatalf("%s: suffix for a KB it cannot read whole: %q", tok, before)
		}
		putThroughServer(t, b, "visible/added", "new page\n")
		putThroughServer(t, b, "visible/back", "Back to [[kb:kb-a/visible/alpha]].\n")
		if after := rev(tok, "kb-a"); after != before {
			t.Fatalf("%s: revision moved for kb-b: %q -> %q", tok, before, after)
		}
		putThroughServer(t, b, "visible/added", "")
		putThroughServer(t, b, "visible/back", "")
	}
	// A KB with no cross link has today's token, no suffix.
	if tok := rev("admin", "kb-c"); strings.Contains(tok, "-x") {
		t.Fatalf("kb-c has no cross link yet carries a suffix: %q", tok)
	}
}

// ---- GET /kb-map ----

func kbMapPerm(kbName string, maps ...string) auth.Permission {
	return auth.Permission{KB: kbName, Maps: maps}
}

func kbMapTokens() *auth.TokenStore {
	pol := func(p ...auth.Permission) auth.Policy { return auth.Policy{Permissions: p} }
	return auth.NewScopedTokenStore([]auth.ScopedToken{
		{Token: "admin", Policy: pol(kbMapPerm("kb-a"), kbMapPerm("kb-b"), kbMapPerm("core-kb"), kbMapPerm("kb-hidden"))},
		{Token: "noHidden", Policy: pol(kbMapPerm("kb-a"), kbMapPerm("kb-b"), kbMapPerm("core-kb"))},
		{Token: "bScoped", Policy: pol(kbMapPerm("kb-a"), kbMapPerm("kb-b", "pub"), kbMapPerm("core-kb"))},
		{Token: "aScoped", Policy: pol(kbMapPerm("kb-a", "pub"), kbMapPerm("kb-b"), kbMapPerm("core-kb"))},
		{Token: "aOnly", Policy: pol(kbMapPerm("kb-a"))},
		{Token: "nobody", Policy: pol(kbMapPerm("elsewhere"))},
	})
}

// kbMapFixture mounts names (each with a git origin and the declared deps),
// two maps "pub" and "secret" per KB, and returns the handler and the KBs.
func kbMapFixture(t *testing.T, names []string, deps map[string][][2]string) (http.Handler, *relFixture) {
	t.Helper()
	f := newRelFixture(t, names, deps)
	multi := NewMultiKBServer("test")
	for _, n := range names {
		k := f.byName[n]
		for _, c := range []string{"pub", "secret"} {
			if err := k.CreateMap(c, c, "map", nil); err != nil {
				t.Fatal(err)
			}
		}
		multi.MountKB(n, func(s *Server) { RegisterKBTools(s, k, Deps{}) })
	}
	multi.EnableWeb(nil)
	return kbMapTokens().Middleware(multi.Handler()), f
}

func kbMapGet(t *testing.T, h http.Handler, token string) (uiKBMap, string) {
	t.Helper()
	rr := getUI(t, h, UIAPIPrefix+"/kb-map", token)
	if rr.Code != http.StatusOK {
		t.Fatalf("%s: status %d: %s", token, rr.Code, rr.Body.String())
	}
	var out uiKBMap
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out, rr.Body.String()
}

func kbMapNames(m uiKBMap) []string {
	out := []string{}
	for _, n := range m.Nodes {
		out = append(out, n.Name)
	}
	return out
}

var kbMapDeps = map[string][][2]string{
	"kb-a": {{"core-kb", "core-kb"}, {"kb-hidden", "kb-hidden"}},
	"kb-b": {{"core-kb", "core-kb"}, {"kb-a", "kb-a"}},
}

// kbMapPages writes the pages every scenario shares: three links kb-a -> kb-b
// and one from a page of a hidden map, one link to and one from kb-hidden.
func kbMapPages(t *testing.T, f *relFixture) {
	t.Helper()
	a, b := f.byName["kb-a"], f.byName["kb-b"]
	putThroughServer(t, b, "pub/t", "target\n")
	putThroughServer(t, b, "secret/t", "hidden target\n")
	putThroughServer(t, a, "pub/ok", "[[kb:kb-b/pub/t]]\n")
	putThroughServer(t, a, "pub/missing", "[[kb:kb-b/pub/nope]]\n")
	putThroughServer(t, a, "pub/hid", "[[kb:kb-b/secret/t]]\n")
	putThroughServer(t, a, "secret/src", "[[kb:kb-b/pub/t]]\n")
	putThroughServer(t, a, "pub/odd", "[[kb:kb-a/pub/ok]] [[kb:kb-b@host/pub/t]] [[kb:bad name/x]] [[kb:kb-hidden/pub/t]]\n")
	if h, ok := f.byName["kb-hidden"]; ok {
		putThroughServer(t, h, "pub/t", "hidden kb\n")
		putThroughServer(t, h, "pub/back", "[[kb:kb-a/pub/ok]] [[kb:kb-b/pub/t]]\n")
	}
}

func linkCount(m uiKBMap, from, to string) int {
	for _, l := range m.Links {
		if l.From == from && l.To == to {
			return l.Count
		}
	}
	return 0
}

// AC-atlas-kb-map-4
func TestKBMapUnreadableEqualsAbsent(t *testing.T) {
	t.Parallel()
	with, fw := kbMapFixture(t, []string{"kb-a", "kb-b", "core-kb", "kb-hidden"}, kbMapDeps)
	without, fo := kbMapFixture(t, []string{"kb-a", "kb-b", "core-kb"}, kbMapDeps)
	kbMapPages(t, fw)
	kbMapPages(t, fo)
	for _, tok := range []string{"noHidden", "bScoped", "aScoped", "aOnly", "nobody"} {
		_, x := kbMapGet(t, with, tok)
		_, y := kbMapGet(t, without, tok)
		if x != y {
			t.Errorf("%s: kb-hidden mounted changes the answer:\n%s\n%s", tok, x, y)
		}
	}
	// The admin does see it: the test would pass vacuously otherwise.
	m, _ := kbMapGet(t, with, "admin")
	if !slices.Contains(kbMapNames(m), "kb-hidden") {
		t.Fatalf("admin does not see kb-hidden: %v", kbMapNames(m))
	}
}

// AC-atlas-kb-map-5
func TestKBMapNodesEqualKBList(t *testing.T) {
	t.Parallel()
	h, f := kbMapFixture(t, []string{"kb-a", "kb-b", "core-kb", "kb-hidden"}, kbMapDeps)
	kbMapPages(t, f)
	for _, tok := range []string{"admin", "noHidden", "bScoped", "aScoped", "aOnly", "nobody"} {
		m, _ := kbMapGet(t, h, tok)
		list := []string{}
		for _, row := range decodeUI(t, getUI(t, h, UIAPIPrefix+"/kbs", tok))["kbs"].([]any) {
			list = append(list, row.(map[string]any)["name"].(string))
		}
		if !slices.Equal(kbMapNames(m), list) {
			t.Errorf("%s: nodes %v, /kbs %v", tok, kbMapNames(m), list)
		}
		for _, r := range m.Relations {
			if !slices.Contains(list, r.From) || !slices.Contains(list, r.To) {
				t.Errorf("%s: relation %v has an end that is not a node", tok, r)
			}
		}
		for _, l := range m.Links {
			if !slices.Contains(list, l.From) || !slices.Contains(list, l.To) || l.Count < 1 || l.From == l.To {
				t.Errorf("%s: link %v", tok, l)
			}
		}
		if m.Nodes == nil || m.Relations == nil || m.Links == nil {
			t.Errorf("%s: a slice is null", tok)
		}
	}
}

// AC-atlas-kb-map-6
func TestKBMapLinkCounting(t *testing.T) {
	t.Parallel()
	h, f := kbMapFixture(t, []string{"kb-a", "kb-b", "core-kb", "kb-hidden"}, kbMapDeps)
	kbMapPages(t, f)
	// ok, hid and secret/src resolve for a whole reader; "missing" never counts.
	for tok, want := range map[string]int{"admin": 3, "bScoped": 2, "aScoped": 2, "aOnly": 0} {
		m, _ := kbMapGet(t, h, tok)
		if got := linkCount(m, "kb-a", "kb-b"); got != want {
			t.Errorf("%s: kb-a -> kb-b = %d, want %d (%v)", tok, got, want, m.Links)
		}
		// Self-named, @server, malformed links add nothing, and kb-b -> kb-a has no page.
		if got := linkCount(m, "kb-a", "kb-a") + linkCount(m, "kb-b", "kb-a"); got != 0 {
			t.Errorf("%s: spurious edge: %v", tok, m.Links)
		}
	}
	m, _ := kbMapGet(t, h, "admin")
	if linkCount(m, "kb-a", "kb-hidden") != 1 || linkCount(m, "kb-hidden", "kb-a") != 1 || linkCount(m, "kb-hidden", "kb-b") != 1 {
		t.Errorf("admin links: %v", m.Links)
	}
	if m.Truncated {
		t.Error("truncated without a budget")
	}
}

// AC-atlas-kb-map-7
func TestKBMapRelationsAndLayer(t *testing.T) {
	t.Parallel()
	h, f := kbMapFixture(t, []string{"kb-a", "kb-b", "core-kb", "kb-hidden"}, kbMapDeps)
	kbMapPages(t, f)
	m, _ := kbMapGet(t, h, "noHidden")
	wantRel := []uiKBMapRelation{{"kb-a", "core-kb"}, {"kb-b", "core-kb"}, {"kb-b", "kb-a"}}
	if !slices.Equal(m.Relations, wantRel) {
		t.Errorf("relations = %v, want %v (never the declared kb-hidden)", m.Relations, wantRel)
	}
	layers := map[string]int{}
	for _, n := range m.Nodes {
		layers[n.Name] = n.Layer
	}
	if layers["core-kb"] != 0 || layers["kb-a"] != 1 || layers["kb-b"] != 2 {
		t.Errorf("layers = %v", layers)
	}
	// A reader of kb-b in part: no edge from kb-b, no flag, the same bytes as
	// when kb-b declares nothing.
	h2, f2 := kbMapFixture(t, []string{"kb-a", "kb-b", "core-kb", "kb-hidden"}, map[string][][2]string{"kb-a": kbMapDeps["kb-a"]})
	kbMapPages(t, f2)
	pm, partial := kbMapGet(t, h, "bScoped")
	_, none := kbMapGet(t, h2, "bScoped")
	if partial != none {
		t.Errorf("a partial reader sees kb-b's declarations:\n%s\n%s", partial, none)
	}
	for _, r := range pm.Relations {
		if r.From == "kb-b" {
			t.Errorf("relation from a partially read KB: %v", r)
		}
	}
	for _, n := range pm.Nodes {
		if n.RelationsUnavailable {
			t.Errorf("flag on %s", n.Name)
		}
	}
}

// AC-atlas-kb-map-13
func TestKBMapRelationsUnavailable(t *testing.T) {
	t.Parallel()
	deps := map[string][][2]string{"kb-a": {{"core-kb", "core-kb"}}}
	h, f := kbMapFixture(t, []string{"kb-a", "kb-b", "core-kb"}, deps)
	kbMapPages(t, f)
	hOK, fOK := kbMapFixture(t, []string{"kb-a", "kb-b", "core-kb"}, deps)
	kbMapPages(t, fOK)
	before, _ := kbMapGet(t, hOK, "noHidden")
	if len(before.Relations) != 1 {
		t.Fatalf("relations = %v", before.Relations)
	}
	// git cannot answer for the entered node: kb-a's relations cannot be read
	// (nor those of core-kb, whose repository is the broken one).
	f.byName["core-kb"].NormalizedOrigin()
	if err := os.RemoveAll(filepath.Join(f.byName["core-kb"].Root, ".git")); err != nil {
		t.Fatal(err)
	}
	after, _ := kbMapGet(t, h, "noHidden")
	if len(after.Relations) != 0 {
		t.Errorf("relations = %v, want none", after.Relations)
	}
	for _, n := range after.Nodes {
		if want := n.Name != "kb-b"; n.RelationsUnavailable != want {
			t.Errorf("%s: relations_unavailable = %v", n.Name, n.RelationsUnavailable)
		}
	}
	if !reflect.DeepEqual(after.Links, before.Links) || len(after.Nodes) != len(before.Nodes) {
		t.Errorf("links or nodes changed: %v vs %v", after, before)
	}
}

// AC-atlas-kb-map-8
func TestKBMapETagAndContract(t *testing.T) {
	t.Parallel()
	h, f := kbMapFixture(t, []string{"kb-a", "kb-b", "core-kb"}, kbMapDeps)
	kbMapPages(t, f)
	rr := getUI(t, h, UIAPIPrefix+"/kb-map", "admin")
	etag := rr.Header().Get("ETag")
	if rr.Code != 200 || etag == "" || !strings.Contains(rr.Header().Get("Cache-Control"), "private") {
		t.Fatalf("status %d etag %q cc %q", rr.Code, etag, rr.Header().Get("Cache-Control"))
	}
	req := httptest.NewRequest(http.MethodGet, UIAPIPrefix+"/kb-map", nil)
	req.Header.Set("Authorization", "Bearer admin")
	req.Header.Set("If-None-Match", etag)
	rr2 := httptest.NewRecorder()
	h.ServeHTTP(rr2, req)
	if rr2.Code != http.StatusNotModified || rr2.Body.Len() != 0 {
		t.Fatalf("repeat: %d %q", rr2.Code, rr2.Body.String())
	}
	// Another caller's body has its own tag.
	if other := getUI(t, h, UIAPIPrefix+"/kb-map", "aOnly").Header().Get("ETag"); other == etag {
		t.Error("two callers share a tag")
	}
	found := false
	for _, r := range apiRoutes {
		found = found || r.Pattern == "/kb-map"
	}
	spec, err := os.ReadFile(filepath.Join("..", "..", "docs", "openapi.json"))
	if err != nil || !found || !strings.Contains(string(spec), `/kb-map"`) {
		t.Errorf("route in table %v, in openapi: %v", found, err)
	}
}

// The budget bounds the links examined; the answer says it is a lower bound.
// Not parallel: it sets a package variable.
func TestKBMapEdgeBudget(t *testing.T) {
	old := kbMapEdgeBudget
	kbMapEdgeBudget = 2
	t.Cleanup(func() { kbMapEdgeBudget = old })
	h, f := kbMapFixture(t, []string{"kb-a", "kb-b", "core-kb"}, kbMapDeps)
	kbMapPages(t, f)
	m, _ := kbMapGet(t, h, "noHidden")
	if !m.Truncated {
		t.Error("truncated not set")
	}
	if linkCount(m, "kb-a", "kb-b") > 2 {
		t.Errorf("more than the budget was counted: %v", m.Links)
	}
}

// A single-KB stdio server: one node, no edges.
func TestKBMapSingleKB(t *testing.T) {
	t.Parallel()
	k := uiFixtureKB(t, "docs")
	multi := NewMultiKBServer("test")
	multi.MountKB("docs", func(s *Server) { RegisterKBTools(s, k, Deps{}) })
	multi.EnableWeb(nil)
	m, _ := kbMapGet(t, auth.NewTokenStore(nil).Middleware(multi.Handler()), "")
	if len(m.Nodes) != 1 || len(m.Links) != 0 || len(m.Relations) != 0 || m.Nodes[0].Layer != 0 {
		t.Errorf("single KB: %+v", m)
	}
}

// Polling tabs share one validation: inside the window a repeated /kb-map
// walks no KB's files again. Not parallel: it sets a package variable.
func TestKBMapPollsShareOneValidation(t *testing.T) {
	setRevisionMaxAge(t, time.Hour)
	h, f := kbMapFixture(t, []string{"kb-a", "kb-b", "core-kb"}, kbMapDeps)
	kbMapPages(t, f)
	kbMapGet(t, h, "noHidden")
	before := map[string]int{}
	for n, k := range f.byName {
		before[n] = k.GraphValidations()
	}
	for i := 0; i < 5; i++ {
		kbMapGet(t, h, "noHidden")
	}
	for n, k := range f.byName {
		if got := k.GraphValidations(); got != before[n] {
			t.Errorf("%s: five repeated requests validated the graph %d times", n, got-before[n])
		}
	}
}
