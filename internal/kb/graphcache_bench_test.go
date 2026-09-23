package kb_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/lint"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// syntheticKB writes a deterministic KB shaped like the largest real one
// measured for D241 (734 concepts, 12 MB): 1,000 concepts over 8 maps and a
// journal, a mean out-degree of 8 in both link syntaxes, 5% broken links, 3%
// expanded concepts and ~15 KB bodies. Real user KBs never enter the
// repository; this stands in for them.
func syntheticKB(b *testing.B) *kb.KB {
	b.Helper()
	k, err := kb.Init(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	collections := []string{"infra", "apps", "network", "storage", "security", "people", "vendors", "ops", "journal"}
	for i, name := range collections {
		kind := "map"
		if i == len(collections)-1 {
			kind = "journal"
		}
		if err := k.CreateMap(name, strings.ToUpper(name[:1])+name[1:], kind, nil, ""); err != nil {
			b.Fatal(err)
		}
	}
	const n = 1000
	id := func(i int) string { return fmt.Sprintf("%s/c%04d", collections[i%len(collections)], i) }
	state := uint32(1)
	rand := func(m int) int {
		state = state*1664525 + 1013904223
		return int(state>>8) % m
	}
	filler := strings.Repeat("Lorem ipsum dolor sit amet, consectetur adipiscing elit. ", 260)
	old := time.Now().Add(-time.Hour)
	for i := 0; i < n; i++ {
		var body strings.Builder
		fmt.Fprintf(&body, "---\ntype: Entity\ntitle: Concept %d\nstatus: active\n---\n# Concept %d\n\n", i, i)
		for l := 0; l < 8; l++ {
			target := id(rand(n))
			switch {
			case rand(100) < 5:
				fmt.Fprintf(&body, "See [[%s/missing-%d]].\n", collections[rand(len(collections))], rand(1000))
			case l%2 == 0:
				fmt.Fprintf(&body, "See [[%s]].\n", target)
			default:
				rel, _ := filepath.Rel(filepath.Dir(id(i)), target)
				fmt.Fprintf(&body, "See [link](%s.md).\n", filepath.ToSlash(rel))
			}
		}
		body.WriteString(filler)
		rel := id(i) + ".md"
		if i%33 == 0 {
			rel = id(i) + "/index.md"
		}
		p := filepath.Join(k.DataRoot(), filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			b.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body.String()), 0o644); err != nil {
			b.Fatal(err)
		}
		// Out of the racy window, as a KB that was not just written is.
		_ = os.Chtimes(p, old, old)
	}
	return k
}

func BenchmarkGraphNeighborsCold(b *testing.B) {
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		k := syntheticKB(b)
		b.StartTimer()
		if _, err := k.GraphNeighbors("infra/c0000", 1); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkGraphNeighborsWarm(b *testing.B) {
	k := syntheticKB(b)
	if _, err := k.GraphNeighbors("infra/c0000", 1); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := k.GraphNeighbors(okf.ConceptID("infra/c0000"), 1); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkGraphSnapshotWarm(b *testing.B) {
	k := syntheticKB(b)
	if _, err := k.GraphSnapshot(kb.GraphSnapshotOptions{}); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := k.GraphSnapshot(kb.GraphSnapshotOptions{}); err != nil {
			b.Fatal(err)
		}
	}
}

// A 100-concept scope with scope_neighbors: one full walk per concept before
// D241, one validation now.
func BenchmarkLintScopeNeighbors(b *testing.B) {
	k := syntheticKB(b)
	if _, err := k.LinkGraph(); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := lint.Run(k, "infra", true); err != nil {
			b.Fatal(err)
		}
	}
}
