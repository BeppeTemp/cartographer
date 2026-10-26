package mcpserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/kbtest"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// archivedServer holds one live and one archived concept that both match
// "gateway", plus an archived-only term.
func archivedServer(t *testing.T) (*Server, *kb.KB) {
	t.Helper()
	k := kbtest.New(t)
	writeKBFile(t, k, "ops/_map.md", "---\ntype: Map\ntitle: Ops\n---\n")
	writeKBFile(t, k, "ops/live.md", "---\ntype: Note\ntitle: Live gateway\n---\n# Live\n\nThe gateway runs here.\n")
	writeKBFile(t, k, "ops/old.md", "---\ntype: Note\ntitle: Old gateway\nstatus: archived\n---\n# Old\n\nThe gateway failed. Zebrafish only here.\n")
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	return s, k
}

func searchIDs(t *testing.T, s *Server, args string) (ids []string, out map[string]interface{}) {
	t.Helper()
	out = decodeJSON(t, callOK(t, s, "search", args))
	if rs, ok := out["results"].([]interface{}); ok {
		for _, r := range rs {
			ids = append(ids, r.(map[string]interface{})["id"].(string))
		}
	}
	return ids, out
}

func TestSearch_ArchivedHiddenByDefault(t *testing.T) {
	t.Parallel()
	s, _ := archivedServer(t)
	ids, out := searchIDs(t, s, `{"query":"gateway"}`)
	if len(ids) != 1 || ids[0] != "ops/live" {
		t.Fatalf("default search must return only the live concept: %v", ids)
	}
	if _, ok := out["archived_fallback"]; ok {
		t.Fatalf("a live hit must not flag a fallback: %v", out)
	}
	ids, _ = searchIDs(t, s, `{"query":"gateway","include_archived":true}`)
	if len(ids) != 2 {
		t.Fatalf("include_archived must return both: %v", ids)
	}
}

func TestSearch_ArchivedFallback(t *testing.T) {
	t.Parallel()
	s, _ := archivedServer(t)
	ids, out := searchIDs(t, s, `{"query":"zebrafish"}`)
	if len(ids) != 1 || ids[0] != "ops/old" || out["archived_fallback"] != true {
		t.Fatalf("only an archived match must come back as a marked fallback: %v %v", ids, out)
	}
	if hit := out["results"].([]interface{})[0].(map[string]interface{}); hit["archived"] != true {
		t.Fatalf("hit not marked archived: %v", hit)
	}
	// An explicit false is a verification search: no fallback.
	ids, out = searchIDs(t, s, `{"query":"zebrafish","include_archived":false}`)
	if len(ids) != 0 || out["archived_fallback"] != nil {
		t.Fatalf("include_archived:false must not fall back: %v %v", ids, out)
	}
}

func TestReadCost_ExcludesArchived(t *testing.T) {
	t.Parallel()
	k := kbtest.New(t)
	writeKBFile(t, k, "ops/_map.md", "---\ntype: Map\ntitle: Ops\n---\n")
	writeKBFile(t, k, "ops/live.md", "---\ntype: Note\ntitle: L\n---\n# L\n")
	writeKBFile(t, k, "ops/big.md", "---\ntype: Note\ntitle: B\nstatus: archived\n---\n# B\n\n"+strings.Repeat("x", 10000)+"\n")
	rc, err := k.ReadCost(nil)
	if err != nil {
		t.Fatal(err)
	}
	if rc.ConceptBytes.Max >= 10000 {
		t.Fatalf("an archived concept must not count in read_cost: %+v", rc)
	}
}

func TestAtlasStructure_ExcludesArchived(t *testing.T) {
	t.Parallel()
	k := kbtest.New(t)
	writeKBFile(t, k, "ops/_map.md", "---\ntype: Map\ntitle: Ops\n---\n")
	writeKBFile(t, k, "ops/a.md", "---\ntype: Note\ntitle: Live A\n---\n# A\n\n[b](b.md)\n")
	writeKBFile(t, k, "ops/b.md", "---\ntype: Note\ntitle: Live B\n---\n# B\n\n[a](a.md)\n")
	for _, n := range []string{"x", "y", "z"} {
		writeKBFile(t, k, "ops/"+n+".md", "---\ntype: Note\ntitle: Gone "+n+"\nstatus: archived\n---\n# "+n+"\n\n[x](x.md) [y](y.md) [z](z.md)\n")
	}
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	out := callOK(t, s, "atlas_overview", `{"structure":true}`)
	i := strings.Index(out, "## Structure")
	if i < 0 {
		t.Fatalf("no structure section: %s", out)
	}
	if strings.Contains(out[i:], "Gone") || !strings.Contains(out[i:], "Live A") {
		t.Fatalf("structure must hold live concepts only: %s", out[i:])
	}
}

