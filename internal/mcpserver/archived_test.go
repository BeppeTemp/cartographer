package mcpserver

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/kb"
)

// archivedServer holds one live and one archived concept that both match
// "gateway", plus an archived-only term.
func archivedServer(t *testing.T) (*Server, *kb.KB) {
	t.Helper()
	k, err := kb.Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
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
	k, err := kb.Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
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
	k, err := kb.Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
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

// --- map_update: concept_types, ontology_mode, harvest_after (D322) ---

func strictMapServer(t *testing.T) (*Server, *kb.KB) {
	t.Helper()
	k, s := repairKB(t, 0)
	if err := k.CreateMapWithContract("jr", "Journal", "journal", []string{"Note"}, "strict", kb.MapContract{}); err != nil {
		t.Fatal(err)
	}
	return s, k
}

func TestMapUpdate_ExtendsConceptTypes(t *testing.T) {
	s, k := strictMapServer(t)
	digest := `{"operations":[{"op":"write","id":"jr/d","frontmatter":{"type":"Digest","title":"D"},"body":"# D"}]}`
	if res := callTool(t, s, "concept_batch", digest); !res.IsError || !strings.Contains(res.Content[0].Text, "not allowed in map") {
		t.Fatal("strict map must refuse an unknown type")
	}
	callOK(t, s, "map_update", `{"map":"jr","concept_types":["Note","Digest","Digest"]}`)
	callOK(t, s, "concept_batch", digest)
	callOK(t, s, "map_update", `{"map":"jr","concept_types":[]}`)
	meta, err := k.ReadArchiveMeta("jr")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := meta.Get("concept_types"); ok {
		t.Fatal("an empty list must remove concept_types")
	}
}

func TestMapUpdate_OntologyStrictInfersTypes(t *testing.T) {
	k, s := repairKB(t, 3) // ops holds Note concepts, flexible
	callOK(t, s, "map_update", `{"map":"ops","ontology_mode":"strict"}`)
	meta, err := k.ReadArchiveMeta("ops")
	if err != nil {
		t.Fatal(err)
	}
	ct, _ := meta.Get("concept_types")
	if got, _ := ct.([]string); len(got) != 1 || got[0] != "Note" {
		t.Fatalf("concept_types must be inferred from the map: %v", ct)
	}
}

func TestMapUpdate_ConceptTypesOnFlexibleRefused(t *testing.T) {
	_, s := repairKB(t, 0)
	res := callTool(t, s, "map_update", `{"map":"ops","concept_types":["Note"]}`)
	if !res.IsError || !strings.Contains(res.Content[0].Text, "flexible") {
		t.Fatalf("want a refusal naming the flexible map: %+v", res)
	}
	callOK(t, s, "map_update", `{"map":"ops","ontology_mode":"strict","concept_types":["Note"]}`)
	if res := callTool(t, s, "map_update", `{"map":"ops","ontology_mode":"bogus"}`); !res.IsError {
		t.Fatal("an unknown ontology_mode must be refused")
	}
}

func TestMapUpdate_HarvestAfter(t *testing.T) {
	k, s := repairKB(t, 0)
	callOK(t, s, "map_update", `{"map":"ops","harvest_after":90}`)
	if c, err := k.ReadMapContract("ops"); err != nil || c.HarvestAfterDays != 90 {
		t.Fatalf("harvest_after not written: %+v %v", c, err)
	}
	callOK(t, s, "map_update", `{"map":"ops","harvest_after":0}`)
	if c, _ := k.ReadMapContract("ops"); c.HarvestAfterDays != -1 || len(c.Malformed) != 0 {
		t.Fatalf("harvest_after 0 must read back as off: %+v", c)
	}
	callOK(t, s, "map_update", `{"map":"ops","harvest_after":-1}`)
	if c, _ := k.ReadMapContract("ops"); c.HarvestAfterDays != 0 {
		t.Fatalf("harvest_after not removed: %+v", c)
	}
}

func TestMapUpdate_OpenField(t *testing.T) {
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
	k, err := kb.Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	l := newReadAccessLog(k)
	if n, _, _ := l.summary(); n != 0 {
		t.Fatalf("fresh log tracks %d", n)
	}
	l.record("ops/a", "ops/b")
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
	if n, oldest, newest := again.summary(); n != 2 || oldest == "" || newest == "" {
		t.Fatalf("summary = %d %q %q", n, oldest, newest)
	}
	var nilLog *readAccessLog
	nilLog.record("x") // the UI path: must not panic
}

func TestReadAccessLogFlushesAfterInterval(t *testing.T) {
	k, err := kb.Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	prev := readAccessNow
	readAccessNow = func() time.Time { return now }
	t.Cleanup(func() { readAccessNow = prev })
	l := newReadAccessLog(k)
	l.record("ops/a")
	if n, _, _ := newReadAccessLog(k).summary(); n != 0 {
		t.Fatal("a record inside the interval must not write the file")
	}
	now = now.Add(readAccessFlushEvery)
	l.record("ops/b")
	if n, _, _ := newReadAccessLog(k).summary(); n != 2 {
		t.Fatalf("a record past the interval must flush both: %d", n)
	}
}

func TestReadsAreTracked(t *testing.T) {
	s, _ := archivedServer(t)
	if _, ok := kbStatusResult(t, s)["read_access"]; ok {
		t.Fatal("read_access present before any read")
	}
	callOK(t, s, "concept_read", `{"id":"ops/live"}`)
	callOK(t, s, "search", `{"query":"gateway"}`)
	callOK(t, s, "graph_neighbors", `{"id":"ops/live"}`)
	var ra struct {
		Tracked int `json:"tracked_concepts"`
	}
	if err := json.Unmarshal(kbStatusResult(t, s)["read_access"], &ra); err != nil || ra.Tracked != 1 {
		t.Fatalf("read_access = %+v, %v (only ops/live was read or hit)", ra, err)
	}
}

// D346: stale_after 0 is an explicit off, -1 resets to the default, the
// response echoes what was written, and map_list shows the effective value.
func TestMapUpdate_StaleAfterOffAndReset(t *testing.T) {
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
	callOK(t, s, "map_update", `{"map":"ops","stale_after":0}`)
	if c, err := k.ReadMapContract("ops"); err != nil || !c.StaleAfterOff || c.StaleAfterDays != 0 || len(c.Malformed) != 0 {
		t.Fatalf("stale_after 0 must persist as off: %+v %v", c, err)
	}
	if out := callOK(t, s, "map_list", `{}`); strings.Contains(out, `"stale_after"`) {
		t.Fatalf("off must be omitted from map_list: %s", out)
	}
	out = callOK(t, s, "map_update", `{"map":"ops","stale_after":-1}`)
	if !strings.Contains(out, `"stale_after": 30`) || !strings.Contains(out, `"stale_after_defaulted": true`) {
		t.Fatalf("-1 must fall back to the work-map default: %s", out)
	}
	if c, _ := k.ReadMapContract("ops"); c.StaleAfterOff || c.StaleAfterDays != 0 {
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
