package mcpserver

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/gitx"
)

// An expanded concept's body is "data/<map>/<concept>/index.md": its edits are
// concept changes, not other_changes.
func TestChangesSince_ExpandedConceptIsAConcept(t *testing.T) {
	t.Parallel()
	k := setupTestKB(t)
	if !gitx.IsRepo(k.Root) {
		t.Skip("git not in PATH")
	}
	base := time.Now().UTC().Truncate(time.Second).Add(5 * time.Minute)
	commitAt := func(at time.Time, subject string) {
		t.Helper()
		if err := gitx.Commit(k.Root, subject, "Alice", "alice@example.test",
			"GIT_AUTHOR_DATE="+at.Format(time.RFC3339), "GIT_COMMITTER_DATE="+at.Format(time.RFC3339)); err != nil {
			t.Fatal(err)
		}
	}
	write := func(rel, content string) {
		t.Helper()
		p := filepath.Join(k.Root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	commitAt(base, "test: fixture")
	write("data/m/c/index.md", "one\n")
	commitAt(base.Add(time.Minute), "concept_write: m/c")
	write("data/m/c/index.md", "two\n")
	commitAt(base.Add(2*time.Minute), "concept_patch: m/c")
	if err := os.Rename(filepath.Join(k.Root, "data/m/c"), filepath.Join(k.Root, "data/m/d")); err != nil {
		t.Fatal(err)
	}
	commitAt(base.Add(3*time.Minute), "concept_move: m/c -> m/d")

	tr, err := toolChangesSince(k).Handler(authLocalContext(), json.RawMessage(fmt.Sprintf(`{"since":%q}`, base.Add(time.Second).Format(time.RFC3339))))
	if err != nil || tr.IsError {
		t.Fatalf("changes_since: %+v %v", tr, err)
	}
	var res changesSinceResult
	if err := json.Unmarshal([]byte(tr.Content[0].Text), &res); err != nil {
		t.Fatal(err)
	}
	if res.OtherChanges != 0 {
		t.Errorf("other_changes = %d, want 0", res.OtherChanges)
	}
	if len(res.Concepts) != 1 || res.Concepts[0].ID != "m/d" {
		t.Errorf("concepts = %+v, want only m/d (c renamed to d)", res.Concepts)
	}
}

// Two targets in an ancestor/descendant relation fail halfway: the flat move
// creates m/y/ and the directory rename then finds it.
func TestConceptMove_RefusesCollidingTargets(t *testing.T) {
	t.Parallel()
	k := setupTestKB(t)
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	files := satMapFiles("m")
	files["m/z.md"] = satPage("z\n")
	files["m/x/index.md"] = satPage("x\n")
	files["m/x/sat.md"] = satPage("sat\n")
	satWriteFiles(t, k, files)

	res := callTool(t, s, "concept_move", `{"moves":[{"source_id":"m/z","target_id":"m/y/child"},{"source_id":"m/x","target_id":"m/y"}]}`)
	if !res.IsError {
		t.Fatalf("want a refusal, got %+v", res.Content)
	}
	for _, rel := range []string{"m/z.md", "m/x/index.md", "m/x/sat.md"} {
		if _, err := os.Stat(filepath.Join(k.DataRoot(), filepath.FromSlash(rel))); err != nil {
			t.Errorf("%s moved despite the refusal: %v", rel, err)
		}
	}
	if _, err := os.Stat(filepath.Join(k.DataRoot(), "m", "y")); !os.IsNotExist(err) {
		t.Errorf("m/y was created: %v", err)
	}
}

func TestInsertUnderHeading(t *testing.T) {
	t.Parallel()
	body := "# M\n\n## New pages\n\n- [a](a.md)\n\n## Archive\n\n- old\n"
	got, ok := insertUnderHeading(body, "## New pages", "- [b](b.md)")
	if !ok {
		t.Fatal("heading not found")
	}
	want := "# M\n\n## New pages\n\n- [a](a.md)\n- [b](b.md)\n\n## Archive\n\n- old\n"
	if got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
	// Last section: entry at the end.
	got, _ = insertUnderHeading("# M\n\n## New pages\n\n- [a](a.md)\n", "## New pages", "- [b](b.md)")
	if got != "# M\n\n## New pages\n\n- [a](a.md)\n- [b](b.md)\n\n" && got != "# M\n\n## New pages\n\n- [a](a.md)\n- [b](b.md)\n" {
		t.Errorf("last section: %q", got)
	}
	// Empty section.
	got, _ = insertUnderHeading("## New pages\n\n## Archive\n", "## New pages", "- x")
	if got != "## New pages\n\n- x\n\n## Archive\n" {
		t.Errorf("empty section: %q", got)
	}
	// Prose or a code fence mentioning the heading does not match.
	if _, ok := insertUnderHeading("see ## New pages in prose\n\n```\n## New pages\n```\n", "## New pages", "- x"); ok {
		t.Error("prose or fenced heading matched")
	}
	if strings.Contains(got, "\r") {
		t.Error("unexpected CR")
	}
}

// Editor junk inside an expanded concept neither blocks concept_transfer nor
// travels: the source directory is removed whole (#1377).
func TestConceptTransfer_IgnoresEditorJunk(t *testing.T) {
	t.Parallel()
	f := newTransferFixture(t)
	xferWrite(t, f.a, "notes/dossier/index.md", "---\ntype: Note\ntitle: Dossier\n---\n[sat](part.md)\n")
	xferWrite(t, f.a, "notes/dossier/part.md", "---\ntype: Note\ntitle: Part\n---\nsatellite\n")
	xferCommit(t, f.a)
	xferWrite(t, f.a, "notes/dossier/.DS_Store", "junk")

	res := f.transfer(t, `{"target_kb":"kb-b","moves":[{"source_id":"notes/dossier","target_id":"wiki/dossier"}]}`)
	if res.IsError {
		t.Fatalf("concept_transfer: %s", xferText(res))
	}
	if _, err := os.Stat(filepath.Join(f.a.DataRoot(), "notes", "dossier")); !os.IsNotExist(err) {
		t.Error("source directory still exists")
	}
	if _, err := os.Stat(filepath.Join(f.b.DataRoot(), "wiki", "dossier", ".DS_Store")); !os.IsNotExist(err) {
		t.Error("junk was carried to the target")
	}
	xferClean(t, f.a)
	xferClean(t, f.b)
}
