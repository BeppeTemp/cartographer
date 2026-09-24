package mcpserver

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// fullScanRewriteBacklinks is the pre-D248 rewriteBacklinks, kept as a test
// oracle: one WalkConcepts pass over the whole KB, an index.md probe per
// concept, fed the masked, resolver-aware RewriteLinks.
func fullScanRewriteBacklinks(k *kb.KB, moveMap map[string]string) ([]rewrittenConcept, int, error) {
	var touched []rewrittenConcept
	total := 0
	err := k.WalkConcepts(func(id okf.ConceptID, content string) error {
		fmRaw, body, _ := okf.SplitFrontmatter(content)
		basePath := okf.IDToPath(id)
		if _, err := k.ReadRaw(path.Join(string(id), "index.md")); err == nil {
			basePath = path.Join(string(id), "index.md")
		}
		newBody, count := kb.RewriteLinks(body, basePath, moveMap, k.AssetExists)
		if count == 0 && !strings.Contains(fmRaw, "superseded_by") {
			return nil
		}
		fm, err := okf.ParseFrontmatter(fmRaw)
		if err != nil {
			if count == 0 {
				return nil
			}
			return fmt.Errorf("parse frontmatter %q: %w", id, err)
		}
		if v, ok := fm.Get("superseded_by"); ok {
			if old, ok := v.(string); ok {
				if moved, ok := moveMap[old]; ok {
					fm.Set("superseded_by", moved)
					count++
				}
			}
		}
		if count == 0 {
			return nil
		}
		if _, err := k.WriteConcept(id, fm, newBody, okf.ContentHash(content)); err != nil {
			return fmt.Errorf("write %q: %w", id, err)
		}
		touched = append(touched, rewrittenConcept{ID: string(id), Replacements: count})
		total += count
		return nil
	})
	return touched, total, err
}

// TestRewriteBacklinks_MatchesFullScan: the graph-driven candidate set (D248)
// rewrites the same files, to the same bytes, with the same rewritten list in
// the same order, as a whole-KB pass.
func TestRewriteBacklinks_MatchesFullScan(t *testing.T) {
	const note = "---\ntype: Note\n---\n"
	// The post-move state: notes/x → arch/x (flat), notes/exp → arch/exp
	// (expanded), notes/y → notes/y2 (co-moved with x).
	files := map[string]string{
		"arch/x.md":              note + "co-moved [[notes/y]] and [o](../notes/other.md)\n",
		"arch/exp/index.md":      note + "[y](../../notes/y.md) [self](../../notes/exp.md)\n",
		"notes/y2.md":            note + "[[notes/x#sec|label]] and [[notes/exp]]\n",
		"notes/linker.md":        note + "[x](x.md#frag) [e](exp.md) example `[x](x.md)`\n```\n[[notes/x]]\n```\n",
		"notes/exp2/index.md":    note + "[x](../x.md) [y](../y)\n",
		"notes/sup.md":           "---\ntype: Note\nsuperseded_by: notes/x\n---\nold\n",
		"notes/sup-other.md":     "---\ntype: Note\nsuperseded_by: notes/other\n---\nold\n",
		"notes/code-only.md":     note + "only `[[notes/x]]` in code\n",
		"notes/unrelated.md":     note + "[[notes/other]] [o](other.md)\n",
		"notes/other.md":         note + "other\n",
		"deep/a/c.md":            note + "[x](../../notes/x.md) [[notes/y2]]\n",
		"notes/asset/index.md":   note + "[df](Dockerfile)\n",
		"notes/asset/Dockerfile": "FROM scratch\n",
	}
	moveMap := map[string]string{"notes/x": "arch/x", "notes/exp": "arch/exp", "notes/y": "notes/y2", "notes/asset/Dockerfile": "gone"}

	build := func() *kb.KB {
		k, err := kb.Init(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		for rel, content := range files {
			abs := filepath.Join(k.DataRoot(), filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return k
	}
	oracleKB, graphKB := build(), build()

	wantTouched, wantTotal, err := fullScanRewriteBacklinks(oracleKB, moveMap)
	if err != nil {
		t.Fatal(err)
	}
	gotTouched, gotTotal, err := rewriteBacklinks(graphKB, moveMap)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotTouched, wantTouched) || gotTotal != wantTotal {
		t.Errorf("rewritten = %v (%d), full scan = %v (%d)", gotTouched, gotTotal, wantTouched, wantTotal)
	}
	if len(wantTouched) < 6 {
		t.Fatalf("scripted KB exercises too little: %v", wantTouched)
	}

	snapshot := func(k *kb.KB) map[string]string {
		out := map[string]string{}
		err := filepath.WalkDir(k.DataRoot(), func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			rel, _ := filepath.Rel(k.DataRoot(), p)
			if rel == "index.md" {
				return nil // titled after the temp dir, which differs
			}
			b, err := os.ReadFile(p)
			out[filepath.ToSlash(rel)] = string(b)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	want, got := snapshot(oracleKB), snapshot(graphKB)
	for rel := range want {
		if got[rel] != want[rel] {
			t.Errorf("%s differs:\n got %q\nwant %q", rel, got[rel], want[rel])
		}
	}
	for rel := range got {
		if _, ok := want[rel]; !ok {
			t.Errorf("%s exists only after the graph-driven pass", rel)
		}
	}
	// Code spans stay untouched by design (D150).
	if !strings.Contains(got["notes/code-only.md"], "`[[notes/x]]`") || !strings.Contains(got["notes/linker.md"], "`[x](x.md)`") {
		t.Errorf("a link inside code was rewritten")
	}
}
