package system

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/kbtest"
	"github.com/BeppeTemp/cartographer/internal/lint"
	"github.com/BeppeTemp/cartographer/internal/mcpserver"
	"github.com/BeppeTemp/cartographer/internal/okf"
	"github.com/BeppeTemp/cartographer/internal/worker/feed"
)

// env is a real in-process KB behind its MCP tools, with a system Source.
type env struct {
	t     *testing.T
	k     *kb.KB
	c     *client.MCPClient
	id    string
	calls map[string]int
	store *Store
}

func newEnv(t *testing.T) *env {
	k := kbtest.New(t)
	s := mcpserver.New("test")
	mcpserver.RegisterKBTools(s, k, mcpserver.Deps{BundleFS: fstest.MapFS{}})
	h := s.HTTPHandler()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(w, r.WithContext(auth.ContextWithPrincipal(r.Context(), auth.LocalAdminPrincipal())))
	}))
	t.Cleanup(srv.Close)
	e := &env{t: t, k: k, c: client.New(srv.URL+"/mcp", ""), calls: map[string]int{}}
	e.call("map_create", map[string]any{"name": "sources", "kind": "journal", "title": "Sources"})
	out := e.call("source_register", map[string]any{"title": "Dev cluster", "source_kind": "system", "locator": "kubernetes://dev-cluster",
		"source_shape": "system", "recipe": "k8s", "map": "sources"})
	var r struct{ ID string }
	_ = json.Unmarshal([]byte(out), &r)
	e.id = r.ID
	e.store = &Store{Source: e.id, Call: func(tool string, args map[string]any) (string, error) {
		e.calls[tool]++
		o, err := e.c.CallTool(tool, args)
		if err != nil {
			return "", err
		}
		if o.IsError {
			return "", &Refusal{Text: o.Text}
		}
		return o.Text, nil
	}}
	return e
}

func (e *env) call(tool string, args map[string]any) string {
	e.t.Helper()
	o, err := e.c.CallTool(tool, args)
	if err != nil || o.IsError {
		e.t.Fatalf("%s: %v %s", tool, err, o.Text)
	}
	return o.Text
}

// lease patches feed_checked_at like the poll does and returns the hash.
func (e *env) lease(at string) string {
	var r struct {
		Hash string `json:"content_hash"`
	}
	text := e.call("concept_read", map[string]any{"id": e.id, "full": true})
	_ = json.Unmarshal([]byte(text), &r)
	out := e.call("concept_patch", map[string]any{"id": e.id, "if_match": r.Hash, "frontmatter": map[string]any{"feed_checked_at": at}})
	_ = json.Unmarshal([]byte(out), &r)
	return r.Hash
}

func (e *env) state(kv ...string) State {
	return State{}.Apply(handled(mustGroups(e.t, kv...)))
}

func handled(gs []Group) []Handled {
	var h []Handled
	for _, g := range gs {
		h = append(h, Handled{Key: g.Key, Group: g, At: "2031-02-03T04:05:06Z"})
	}
	return h
}

