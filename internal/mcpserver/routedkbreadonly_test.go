package mcpserver

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/config"
)

// ?readonly= on the routed mount (#1409): the client sets some KBs read-only.

func writeArgs(kbName string) map[string]any {
	a := map[string]any{"id": "manutenzione/new", "frontmatter": map[string]any{"title": "x"}, "body": "x"}
	if kbName != "" {
		a["kb"] = kbName
	}
	return a
}

func listedNames(t *testing.T, f *viewFixture, path string) map[string]bool {
	t.Helper()
	got := map[string]bool{}
	for _, tool := range decodeToolList(t, f.post(t, tokAdmin, path, toolsListBody)) {
		got[tool.Name] = true
	}
	return got
}

func TestRoutedKBReadOnly_AllKBsUnlistsWrites(t *testing.T) {
	t.Parallel()
	f := newViewFixture(t, nil, "kb-a", "kb-b")
	path := RoutedMountPath + "?readonly=kb-a,kb-b"
	got := listedNames(t, f, path)
	full := listedNames(t, f, RoutedMountPath)
	for n := range full {
		if ToolRequiresWrite(n) && got[n] {
			t.Errorf("write tool %q listed when every KB is read-only", n)
		}
		if !ToolRequiresWrite(n) && !got[n] {
			t.Errorf("read tool %q missing", n)
		}
	}
	res := f.call(t, tokAdmin, path, "concept_write", writeArgs("kb-a"))
	if !res.IsError || !strings.Contains(res.Content[0].Text, denyReadOnlyKB) {
		t.Fatalf("write = %+v, want %s", res, denyReadOnlyKB)
	}
	assertNoCLICommand(t, "read_only_kb refusal", res.Content[0].Text)
	if r := f.call(t, tokAdmin, path, "concept_read", map[string]any{"kb": "kb-a", "id": "manutenzione/marker"}); r.IsError {
		t.Errorf("read refused: %+v", r)
	}
	if got := instructionsOf(t, f.post(t, tokAdmin, path+"&provisioned=1", initializeBody)); !strings.Contains(got, "read_only_kb") {
		t.Errorf("instructions = %q", got)
	}
}

func TestRoutedKBReadOnly_SubsetRefusesPerCall(t *testing.T) {
	t.Parallel()
	f := newViewFixture(t, nil, "kb-a", "kb-b")
	path := RoutedMountPath + "?readonly=kb-a"
	got := listedNames(t, f, path)
	if !got["concept_write"] {
		t.Fatal("write tools must stay listed when only some KBs are read-only")
	}
	res := f.call(t, tokAdmin, path, "concept_write", writeArgs("kb-a"))
	if !res.IsError || !strings.Contains(res.Content[0].Text, denyReadOnlyKB) {
		t.Fatalf("write to the read-only KB = %+v", res)
	}
	if r := f.call(t, tokAdmin, path, "concept_write", writeArgs("kb-b")); r.IsError && strings.Contains(r.Content[0].Text, denyReadOnlyKB) {
		t.Errorf("write to the other KB refused as read-only: %+v", r)
	}
	if r := f.call(t, tokAdmin, path, "concept_read", map[string]any{"kb": "kb-a", "id": "manutenzione/marker"}); r.IsError {
		t.Errorf("read of the read-only KB refused: %+v", r)
	}
	// A read-only target of a cross-KB move is refused and the source is untouched.
	marker := filepath.Join(f.kbs["kb-b"].DataRoot(), "manutenzione", "marker.md")
	for _, tc := range []struct {
		tool string
		args map[string]any
	}{
		{"concept_transfer", map[string]any{"kb": "kb-b", "target_kb": "kb-a", "moves": []map[string]any{{"source_id": "manutenzione/marker", "target_id": "manutenzione/marker"}}}},
		{"artifact_move", map[string]any{"kb": "kb-b", "target_kb": "kb-a", "paths": []string{"skills/x"}}},
	} {
		r := f.call(t, tokAdmin, path, tc.tool, tc.args)
		if !r.IsError || !strings.Contains(r.Content[0].Text, denyReadOnlyKB) {
			t.Errorf("%s into a read-only KB = %+v", tc.tool, r)
		}
		if _, err := os.Stat(marker); err != nil {
			t.Errorf("%s touched the source: %v", tc.tool, err)
		}
	}
	if s := instructionsOf(t, f.post(t, tokAdmin, path+"&provisioned=1", initializeBody)); !strings.Contains(s, "Read-only on this connection: kb-a") {
		t.Errorf("instructions lack the read-only sentence: %q", s)
	}
}

