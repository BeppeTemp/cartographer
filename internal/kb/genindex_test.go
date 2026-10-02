package kb

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/okf"
)

// TestUpdateMapContract_CostKeys covers the D301 keys: set, read back,
// removed, a bad index value refused on update and reported when hand-written.
func TestUpdateMapContract_CostKeys(t *testing.T) {
	k, _ := Init(tempKB(t))
	if err := k.CreateMap("m", "M", "map", nil, ""); err != nil {
		t.Fatal(err)
	}
	gen, n := IndexGenerated, 5
	c, err := k.UpdateMapContract("m", MapContractUpdate{Index: &gen, RepeatedFactMin: &n, HotspotInDegree: &n, HotspotBytes: &n, OversizeBytes: &n})
	if err != nil {
		t.Fatal(err)
	}
	if c.Index != IndexGenerated || c.RepeatedFactMin != 5 || c.HotspotInDegree != 5 || c.HotspotBytes != 5 || c.OversizeBytes != 5 || len(c.Malformed) != 0 {
		t.Fatalf("after set: %+v", c)
	}
	cur, zero := "curated", 0
	c, err = k.UpdateMapContract("m", MapContractUpdate{Index: &cur, RepeatedFactMin: &zero, HotspotInDegree: &zero, HotspotBytes: &zero, OversizeBytes: &zero})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := k.ReadRaw("m/_map.md")
	if c.Index != "" || c.OversizeBytes != 0 || strings.Contains(raw, "index:") || strings.Contains(raw, "oversize_bytes") {
		t.Fatalf("after remove: %+v\n%s", c, raw)
	}
	bad := "auto"
	if _, err := k.UpdateMapContract("m", MapContractUpdate{Index: &bad}); err == nil {
		t.Fatal("index auto accepted")
	}
	if err := os.WriteFile(filepath.Join(k.DataRoot(), "m", "_map.md"), []byte(strings.Replace(raw, "---\n", "---\nindex: auto\nhotspot_bytes: -1\n", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if c, err = k.ReadMapContract("m"); err != nil || c.Index != "" || len(c.Malformed) != 2 {
		t.Fatalf("malformed keys: %+v %v", c, err)
	}
}

func TestRenderIndexBlock_Map(t *testing.T) {
	es := []ConceptSummary{
		{ID: "m/b", Title: "Beta", Type: "Topic"},
		{ID: "m/a", Title: "Alpha", Type: "Entity"},
		{ID: "m/c", Type: "Entity"},
	}
	got := RenderIndexBlock("map", es)
	want := IndexBlockBegin + "\n### Entity\n- [[m/c]] — m/c\n- [[m/a]] — Alpha\n### Topic\n- [[m/b]] — Beta\n" + IndexBlockEnd + "\n"
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
	// One type: no heading. Same set in another order: same bytes.
	one := RenderIndexBlock("map", []ConceptSummary{es[1], es[2]})
	if strings.Contains(one, "###") || one != RenderIndexBlock("map", []ConceptSummary{es[2], es[1]}) {
		t.Fatalf("single type block:\n%s", one)
	}
}

func TestRenderIndexBlock_Journal(t *testing.T) {
	short := RenderIndexBlock("journal", []ConceptSummary{{ID: "j/2026-01-02-a"}, {ID: "j/2026-03-01-b"}})
	if strings.Index(short, "2026-03-01-b") > strings.Index(short, "2026-01-02-a") || strings.Contains(short, "###") {
		t.Fatalf("short journal not newest-first or grouped:\n%s", short)
	}
	var es []ConceptSummary
	for i := 1; i <= journalMonthGroupMin+1; i++ {
		es = append(es, ConceptSummary{ID: okf.ConceptID(fmt.Sprintf("j/2026-%02d-01-e%d", i%3+1, i))})
	}
	es = append(es, ConceptSummary{ID: "j/notes"})
	long := RenderIndexBlock("journal", es)
	m3, m1, und := strings.Index(long, "### 2026-03"), strings.Index(long, "### 2026-01"), strings.Index(long, "### Undated")
	if m3 < 0 || m1 < m3 || und < m1 || strings.Count(long, "### 2026-03") != 1 {
		t.Fatalf("long journal grouping:\n%s", long)
	}
}

// TestRegenerateIndexes covers the D301 invariants: only generated maps are
// touched; curated text outside the block is byte-identical; an expanded
// concept is one entry and its satellites none; a second run writes nothing;
// a deleted concept leaves the block.
func TestRegenerateIndexes(t *testing.T) {
	k, _ := Init(tempKB(t))
	for _, m := range []string{"gen", "cur"} {
		if err := k.CreateMap(m, m, "map", nil, ""); err != nil {
			t.Fatal(err)
		}
	}
	gen := IndexGenerated
	if _, err := k.UpdateMapContract("gen", MapContractUpdate{Index: &gen}); err != nil {
		t.Fatal(err)
	}
	curated := "# Gen\n\nHand-written intro.\n"
	indexPath := filepath.Join(k.DataRoot(), "gen", "index.md")
	if err := os.WriteFile(indexPath, []byte(curated), 0o644); err != nil {
		t.Fatal(err)
	}
	curBefore, _ := k.ReadRaw("cur/index.md")
	write := func(rel, title string) {
		p := filepath.Join(k.DataRoot(), rel)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte("---\ntype: Note\ntitle: "+title+"\n---\nbody\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("gen/a.md", "A")
	write("gen/x/index.md", "X")
	write("gen/x/sat.md", "Sat")
	write("cur/c.md", "C")

	written, err := k.RegenerateIndexes()
	if err != nil || len(written) != 1 || written[0] != "gen" {
		t.Fatalf("written %v, %v", written, err)
	}
	got, _ := k.ReadRaw("gen/index.md")
	want := curated + "\n" + IndexBlockBegin + "\n- [[gen/a]] — A\n- [[gen/x]] — X\n" + IndexBlockEnd + "\n"
	if got != want {
		t.Fatalf("index:\n%q\nwant\n%q", got, want)
	}
	if after, _ := k.ReadRaw("cur/index.md"); after != curBefore {
		t.Fatal("a curated map was touched")
	}
	if written, _ := k.RegenerateIndexes(); len(written) != 0 {
		t.Fatalf("second run wrote %v", written)
	}
	// Curated text edited around the block survives the next regeneration.
	edited := strings.Replace(got, "Hand-written intro.", "Edited intro.", 1) + "\nTrailer.\n"
	_ = os.WriteFile(indexPath, []byte(edited), 0o644)
	_ = os.Remove(filepath.Join(k.DataRoot(), "gen", "a.md"))
	if _, err := k.RegenerateIndexes(); err != nil {
		t.Fatal(err)
	}
	got, _ = k.ReadRaw("gen/index.md")
	if strings.Contains(got, "gen/a") || !strings.HasPrefix(got, "# Gen\n\nEdited intro.\n") || !strings.HasSuffix(got, IndexBlockEnd+"\n\nTrailer.\n") {
		t.Fatalf("after delete:\n%s", got)
	}
}
