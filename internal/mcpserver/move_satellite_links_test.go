package mcpserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/lint"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// satPage is a concept file with the minimum frontmatter a write accepts.
func satPage(body string) string {
	return "---\ntype: Note\ntitle: Page\n---\n" + body
}

// satWriteFiles lays files out under the KB, each in the namespace its path
// belongs to (services/ lives at the KB root, everything else in data/).
func satWriteFiles(t *testing.T, k *kb.KB, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		abs, err := k.ResolvePath(rel, true)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func satMapFiles(names ...string) map[string]string {
	files := map[string]string{}
	for _, n := range names {
		files[n+"/_map.md"] = "---\ntype: Map\nkind: map\ntitle: " + n + "\n---\n# " + n + "\n"
		files[n+"/index.md"] = "---\ntype: Index\ntitle: " + n + "\n---\n# " + n + "\n"
	}
	return files
}

func satBody(t *testing.T, k *kb.KB, id string) string {
	t.Helper()
	d, err := k.ReadConcept(okf.ConceptID(id))
	if err != nil {
		t.Fatalf("read %s: %v", id, err)
	}
	return d.Body
}

func satBrokenLinks(t *testing.T, k *kb.KB) []string {
	t.Helper()
	findings, err := lint.Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, f := range findings {
		if f.Check == "broken_link" {
			out = append(out, f.Path+": "+f.Message)
		}
	}
	return out
}

func satMove(t *testing.T, s *Server, args string) {
	t.Helper()
	if res := callTool(t, s, "concept_move", args); res.IsError {
		t.Fatalf("concept_move: %+v", res.Content)
	}
}

func satWantBody(t *testing.T, k *kb.KB, id, want string) {
	t.Helper()
	if got := satBody(t, k, id); got != want {
		t.Errorf("%s body:\n got: %q\nwant: %q", id, got, want)
	}
}

// An expanded concept's ID has two segments, so a move never changes its depth:
// what changes is the directory it sits in, and a satellite's "../x.md" points
// at the old parent's sibling. Every page of the concept must be rebased, not
// just index.md, whichever direction the move goes.
func TestConceptMove_RebasesEveryPageOfAnExpandedConcept(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, from, to string }{
		{"notes-to-area", "notes", "area"},
		{"area-to-notes", "area", "notes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			k := setupTestKB(t)
			s := New("test")
			RegisterKBTools(s, k, Deps{})
			from, to := tc.from, tc.to
			files := satMapFiles(from, to)
			files[from+"/other.md"] = satPage("other\n")
			files[to+"/peer.md"] = satPage("peer\n")
			files[from+"/topic/index.md"] = satPage("[detail](detail.md) [other](../other.md) [peer](../../" + to + "/peer.md) ![fig](img/fig.png)\n")
			files[from+"/topic/detail.md"] = satPage("[index](index.md) [other](../other.md) [peer](../../" + to + "/peer.md) ![fig](img/fig.png) [dir](../topic/more.md)\n")
			files[from+"/topic/more.md"] = satPage("[detail](detail.md) [up](index.md#top) [other](../other.md#x)\n")
			files[from+"/topic/img/fig.png"] = "png"
			satWriteFiles(t, k, files)
			if broken := satBrokenLinks(t, k); len(broken) != 0 {
				t.Fatalf("fixture is not clean before the move: %v", broken)
			}

			satMove(t, s, `{"source_id":"`+from+`/topic","target_id":"`+to+`/topic"}`)
			if _, err := k.ReadConcept(okf.ConceptID(to + "/topic/detail")); err != nil {
				t.Fatalf("satellite did not move: %v", err)
			}

			// Out-of-concept links are rebased; links between the pages of the
			// concept resolve to the same pages as before, assets included.
			satWantBody(t, k, to+"/topic",
				"[detail](detail.md) [other](../../"+from+"/other.md) [peer](../peer.md) ![fig](img/fig.png)\n")
			satWantBody(t, k, to+"/topic/detail",
				"[index](index.md) [other](../../"+from+"/other.md) [peer](../peer.md) ![fig](img/fig.png) [dir](more.md)\n")
			satWantBody(t, k, to+"/topic/more",
				"[detail](detail.md) [up](index.md#top) [other](../../"+from+"/other.md#x)\n")
			if broken := satBrokenLinks(t, k); len(broken) != 0 {
				t.Errorf("broken_link after the move: %v", broken)
			}
		})
	}
}

// The depth of an expanded concept cannot change: a move to a three-segment ID
// is refused before anything is applied, so only the directory ever needs the
// rebase above.
func TestConceptMove_ExpandedConceptKeepsItsDepth(t *testing.T) {
	t.Parallel()
	k := setupTestKB(t)
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	files := satMapFiles("notes")
	files["notes/topic/index.md"] = satPage("topic\n")
	files["notes/topic/detail.md"] = satPage("[other](../other.md)\n")
	files["notes/other.md"] = satPage("other\n")
	satWriteFiles(t, k, files)

	res := callTool(t, s, "concept_move", `{"source_id":"notes/topic","target_id":"notes/area/topic"}`)
	if !res.IsError || !strings.Contains(res.Content[0].Text, "two-segment") {
		t.Fatalf("a depth change must be refused: %+v", res)
	}
	satWantBody(t, k, "notes/topic/detail", "[other](../other.md)\n")
}

