package mcpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/skillbundle"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// D1097: every connection that does not declare itself provisioned gets the
// operating guide in initialize, for its own KBs and its own visibility.

func writeInstructions(t *testing.T, f *viewFixture, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.kbs[name].Root, "instructions.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (f *viewFixture) instructions(t *testing.T, token, path string) string {
	t.Helper()
	return instructionsOf(t, f.post(t, token, path, initializeBody))
}

func TestGuide_TwoKBsInNameOrder(t *testing.T) {
	t.Parallel()
	f := newViewFixture(t, nil, "kb-a", "kb-b")
	writeInstructions(t, f, "kb-a", "Rules of kb-a.\n")
	writeInstructions(t, f, "kb-b", "Rules of kb-b.\n")
	got := f.instructions(t, tokAdmin, RoutedMountPath)
	idx := func(s string) int { return strings.Index(got, s) }
	for _, want := range []string{"pass `kb: \"kb-a\"`", "pass `kb: \"kb-b\"`", "Maps: manutenzione/", "Operational instructions:", "Rules of kb-a.", "Rules of kb-b."} {
		if idx(want) < 0 {
			t.Fatalf("guide lacks %q:\n%s", want, got)
		}
	}
	if !(idx("pass `kb: \"kb-a\"`") < idx("pass `kb: \"kb-b\"`") && idx("pass `kb: \"kb-b\"`") < idx("Operational instructions:") &&
		idx("Operational instructions:") < idx("Rules of kb-a.") && idx("Rules of kb-a.") < idx("Rules of kb-b.")) {
		t.Errorf("order must be routing a, routing b, bullets, curated a, curated b:\n%s", got)
	}
	if strings.Count(got, "Operational instructions:") != 1 {
		t.Errorf("bullets must appear once:\n%s", got)
	}
}

func TestGuide_KBSNarrowsTheGuide(t *testing.T) {
	t.Parallel()
	f := newViewFixture(t, nil, "kb-a", "work-kb")
	writeInstructions(t, f, "work-kb", "Secret rules of work-kb.\n")
	got := f.instructions(t, tokAdmin, RoutedMountPath+"?kbs=kb-a")
	if strings.Contains(got, "work-kb") {
		t.Errorf("?kbs=kb-a leaked work-kb:\n%s", got)
	}
	if !strings.Contains(got, "kb-a") {
		t.Errorf("guide must name kb-a:\n%s", got)
	}
}

func TestGuide_ProvisionedGetsNone(t *testing.T) {
	t.Parallel()
	f := newViewFixture(t, nil, "kb-a")
	if got := f.instructions(t, tokAdmin, RoutedMountPath+"?provisioned=1"); got != "" {
		t.Errorf("provisioned instructions = %q, want none", got)
	}
	if got := f.instructions(t, tokAdmin, RoutedMountPath+"?provisioned=1&access=read"); got != readOnlyInstructions {
		t.Errorf("provisioned read-only instructions = %q, want readOnlyInstructions", got)
	}
}

