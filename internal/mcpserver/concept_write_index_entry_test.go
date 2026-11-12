package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// D1099: concept_write takes index_entry with the concept_new semantics, and
// the first page of an archive is accepted by the write gate.

// The map is not called "docs": the repodocs check fails the build on a cited
// docs/*.md path that does not exist.
func cwArgs(id, title, body string, extra map[string]any) string {
	m := map[string]any{"id": id, "frontmatter": map[string]any{"type": "Note", "title": title}, "body": body}
	for k, v := range extra {
		m[k] = v
	}
	b, _ := json.Marshal(m)
	return string(b)
}

func TestConceptWrite_IndexEntry_AddsEntryInOneCommit(t *testing.T) {
	t.Parallel()
	k, _ := setupGitKB(t)
	seedIndexMaps(t, k, mapFiles("pages", ""))
	forwardLinks(t, k, "pages/fresh", "pages/plain")
	if _, err := k.CommitOpAs("test: seed", "", ""); err != nil {
		t.Fatal(err)
	}
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	before := commitCount(t, k)

	text, isErr := callJSON(t, s, adminCtx, "concept_write", cwArgs("pages/fresh", "Fresh", "# Fresh\n", map[string]any{"index_entry": true}))
	if isErr {
		t.Fatalf("%s", text)
	}
	if !strings.Contains(readIdx(t, k, "pages"), "## New pages\n\n- [Fresh](fresh.md)\n") {
		t.Fatalf("index:\n%s", readIdx(t, k, "pages"))
	}
	if !strings.Contains(text, "curated_indexes") || strings.Contains(text, "index_incomplete") {
		t.Fatalf("result: %s", text)
	}
	if got := commitCount(t, k) - before; got != 1 {
		t.Fatalf("commits = %d, want 1", got)
	}
	if _, err := k.ReadConcept("pages/fresh"); err != nil {
		t.Fatal(err)
	}

	// Without index_entry the response has no curated_indexes key and the
	// gate asks for the entry (D907).
	preIdx := readIdx(t, k, "pages")
	text, isErr = callJSON(t, s, adminCtx, "concept_write", cwArgs("pages/plain", "Plain", "# Plain\n", nil))
	if !isErr || !strings.Contains(text, "index_incomplete") || readIdx(t, k, "pages") != preIdx {
		t.Fatalf("plain creating write: err=%v %s", isErr, text)
	}
}

func TestConceptWrite_IndexEntry_GeneratedAndAlreadyLinked(t *testing.T) {
	t.Parallel()
	k := setupTestKB(t)
	seedIndexMaps(t, k, mapFiles("gen", "index: generated\n"))
	seedIndexMaps(t, k, mapFiles("pages", ""))
	forwardLinks(t, k, "gen/new", "pages/old")
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	text, isErr := callJSON(t, s, adminCtx, "concept_write", cwArgs("gen/new", "New", "# New\n", map[string]any{"index_entry": true}))
	if isErr || !strings.Contains(text, "gen/index.md is generated: it lists gen/new already") {
		t.Fatalf("generated: %v %s", isErr, text)
	}
	if strings.Contains(readIdx(t, k, "gen"), "New pages") {
		t.Fatalf("generated index edited:\n%s", readIdx(t, k, "gen"))
	}
	// "old" is linked by the seeded curated index already.
	text, isErr = callJSON(t, s, adminCtx, "concept_write", cwArgs("pages/old", "Old", "# Old\n", map[string]any{"index_entry": true}))
	if isErr || !strings.Contains(text, "already links pages/old") {
		t.Fatalf("linked: %v %s", isErr, text)
	}
	if idx := readIdx(t, k, "pages"); strings.Count(idx, "old.md") != 1 || strings.Contains(idx, "New pages") {
		t.Fatalf("duplicate entry:\n%s", idx)
	}
}

