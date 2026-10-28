package mcpserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/gitx"
	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// batchCallMsg builds a tools/call JSON-RPC message for concept_batch with
// the given operations, reusing artifactCallMsg's generic envelope.
func batchCallMsg(t *testing.T, id int, operations []map[string]any) string {
	t.Helper()
	return artifactCallMsg(t, id, "concept_batch", map[string]any{"operations": operations})
}

// --- WP1: bounded preflight rejects every invalid shape before any write ---

func TestConceptBatch_PreflightRejectsEveryInvalidShape(t *testing.T) {
	t.Parallel()
	k := setupTestKB(t)
	if err := k.CreateMapWithContract("strictmap", "Strict Map", "map",
		[]string{"Runbook"}, kb.MapContract{}); err != nil {
		t.Fatalf("CreateMapWithContract: %v", err)
	}
	if err := k.CreateMapWithContract("contractmap", "Contract Map", "map",
		nil, kb.MapContract{RequiredFields: []string{"owner"}}); err != nil {
		t.Fatalf("CreateMapWithContract: %v", err)
	}
	s := New("test")
	RegisterKBTools(s, k, Deps{})

	tooMany := make([]map[string]any, conceptBatchMaxOps+1)
	for i := range tooMany {
		tooMany[i] = map[string]any{"op": "write", "id": "batch/many-" + strconv.Itoa(i), "frontmatter": map[string]any{"type": "Note"}, "body": "x"}
	}

	bigBody := strings.Repeat("a", conceptBatchMaxTotalBytes/2+100)
	tooBig := []map[string]any{
		{"op": "write", "id": "batch/big-one", "frontmatter": map[string]any{"type": "Note"}, "body": bigBody},
		{"op": "write", "id": "batch/big-two", "frontmatter": map[string]any{"type": "Note"}, "body": bigBody},
	}

	cases := []struct {
		name    string
		ops     []map[string]any
		wantSub string
	}{
		{"empty batch", []map[string]any{}, "cannot be empty"},
		{"too many operations", tooMany, "exceeding the max"},
		{"duplicate id", []map[string]any{
			{"op": "write", "id": "batch/dup", "frontmatter": map[string]any{"type": "Note"}, "body": "x"},
			{"op": "write", "id": "batch/dup", "frontmatter": map[string]any{"type": "Note"}, "body": "y"},
		}, "duplicate id"},
		{"invalid ConceptID", []map[string]any{
			{"op": "write", "id": "../escape", "frontmatter": map[string]any{"type": "Note"}, "body": "x"},
		}, "invalid ConceptID"},
		{"missing op", []map[string]any{
			{"id": "batch/no-op", "frontmatter": map[string]any{"type": "Note"}, "body": "x"},
		}, "'op' is required"},
		{"invalid op value", []map[string]any{
			{"op": "frobnicate", "id": "batch/bad-op", "frontmatter": map[string]any{"type": "Note"}, "body": "x"},
		}, "invalid 'op'"},
		{"write missing frontmatter", []map[string]any{
			{"op": "write", "id": "batch/no-fm", "body": "x"},
		}, "'frontmatter' is required"},
		{"write update without if_match", []map[string]any{
			{"op": "write", "id": "manutenzione/test-runbook", "frontmatter": map[string]any{"type": "Runbook"}, "body": "x"},
		}, "if_match is required"},
		{"write if_match on nonexistent target", []map[string]any{
			{"op": "write", "id": "batch/ghost", "frontmatter": map[string]any{"type": "Note"}, "body": "x", "if_match": "deadbeef"},
		}, "stale_write"},
		{"write wrong if_match", []map[string]any{
			{"op": "write", "id": "manutenzione/test-runbook", "frontmatter": map[string]any{"type": "Runbook"}, "body": "x", "if_match": "wrong"},
		}, "stale_write"},
		{"write missing type", []map[string]any{
			{"op": "write", "id": "batch/no-type", "frontmatter": map[string]any{"title": "No Type"}, "body": "x"},
		}, "type field is required"},
		{"patch missing if_match", []map[string]any{
			{"op": "patch", "id": "manutenzione/test-runbook", "old_string": "x", "new_string": "y"},
		}, "'if_match' is required"},
		{"patch not found", []map[string]any{
			{"op": "patch", "id": "batch/ghost", "if_match": "x", "old_string": "x", "new_string": "y"},
		}, "not found"},
		{"patch wrong if_match", []map[string]any{
			{"op": "patch", "id": "manutenzione/test-runbook", "if_match": "wrong", "old_string": "x", "new_string": "y"},
		}, "stale_write"},
		{"patch missing old_string and edits", func() []map[string]any {
			hash := readHash(t, k, "manutenzione/test-runbook")
			return []map[string]any{{"op": "patch", "id": "manutenzione/test-runbook", "if_match": hash}}
		}(), "'old_string' is required"},
		{"patch empty edits", func() []map[string]any {
			hash := readHash(t, k, "manutenzione/test-runbook")
			return []map[string]any{{"op": "patch", "id": "manutenzione/test-runbook", "if_match": hash, "edits": []map[string]any{}}}
		}(), "cannot be empty"},
		{"patch old_string not found", func() []map[string]any {
			hash := readHash(t, k, "manutenzione/test-runbook")
			return []map[string]any{{"op": "patch", "id": "manutenzione/test-runbook", "if_match": hash, "old_string": "no-such-text", "new_string": "y"}}
		}(), "old_string_not_found"},
		{"strict ontology violation", []map[string]any{
			{"op": "write", "id": "strictmap/bad-type", "frontmatter": map[string]any{"type": "Note"}, "body": "x"},
		}, "not allowed in map"},
		{"missing required field", []map[string]any{
			{"op": "write", "id": "contractmap/no-owner", "frontmatter": map[string]any{"type": "Note"}, "body": "x"},
		}, "missing required field"},
		{"aggregate bytes exceeded", tooBig, "aggregate batch content exceeds"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resps := runMCPSequence(t, s, []string{initMsg, batchCallMsg(t, 2, c.ops)})
			tr := decodeToolResult(t, resps[1])
			if !tr.IsError {
				t.Fatalf("expected an application error, got success: %+v", tr.Content)
			}
			if len(tr.Content) == 0 || !strings.Contains(tr.Content[0].Text, c.wantSub) {
				t.Fatalf("error = %+v, want substring %q", tr.Content, c.wantSub)
			}
		})
	}
}