func TestLinkSuggest_SkipsArchived(t *testing.T) {
	t.Parallel()
	s := graphToolKB(t, map[string]string{
		"ops/u.md": "[a](a.md) [b](b.md).\n",
		"ops/a.md": "[x](x.md) [z](z.md).\n",
		"ops/b.md": "[x](x.md) [z](z.md).\n",
		"ops/x.md": "X.\n",
		"ops/z.md": "---\ntype: Note\nstatus: archived\n---\nZ.\n",
	})
	out := decodeJSON(t, mustText(t, s, "link_suggest", `{"id":"ops/u"}`))
	cands := out["candidates"].([]interface{})
	if len(cands) != 1 || cands[0].(map[string]interface{})["id"] != "ops/x" {
		t.Fatalf("an archived concept must not be suggested: %v", cands)
	}
}

// --- map_update: concept_types, harvest_after (D322) ---

func strictMapServer(t *testing.T) (*Server, *kb.KB) {
	t.Helper()
	k, s := repairKB(t, 0)
	if err := k.CreateMapWithContract("jr", "Journal", "journal", []string{"Note"}, kb.MapContract{}); err != nil {
		t.Fatal(err)
	}
	return s, k
}

func TestMapUpdate_ExtendsConceptTypes(t *testing.T) {
	t.Parallel()
	s, k := strictMapServer(t)
	digest := `{"operations":[{"op":"write","id":"jr/d","frontmatter":{"type":"Digest","title":"D"},"body":"# D\n\n[[anchor]]\n","index_entry":true}]}`
	if res := callTool(t, s, "concept_batch", digest); !res.IsError || !strings.Contains(res.Content[0].Text, "not allowed in map") {
		t.Fatal("strict map must refuse an unknown type")
	}
	callOK(t, s, "map_update", `{"map":"jr","concept_types":["Note","Digest","Digest"],"reason":"digests belong in this journal"}`)
	callOK(t, s, "concept_batch", digest)
	callOK(t, s, "map_update", `{"map":"jr","concept_types":[],"reason":"the journal takes any type again"}`)
	meta, err := k.ReadArchiveMeta("jr")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := meta.Get("concept_types"); ok {
		t.Fatal("an empty list must remove concept_types")
	}
}

func TestMapUpdate_ConceptTypesMakeTheMapStrict(t *testing.T) {
	t.Parallel()
	k, s := repairKB(t, 0)
	if c, err := k.ReadMapContract("ops"); err != nil || c.OntologyMode != "flexible" {
		t.Fatalf("ops starts flexible: %+v %v", c, err)
	}
	callOK(t, s, "map_update", `{"map":"ops","concept_types":["Note"]}`)
	if c, err := k.ReadMapContract("ops"); err != nil || c.OntologyMode != "strict" || len(c.ConceptTypes) != 1 {
		t.Fatalf("concept_types must make the map strict: %+v %v", c, err)
	}
}

func TestMapUpdate_HarvestAfter(t *testing.T) {
	t.Parallel()
	k, s := repairKB(t, 0)
	callOK(t, s, "map_update", `{"map":"ops","harvest_after":90}`)
	if c, err := k.ReadMapContract("ops"); err != nil || c.HarvestAfterDays != 90 {
		t.Fatalf("harvest_after not written: %+v %v", c, err)
	}
	if res := callTool(t, s, "map_update", `{"map":"ops","harvest_after":0}`); !res.IsError || !strings.Contains(res.Content[0].Text, "harvest_after must be") {
		t.Fatalf("harvest_after 0 must be refused: %+v", res)
	}
	if c, _ := k.ReadMapContract("ops"); c.HarvestAfterDays != 90 {
		t.Fatalf("a refused 0 must leave the value alone: %+v", c)
	}
	callOK(t, s, "map_update", `{"map":"ops","harvest_after":-1}`)
	if c, _ := k.ReadMapContract("ops"); c.HarvestAfterDays != 0 {
		t.Fatalf("harvest_after not removed: %+v", c)
	}
}

