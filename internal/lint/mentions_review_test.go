package lint

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/gitx"
	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/kbtest"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

func note(title, body string) string {
	return "---\ntype: Note\ntitle: " + title + "\n---\n" + body + "\n"
}

// commitAll commits the work tree with the given subject.
func commitAll(t *testing.T, k *kb.KB, subject string) {
	t.Helper()
	if err := gitx.Commit(k.Root, subject, "user", "user@example.com"); err != nil {
		t.Fatal(err)
	}
}

// moveFile renames a concept file (data-relative paths) and commits it.
func moveFile(t *testing.T, k *kb.KB, from, to, subject string) {
	t.Helper()
	dst := filepath.Join(k.DataRoot(), to)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(k.DataRoot(), from), dst); err != nil {
		t.Fatal(err)
	}
	commitAll(t, k, subject)
}

// movedKB has ops/net moved to ops/network by a concept_move commit and a page
// that still names ops/net in prose.
func movedKB(t *testing.T) *kb.KB {
	t.Helper()
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "ops/net.md", note("Net", "The router and switch configuration lives here, in detail, line by line."))
	writeFile(t, k.DataRoot(), "ops/page.md", note("Page", "The router is ops/net."))
	commitAll(t, k, "setup")
	moveFile(t, k, "ops/net.md", "ops/network.md", "concept_move: ops/net")
	return k
}

func movedItems(t *testing.T, k *kb.KB) []ReviewItem {
	t.Helper()
	var out []ReviewItem
	for _, it := range itemsOf(review(t, k), ReviewObservation) {
		if it.Check == "moved_id_cited" {
			out = append(out, it)
		}
	}
	return out
}

func TestMovedIDCited_ItemNamesTheCurrentID(t *testing.T) {
	t.Parallel()
	k := movedKB(t)
	items := movedItems(t, k)
	if len(items) != 1 || len(items[0].Concepts) != 1 || items[0].Concepts[0] != "ops/page" {
		t.Fatalf("items = %+v", items)
	}
	if !strings.Contains(items[0].Evidence, "cites ops/net (line 5) — now ops/network") {
		t.Errorf("evidence = %q", items[0].Evidence)
	}
	if !items[0].wholeGraph {
		t.Error("the item states history: whole-graph")
	}
	if got := FilterReview(items, func(string) bool { return true }, false); len(got) != 0 {
		t.Errorf("a caller without the whole KB got %+v", got)
	}
	if got := FilterReview(items, func(string) bool { return true }, true); len(got) != 1 {
		t.Errorf("a whole-KB caller got %+v", got)
	}
}

func TestMovedIDCited_Chain(t *testing.T) {
	t.Parallel()
	k := movedKB(t)
	moveFile(t, k, "ops/network.md", "ops/fabric.md", "concept_move: ops/network")
	items := movedItems(t, k)
	if len(items) != 1 || !strings.Contains(items[0].Evidence, "cites ops/net (line 5) — now ops/fabric") {
		t.Fatalf("items = %+v", items)
	}
}

func TestMovedIDCited_LinksAreBrokenLinkNotThisItem(t *testing.T) {
	t.Parallel()
	k := movedKB(t)
	writeFile(t, k.DataRoot(), "ops/page.md", note("Page", "See [the net](ops/net.md) and [[ops/net]]."))
	if got := movedItems(t, k); len(got) != 0 {
		t.Fatalf("links reported twice: %+v", got)
	}
	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if !hasCheck(findings, "ops/page.md", "broken_link") && !hasCheck(findings, "ops/page", "broken_link") {
		t.Errorf("broken_link no longer reports the link: %+v", findings)
	}
}

func TestMovedIDCited_WholeTokenOnly(t *testing.T) {
	t.Parallel()
	k := movedKB(t)
	writeFile(t, k.DataRoot(), "ops/page.md", note("Page", "dev/ops/net and ops/netx and ops/net-two."))
	if got := movedItems(t, k); len(got) != 0 {
		t.Fatalf("items = %+v", got)
	}
}

func TestMovedIDCited_Artifacts(t *testing.T) {
	t.Parallel()
	k := movedKB(t)
	writeFile(t, k.Root, "skills/plan/SKILL.md", skillMD("plan", "Plans", "# Plan\nRead ops/net first.\n"))
	writeFile(t, k.Root, "instructions.md", "Start from ops/net.\n")
	n := 0
	for _, it := range movedItems(t, k) {
		if it.Artifact == "skills/plan/SKILL.md" || it.Artifact == "instructions.md" {
			n++
		}
	}
	if n != 2 {
		t.Fatalf("artifact items = %d, want 2: %+v", n, movedItems(t, k))
	}
}