// TestConceptBatch_PreflightFailureLeavesFilesLogIndexUnchanged proves the
// "reject before touching the filesystem" acceptance criterion (D125 WP1):
// a batch whose second operation is invalid must not create the first
// operation's file, must not touch log.md, and must not touch search.
func TestConceptBatch_PreflightFailureLeavesFilesLogIndexUnchanged(t *testing.T) {
	t.Parallel()
	k := setupTestKB(t)
	s := New("test")
	RegisterKBTools(s, k, Deps{})

	preLog, err := k.ReadRaw("log.md")
	if err != nil {
		t.Fatalf("ReadRaw log.md: %v", err)
	}

	ops := []map[string]any{
		{"op": "write", "id": "batch/would-succeed", "frontmatter": map[string]any{"type": "Note"}, "body": "x"},
		{"op": "write", "id": "manutenzione/test-runbook", "frontmatter": map[string]any{"type": "Runbook"}, "body": "y"}, // missing if_match on existing concept
	}
	resps := runMCPSequence(t, s, []string{initMsg, batchCallMsg(t, 2, ops)})
	tr := decodeToolResult(t, resps[1])
	if !tr.IsError {
		t.Fatalf("expected application error, got success: %+v", tr.Content)
	}

	if _, statErr := os.Stat(filepath.Join(k.DataRoot(), "batch", "would-succeed.md")); !os.IsNotExist(statErr) {
		t.Error("first operation's file must not exist after the second operation's preflight failure")
	}
	postLog, err := k.ReadRaw("log.md")
	if err != nil {
		t.Fatalf("ReadRaw log.md: %v", err)
	}
	if postLog != preLog {
		t.Errorf("log.md changed on preflight failure:\nbefore: %q\nafter:  %q", preLog, postLog)
	}

	searchResp := runMCPSequence(t, s, []string{initMsg, artifactCallMsg(t, 3, "search", map[string]any{"query": "would-succeed"})})
	str := decodeToolResult(t, searchResp[1])
	if strings.Contains(str.Content[0].Text, "batch/would-succeed") {
		t.Error("search index must not reference a concept from a rejected batch")
	}
}

