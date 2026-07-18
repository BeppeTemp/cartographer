package kb

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/okf"
)

func TestExtractLinks(t *testing.T) {
	body := `# Overview

See [runbook](runbook.md) for details.
Also check [cert rotation](../certs/rotation.md) and [external](https://example.com).
Reference to [section only](#section) and [anchor link](other.md#heading).
`
	links := ExtractLinks(body, "arch/overview.md")

	want := map[string]bool{
		"arch/runbook":   true,
		"certs/rotation": true,
		"arch/other":     true,
	}

	got := map[string]bool{}
	for _, l := range links {
		got[string(l)] = true
	}

	for w := range want {
		if !got[w] {
			t.Errorf("missing expected link %q, got %v", w, got)
		}
	}
	for g := range got {
		if !want[g] {
			t.Errorf("unexpected link %q", g)
		}
	}
}

func TestExtractLinks_NoExtension(t *testing.T) {
	body := `Link to [concept](sibling) without .md extension.`
	links := ExtractLinks(body, "arch/source.md")

	if len(links) != 1 {
		t.Fatalf("expected 1 link, got %d: %v", len(links), links)
	}
	if string(links[0]) != "arch/sibling" {
		t.Errorf("got %q, want arch/sibling", links[0])
	}
}

func TestExtractLinks_SkipAbsolute(t *testing.T) {
	body := `[abs](https://example.com) and [mail](mailto:a@b.com)`
	links := ExtractLinks(body, "a.md")
	if len(links) != 0 {
		t.Errorf("expected 0 links, got %v", links)
	}
}

func TestExtractLinks_WikiLinkSimple(t *testing.T) {
	body := `See [[entities/smart-home/otbr]] for details.`
	links := ExtractLinks(body, "topics/infra/overview.md")

	if len(links) != 1 || string(links[0]) != "entities/smart-home/otbr" {
		t.Fatalf("got %v, want [entities/smart-home/otbr]", links)
	}
}

func TestExtractLinks_WikiLinkWithAnchor(t *testing.T) {
	body := `See [[entities/smart-home/otbr#Firmware]] for details.`
	links := ExtractLinks(body, "topics/infra/overview.md")

	if len(links) != 1 || string(links[0]) != "entities/smart-home/otbr" {
		t.Fatalf("got %v, want [entities/smart-home/otbr]", links)
	}
}

func TestExtractLinks_WikiLinkRootRelativeFromNested(t *testing.T) {
	// basePath is a deeply nested concept; the wiki-link target must stay
	// root-relative (unaffected by basePath's directory), unlike markdown links.
	body := `[[entities/infra/dossier/other]]`
	links := ExtractLinks(body, "topics/smart-home-protocols/dossier/deep/concept.md")

	if len(links) != 1 || string(links[0]) != "entities/infra/dossier/other" {
		t.Fatalf("got %v, want [entities/infra/dossier/other]", links)
	}
}

func TestExtractLinks_DedupMarkdownAndWiki(t *testing.T) {
	body := `See [runbook](../arch/runbook.md) and also [[arch/runbook]].`
	links := ExtractLinks(body, "topics/x.md")

	if len(links) != 1 || string(links[0]) != "arch/runbook" {
		t.Fatalf("expected dedup to a single 'arch/runbook', got %v", links)
	}
}

func TestExtractLinks_WikiLinkAliasNotExtracted(t *testing.T) {
	body := `[[entities/infra/otbr|OTBR gateway]]`
	links := ExtractLinks(body, "topics/x.md")

	if len(links) != 0 {
		t.Errorf("alias form should not be extracted, got %v", links)
	}
}

func TestExtractLinks_MarkdownLinksUnaffectedByWikiSupport(t *testing.T) {
	body := `See [runbook](runbook.md) and [[entities/infra/otbr]].`
	links := ExtractLinks(body, "arch/overview.md")

	want := map[string]bool{"arch/runbook": true, "entities/infra/otbr": true}
	got := map[string]bool{}
	for _, l := range links {
		got[string(l)] = true
	}
	for w := range want {
		if !got[w] {
			t.Errorf("missing expected link %q, got %v", w, got)
		}
	}
	for g := range got {
		if !want[g] {
			t.Errorf("unexpected link %q", g)
		}
	}
}

func TestGraphNeighbors(t *testing.T) {
	dir, err := os.MkdirTemp("", "kb-graph-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	k, err := Init(dir)
	if err != nil {
		t.Fatal(err)
	}

	os.MkdirAll(filepath.Join(k.DataRoot(), "arch"), 0o755)
	write := func(rel, content string) {
		os.WriteFile(filepath.Join(k.DataRoot(), rel), []byte(content), 0o644)
	}

	write("arch/a.md", "---\ntype: Note\n---\nLinks to [b](b.md) and [c](c.md).\n")
	write("arch/b.md", "---\ntype: Note\n---\nLinks to [c](c.md).\n")
	write("arch/c.md", "---\ntype: Note\n---\nNo outgoing links.\n")

	neighbors, err := k.GraphNeighbors(okf.ConceptID("arch/a"), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(neighbors) != 2 {
		t.Fatalf("depth 1: expected 2 neighbors, got %d: %v", len(neighbors), neighbors)
	}
	if neighbors["arch/b"] != 1 || neighbors["arch/c"] != 1 {
		t.Errorf("depth 1: unexpected distances: %v", neighbors)
	}

	neighbors2, err := k.GraphNeighbors(okf.ConceptID("arch/a"), 2)
	if err != nil {
		t.Fatal(err)
	}
	if neighbors2["arch/b"] != 1 {
		t.Errorf("depth 2: arch/b distance should be 1, got %d", neighbors2["arch/b"])
	}
	if neighbors2["arch/c"] != 1 {
		t.Errorf("depth 2: arch/c distance should be 1 (found at depth 1), got %d", neighbors2["arch/c"])
	}
}

func TestWalkConcepts(t *testing.T) {
	dir, err := os.MkdirTemp("", "kb-walk-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	k, err := Init(dir)
	if err != nil {
		t.Fatal(err)
	}

	os.MkdirAll(filepath.Join(k.DataRoot(), "arch"), 0o755)
	os.WriteFile(filepath.Join(k.DataRoot(), "arch", "concept.md"), []byte("---\ntype: Note\n---\nbody\n"), 0o644)
	os.WriteFile(filepath.Join(k.DataRoot(), "arch", "_archive.md"), []byte("---\ntype: Archive\n---\n"), 0o644)
	// raw/ is a sibling of data/ and must be excluded from the walk.
	os.MkdirAll(filepath.Join(dir, "raw"), 0o755)
	os.WriteFile(filepath.Join(dir, "raw", "source.md"), []byte("raw data"), 0o644)
	// services/ concepts are included in the walk.
	os.MkdirAll(filepath.Join(dir, "services"), 0o755)
	os.WriteFile(filepath.Join(dir, "services", "keycloak.md"), []byte("---\ntype: Service\n---\nsvc\n"), 0o644)

	var ids []string
	err = k.WalkConcepts(func(id okf.ConceptID, content string) error {
		ids = append(ids, string(id))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(ids) != 2 {
		t.Fatalf("expected 2 concepts, got %d: %v", len(ids), ids)
	}
	found := map[string]bool{}
	for _, id := range ids {
		found[id] = true
	}
	if !found["arch/concept"] {
		t.Errorf("expected 'arch/concept' in %v", ids)
	}
	if !found["services/keycloak"] {
		t.Errorf("expected 'services/keycloak' in %v", ids)
	}
}