// A rename inside the same map keeps the directory but changes the name a link
// such as "../topic/x.md" spells: the page still has to find its own sibling.
func TestConceptMove_RenameKeepsLinksBetweenThePagesOfTheConcept(t *testing.T) {
	t.Parallel()
	k := setupTestKB(t)
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	files := satMapFiles("notes")
	files["notes/other.md"] = satPage("other\n")
	files["notes/topic/index.md"] = satPage("[detail](detail.md)\n")
	files["notes/topic/detail.md"] = satPage("[index](index.md) [self](../topic/index.md) [other](../other.md)\n")
	satWriteFiles(t, k, files)

	satMove(t, s, `{"source_id":"notes/topic","target_id":"notes/renamed"}`)

	satWantBody(t, k, "notes/renamed/detail", "[index](index.md) [self](index.md) [other](../other.md)\n")
	if broken := satBrokenLinks(t, k); len(broken) != 0 {
		t.Errorf("broken_link after the rename: %v", broken)
	}
}

// A satellite can sit in a nested directory (legacy data/ pages deeper than the
// write cap stay readable, and services/ has no cap at all): its links are
// rebased too, against its own directory. The destination here is services/,
// the only place a page that deep can be rewritten.
func TestConceptMove_RebasesNestedSatellites(t *testing.T) {
	t.Parallel()
	k := setupTestKB(t)
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	files := satMapFiles("apps")
	files["services/ref.md"] = satPage("ref\n")
	files["apps/peer.md"] = satPage("peer\n")
	files["apps/owner/index.md"] = satPage("[sat](sat.md) [peer](../peer.md)\n")
	files["apps/owner/sat.md"] = satPage("[peer](../peer.md) [leaf](sub/leaf.md)\n")
	files["apps/owner/sub/leaf.md"] = satPage("[peer](../../peer.md) [sat](../sat.md) [index](../index.md) [ref](../../../services/ref.md)\n")
	satWriteFiles(t, k, files)
	if broken := satBrokenLinks(t, k); len(broken) != 0 {
		t.Fatalf("fixture is not clean before the move: %v", broken)
	}

	satMove(t, s, `{"source_id":"apps/owner","target_id":"services/owner"}`)

	satWantBody(t, k, "services/owner", "[sat](sat.md) [peer](../../apps/peer.md)\n")
	satWantBody(t, k, "services/owner/sat", "[peer](../../apps/peer.md) [leaf](sub/leaf.md)\n")
	satWantBody(t, k, "services/owner/sub/leaf", "[peer](../../../apps/peer.md) [sat](../sat.md) [index](../index.md) [ref](../../ref.md)\n")
	if broken := satBrokenLinks(t, k); len(broken) != 0 {
		t.Errorf("broken_link after the move: %v", broken)
	}
}

// Two expanded concepts moved in one call: a satellite of the first that links
// a page of the second follows it to where it lands, not to where it was.
func TestConceptMove_BatchOfExpandedConceptsComposes(t *testing.T) {
	t.Parallel()
	k := setupTestKB(t)
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	files := satMapFiles("notes", "area", "deep")
	files["notes/other.md"] = satPage("other\n")
	files["notes/a/index.md"] = satPage("[sat](sat.md)\n")
	files["notes/a/sat.md"] = satPage("[b](../b/sat.md) [other](../other.md)\n")
	files["notes/b/index.md"] = satPage("[sat](sat.md)\n")
	files["notes/b/sat.md"] = satPage("[a](../a/sat.md) [other](../other.md)\n")
	satWriteFiles(t, k, files)
	if broken := satBrokenLinks(t, k); len(broken) != 0 {
		t.Fatalf("fixture is not clean before the move: %v", broken)
	}

	satMove(t, s, `{"moves":[
		{"source_id":"notes/a","target_id":"area/a"},
		{"source_id":"notes/b","target_id":"deep/b"}]}`)

	satWantBody(t, k, "area/a/sat", "[b](../../deep/b/sat.md) [other](../../notes/other.md)\n")
	satWantBody(t, k, "deep/b/sat", "[a](../../area/a/sat.md) [other](../../notes/other.md)\n")
	if broken := satBrokenLinks(t, k); len(broken) != 0 {
		t.Errorf("broken_link after the batch: %v", broken)
	}
}

// concept_archive shares applyConceptMoves with concept_move (D344): archiving
// an expanded concept must rebase its satellites the same way.
func TestConceptArchive_RebasesEveryPageOfAnExpandedConcept(t *testing.T) {
	t.Parallel()
	k, s := archiveFixture(t, "", "- [topic](topic/index.md)\n- [friend](friend.md)\n")
	satWriteFiles(t, k, map[string]string{
		"src/topic/index.md":  satPage("[detail](detail.md) [friend](../friend.md)\n"),
		"src/topic/detail.md": satPage("[index](index.md) [friend](../friend.md) ![fig](fig.png)\n"),
		"src/topic/fig.png":   "png",
	})
	if broken := satBrokenLinks(t, k); len(broken) != 0 {
		t.Fatalf("fixture is not clean before the archive: %v", broken)
	}

	args, _ := json.Marshal(map[string]string{"id": "src/topic", "if_match": hashOf(t, k, "src/topic")})
	if res := callTool(t, s, "concept_archive", string(args)); res.IsError {
		t.Fatalf("concept_archive: %+v", res.Content)
	}

	if got := satBody(t, k, "archive/topic"); got != "[detail](detail.md) [friend](../../src/friend.md)\n" {
		t.Errorf("archive/topic body: %q", got)
	}
	if got := satBody(t, k, "archive/topic/detail"); !strings.Contains(got, "[friend](../../src/friend.md)") ||
		!strings.Contains(got, "[index](index.md)") || !strings.Contains(got, "![fig](fig.png)") {
		t.Errorf("archive/topic/detail body: %q", got)
	}
	if broken := satBrokenLinks(t, k); len(broken) != 0 {
		t.Errorf("broken_link after the archive: %v", broken)
	}
}