// --- WP2/WP3 integration: happy path, one commit, one log entry, ordering ---

func TestConceptBatch_HappyPath_OneCommitOneLogEntryOrderedResults(t *testing.T) {
	t.Parallel()
	k, sha1 := setupGitKB(t)
	// Seed a concept the patch operation will target.
	fm, _ := okf.ParseFrontmatter("type: Runbook\ntitle: Seed")
	if _, err := k.WriteConcept(okf.ConceptID("batch/seed"), fm, "# Seed\n\nOriginal body.\n", ""); err != nil {
		t.Fatalf("seed write: %v", err)
	}
	if _, err := k.CommitOp("test: seed"); err != nil {
		t.Fatal(err)
	}
	seedHash := readHash(t, k, "batch/seed")

	s := New("test")
	RegisterKBTools(s, k, Deps{})

	ops := []map[string]any{
		{"op": "write", "id": "batch/new-one", "frontmatter": map[string]any{"type": "Note", "title": "New One"}, "body": "# New One\n\n[[batch/seed]]\n"},
		{"op": "patch", "id": "batch/seed", "if_match": seedHash, "old_string": "Original body.", "new_string": "Patched body."},
	}
	resps := runMCPSequence(t, s, []string{initMsg, batchCallMsg(t, 2, ops)})
	tr := decodeToolResult(t, resps[1])
	if tr.IsError {
		t.Fatalf("concept_batch failed: %+v", tr.Content)
	}

	var out struct {
		Results []struct {
			ID          string `json:"id"`
			ContentHash string `json:"content_hash"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(tr.Content[0].Text), &out); err != nil {
		t.Fatalf("decode results: %v", err)
	}
	if len(out.Results) != 2 || out.Results[0].ID != "batch/new-one" || out.Results[1].ID != "batch/seed" {
		t.Fatalf("results out of request order: %+v", out.Results)
	}
	for _, r := range out.Results {
		if r.ContentHash == "" {
			t.Errorf("missing content_hash for %s", r.ID)
		}
	}

	sha2, err := gitx.HeadSHA(k.Root)
	if err != nil {
		t.Fatalf("HeadSHA: %v", err)
	}
	if sha1 == sha2 {
		t.Fatal("expected a new commit after concept_batch")
	}
	count := func() int {
		out, err := exec.Command("git", "-C", k.Root, "rev-list", "--count", "HEAD").Output()
		if err != nil {
			t.Fatal(err)
		}
		var n int
		if _, err := fmt.Sscan(string(out), &n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if got := count(); got != 4 { // init + anchor + seed + batch
		t.Fatalf("commit count = %d, want 4 (one commit for the whole batch)", got)
	}

	logContent, err := k.ReadRaw("log.md")
	if err != nil {
		t.Fatalf("ReadRaw log.md: %v", err)
	}
	if n := strings.Count(logContent, "concept_batch ("); n != 1 {
		t.Errorf("expected exactly one concept_batch summary log entry, got %d in:\n%s", n, logContent)
	}

	patched, err := k.ReadRaw("batch/seed.md")
	if err != nil {
		t.Fatalf("ReadRaw batch/seed.md: %v", err)
	}
	if !strings.Contains(patched, "Patched body.") {
		t.Errorf("patch not applied: %q", patched)
	}

	// The live/SQLite indexes must reflect the batch immediately (D70-style).
	searchResp := runMCPSequence(t, s, []string{initMsg, artifactCallMsg(t, 3, "search", map[string]any{"query": "new-one"})})
	str := decodeToolResult(t, searchResp[1])
	if !strings.Contains(str.Content[0].Text, "batch/new-one") {
		t.Errorf("search does not see the newly written concept: %s", str.Content[0].Text)
	}
}

// --- WP3: transport, authorization, visibility, prefix ---

func TestConceptBatch_StdioRoundTrip(t *testing.T) {
	t.Parallel()
	k := setupTestKB(t)
	s := New("test")
	RegisterKBTools(s, k, Deps{})

	ops := []map[string]any{{"op": "write", "id": "batch/stdio", "frontmatter": map[string]any{"type": "Note"}, "body": "x\n\n[[manutenzione/test-runbook]]\n"}}
	resps := runMCPSequence(t, s, []string{initMsg, batchCallMsg(t, 2, ops)})
	if tr := decodeToolResult(t, resps[1]); tr.IsError {
		t.Fatalf("stdio concept_batch: %+v", tr.Content)
	}
}

func TestConceptBatch_HTTPRoundTrip(t *testing.T) {
	t.Parallel()
	ts := auth.NewScopedTokenStore([]auth.ScopedToken{
		{Token: "rw-tok", Scopes: []auth.KBScope{{KB: "kbx", Write: true}}},
	})
	handler := newScopedTestHandler(t, ts)
	body := batchCallMsg(t, 1, []map[string]any{{"op": "write", "id": "batch/http", "frontmatter": map[string]any{"type": "Note"}, "body": "x\n\n[[manutenzione/test-runbook]]\n"}})

	rr := doMCP(handler, "kbx", "rw-tok", body)
	if rr.Code != http.StatusOK {
		t.Fatalf("HTTP concept_batch: status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var resp Response
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if tr := decodeToolResult(t, resp); tr.IsError {
		t.Fatalf("HTTP concept_batch application error: %+v", tr.Content)
	}
}

func TestConceptBatch_ReadOnlyTokenDenied(t *testing.T) {
	t.Parallel()
	ts := auth.NewScopedTokenStore([]auth.ScopedToken{
		{Token: "r-tok", Scopes: []auth.KBScope{{KB: "kbx", Write: false}}},
	})
	handler := newScopedTestHandler(t, ts)
	body := batchCallMsg(t, 1, []map[string]any{{"op": "write", "id": "batch/denied", "frontmatter": map[string]any{"type": "Note"}, "body": "x"}})

	rr := doMCP(handler, "kbx", "r-tok", body)
	assertMCPForbidden(t, rr)
}

// TestConceptBatch_VisibleUnderAgentAndFullProfile verifies concept_batch is
// advertised in tools/list under both profiles (D318; D125 WP3 had hidden it)
// and callable via tools/call.
func TestConceptBatch_VisibleUnderAgentAndFullProfile(t *testing.T) {
	t.Parallel()
	k := setupTestKB(t)
	s := New("test")
	RegisterKBTools(s, k, Deps{})

	s.SetToolsProfile("agent")
	agentNames := listToolNames(t, s)
	if !agentNames["concept_batch"] {
		t.Error("concept_batch must be advertised in tools/list under the agent profile (D318)")
	}
	ops := []map[string]any{{"op": "write", "id": "batch/agent-profile", "frontmatter": map[string]any{"type": "Note"}, "body": "x\n\n[[manutenzione/test-runbook]]\n"}}
	resps := runMCPSequence(t, s, []string{initMsg, batchCallMsg(t, 2, ops)})
	if tr := decodeToolResult(t, resps[1]); tr.IsError {
		t.Fatalf("concept_batch must stay callable via tools/call under the agent profile: %+v", tr.Content)
	}

	s.SetToolsProfile("full")
	fullNames := listToolNames(t, s)
	if !fullNames["concept_batch"] {
		t.Error("concept_batch must be advertised in tools/list under the full profile")
	}
}

// --- helpers ---

func readHash(t *testing.T, k *kb.KB, id string) string {
	t.Helper()
	data, err := k.ReadConcept(okf.ConceptID(id))
	if err != nil {
		t.Fatalf("ReadConcept(%s): %v", id, err)
	}
	return data.ContentHash
}

// TestConceptPatch_FrontmatterOnly: a frontmatter merge is a complete patch
// on its own, with the edits key absent or empty, in concept_patch and in a
// concept_batch patch operation alike; the body is left byte-for-byte (#321).
func TestConceptPatch_FrontmatterOnly(t *testing.T) {
	t.Parallel()
	// The title is the page's own H1: a write that changed it alone would have
	// the body repaired to match (title_h1_mismatch), and this test is about
	// a patch that touches no body.
	const id = "manutenzione/test-runbook"
	cases := []struct {
		name string
		call func(t *testing.T, k *kb.KB, hash string) string
	}{
		{"concept_patch, no edits key", func(t *testing.T, k *kb.KB, hash string) string {
			return artifactCallMsg(t, 2, "concept_patch", map[string]any{"id": id, "if_match": hash, "frontmatter": map[string]any{"title": "Schema"}})
		}},
		{"concept_patch, empty edits", func(t *testing.T, k *kb.KB, hash string) string {
			return artifactCallMsg(t, 2, "concept_patch", map[string]any{"id": id, "if_match": hash, "edits": []any{}, "frontmatter": map[string]any{"title": "Schema"}})
		}},
		{"concept_batch patch", func(t *testing.T, k *kb.KB, hash string) string {
			return batchCallMsg(t, 2, []map[string]any{{"op": "patch", "id": id, "if_match": hash, "frontmatter": map[string]any{"title": "Schema"}}})
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			k := setupTestKB(t)
			s := New("test")
			RegisterKBTools(s, k, Deps{})
			before, err := k.ReadConcept(id)
			if err != nil {
				t.Fatalf("ReadConcept: %v", err)
			}
			resps := runMCPSequence(t, s, []string{initMsg, tc.call(t, k, readHash(t, k, id))})
			if tr := decodeToolResult(t, resps[1]); tr.IsError {
				t.Fatalf("frontmatter-only patch refused: %s", tr.Content[0].Text)
			}
			after, err := k.ReadConcept(id)
			if err != nil {
				t.Fatalf("ReadConcept: %v", err)
			}
			if after.Body != before.Body {
				t.Errorf("body changed:\nbefore: %q\nafter:  %q", before.Body, after.Body)
			}
			fm, err := okf.ParseFrontmatter(after.FrontmatterRaw)
			if err != nil {
				t.Fatalf("ParseFrontmatter: %v", err)
			}
			if v, _ := fm.Get("title"); v != "Schema" {
				t.Errorf("title = %v, want %q", v, "Schema")
			}
		})
	}
}

// TestConceptBatch_PatchUnsetKey (D309): a batch patch op removes the keys in
// its 'unset' array, with no body edit.
func TestConceptBatch_PatchUnsetKey(t *testing.T) {
	t.Parallel()
	k, _ := setupGitKB(t)
	fm, _ := okf.ParseFrontmatter("type: Note\ntitle: Seed\nstatus: draft")
	if _, err := k.WriteConcept(okf.ConceptID("batch/unset"), fm, "# Seed\n", ""); err != nil {
		t.Fatalf("seed write: %v", err)
	}
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	ops := []map[string]any{
		{"op": "patch", "id": "batch/unset", "if_match": readHash(t, k, "batch/unset"), "unset": []string{"status"}},
	}
	resps := runMCPSequence(t, s, []string{initMsg, batchCallMsg(t, 2, ops)})
	if tr := decodeToolResult(t, resps[1]); tr.IsError {
		t.Fatalf("concept_batch failed: %+v", tr.Content)
	}
	cd, err := k.ReadConcept("batch/unset")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(cd.FrontmatterRaw, "status") || !strings.Contains(cd.FrontmatterRaw, "title: Seed") {
		t.Fatalf("status not removed:\n%s", cd.FrontmatterRaw)
	}
}

// TestConceptBatch_PatchesCountEditsNotBodies (D318): ten one-word patches on
// ~100 KiB concepts total well over the aggregate limit if each counted its
// full post-patch body, and must be accepted: a patch counts what it sends.
func TestConceptBatch_PatchesCountEditsNotBodies(t *testing.T) {
	// Serial on purpose: ten 100 KiB concepts are CPU-heavy (about 20s under
	// -race), and runMCPSequence gives a response 20s, so contention flakes it.
	k := setupTestKB(t)
	s := New("test")
	RegisterKBTools(s, k, Deps{})

	big := "# Big\n\nmarker\n\n" + strings.Repeat("lorem ipsum dolor sit amet\n", 4000)
	if len(big)*10 <= conceptBatchMaxTotalBytes {
		t.Fatal("fixture too small to prove the point")
	}
	var ops []map[string]any
	for i := 0; i < 10; i++ {
		id := "batch/large-" + strconv.Itoa(i)
		fm, _ := okf.ParseFrontmatter("type: Note\ntitle: Large")
		if _, err := k.WriteConcept(okf.ConceptID(id), fm, big, ""); err != nil {
			t.Fatal(err)
		}
		ops = append(ops, map[string]any{"op": "patch", "id": id, "if_match": readHash(t, k, id), "old_string": "marker", "new_string": "done"})
	}
	resps := runMCPSequence(t, s, []string{initMsg, batchCallMsg(t, 2, ops)})
	if tr := decodeToolResult(t, resps[1]); tr.IsError {
		t.Fatalf("small patches on large concepts rejected: %+v", tr.Content)
	}
	if cd, err := k.ReadConcept("batch/large-9"); err != nil || !strings.Contains(cd.Body, "done") {
		t.Fatalf("patch not applied: %v", err)
	}
}

// batchFixtureCall runs one concept_batch against a git KB and returns its result.
func batchFixtureCall(t *testing.T, s *Server, id int, ops []map[string]any) ToolResult {
	t.Helper()
	return decodeToolResult(t, runMCPSequence(t, s, []string{initMsg, batchCallMsg(t, id, ops)})[1])
}

// Two pages that link each other can be deleted in one batch: a link from a page
// deleted in the same batch does not block, and orphan is not judged for it
// (D1327). A third page outside the batch that links to one of them still blocks.
func TestConceptBatch_DeleteClosedGroup(t *testing.T) {
	t.Parallel()
	k, _ := setupGitKB(t)
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	note := func(id, body string) map[string]any {
		return map[string]any{"op": "write", "id": id, "frontmatter": map[string]any{"type": "Note", "title": id}, "body": body}
	}

	// a and b link each other; c links a and b links c, so nothing is an orphan.
	create := batchFixtureCall(t, s, 2, []map[string]any{
		note("closed/a", "# A\n\n[[closed/b]]\n"),
		note("closed/b", "# B\n\n[[closed/a]] [[closed/c]]\n"),
		note("closed/c", "# C\n\n[[closed/a]]\n"),
	})
	if create.IsError {
		t.Fatalf("create: %s", create.Content[0].Text)
	}

	// c is outside the batch and links a: refused, nothing deleted.
	res := batchFixtureCall(t, s, 3, []map[string]any{{"op": "delete", "id": "closed/a"}, {"op": "delete", "id": "closed/b"}})
	if !res.IsError || !strings.Contains(res.Content[0].Text, "inbound_links") || !strings.Contains(res.Content[0].Text, "closed/c") {
		t.Fatalf("a link from outside the batch must block: %+v", res.Content)
	}
	for _, id := range []string{"closed/a", "closed/b", "closed/c"} {
		if _, err := k.ReadConcept(okf.ConceptID(id)); err != nil {
			t.Fatalf("%s must survive a refused batch: %v", id, err)
		}
	}

	// Deleting the whole closed group works, with if_match honoured.
	before, _ := gitx.HeadSHA(k.Root)
	res = batchFixtureCall(t, s, 4, []map[string]any{
		{"op": "delete", "id": "closed/a", "if_match": readHash(t, k, "closed/a")},
		{"op": "delete", "id": "closed/b"},
		{"op": "delete", "id": "closed/c"},
	})
	if res.IsError {
		t.Fatalf("delete of a closed group: %s", res.Content[0].Text)
	}
	var out struct {
		Results []struct {
			ID      string `json:"id"`
			Deleted bool   `json:"deleted"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(res.Content[0].Text), &out); err != nil || len(out.Results) != 3 {
		t.Fatalf("result = %s (%v)", res.Content[0].Text, err)
	}
	for _, r := range out.Results {
		if !r.Deleted {
			t.Errorf("%s not reported deleted", r.ID)
		}
		if _, err := k.ReadConcept(okf.ConceptID(r.ID)); !errors.Is(err, okf.ErrNotFound) {
			t.Errorf("%s still readable: %v", r.ID, err)
		}
	}
	if after, _ := gitx.HeadSHA(k.Root); after == before {
		t.Error("the batch must commit")
	}

	// A stale if_match and an unknown id are refused whole.
	res = batchFixtureCall(t, s, 5, []map[string]any{{"op": "delete", "id": "closed/zzz"}})
	if !res.IsError || !strings.Contains(res.Content[0].Text, "not found") {
		t.Fatalf("unknown id: %+v", res.Content)
	}
}

// A linker the batch patches is judged on its content after the batch: dropping
// the link and deleting the page is one call, keeping the link refuses it, and a
// stale if_match on a delete is refused with nothing deleted (D1327).
func TestConceptBatch_DeleteWithRelinkAndStaleIfMatch(t *testing.T) {
	t.Parallel()
	k, _ := setupGitKB(t)
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	note := func(id, body string) map[string]any {
		return map[string]any{"op": "write", "id": id, "frontmatter": map[string]any{"type": "Note", "title": id}, "body": body}
	}
	// hub links both pages, so only the link from hub to gone is in question.
	if res := batchFixtureCall(t, s, 2, []map[string]any{
		note("rl/hub", "# Hub\n\n[[rl/gone]] [[rl/keep]]\n"),
		note("rl/gone", "# Gone\n\n[[rl/keep]]\n"),
		note("rl/keep", "# Keep\n\n[[rl/hub]]\n"),
	}); res.IsError {
		t.Fatalf("create: %s", res.Content[0].Text)
	}

	// A patch that leaves the link in place does not lift the block.
	res := batchFixtureCall(t, s, 3, []map[string]any{
		{"op": "patch", "id": "rl/hub", "if_match": readHash(t, k, "rl/hub"), "old_string": "[[rl/keep]]", "new_string": "[[rl/keep]] more"},
		{"op": "delete", "id": "rl/gone"},
	})
	if !res.IsError || !strings.Contains(res.Content[0].Text, "inbound_links") || !strings.Contains(res.Content[0].Text, "rl/hub") {
		t.Fatalf("a linker still linking must block: %+v", res.Content)
	}

	// A stale if_match on a delete is refused whole.
	res = batchFixtureCall(t, s, 4, []map[string]any{{"op": "delete", "id": "rl/gone", "if_match": "0000"}})
	if !res.IsError || !strings.Contains(res.Content[0].Text, "stale_write") {
		t.Fatalf("stale delete: %+v", res.Content)
	}
	if _, err := k.ReadConcept("rl/gone"); err != nil {
		t.Fatalf("a refused delete must keep the page: %v", err)
	}

	// Dropping the link in the same batch lets the delete through.
	res = batchFixtureCall(t, s, 5, []map[string]any{
		{"op": "patch", "id": "rl/hub", "if_match": readHash(t, k, "rl/hub"), "old_string": "[[rl/gone]] ", "new_string": ""},
		{"op": "delete", "id": "rl/gone"},
	})
	if res.IsError {
		t.Fatalf("relink plus delete: %s", res.Content[0].Text)
	}
	if _, err := k.ReadConcept("rl/gone"); !errors.Is(err, okf.ErrNotFound) {
		t.Fatalf("rl/gone must be deleted: %v", err)
	}
}