func TestMapUpdate_OpenField(t *testing.T) {
	t.Parallel()
	k, s := repairKB(t, 0)
	callOK(t, s, "map_update", `{"map":"ops","open_field":"outcome"}`)
	if c, err := k.ReadMapContract("ops"); err != nil || c.OpenField != "outcome" {
		t.Fatalf("open_field not written: %+v %v", c, err)
	}
	if res := callTool(t, s, "map_update", `{"map":"ops","open_field":"a b"}`); !res.IsError {
		t.Fatal("a non-flat open_field must be refused")
	}
	callOK(t, s, "map_update", `{"map":"ops","open_field":""}`)
	if c, _ := k.ReadMapContract("ops"); c.OpenField != "" {
		t.Fatalf("open_field not cleared: %+v", c)
	}
}

// --- read-access log (D322) ---

func TestReadAccessLogRoundTrip(t *testing.T) {
	t.Parallel()
	k := kbtest.New(t)
	l := newReadAccessLog(k)
	if r, s, since := l.summary(); r.TrackedConcepts != 0 || s.TrackedConcepts != 0 || since == "" {
		t.Fatalf("fresh log = %+v %+v %q", r, s, since)
	}
	l.record("ops/a", "ops/b")
	l.recordSurfaced("ops/c")
	if _, ok := l.lastReadTime("ops/a"); !ok {
		t.Fatal("ops/a not recorded")
	}
	l.save()
	again := newReadAccessLog(k)
	got, ok := again.lastReadTime("ops/b")
	want, _ := l.lastReadTime("ops/b")
	if !ok || got.Unix() != want.Unix() {
		t.Fatalf("reload lost the entry: %v %v", got, ok)
	}
	if _, ok := again.lastSurfacedTime("ops/c"); !ok {
		t.Fatal("reload lost the surfaced entry")
	}
	r, s, since := again.summary()
	if r.TrackedConcepts != 2 || r.Oldest == "" || r.Newest == "" || s.TrackedConcepts != 1 {
		t.Fatalf("summary = %+v %+v", r, s)
	}
	if _, _, first := l.summary(); since != first {
		t.Fatalf("since moved on reload: %q, was %q", since, first)
	}
	var nilLog *readAccessLog
	nilLog.record("x") // the UI path: must not panic
	nilLog.recordSurfaced("x")
}

func TestReadAccessLogFlushesAfterInterval(t *testing.T) {
	k := kbtest.New(t)
	now := time.Now()
	prev := readAccessNow
	readAccessNow = func() time.Time { return now }
	t.Cleanup(func() { readAccessNow = prev })
	l := newReadAccessLog(k)
	l.record("ops/a")
	if r, _, _ := newReadAccessLog(k).summary(); r.TrackedConcepts != 0 {
		t.Fatal("a record inside the interval must not write the file")
	}
	now = now.Add(readAccessFlushEvery)
	l.recordSurfaced("ops/b")
	r, s, _ := newReadAccessLog(k).summary()
	if r.TrackedConcepts != 1 || s.TrackedConcepts != 1 {
		t.Fatalf("a record past the interval must flush both maps: %+v %+v", r, s)
	}
	data, err := os.ReadFile(filepath.Join(k.Root, ".cartographer", readAccessFile))
	if err != nil {
		t.Fatal(err)
	}
	var f readAccessFileV2
	if err := json.Unmarshal(data, &f); err != nil || f.Since == "" || len(f.Read) != 1 || len(f.Surfaced) != 1 {
		t.Fatalf("file shape = %s (%v)", data, err)
	}
}

