package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/kb"
)

// lintCounts calls the lint tool of name's server as ctx and returns the count
// of each relation check.
func (f *relFixture) lintCounts(t *testing.T, ctx context.Context, name string) (unresolved, cycle int) {
	t.Helper()
	text, isErr := callJSON(t, f.srv[name], ctx, "lint", `{}`)
	if isErr {
		t.Fatalf("lint: %s", text)
	}
	var out struct {
		CountsByCheck map[string]int `json:"counts_by_check"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatal(err)
	}
	return out.CountsByCheck["kb_relation_unresolved"], out.CountsByCheck["kb_relation_cycle"]
}

// AC-kbrel-23
func TestRelationFindingVisibility(t *testing.T) {
	t.Parallel()
	// core-kb is mounted with another origin than the one declared: remote_mismatch
	// for a caller who may speak of it, gone-kb is not mounted at all.
	f := newRelFixture(t, []string{"client-kb-1", "core-kb"}, map[string][][2]string{
		"client-kb-1": {{"core-kb", "core-x"}, {"gone-kb", "gone-kb"}},
	})
	callers := map[string]context.Context{
		"admin":         auth.ContextWithPrincipal(context.Background(), auth.Principal{ID: "adm", Policy: auth.Policy{Admin: true}}),
		"reads-both":    readsOnly("client-kb-1", "core-kb", "gone-kb"),
		"core-by-a-map": auth.ContextWithPrincipal(context.Background(), auth.Principal{ID: "u", Policy: auth.Policy{Permissions: []auth.Permission{{KB: "client-kb-1"}, {KB: "core-kb", Maps: []string{"ops"}}, {KB: "gone-kb"}}}}),
		"client-only":   readsOnly("client-kb-1"),
		"zero":          auth.ContextWithPrincipal(context.Background(), auth.Principal{ID: "z"}),
	}
	wantFindings := map[string]int{"admin": 2, "reads-both": 2, "core-by-a-map": 1, "client-only": 0}
	for who, ctx := range callers {
		if who == "zero" {
			continue // no read of the client KB: no lint at all
		}
		u, _ := f.lintCounts(t, ctx, "client-kb-1")
		if u != wantFindings[who] {
			t.Errorf("%s: kb_relation_unresolved = %d, want %d", who, u, wantFindings[who])
		}
		// A finding naming X shown implies the view does not hide X as not_mounted
		// when X is mounted: the two predicates agree.
		if u == 2 {
			v, err := relationViewFor(ctx, f.byName["client-kb-1"])
			if err != nil {
				t.Fatal(err)
			}
			for _, x := range v.Unresolved {
				if x.Name == "core-kb" && x.Reason == kb.RelationNotMounted {
					t.Errorf("%s sees the finding about core-kb but its view hides core-kb as not_mounted", who)
				}
			}
		}
	}
}

// relOverviewHandler serves the Atlas over the KBs of f, with one token per
// caller.
func relOverviewHandler(f *relFixture, tokens map[string][]string) http.Handler {
	var scoped []auth.ScopedToken
	for tok, kbs := range tokens {
		var perms []auth.Permission
		for _, n := range kbs {
			perms = append(perms, auth.Permission{KB: n})
		}
		scoped = append(scoped, auth.ScopedToken{Token: tok, Policy: auth.Policy{Permissions: perms}})
	}
	multi := NewMultiKBServer("test")
	for name, k := range f.byName {
		multi.MountKB(name, func(s *Server) { RegisterKBTools(s, k, Deps{BundleFS: fstest.MapFS{}}) })
	}
	multi.EnableWeb(nil)
	return auth.NewScopedTokenStore(scoped).Middleware(multi.Handler())
}

func overviewRelationLint(t *testing.T, h http.Handler, token string) int {
	t.Helper()
	rr := getUI(t, h, UIAPIPrefix+"/kbs/client-kb-1/overview", token)
	if rr.Code != http.StatusOK {
		t.Fatalf("overview as %s: %d %s", token, rr.Code, rr.Body.String())
	}
	var out struct {
		Lint struct {
			ByCheck map[string]int `json:"by_check"`
		} `json:"lint"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out.Lint.ByCheck["kb_relation_unresolved"]
}

// AC-kbrel-24
func TestRelationFindingsOverviewCache(t *testing.T) {
	// Not parallel: the computes counter is read around each request.
	f := newRelFixture(t, []string{"client-kb-1", "gone-kb"}, map[string][][2]string{
		"client-kb-1": {{"gone-kb", "gone-kb"}},
	})
	c := f.byName["client-kb-1"]
	c.SetMounts(kb.NewMounts(map[string]*kb.KB{"client-kb-1": c}, []string{"client-kb-1"}, nil))
	h := relOverviewHandler(f, map[string][]string{
		"reads-target": {"client-kb-1", "gone-kb"},
		"same-rights":  {"client-kb-1", "gone-kb"},
		"client-only":  {"client-kb-1"},
	})
	base := uiBodyComputes(c)
	if got := overviewRelationLint(t, h, "reads-target"); got != 1 {
		t.Errorf("the caller who reads the target must see the finding, got %d", got)
	}
	if got := overviewRelationLint(t, h, "client-only"); got != 0 {
		t.Errorf("the caller who cannot read the target must not see the finding, got %d", got)
	}
	if n := uiBodyComputes(c) - base; n != 2 {
		t.Errorf("two callers with different rights computed %d times, want 2", n)
	}
	if got := overviewRelationLint(t, h, "same-rights"); got != 1 {
		t.Errorf("same rights, different token: %d", got)
	}
	if n := uiBodyComputes(c) - base; n != 2 {
		t.Errorf("callers with the same rights must share one entry: %d computes", n)
	}
	// The declared target becomes mounted: the finding is gone with no change in the KB.
	m := kb.NewMounts(f.byName, []string{"client-kb-1", "gone-kb"}, nil)
	for _, k := range f.byName {
		k.SetMounts(m)
	}
	if got := overviewRelationLint(t, h, "reads-target"); got != 0 {
		t.Errorf("the finding must be gone once the target is mounted, got %d", got)
	}
}

// relUnresolvedPair is a KB whose only dependency is not mounted (so the lint
// tool shows a relation finding) and the function that mounts it (the same KB
// without the finding).
func relUnresolvedPair(t *testing.T) (f *relFixture, admin context.Context, resolve func()) {
	t.Helper()
	f = newRelFixture(t, []string{"client-kb-1", "gone-kb"}, map[string][][2]string{
		"client-kb-1": {{"gone-kb", "gone-kb"}},
	})
	c := f.byName["client-kb-1"]
	admin = auth.ContextWithPrincipal(context.Background(), auth.Principal{ID: "adm", Policy: auth.Policy{Admin: true}})
	c.SetMounts(kb.NewMounts(map[string]*kb.KB{"client-kb-1": c}, []string{"client-kb-1"}, nil))
	resolve = func() {
		m := kb.NewMounts(f.byName, []string{"client-kb-1", "gone-kb"}, nil)
		for _, k := range f.byName {
			k.SetMounts(m)
		}
	}
	return f, admin, resolve
}

// AC-kbrel-25
func TestRelationFindingsStayOutOfHistoryAndNudge(t *testing.T) {
	// Not parallel: it reads the lint-run stamp of a shared helper.
	f, admin, resolve := relUnresolvedPair(t)
	c, srv := f.byName["client-kb-1"], f.srv["client-kb-1"]
	if u, _ := f.lintCounts(t, admin, "client-kb-1"); u != 1 {
		t.Fatalf("the lint tool must show the finding, got %d", u)
	}
	for _, name := range []string{"kb_relation_unresolved", "kb_relation_cycle"} {
		if conformanceChecks[name] {
			t.Errorf("%s counts as conformance debt", name)
		}
	}
	// Every caller that must not see a relation finding, as one string.
	observe := func() map[string]string {
		out := map[string]string{}
		for _, tool := range []string{"gate_check", "commit_gate"} {
			text, _ := callJSON(t, srv, admin, tool, `{}`)
			out[tool] = text
		}
		repair, _ := callJSON(t, srv, admin, "kb_repair", `{"check":"*"}`)
		out["kb_repair"] = repair
		all, err := srv.conformance.lintFindings(c) // doctor nudge, history, overview
		if err != nil {
			t.Fatal(err)
		}
		vis, _ := uiVisibleFindingsFrom(admin, c, "", all)
		sum, _ := json.Marshal(summarizeConformance(vis, 0, "", 0, time.Unix(0, 0).UTC()))
		out["nudge_summary"] = string(sum)
		sample, _ := json.Marshal(newLintSample(c, all, time.Unix(0, 0)))
		out["lint_sample"] = string(sample)
		art, err := artifactFindings(srv)
		if err != nil {
			t.Fatal(err)
		}
		a, _ := json.Marshal(art)
		out["artifact_findings"] = string(a)
		for who, text := range out {
			if strings.Contains(text, "kb_relation") {
				t.Errorf("%s carries a relation finding: %s", who, text)
			}
		}
		return out
	}
	with := observe()
	resolve()
	without := observe()
	for who, w := range with {
		if without[who] != w {
			t.Errorf("%s differs with and without a relation finding:\n%s\n%s", who, w, without[who])
		}
	}
}

// AC-kbrel-22
func TestRelationFindingsDoNotBlockAWriteOrEnterRepair(t *testing.T) {
	t.Parallel()
	f, admin, _ := relUnresolvedPair(t)
	srv := f.srv["client-kb-1"]
	if u, _ := f.lintCounts(t, admin, "client-kb-1"); u != 1 {
		t.Fatalf("the KB must carry a relation finding, got %d", u)
	}
	text, isErr := callJSON(t, srv, admin, "concept_write",
		`{"id":"notes/first","frontmatter":{"type":"Note","title":"First"},"body":"A note.\n"}`)
	if isErr {
		t.Fatalf("a write is refused while a relation finding exists: %s", text)
	}
	repair, _ := callJSON(t, srv, admin, "kb_repair", `{"check":"*"}`)
	if strings.Contains(repair, "kb_relation") {
		t.Errorf("kb_repair lists a relation finding: %s", repair)
	}
}

func TestLintToolScopeCarriesNoRelationFinding(t *testing.T) {
	t.Parallel()
	f, admin, _ := relUnresolvedPair(t)
	if u, _ := f.lintCounts(t, admin, "client-kb-1"); u != 1 {
		t.Fatalf("whole-KB lint must show the finding, got %d", u)
	}
	if text, isErr := callJSON(t, f.srv["client-kb-1"], admin, "concept_write",
		`{"id":"notes/first","frontmatter":{"type":"Note","title":"First"},"body":"A note.\n"}`); isErr {
		t.Fatal(text)
	}
	text, isErr := callJSON(t, f.srv["client-kb-1"], admin, "lint", `{"scope":"notes"}`)
	if isErr {
		t.Fatalf("lint with a scope: %s", text)
	}
	if strings.Contains(text, "kb_relation") {
		t.Errorf("a scoped lint carries a relation finding: %s", text)
	}
}

// A three-KB cycle a -> b -> c -> a names every KB on its path: a caller who
// reads a and b but not c is told nothing of it, one who reads all three is.
func TestRelationCycleHiddenFromACallerMissingAMember(t *testing.T) {
	t.Parallel()
	f := newRelFixture(t, []string{"a-kb", "b-kb", "c-kb"}, map[string][][2]string{
		"a-kb": {{"b-kb", "b-kb"}},
		"b-kb": {{"c-kb", "c-kb"}},
		"c-kb": {{"a-kb", "a-kb"}},
	})
	for who, tc := range map[string]struct {
		ctx  context.Context
		want int
	}{
		"reads a and b only": {readsOnly("a-kb", "b-kb"), 0},
		"reads a and c only": {readsOnly("a-kb", "c-kb"), 0},
		"reads all three":    {readsOnly("a-kb", "b-kb", "c-kb"), 1},
	} {
		if _, cyc := f.lintCounts(t, tc.ctx, "a-kb"); cyc != tc.want {
			t.Errorf("%s: kb_relation_cycle = %d, want %d", who, cyc, tc.want)
		}
	}
}

// A KB just created with a remote has not learned the remote's default branch:
// the relation findings are left out without a warning on stderr (the state is
// expected until the first sync), and a real read failure still warns.
func TestRelationFindingsQuietBeforeFirstSync(t *testing.T) {
	f := newRelFixture(t, []string{"kb-a", "kb-b"}, map[string][][2]string{
		"kb-a": {{"kb-b", "kb-b"}},
	}, "kb-a")
	k := f.byName["kb-a"]
	if _, err := k.Relations(); !errors.Is(err, kb.ErrRelationsRefUnknown) {
		t.Fatalf("Relations error = %v, want ErrRelationsRefUnknown", err)
	}
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = w
	got := appendRelationFindings(auth.ContextWithPrincipal(context.Background(), auth.Principal{ID: "adm", Policy: auth.Policy{Admin: true}}), k, nil)
	os.Stderr = old
	w.Close()
	out, _ := io.ReadAll(r)
	if len(got) != 0 {
		t.Errorf("findings = %+v, want none", got)
	}
	if len(out) != 0 {
		t.Errorf("stderr = %q, want nothing before the first sync", out)
	}
}

// Only an unset origin/HEAD is quiet: a git failure reading it is still warned.
func TestRelationFindingsStillWarnOnAGitFailure(t *testing.T) {
	f := newRelFixture(t, []string{"kb-a"}, nil)
	k := f.byName["kb-a"]
	if err := os.RemoveAll(filepath.Join(k.Root, ".git", "refs", "remotes", "origin")); err != nil {
		t.Fatal(err)
	}
	// A symbolic origin/HEAD whose file is unreadable as a ref: git fails, not "absent".
	if err := os.WriteFile(filepath.Join(k.Root, ".git", "HEAD"), []byte("garbage\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := k.Relations(); err == nil || errors.Is(err, kb.ErrRelationsRefUnknown) {
		t.Fatalf("Relations error = %v, want a plain error", err)
	}
}
