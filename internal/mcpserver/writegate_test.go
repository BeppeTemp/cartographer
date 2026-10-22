package mcpserver

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/kb"
)

// gateKB is a git KB: map "arch" generates its index and carries
// field_values.*.status [active, deprecated], map "ops" is free and keeps a
// curated index (the default). The write gate needs no setting (D907).
func gateKB(t *testing.T) *kb.KB {
	t.Helper()
	k, _ := setupGitKB(t)
	for _, m := range []string{"arch", "ops"} {
		if err := k.CreateMap(m, strings.ToUpper(m), "map", nil); err != nil {
			t.Fatal(err)
		}
	}
	mapMD := "---\ntype: Map\nkind: map\ntitle: Arch\nindex: generated\nfield_values.*.status: [active, deprecated]\n---\n# Arch\n"
	if err := os.WriteFile(filepath.Join(k.DataRoot(), "arch", "_map.md"), []byte(mapMD), 0o644); err != nil {
		t.Fatal(err)
	}
	return k
}

func gateSeed(t *testing.T, k *kb.KB, files map[string]string) *Server {
	t.Helper()
	for id, content := range files {
		p := filepath.Join(k.DataRoot(), filepath.FromSlash(id)+".md")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := k.CommitOp("test: seed"); err != nil {
		t.Fatal(err)
	}
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	return s
}

func gateClean(t *testing.T, k *kb.KB) {
	t.Helper()
	if st := strings.TrimSpace(gitOut(t, k, "status", "--short")); st != "" {
		t.Fatalf("tree not clean after a refused write:\n%s", st)
	}
}

const draftPage = "---\ntype: Note\ntitle: P\nstatus: draft\n---\n# P\n"

// gateWrite is one creating batch operation, with index_entry when the page
// asks for its curated index entry.
func gateWrite(id, title, body string, indexEntry bool, extra string) string {
	fm := `{"type":"Note","title":"` + title + `"` + extra + `}`
	op := `{"op":"write","id":"` + id + `","frontmatter":` + fm + `,"body":` + jsonString(body)
	if indexEntry {
		op += `,"index_entry":true`
	}
	return op + "}"
}

func gateBatch(ops ...string) string {
	return `{"operations":[` + strings.Join(ops, ",") + `]}`
}

// refusedCleanly asserts a refusal that names check and left HEAD, the tree
// and the KB as they were.
func refusedCleanly(t *testing.T, k *kb.KB, head, text string, isErr bool, check string) {
	t.Helper()
	if !isErr || !strings.Contains(text, "write_gate: refused, nothing was written") || !strings.Contains(text, `"check":"`+check+`"`) {
		t.Fatalf("want a refusal naming %s, got %q (error=%v)", check, text, isErr)
	}
	gateClean(t, k)
	if gitOut(t, k, "rev-parse", "HEAD") != head {
		t.Fatal("HEAD moved on a refused write")
	}
}

// Acceptance: a write adding a broken link is refused, and the same batch
// creating the target passes.
func TestWriteGate_BrokenLinkRefusedTargetInBatchPasses(t *testing.T) {
	t.Parallel()
	k := gateKB(t)
	s := gateSeed(t, k, nil)
	head := gitOut(t, k, "rev-parse", "HEAD")
	newPage := gateWrite("ops/new", "New", "# New\n\nSee [Target](target.md).\n", true, "")

	text, isErr := callJSON(t, s, adminCtx, "concept_batch", gateBatch(newPage))
	refusedCleanly(t, k, head, text, isErr, "broken_link")
	if !strings.Contains(text, "broken link to ops/target.md") {
		t.Fatalf("refusal does not say which link: %s", text)
	}
	if _, err := k.ReadConcept("ops/new"); err == nil {
		t.Fatal("a refused page exists")
	}

	target := gateWrite("ops/target", "Target", "# Target\n\nBack to [New](new.md).\n", true, "")
	if text, isErr = callJSON(t, s, adminCtx, "concept_batch", gateBatch(newPage, target)); isErr {
		t.Fatalf("the batch creating the target was refused: %s", text)
	}
}

// Acceptance: a new page without its curated index entry is refused, and the
// same batch with index_entry passes.
func TestWriteGate_MissingIndexEntryRefusedEntryInBatchPasses(t *testing.T) {
	t.Parallel()
	k := gateKB(t)
	s := gateSeed(t, k, nil)
	head := gitOut(t, k, "rev-parse", "HEAD")
	pair := func(indexEntry bool) string {
		return gateBatch(
			gateWrite("ops/one", "One", "# One\n\nSee [Two](two.md).\n", indexEntry, ""),
			gateWrite("ops/two", "Two", "# Two\n\nSee [One](one.md).\n", indexEntry, ""))
	}
	text, isErr := callJSON(t, s, adminCtx, "concept_batch", pair(false))
	refusedCleanly(t, k, head, text, isErr, "index_incomplete")
	if !strings.Contains(text, "missing curated index entry for ops/one") {
		t.Fatalf("refusal = %s", text)
	}
	if !strings.Contains(text, "concept_new") || !strings.Contains(text, "concept_batch") || !strings.Contains(text, "index_entry: true") {
		t.Fatalf("the hint does not name the tools that add the entry: %s", text)
	}
	if text, isErr = callJSON(t, s, adminCtx, "concept_batch", pair(true)); isErr {
		t.Fatalf("the batch with index_entry was refused: %s", text)
	}
}

// Acceptance: a finding a write could have avoided refuses, a title_quality one
// as much as any other, and no setting or argument lets it through: an
// accept_findings argument (removed, D907) changes nothing.
func TestWriteGate_AvoidableRefusesWhateverTheArguments(t *testing.T) {
	t.Parallel()
	k := gateKB(t)
	s := gateSeed(t, k, nil)
	head := gitOut(t, k, "rev-parse", "HEAD")
	batch := gateBatch(
		gateWrite("ops/one", "Active Runbook", "# Active Runbook\n\nSee [Two](two.md).\n", true, `,"status":"active"`),
		gateWrite("ops/two", "Two", "# Two\n\nSee [One](one.md).\n", true, ""))
	text, isErr := callJSON(t, s, adminCtx, "concept_batch", batch)
	refusedCleanly(t, k, head, text, isErr, "title_quality")
	if strings.Contains(text, "severity") {
		t.Fatalf("the refusal names a severity: %s", text)
	}
	accepted := strings.TrimSuffix(batch, "}") + `,"accept_findings":[{"check":"title_quality","reason":"kept"}]}`
	text, isErr = callJSON(t, s, adminCtx, "concept_batch", accepted)
	refusedCleanly(t, k, head, text, isErr, "title_quality")
	for name, tool := range s.Tools() {
		if strings.Contains(string(tool.InputSchema), "accept_findings") {
			t.Errorf("%s still advertises accept_findings", name)
		}
	}
}

// Acceptance: a finding that was already there, on the concept written or
// elsewhere, never refuses; a patch that adds one is refused with exactly it.
func TestWriteGate_PreexistingNeverBlocks(t *testing.T) {
	t.Parallel()
	k := gateKB(t)
	s := gateSeed(t, k, map[string]string{
		"ops/p": "---\ntype: Note\ntitle: P\n---\n# P\n\nSee [Gone](gone.md).\n\nText.\n",
		"ops/q": "---\ntype: Note\ntitle: Q\n---\n# Q\n\nSee [P](p.md).\n",
	})
	head := gitOut(t, k, "rev-parse", "HEAD")

	// Elsewhere: ops/p's broken link and the two missing index entries do not
	// stop a clean batch.
	clean := gateBatch(
		gateWrite("arch/one", "One", "# One\n\nSee [Two](two.md).\n", false, `,"status":"active"`),
		gateWrite("arch/two", "Two", "# Two\n\nSee [One](one.md).\n", false, `,"status":"active"`))
	if text, isErr := callJSON(t, s, adminCtx, "concept_batch", clean); isErr {
		t.Fatalf("a clean batch was refused for findings elsewhere: %s", text)
	}

	patch := func(old, repl string) string {
		cd, _ := k.ReadConcept("ops/p")
		b, _ := json.Marshal(map[string]string{"id": "ops/p", "old_string": old, "new_string": repl, "if_match": cd.ContentHash})
		return string(b)
	}
	// On the concept written: a patch that adds nothing passes.
	if text, isErr := callJSON(t, s, adminCtx, "concept_patch", patch("Text.", "More text.")); isErr {
		t.Fatalf("neutral patch refused: %s", text)
	}
	head = gitOut(t, k, "rev-parse", "HEAD")
	text, isErr := callJSON(t, s, adminCtx, "concept_patch", patch("More text.", "More text, see [Lost](lost.md)."))
	refusedCleanly(t, k, head, text, isErr, "broken_link")
	if !strings.Contains(text, "introduced 1 finding(s)") || !strings.Contains(text, "broken link to ops/lost.md") || strings.Contains(text, "ops/gone.md") {
		t.Fatalf("refusal = %s", text)
	}
	cd, _ := k.ReadConcept("ops/p")
	if strings.Contains(cd.Content, "Lost") {
		t.Fatal("refused patch left content on disk")
	}
	// read and search keep answering with the old content.
	read := mustText(t, s, "concept_read", `{"id":"ops/p"}`)
	if strings.Contains(read, "Lost") || !strings.Contains(read, "More text.") {
		t.Fatalf("concept_read after refusal: %s", read)
	}
	if hits := mustText(t, s, "search", `{"query":"lost"}`); strings.Contains(hits, "ops/p") {
		t.Fatalf("search sees refused content: %s", hits)
	}

	// A moved page keeps its pre-existing findings, compared under the old
	// id: the broken link and the missing index entry that names it.
	if text, isErr := callJSON(t, s, adminCtx, "concept_move", `{"source_id":"ops/p","target_id":"ops/p2"}`); isErr {
		t.Fatalf("move of a page with pre-existing findings refused: %s", text)
	}
}

// A move is judged like any write: one that introduces an error is rolled
// back, and concept_batch is one call, so any introduced finding refuses the
// whole batch.
func TestWriteGate_MoveAndBatchRolledBack(t *testing.T) {
	t.Parallel()
	k := gateKB(t)
	s := gateSeed(t, k, map[string]string{"ops/p": draftPage})
	head := gitOut(t, k, "rev-parse", "HEAD")

	text, isErr := callJSON(t, s, adminCtx, "concept_move", `{"source_id":"ops/p","target_id":"arch/p"}`)
	refusedCleanly(t, k, head, text, isErr, "invalid_field_value")
	if _, err := k.ReadConcept("ops/p"); err != nil {
		t.Fatalf("source gone after a refused move: %v", err)
	}
	if _, err := k.ReadConcept("arch/p"); err == nil {
		t.Fatal("target exists after a refused move")
	}

	bad := gateBatch(
		gateWrite("arch/one", "One", "# One\n\nSee [Two](two.md).\n", false, `,"status":"active"`),
		gateWrite("arch/two", "Two", "# Two\n\nSee [One](one.md).\n", false, `,"status":"draft"`))
	text, isErr = callJSON(t, s, adminCtx, "concept_batch", bad)
	refusedCleanly(t, k, head, text, isErr, "invalid_field_value")
	if _, err := k.ReadConcept("arch/one"); err == nil {
		t.Fatal("the clean half of a refused batch survived")
	}
	// "deprecated" would be valid too, but a retired page that a live one links
	// is its own finding (link_to_retired): fix the value with a live status.
	good := strings.Replace(bad, `"status":"draft"`, `"status":"active"`, 1)
	if text, isErr = callJSON(t, s, adminCtx, "concept_batch", good); isErr {
		t.Fatalf("fixed batch refused: %s", text)
	}
}

// D357: the foreign change is committed on its own before the write, so the
// tree is clean when the gate looks (it used to skip the gate on a dirty tree).
func TestWriteGate_DirtyTreeIsCommittedFirst(t *testing.T) {
	k := gateKB(t)
	s := gateSeed(t, k, map[string]string{"ops/p": draftPage})
	if err := os.WriteFile(filepath.Join(k.DataRoot(), "ops", "foreign.md"), []byte("---\ntype: Note\ntitle: F\n---\nx\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r, w, _ := os.Pipe()
	old := os.Stderr
	os.Stderr = w
	text, isErr := callJSON(t, s, adminCtx, "concept_move", `{"source_id":"ops/p","target_id":"ops/p2"}`)
	w.Close()
	os.Stderr = old
	buf := make([]byte, 1<<16)
	n, _ := r.Read(buf)
	if isErr {
		t.Fatalf("write refused: %s", text)
	}
	if strings.Contains(string(buf[:n]), "write_gate skipped") {
		t.Fatalf("the gate was skipped:\n%s", buf[:n])
	}
	if _, err := k.ReadConcept("ops/p2"); err != nil {
		t.Fatalf("write did not happen: %v", err)
	}
	if subjects := gitOut(t, k, "log", "-2", "--format=%s"); !strings.HasPrefix(subjects, "concept_move") || !strings.HasSuffix(subjects, externalChangesSubject) {
		t.Fatalf("history:\n%s", subjects)
	}
}

func TestWriteGate_StashPopFailureRollsBack(t *testing.T) {
	k := gateKB(t)
	s := gateSeed(t, k, map[string]string{"ops/p": draftPage})
	head := gitOut(t, k, "rev-parse", "HEAD")
	orig := gateStashPop
	gateStashPop = func(string) error { return errors.New("injected") }
	defer func() { gateStashPop = orig }()
	text, isErr := callJSON(t, s, adminCtx, "concept_move", `{"source_id":"ops/p","target_id":"arch/p"}`)
	if !isErr || !strings.Contains(text, "write_gate: could not restore the write, nothing was written") {
		t.Fatalf("got %q (error=%v)", text, isErr)
	}
	// A refusal that wrote nothing has nothing to undo: no recovery hint (#838).
	if strings.Contains(text, "concept_history") {
		t.Fatalf("the refusal carries a recovery hint: %q", text)
	}
	gateClean(t, k)
	if gitOut(t, k, "rev-parse", "HEAD") != head || strings.TrimSpace(gitOut(t, k, "stash", "list")) != "" {
		t.Fatal("rollback left HEAD moved or a stash behind")
	}
}

// Outside a git repository there is nothing to roll back to: the write lands
// and reports its findings, as every write does.
func TestWriteGate_NoGateOutsideAGitRepository(t *testing.T) {
	t.Parallel()
	k := gateKB(t)
	if err := os.RemoveAll(filepath.Join(k.Root, ".git")); err != nil {
		t.Fatal(err)
	}
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	if writeGateActive(k) {
		t.Fatal("the gate is active without a repository")
	}
	if _, has := kbCapabilities(k)["write_gate"]; has {
		t.Fatal("kb_status still reports a write_gate capability")
	}
	text, isErr := callJSON(t, s, adminCtx, "concept_write", `{"id":"arch/w","frontmatter":{"type":"Note","title":"W","status":"bogus"},"body":"b"}`)
	if isErr || !strings.Contains(text, "invalid_field_value") {
		t.Fatalf("write outside a repository = %q (error=%v)", text, isErr)
	}
}

// Accepted-Finding trailers written before D907 stay readable: history and
// changes_since still surface them.
func TestWriteGate_HistorySurfacesAcceptedFindings(t *testing.T) {
	t.Parallel()
	k := gateKB(t)
	s := gateSeed(t, k, map[string]string{"ops/p": draftPage})
	p := filepath.Join(k.DataRoot(), "ops", "p.md")
	if err := os.WriteFile(p, []byte(draftPage+"\nMore.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := k.CommitOp("concept_write: ops/p\n\nReason: restructure\nAccepted-Finding: invalid_field_value: legacy status"); err != nil {
		t.Fatal(err)
	}
	hist := decodeJSON(t, mustText(t, s, "concept_history", `{"id":"ops/p"}`))
	revs := hist["revisions"].([]interface{})
	top := revs[0].(map[string]interface{})
	af, _ := top["accepted_findings"].([]interface{})
	if len(af) != 1 || af[0] != "invalid_field_value: legacy status" || top["reason"] != "restructure" {
		t.Fatalf("history top revision = %v", top)
	}
	if len(revs) > 1 {
		if _, has := revs[1].(map[string]interface{})["accepted_findings"]; has {
			t.Fatal("a commit without the trailer reports accepted_findings")
		}
	}
	ch := decodeJSON(t, mustText(t, s, "changes_since", `{"since":"1d"}`))
	found := false
	for _, c := range ch["concepts"].([]interface{}) {
		m := c.(map[string]interface{})
		if m["id"] == "ops/p" {
			found = true
			if af, _ := m["accepted_findings"].([]interface{}); len(af) != 1 {
				t.Fatalf("changes_since entry = %v", m)
			}
		}
	}
	if !found {
		t.Fatalf("changes_since lacks ops/p: %v", ch)
	}
}

// A refusal for a caller scoped to some maps judges the write the same but
// names no page it cannot see: a retired page that hidden pages link reports
// neither their ids nor how many they are.
func TestWriteGate_RefusalNamesOnlyVisiblePages(t *testing.T) {
	t.Parallel()
	k := gateKB(t)
	k.AuthName = "docs"
	hidden := "---\ntype: Note\ntitle: %s\n---\n# H\n\nSee [R](../arch/r.md).\n"
	s := gateSeed(t, k, map[string]string{
		"arch/r":           "---\ntype: Note\ntitle: R\nstatus: deprecated\n---\n# R\n",
		"ops/hidden-one":   strings.Replace(hidden, "%s", "H1", 1),
		"ops/hidden-two":   strings.Replace(hidden, "%s", "H2", 1),
		"ops/hidden-three": strings.Replace(hidden, "%s", "H3", 1),
	})
	head := gitOut(t, k, "rev-parse", "HEAD")
	ctx := restrictedContext(auth.Policy{Permissions: []auth.Permission{{KB: "docs", Write: true, Maps: []string{"arch"}}}})
	text, isErr := callJSON(t, s, ctx, "concept_batch",
		gateBatch(gateWrite("arch/new", "New", "# New\n\nSee [R](r.md).\n", false, "")))
	refusedCleanly(t, k, head, text, isErr, "link_to_retired")
	if strings.Contains(text, "ops/") || strings.Contains(text, "hidden") || strings.Contains(text, "linked by") {
		t.Fatalf("the refusal names what the caller cannot see: %s", text)
	}
	if !strings.Contains(text, "arch/r") {
		t.Fatalf("the refusal does not name the visible page: %s", text)
	}
	// The fix of a whole-graph finding comes from the whole KB: it is dropped.
	if strings.Contains(text, `"fix"`) || strings.Contains(text, "retarget_links") {
		t.Fatalf("the refusal carries a whole-graph fix: %s", text)
	}
}

// A successful write answers a scoped caller with the findings and rewritten
// pages it can see: a move whose old id is linked from a hidden map names no
// hidden id.
func TestWriteResponse_ScopedCallerSeesNoHiddenPage(t *testing.T) {
	t.Parallel()
	k := gateKB(t)
	k.AuthName = "docs"
	hidden := "---\ntype: Note\ntitle: H\n---\n# H\n\nSee [R](../arch/r.md).\n"
	s := gateSeed(t, k, map[string]string{
		"arch/r":         "---\ntype: Note\ntitle: R\n---\n# R\n\nSee [S](s.md).\n",
		"arch/s":         "---\ntype: Note\ntitle: S\n---\n# S\n",
		"ops/hidden-one": hidden,
		"ops/hidden-two": hidden,
	})
	ctx := restrictedContext(auth.Policy{Permissions: []auth.Permission{{KB: "docs", Write: true, Maps: []string{"arch"}}}})
	text, isErr := callJSON(t, s, ctx, "concept_move", `{"source_id":"arch/r","target_id":"arch/r2","rewrite_links":false}`)
	if strings.Contains(text, "ops/") || strings.Contains(text, "hidden") {
		t.Fatalf("the response names a hidden page (error=%v): %s", isErr, text)
	}
	if isErr {
		t.Fatalf("the move was refused: %s", text)
	}
}

// A caller without whole-KB visibility is told of no index path (an index is
// a whole-KB resource).
func TestCallerView_IndexPathsAreWholeKBOnly(t *testing.T) {
	t.Parallel()
	paths := []string{"arch/index.md", "index.md"}
	scoped := &callerView{visible: func(string) bool { return true }}
	if got := scoped.filterPaths(paths); len(got) != 0 {
		t.Errorf("scoped index paths = %v, want none", got)
	}
	for _, v := range []*callerView{nil, {whole: true}} {
		if got := v.filterPaths(paths); len(got) != 2 {
			t.Errorf("whole index paths = %v", got)
		}
	}
}