func TestConceptWrite_IndexEntry_RefusedShapesChangeNothing(t *testing.T) {
	t.Parallel()
	k := setupTestKB(t)
	seedIndexMaps(t, k, mapFiles("pages", ""))
	// A map whose curated index cannot be read.
	seedIndexMaps(t, k, map[string]string{"broken/_map.md": "---\ntype: Map\nkind: map\ntitle: broken\n---\n"})
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	existing := readHash(t, k, "manutenzione/test-runbook")
	cases := []struct {
		name, args, want string
	}{
		{"existing id", cwArgs("manutenzione/test-runbook", "T", "x", map[string]any{"index_entry": true}), "applies only to a creation"},
		{"existing id with if_match", cwArgs("manutenzione/test-runbook", "T", "x", map[string]any{"index_entry": true, "if_match": existing}), "applies only to a creation"},
		{"missing id with if_match", cwArgs("pages/ghost", "G", "x", map[string]any{"index_entry": true, "if_match": "abc"}), "applies only to a creation"},
		{"top-level id", cwArgs("toplevel", "Top", "x", map[string]any{"index_entry": true}), "needs a concept inside a map"},
		{"unreadable index", cwArgs("broken/page", "P", "x", map[string]any{"index_entry": true}), "index.md unreadable"},
		// services/ is the registry, not a Map; a folder with no _map.md is no Map either.
		{"services id", cwArgs("services/svc-a", "S", "x", map[string]any{"index_entry": true}), "services/ is not a Map, so it has no index to add an entry to: drop index_entry"},
		{"folder without a map", cwArgs("nomap/page", "P", "x", map[string]any{"index_entry": true}), "nomap/ is not a Map (no _map.md): drop index_entry, or create the Map first"},
	}
	preLog, _ := k.ReadRaw("log.md")
	preIdx := readIdx(t, k, "pages")
	preBody, _ := os.ReadFile(filepath.Join(k.DataRoot(), "manutenzione", "test-runbook.md"))
	for _, c := range cases {
		text, isErr := callJSON(t, s, adminCtx, "concept_write", c.args)
		if !isErr || !strings.Contains(text, c.want) {
			t.Fatalf("%s: err=%v %s", c.name, isErr, text)
		}
	}
	postLog, _ := k.ReadRaw("log.md")
	postBody, _ := os.ReadFile(filepath.Join(k.DataRoot(), "manutenzione", "test-runbook.md"))
	if postLog != preLog || readIdx(t, k, "pages") != preIdx || string(postBody) != string(preBody) {
		t.Fatal("log, index or page changed on a refusal")
	}
	for _, rel := range []string{"pages/ghost.md", "toplevel.md", "broken/page.md", "nomap/page.md"} {
		if _, err := os.Stat(filepath.Join(k.DataRoot(), rel)); !os.IsNotExist(err) {
			t.Fatalf("%s written by a refused call", rel)
		}
	}
	if _, err := os.Stat(filepath.Join(k.Root, "services", "svc-a.md")); !os.IsNotExist(err) {
		t.Fatal("services/svc-a written by a refused call")
	}
}

