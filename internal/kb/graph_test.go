package kb

import (
	"os"
	"path/filepath"
	"strings"
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

func TestExtractLinks_AssetTargetsAreNotConcepts(t *testing.T) {
	body := `[csv](report.csv) [png](evidence/screen.png) [concept](sibling.md)`
	links := ExtractLinks(body, "map/owner/index.md")
	if len(links) != 1 || links[0] != "map/owner/sibling" {
		t.Fatalf("asset paths became concept links: %v", links)
	}
	assets := ExtractAssetLinks(body, "map/owner/index.md")
	if len(assets) != 2 || assets[0] != "map/owner/report.csv" || assets[1] != "map/owner/evidence/screen.png" {
		t.Fatalf("asset links: %v", assets)
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

// The alias form is now extracted, with the label discarded (D150): a wiki-link
// is the only base-independent form (D149), and choosing it used to cost the
// human-readable label.
func TestExtractLinks_WikiLinkAliasIsExtracted(t *testing.T) {
	cases := map[string]string{
		"[[entities/infra/otbr|OTBR gateway]]":        "entities/infra/otbr",
		"[[entities/infra/otbr#Schema|OTBR gateway]]": "entities/infra/otbr",
		"[[entities/infra/otbr|a|b]]":                 "entities/infra/otbr",
	}
	for body, want := range cases {
		links := ExtractLinks(body, "topics/x.md")
		if len(links) != 1 || string(links[0]) != want {
			t.Errorf("ExtractLinks(%q) = %v, want [%s]", body, links, want)
		}
	}

	// An empty ID is not a link.
	if links := ExtractLinks(`[[|just a label]]`, "topics/x.md"); len(links) != 0 {
		t.Errorf("empty-ID alias should not be extracted, got %v", links)
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

func TestGraphNeighbors_DirectionsAndExpandedPhysicalPath(t *testing.T) {
	dir, err := os.MkdirTemp("", "kb-graph-directions-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	k, err := Init(dir)
	if err != nil {
		t.Fatal(err)
	}
	write := func(rel, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(k.DataRoot(), rel)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(k.DataRoot(), rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("map/a.md", "---\ntype: Note\n---\n[[map/owner]]\n")
	write("map/b.md", "---\ntype: Note\n---\n[a](a.md) and [missing](missing.md)\n")
	write("map/owner/index.md", "---\ntype: Note\n---\n[child](child.md)\n")
	write("map/owner/child.md", "---\ntype: Note\n---\n[leaf](../leaf.md)\n")
	write("map/leaf.md", "---\ntype: Note\n---\n[a](a.md)\n")
	write("map/no-backlinks.md", "---\ntype: Note\n---\n")

	out, err := k.GraphNeighbors("map/owner", 2, "out")
	if err != nil {
		t.Fatal(err)
	}
	if out["map/owner/child"] != 1 || out["map/leaf"] != 2 {
		t.Fatalf("expanded outbound links use wrong physical path: %v", out)
	}

	in, err := k.GraphNeighbors("map/owner", 2, "in")
	if err != nil {
		t.Fatal(err)
	}
	if in["map/a"] != 1 || in["map/b"] != 2 {
		t.Fatalf("inbound depth traversal: %v", in)
	}

	both, err := k.GraphNeighbors("map/a", 3, "both")
	if err != nil {
		t.Fatal(err)
	}
	if both["map/owner"] != 1 || both["map/b"] != 1 || both["map/owner/child"] != 2 || both["map/leaf"] != 1 {
		t.Fatalf("both direction/minimum distance: %v", both)
	}
	// leaf is reachable outbound in three hops and inbound in one, so both
	// must retain the minimum distance.
	if both["map/leaf"] != 1 {
		t.Fatalf("both direction must retain minimum distance: %v", both)
	}

	missing, err := k.GraphNeighbors("map/missing", 1, "in")
	if err != nil {
		t.Fatal(err)
	}
	if missing["map/b"] != 1 {
		t.Fatalf("missing target should retain inbound backlinks: %v", missing)
	}

	noBacklinks, err := k.GraphNeighbors("map/no-backlinks", 1, "in")
	if err != nil {
		t.Fatal(err)
	}
	if len(noBacklinks) != 0 {
		t.Fatalf("concept without backlinks should return empty without error: %v", noBacklinks)
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

// --- D150: what counts as a link ---

// Two constructs that are normal in any KB documenting commands or drawing
// diagrams used to become link targets, and in the field every surviving
// broken_link finding had this root cause.
func TestExtractLinks_SkipsCodeSpans(t *testing.T) {
	cases := []struct {
		name string
		body string
		want int
	}{
		{"mermaid subroutine node", "```mermaid\nflowchart LR\n  N1[[\"a label\"]]\n```\n", 0},
		{"posix character class", "```sh\ngrep -nE \"listen[[:space:]]\"\n```\n", 0},
		{"markdown link inside a fence", "```md\n[label](path.md)\n```\n", 0},
		{"inline code span", "See `[x](y.md)` for the syntax.\n", 0},
		{"tilde fence", "~~~\n[x](y.md)\n~~~\n", 0},
		{"longer fence", "````\n[x](y.md)\n````\n", 0},
		{"unclosed fence masks the rest", "```\n[x](y.md)\n[z](w.md)\n", 0},
		{"link after a closed fence survives", "```\n[x](y.md)\n```\n[real](real.md)\n", 1},
		{"indented block is not code", "    [x](y.md)\n", 1},
		{"wiki-link inside a fence", "```\n[[m/c]]\n```\n", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ExtractLinks(tc.body, "m/a.md"); len(got) != tc.want {
				t.Errorf("ExtractLinks = %v (%d), want %d links", got, len(got), tc.want)
			}
		})
	}
}

// maskCodeSpans must not change offsets or line count: lint's machine_path
// check already reasons about spans over the same body.
func TestMaskCodeSpans_PreservesLengthAndLines(t *testing.T) {
	body := "before\n```sh\ngrep x\n```\nafter `code` end\n"
	masked := maskCodeSpans(body)
	if len(masked) != len(body) {
		t.Errorf("length %d, want %d", len(masked), len(body))
	}
	if strings.Count(masked, "\n") != strings.Count(body, "\n") {
		t.Errorf("line count changed")
	}
	if !strings.Contains(masked, "before") || !strings.Contains(masked, "after") {
		t.Errorf("prose outside code spans was masked: %q", masked)
	}
	if strings.Contains(masked, "grep") || strings.Contains(masked, "code") {
		t.Errorf("code was not masked: %q", masked)
	}
}

// Citing an extensionless asset — a Dockerfile, a Makefile, a LICENSE — used to
// produce a link to a nonexistent concept and leave the asset orphan forever.
func TestExtractLinks_ExtensionlessAssetIsNotAConcept(t *testing.T) {
	dir, err := os.MkdirTemp("", "graph-asset-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	k, err := Init(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(k.DataRoot(), "m", "c"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(k.DataRoot(), "m", "c", "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	body := "- [Dockerfile](Dockerfile)\n- [sibling](sibling)\n"
	got := ExtractLinks(body, "m/c/index.md", k.AssetExists)
	if len(got) != 1 || string(got[0]) != "m/c/sibling" {
		t.Errorf("ExtractLinks = %v, want only the concept shorthand m/c/sibling", got)
	}

	// Without a resolver the shorthand behaviour is unchanged.
	if got := ExtractLinks(body, "m/c/index.md"); len(got) != 2 {
		t.Errorf("without a resolver ExtractLinks = %v, want both treated as concepts", got)
	}
}