func TestMovedIDCited_NothingToSuggest(t *testing.T) {
	t.Parallel()
	t.Run("moved then deleted", func(t *testing.T) {
		t.Parallel()
		k := movedKB(t)
		if err := os.Remove(filepath.Join(k.DataRoot(), "ops/network.md")); err != nil {
			t.Fatal(err)
		}
		if got := movedItems(t, k); len(got) != 0 {
			t.Fatalf("items = %+v", got)
		}
	})
	t.Run("old ID recreated", func(t *testing.T) {
		t.Parallel()
		k := movedKB(t)
		writeFile(t, k.DataRoot(), "ops/net.md", note("Net", "Back again."))
		if got := movedItems(t, k); len(got) != 0 {
			t.Fatalf("items = %+v", got)
		}
	})
	t.Run("hand git mv and external changes", func(t *testing.T) {
		t.Parallel()
		for _, subject := range []string{"manual rename", "external changes", "concept_expand: ops/net", "concept_moved: ops/net", "concept_movement", "kb_repairs: ops/net"} {
			k := tempKB(t)
			writeFile(t, k.DataRoot(), "ops/net.md", note("Net", "The router and switch configuration lives here, in detail, line by line."))
			writeFile(t, k.DataRoot(), "ops/page.md", note("Page", "The router is ops/net."))
			commitAll(t, k, "setup")
			moveFile(t, k, "ops/net.md", "ops/network.md", subject)
			if got := movedItems(t, k); len(got) != 0 {
				t.Fatalf("%s: items = %+v", subject, got)
			}
		}
	})
	t.Run("no git", func(t *testing.T) {
		t.Parallel()
		k := movedKB(t)
		if err := os.RemoveAll(filepath.Join(k.Root, ".git")); err != nil {
			t.Fatal(err)
		}
		if got := movedItems(t, k); len(got) != 0 {
			t.Fatalf("items = %+v", got)
		}
	})
}

func TestMovedIDCited_ManyOldIDsOneItemSortedAndClosing(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	for _, n := range []string{"c", "a", "b"} {
		writeFile(t, k.DataRoot(), "ops/old-"+n+".md", note("O"+n, "Distinct content for page "+n+" that is long enough to be detected as a rename."))
	}
	writeFile(t, k.DataRoot(), "ops/page.md", note("Page", "Uses ops/old-c, ops/old-a and ops/old-b."))
	commitAll(t, k, "setup")
	for _, n := range []string{"c", "a", "b"} {
		moveFile(t, k, "ops/old-"+n+".md", "ops/new-"+n+".md", "concept_move: ops/old-"+n)
	}
	items := movedItems(t, k)
	if len(items) != 1 {
		t.Fatalf("items = %+v", items)
	}
	want := "cites ops/old-a (line 5) — now ops/new-a; cites ops/old-b (line 5) — now ops/new-b; cites ops/old-c (line 5) — now ops/new-c"
	if !strings.Contains(items[0].Evidence, want) {
		t.Errorf("evidence = %q", items[0].Evidence)
	}
	again := movedItems(t, k)
	if fmt.Sprint(again) != fmt.Sprint(items) {
		t.Error("two runs differ")
	}
	writeFile(t, k.DataRoot(), "ops/page.md", note("Page", "Uses new ones."))
	if got := movedItems(t, k); len(got) != 0 {
		t.Fatalf("item not closed by the edit: %+v", got)
	}
}

func TestMovedIDCited_NoScanWithoutMissingOldID(t *testing.T) {
	t.Parallel()
	k := movedKB(t)
	// The old ID exists again: nothing resolves, the KB is not scanned.
	writeFile(t, k.DataRoot(), "ops/net.md", note("Net", "Back."))
	links, err := k.Links()
	if err != nil {
		t.Fatal(err)
	}
	got, err := movedIDCitations(k, links.Exists, nil)
	if err != nil || len(got) != 0 {
		t.Fatalf("got %+v, %v", got, err)
	}
}

