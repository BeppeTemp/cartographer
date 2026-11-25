package mcpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/kbtest"
	"github.com/BeppeTemp/cartographer/internal/lint"
)

// crossRig is kb-a linking kb-b, both mounted, plus kb-c mounted and never
// linked. kb-b holds ops/x; ops/gone does not exist.
type crossRig struct{ a, b, c *kb.KB }

func newCrossRig(t *testing.T) crossRig {
	t.Helper()
	r := crossRig{a: kbtest.New(t), b: kbtest.New(t), c: kbtest.New(t)}
	r.a.AuthName, r.b.AuthName, r.c.AuthName = "kb-a", "kb-b", "kb-c"
	m := kb.NewMounts(map[string]*kb.KB{"kb-a": r.a, "kb-b": r.b, "kb-c": r.c}, []string{"kb-a", "kb-b", "kb-c"}, map[string]string{"old-b": "kb-b"})
	for _, k := range []*kb.KB{r.a, r.b, r.c} {
		k.SetMounts(m)
	}
	write := func(k *kb.KB, id, body string) {
		p := filepath.Join(k.DataRoot(), filepath.FromSlash(id)+".md")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("---\ntype: Note\n---\n"+body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(r.b, "ops/x", "x\n")
	write(r.a, "p", "[[kb:kb-b/ops/x]]\n")
	return r
}

func principal(perms ...auth.Permission) context.Context {
	return auth.ContextWithPrincipal(context.Background(), auth.Principal{ID: "u", Policy: auth.Policy{Permissions: perms}})
}

// callers of the table: what each may read of kb-a and kb-b.
func crossCallers() map[string]context.Context {
	return map[string]context.Context{
		"admin":        auth.ContextWithPrincipal(context.Background(), auth.Principal{ID: "adm", Policy: auth.Policy{Admin: true}}),
		"reads-a-b":    principal(auth.Permission{KB: "kb-a"}, auth.Permission{KB: "kb-b"}),
		"b-scoped":     principal(auth.Permission{KB: "kb-a"}, auth.Permission{KB: "kb-b", Maps: []string{"ops"}}),
		"a-only":       principal(auth.Permission{KB: "kb-a"}),
		"zero":         context.Background(),
		"b-not-a":      principal(auth.Permission{KB: "kb-b"}),
		"a-and-c-only": principal(auth.Permission{KB: "kb-a"}, auth.Permission{KB: "kb-c"}),
	}
}

// AC-cross-kb-links-8
func TestCrossStateForNeutral(t *testing.T) {
	t.Parallel()
	r := newCrossRig(t)
	callers := crossCallers()
	links := map[string]kb.CrossLink{
		"absent":         {KB: "nope", ID: "ops/x"},
		"server":         {Server: "host", KB: "kb-b", ID: "ops/x"},
		"page-exists":    {KB: "kb-b", ID: "ops/x"},
		"page-missing":   {KB: "kb-b", ID: "ops/gone"},
		"alias":          {KB: "old-b", ID: "ops/x"},
		"unlinked-kb":    {KB: "kb-c", ID: "ops/x"},
		"malformed-name": {KB: "kb b", ID: "ops/x", Malformed: true},
	}
	want := map[string]map[string]crossState{
		"admin":     {"absent": crossUnresolved, "server": crossUnresolved, "page-exists": crossResolved, "page-missing": crossMissing, "alias": crossResolved, "unlinked-kb": crossMissing},
		"reads-a-b": {"absent": crossUnresolved, "server": crossUnresolved, "page-exists": crossResolved, "page-missing": crossMissing, "alias": crossResolved, "unlinked-kb": crossUnresolved},
		// A reader of one map of kb-b is never told a page of it is missing.
		"b-scoped":     {"page-exists": crossResolved, "page-missing": crossUnresolved, "alias": crossResolved},
		"a-only":       {"page-exists": crossUnresolved, "page-missing": crossUnresolved, "alias": crossUnresolved},
		"zero":         {"page-exists": crossUnresolved, "page-missing": crossUnresolved, "alias": crossUnresolved},
		"a-and-c-only": {"page-exists": crossUnresolved, "page-missing": crossUnresolved, "unlinked-kb": crossMissing},
	}
	for who, ctx := range callers {
		for name, l := range links {
			got := crossStateFor(ctx, r.a, l)
			if w, ok := want[who][name]; ok && got != w {
				t.Errorf("%s / %s = %q, want %q", who, name, got, w)
			}
			if name == "malformed-name" && got != crossUnresolved {
				t.Errorf("%s / malformed = %q", who, got)
			}
		}
	}
	// The neutral cells are one value, byte for byte, whatever the cause.
	cells := []crossState{
		crossStateFor(callers["a-only"], r.a, links["absent"]),
		crossStateFor(callers["a-only"], r.a, links["server"]),
		crossStateFor(callers["a-only"], r.a, links["page-exists"]),
		crossStateFor(callers["a-only"], r.a, links["page-missing"]),
		crossStateFor(callers["zero"], r.a, links["page-exists"]),
		crossStateFor(callers["b-scoped"], r.a, links["page-missing"]),
	}
	for i, c := range cells {
		if c != crossUnresolved {
			t.Errorf("neutral cell %d = %q", i, c)
		}
	}
	// No mounts: nothing resolves.
	if got := crossStateFor(callers["admin"], kbtest.New(t), links["page-exists"]); got != crossUnresolved {
		t.Errorf("a KB with no mounts = %q", got)
	}
}

// AC-cross-kb-links-9, AC-cross-kb-links-14 (leak surfaces 3, 4, 5, 6)
func TestOtherKBsFindingFiltered(t *testing.T) {
	t.Parallel()
	r := newCrossRig(t)
	cross := lint.Finding{Path: "p.md", Check: "broken_link", Message: "dangling", OtherKBs: []string{"kb-b"}}
	plain := lint.Finding{Path: "p.md", Check: "broken_link", Message: "local"}
	viaAlias := lint.Finding{Path: "p.md", Check: "broken_link", Message: "alias", OtherKBs: []string{"old-b"}}
	all := []lint.Finding{plain, cross, viaAlias}
	keeps := map[string]bool{"admin": true, "reads-a-b": true}
	for who, ctx := range crossCallers() {
		if who == "zero" || who == "b-not-a" {
			continue // no read of kb-a: nothing of its findings is shown at all
		}
		want := 1
		if keeps[who] {
			want = 3
		}
		if got := newCallerView(ctx, r.a).filter(all); len(got) != want {
			t.Errorf("%s: callerView.filter kept %d, want %d", who, len(got), want)
		}
		if got := lintFilteredFor(ctx, r.a, all); len(got) != want {
			t.Errorf("%s: lintFilteredFor kept %d, want %d", who, len(got), want)
		}
		vis, err := uiVisibleFindingsFrom(ctx, r.a, "", all)
		if err != nil {
			t.Fatal(err)
		}
		if len(vis) > want {
			t.Errorf("%s: uiVisibleFindingsFrom kept %d, want at most %d", who, len(vis), want)
		}
		if keeps[who] && len(vis) != 3 {
			t.Errorf("%s: uiVisibleFindingsFrom kept %d, want 3", who, len(vis))
		}
		// The write gate's view: a refusal for it would reveal the page is missing.
		if newCallerView(ctx, r.a).sees(cross) != keeps[who] {
			t.Errorf("%s: callerView.sees(cross) = %v", who, !keeps[who])
		}
	}
	// A finding that cannot be told is dropped in place, the slice of the cache untouched.
	if len(all) != 3 || all[1].OtherKBs == nil {
		t.Errorf("the unfiltered list was modified: %+v", all)
	}
	// The sample of the whole-KB totals never counts it, nor the check.
	s := newLintSample(r.a, append(all, lint.Finding{Path: "p.md", Check: "broken_cross_kb_link", Message: "m"}), time.Now())
	if s.Total != 1 {
		t.Errorf("lint sample total = %d, want 1", s.Total)
	}
}

// AC-cross-kb-links-14 (leak surfaces 1 and 2): the totals of lint and the verdict of gate_check.
func TestCrossFindingsLeakNoCountOrVerdict(t *testing.T) {
	// Not parallel: it swaps lintRunFn.
	r := newCrossRig(t)
	orig := lintRunFn
	t.Cleanup(func() { lintRunFn = orig })
	lintRunFn = func(*kb.KB, string, bool) ([]lint.Finding, error) {
		return []lint.Finding{{Path: "p.md", Check: "broken_link", Message: "dangling", OtherKBs: []string{"kb-b"}}}, nil
	}
	run := func(ctx context.Context, tool Tool) map[string]any {
		t.Helper()
		res, err := tool.Handler(ctx, json.RawMessage(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		var out map[string]any
		if err := json.Unmarshal([]byte(res.Content[0].Text), &out); err != nil {
			t.Fatalf("%v: %s", err, res.Content[0].Text)
		}
		return out
	}
	hidden := principal(auth.Permission{KB: "kb-a"})
	shown := principal(auth.Permission{KB: "kb-a"}, auth.Permission{KB: "kb-b"})
	if got := run(hidden, toolLint(r.a)); got["count"] != float64(0) || len(got["counts_by_check"].(map[string]any)) != 0 {
		t.Errorf("lint leaks a count to a caller who cannot read kb-b: %v", got)
	}
	if got := run(shown, toolLint(r.a)); got["count"] != float64(1) {
		t.Errorf("lint hides the finding from a caller who reads kb-b: %v", got)
	}
	if got := run(hidden, toolGateCheck(r.a)); got["pass"] != true {
		t.Errorf("gate_check refuses for a finding the caller cannot see: %v", got)
	}
	if got := run(shown, toolGateCheck(r.a)); got["pass"] != false {
		t.Errorf("gate_check passes over a finding the caller can see: %v", got)
	}
}

// AC-cross-kb-links-10
func TestOverviewCacheKeyAccountsForCrossRights(t *testing.T) {
	t.Parallel()
	r := newCrossRig(t)
	callers := crossCallers()
	if fp := crossFingerprint(callers["admin"], r.b); fp != "" {
		t.Errorf("a KB with no cross link must keep the key whole, got %q", fp)
	}
	reads, hides := crossFingerprint(callers["reads-a-b"], r.a), crossFingerprint(callers["a-only"], r.a)
	if reads == "" || hides == "" || reads == hides {
		t.Errorf("fingerprints reads=%q hides=%q: a KB with links must not share an entry with a link-free one, nor across rights", reads, hides)
	}
	if reads != crossFingerprint(callers["admin"], r.a) {
		t.Errorf("callers with the same rights on the linked KBs must share a key")
	}
	computes := 0
	inner := func(w http.ResponseWriter, _ *http.Request) {
		computes++
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{}"))
	}
	serve := func(ctx context.Context) {
		req := httptest.NewRequest(http.MethodGet, "/overview", nil).WithContext(ctx)
		serveUICached(httptest.NewRecorder(), req, r.a, "overview", true, uiWholeStamp, inner)
	}
	// Two whole-KB callers of kb-a, one reading kb-b whole and one not.
	for _, who := range []string{"reads-a-b", "a-only", "reads-a-b", "a-only", "admin"} {
		serve(callers[who])
	}
	if computes != 2 {
		t.Errorf("computes = %d, want 2 (one per set of rights, admin shares with reads-a-b)", computes)
	}
}

// AC-cross-kb-links-11
func TestCrossStateOutOfScope(t *testing.T) {
	t.Parallel()
	r := newCrossRig(t)
	callers := crossCallers()
	set := withRoutedSet(callers["reads-a-b"], []string{"kb-a"})
	for _, l := range []kb.CrossLink{{KB: "kb-b", ID: "ops/x"}, {KB: "kb-b", ID: "ops/gone"}, {KB: "old-b", ID: "ops/x"}} {
		if got := crossStateFor(set, r.a, l); got != crossOutOfScope {
			t.Errorf("%+v = %q, want out_of_scope", l, got)
		}
	}
	// A caller with no read grant on kb-b gets the same bytes as for an absent KB.
	noGrant := withRoutedSet(callers["a-only"], []string{"kb-a"})
	if got, absent := crossStateFor(noGrant, r.a, kb.CrossLink{KB: "kb-b", ID: "ops/x"}), crossStateFor(noGrant, r.a, kb.CrossLink{KB: "nope", ID: "ops/x"}); got != absent || got != crossUnresolved {
		t.Errorf("unreadable %q vs absent %q", got, absent)
	}
	// No routed set (the Atlas, a per-KB endpoint): never out_of_scope.
	if got := crossStateFor(callers["reads-a-b"], r.a, kb.CrossLink{KB: "kb-b", ID: "ops/x"}); got == crossOutOfScope {
		t.Error("a context with no routed set yielded out_of_scope")
	}
	op := bindHint(withOperator(callers["reads-a-b"]), "kb-b")
	for _, want := range []string{"cartographer client bind <provider> kb-b", "cartographer workspace bind <provider> <path> --kb kb-b", "cartographer sync"} {
		if !strings.Contains(op, want) {
			t.Errorf("operator hint lacks %q: %s", want, op)
		}
	}
	generic := bindHint(callers["reads-a-b"], "kb-b")
	assertNoCLICommand(t, "bindHint", generic)
	if !strings.Contains(generic, "adds the KB to its URL (?kbs=)") || strings.Contains(generic, "cartographer ") {
		t.Errorf("generic hint = %s", generic)
	}
}

// A fix for a finding the caller cannot see is never applied: it would rewrite a
// page for a writer who cannot read the target, and the repaired count and the
// new hash would say so.
func TestRepairCandidatesSkipInvisibleCrossFinding(t *testing.T) {
	t.Parallel()
	r := newCrossRig(t)
	checks := map[string]bool{"nonstandard_field": true}
	written := map[string]bool{"p": true}
	f := lint.Finding{Path: "p.md", Check: "nonstandard_field", Message: "m", Fix: &lint.Fix{Kind: "rename_field", Field: "a", To: "b"}, OtherKBs: []string{"kb-b"}}
	callers := crossCallers()
	if got := repairCandidates([]lint.Finding{f}, checks, written, newCallerView(callers["a-only"], r.a)); len(got) != 0 {
		t.Errorf("a caller who cannot read kb-b got a repair candidate: %+v", got)
	}
	if got := repairCandidates([]lint.Finding{f}, checks, written, newCallerView(callers["reads-a-b"], r.a)); len(got["p"]) != 1 {
		t.Errorf("a caller who reads kb-b lost the candidate: %+v", got)
	}
	// A finding that names no KB stays a candidate, even on a page the caller cannot see.
	plain := f
	plain.OtherKBs = nil
	narrow := principal(auth.Permission{KB: "kb-a", Maps: []string{"zzz"}})
	if got := repairCandidates([]lint.Finding{plain}, checks, written, newCallerView(narrow, r.a)); len(got["p"]) != 1 {
		t.Errorf("a non-cross finding stopped being a repair candidate: %+v", got)
	}
}

// The review totals never count an item whose finding names a KB the caller cannot read.
func TestDropCrossReview(t *testing.T) {
	t.Parallel()
	r := newCrossRig(t)
	items := []lint.ReviewItem{{Kind: lint.ReviewLintJudgement, Concepts: []string{"p"}}, {Kind: lint.ReviewLintJudgement, Concepts: []string{"p"}, OtherKBs: []string{"kb-b"}}}
	callers := crossCallers()
	if got := dropCrossReview(callers["a-only"], r.a, items); len(got) != 1 {
		t.Errorf("hidden caller sees %d items, want 1", len(got))
	}
	if got := dropCrossReview(callers["reads-a-b"], r.a, items); len(got) != 2 {
		t.Errorf("reader sees %d items, want 2", len(got))
	}
}