// A file written before reads and hits were told apart may hold hits nobody
// opened: its entries come back as surfaced, never as reads, and reads start
// counting from the upgrade.
func TestReadAccessLogMigratesTheFlatFile(t *testing.T) {
	k := kbtest.New(t)
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	prev := readAccessNow
	readAccessNow = func() time.Time { return now }
	t.Cleanup(func() { readAccessNow = prev })
	path := filepath.Join(k.Root, ".cartographer", readAccessFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"ops/a":"2026-10-06T09:00:00Z","ops/b":"2026-10-07T09:00:00Z"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	l := newReadAccessLog(k)
	if _, ok := l.lastReadTime("ops/a"); ok {
		t.Fatal("a flat-file entry must not come back as a read")
	}
	if got, ok := l.lastSurfacedTime("ops/b"); !ok || !got.Equal(time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)) {
		t.Fatalf("flat-file entry not kept as surfaced: %v %v", got, ok)
	}
	if _, _, since := l.summary(); since != "2026-10-13T12:00:00Z" {
		t.Fatalf("since = %q, want the upgrade instant", since)
	}
	l.save()
	var f readAccessFileV2
	data, _ := os.ReadFile(path)
	if err := json.Unmarshal(data, &f); err != nil || len(f.Surfaced) != 2 || len(f.Read) != 0 || f.Since != "2026-10-13T12:00:00Z" {
		t.Fatalf("migrated file = %s (%v)", data, err)
	}
}

func TestReadsAreTracked(t *testing.T) {
	t.Parallel()
	s, _ := archivedServer(t)
	if _, ok := kbStatusResult(t, s)["read_access"]; ok {
		t.Fatal("read_access present before any read")
	}
	callOK(t, s, "concept_read", `{"id":"ops/old"}`)
	callOK(t, s, "search", `{"query":"gateway"}`) // hits ops/live only
	callOK(t, s, "graph_neighbors", `{"id":"ops/live"}`)
	var ra struct {
		Tracked  int    `json:"tracked_concepts"`
		Since    string `json:"since"`
		Surfaced struct {
			Tracked int `json:"tracked_concepts"`
		} `json:"surfaced"`
	}
	if err := json.Unmarshal(kbStatusResult(t, s)["read_access"], &ra); err != nil {
		t.Fatal(err)
	}
	if ra.Tracked != 1 || ra.Surfaced.Tracked != 1 || ra.Since == "" {
		t.Fatalf("read_access = %+v: only ops/old was read, only ops/live was a hit", ra)
	}
}

// Only concept_read is a read: a hit is a pointer the agent may never follow,
// and navigation is neither.
func TestSearchHitIsNotARead(t *testing.T) {
	now := time.Now()
	prev := readAccessNow
	readAccessNow = func() time.Time { return now }
	t.Cleanup(func() { readAccessNow = prev })
	// Trap: the clock is set before the server exists, which owns the log. A
	// second server on the same *kb.KB would get an empty search index: the
	// graph cache hands each change to one reconciler only (D245).
	s, k := archivedServer(t)
	callOK(t, s, "search", `{"query":"gateway"}`)
	callOK(t, s, "graph_context", `{"ids":["ops/live"]}`)
	now = now.Add(readAccessFlushEvery) // the next record flushes the file
	callOK(t, s, "concept_read", `{"id":"ops/old"}`)
	l := newReadAccessLog(k)
	if _, ok := l.lastReadTime("ops/live"); ok {
		t.Fatal("a search hit was recorded as a read")
	}
	if _, ok := l.lastSurfacedTime("ops/live"); !ok {
		t.Fatal("the hit was not recorded as surfaced")
	}
	if _, ok := l.lastReadTime("ops/old"); !ok {
		t.Fatal("concept_read was not recorded as a read")
	}
	if _, ok := l.lastSurfacedTime("ops/old"); ok {
		t.Fatal("concept_read must not stamp surfaced")
	}
}

// D346: stale_after -1 resets to the default, 0 is refused (no way to switch
// it off), the response echoes what was written, and map_list shows the
// effective value.
func TestMapUpdate_StaleAfterReset(t *testing.T) {
	t.Parallel()
	k, s := repairKB(t, 0)
	out := callOK(t, s, "map_update", `{"map":"ops","open_statuses":["open"],"harvest_after":90,"stale_after":90}`)
	for _, want := range []string{`"stale_after": 90`, `"open_statuses": [`, `"harvest_after": 90`} {
		if !strings.Contains(out, want) {
			t.Fatalf("echo lacks %s: %s", want, out)
		}
	}
	if strings.Contains(out, "stale_after_defaulted") {
		t.Fatalf("explicit value reported as defaulted: %s", out)
	}
	if res := callTool(t, s, "map_update", `{"map":"ops","stale_after":0}`); !res.IsError || !strings.Contains(res.Content[0].Text, "stale_after must be") {
		t.Fatalf("stale_after 0 must be refused: %+v", res)
	}
	if c, err := k.ReadMapContract("ops"); err != nil || c.StaleAfterDays != 90 {
		t.Fatalf("a refused 0 must leave the value alone: %+v %v", c, err)
	}
	out = callOK(t, s, "map_update", `{"map":"ops","stale_after":-1}`)
	if !strings.Contains(out, `"stale_after": 30`) || !strings.Contains(out, `"stale_after_defaulted": true`) {
		t.Fatalf("-1 must fall back to the work-map default: %s", out)
	}
	if c, _ := k.ReadMapContract("ops"); c.StaleAfterDays != 0 {
		t.Fatalf("-1 must delete the key: %+v", c)
	}
	if out := callOK(t, s, "map_list", `{}`); !strings.Contains(out, `"stale_after": 30`) || !strings.Contains(out, `"stale_after_defaulted": true`) {
		t.Fatalf("map_list lacks the effective value: %s", out)
	}
	if res := callTool(t, s, "map_update", `{"map":"ops","stale_after":-2}`); !res.IsError || !strings.Contains(res.Content[0].Text, "stale_after must be") {
		t.Fatalf("-2 must be refused: %+v", res)
	}
}

