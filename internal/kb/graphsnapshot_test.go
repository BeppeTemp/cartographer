package kb

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/okf"
)

// snapshotKB builds the fixture every snapshot test reads: two collections, an
// expanded concept, a self-link, a link to a missing target and an orphan.
func snapshotKB(t *testing.T) *KB {
	t.Helper()
	dir := t.TempDir()
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
	write("infra/a.md", "---\ntype: Note\n---\n[b](b.md), [[infra/owner]] and [gone](missing.md)\n")
	write("infra/b.md", "---\ntype: Note\ntitle: Bravo\nstatus: active\n---\n[self](b.md) and [a](a.md)\n")
	write("infra/owner/index.md", "---\ntype: Note\n---\n[child](child.md)\n")
	write("infra/owner/child.md", "---\ntype: Note\n---\n[cross](../../notes/n.md)\n")
	write("infra/orphan.md", "---\ntype: Note\n---\nNothing here.\n")
	write("notes/n.md", "---\ntype: Note\n---\n[a](../infra/a.md)\n")
	return k
}

func nodeIDs(snap GraphSnapshot) []string {
	out := make([]string, 0, len(snap.Nodes))
	for _, n := range snap.Nodes {
		out = append(out, string(n.ID))
	}
	return out
}

func edgePairs(snap GraphSnapshot) []string {
	out := make([]string, 0, len(snap.Edges))
	for _, e := range snap.Edges {
		out = append(out, string(e.Source)+"->"+string(e.Target))
	}
	return out
}

func findNode(t *testing.T, snap GraphSnapshot, id string) GraphNode {
	t.Helper()
	for _, n := range snap.Nodes {
		if string(n.ID) == id {
			return n
		}
	}
	t.Fatalf("node %q missing from snapshot: %v", id, nodeIDs(snap))
	return GraphNode{}
}

func TestGraphSnapshot_NodesEdgesAndClassification(t *testing.T) {
	k := snapshotKB(t)
	snap, err := k.GraphSnapshot(GraphSnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}

	want := []string{"infra/a", "infra/b", "infra/orphan", "infra/owner", "infra/owner/child", "notes/n"}
	if got := nodeIDs(snap); !reflect.DeepEqual(got, want) {
		t.Fatalf("nodes: got %v, want %v", got, want)
	}
	wantEdges := []string{
		"infra/a->infra/b",
		"infra/a->infra/owner",
		"infra/b->infra/a",
		"infra/owner->infra/owner/child",
		"infra/owner/child->notes/n",
		"notes/n->infra/a",
	}
	if got := edgePairs(snap); !reflect.DeepEqual(got, wantEdges) {
		t.Fatalf("edges: got %v, want %v", got, wantEdges)
	}

	// A self-link is a node flag, never an edge.
	if !findNode(t, snap, "infra/b").SelfLink {
		t.Error("infra/b links to itself: SelfLink should be set")
	}
	for _, e := range snap.Edges {
		if e.Source == e.Target {
			t.Errorf("self-edge leaked into the edge list: %v", e)
		}
	}

	// An expanded concept is flagged and its outbound links resolve from its
	// physical path, not from IDToPath.
	if !findNode(t, snap, "infra/owner").Expanded {
		t.Error("infra/owner lives at owner/index.md: Expanded should be set")
	}
	if findNode(t, snap, "infra/a").Expanded {
		t.Error("infra/a is a flat concept: Expanded should not be set")
	}

	// A missing target is a broken target, never a node.
	wantBroken := []BrokenTarget{{Source: "infra/a", Target: "infra/missing"}}
	if !reflect.DeepEqual(snap.Broken, wantBroken) {
		t.Fatalf("broken: got %v, want %v", snap.Broken, wantBroken)
	}
	for _, n := range snap.Nodes {
		if n.ID == "infra/missing" {
			t.Error("a missing target was promoted to a node")
		}
	}

	// The facets a client filters on travel with the node.
	if a := findNode(t, snap, "infra/a"); a.Type != "Note" {
		t.Errorf("node type: got %q, want Note", a.Type)
	}
	if b := findNode(t, snap, "infra/b"); b.Status != "active" {
		t.Errorf("node status: got %q, want active", b.Status)
	}
	// The title names the node; a concept without one leaves it empty.
	if b := findNode(t, snap, "infra/b"); b.Title != "Bravo" {
		t.Errorf("node title: got %q, want Bravo", b.Title)
	}
	if a := findNode(t, snap, "infra/a"); a.Title != "" {
		t.Errorf("untitled node: got title %q, want empty", a.Title)
	}

	// Degrees come from the same traversal.
	a := findNode(t, snap, "infra/a")
	if a.OutDegree != 2 || a.InDegree != 2 {
		t.Errorf("infra/a degrees: got out=%d in=%d, want out=2 in=2", a.OutDegree, a.InDegree)
	}
	if orphan := findNode(t, snap, "infra/orphan"); orphan.InDegree != 0 || orphan.OutDegree != 0 {
		t.Errorf("orphan degrees: got out=%d in=%d, want 0/0", orphan.OutDegree, orphan.InDegree)
	}
}