// BenchmarkScanMentions pins the bound of the scan the review runs when old IDs
// are missing: 2,000 pages of about 8 KB against 100 old IDs.
func BenchmarkScanMentions(b *testing.B) {
	k := kbtest.New(b)
	body := strings.Repeat("Lorem ipsum dolor sit amet, consectetur adipiscing elit. ", 140)
	for i := 0; i < 2000; i++ {
		p := filepath.Join(k.DataRoot(), "ops", fmt.Sprintf("p%04d.md", i))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			b.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(note("P", body)), 0o644); err != nil {
			b.Fatal(err)
		}
	}
	moveMap := map[string]string{}
	for i := 0; i < 100; i++ {
		moveMap[fmt.Sprintf("ops/old-%03d", i)] = fmt.Sprintf("ops/new-%03d", i)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = ScanMentions(k, moveMap, nil)
	}
}

// The scan uses the pages Review holds: it reads no concept from disk again.
func TestMovedIDCited_ReadsNoConceptFromDisk(t *testing.T) {
	t.Parallel()
	k := movedKB(t)
	links, err := k.Links()
	if err != nil {
		t.Fatal(err)
	}
	// The disk says the page cites the old ID (movedKB); the pages handed in say it does not.
	got, err := movedIDCitations(k, links.Exists, []MentionFile{{Path: "ops/page.md", Content: note("Page", "Nothing here.")}})
	if err != nil || len(got) != 0 {
		t.Fatalf("a concept was read from disk: %+v, %v", got, err)
	}
	// And the pages handed in are what is reported, whatever the disk holds.
	got, err = movedIDCitations(k, links.Exists, []MentionFile{{Path: "ops/other.md", Content: note("Other", "Cites ops/net.")}})
	if err != nil || len(got) != 1 || got[0].Path != "ops/other" {
		t.Fatalf("got %+v, %v", got, err)
	}
}

// An unreadable history costs the observation, never the review.
func TestMovedIDCited_UnreadableHistoryDegrades(t *testing.T) {
	k := movedKB(t)
	old := recordedMoves
	recordedMoves = func(*kb.KB) ([]kb.Move, error) { return nil, fmt.Errorf("git log: boom") }
	defer func() { recordedMoves = old }()
	if got := movedItems(t, k); len(got) != 0 {
		t.Fatalf("items = %+v", got)
	}
	if _, err := Review(k, nil); err != nil {
		t.Fatalf("the review failed: %v", err)
	}
}

// An artifact scan that did not finish costs the observation for that run
// (#1853): a partial list is not shown as a KB with no citation.
func TestMovedIDCited_IncompleteArtifactScanSkips(t *testing.T) {
	k := movedKB(t)
	if got := movedItems(t, k); len(got) != 1 {
		t.Fatalf("control: items = %+v", got)
	}
	old := artifactFiles
	artifactFiles = func(*kb.KB) ([]ArtifactFile, error) { return nil, fmt.Errorf("walk: boom") }
	defer func() { artifactFiles = old }()
	if got := movedItems(t, k); len(got) != 0 {
		t.Fatalf("items = %+v", got)
	}
	if _, err := Review(k, nil); err != nil {
		t.Fatalf("the review failed: %v", err)
	}
}

// ScanMentions returns the mentions it found and the error of a walk that
// failed, from either side (#1853).
func TestScanMentions_WalkErrorReachesTheCaller(t *testing.T) {
	t.Parallel()
	moveMap := map[string]string{"ops/net": "ops/network"}
	okWalk := func(fn func(okf.ConceptID, string, string) error) error {
		return fn("ops/page", "ops/page.md", note("Page", "The router is ops/net."))
	}
	noArtifacts := func() ([]ArtifactFile, error) { return nil, nil }
	got, err := scanMentions(newMentionScanner(moveMap), okWalk, noArtifacts)
	if err != nil || len(got) != 1 {
		t.Fatalf("control: %+v, %v", got, err)
	}
	failWalk := func(fn func(okf.ConceptID, string, string) error) error {
		if err := okWalk(fn); err != nil {
			return err
		}
		return fmt.Errorf("walk: boom")
	}
	got, err = scanMentions(newMentionScanner(moveMap), failWalk, noArtifacts)
	if err == nil || !strings.Contains(err.Error(), "walk: boom") || len(got) != 1 {
		t.Fatalf("concept walk: %+v, %v", got, err)
	}
	failArt := func() ([]ArtifactFile, error) {
		return []ArtifactFile{{Path: "instructions.md", Content: "see ops/net"}}, fmt.Errorf("artifacts: boom")
	}
	got, err = scanMentions(newMentionScanner(moveMap), okWalk, failArt)
	if err == nil || !strings.Contains(err.Error(), "artifacts: boom") || len(got) != 2 {
		t.Fatalf("artifact walk: %+v, %v", got, err)
	}
}
