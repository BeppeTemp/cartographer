package mcpserver

import (
	"encoding/json"
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
	"github.com/BeppeTemp/cartographer/internal/sqlindex"
)

// batchCallMsg builds a tools/call JSON-RPC message for concept_batch with
// the given operations, reusing artifactCallMsg's generic envelope.
func batchCallMsg(t *testing.T, id int, operations []map[string]any) string {
	t.Helper()
	return artifactCallMsg(t, id, "concept_batch", map[string]any{"operations": operations})
}

// --- WP1: bounded preflight rejects every invalid shape before any write ---

func TestConceptBatch_PreflightRejectsEveryInvalidShape(t *testing.T) {
	k := setupTestKB(t)
	if err := k.CreateMapWithContract("strictmap", "Strict Map", "map",
		[]string{"Runbook"}, "strict", kb.MapContract{}); err != nil {
		t.Fatalf("CreateMapWithContract: %v", err)
	}
	if err := k.CreateMapWithContract("contractmap", "Contract Map", "map",
		nil, "", kb.MapContract{RequiredFields: []string{"owner"}}); err != nil {
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
			{"op": "delete", "id": "batch/bad-op", "frontmatter": map[string]any{"type": "Note"}, "body": "x"},
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
	k, sha1 := setupGitKB(t)
	k.AutoCommit = true
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
		{"op": "write", "id": "batch/new-one", "frontmatter": map[string]any{"type": "Note", "title": "New One"}, "body": "# New One\n"},
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
	if got := count(); got != 3 { // init + seed + batch
		t.Fatalf("commit count = %d, want 3 (one commit for the whole batch)", got)
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

// TestConceptBatch_SQLIndexFailureRollsBackFilesLogAndLiveIndex is the
// MCP-layer integration test for D125 WP2's index-failure rollback: closing
// the SQLite index before the call forces sqlIdx.Upsert to fail during
// afterFiles, after every file and the log entry already succeeded.
func TestConceptBatch_SQLIndexFailureRollsBackFilesLogAndLiveIndex(t *testing.T) {
	k := setupTestKB(t)
	dbPath := filepath.Join(t.TempDir(), "index.db")
	sqlIdx, err := sqlindex.Open(dbPath)
	if err != nil {
		t.Skipf("sqlindex.Open: %v (FTS5 likely unavailable)", err)
	}
	s := New("test")
	RegisterKBTools(s, k, Deps{SQLIndex: sqlIdx})
	if err := sqlIdx.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	preLog, err := k.ReadRaw("log.md")
	if err != nil {
		t.Fatalf("ReadRaw log.md: %v", err)
	}

	ops := []map[string]any{
		{"op": "write", "id": "batch/should-roll-back", "frontmatter": map[string]any{"type": "Note"}, "body": "x"},
	}
	resps := runMCPSequence(t, s, []string{initMsg, batchCallMsg(t, 2, ops)})
	tr := decodeToolResult(t, resps[1])
	if !tr.IsError {
		t.Fatalf("expected the closed sqlindex to fail the batch, got success: %+v", tr.Content)
	}

	if _, statErr := os.Stat(filepath.Join(k.DataRoot(), "batch", "should-roll-back.md")); !os.IsNotExist(statErr) {
		t.Error("file committed before the index failure must be rolled back")
	}
	postLog, err := k.ReadRaw("log.md")
	if err != nil {
		t.Fatalf("ReadRaw log.md: %v", err)
	}
	if postLog != preLog {
		t.Error("log.md must be rolled back together with the files")
	}

	searchResp := runMCPSequence(t, s, []string{initMsg, artifactCallMsg(t, 3, "search", map[string]any{"query": "should-roll-back"})})
	str := decodeToolResult(t, searchResp[1])
	if strings.Contains(str.Content[0].Text, "batch/should-roll-back") {
		t.Error("live keyword index must be reconciled back to the pre-batch state")
	}
}

// --- WP3: transport, authorization, visibility, prefix ---

func TestConceptBatch_StdioRoundTrip(t *testing.T) {
	k := setupTestKB(t)
	s := New("test")
	RegisterKBTools(s, k, Deps{})

	ops := []map[string]any{{"op": "write", "id": "batch/stdio", "frontmatter": map[string]any{"type": "Note"}, "body": "x"}}
	resps := runMCPSequence(t, s, []string{initMsg, batchCallMsg(t, 2, ops)})
	if tr := decodeToolResult(t, resps[1]); tr.IsError {
		t.Fatalf("stdio concept_batch: %+v", tr.Content)
	}
}

func TestConceptBatch_HTTPRoundTrip(t *testing.T) {
	ts := auth.NewScopedTokenStore([]auth.ScopedToken{
		{Token: "rw-tok", Scopes: []auth.KBScope{{KB: "kbx", Write: true}}},
	})
	handler := newScopedTestHandler(t, ts)
	body := batchCallMsg(t, 1, []map[string]any{{"op": "write", "id": "batch/http", "frontmatter": map[string]any{"type": "Note"}, "body": "x"}})

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
	ts := auth.NewScopedTokenStore([]auth.ScopedToken{
		{Token: "r-tok", Scopes: []auth.KBScope{{KB: "kbx", Write: false}}},
	})
	handler := newScopedTestHandler(t, ts)
	body := batchCallMsg(t, 1, []map[string]any{{"op": "write", "id": "batch/denied", "frontmatter": map[string]any{"type": "Note"}, "body": "x"}})

	rr := doMCP(handler, "kbx", "r-tok", body)
	assertMCPForbidden(t, rr)
}

// TestConceptBatch_HiddenUnderAgentVisibleUnderFullProfile verifies the
// D65/D123-style profile behavior (D125 WP3): concept_batch is advanced, so
// it must be absent from tools/list under the default "agent" profile and
// present under "full", while remaining callable via tools/call in both.
func TestConceptBatch_HiddenUnderAgentVisibleUnderFullProfile(t *testing.T) {
	k := setupTestKB(t)
	s := New("test")
	RegisterKBTools(s, k, Deps{})

	s.SetToolsProfile("agent")
	agentNames := listToolNames(t, s)
	if agentNames["concept_batch"] {
		t.Error("concept_batch must be hidden from tools/list under the agent profile")
	}
	ops := []map[string]any{{"op": "write", "id": "batch/agent-profile", "frontmatter": map[string]any{"type": "Note"}, "body": "x"}}
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

// TestConceptBatch_PrefixedNaming verifies concept_batch is reachable and
// correctly hidden/visible under a configured multi-KB tool-name prefix
// (D125 WP3), the same shape as TestServer_ToolsProfile_Prefixed.
func TestConceptBatch_PrefixedNaming(t *testing.T) {
	k := setupTestKB(t)
	s := New("test")
	s.SetToolNamePrefix("aiteam")
	RegisterKBTools(s, k, Deps{})
	s.SetToolsProfile("agent")

	resps := runMCPSequence(t, s, []string{toolsListBody})
	names := toolNamesFromToolsList(t, resps[0])
	if _, ok := names["aiteam__concept_batch"]; ok {
		t.Error("aiteam__concept_batch is advanced and must be hidden under the agent profile")
	}

	callResp := runMCPSequence(t, s, []string{initMsg, artifactCallMsg(t, 2, "aiteam__concept_batch", map[string]any{
		"operations": []map[string]any{{"op": "write", "id": "batch/prefixed", "frontmatter": map[string]any{"type": "Note"}, "body": "x"}},
	})})
	if tr := decodeToolResult(t, callResp[1]); tr.IsError {
		t.Fatalf("prefixed concept_batch call: %+v", tr.Content)
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