func TestGraphSnapshot_NoDanglingEdgeEndpoint(t *testing.T) {
	k := snapshotKB(t)
	for _, opts := range []GraphSnapshotOptions{
		{},
		{Scope: "infra"},
		{Limit: 2},
		{Scope: "infra", Limit: 1},
		{Include: func(id string) bool { return id != "infra/a" }},
	} {
		snap, err := k.GraphSnapshot(opts)
		if err != nil {
			t.Fatal(err)
		}
		present := map[okf.ConceptID]bool{}
		for _, n := range snap.Nodes {
			present[n.ID] = true
		}
		for _, e := range snap.Edges {
			if !present[e.Source] || !present[e.Target] {
				t.Fatalf("opts %+v: edge %v has an endpoint absent from the node list %v", opts, e, nodeIDs(snap))
			}
		}
		for _, b := range snap.Broken {
			if !present[b.Source] {
				t.Fatalf("opts %+v: broken target %v has a source absent from the node list", opts, b)
			}
		}
	}
}

func TestGraphSnapshot_ScopeDropsOutOfScopeEdgesButKeepsDegrees(t *testing.T) {
	k := snapshotKB(t)
	snap, err := k.GraphSnapshot(GraphSnapshotOptions{Scope: "infra"})
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range snap.Nodes {
		if n.Collection != "infra" {
			t.Fatalf("scope infra returned a node from %q: %s", n.Collection, n.ID)
		}
	}
	for _, e := range edgePairs(snap) {
		if e == "infra/owner/child->notes/n" || e == "notes/n->infra/a" {
			t.Fatalf("edge crossing the scope boundary was kept: %s", e)
		}
	}
	// The counters still describe the whole KB, so a client can say how much
	// of a node's context the scope is hiding.
	if child := findNode(t, snap, "infra/owner/child"); child.OutDegree != 1 {
		t.Errorf("out-of-scope link must still count: got OutDegree=%d, want 1", child.OutDegree)
	}
	if a := findNode(t, snap, "infra/a"); a.InDegree != 2 {
		t.Errorf("inbound degree must stay whole-KB: got %d, want 2", a.InDegree)
	}
}

