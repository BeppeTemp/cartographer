package kb

import (
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/okf"
)

// The ranking of a snapshot is whole-graph (D244): a request that differs only
// in scope or limit must reuse it, a new generation must recompute it.
func TestGraphSnapshot_RankIsMemoizedPerGeneration(t *testing.T) {
	t.Parallel()
	k := snapshotKB(t)
	first, err := k.GraphSnapshot(GraphSnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if n := k.GraphRankComputations(); n != 1 {
		t.Fatalf("first snapshot computed %d rankings, want 1", n)
	}
	// Same visible set, other scope and limit: nothing recomputed, and the
	// whole-graph numbers are the ones the first request reported.
	scoped, err := k.GraphSnapshot(GraphSnapshotOptions{Scope: "infra", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	again, err := k.GraphSnapshot(GraphSnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if n := k.GraphRankComputations(); n != 1 {
		t.Fatalf("repeated snapshots computed %d rankings, want 1", n)
	}
	if !reflect.DeepEqual(first, again) {
		t.Errorf("a memoized snapshot differs from the computed one:\n%+v\n%+v", first, again)
	}
	if !reflect.DeepEqual(scoped.Communities, first.Communities) {
		t.Errorf("a scoped view lost the whole-KB communities")
	}

	// An edit moves the generation: the next snapshot ranks again.
	page := "---\ntype: Note\n---\n[a](a.md)\n"
	if err := os.WriteFile(filepath.Join(k.DataRoot(), "infra", "fresh.md"), []byte(page), 0o644); err != nil {
		t.Fatal(err)
	}
	after, err := k.GraphSnapshot(GraphSnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if n := k.GraphRankComputations(); n != 2 {
		t.Fatalf("a new generation computed %d rankings in all, want 2", n)
	}
	if after.TotalNodes != first.TotalNodes+1 {
		t.Errorf("the edit is not in the snapshot: %d nodes, was %d", after.TotalNodes, first.TotalNodes)
	}
}

// Trap (D226, D244): the key is the visible set, so a caller that sees fewer
// concepts never gets the ranking computed for one that sees more, and the
// reverse.
func TestGraphSnapshot_RankMemoIsKeyedByTheVisibleSet(t *testing.T) {
	t.Parallel()
	k := snapshotKB(t)
	whole, err := k.GraphSnapshot(GraphSnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	narrowOpts := GraphSnapshotOptions{Include: func(id string) bool { return !strings.HasPrefix(id, "notes/") }}
	narrow, err := k.GraphSnapshot(narrowOpts)
	if err != nil {
		t.Fatal(err)
	}
	if n := k.GraphRankComputations(); n != 2 {
		t.Fatalf("two visible sets share a ranking: %d computations, want 2", n)
	}
	if narrow.TotalNodes != whole.TotalNodes-1 {
		t.Fatalf("the narrow snapshot holds %d nodes, whole %d", narrow.TotalNodes, whole.TotalNodes)
	}
	// What the narrow caller gets is what a KB it alone describes would give.
	fresh := snapshotKB(t)
	want, err := fresh.GraphSnapshot(narrowOpts)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(narrow, want) {
		t.Errorf("narrow snapshot after a whole one differs from a cold one:\n%+v\n%+v", narrow, want)
	}
	// Both stay served from the memo, each its own.
	whole2, _ := k.GraphSnapshot(GraphSnapshotOptions{})
	narrow2, _ := k.GraphSnapshot(narrowOpts)
	if n := k.GraphRankComputations(); n != 2 {
		t.Errorf("serving both again computed %d rankings in all, want 2", n)
	}
	if !reflect.DeepEqual(whole, whole2) || !reflect.DeepEqual(narrow, narrow2) {
		t.Errorf("a memoized snapshot changed between calls")
	}
}

// A poller trusts the last validation for maxAge; any strict reader refreshes
// it, so the poller never lags behind a validation someone else paid for.
func TestGraphGenerationWithin_TrustsALastValidation(t *testing.T) {
	t.Parallel()
	k := snapshotKB(t)
	gen, err := k.GraphGenerationWithin(time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	validations := k.GraphValidations()
	if validations == 0 {
		t.Fatal("the first call did not validate")
	}
	for i := 0; i < 5; i++ {
		if g, _ := k.GraphGenerationWithin(time.Hour); g != gen {
			t.Fatalf("generation moved with nothing changed: %d → %d", gen, g)
		}
	}
	if n := k.GraphValidations(); n != validations {
		t.Fatalf("polling inside the window validated %d more times", n-validations)
	}

	// An out-of-band edit is not seen inside the window ...
	page := "---\ntype: Note\n---\nNew.\n"
	if err := os.WriteFile(filepath.Join(k.DataRoot(), "infra", "late.md"), []byte(page), 0o644); err != nil {
		t.Fatal(err)
	}
	if g, _ := k.GraphGenerationWithin(time.Hour); g != gen {
		t.Fatalf("the poll validated inside its window: generation %d → %d", gen, g)
	}
	// ... until a strict reader validates, and then the poller has it at once.
	if _, err := k.GraphGeneration(); err != nil {
		t.Fatal(err)
	}
	if g, _ := k.GraphGenerationWithin(time.Hour); g == gen {
		t.Fatal("the poll did not see the validation a strict reader paid for")
	}
	// A zero window is the strict read.
	if err := os.WriteFile(filepath.Join(k.DataRoot(), "infra", "later.md"), []byte(page), 0o644); err != nil {
		t.Fatal(err)
	}
	before, _ := k.GraphGenerationWithin(time.Hour)
	if g, _ := k.GraphGenerationWithin(0); g == before {
		t.Fatal("a zero window did not validate")
	}
}

// A write this server makes goes through WithGitLock and ends the trust in
// the last validation: the poll that follows sees it inside the window.
func TestGraphGenerationWithin_SeesAServerWriteAtOnce(t *testing.T) {
	t.Parallel()
	k := snapshotKB(t)
	gen, err := k.GraphGenerationWithin(time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	err = k.WithGitLock(func() error {
		page := "---\ntype: Note\n---\nWritten by a handler.\n"
		return os.WriteFile(filepath.Join(k.DataRoot(), "infra", "handled.md"), []byte(page), 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	if g, _ := k.GraphGenerationWithin(time.Hour); g == gen {
		t.Fatal("the poll answered with the generation from before a server write")
	}
}

// The catalog is the walk's facets from the cache: same entries in the same
// order, and a warm call reads no file.
func TestConceptCatalog_MatchesTheWalkAndReadsNothingWarm(t *testing.T) {
	t.Parallel()
	k := snapshotKB(t)
	backdateTree(t, k.DataRoot())
	cat, err := k.ConceptCatalog()
	if err != nil {
		t.Fatal(err)
	}
	var want []CatalogEntry
	err = k.WalkConceptPaths(func(id okf.ConceptID, physical, content string) error {
		f := facetsOf(content)
		want = append(want, CatalogEntry{ID: id, Expanded: physical == string(id)+"/index.md", Title: f.Title, Type: f.Type, Status: f.Status})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cat, want) {
		t.Fatalf("catalog differs from the walk:\n%+v\n%+v", cat, want)
	}
	reads := k.GraphFileReads()
	if _, err := k.ConceptCatalog(); err != nil {
		t.Fatal(err)
	}
	if n := k.GraphFileReads(); n != reads {
		t.Errorf("a warm catalog read %d files", n-reads)
	}
}

// backdateTree makes every file under root old enough for the graph cache to
// trust its signature: a file modified within the racy window (D241) is read
// again at each validation, which is right and not what these tests measure.
func backdateTree(t *testing.T, root string) {
	t.Helper()
	old := time.Now().Add(-time.Hour)
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			err = os.Chtimes(p, old, old)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}
