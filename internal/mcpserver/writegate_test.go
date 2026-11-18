package mcpserver

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/gitx"
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
	if _, err := k.CommitOpAs("test: seed", "", ""); err != nil {
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

// noHostGitIdentity removes the git identity from everywhere git looks (empty
// home, no system or global config, no identity variables). t.Setenv forbids
// t.Parallel, so the caller must not be parallel.
func noHostGitIdentity(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, "missing-gitconfig"))
	for _, v := range []string{"GIT_AUTHOR_NAME", "GIT_AUTHOR_EMAIL", "GIT_COMMITTER_NAME", "GIT_COMMITTER_EMAIL", "EMAIL", "USER", "USERNAME"} {
		t.Setenv(v, "")
	}
}

// The gate stashes the write to judge the baseline, and git stash creates
// commits: on a host with no git identity the stash must use the server one
// (kb.ServerGitEnv). The gate FAILS OPEN when the stash fails (it logs "write_gate
// skipped" and accepts the write), so "the write succeeded" would pass with the
// bug present: the write here is one the gate refuses, and the refusal proves it
// ran. Not parallel: it sets the process environment.
func TestWriteGate_RefusesOnHostWithoutGitIdentity(t *testing.T) {
	k := gateKB(t)
	s := gateSeed(t, k, nil)
	head := gitOut(t, k, "rev-parse", "HEAD")
	noHostGitIdentity(t)

	newPage := gateWrite("ops/new", "New", "# New\n\nSee [Target](target.md).\n", true, "")
	text, isErr := callJSON(t, s, adminCtx, "concept_batch", gateBatch(newPage))
	refusedCleanly(t, k, head, text, isErr, "broken_link")
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
	if !strings.Contains(text, "concept_write") || !strings.Contains(text, "concept_new") || !strings.Contains(text, "concept_batch") || !strings.Contains(text, "index_entry: true") {
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
		gateWrite("ops/one", "Runbook (active)", "# Runbook (active)\n\nSee [Two](two.md).\n", true, `,"status":"active"`),
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
	gateStashPop = func(string, ...string) error { return errors.New("injected") }
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
	if _, err := k.CommitOpAs("concept_write: ops/p\n\nReason: restructure\nAccepted-Finding: invalid_field_value: legacy status", "", ""); err != nil {
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

// D1069: a wait with no date is refused at write; one call writing both keys
// passes.
func TestWriteGate_WaitUndatedRefused(t *testing.T) {
	t.Parallel()
	k := gateKB(t)
	s := gateSeed(t, k, nil)
	head := gitOut(t, k, "rev-parse", "HEAD")
	undated := gateWrite("ops/waiting", "W", "# W\n", true, `,"status":"draft","waiting_on":"vendor"`)
	text, isErr := callJSON(t, s, adminCtx, "concept_batch", gateBatch(undated))
	refusedCleanly(t, k, head, text, isErr, "wait_undated")
	dated := gateWrite("ops/waiting", "W", "# W\n", true, `,"status":"draft","waiting_on":"vendor","review_after":"2999-01-01"`)
	if text, isErr = callJSON(t, s, adminCtx, "concept_batch", gateBatch(dated)); isErr {
		t.Fatalf("a dated wait was refused: %s", text)
	}
}

// repoObjects is what a refused write must leave unchanged (#2186): the loose
// object count and every unreachable object.
func repoObjects(t *testing.T, k *kb.KB) string {
	t.Helper()
	count := gitOut(t, k, "count-objects", "-v")
	fsck := gitOut(t, k, "fsck", "--unreachable", "--no-reflogs")
	return count + "\n--\n" + fsck
}

// A refused write creates no git object: no stash commit, no blob of the text
// it refused (the value that looked like a credential included), and the
// rollback leaves the tree clean (#2186).
func TestWriteGate_RefusedWriteLeavesNoObject(t *testing.T) {
	k := gateKB(t)
	s := gateSeed(t, k, map[string]string{"ops/p": draftPage})
	secret := "ghp_" + "aB3dE5fG7hJ9kL1mN3pQ5rS7tU9vW1xY3zA5"
	cases := map[string]string{
		"avoidable finding":             `{"id":"ops/q","frontmatter":{"type":"Note","title":"Q","status":"bogus"},"body":"# Q\nbody"}`,
		"potential_secret":              `{"id":"ops/q","frontmatter":{"type":"Note","title":"Q"},"body":"# Q\ntoken ` + secret + `"}`,
		"secret on a page with history": `{"id":"ops/p","frontmatter":{"type":"Note","title":"P","status":"draft"},"body":"# P\ntoken ` + secret + `"}`,
	}
	for name, args := range cases {
		before := repoObjects(t, k)
		head := gitOut(t, k, "rev-parse", "HEAD")
		text, isErr := callJSON(t, s, adminCtx, "concept_write", args)
		if !isErr || !strings.Contains(text, "write_gate: refused") {
			t.Fatalf("%s: got %q (error=%v)", name, text, isErr)
		}
		if strings.Contains(text, secret) {
			t.Fatalf("%s: the refusal echoes the value", name)
		}
		gateClean(t, k)
		if after := repoObjects(t, k); after != before {
			t.Fatalf("%s: the refused write left objects behind:\nbefore:\n%s\nafter:\n%s", name, before, after)
		}
		if gitOut(t, k, "rev-parse", "HEAD") != head || strings.TrimSpace(gitOut(t, k, "stash", "list")) != "" {
			t.Fatalf("%s: HEAD moved or a stash is left", name)
		}
		if ents, _ := filepath.Glob(filepath.Join(k.Root, ".git", "cartographer-quarantine-*")); len(ents) != 0 {
			t.Fatalf("%s: quarantine left behind: %v", name, ents)
		}
		hasHistory := strings.Contains(text, "git history")
		if strings.Contains(text, "ever committed") || hasHistory != (name == "secret on a page with history") && strings.Contains(text, "potential secret") {
			t.Fatalf("%s: history wording wrong (history clause=%v): %q", name, hasHistory, text)
		}
	}
}

// A write the gate lets through still commits its blobs into the real store.
func TestWriteGate_PassingWriteCommitsIntoTheRealStore(t *testing.T) {
	k := gateKB(t)
	s := gateSeed(t, k, map[string]string{"ops/p": draftPage})
	text, isErr := callJSON(t, s, adminCtx, "concept_write", `{"index_entry":true,"id":"ops/ok","frontmatter":{"type":"Note","title":"Ok","status":"draft"},"body":"# Ok\nfine [p](p.md)"}`)
	if isErr {
		t.Fatalf("refused: %s", text)
	}
	gateClean(t, k)
	if out := gitOut(t, k, "fsck", "--full", "--strict"); strings.Contains(out, "missing") || strings.Contains(out, "error") {
		t.Fatalf("fsck:\n%s", out)
	}
	if got := gitOut(t, k, "show", "HEAD:data/ops/ok.md"); !strings.Contains(got, "# Ok") {
		t.Fatalf("committed page: %q", got)
	}
}

// #2220: editing the line that quotes a pre-existing open_marker is not a new
// finding; a marker the call adds is.
func TestWriteGate_EditedMarkerLineIsNotNew(t *testing.T) {
	t.Parallel()
	k := gateKB(t)
	s := gateSeed(t, k, map[string]string{
		"ops/p": "---\ntype: Note\ntitle: P\n---\n# P\n\nTODO fix revproxy later\n\nSee [Q](q.md).\n",
		"ops/q": "---\ntype: Note\ntitle: Q\n---\n# Q\n\nSee [P](p.md).\n",
	})
	patch := func(old, repl string) string {
		cd, _ := k.ReadConcept("ops/p")
		b, _ := json.Marshal(map[string]string{"id": "ops/p", "old_string": old, "new_string": repl, "if_match": cd.ContentHash})
		return string(b)
	}
	if text, isErr := callJSON(t, s, adminCtx, "concept_patch", patch("revproxy", "Reverse proxy")); isErr {
		t.Fatalf("editing the line of a pre-existing marker was refused: %s", text)
	}
	head := gitOut(t, k, "rev-parse", "HEAD")
	text, isErr := callJSON(t, s, adminCtx, "concept_patch", patch("See [Q]", "TODO another\n\nSee [Q]"))
	refusedCleanly(t, k, head, text, isErr, "open_marker")
}

// #2220: the write gate and gate_check changed_ids report the unknown_type
// that lint reports on the page (type note where the KB declares Note).
func TestWriteGate_UnknownTypeIsJudged(t *testing.T) {
	t.Parallel()
	k := gateKB(t)
	if err := os.MkdirAll(filepath.Join(k.Root, "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(k.Root, "templates", "note.md"), []byte("---\ntype: Note\ntitle: Note\n---\n# {{title}}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mapMD := "---\ntype: Map\nkind: map\ntitle: Ops\ntemplates: [note]\n---\n# Ops\n"
	s := gateSeed(t, k, map[string]string{
		"ops/_map": mapMD,
		"ops/ok":   "---\ntype: Note\ntitle: Ok\n---\n# Ok\n\nSee [Ok2](ok2.md).\n",
		"ops/ok2":  "---\ntype: Note\ntitle: Ok2\n---\n# Ok2\n\nSee [Ok](ok.md).\n",
	})
	head := gitOut(t, k, "rev-parse", "HEAD")
	text, isErr := callJSON(t, s, adminCtx, "concept_write", `{"id":"ops/bad","frontmatter":{"type":"note","title":"Bad"},"body":"# Bad\n\nSee [Ok](ok.md).\n","index_entry":true}`)
	refusedCleanly(t, k, head, text, isErr, "unknown_type")
}

// #2220: gate_check changed_ids lists the same per-page findings lint does:
// unknown_type and contradiction_status.
func TestGateCheck_ChangedIDsReportWhatLintReports(t *testing.T) {
	t.Parallel()
	k := gateKB(t)
	if err := os.MkdirAll(filepath.Join(k.Root, "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(k.Root, "templates", "note.md"), []byte("---\ntype: Note\ntitle: Note\n---\n# {{title}}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := gateSeed(t, k, map[string]string{
		"ops/_map": "---\ntype: Map\nkind: map\ntitle: Ops\ntemplates: [note]\n---\n# Ops\n",
		"ops/a":    "---\ntype: note\ntitle: A\n---\n# A\n",
		"ops/c":    "---\ntype: Contradiction\ntitle: C\nresolution_status: banana\n---\n# C\n",
	})
	text := mustText(t, s, "gate_check", `{"changed_ids":["ops/a","ops/c"]}`)
	for _, check := range []string{"unknown_type", "contradiction_status"} {
		if !strings.Contains(text, `"check":"`+check+`"`) {
			t.Errorf("gate_check changed_ids misses %s: %s", check, text)
		}
	}
}

// A foreign index.lock taken while the gate holds its staged write must never
// strand the repository's index on blobs the gate's quarantine took with it
// (#2222): the gate stages into a private index, so the real one is untouched,
// every later write succeeds and the object store is whole.
func TestWriteGate_ForeignIndexLockLeavesNoDanglingIndex(t *testing.T) {
	k := gateKB(t)
	s := gateSeed(t, k, map[string]string{"ops/p": draftPage})
	lock := filepath.Join(k.Root, ".git", "index.lock")
	orig := gateAfterStage
	gateAfterStage = func(*kb.KB) {
		if err := os.WriteFile(lock, nil, 0o644); err != nil {
			t.Error(err)
		}
	}
	defer func() { gateAfterStage = orig }()
	callJSON(t, s, adminCtx, "concept_write", `{"id":"ops/q","frontmatter":{"type":"Note","title":"Q"},"body":"See [P](p.md).","index_entry":true}`)
	gateAfterStage = orig
	if err := os.Remove(lock); err != nil {
		t.Fatal(err)
	}
	// Every blob the index names is in the object store.
	for _, line := range strings.Split(strings.TrimSpace(gitOut(t, k, "ls-files", "-s")), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		if err := exec.Command("git", "-C", k.Root, "cat-file", "-e", f[1]).Run(); err != nil {
			t.Fatalf("index names a missing object: %s", line)
		}
	}
	if text, isErr := callJSON(t, s, adminCtx, "concept_write", `{"id":"ops/r","frontmatter":{"type":"Note","title":"R"},"body":"See [P](p.md).","index_entry":true}`); isErr {
		t.Fatalf("the next write failed: %s", text)
	}
}

// takeLockAfterStage makes a foreign index.lock appear once the commit has
// staged the write (the window of #2241) and releases it after hold.
func takeLockAfterStage(t *testing.T, k *kb.KB, hold time.Duration) {
	t.Helper()
	lock := filepath.Join(k.Root, ".git", "index.lock")
	armed := true
	gitx.AfterStage = func(string) {
		if !armed {
			return
		}
		armed = false
		if err := os.WriteFile(lock, nil, 0o644); err != nil {
			t.Error(err)
			return
		}
		go func() {
			time.Sleep(hold)
			os.Remove(lock)
		}()
	}
	oldWait, oldPoll := gitx.IndexLockWait, gitx.IndexLockPoll
	t.Cleanup(func() {
		gitx.AfterStage = nil
		gitx.IndexLockWait, gitx.IndexLockPoll = oldWait, oldPoll
		os.Remove(lock)
	})
	gitx.IndexLockPoll = 10 * time.Millisecond
}

// A write whose commit fails on a foreign index.lock that clears within the
// wait leaves nothing on disk (#2241).
func TestFailedCommit_LockClearedWithinTheWaitRestoresTheTree(t *testing.T) {
	k := gateKB(t)
	s := gateSeed(t, k, map[string]string{"ops/p": draftPage})
	takeLockAfterStage(t, k, 200*time.Millisecond)
	gitx.IndexLockWait = 3 * time.Second
	text, isErr := callJSON(t, s, adminCtx, "concept_write", `{"id":"ops/q","frontmatter":{"type":"Note","title":"Q"},"body":"See [P](p.md).","index_entry":true}`)
	if !isErr || !strings.Contains(text, "not saved") {
		t.Fatalf("want a not-saved error, got %q", text)
	}
	if st := strings.TrimSpace(gitOut(t, k, "status", "--porcelain")); st != "" {
		t.Fatalf("the failed write left files: %s", st)
	}
}

// A lock that outlasts the wait leaves the files, but the next write discards
// them instead of committing them as external changes (#2241).
func TestFailedCommit_LockHeldPastTheWaitIsDiscardedByTheNextWrite(t *testing.T) {
	k := gateKB(t)
	s := gateSeed(t, k, map[string]string{"ops/p": draftPage})
	takeLockAfterStage(t, k, 400*time.Millisecond)
	gitx.IndexLockWait = 50 * time.Millisecond
	text, isErr := callJSON(t, s, adminCtx, "concept_write", `{"id":"ops/q","frontmatter":{"type":"Note","title":"Q"},"body":"See [P](p.md).","index_entry":true}`)
	if !isErr || !strings.Contains(text, "not saved") {
		t.Fatalf("want a not-saved error, got %q", text)
	}
	time.Sleep(600 * time.Millisecond) // the lock is gone
	if text, isErr := callJSON(t, s, adminCtx, "concept_write", `{"id":"ops/r","frontmatter":{"type":"Note","title":"R"},"body":"See [P](p.md).","index_entry":true}`); isErr {
		t.Fatalf("the next write failed: %s", text)
	}
	for _, rev := range strings.Fields(gitOut(t, k, "rev-list", "HEAD")) {
		files := gitOut(t, k, "show", "--name-only", "--format=", rev)
		if strings.Contains(files, "ops/q.md") {
			t.Fatalf("the refused page reached HEAD in %s:\n%s", rev, files)
		}
	}
	if _, err := os.Stat(filepath.Join(k.Root, "data", "ops", "q.md")); err == nil {
		t.Fatal("the refused page is still on disk")
	}
	if _, err := os.Stat(filepath.Join(k.Root, "data", "ops", "r.md")); err != nil {
		t.Fatalf("the next write's own page is missing: %v\n%s", err, gitOut(t, k, "ls-files"))
	}
}