func TestRoutedKBReadOnly_SingleKBSetWithKBOmitted(t *testing.T) {
	t.Parallel()
	f := newViewFixture(t, nil, "kb-a", "kb-b")
	path := RoutedMountPath + "?kbs=kb-a&readonly=kb-a"
	res := f.call(t, tokAdmin, path, "concept_write", writeArgs(""))
	if !res.IsError || !strings.Contains(res.Content[0].Text, denyReadOnlyKB) {
		t.Fatalf("write with kb omitted = %+v", res)
	}
	// readonly names outside the set are dropped: kb-b is not in the set.
	rr := f.post(t, tokAdmin, RoutedMountPath+"?kbs=kb-a&readonly=kb-b", toolsListBody)
	if h := rr.Header().Get(ReadonlyHeader); h != "" {
		t.Errorf("header = %q, want empty", h)
	}
	if !listedNames(t, f, RoutedMountPath+"?kbs=kb-a&readonly=kb-b")["concept_write"] {
		t.Error("a name outside the set must not make the set read-only")
	}
}

func TestRoutedKBReadOnly_IgnoredNamesAndHeader(t *testing.T) {
	t.Parallel()
	f := newViewFixture(t, []config.KBAlias{{From: "kb-old", To: "kb-b"}}, "kb-a", "kb-b")
	for _, tc := range []struct{ query, header string }{
		{"readonly=kb-a", "kb-a"},
		{"readonly=kb-b,kb-a", "kb-a,kb-b"},
		{"readonly=nope,kb-a,%20", "kb-a"},
		{"readonly=", ""},
		{"readonly=nope", ""},
		{"readonly=kb-old", "kb-b"}, // renamed: applied to its target
	} {
		rr := f.post(t, tokAdmin, RoutedMountPath+"?"+tc.query, toolsListBody)
		if rr.Code != 200 {
			t.Errorf("%s: status %d", tc.query, rr.Code)
		}
		if _, ok := rr.Header()[ReadonlyHeader]; !ok {
			t.Errorf("%s: header absent", tc.query)
		}
		if got := rr.Header().Get(ReadonlyHeader); got != tc.header {
			t.Errorf("%s: header %q, want %q", tc.query, got, tc.header)
		}
	}
	// A name the token cannot read is indistinguishable from a missing one.
	if rr := f.post(t, tokRO, RoutedMountPath+"?readonly=kb-b", toolsListBody); rr.Header().Get(ReadonlyHeader) != "" {
		t.Errorf("unreadable name confirmed: %q", rr.Header().Get(ReadonlyHeader))
	}
	// The renamed name refuses the write on its target.
	res := f.call(t, tokAdmin, RoutedMountPath+"?readonly=kb-old", "concept_write", writeArgs("kb-b"))
	if !res.IsError || !strings.Contains(res.Content[0].Text, denyReadOnlyKB) {
		t.Errorf("write to the renamed target = %+v", res)
	}
	// Default request: no header at all.
	if rr := f.post(t, tokAdmin, RoutedMountPath, toolsListBody); len(rr.Header().Values(ReadonlyHeader)) != 0 {
		t.Error("default request carries the header")
	}
}

