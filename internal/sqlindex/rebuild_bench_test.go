package sqlindex

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// rebuildBody is a concept of about 6 KB with eight "##" sections, the shape
// of a real page, so a cold rebuild exercises the section index (#1329).
func rebuildBody(i int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "---\ntitle: Concept %d\ntags: [a, b]\n---\n# Concept %d\n\nIntro paragraph for concept %d.\n\n", i, i, i)
	for s := 0; s < 8; s++ {
		fmt.Fprintf(&b, "## Section %d\n\n", s)
		for l := 0; l < 6; l++ {
			fmt.Fprintf(&b, "Line %d of section %d in concept %d: the quick brown fox jumps over the lazy dog near host%d.example.com.\n", l, s, i, (i*7+s*3+l)%97)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// BenchmarkColdRebuild upserts 400 concepts into a fresh index and reports the
// size of index.db: the cold-start cost of a KB of that size (#1329).
func BenchmarkColdRebuild(b *testing.B) {
	const n = 400
	bodies := make([]string, n)
	for i := range bodies {
		bodies[i] = rebuildBody(i)
	}
	var size int64
	for b.Loop() {
		path := filepath.Join(b.TempDir(), "index.db")
		ix, err := Open(path)
		if err != nil {
			b.Fatal(err)
		}
		w := ix.Batch()
		for i, body := range bodies {
			if err := w.Upsert(fmt.Sprintf("c/%d", i), "h", body, ""); err != nil {
				b.Fatal(err)
			}
		}
		if err := w.Commit(); err != nil {
			b.Fatal(err)
		}
		if _, err := ix.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
			b.Fatal(err)
		}
		ix.Close()
		st, _ := os.Stat(path)
		size = st.Size()
	}
	b.ReportMetric(float64(size)/1e6, "MB")
}