func TestGuide_MapScopedTokenSeesItsMapsButTheCuratedText(t *testing.T) {
	t.Parallel()
	f := newViewFixture(t, nil, "kb-a")
	if err := os.MkdirAll(filepath.Join(f.kbs["kb-a"].DataRoot(), "hidden-map"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeInstructions(t, f, "kb-a", "Curated rules.\n")
	got := f.instructions(t, tokMapRule, RoutedMountPath)
	if !strings.Contains(got, "Maps: manutenzione/.") || strings.Contains(got, "hidden-map") {
		t.Errorf("archives must be the visible ones only:\n%s", got)
	}
	if !strings.Contains(got, "Curated rules.") {
		t.Errorf("a token with a rule on the KB gets its curated text:\n%s", got)
	}
}

func TestGuide_NoRuleOnTheKBGetsNothing(t *testing.T) {
	t.Parallel()
	f := newViewFixture(t, nil, "kb-a")
	writeInstructions(t, f, "kb-a", "Curated rules.\n")
	rr := f.post(t, tokNone, "/mcp/kb-a", initializeBody)
	if strings.Contains(rr.Body.String(), "Curated rules.") || strings.Contains(rr.Body.String(), "Operational instructions") {
		t.Errorf("a token with no rule on the KB got text: %s", rr.Body.String())
	}
	if got := f.instructions(t, tokNone, RoutedMountPath); strings.Contains(got, "kb-a") {
		t.Errorf("a token with no rule named kb-a: %q", got)
	}
}

func TestGuide_SymlinkedInstructionsLeaksNothing(t *testing.T) {
	t.Parallel()
	f := newViewFixture(t, nil, "kb-a")
	outside := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(outside, []byte("OUTSIDE-SECRET\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(f.kbs["kb-a"].Root, "instructions.md")
	_ = os.Remove(link)
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("no symlinks: %v", err)
	}
	got := f.instructions(t, tokAdmin, RoutedMountPath)
	if strings.Contains(got, "OUTSIDE-SECRET") || !strings.Contains(got, "Operational instructions:") {
		t.Errorf("a symlinked instructions.md must yield no curated text but still the guide:\n%s", got)
	}
}

func TestGuide_SymlinkedDataListsNoArchive(t *testing.T) {
	t.Parallel()
	f := newViewFixture(t, nil, "kb-a")
	root := f.kbs["kb-a"].Root
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(outside, "elsewhere"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := guideArchiveNames(root); len(got) == 0 {
		t.Fatalf("fixture has archives: %v", got)
	}
	link := filepath.Join(t.TempDir(), "kb")
	if err := os.MkdirAll(link, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(link, "data")); err != nil {
		t.Skipf("no symlinks: %v", err)
	}
	if got := guideArchiveNames(link); len(got) != 0 {
		t.Errorf("a symlinked data/ listed %v", got)
	}
}

// A host that lists only tools/list can follow the guide (D123).
func TestGuide_NamesOnlyListedTools(t *testing.T) {
	t.Parallel()
	f := newViewFixture(t, nil, "kb-a", "kb-b")
	got := f.instructions(t, tokAdmin, RoutedMountPath)
	bullets := got[strings.Index(got, "Operational instructions:"):]
	if end := strings.Index(bullets, "\n\n"); end > 0 {
		bullets = bullets[:end]
	}
	found := 0
	for _, m := range regexp.MustCompile("`([a-z_]+)`").FindAllStringSubmatch(bullets, -1) {
		name := m[1]
		if !strings.Contains(name, "_") && name != "search" {
			continue
		}
		if name == "waiting_on" || name == "review_after" || name == "in_progress" {
			continue
		}
		found++
		if !IsToolName(name) {
			t.Errorf("guide names %q, which is not a tool", name)
		} else if ToolAdvanced(name) {
			t.Errorf("guide names %q, an advanced tool that tools/list hides", name)
		}
	}
	if found < 5 {
		t.Errorf("only %d tool names found in the guide:\n%s", found, bullets)
	}
}

func TestGuide_FreshAtEachHandshake(t *testing.T) {
	t.Parallel()
	f := newViewFixture(t, nil, "kb-a")
	first := f.instructions(t, tokAdmin, RoutedMountPath)
	view1 := f.view(t, tokAdmin, nil, false)
	writeInstructions(t, f, "kb-a", "Brand new directions.\n")
	if err := os.MkdirAll(filepath.Join(f.kbs["kb-a"].DataRoot(), "fresh-map"), 0o755); err != nil {
		t.Fatal(err)
	}
	second := f.instructions(t, tokAdmin, RoutedMountPath)
	if first == second || !strings.Contains(second, "Brand new directions.") || !strings.Contains(second, "fresh-map/") {
		t.Errorf("the guide did not follow the disk:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
	if view1 != f.view(t, tokAdmin, nil, false) {
		t.Error("the routed view was rebuilt")
	}
}

func TestGuide_PerKBEndpoint(t *testing.T) {
	t.Parallel()
	f := newViewFixture(t, nil, "kb-a", "kb-b")
	for _, path := range []string{"/mcp/kb-a", "/mcp?kb=kb-a"} {
		got := f.instructions(t, tokAdmin, path)
		if !strings.Contains(got, `The "kb-a" KB`) || strings.Contains(got, "kb-b") || strings.Contains(got, "`kb` argument") {
			t.Errorf("%s: per-KB guide = %q", path, got)
		}
		// The marker is read on the routed path only.
		if prov := f.instructions(t, tokAdmin, path+map[bool]string{true: "&", false: "?"}[strings.Contains(path, "?")]+"provisioned=1"); prov == "" {
			t.Errorf("%s: a per-KB endpoint ignores the marker and still sends the guide", path)
		}
	}
}

func TestGuide_DiscoverCarriesTheSameTextPrivately(t *testing.T) {
	t.Parallel()
	f := newViewFixture(t, nil, "kb-a")
	view := f.view(t, tokAdmin, nil, false)
	ctx := f.ctx(t, tokAdmin)
	res := &sdk.DiscoverResult{}
	next := func(context.Context, string, sdk.Request) (sdk.Result, error) { return res, nil }
	if _, err := view.guideMiddleware()(next)(ctx, "server/discover", nil); err != nil {
		t.Fatal(err)
	}
	if res.CacheScope != "private" || res.Instructions != view.connectionGuide(ctx) || res.Instructions == "" {
		t.Errorf("discover: scope=%q instructions=%q", res.CacheScope, res.Instructions)
	}
	res = &sdk.DiscoverResult{}
	if _, err := view.guideMiddleware()(next)(withProvisioned(ctx), "server/discover", nil); err != nil {
		t.Fatal(err)
	}
	if res.Instructions != "" {
		t.Errorf("provisioned discover instructions = %q", res.Instructions)
	}
}

func TestGuide_StdioInitializeCarriesTheSingleKBGuide(t *testing.T) {
	t.Parallel()
	k := setupNamedTestKB(t, "kb-a")
	s := New("test")
	s.SetPolicyKB("kb-a")
	RegisterKBTools(s, k, Deps{})
	resps := runStdioSession(t, s.Run, []string{initializeBody})
	if len(resps) != 1 {
		t.Fatalf("expected 1 response, got %d", len(resps))
	}
	var res struct {
		Instructions string `json:"instructions"`
	}
	raw, _ := json.Marshal(resps[0].Result)
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Instructions, `The "kb-a" KB`) || strings.Contains(res.Instructions, "`kb` argument") {
		t.Errorf("stdio initialize instructions: %q", res.Instructions)
	}
}

func TestProvisionedParam(t *testing.T) {
	t.Parallel()
	handler := newRoutedTestHandler(t, "kb-a")
	status := func(path string) int {
		return routedPost(t, handler, path, toolsListBody).Code
	}
	for _, ok := range []string{"?provisioned=1", "?provisioned=", "?provisioned=1&access=read", "?kbs=kb-a&provisioned=1", "?foo=bar", "?provisioned=1&foo=bar"} {
		if c := status(RoutedMountPath + ok); c != http.StatusOK {
			t.Errorf("%s: status %d, want 200", ok, c)
		}
	}
	for _, bad := range []string{"0", "yes", "true"} {
		rr := routedPost(t, handler, RoutedMountPath+"?provisioned="+bad, toolsListBody)
		if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), `"1"`) {
			t.Errorf("provisioned=%s: %d %s", bad, rr.Code, rr.Body.String())
		}
	}
	if c := status(RoutedMountPath + "?kb=kb-a&provisioned=1"); c != http.StatusBadRequest {
		t.Errorf("?kb= still conflicts: %d", c)
	}
}

func TestProvisionedContextCarrier(t *testing.T) {
	t.Parallel()
	if isProvisioned(context.Background()) || !isProvisioned(withProvisioned(context.Background())) {
		t.Error("withProvisioned/isProvisioned do not round-trip")
	}
	f := newViewFixture(t, nil, "kb-a")
	if f.view(t, tokAdmin, nil, false) != f.view(t, tokAdmin, nil, false) {
		t.Error("the marker must not touch the view cache")
	}
}

func TestGuide_OversizeInstructionsAreCut(t *testing.T) {
	t.Parallel()
	f := newViewFixture(t, nil, "kb-a")
	writeInstructions(t, f, "kb-a", "HEAD-RULE\n"+strings.Repeat("filler line of text\n", 20000)+"TAIL-RULE\n")
	got := f.instructions(t, tokAdmin, RoutedMountPath)
	if !strings.Contains(got, "HEAD-RULE") || strings.Contains(got, "TAIL-RULE") || !strings.Contains(got, "bytes left out") {
		t.Errorf("an oversize instructions.md must be cut with a marker")
	}
	if len(got) > 70*1024 {
		t.Errorf("guide is %d bytes, not bounded", len(got))
	}
}

// #1316: a bare agent on an empty KB gets the cold-start section and the
// procedures list in `initialize`, with the whole-guide line; a KB with maps
// gets the list and no cold start.
func TestGuide_ColdStartAndProceduresInInitialize(t *testing.T) {
	t.Parallel()
	f := newViewFixtureWithBundle(t, skillbundle.FS, nil, "kb-a")
	withMaps := f.instructions(t, tokAdmin, RoutedMountPath)
	for _, want := range []string{"`ingest`", "`conflict-resolve`", "`import`", "the whole guide", "Maps: manutenzione/"} {
		if !strings.Contains(withMaps, want) {
			t.Errorf("guide lacks %q:\n%s", want, withMaps)
		}
	}
	if strings.Contains(withMaps, "This KB is empty") {
		t.Errorf("a KB with maps gets no cold-start section:\n%s", withMaps)
	}
	if err := os.RemoveAll(filepath.Join(f.kbs["kb-a"].Root, "data", "manutenzione")); err != nil {
		t.Fatal(err)
	}
	empty := f.instructions(t, tokAdmin, RoutedMountPath)
	for _, want := range []string{"No maps yet.", "This KB is empty", "`map_create`", "Reserved map name: services", "`ingest`"} {
		if !strings.Contains(empty, want) {
			t.Errorf("empty-KB guide lacks %q:\n%s", want, empty)
		}
	}
	// The same host with no bundle lists no procedure and names none.
	bare := newViewFixtureWithBundle(t, nil, nil, "kb-a")
	if got := bare.instructions(t, tokAdmin, RoutedMountPath); strings.Contains(got, "procedure: <name>") {
		t.Errorf("no bundle, no procedures list:\n%s", got)
	}
}

// The map_create description names the reserved set (the constant it reads).
func TestMapCreateDescriptionNamesReservedNames(t *testing.T) {
	t.Parallel()
	d := toolMapCreate(setupTestKB(t)).Description
	for _, r := range kb.ReservedMapNames {
		if !strings.Contains(d, r) {
			t.Errorf("map_create description does not name reserved %q: %s", r, d)
		}
	}
}