func TestStoreFirstSaveExpandsWritesAndLinksWithoutLintFindings(t *testing.T) {
	e := newEnv(t)
	l, err := e.store.Load("")
	if err != nil || !l.Trusted || len(l.State) != 0 || l.Digest != "" {
		t.Fatalf("first load = %+v, %v", l, err)
	}
	hash := e.lease("2031-02-03T04:05:06Z")
	next := e.state("namespace/team-a", "deployment api image:v1 CANARY-SNAPSHOT-TEXT")
	digest, hash2, err := e.store.Save(l, next, hash, "2031-02-03T04:05:06Z", "poll "+e.id)
	if err != nil || hash2 == "" || hash2 == hash {
		t.Fatalf("save: %v %q", err, hash2)
	}
	asset := e.call("asset_read", map[string]any{"concept_id": e.id, "path": StateFile})
	var a struct{ Sha256 string }
	_ = json.Unmarshal([]byte(asset), &a)
	if a.Sha256 != digest {
		t.Fatalf("the digest %s is not asset_read's sha256 %s", digest, a.Sha256)
	}
	findings, err := lint.Run(e.k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range findings {
		if f.Check == "orphan_asset" || f.Check == "broken_link" {
			t.Errorf("lint: %+v", f)
		}
	}
	if txt, _, err := e.k.IndexableAssetText(okf.ConceptID(e.id)); err != nil || txt != "" {
		t.Errorf("the state file is indexed into the Source's search document: %q %v", txt, err)
	}
	if hits := e.call("search", map[string]any{"query": "CANARY-SNAPSHOT-TEXT"}); strings.Contains(hits, e.id) {
		t.Errorf("search finds the snapshot on the Source: %s", hits)
	}
	// The next load trusts it, with the digest as the cursor.
	l2, err := e.store.Load(digest)
	if err != nil || !l2.Trusted || len(l2.State) != 1 {
		t.Fatalf("second load = %+v, %v", l2, err)
	}
	// An unchanged state writes nothing: no further call at all.
	before := len(e.calls)
	writes := e.calls["asset_write"] + e.calls["concept_expand"] + e.calls["concept_patch"]
	if d, h, err := e.store.Save(l2, l2.State, hash2, "2031-02-03T04:05:06Z", "r"); err != nil || d != digest || h != hash2 {
		t.Fatalf("unchanged: %v %s", err, d)
	}
	if e.calls["asset_write"]+e.calls["concept_expand"]+e.calls["concept_patch"] != writes || len(e.calls) < before {
		t.Error("an unchanged state wrote")
	}
	// A changed state overwrites under if_match; already_expanded is tolerated;
	// the body keeps one link.
	d3, h3, err := e.store.Save(l2, e.state("namespace/team-a", "deployment api image:v2"), hash2, "2031-02-03T04:05:06Z", "r")
	if err != nil || d3 == digest || h3 == "" {
		t.Fatalf("overwrite: %v", err)
	}
	if body := e.call("concept_read", map[string]any{"id": e.id, "full": true}); strings.Count(body, "("+StateFile+")") != 1 {
		t.Errorf("link count: %s", body)
	}
}

func TestStoreUntrustedStatesAreRebuiltNotRepaired(t *testing.T) {
	e := newEnv(t)
	l, _ := e.store.Load("")
	hash := e.lease("2031-02-03T04:05:06Z")
	digest, hash, err := e.store.Save(l, e.state("namespace/a", "x"), hash, "2031-02-03T04:05:06Z", "r")
	if err != nil {
		t.Fatal(err)
	}
	for name, cursor := range map[string]string{"cursor differs": strings.Repeat("0", 64), "cursor lost": ""} {
		if l, err := e.store.Load(cursor); err != nil || l.Trusted || len(l.State) != 0 || l.Reason == "" {
			t.Errorf("%s: %+v %v", name, l, err)
		}
	}
	// A flipped byte, a bad header: the file is read back, not trusted.
	data, _, _ := e.state("namespace/a", "x").Encode()
	for name, bad := range map[string]string{"flipped": strings.Replace(string(data), `"x"`, `"y"`, 1), "header": strings.Replace(string(data), "version", "versio", 1)} {
		e.call("asset_write", map[string]any{"concept_id": e.id, "path": StateFile, "content": bad, "if_match": digest})
		cur := Digest([]byte(bad))
		l, err := e.store.Load(cur)
		if name == "flipped" {
			// Same digest as the cursor but valid content: this is a different state, trusted by digest.
			if err != nil || !l.Trusted {
				t.Errorf("%s: %+v %v", name, l, err)
			}
		} else if err != nil || l.Trusted {
			t.Errorf("%s: %+v %v", name, l, err)
		}
		e.call("asset_write", map[string]any{"concept_id": e.id, "path": StateFile, "content": string(data), "if_match": cur})
	}
	_ = hash
}

func TestStoreStaleWriteAndLostLeaseStopWithoutFurtherWrites(t *testing.T) {
	e := newEnv(t)
	l, _ := e.store.Load("")
	hash := e.lease("2031-02-03T04:05:06Z")
	// Another replica moved feed_checked_at: the lease is lost before any write.
	if _, _, err := e.store.Save(l, e.state("namespace/a", "x"), hash, "2031-02-03T09:09:09Z", "r"); !errors.Is(err, feed.ErrLeaseLost) {
		t.Fatalf("lost lease = %v", err)
	}
	if e.calls["asset_write"] != 0 {
		t.Error("wrote after the lease was lost")
	}
	// The file appears under our feet: asset_write without if_match is refused.
	e.call("asset_write", map[string]any{"concept_id": e.id, "path": StateFile, "content": "{\"version\":1}\n"})
	if _, _, err := e.store.Save(l, e.state("namespace/a", "x"), hash, "2031-02-03T04:05:06Z", "r"); err == nil {
		t.Fatal("an existing file was overwritten without if_match")
	}
}

func TestStoreSurfacesAPolicyRefusalAndRefusesAnOversizedState(t *testing.T) {
	e := newEnv(t)
	boom := &Store{Source: e.id, Call: func(string, map[string]any) (string, error) {
		return "", &Refusal{Text: "forbidden: the token's scope excludes this map"}
	}}
	if _, err := boom.Load(""); err == nil || !strings.Contains(err.Error(), "forbidden") {
		t.Errorf("load = %v", err)
	}
	if _, _, err := boom.Save(Loaded{State: State{}, Trusted: true}, e.state("namespace/a", "x"), "h", "t", "r"); err == nil || errors.Is(err, feed.ErrLeaseLost) {
		t.Errorf("save = %v", err)
	}
	huge := State{"namespace/a": {Key: "namespace/a", Revision: Revision("x"), Snapshot: strings.Repeat("x", MaxStateBytes)}}
	calls := 0
	s := &Store{Source: e.id, Call: func(string, map[string]any) (string, error) { calls++; return "", nil }}
	if _, _, err := s.Save(Loaded{State: State{}}, huge, "h", "t", "r"); err == nil || calls != 0 {
		t.Errorf("oversized: %v, %d calls", err, calls)
	}
}