func TestMapList_StaleAfterJournalAndReference(t *testing.T) {
	t.Parallel()
	s, _ := strictMapServer(t) // jr is a journal; ops is a plain map
	out := callOK(t, s, "map_list", `{}`)
	var infos []map[string]interface{}
	if err := json.Unmarshal([]byte(out), &infos); err != nil {
		t.Fatal(err)
	}
	for _, m := range infos {
		switch m["name"] {
		case "jr":
			if m["stale_after"] != float64(60) || m["stale_after_defaulted"] != true {
				t.Fatalf("journal: %v", m)
			}
		case "ops":
			if _, ok := m["stale_after"]; ok {
				t.Fatalf("reference map must omit stale_after: %v", m)
			}
		}
	}
}

// TestConceptMove_RefusesATypeTheStrictTargetDoesNotAdmit: a move into a strict
// map that does not list the concept's type used to succeed and leave the page
// failing the next validate (#921). It is refused before anything is written;
// a type the map admits still moves.
func TestConceptMove_RefusesATypeTheStrictTargetDoesNotAdmit(t *testing.T) {
	t.Parallel()
	s, k := strictMapServer(t)
	for id, typ := range map[string]string{"ops/client": "Client", "ops/note": "Note"} {
		fm := newFM()
		fm.Set("type", typ)
		fm.Set("title", id)
		if _, err := k.WriteConcept(okf.ConceptID(id), fm, "# T\n", ""); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := k.CommitOp("test: seed"); err != nil {
		t.Fatal(err)
	}

	res := callTool(t, s, "concept_move", `{"source_id":"ops/client","target_id":"jr/client"}`)
	if !res.IsError {
		t.Fatal("a Client page must not move into a map that admits only Note")
	}
	for _, want := range []string{`"Client"`, "map jr", "Note", "Nothing was moved"} {
		if !strings.Contains(res.Content[0].Text, want) {
			t.Errorf("refusal lacks %q: %s", want, res.Content[0].Text)
		}
	}
	if _, err := k.ReadConcept("ops/client"); err != nil {
		t.Errorf("the source must still be there: %v", err)
	}
	if _, err := k.ReadConcept("jr/client"); err == nil {
		t.Error("the target must not exist")
	}

	// One refused entry aborts the whole batch.
	res = callTool(t, s, "concept_move", `{"moves":[{"source_id":"ops/note","target_id":"jr/note"},{"source_id":"ops/client","target_id":"jr/client"}]}`)
	if !res.IsError {
		t.Fatal("a batch with a refused entry must abort")
	}
	if _, err := k.ReadConcept("ops/note"); err != nil {
		t.Errorf("the admitted page of an aborted batch must stay: %v", err)
	}

	callOK(t, s, "concept_move", `{"source_id":"ops/note","target_id":"jr/note"}`)
	if _, err := k.ReadConcept("jr/note"); err != nil {
		t.Errorf("an admitted type must move: %v", err)
	}
}

// A template narrower than its map is allowed by design (D352): map_update says
// so in "hints", and stays silent when the lists agree.
func TestMapUpdate_HintsWhenATemplateNarrowsTheVocabulary(t *testing.T) {
	t.Parallel()
	k, s := repairKB(t, 0)
	callOK(t, s, "map_update", `{"map":"ops","field_values":{"*":{"state":["up","down","parked"]}}}`)
	tpl := func(values string) {
		t.Helper()
		abs := filepath.Join(k.Root, "templates", "server.md")
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		content := "---\ntype: Host\ntitle: \"{{title}}\"\nx-template.field_values.state: " + values + "\n---\n# {{title}}\n"
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	hints := func(out string) []string {
		t.Helper()
		var r struct {
			Hints []string `json:"hints"`
		}
		if err := json.Unmarshal([]byte(out), &r); err != nil {
			t.Fatal(err)
		}
		return r.Hints
	}

	tpl("[up, down]")
	got := hints(callOK(t, s, "map_update", `{"map":"ops","templates":["server"]}`))
	want := `template "server" allows only [down, up] for "state" while map allows [down, parked, up]: pages with the other values get template_field_value`
	if len(got) != 1 || got[0] != want {
		t.Fatalf("hints = %q, want [%q]", got, want)
	}

	tpl("[up, down, parked]")
	if got := hints(callOK(t, s, "map_update", `{"map":"ops","templates":["server"]}`)); len(got) != 0 {
		t.Fatalf("equal lists: no hint, got %q", got)
	}
	if got := hints(callOK(t, s, "map_update", `{"map":"ops","title":"Ops"}`)); len(got) != 0 {
		t.Fatalf("an update that sets no template key: no hint, got %q", got)
	}
}

// --- map_update: a per-type vocabulary that shadows a map-wide one (#837) ---
// map_create is out of scope: its author has the list in front of them.

func mapUpdateWarnings(t *testing.T, s *Server, args string) []string {
	t.Helper()
	var out struct {
		Warnings []string `json:"warnings"`
	}
	if err := json.Unmarshal([]byte(callOK(t, s, "map_update", args)), &out); err != nil {
		t.Fatal(err)
	}
	return out.Warnings
}

func TestMapUpdate_WarnsWhenAPerTypeVocabularyShadowsTheMapWideOne(t *testing.T) {
	t.Parallel()
	_, s := repairKB(t, 0)
	callOK(t, s, "map_update", `{"map":"ops","field_values":{"*":{"status":["active","draft","archived"]}}}`)

	// Narrower per-type list: names the type, the field and the hidden values.
	w := mapUpdateWarnings(t, s, `{"map":"ops","field_values":{"*":{"status":["active","draft","archived"]},"Incident":{"status":["active","draft"]}},"reason":"incidents never archive"}`)
	if len(w) != 1 || !strings.Contains(w[0], `type "Incident"`) || !strings.Contains(w[0], `field "status"`) ||
		!strings.Contains(w[0], "rejected") || !strings.HasSuffix(w[0], "archived") {
		t.Fatalf("shadow warning = %v", w)
	}

	// Superset: the list is replaced, not extended, with no "rejected" clause.
	w = mapUpdateWarnings(t, s, `{"map":"ops","field_values":{"*":{"status":["active","draft","archived"]},"Incident":{"status":["active","draft","archived","open"]}},"reason":"widen"}`)
	if len(w) != 1 || strings.Contains(w[0], "rejected") || !strings.Contains(w[0], "replaces the map-wide one") {
		t.Fatalf("superset warning = %v", w)
	}

	// A write that does not carry field_values is not nagged about the old shadow.
	if w = mapUpdateWarnings(t, s, `{"map":"ops","harvest_after":90}`); len(w) != 0 {
		t.Fatalf("unrelated update warned: %v", w)
	}

	// A title warning and a shadow warning coexist.
	w = mapUpdateWarnings(t, s, `{"map":"ops","title":"Completely Unrelated Name","field_values":{"*":{"status":["active","draft","archived"]},"Incident":{"status":["active"]}},"reason":"narrow"}`)
	if len(w) != 2 || !strings.Contains(w[0], "map title") || !strings.Contains(w[1], `type "Incident"`) {
		t.Fatalf("title+shadow warnings = %v", w)
	}
}

func TestMapUpdate_NoShadowNoWarnings(t *testing.T) {
	t.Parallel()
	_, s := repairKB(t, 0)
	out := callOK(t, s, "map_update", `{"map":"ops","field_values":{"Incident":{"status":["active"]}}}`)
	if strings.Contains(out, "warnings") {
		t.Fatalf("no map-wide list, yet warned: %s", out)
	}
}