func TestGraphSnapshot_InvisibleConceptIsAbsentEverywhere(t *testing.T) {
	k := snapshotKB(t)
	snap, err := k.GraphSnapshot(GraphSnapshotOptions{
		Include: func(id string) bool { return id != "notes/n" },
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range snap.Nodes {
		if n.ID == "notes/n" {
			t.Fatal("a hidden concept was returned as a node")
		}
	}
	for _, e := range snap.Edges {
		if e.Source == "notes/n" || e.Target == "notes/n" {
			t.Fatalf("a hidden concept was returned as an edge endpoint: %v", e)
		}
	}
	// It exists, so it must not resurface as a broken target either: that
	// would disclose its id to a caller that may not see it.
	for _, b := range snap.Broken {
		if b.Target == "notes/n" {
			t.Fatal("a hidden but existing concept was reported as a broken target")
		}
	}
	if child := findNode(t, snap, "infra/owner/child"); child.OutDegree != 0 {
		t.Errorf("a hidden target must not be counted: got OutDegree=%d, want 0", child.OutDegree)
	}
	if snap.TotalNodes != 5 {
		t.Errorf("TotalNodes must exclude the hidden concept: got %d, want 5", snap.TotalNodes)
	}

	// PageRank and communities are those of the KB without it (D244): the
	// numbers must not disclose a concept the caller cannot see.
	stripped := snapshotKB(t)
	if err := os.Remove(filepath.Join(stripped.DataRoot(), "notes", "n.md")); err != nil {
		t.Fatal(err)
	}
	want, err := stripped.GraphSnapshot(GraphSnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(snap.Nodes, want.Nodes) || !reflect.DeepEqual(snap.Communities, want.Communities) {
		t.Errorf("narrowed snapshot differs from the KB without the hidden concept:\n%+v\n%+v", snap, want)
	}
}

// A scoped snapshot keeps whole-KB communities (D244): the colours of a node do
// not change when the view narrows to its collection.
func TestGraphSnapshot_ScopeKeepsCommunities(t *testing.T) {
	k := snapshotKB(t)
	all, err := k.GraphSnapshot(GraphSnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	scoped, err := k.GraphSnapshot(GraphSnapshotOptions{Scope: "infra"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(all.Communities, scoped.Communities) {
		t.Fatalf("communities differ: %+v vs %+v", all.Communities, scoped.Communities)
	}
	for _, n := range scoped.Nodes {
		if full := findNode(t, all, string(n.ID)); full.Community != n.Community || full.PageRank != n.PageRank {
			t.Errorf("%s: scoped %d/%v, unscoped %d/%v", n.ID, n.Community, n.PageRank, full.Community, full.PageRank)
		}
	}
	var sum float64
	for _, n := range all.Nodes {
		sum += n.PageRank
	}
	if math.Abs(sum-1) > 1e-5 {
		t.Errorf("pagerank sums to %v", sum)
	}
}

func TestGraphSnapshot_DeterministicAndTruncated(t *testing.T) {
	k := snapshotKB(t)
	first, err := k.GraphSnapshot(GraphSnapshotOptions{Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	second, err := k.GraphSnapshot(GraphSnapshotOptions{Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("two identical requests returned different snapshots")
	}
	// The limit keeps the most important nodes (D244), not the first ids:
	// notes/n sorts last but sits on the a → owner → child → n → a cycle that
	// holds most of the rank, while infra/b and the orphan do not; the kept
	// nodes are still returned sorted by id.
	if got := nodeIDs(first); !reflect.DeepEqual(got, []string{"infra/a", "infra/owner/child", "notes/n"}) {
		t.Fatalf("truncation does not keep the most important nodes: %v", got)
	}
	full, err := k.GraphSnapshot(GraphSnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	kept := map[okf.ConceptID]bool{}
	for _, n := range first.Nodes {
		kept[n.ID] = true
		if full := findNode(t, full, string(n.ID)); full.PageRank != n.PageRank || full.Community != n.Community {
			t.Errorf("%s: truncation changed its pagerank or community", n.ID)
		}
	}
	for _, dropped := range full.Nodes {
		for _, n := range first.Nodes {
			if !kept[dropped.ID] && dropped.PageRank > n.PageRank {
				t.Errorf("dropped %s (%v) ranks above kept %s (%v)", dropped.ID, dropped.PageRank, n.ID, n.PageRank)
			}
		}
	}
	if !first.Truncated {
		t.Error("Truncated should be set when the node set exceeds the limit")
	}
	if first.TotalNodes != 6 {
		t.Errorf("TotalNodes must be the untruncated count: got %d, want 6", first.TotalNodes)
	}
	if first.TotalEdges != 6 {
		t.Errorf("TotalEdges must be the untruncated count: got %d, want 6", first.TotalEdges)
	}
	if got := edgePairs(first); !reflect.DeepEqual(got, []string{"infra/owner/child->notes/n", "notes/n->infra/a"}) {
		t.Errorf("truncated snapshot must return the induced subgraph of the kept nodes: %v", got)
	}
}

func TestGraphSnapshot_LimitDefaultsAndClamps(t *testing.T) {
	k := snapshotKB(t)
	for _, tc := range []struct{ in, want int }{
		{0, DefaultGraphNodeLimit},
		{-1, DefaultGraphNodeLimit},
		{10, 10},
		{MaxGraphNodeLimit + 1, MaxGraphNodeLimit},
		{1 << 20, MaxGraphNodeLimit},
	} {
		snap, err := k.GraphSnapshot(GraphSnapshotOptions{Limit: tc.in})
		if err != nil {
			t.Fatal(err)
		}
		if snap.Limit != tc.want {
			t.Errorf("limit %d: effective limit %d, want %d", tc.in, snap.Limit, tc.want)
		}
		if snap.Truncated {
			t.Errorf("limit %d: a 6-node KB must not report truncation", tc.in)
		}
	}
}

func TestGraphSnapshot_RootConceptHasNoCollection(t *testing.T) {
	dir := t.TempDir()
	k, err := Init(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(k.DataRoot(), "loose.md"), []byte("---\ntype: Note\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	snap, err := k.GraphSnapshot(GraphSnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := findNode(t, snap, "loose").Collection; got != "" {
		t.Errorf("a KB-root concept has no collection, got %q", got)
	}
}

func TestGraphSnapshot_DuplicateLinksCollapseIntoOneEdge(t *testing.T) {
	dir := t.TempDir()
	k, err := Init(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(k.DataRoot(), "m"), 0o755); err != nil {
		t.Fatal(err)
	}
	for rel, content := range map[string]string{
		"m/a.md": "---\ntype: Note\n---\n[b](b.md) again [b](b.md) and [[m/b]]\n",
		"m/b.md": "---\ntype: Note\n---\nleaf\n",
	} {
		if err := os.WriteFile(filepath.Join(k.DataRoot(), rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	snap, err := k.GraphSnapshot(GraphSnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := edgePairs(snap); !reflect.DeepEqual(got, []string{"m/a->m/b"}) {
		t.Fatalf("three links to the same target must collapse into one edge: %v", got)
	}
	if a := findNode(t, snap, "m/a"); a.OutDegree != 1 {
		t.Errorf("degree counts edges, not link occurrences: got %d, want 1", a.OutDegree)
	}
}

func TestGraphSnapshot_EdgeCapTruncatesWithoutDangling(t *testing.T) {
	dir := t.TempDir()
	k, err := Init(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(k.DataRoot(), "m"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A hub linking to everything, so the edge count is predictable.
	const n = 40
	body := ""
	for i := 0; i < n; i++ {
		body += fmt.Sprintf("[t](t%03d.md)\n", i)
		if err := os.WriteFile(filepath.Join(k.DataRoot(), fmt.Sprintf("m/t%03d.md", i)), []byte("---\ntype: Note\n---\nleaf\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(k.DataRoot(), "m/hub.md"), []byte("---\ntype: Note\n---\n"+body), 0o644); err != nil {
		t.Fatal(err)
	}
	snap, err := k.GraphSnapshot(GraphSnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if snap.TotalEdges != n || len(snap.Edges) != n {
		t.Fatalf("expected %d edges, got %d (total %d)", n, len(snap.Edges), snap.TotalEdges)
	}
	if hub := findNode(t, snap, "m/hub"); hub.OutDegree != n {
		t.Errorf("hub OutDegree: got %d, want %d", hub.OutDegree, n)
	}
}