func TestRoutedKBReadOnly_AccessWins(t *testing.T) {
	t.Parallel()
	f := newViewFixture(t, nil, "kb-a", "kb-b")
	rr := f.post(t, tokAdmin, RoutedMountPath+"?access=read-min&readonly=kb-a", toolsListBody)
	if _, ok := rr.Header()[ReadonlyHeader]; !ok || rr.Header().Get(AccessHeader) != AccessReadMinValue {
		t.Errorf("headers = %v", rr.Header())
	}
	res := f.call(t, tokAdmin, RoutedMountPath+"?access=read-min&readonly=kb-a", "concept_write", writeArgs("kb-b"))
	if !res.IsError || !strings.Contains(res.Content[0].Text, denyReadOnlyProfile) {
		t.Errorf("read-min + readonly = %+v", res)
	}
	res = f.call(t, tokAdmin, RoutedMountPath+"?access=read&readonly=kb-a,kb-b", "concept_write", writeArgs("kb-b"))
	if !res.IsError || !strings.Contains(res.Content[0].Text, denyUnboundFolder) {
		t.Errorf("read + readonly = %+v", res)
	}
}

func TestRoutedKBReadOnly_TokenRefusalStaysTheTokens(t *testing.T) {
	t.Parallel()
	f := newViewFixture(t, nil, "kb-a", "kb-b")
	// tokMixed writes kb-a and only reads kb-b: kb-b is not read_only_kb (not named).
	res := f.call(t, tokMixed, RoutedMountPath+"?readonly=kb-a", "concept_write", writeArgs("kb-b"))
	if !res.IsError || !strings.Contains(res.Content[0].Text, denyReadOnlyToken) {
		t.Errorf("token-read-only KB = %+v", res)
	}
	// A token that writes nothing keeps its own view and its own refusal.
	res = f.call(t, tokRO, RoutedMountPath+"?readonly=kb-a", "concept_write", writeArgs("kb-a"))
	if !res.IsError || !strings.Contains(res.Content[0].Text, denyReadOnlyToken) {
		t.Errorf("read-only token = %+v", res)
	}
}

// The cached view is shared by requests with different readonly subsets: the
// names ride the context, never the view (race detector on).
func TestRoutedKBReadOnly_CachedViewSharedWithoutLeak(t *testing.T) {
	t.Parallel()
	f := newViewFixture(t, nil, "kb-a", "kb-b")
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ro, other := "kb-a", "kb-b"
			if i%2 == 1 {
				ro, other = other, ro
			}
			path := RoutedMountPath + "?readonly=" + ro
			if r := f.call(t, tokAdmin, path, "concept_write", writeArgs(ro)); !r.IsError || !strings.Contains(r.Content[0].Text, denyReadOnlyKB) {
				t.Errorf("%s not refused: %+v", ro, r)
			}
			if r := f.call(t, tokAdmin, path, "concept_write", writeArgs(other)); r.IsError && strings.Contains(r.Content[0].Text, denyReadOnlyKB) {
				t.Errorf("%s leaked another request's read-only: %+v", other, r)
			}
		}(i)
	}
	wg.Wait()
}

// Fail-closed: every registered tool that is not read-only, and a tool no one
// registered, is refused by the all-read-only view's authorizer.
func TestRoutedKBReadOnly_EveryWriteToolIsRefused(t *testing.T) {
	t.Parallel()
	f := newViewFixture(t, nil, "kb-a")
	view := f.multi.routedView([]string{"kb-a"}, routedKBsReadOnly)
	ctx := f.ctx(t, tokAdmin)
	checked := 0
	for name, tool := range f.multi.servers["kb-a"].Tools() {
		if tool.ReadOnly {
			continue
		}
		checked++
		if err := view.authorize(ctx, name, nil); err == nil || !strings.Contains(err.Error(), denyReadOnlyKB) {
			t.Errorf("%s: authorize = %v", name, err)
		}
	}
	if err := view.authorize(ctx, "tool_nobody_registered", nil); err == nil || !strings.Contains(err.Error(), denyReadOnlyKB) {
		t.Errorf("unknown tool: %v", err)
	}
	if checked == 0 {
		t.Fatal("no write tool found")
	}
}