// The unreadable-descriptor and unreadable-index refusals name the folder, never
// the directory the server keeps its data in (relPathErr, #1911). A directory
// where the file should be fails to read with EISDIR on every platform.
func TestConceptWrite_IndexEntry_UnreadableErrorsCarryNoServerPath(t *testing.T) {
	t.Parallel()
	k := setupTestKB(t)
	seedIndexMaps(t, k, map[string]string{
		"nodesc/index.md": "---\ntype: Index\ntitle: nodesc\n---\n# nodesc\n",
		"noindex/_map.md": "---\ntype: Map\nkind: map\ntitle: noindex\n---\n# noindex\n",
	})
	// nodesc/_map.md and noindex/index.md are directories.
	for _, dir := range []string{"nodesc/_map.md", "noindex/index.md"} {
		if err := os.MkdirAll(filepath.Join(k.DataRoot(), dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	for _, id := range []string{"nodesc/page", "noindex/page"} {
		text, isErr := callJSON(t, s, adminCtx, "concept_write", cwArgs(id, "P", "x", map[string]any{"index_entry": true}))
		if !isErr || !strings.Contains(text, "index.md unreadable") {
			t.Fatalf("%s: err=%v %s", id, isErr, text)
		}
		for _, root := range []string{k.Root, k.DataRoot()} {
			if strings.Contains(text, root) {
				t.Fatalf("%s: the result carries the server path %q: %s", id, root, text)
			}
		}
	}
}

// An index that becomes unreadable after planning (appendCuratedEntries) and one
// that is unreadable when a delete drops its entry (removeCuratedEntryAt) reach
// the caller as notes: neither carries the server's data directory (#1911).
func TestIndexEntry_UnreadableAtEditTimeCarriesNoServerPath(t *testing.T) {
	asDir := func(t *testing.T, k *kb.KB, mapName string) {
		t.Helper()
		idx := filepath.Join(k.DataRoot(), mapName, "index.md")
		if err := os.Remove(idx); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(idx, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	noRoot := func(t *testing.T, k *kb.KB, text string) {
		t.Helper()
		if !strings.Contains(text, "unreadable") {
			t.Fatalf("want an unreadable note: %s", text)
		}
		for _, root := range []string{k.Root, k.DataRoot()} {
			if strings.Contains(text, root) {
				t.Fatalf("the result carries the server path %q: %s", root, text)
			}
		}
	}

	t.Run("append after planning", func(t *testing.T) {
		k := setupTestKB(t)
		if err := os.RemoveAll(filepath.Join(k.Root, ".git")); err != nil {
			t.Fatal(err)
		}
		seedIndexMaps(t, k, mapFiles("pages", ""))
		s := New("test")
		RegisterKBTools(s, k, Deps{})
		indexPatchHook = func(mapName string) error { asDir(t, k, mapName); return nil }
		t.Cleanup(func() { indexPatchHook = nil })
		text, isErr := callJSON(t, s, adminCtx, "concept_write", cwArgs("pages/fresh", "Fresh", "# Fresh\n", map[string]any{"index_entry": true}))
		if isErr {
			t.Fatalf("want a note, got an error: %s", text)
		}
		noRoot(t, k, text)
	})

	t.Run("delete drops the entry", func(t *testing.T) {
		k := setupTestKB(t)
		if err := os.RemoveAll(filepath.Join(k.Root, ".git")); err != nil {
			t.Fatal(err)
		}
		files := mapFiles("pages", "")
		files["pages/gone.md"] = "---\ntype: Note\ntitle: gone\n---\n# gone\n"
		seedIndexMaps(t, k, files)
		asDir(t, k, "pages")
		s := New("test")
		RegisterKBTools(s, k, Deps{})
		text, isErr := callJSON(t, s, adminCtx, "concept_delete", `{"id":"pages/gone"}`)
		if isErr {
			t.Fatalf("want a note, got an error: %s", text)
		}
		noRoot(t, k, text)
	})
}

// readOnlyDir makes dir refuse new files, so the atomic write of the index in
// it fails in os.CreateTemp with an *fs.PathError that carries the absolute
// data directory. It skips where permission bits do not bind: Windows, and a
// process that is root.
func readOnlyDir(t *testing.T, dir string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("a directory cannot be made read-only through its mode on Windows")
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	probe, err := os.CreateTemp(dir, "probe")
	if err == nil {
		probe.Close()
		os.Remove(probe.Name())
		t.Skip("the process can write to a read-only directory (root?)")
	}
}

// The index file that cannot be written (PatchIndex fails with a path error)
// reaches the caller as a note, and neither the index_entry note nor the one
// of a delete carries the server's data directory (#1911).
func TestIndexEntry_UnwritableIndexCarriesNoServerPath(t *testing.T) {
	noRoot := func(t *testing.T, k *kb.KB, text string) {
		t.Helper()
		if !strings.Contains(text, "permission denied") {
			t.Fatalf("want the write failure in the note: %s", text)
		}
		for _, root := range []string{k.Root, k.DataRoot()} {
			if strings.Contains(text, root) {
				t.Fatalf("the result carries the server path %q: %s", root, text)
			}
		}
	}

	t.Run("append", func(t *testing.T) {
		k := setupTestKB(t)
		if err := os.RemoveAll(filepath.Join(k.Root, ".git")); err != nil {
			t.Fatal(err)
		}
		seedIndexMaps(t, k, mapFiles("pages", ""))
		s := New("test")
		RegisterKBTools(s, k, Deps{})
		indexPatchHook = func(mapName string) error { readOnlyDir(t, filepath.Join(k.DataRoot(), mapName)); return nil }
		t.Cleanup(func() { indexPatchHook = nil })
		text, isErr := callJSON(t, s, adminCtx, "concept_write", cwArgs("pages/fresh", "Fresh", "# Fresh\n", map[string]any{"index_entry": true}))
		if isErr {
			t.Fatalf("want a note, got an error: %s", text)
		}
		noRoot(t, k, text)
	})

	t.Run("remove", func(t *testing.T) {
		k := setupTestKB(t)
		files := mapFiles("pages", "")
		files["pages/gone.md"] = "---\ntype: Note\ntitle: gone\n---\n# gone\n"
		seedIndexMaps(t, k, files)
		idx := filepath.Join(k.DataRoot(), "pages", "index.md")
		data, err := os.ReadFile(idx)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(idx, append(data, []byte("\n- [gone](gone.md) - gone\n")...), 0o644); err != nil {
			t.Fatal(err)
		}
		readOnlyDir(t, filepath.Dir(idx))
		removed, _, err := removeCuratedEntry(k, "pages", "pages/gone")
		if removed || err == nil {
			t.Fatalf("want a write failure, got removed=%v err=%v", removed, err)
		}
		noRoot(t, k, err.Error())
	})
}

// The scrubbed index errors keep their chain: a stale write stays
// okf.ErrStaleWrite through applyIndexEntries' wrapping, and batchWriteError
// still maps it to stale_write, while the path error stays reachable and its
// text loses the data directory.
func TestScrubErr_KeepsTheChainAndHidesThePath(t *testing.T) {
	k := setupTestKB(t)
	root := k.DataRoot()
	if scrubErr(nil, root) != nil {
		t.Fatal("scrubErr(nil) must be nil")
	}

	stale := scrubErr(fmt.Errorf("%w", okf.ErrStaleWrite), k.Root, root)
	wrapped := fmt.Errorf("batch op 0 (pages/fresh): %w", fmt.Errorf("pages/index.md: %w", stale))
	if !errors.Is(wrapped, okf.ErrStaleWrite) {
		t.Fatalf("a stale index write lost its identity: %v", wrapped)
	}
	ops := []kb.BatchWriteOp{{ID: "pages/fresh"}}
	if msg := batchWriteError(context.Background(), k, ops, wrapped); !strings.HasPrefix(msg, "stale_write: ") {
		t.Errorf("a stale index write must map to stale_write: %q", msg)
	}

	pe := &fs.PathError{Op: "open", Path: filepath.Join(root, "pages", ".tmp-1"), Err: fs.ErrPermission}
	scrubbed := scrubErr(fmt.Errorf("PatchIndex: %w", pe), k.Root, root)
	if strings.Contains(scrubbed.Error(), root) {
		t.Errorf("the text carries the data directory: %q", scrubbed)
	}
	var got *fs.PathError
	if !errors.Is(scrubbed, fs.ErrPermission) || !errors.As(scrubbed, &got) {
		t.Errorf("the chain of the original is lost: %v", scrubbed)
	}
}

// The index edit failing after the write is a note, not an error; the KB is
// not a git repository, so no gate rolls the write back.
func TestConceptWrite_IndexEntry_EditFailureIsANote(t *testing.T) {
	k := setupTestKB(t)
	if err := os.RemoveAll(filepath.Join(k.Root, ".git")); err != nil {
		t.Fatal(err)
	}
	seedIndexMaps(t, k, mapFiles("pages", ""))
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	indexPatchHook = func(string) error { return errors.New("injected failure") }
	t.Cleanup(func() { indexPatchHook = nil })
	text, isErr := callJSON(t, s, adminCtx, "concept_write", cwArgs("pages/fresh", "Fresh", "# Fresh\n", map[string]any{"index_entry": true}))
	if isErr || !strings.Contains(text, "injected failure") || !strings.Contains(text, "curated_indexes") {
		t.Fatalf("want a note, got err=%v %s", isErr, text)
	}
	if _, err := k.ReadConcept("pages/fresh"); err != nil {
		t.Fatalf("the concept must stay written: %v", err)
	}
}

func TestConceptWrite_SchemaAdvertisesIndexEntry(t *testing.T) {
	t.Parallel()
	k := setupTestKB(t)
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	var tool *Tool
	for _, tl := range s.tools {
		if tl.Name == "concept_write" {
			tool = tl
		}
	}
	if tool == nil {
		t.Fatal("concept_write not registered")
	}
	var schema struct {
		Properties map[string]struct {
			Type string `json:"type"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
		t.Fatal(err)
	}
	if schema.Properties["index_entry"].Type != "boolean" {
		t.Fatalf("index_entry not advertised as boolean: %+v", schema.Properties)
	}
}

// The first-write path end to end (D1099): a fresh git KB, a new map, then the
// first page with index_entry succeeds; a second page that links nothing is
// refused as orphan; the same call linking the first succeeds.
func TestWriteGate_FirstPageOfNewMapPasses_SecondMustLink(t *testing.T) {
	t.Parallel()
	k, _ := setupGitKB(t)
	s := New("test")
	RegisterKBTools(s, k, Deps{})

	if text, isErr := callJSON(t, s, adminCtx, "map_create", `{"name":"hr","title":"HR"}`); isErr {
		t.Fatalf("map_create: %s", text)
	}
	before := commitCount(t, k)
	text, isErr := callJSON(t, s, adminCtx, "concept_write", cwArgs("hr/first", "First", "# First\n", map[string]any{"index_entry": true}))
	if isErr {
		t.Fatalf("the first page was refused: %s", text)
	}
	if got := commitCount(t, k) - before; got != 1 {
		t.Fatalf("commits = %d, want 1 (page and index entry)", got)
	}
	gateClean(t, k)
	assertNoFinding := func(check string) {
		t.Helper()
		out, _ := callJSON(t, s, adminCtx, "lint", `{}`)
		for _, line := range strings.Split(out, "\n") {
			if strings.Contains(line, `"`+check+`"`) && strings.Contains(line, "hr/") {
				t.Fatalf("lint reports %s: %s", check, out)
			}
		}
	}
	assertNoFinding("orphan")
	assertNoFinding("index_incomplete")

	head := gitOut(t, k, "rev-parse", "HEAD")
	text, isErr = callJSON(t, s, adminCtx, "concept_write", cwArgs("hr/second", "Second", "# Second\n", map[string]any{"index_entry": true}))
	refusedCleanly(t, k, head, text, isErr, "orphan")

	text, isErr = callJSON(t, s, adminCtx, "concept_write", cwArgs("hr/second", "Second", "# Second\n\nSee [First](first.md).\n", map[string]any{"index_entry": true}))
	if isErr {
		t.Fatalf("the linking second page was refused: %s", text)
	}
	assertNoFinding("orphan")
}

// #1273: a page that is both an orphan and missing from its map's curated index
// is one finding naming both, with the one fix; a page that is only missing
// from the index keeps its index_incomplete finding.
func TestConceptWrite_OrphanAndMissingIndexEntryIsOneFinding(t *testing.T) {
	t.Parallel()
	k, _ := setupGitKB(t)
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	if text, isErr := callJSON(t, s, adminCtx, "map_create", `{"name":"hr","title":"HR"}`); isErr {
		t.Fatalf("map_create: %s", text)
	}
	if text, isErr := callJSON(t, s, adminCtx, "concept_write", cwArgs("hr/first", "First", "# First\n", map[string]any{"index_entry": true})); isErr {
		t.Fatalf("first page: %s", text)
	}

	text, isErr := callJSON(t, s, adminCtx, "concept_write", cwArgs("hr/lonely", "Lonely", "# Lonely\n", nil))
	if !isErr || !strings.Contains(text, "orphan and index_incomplete") {
		t.Fatalf("lonely page: err=%v %s", isErr, text)
	}
	if strings.Count(text, `"path":"hr/lonely.md"`) != 1 || strings.Contains(text, "missing curated index entry") {
		t.Errorf("the page must be one finding, not two: %s", text)
	}

	text, isErr = callJSON(t, s, adminCtx, "concept_write", cwArgs("hr/linked", "Linked", "# Linked\n\nSee [First](first.md).\n", nil))
	if !isErr || !strings.Contains(text, "missing curated index entry") || strings.Contains(text, "orphan and index_incomplete") {
		t.Errorf("a linked page keeps its plain index_incomplete: err=%v %s", isErr, text)
	}
}
