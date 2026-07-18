package mcpserver

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/sqlindex"
)

// setupTestKB creates a temporary KB with minimal content for tests.
func setupTestKB(t *testing.T) *kb.KB {
	t.Helper()
	dir, err := os.MkdirTemp("", "wiki-mcp-test-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	k, err := kb.Init(dir)
	if err != nil {
		t.Fatalf("kb.Init: %v", err)
	}

	// Create a test archive and concept under the data root.
	os.MkdirAll(filepath.Join(k.DataRoot(), "manutenzione"), 0o755)
	conceptContent := "---\ntype: Runbook\ntitle: Test Runbook\n---\n# Schema\nIl runbook di test.\n"
	os.WriteFile(filepath.Join(k.DataRoot(), "manutenzione", "test-runbook.md"), []byte(conceptContent), 0o644)

	return k
}

// runMCPSequence feeds the server a sequence of JSON-RPC messages and collects the responses.
// Notifications (messages without an "id" field) are skipped.
func runMCPSequence(t *testing.T, s *Server, messages []string) []Response {
	t.Helper()

	input := strings.Join(messages, "\n") + "\n"
	reader := strings.NewReader(input)

	pr, pw := io.Pipe()
	done := make(chan struct{})
	var responses []Response

	go func() {
		defer close(done)
		dec := json.NewDecoder(pr)
		for {
			var resp Response
			if err := dec.Decode(&resp); err != nil {
				pr.Close()
				return
			}
			// Skip notifications (no id field).
			if len(resp.ID) == 0 {
				continue
			}
			responses = append(responses, resp)
		}
	}()

	s.Run(reader, pw)
	pw.Close()
	<-done

	return responses
}

func TestServer_Initialize(t *testing.T) {
	k := setupTestKB(t)
	s := New("0.1.0-m1")
	RegisterKBTools(s, k, Deps{})

	initMsg := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{}}}`
	resps := runMCPSequence(t, s, []string{initMsg})

	if len(resps) != 1 {
		t.Fatalf("expected 1 response, received %d", len(resps))
	}
	resp := resps[0]
	if resp.Error != nil {
		t.Fatalf("initialize: unexpected error: %v", resp.Error)
	}
	if resp.Result == nil {
		t.Fatal("initialize: result nil")
	}
	resultBytes, _ := json.Marshal(resp.Result)
	var result map[string]interface{}
	json.Unmarshal(resultBytes, &result)

	if _, ok := result["protocolVersion"]; !ok {
		t.Error("initialize: protocolVersion missing in result")
	}
	if info, ok := result["serverInfo"].(map[string]interface{}); !ok || info["name"] != "cartographer" {
		t.Errorf("initialize: unexpected serverInfo: %v", result["serverInfo"])
	}
}

func TestServer_ToolsList(t *testing.T) {
	k := setupTestKB(t)
	s := New("0.1.0-m1")
	RegisterKBTools(s, k, Deps{})

	msgs := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
	}
	resps := runMCPSequence(t, s, msgs)

	if len(resps) != 2 {
		t.Fatalf("expected 2 responses, received %d", len(resps))
	}

	listResp := resps[1]
	if listResp.Error != nil {
		t.Fatalf("tools/list: error: %v", listResp.Error)
	}

	resultBytes, _ := json.Marshal(listResp.Result)
	var result map[string]interface{}
	json.Unmarshal(resultBytes, &result)

	toolsRaw, ok := result["tools"]
	if !ok {
		t.Fatal("tools/list: 'tools' missing in result")
	}
	tools := toolsRaw.([]interface{})
	expectedTools := []string{
		"atlas_overview", "index_get", "concept_read", "log_tail",
		"concept_write", "map_create", "concept_expand", "log_append", "snapshot", "validate",
		"map_list", "graph_neighbors", "search", "index_rebuild",
		"lint", "commit_gate", "gate_check",
	}
	foundTools := map[string]bool{}
	for _, tRaw := range tools {
		tMap := tRaw.(map[string]interface{})
		foundTools[tMap["name"].(string)] = true
	}
	for _, expected := range expectedTools {
		if !foundTools[expected] {
			t.Errorf("tools/list: tool %q not found", expected)
		}
	}

	// D77 WP3: the pre-rename tool names must be gone, with no retrocompat
	// alias (see docs/plans/atlas-hierarchy.md).
	for _, removed := range []string{"kb_overview", "archive_create", "dossier_create", "archive_list", "dossier_list"} {
		if foundTools[removed] {
			t.Errorf("tools/list: legacy tool %q must not be registered (D77 rename, no alias)", removed)
		}
	}
}

// TestServer_ToolsList_ReadOnlyHint verifies that tools/list annotates
// read-only tools (WP2, docs/plans/write-path-latency.md) with
// annotations.readOnlyHint=true, and omits it (or leaves it false) for
// write tools.
func TestServer_ToolsList_ReadOnlyHint(t *testing.T) {
	k := setupTestKB(t)
	s := New("0.1.0-m1")
	RegisterKBTools(s, k, Deps{})

	msgs := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
	}
	resps := runMCPSequence(t, s, msgs)
	if len(resps) != 2 {
		t.Fatalf("expected 2 responses, received %d", len(resps))
	}
	listResp := resps[1]
	if listResp.Error != nil {
		t.Fatalf("tools/list: error: %v", listResp.Error)
	}

	resultBytes, _ := json.Marshal(listResp.Result)
	var result struct {
		Tools []struct {
			Name        string `json:"name"`
			Annotations *struct {
				ReadOnlyHint bool `json:"readOnlyHint"`
			} `json:"annotations"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(resultBytes, &result); err != nil {
		t.Fatalf("tools/list: decode: %v", err)
	}

	byName := map[string]bool{} // name -> readOnlyHint (true only if annotations present and true)
	for _, tool := range result.Tools {
		byName[tool.Name] = tool.Annotations != nil && tool.Annotations.ReadOnlyHint
	}

	for _, name := range []string{"concept_read", "search"} {
		if hint, ok := byName[name]; !ok {
			t.Errorf("tools/list: tool %q not found", name)
		} else if !hint {
			t.Errorf("tools/list: tool %q: expected annotations.readOnlyHint=true", name)
		}
	}
	for _, name := range []string{"concept_write", "concept_patch"} {
		if hint, ok := byName[name]; !ok {
			t.Errorf("tools/list: tool %q not found", name)
		} else if hint {
			t.Errorf("tools/list: tool %q: expected no readOnlyHint=true (write tool)", name)
		}
	}
}

func TestServer_AtlasOverview(t *testing.T) {
	k := setupTestKB(t)
	s := New("0.1.0-m1")
	RegisterKBTools(s, k, Deps{})

	msgs := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"atlas_overview","arguments":{}}}`,
	}
	resps := runMCPSequence(t, s, msgs)

	if len(resps) != 2 {
		t.Fatalf("expected 2 responses, received %d", len(resps))
	}

	callResp := resps[1]
	if callResp.Error != nil {
		t.Fatalf("atlas_overview: RPC error: %v", callResp.Error)
	}

	// Verify the result is a valid ToolResult with content.
	resultBytes, _ := json.Marshal(callResp.Result)
	var tr ToolResult
	if err := json.Unmarshal(resultBytes, &tr); err != nil {
		t.Fatalf("atlas_overview: result is not a ToolResult: %v", err)
	}
	if tr.IsError {
		t.Fatalf("atlas_overview: isError=true: %v", tr.Content)
	}
	if len(tr.Content) == 0 || tr.Content[0].Text == "" {
		t.Fatal("atlas_overview: empty content")
	}
	// setupTestKB creates a flat map ("manutenzione") with a concept file directly
	// inside it (no dossier subdirectory): the overview must report it as a concept,
	// not as 0 dossiers (regression for a flat-KB map being reported as empty).
	if !strings.Contains(tr.Content[0].Text, "**manutenzione** (1 concepts)") {
		t.Fatalf("atlas_overview: expected flat map concept count in output, got: %s", tr.Content[0].Text)
	}
}

func TestServer_Notification(t *testing.T) {
	k := setupTestKB(t)
	s := New("0.1.0-m1")
	RegisterKBTools(s, k, Deps{})

	// Notifications (without id) must not generate a response.
	msgs := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"ping"}`,
	}
	resps := runMCPSequence(t, s, msgs)

	// Must be 2 responses (initialize + ping), not 3.
	if len(resps) != 2 {
		t.Fatalf("expected 2 responses (notification without response), received %d", len(resps))
	}
}

func TestServer_MethodNotFound(t *testing.T) {
	s := New("0.1.0-m1")
	msgs := []string{
		`{"jsonrpc":"2.0","id":1,"method":"nonEsiste","params":{}}`,
	}
	resps := runMCPSequence(t, s, msgs)
	if len(resps) != 1 {
		t.Fatalf("expected 1 response, received %d", len(resps))
	}
	if resps[0].Error == nil || resps[0].Error.Code != ErrCodeMethodNotFound {
		t.Fatalf("expected method not found error: %v", resps[0])
	}
}

// decodeToolResult extracts the ToolResult from an RPC response and decodes it.
func decodeToolResult(t *testing.T, resp Response) ToolResult {
	t.Helper()
	if resp.Error != nil {
		t.Fatalf("unexpected RPC error: %v", resp.Error)
	}
	b, _ := json.Marshal(resp.Result)
	var tr ToolResult
	if err := json.Unmarshal(b, &tr); err != nil {
		t.Fatalf("decode ToolResult: %v", err)
	}
	return tr
}

func TestServer_ConceptWrite_Creazione(t *testing.T) {
	k := setupTestKB(t)
	s := New("0.1.0-m1")
	RegisterKBTools(s, k, Deps{})

	msgs := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"concept_write","arguments":{"id":"note/nuovo-concept","frontmatter":{"type":"Note","title":"Nuovo Concept"},"body":"# Titolo\n\nContenuto di prova.\n"}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"concept_read","arguments":{"id":"note/nuovo-concept"}}}`,
	}
	resps := runMCPSequence(t, s, msgs)

	if len(resps) != 3 {
		t.Fatalf("expected 3 responses, received %d", len(resps))
	}

	// Verify concept_write returns content_hash without errors.
	tr := decodeToolResult(t, resps[1])
	if tr.IsError {
		t.Fatalf("concept_write: isError=true: %v", tr.Content)
	}
	if !strings.Contains(tr.Content[0].Text, "content_hash") {
		t.Errorf("concept_write: response does not contain content_hash: %s", tr.Content[0].Text)
	}

	// Verify the concept is readable.
	tr2 := decodeToolResult(t, resps[2])
	if tr2.IsError {
		t.Fatalf("concept_read: isError=true: %v", tr2.Content)
	}
	if !strings.Contains(tr2.Content[0].Text, "nuovo-concept") {
		t.Errorf("concept_read: response does not contain expected id: %s", tr2.Content[0].Text)
	}
}

// --- concept_read: outline / section-not-found / size guard (D78) ---

func TestServer_ConceptRead_Outline(t *testing.T) {
	k := setupTestKB(t)
	s := New("0.1.0-m1")
	RegisterKBTools(s, k, Deps{})

	body := "# Uno\n\nContenuto uno.\n\n## Sotto\n\nDettaglio.\n\n# Due\n\nContenuto due.\n"
	msgs := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"concept_write","arguments":{"id":"note/outline-test","frontmatter":{"type":"Note","title":"Outline"},"body":%q}}}`, body),
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"concept_read","arguments":{"id":"note/outline-test","outline":true}}}`,
	}
	resps := runMCPSequence(t, s, msgs)
	if len(resps) != 3 {
		t.Fatalf("expected 3 responses, received %d", len(resps))
	}
	if tr := decodeToolResult(t, resps[1]); tr.IsError {
		t.Fatalf("concept_write: isError=true: %v", tr.Content)
	}

	tr := decodeToolResult(t, resps[2])
	if tr.IsError {
		t.Fatalf("concept_read outline: isError=true: %v", tr.Content)
	}
	text := tr.Content[0].Text
	for _, want := range []string{"\"outline\"", "\"Uno\"", "\"Sotto\"", "\"Due\"", "\"body_bytes\""} {
		if !strings.Contains(text, want) {
			t.Errorf("concept_read outline: expected %s in response: %s", want, text)
		}
	}
	if strings.Contains(text, "\"content\"") {
		t.Errorf("concept_read outline: must not include full content: %s", text)
	}
}

func TestServer_ConceptRead_SectionNotFound_ListsHeadings(t *testing.T) {
	k := setupTestKB(t)
	s := New("0.1.0-m1")
	RegisterKBTools(s, k, Deps{})

	body := "# Uno\n\nContenuto uno.\n\n# Due\n\nContenuto due.\n"
	msgs := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"concept_write","arguments":{"id":"note/section-test","frontmatter":{"type":"Note","title":"Section"},"body":%q}}}`, body),
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"concept_read","arguments":{"id":"note/section-test","section":"# NonEsiste"}}}`,
	}
	resps := runMCPSequence(t, s, msgs)
	if len(resps) != 3 {
		t.Fatalf("expected 3 responses, received %d", len(resps))
	}
	if tr := decodeToolResult(t, resps[1]); tr.IsError {
		t.Fatalf("concept_write: isError=true: %v", tr.Content)
	}

	tr := decodeToolResult(t, resps[2])
	if !tr.IsError {
		t.Fatal("concept_read section not found: expected isError=true")
	}
	errText := tr.Content[0].Text
	if !strings.Contains(errText, "# Uno") || !strings.Contains(errText, "# Due") {
		t.Errorf("concept_read section not found: error must list available headings, got: %s", errText)
	}
}

func TestServer_ConceptRead_SizeGuard_OutlineThenFull(t *testing.T) {
	k := setupTestKB(t)
	s := New("0.1.0-m1")
	RegisterKBTools(s, k, Deps{})

	big := "# Grande\n\n" + strings.Repeat("a", conceptReadSizeGuard+1) + "\n"
	msgs := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"concept_write","arguments":{"id":"note/big","frontmatter":{"type":"Note","title":"Big"},"body":%q}}}`, big),
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"concept_read","arguments":{"id":"note/big"}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"concept_read","arguments":{"id":"note/big","full":true}}}`,
	}
	resps := runMCPSequence(t, s, msgs)
	if len(resps) != 4 {
		t.Fatalf("expected 4 responses, received %d", len(resps))
	}
	if tr := decodeToolResult(t, resps[1]); tr.IsError {
		t.Fatalf("concept_write: isError=true: %v", tr.Content)
	}

	trGuarded := decodeToolResult(t, resps[2])
	if trGuarded.IsError {
		t.Fatalf("concept_read (guarded): isError=true: %v", trGuarded.Content)
	}
	guardedText := trGuarded.Content[0].Text
	if !strings.Contains(guardedText, "\"outline\"") || !strings.Contains(guardedText, "\"note\"") {
		t.Errorf("concept_read over size guard: expected outline+note, got: %s", guardedText)
	}
	if strings.Contains(guardedText, strings.Repeat("a", 100)) {
		t.Errorf("concept_read over size guard: must not include the full body")
	}

	trFull := decodeToolResult(t, resps[3])
	if trFull.IsError {
		t.Fatalf("concept_read full=true: isError=true: %v", trFull.Content)
	}
	if !strings.Contains(trFull.Content[0].Text, strings.Repeat("a", 100)) {
		t.Errorf("concept_read full=true: expected full body content")
	}
}

func TestServer_ConceptWrite_StaleWrite(t *testing.T) {
	k := setupTestKB(t)
	s := New("0.1.0-m1")
	RegisterKBTools(s, k, Deps{})

	msgs := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		// First write without if_match.
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"concept_write","arguments":{"id":"note/concept-stale","frontmatter":{"type":"Note"},"body":"# Corpo\n"}}}`,
		// Second write with wrong if_match.
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"concept_write","arguments":{"id":"note/concept-stale","frontmatter":{"type":"Note"},"body":"# Aggiornato\n","if_match":"hash-sbagliato-xyz"}}}`,
	}
	resps := runMCPSequence(t, s, msgs)

	if len(resps) != 3 {
		t.Fatalf("expected 3 responses, received %d", len(resps))
	}

	// Verify first write OK.
	tr := decodeToolResult(t, resps[1])
	if tr.IsError {
		t.Fatalf("first concept_write: unexpected isError=true: %v", tr.Content)
	}

	// Verify second write fails with stale_write.
	tr2 := decodeToolResult(t, resps[2])
	if !tr2.IsError {
		t.Fatal("concept_write with wrong if_match: expected isError=true")
	}
	if !strings.Contains(tr2.Content[0].Text, "stale_write") {
		t.Errorf("concept_write: message does not contain 'stale_write': %s", tr2.Content[0].Text)
	}
}

func TestServer_ConceptDelete(t *testing.T) {
	k := setupTestKB(t)
	s := New("0.1.0-m1")
	RegisterKBTools(s, k, Deps{})

	msgs := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"concept_write","arguments":{"id":"note/da-cancellare","frontmatter":{"type":"Note","title":"Da Cancellare"},"body":"# Corpo\n"}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"concept_delete","arguments":{"id":"note/da-cancellare"}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"concept_read","arguments":{"id":"note/da-cancellare"}}}`,
	}
	resps := runMCPSequence(t, s, msgs)

	if len(resps) != 4 {
		t.Fatalf("expected 4 responses, received %d", len(resps))
	}

	if tr := decodeToolResult(t, resps[1]); tr.IsError {
		t.Fatalf("concept_write: unexpected isError=true: %v", tr.Content)
	}

	trDelete := decodeToolResult(t, resps[2])
	if trDelete.IsError {
		t.Fatalf("concept_delete: unexpected isError=true: %v", trDelete.Content)
	}
	if !strings.Contains(trDelete.Content[0].Text, "deleted note/da-cancellare") {
		t.Errorf("concept_delete: unexpected message: %s", trDelete.Content[0].Text)
	}
	if !strings.Contains(trDelete.Content[0].Text, "Warning") {
		t.Errorf("concept_delete: expected inbound-link warning: %s", trDelete.Content[0].Text)
	}

	trRead := decodeToolResult(t, resps[3])
	if !trRead.IsError {
		t.Fatal("concept_read after concept_delete: expected isError=true (concept removed)")
	}

	if _, err := os.Stat(filepath.Join(k.DataRoot(), "note", "da-cancellare.md")); !os.IsNotExist(err) {
		t.Errorf("concept_delete: file still present on disk, err=%v", err)
	}
}

func TestServer_ConceptDelete_StaleWrite(t *testing.T) {
	k := setupTestKB(t)
	s := New("0.1.0-m1")
	RegisterKBTools(s, k, Deps{})

	msgs := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"concept_write","arguments":{"id":"note/stale-delete","frontmatter":{"type":"Note"},"body":"# Corpo\n"}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"concept_delete","arguments":{"id":"note/stale-delete","if_match":"hash-sbagliato-xyz"}}}`,
	}
	resps := runMCPSequence(t, s, msgs)

	if len(resps) != 3 {
		t.Fatalf("expected 3 responses, received %d", len(resps))
	}

	if tr := decodeToolResult(t, resps[1]); tr.IsError {
		t.Fatalf("concept_write: unexpected isError=true: %v", tr.Content)
	}

	trDelete := decodeToolResult(t, resps[2])
	if !trDelete.IsError {
		t.Fatal("concept_delete with wrong if_match: expected isError=true")
	}
	if !strings.Contains(trDelete.Content[0].Text, "stale_write") {
		t.Errorf("concept_delete: message does not contain 'stale_write': %s", trDelete.Content[0].Text)
	}

	if _, err := os.Stat(filepath.Join(k.DataRoot(), "note", "stale-delete.md")); err != nil {
		t.Errorf("concept_delete: file should still be present after stale_write rejection, err=%v", err)
	}
}

func TestServer_ConceptDelete_ReservedAndNotFound(t *testing.T) {
	k := setupTestKB(t)
	s := New("0.1.0-m1")
	RegisterKBTools(s, k, Deps{})

	msgs := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"concept_delete","arguments":{"id":"index"}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"concept_delete","arguments":{"id":"note/non-esiste"}}}`,
	}
	resps := runMCPSequence(t, s, msgs)

	if len(resps) != 3 {
		t.Fatalf("expected 3 responses, received %d", len(resps))
	}

	trReserved := decodeToolResult(t, resps[1])
	if !trReserved.IsError {
		t.Fatal("concept_delete on reserved file (index): expected isError=true")
	}

	trNotFound := decodeToolResult(t, resps[2])
	if !trNotFound.IsError {
		t.Fatal("concept_delete on nonexistent concept: expected isError=true")
	}
	if !strings.Contains(trNotFound.Content[0].Text, "not found") {
		t.Errorf("concept_delete: expected 'not found' message: %s", trNotFound.Content[0].Text)
	}
}

func TestServer_MapCreate(t *testing.T) {
	k := setupTestKB(t)
	s := New("0.1.0-m1")
	RegisterKBTools(s, k, Deps{})

	msgs := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"map_create","arguments":{"name":"test-map","title":"Test Map","kind":"map"}}}`,
	}
	resps := runMCPSequence(t, s, msgs)

	if len(resps) != 2 {
		t.Fatalf("expected 2 responses, received %d", len(resps))
	}

	tr := decodeToolResult(t, resps[1])
	if tr.IsError {
		t.Fatalf("map_create: isError=true: %v", tr.Content)
	}
	if !strings.Contains(tr.Content[0].Text, "created") {
		t.Errorf("map_create: response does not contain 'created': %s", tr.Content[0].Text)
	}
}

func TestServer_MapCreate_Journal(t *testing.T) {
	k := setupTestKB(t)
	s := New("0.1.0-m1")
	RegisterKBTools(s, k, Deps{})

	msgs := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"map_create","arguments":{"name":"incidents","title":"Incidents","kind":"journal"}}}`,
	}
	resps := runMCPSequence(t, s, msgs)

	if len(resps) != 2 {
		t.Fatalf("expected 2 responses, received %d", len(resps))
	}

	tr := decodeToolResult(t, resps[1])
	if tr.IsError {
		t.Fatalf("map_create: isError=true: %v", tr.Content)
	}
	content, err := k.ReadRaw("incidents/_map.md")
	if err != nil {
		t.Fatalf("ReadRaw incidents/_map.md: %v", err)
	}
	if !strings.Contains(content, "kind: journal") {
		t.Errorf("map_create: expected kind: journal in _map.md, got: %q", content)
	}
}

func TestServer_ConceptExpand(t *testing.T) {
	k := setupTestKB(t)
	s := New("0.1.0-m1")
	RegisterKBTools(s, k, Deps{})

	msgs := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"concept_write","arguments":{"id":"manutenzione/rotazione-cert","frontmatter":{"type":"note","title":"Rotazione Cert"},"body":"Body.\n"}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"concept_expand","arguments":{"id":"manutenzione/rotazione-cert"}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"concept_read","arguments":{"id":"manutenzione/rotazione-cert"}}}`,
	}
	resps := runMCPSequence(t, s, msgs)

	if len(resps) != 4 {
		t.Fatalf("expected 4 responses, received %d", len(resps))
	}

	trExpand := decodeToolResult(t, resps[2])
	if trExpand.IsError {
		t.Fatalf("concept_expand: isError=true: %v", trExpand.Content)
	}
	if !strings.Contains(trExpand.Content[0].Text, "expanded") {
		t.Errorf("concept_expand: expected 'expanded' in response: %s", trExpand.Content[0].Text)
	}

	trRead := decodeToolResult(t, resps[3])
	if trRead.IsError {
		t.Fatalf("concept_read after expand: isError=true: %v", trRead.Content)
	}
	if !strings.Contains(trRead.Content[0].Text, "Rotazione Cert") {
		t.Errorf("concept_read after expand: content not preserved: %s", trRead.Content[0].Text)
	}

	if _, err := k.ReadRaw("manutenzione/rotazione-cert/index.md"); err != nil {
		t.Errorf("expected manutenzione/rotazione-cert/index.md to exist: %v", err)
	}
}

func TestServer_ConceptExpand_AlreadyExpanded(t *testing.T) {
	k := setupTestKB(t)
	s := New("0.1.0-m1")
	RegisterKBTools(s, k, Deps{})

	msgs := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"concept_write","arguments":{"id":"manutenzione/rotazione-cert","frontmatter":{"type":"note","title":"Rotazione Cert"},"body":"Body.\n"}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"concept_expand","arguments":{"id":"manutenzione/rotazione-cert"}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"concept_expand","arguments":{"id":"manutenzione/rotazione-cert"}}}`,
	}
	resps := runMCPSequence(t, s, msgs)

	if len(resps) != 4 {
		t.Fatalf("expected 4 responses, received %d", len(resps))
	}
	tr := decodeToolResult(t, resps[3])
	if !tr.IsError {
		t.Fatal("concept_expand on an already-expanded concept: expected isError=true")
	}
	if !strings.Contains(tr.Content[0].Text, "already_expanded") {
		t.Errorf("concept_expand: expected 'already_expanded': %s", tr.Content[0].Text)
	}
}

func TestServer_ConceptExpand_Child_Error(t *testing.T) {
	k := setupTestKB(t)
	s := New("0.1.0-m1")
	RegisterKBTools(s, k, Deps{})

	msgs := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"concept_write","arguments":{"id":"manutenzione/rotazione-cert/step1","frontmatter":{"type":"note","title":"Step 1"},"body":"Body.\n"}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"concept_expand","arguments":{"id":"manutenzione/rotazione-cert/step1"}}}`,
	}
	resps := runMCPSequence(t, s, msgs)

	if len(resps) != 3 {
		t.Fatalf("expected 3 responses, received %d", len(resps))
	}
	tr := decodeToolResult(t, resps[2])
	if !tr.IsError {
		t.Fatal("concept_expand on a 3-segment (child) id: expected isError=true")
	}
}

func TestServer_LogAppend(t *testing.T) {
	k := setupTestKB(t)
	s := New("0.1.0-m1")
	RegisterKBTools(s, k, Deps{})

	msgs := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"log_append","arguments":{"entry":"voce-di-test-univoca-123"}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"log_tail","arguments":{"n":5}}}`,
	}
	resps := runMCPSequence(t, s, msgs)

	if len(resps) != 3 {
		t.Fatalf("expected 3 responses, received %d", len(resps))
	}

	tr := decodeToolResult(t, resps[1])
	if tr.IsError {
		t.Fatalf("log_append: isError=true: %v", tr.Content)
	}

	// Verify log_tail contains the just-inserted entry.
	tr2 := decodeToolResult(t, resps[2])
	if tr2.IsError {
		t.Fatalf("log_tail: isError=true: %v", tr2.Content)
	}
	if !strings.Contains(tr2.Content[0].Text, "voce-di-test-univoca-123") {
		t.Errorf("log_tail: entry not found in log: %s", tr2.Content[0].Text)
	}
}

func TestServer_LogTail_PathPrefixedEntries(t *testing.T) {
	k := setupTestKB(t)
	s := New("0.1.0-m1")
	RegisterKBTools(s, k, Deps{})

	msgs := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"log_append","arguments":{"entry":"voce-manutenzione-univoca","path":"manutenzione"}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"log_tail","arguments":{"path":"manutenzione","n":5}}}`,
	}
	resps := runMCPSequence(t, s, msgs)
	if len(resps) != 3 {
		t.Fatalf("expected 3 responses, received %d", len(resps))
	}
	if tr := decodeToolResult(t, resps[1]); tr.IsError {
		t.Fatalf("log_append: isError=true: %v", tr.Content)
	}

	tr := decodeToolResult(t, resps[2])
	if tr.IsError {
		t.Fatalf("log_tail: isError=true: %v", tr.Content)
	}
	if !strings.Contains(tr.Content[0].Text, "voce-manutenzione-univoca") {
		t.Errorf("log_tail(path=manutenzione): entry not found: %s", tr.Content[0].Text)
	}
}

func TestServer_LogTail_EmptyReturnsNote(t *testing.T) {
	k := setupTestKB(t)
	s := New("0.1.0-m1")
	RegisterKBTools(s, k, Deps{})

	msgs := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"log_tail","arguments":{"path":"nessuna-voce-qui"}}}`,
	}
	resps := runMCPSequence(t, s, msgs)
	if len(resps) != 2 {
		t.Fatalf("expected 2 responses, received %d", len(resps))
	}

	tr := decodeToolResult(t, resps[1])
	if tr.IsError {
		t.Fatalf("log_tail: isError=true: %v", tr.Content)
	}
	text := tr.Content[0].Text
	if !strings.Contains(text, "\"entries\": 0") || !strings.Contains(text, "\"note\"") {
		t.Errorf("log_tail without entries: expected a JSON note, got: %s", text)
	}
}

func TestServer_Validate(t *testing.T) {
	k := setupTestKB(t)
	// Manually write a concept without the required type field.
	badContent := "---\ntitle: Senza Tipo\n---\n# Corpo\n"
	os.WriteFile(filepath.Join(k.DataRoot(), "manutenzione", "senza-tipo.md"), []byte(badContent), 0o644)

	s := New("0.1.0-m1")
	RegisterKBTools(s, k, Deps{})

	msgs := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"validate","arguments":{}}}`,
	}
	resps := runMCPSequence(t, s, msgs)

	if len(resps) != 2 {
		t.Fatalf("expected 2 responses, received %d", len(resps))
	}

	// validate always returns textResult (not errorResult) — validation errors
	// are application results, not tool errors.
	tr := decodeToolResult(t, resps[1])
	if tr.IsError {
		t.Fatal("validate: expected isError=false (validation errors are application results)")
	}
	if !strings.Contains(tr.Content[0].Text, "senza-tipo.md") {
		t.Errorf("validate: output does not contain expected error: %s", tr.Content[0].Text)
	}
}

func TestServer_Search(t *testing.T) {
	k := setupTestKB(t)
	s := New("0.3.0-m3")
	RegisterKBTools(s, k, Deps{})

	msgs := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"search","arguments":{"query":"runbook"}}}`,
	}
	resps := runMCPSequence(t, s, msgs)

	if len(resps) != 2 {
		t.Fatalf("expected 2 responses, received %d", len(resps))
	}

	tr := decodeToolResult(t, resps[1])
	if tr.IsError {
		t.Fatalf("search: isError=true: %v", tr.Content)
	}
	if !strings.Contains(tr.Content[0].Text, "test-runbook") {
		t.Errorf("search: expected 'test-runbook' in results: %s", tr.Content[0].Text)
	}
}

func TestServer_SearchNoResults(t *testing.T) {
	k := setupTestKB(t)
	s := New("0.3.0-m3")
	RegisterKBTools(s, k, Deps{})

	msgs := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"search","arguments":{"query":"nonexistentkeyword"}}}`,
	}
	resps := runMCPSequence(t, s, msgs)

	tr := decodeToolResult(t, resps[1])
	if tr.IsError {
		t.Fatalf("search: isError=true: %v", tr.Content)
	}
	if !strings.Contains(tr.Content[0].Text, `"count": 0`) {
		t.Errorf("search: expected count 0: %s", tr.Content[0].Text)
	}
}

func TestServer_SearchScope(t *testing.T) {
	k := setupTestKB(t)
	// Add a concept outside the manutenzione archive.
	os.MkdirAll(filepath.Join(k.DataRoot(), "notes"), 0o755)
	os.WriteFile(filepath.Join(k.DataRoot(), "notes", "test-runbook.md"),
		[]byte("---\ntype: Note\n---\n# Another Runbook\n"), 0o644)

	s := New("0.3.0-m3")
	RegisterKBTools(s, k, Deps{})

	msgs := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"search","arguments":{"query":"runbook","scope":"manutenzione/"}}}`,
	}
	resps := runMCPSequence(t, s, msgs)

	tr := decodeToolResult(t, resps[1])
	if tr.IsError {
		t.Fatalf("search scope: isError=true: %v", tr.Content)
	}
	if !strings.Contains(tr.Content[0].Text, "manutenzione/test-runbook") {
		t.Errorf("search scope: expected manutenzione hit: %s", tr.Content[0].Text)
	}
	if strings.Contains(tr.Content[0].Text, "notes/test-runbook") {
		t.Errorf("search scope: should NOT contain notes/ hit: %s", tr.Content[0].Text)
	}
}

func TestServer_IndexRebuild(t *testing.T) {
	k := setupTestKB(t)
	s := New("0.3.0-m3")
	RegisterKBTools(s, k, Deps{})

	// Add a new concept after initial index build.
	os.WriteFile(filepath.Join(k.DataRoot(), "manutenzione", "new-concept.md"),
		[]byte("---\ntype: Note\n---\n# Fresh Concept\nkeyword42\n"), 0o644)

	msgs := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		// Before rebuild: should NOT find the new concept.
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"search","arguments":{"query":"keyword42"}}}`,
		// Rebuild.
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"index_rebuild","arguments":{}}}`,
		// After rebuild: should find the new concept.
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"search","arguments":{"query":"keyword42"}}}`,
	}
	resps := runMCPSequence(t, s, msgs)

	if len(resps) != 4 {
		t.Fatalf("expected 4 responses, received %d", len(resps))
	}

	// Before rebuild: no results.
	tr1 := decodeToolResult(t, resps[1])
	if tr1.IsError {
		t.Fatalf("search before rebuild: isError=true: %v", tr1.Content)
	}
	if !strings.Contains(tr1.Content[0].Text, `"count": 0`) {
		t.Errorf("search before rebuild: expected 0 results: %s", tr1.Content[0].Text)
	}

	// Rebuild OK.
	tr2 := decodeToolResult(t, resps[2])
	if tr2.IsError {
		t.Fatalf("index_rebuild: isError=true: %v", tr2.Content)
	}
	if !strings.Contains(tr2.Content[0].Text, "rebuilt") {
		t.Errorf("index_rebuild: expected 'rebuilt': %s", tr2.Content[0].Text)
	}

	// After rebuild: found.
	tr3 := decodeToolResult(t, resps[3])
	if tr3.IsError {
		t.Fatalf("search after rebuild: isError=true: %v", tr3.Content)
	}
	if !strings.Contains(tr3.Content[0].Text, "new-concept") {
		t.Errorf("search after rebuild: expected 'new-concept': %s", tr3.Content[0].Text)
	}
}

func TestServer_MapList(t *testing.T) {
	k := setupTestKB(t)
	s := New("0.3.0-m3")
	RegisterKBTools(s, k, Deps{})

	msgs := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"map_create","arguments":{"name":"test-map","title":"Test Map","kind":"journal","ontology_mode":"strict","concept_types":["Runbook","Note"]}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"map_list","arguments":{}}}`,
	}
	resps := runMCPSequence(t, s, msgs)

	if len(resps) != 3 {
		t.Fatalf("expected 3 responses, received %d", len(resps))
	}

	tr := decodeToolResult(t, resps[2])
	if tr.IsError {
		t.Fatalf("map_list: isError=true: %v", tr.Content)
	}
	if !strings.Contains(tr.Content[0].Text, "test-map") {
		t.Errorf("map_list: expected 'test-map': %s", tr.Content[0].Text)
	}
	if !strings.Contains(tr.Content[0].Text, "Test Map") {
		t.Errorf("map_list: expected title 'Test Map': %s", tr.Content[0].Text)
	}
	if !strings.Contains(tr.Content[0].Text, `"kind": "journal"`) {
		t.Errorf("map_list: expected kind 'journal' in output: %s", tr.Content[0].Text)
	}
}

func TestServer_ConceptList(t *testing.T) {
	k := setupTestKB(t)
	// setupTestKB already provides manutenzione/test-runbook (type Runbook, title "Test Runbook").
	os.MkdirAll(filepath.Join(k.DataRoot(), "entities"), 0o755)
	os.WriteFile(filepath.Join(k.DataRoot(), "entities", "bar.md"),
		[]byte("---\ntype: Entity\ntitle: Bar\n---\nBar entity.\n"), 0o644)
	os.WriteFile(filepath.Join(k.DataRoot(), "entities", "foo.md"),
		[]byte("---\ntype: Entity\ntitle: Foo\n---\nFoo entity.\n"), 0o644)
	os.MkdirAll(filepath.Join(k.DataRoot(), "topics"), 0o755)
	os.WriteFile(filepath.Join(k.DataRoot(), "topics", "baz.md"),
		[]byte("---\ntype: Topic\ntitle: Baz\n---\nBaz topic.\n"), 0o644)

	s := New("0.3.0-m3")
	RegisterKBTools(s, k, Deps{})

	type entry struct {
		ID    string `json:"id"`
		Title string `json:"title"`
		Type  string `json:"type"`
	}
	type listResult struct {
		Count     int     `json:"count"`
		Results   []entry `json:"results"`
		Truncated bool    `json:"truncated"`
		Total     int     `json:"total"`
	}
	decode := func(t *testing.T, resp Response) listResult {
		t.Helper()
		tr := decodeToolResult(t, resp)
		if tr.IsError {
			t.Fatalf("concept_list: isError=true: %v", tr.Content)
		}
		var lr listResult
		if err := json.Unmarshal([]byte(tr.Content[0].Text), &lr); err != nil {
			t.Fatalf("concept_list: invalid JSON result: %v; text=%s", err, tr.Content[0].Text)
		}
		return lr
	}

	// Full list, no scope.
	msgs := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"concept_list","arguments":{}}}`,
	}
	resps := runMCPSequence(t, s, msgs)
	lr := decode(t, resps[1])
	if lr.Count != 4 || len(lr.Results) != 4 {
		t.Fatalf("concept_list no scope: expected 4 results, got count=%d len=%d", lr.Count, len(lr.Results))
	}
	wantOrder := []string{"entities/bar", "entities/foo", "manutenzione/test-runbook", "topics/baz"}
	for i, id := range wantOrder {
		if lr.Results[i].ID != id {
			t.Errorf("concept_list no scope: result[%d].ID = %q, want %q (order: %v)", i, lr.Results[i].ID, id, lr.Results)
		}
	}
	// title/type from frontmatter.
	for _, e := range lr.Results {
		if e.ID == "entities/foo" && (e.Title != "Foo" || e.Type != "Entity") {
			t.Errorf("concept_list: entities/foo title/type = %q/%q, want Foo/Entity", e.Title, e.Type)
		}
		if e.ID == "manutenzione/test-runbook" && (e.Title != "Test Runbook" || e.Type != "Runbook") {
			t.Errorf("concept_list: manutenzione/test-runbook title/type = %q/%q, want \"Test Runbook\"/Runbook", e.Title, e.Type)
		}
	}

	// Scope prefix.
	msgs = []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"concept_list","arguments":{"scope":"entities/"}}}`,
	}
	resps = runMCPSequence(t, s, msgs)
	lr = decode(t, resps[1])
	if lr.Count != 2 {
		t.Fatalf("concept_list scope=entities/: expected 2 results, got %d (%v)", lr.Count, lr.Results)
	}
	for _, e := range lr.Results {
		if !strings.HasPrefix(e.ID, "entities/") {
			t.Errorf("concept_list scope=entities/: unexpected id %q outside scope", e.ID)
		}
	}

	// Limit with truncation.
	msgs = []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"concept_list","arguments":{"scope":"entities/","limit":1}}}`,
	}
	resps = runMCPSequence(t, s, msgs)
	lr = decode(t, resps[1])
	if lr.Count != 1 || len(lr.Results) != 1 {
		t.Fatalf("concept_list limit=1: expected 1 result, got count=%d len=%d", lr.Count, len(lr.Results))
	}
	if !lr.Truncated {
		t.Errorf("concept_list limit=1: expected truncated=true")
	}
	if lr.Total != 2 {
		t.Errorf("concept_list limit=1: expected total=2, got %d", lr.Total)
	}
	if lr.Results[0].ID != "entities/bar" {
		t.Errorf("concept_list limit=1: expected first result entities/bar (sorted), got %q", lr.Results[0].ID)
	}
}

func TestServer_GraphNeighbors(t *testing.T) {
	k := setupTestKB(t)
	// Create linked concepts.
	os.MkdirAll(filepath.Join(k.DataRoot(), "manutenzione"), 0o755)
	os.WriteFile(filepath.Join(k.DataRoot(), "manutenzione", "a.md"),
		[]byte("---\ntype: Note\n---\nSee [b](b.md) and [test-runbook](test-runbook.md).\n"), 0o644)

	s := New("0.3.0-m3")
	RegisterKBTools(s, k, Deps{})

	msgs := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"graph_neighbors","arguments":{"id":"manutenzione/a"}}}`,
	}
	resps := runMCPSequence(t, s, msgs)

	if len(resps) != 2 {
		t.Fatalf("expected 2 responses, received %d", len(resps))
	}

	tr := decodeToolResult(t, resps[1])
	if tr.IsError {
		t.Fatalf("graph_neighbors: isError=true: %v", tr.Content)
	}
	if !strings.Contains(tr.Content[0].Text, "manutenzione/b") {
		t.Errorf("graph_neighbors: expected 'manutenzione/b': %s", tr.Content[0].Text)
	}
	if !strings.Contains(tr.Content[0].Text, "manutenzione/test-runbook") {
		t.Errorf("graph_neighbors: expected 'manutenzione/test-runbook': %s", tr.Content[0].Text)
	}
}

func TestServer_Lint_BrokenLink(t *testing.T) {
	k := setupTestKB(t)
	os.WriteFile(filepath.Join(k.DataRoot(), "manutenzione", "with-broken-link.md"),
		[]byte("---\ntype: Note\n---\nSee [missing](nonexistent.md).\n"), 0o644)

	s := New("0.4.0-m4")
	RegisterKBTools(s, k, Deps{})

	msgs := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"lint","arguments":{}}}`,
	}
	resps := runMCPSequence(t, s, msgs)

	tr := decodeToolResult(t, resps[1])
	if tr.IsError {
		t.Fatalf("lint: isError=true: %v", tr.Content)
	}
	if !strings.Contains(tr.Content[0].Text, "broken_link") {
		t.Errorf("lint: expected broken_link finding: %s", tr.Content[0].Text)
	}
}

func TestServer_Lint_Clean(t *testing.T) {
	k := setupTestKB(t)
	s := New("0.4.0-m4")
	RegisterKBTools(s, k, Deps{})

	msgs := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"lint","arguments":{"scope":"manutenzione"}}}`,
	}
	resps := runMCPSequence(t, s, msgs)

	tr := decodeToolResult(t, resps[1])
	if tr.IsError {
		t.Fatalf("lint clean: isError=true: %v", tr.Content)
	}
}

func TestServer_CommitGate_Pass(t *testing.T) {
	k := setupTestKB(t)
	s := New("0.4.0-m4")
	RegisterKBTools(s, k, Deps{})

	msgs := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"commit_gate","arguments":{"changed_ids":["manutenzione/test-runbook"]}}}`,
	}
	resps := runMCPSequence(t, s, msgs)

	tr := decodeToolResult(t, resps[1])
	if tr.IsError {
		t.Fatalf("commit_gate: isError=true: %v", tr.Content)
	}
	if !strings.Contains(tr.Content[0].Text, `"pass": true`) {
		t.Errorf("commit_gate: expected pass=true: %s", tr.Content[0].Text)
	}
}

func TestServer_CommitGate_Blocked(t *testing.T) {
	k := setupTestKB(t)
	// Create an open contradiction involving test-runbook.
	os.MkdirAll(filepath.Join(k.DataRoot(), "conflicts"), 0o755)
	os.WriteFile(filepath.Join(k.DataRoot(), "conflicts", "c1.md"),
		[]byte("---\ntype: Contradiction\nresolution_status: open\ninvolves: [manutenzione/test-runbook]\ncontradiction_kind: factual\nreason: Outdated info\n---\n# Contradiction\n"), 0o644)

	s := New("0.4.0-m4")
	RegisterKBTools(s, k, Deps{})

	msgs := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"commit_gate","arguments":{"changed_ids":["manutenzione/test-runbook"]}}}`,
	}
	resps := runMCPSequence(t, s, msgs)

	tr := decodeToolResult(t, resps[1])
	if tr.IsError {
		t.Fatalf("commit_gate blocked: isError=true: %v", tr.Content)
	}
	if !strings.Contains(tr.Content[0].Text, `"pass": false`) {
		t.Errorf("commit_gate: expected pass=false: %s", tr.Content[0].Text)
	}
	if !strings.Contains(tr.Content[0].Text, "factual") {
		t.Errorf("commit_gate: expected 'factual' kind: %s", tr.Content[0].Text)
	}
}

func TestServer_GateCheck_Pass(t *testing.T) {
	k := setupTestKB(t)
	s := New("0.5.0-m5")
	RegisterKBTools(s, k, Deps{})

	msgs := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"gate_check","arguments":{"changed_ids":["manutenzione/test-runbook"]}}}`,
	}
	resps := runMCPSequence(t, s, msgs)

	tr := decodeToolResult(t, resps[1])
	if tr.IsError {
		t.Fatalf("gate_check: isError=true: %v", tr.Content)
	}
	if !strings.Contains(tr.Content[0].Text, `"pass": true`) {
		t.Errorf("gate_check: expected pass=true: %s", tr.Content[0].Text)
	}
}

func TestServer_GateCheck_Blocked(t *testing.T) {
	k := setupTestKB(t)
	os.MkdirAll(filepath.Join(k.DataRoot(), "conflicts"), 0o755)
	os.WriteFile(filepath.Join(k.DataRoot(), "conflicts", "c1.md"),
		[]byte("---\ntype: Contradiction\nresolution_status: open\ninvolves: [manutenzione/test-runbook]\ncontradiction_kind: factual\nreason: Outdated\n---\n# C\n"), 0o644)

	s := New("0.5.0-m5")
	RegisterKBTools(s, k, Deps{})

	msgs := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"gate_check","arguments":{"changed_ids":["manutenzione/test-runbook"]}}}`,
	}
	resps := runMCPSequence(t, s, msgs)

	tr := decodeToolResult(t, resps[1])
	if tr.IsError {
		t.Fatalf("gate_check blocked: isError=true: %v", tr.Content)
	}
	if !strings.Contains(tr.Content[0].Text, `"pass": false`) {
		t.Errorf("gate_check: expected pass=false: %s", tr.Content[0].Text)
	}
	if !strings.Contains(tr.Content[0].Text, "factual") {
		t.Errorf("gate_check: expected factual blocker: %s", tr.Content[0].Text)
	}
}

func TestServer_E2E_WriteQueryLint(t *testing.T) {
	k := setupTestKB(t)

	// Init no longer generates AGENTS.md/.gitignore (D62): the KB is mediated
	// entirely by the server, never edited directly by an agent.
	if _, err := os.Stat(filepath.Join(k.Root, "AGENTS.md")); !os.IsNotExist(err) {
		t.Errorf("AGENTS.md should not be generated by Init: %v", err)
	}
	if _, err := os.Stat(filepath.Join(k.Root, ".gitignore")); !os.IsNotExist(err) {
		t.Errorf(".gitignore should not be generated by Init: %v", err)
	}

	s := New("0.5.0-m5")
	RegisterKBTools(s, k, Deps{})

	msgs := []string{
		// 1. Initialize
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		// 2. Create map
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"map_create","arguments":{"name":"docs","title":"Documentation"}}}`,
		// 3. Write the "architecture" concept...
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"concept_write","arguments":{"id":"docs/architecture","frontmatter":{"type":"Index","title":"Architecture"},"body":"# Architecture\n"}}}`,
		// 4. ...and expand it into a directory so it can grow satellite concepts.
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"concept_expand","arguments":{"id":"docs/architecture"}}}`,
		// 5. Write a satellite concept under the expanded "docs/architecture"
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"concept_write","arguments":{"id":"docs/architecture/stack","frontmatter":{"type":"Reference","title":"Tech Stack","status":"active"},"body":"# Tech Stack\n\nPostgreSQL for persistence, Redis for caching.\n"}}}`,
		// 6. Rebuild index to include new concept
		`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"index_rebuild","arguments":{}}}`,
		// 7. Search for the concept
		`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"search","arguments":{"query":"postgresql redis"}}}`,
		// 8. Lint scoped to changed concept + neighbors
		`{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"lint","arguments":{"scope":"docs","scope_neighbors":true}}}`,
		// 9. Gate check
		`{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"gate_check","arguments":{"changed_ids":["docs/architecture/stack"]}}}`,
	}
	resps := runMCPSequence(t, s, msgs)

	if len(resps) != 9 {
		t.Fatalf("expected 9 responses, received %d", len(resps))
	}

	// Verify map_create OK
	tr2 := decodeToolResult(t, resps[1])
	if tr2.IsError {
		t.Fatalf("map_create: %v", tr2.Content)
	}

	// Verify concept_expand OK
	tr4 := decodeToolResult(t, resps[3])
	if tr4.IsError {
		t.Fatalf("concept_expand: %v", tr4.Content)
	}

	// Verify concept_write (satellite) OK
	tr5 := decodeToolResult(t, resps[4])
	if tr5.IsError {
		t.Fatalf("concept_write: %v", tr5.Content)
	}

	// Verify search finds the concept
	tr7 := decodeToolResult(t, resps[6])
	if tr7.IsError {
		t.Fatalf("search: %v", tr7.Content)
	}
	if !strings.Contains(tr7.Content[0].Text, "docs/architecture/stack") {
		t.Errorf("search: expected to find docs/architecture/stack: %s", tr7.Content[0].Text)
	}

	// Verify lint clean
	tr8 := decodeToolResult(t, resps[7])
	if tr8.IsError {
		t.Fatalf("lint: %v", tr8.Content)
	}

	// Verify gate_check passes
	tr9 := decodeToolResult(t, resps[8])
	if tr9.IsError {
		t.Fatalf("gate_check: %v", tr9.Content)
	}
	if !strings.Contains(tr9.Content[0].Text, `"pass": true`) {
		t.Errorf("gate_check: expected pass=true: %s", tr9.Content[0].Text)
	}
}

// TestServer_SkillInstall verifies that skill_install copies the bundled skill
// into k.Root/skills/<name>/ and that skill_list shows it as [installed].
func TestServer_SkillInstall(t *testing.T) {
	k := setupTestKB(t)

	bundleFS := fstest.MapFS{
		"bundled/kb-create/SKILL.md": &fstest.MapFile{
			Data: []byte("---\nname: kb-create\ndescription: Guide KB creation\nversion: \"1.0\"\n---\nBody here.\n"),
		},
	}

	s := New("1.0.0")
	RegisterKBTools(s, k, Deps{BundleFS: bundleFS})

	msgs := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"skill_install","arguments":{"name":"kb-create"}}}`,
	}
	resps := runMCPSequence(t, s, msgs)

	if len(resps) != 2 {
		t.Fatalf("expected 2 responses, received %d", len(resps))
	}

	tr := decodeToolResult(t, resps[1])
	if tr.IsError {
		t.Fatalf("skill_install: isError=true: %v", tr.Content)
	}
	if !strings.Contains(tr.Content[0].Text, "installed") {
		t.Errorf("skill_install: expected 'installed' in result: %s", tr.Content[0].Text)
	}

	// Verify the SKILL.md file was written to the KB.
	installedPath := filepath.Join(k.Root, "skills", "kb-create", "SKILL.md")
	if _, err := os.Stat(installedPath); err != nil {
		t.Errorf("SKILL.md not found at expected path %s: %v", installedPath, err)
	}
}

// TestServer_SkillInstall_AlreadyInstalled verifies that a second install without force fails.
func TestServer_SkillInstall_AlreadyInstalled(t *testing.T) {
	k := setupTestKB(t)

	bundleFS := fstest.MapFS{
		"bundled/kb-create/SKILL.md": &fstest.MapFile{
			Data: []byte("---\nname: kb-create\ndescription: Guide\nversion: \"1.0\"\n---\nBody.\n"),
		},
	}

	s := New("1.0.0")
	RegisterKBTools(s, k, Deps{BundleFS: bundleFS})

	msgs := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"skill_install","arguments":{"name":"kb-create"}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"skill_install","arguments":{"name":"kb-create"}}}`,
	}
	resps := runMCPSequence(t, s, msgs)

	if len(resps) != 3 {
		t.Fatalf("expected 3 responses, received %d", len(resps))
	}

	// First install succeeds.
	tr1 := decodeToolResult(t, resps[1])
	if tr1.IsError {
		t.Fatalf("first skill_install: unexpected error: %v", tr1.Content)
	}

	// Second install without force must fail.
	tr2 := decodeToolResult(t, resps[2])
	if !tr2.IsError {
		t.Fatal("second skill_install without force: expected isError=true")
	}
	if !strings.Contains(tr2.Content[0].Text, "force=true") {
		t.Errorf("skill_install: expected 'force=true' in error message: %s", tr2.Content[0].Text)
	}
}

// TestServer_SkillInstall_Unknown verifies that installing an unknown skill fails.
func TestServer_SkillInstall_Unknown(t *testing.T) {
	k := setupTestKB(t)

	bundleFS := fstest.MapFS{
		"bundled/kb-create/SKILL.md": &fstest.MapFile{
			Data: []byte("---\nname: kb-create\ndescription: Guide\n---\nBody.\n"),
		},
	}

	s := New("1.0.0")
	RegisterKBTools(s, k, Deps{BundleFS: bundleFS})

	msgs := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"skill_install","arguments":{"name":"nonexistent"}}}`,
	}
	resps := runMCPSequence(t, s, msgs)

	tr := decodeToolResult(t, resps[1])
	if !tr.IsError {
		t.Fatal("skill_install unknown: expected isError=true")
	}
	if !strings.Contains(tr.Content[0].Text, "unknown bundled skill") {
		t.Errorf("skill_install: expected 'unknown bundled skill': %s", tr.Content[0].Text)
	}
}

// TestServer_ConceptWrite_UpdatesIndex verifies that after a successful
// concept_write the keyword index is updated immediately, so search finds
// the concept without a prior index_rebuild call.
func TestServer_ConceptWrite_UpdatesIndex(t *testing.T) {
	k := setupTestKB(t)
	s := New("1.0.0")
	RegisterKBTools(s, k, Deps{})

	// Use a keyword that is not present in the initial KB content.
	uniqueKW := "xylophonequartz9981"

	msgs := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		// Write a new concept containing the unique keyword.
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"concept_write","arguments":{"id":"notes/kw-test","frontmatter":{"type":"Note","title":"KW Test"},"body":"# Body\n\n` + uniqueKW + `\n"}}}`,
		// Search immediately — must find the concept without index_rebuild.
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"search","arguments":{"query":"` + uniqueKW + `"}}}`,
	}
	resps := runMCPSequence(t, s, msgs)

	if len(resps) != 3 {
		t.Fatalf("expected 3 responses, received %d", len(resps))
	}

	// concept_write must succeed.
	trWrite := decodeToolResult(t, resps[1])
	if trWrite.IsError {
		t.Fatalf("concept_write: unexpected error: %v", trWrite.Content)
	}

	// Search must return the written concept without any index_rebuild.
	trSearch := decodeToolResult(t, resps[2])
	if trSearch.IsError {
		t.Fatalf("search after concept_write: unexpected error: %v", trSearch.Content)
	}
	if !strings.Contains(trSearch.Content[0].Text, "notes/kw-test") {
		t.Errorf("search after concept_write: expected 'notes/kw-test' in results (index not auto-updated?): %s", trSearch.Content[0].Text)
	}
}

// TestServer_ConceptWrite_UpdatesSQLIndex verifies that, with an active
// SQLIndex, a concept_write is immediately searchable — both on the FTS5 path
// and on the in-memory fallback path (exercised here by closing the SQLite
// index before searching, so SearchFTS fails and toolSearch falls back to the
// in-memory index, which was also updated by concept_write).
func TestServer_ConceptWrite_UpdatesSQLIndex(t *testing.T) {
	k := setupTestKB(t)
	dbPath := filepath.Join(t.TempDir(), "index.db")
	sqlIdx, err := sqlindex.Open(dbPath)
	if err != nil {
		t.Fatalf("sqlindex.Open: %v", err)
	}

	uniqueKW := "marmalade7723quokka"

	writeAndSearch := func(t *testing.T, s *Server) (trWrite, trSearch ToolResult) {
		t.Helper()
		msgs := []string{
			`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
			`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"concept_write","arguments":{"id":"notes/sql-kw-test","frontmatter":{"type":"Note","title":"SQL KW Test"},"body":"# Body\n\n` + uniqueKW + `\n"}}}`,
			`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"search","arguments":{"query":"` + uniqueKW + `"}}}`,
		}
		resps := runMCPSequence(t, s, msgs)
		if len(resps) != 3 {
			t.Fatalf("expected 3 responses, received %d", len(resps))
		}
		return decodeToolResult(t, resps[1]), decodeToolResult(t, resps[2])
	}

	t.Run("fts5", func(t *testing.T) {
		s := New("1.0.0")
		RegisterKBTools(s, k, Deps{SQLIndex: sqlIdx})

		trWrite, trSearch := writeAndSearch(t, s)
		if trWrite.IsError {
			t.Fatalf("concept_write: unexpected error: %v", trWrite.Content)
		}
		if trSearch.IsError {
			t.Fatalf("search after concept_write: unexpected error: %v", trSearch.Content)
		}
		if !strings.Contains(trSearch.Content[0].Text, "notes/sql-kw-test") {
			t.Errorf("search after concept_write (fts5): expected 'notes/sql-kw-test' in results: %s", trSearch.Content[0].Text)
		}
		if !strings.Contains(trSearch.Content[0].Text, "keyword_fts5") {
			t.Errorf("search after concept_write: expected mode 'keyword_fts5': %s", trSearch.Content[0].Text)
		}
	})

	t.Run("fallback", func(t *testing.T) {
		k := setupTestKB(t)
		dbPath := filepath.Join(t.TempDir(), "index.db")
		sqlIdx, err := sqlindex.Open(dbPath)
		if err != nil {
			t.Fatalf("sqlindex.Open: %v", err)
		}
		// Close the underlying DB so SearchFTS fails and toolSearch falls back
		// to the in-memory index, which concept_write must have kept in sync.
		sqlIdx.Close()

		s := New("1.0.0")
		RegisterKBTools(s, k, Deps{SQLIndex: sqlIdx})

		trWrite, trSearch := writeAndSearch(t, s)
		if trWrite.IsError {
			t.Fatalf("concept_write: unexpected error: %v", trWrite.Content)
		}
		if trSearch.IsError {
			t.Fatalf("search after concept_write: unexpected error: %v", trSearch.Content)
		}
		if !strings.Contains(trSearch.Content[0].Text, "notes/sql-kw-test") {
			t.Errorf("search after concept_write (fallback): expected 'notes/sql-kw-test' in results: %s", trSearch.Content[0].Text)
		}
		if !strings.Contains(trSearch.Content[0].Text, `"mode": "keyword"`) {
			t.Errorf("search after concept_write: expected fallback mode 'keyword': %s", trSearch.Content[0].Text)
		}
	})
}

// TestServer_ConceptMove_UpdatesIndexes verifies that concept_move keeps both
// the in-memory keyword index and the SQLite FTS5 index in sync: a search on
// the concept's content after the move must return only the new ID, never
// the old (stale) one. Covers the in-memory-only path (no SQLIndex), the
// FTS5 path, and the FTS5-closed fallback path (mirroring
// TestServer_ConceptWrite_UpdatesSQLIndex).
func TestServer_ConceptMove_UpdatesIndexes(t *testing.T) {
	uniqueKW := "flamingo4471tesseract"

	writeMoveSearch := func(t *testing.T, s *Server) (trWrite, trMove, trSearch ToolResult) {
		t.Helper()
		msgs := []string{
			`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
			`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"concept_write","arguments":{"id":"notes/move-src","frontmatter":{"type":"Note","title":"Move Src"},"body":"# Body\n\n` + uniqueKW + `\n"}}}`,
			`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"concept_move","arguments":{"source_id":"notes/move-src","target_id":"notes/move-dst"}}}`,
			`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"search","arguments":{"query":"` + uniqueKW + `"}}}`,
		}
		resps := runMCPSequence(t, s, msgs)
		if len(resps) != 4 {
			t.Fatalf("expected 4 responses, received %d", len(resps))
		}
		return decodeToolResult(t, resps[1]), decodeToolResult(t, resps[2]), decodeToolResult(t, resps[3])
	}

	assertSearchResult := func(t *testing.T, trWrite, trMove, trSearch ToolResult) {
		t.Helper()
		if trWrite.IsError {
			t.Fatalf("concept_write: unexpected error: %v", trWrite.Content)
		}
		if trMove.IsError {
			t.Fatalf("concept_move: unexpected error: %v", trMove.Content)
		}
		if trSearch.IsError {
			t.Fatalf("search after concept_move: unexpected error: %v", trSearch.Content)
		}
		text := trSearch.Content[0].Text
		if !strings.Contains(text, "notes/move-dst") {
			t.Errorf("search after concept_move: expected 'notes/move-dst' in results: %s", text)
		}
		if strings.Contains(text, "notes/move-src") {
			t.Errorf("search after concept_move: stale 'notes/move-src' still present in results: %s", text)
		}
	}

	t.Run("in-memory", func(t *testing.T) {
		k := setupTestKB(t)
		s := New("1.0.0")
		RegisterKBTools(s, k, Deps{})

		trWrite, trMove, trSearch := writeMoveSearch(t, s)
		assertSearchResult(t, trWrite, trMove, trSearch)
	})

	t.Run("fts5", func(t *testing.T) {
		k := setupTestKB(t)
		dbPath := filepath.Join(t.TempDir(), "index.db")
		sqlIdx, err := sqlindex.Open(dbPath)
		if err != nil {
			t.Fatalf("sqlindex.Open: %v", err)
		}
		s := New("1.0.0")
		RegisterKBTools(s, k, Deps{SQLIndex: sqlIdx})

		trWrite, trMove, trSearch := writeMoveSearch(t, s)
		assertSearchResult(t, trWrite, trMove, trSearch)
		if !strings.Contains(trSearch.Content[0].Text, "keyword_fts5") {
			t.Errorf("search after concept_move: expected mode 'keyword_fts5': %s", trSearch.Content[0].Text)
		}
	})

	t.Run("fallback", func(t *testing.T) {
		k := setupTestKB(t)
		dbPath := filepath.Join(t.TempDir(), "index.db")
		sqlIdx, err := sqlindex.Open(dbPath)
		if err != nil {
			t.Fatalf("sqlindex.Open: %v", err)
		}
		// Close the underlying DB so SearchFTS fails and toolSearch falls back
		// to the in-memory index, which concept_move must have kept in sync.
		sqlIdx.Close()

		s := New("1.0.0")
		RegisterKBTools(s, k, Deps{SQLIndex: sqlIdx})

		trWrite, trMove, trSearch := writeMoveSearch(t, s)
		assertSearchResult(t, trWrite, trMove, trSearch)
		if !strings.Contains(trSearch.Content[0].Text, `"mode": "keyword"`) {
			t.Errorf("search after concept_move: expected fallback mode 'keyword': %s", trSearch.Content[0].Text)
		}
	})
}

// TestServer_ConceptMove_StubsImplicitDossierIndex verifies that concept_move
// (D72 WP4) inherits WriteConcept's implicit-dossier stub: moving a concept
// into a new, previously non-existent archive/dossier path creates the
// dossier's index.md, so index_get never fails on a real dossier.
func TestServer_ConceptMove_StubsImplicitDossierIndex(t *testing.T) {
	k := setupTestKB(t)
	s := New("1.0.0")
	RegisterKBTools(s, k, Deps{})

	msgs := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"concept_write","arguments":{"id":"notes/move-src","frontmatter":{"type":"Note","title":"Move Src"},"body":"# Body\n"}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"concept_move","arguments":{"source_id":"notes/move-src","target_id":"notes/sub-topic/moved"}}}`,
	}
	resps := runMCPSequence(t, s, msgs)
	if len(resps) != 3 {
		t.Fatalf("expected 3 responses, received %d", len(resps))
	}
	trWrite := decodeToolResult(t, resps[1])
	if trWrite.IsError {
		t.Fatalf("concept_write: unexpected error: %v", trWrite.Content)
	}
	trMove := decodeToolResult(t, resps[2])
	if trMove.IsError {
		t.Fatalf("concept_move: unexpected error: %v", trMove.Content)
	}

	content, err := os.ReadFile(filepath.Join(k.DataRoot(), "notes", "sub-topic", "index.md"))
	if err != nil {
		t.Fatalf("expected stub index.md for implicitly created dossier notes/sub-topic: %v", err)
	}
	if !strings.Contains(string(content), "type: Index") || !strings.Contains(string(content), "title: Sub Topic") {
		t.Errorf("unexpected stub index.md content: %q", content)
	}
}

// writeTestConcept writes a minimal concept file directly to disk under k's
// DataRoot (bypassing concept_write), creating parent directories as needed.
// Used by concept_move batch tests (D72 WP1) to set up link fixtures.
func writeTestConcept(t *testing.T, k *kb.KB, id, body string) {
	t.Helper()
	absPath := filepath.Join(k.DataRoot(), id+".md")
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		t.Fatalf("writeTestConcept: mkdir: %v", err)
	}
	content := "---\ntype: Note\ntitle: " + id + "\n---\n" + body
	if err := os.WriteFile(absPath, []byte(content), 0o644); err != nil {
		t.Fatalf("writeTestConcept: %v", err)
	}
}

// readTestConceptContent reads a concept's raw file content (frontmatter +
// body) directly from disk under k's DataRoot.
func readTestConceptContent(t *testing.T, k *kb.KB, id string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(k.DataRoot(), id+".md"))
	if err != nil {
		t.Fatalf("readTestConceptContent %q: %v", id, err)
	}
	return string(data)
}

// TestServer_ConceptMove_BatchRewriteLinks covers D72 WP1: a batch of 3
// moves plus the default backlink rewrite, checking all three link forms
// (simple wiki-link, wiki-link with an anchor, relative markdown link) and
// that the search index reflects the new IDs.
func TestServer_ConceptMove_BatchRewriteLinks(t *testing.T) {
	k := setupTestKB(t)
	writeTestConcept(t, k, "notes/x1", "# X1\n\nuniqueKeywordX1Zephyr\n")
	writeTestConcept(t, k, "notes/x2", "# X2\n")
	writeTestConcept(t, k, "notes/x3", "# X3\n")
	writeTestConcept(t, k, "notes/linker",
		"Wiki simple: [[notes/x1]]\n\nWiki anchor: [[notes/x2#Section]]\n\nMarkdown rel: [text](x3.md)\n")

	s := New("1.0.0")
	RegisterKBTools(s, k, Deps{})

	moveArgs := `{"moves":[` +
		`{"source_id":"notes/x1","target_id":"notes/moved-x1"},` +
		`{"source_id":"notes/x2","target_id":"notes/moved-x2"},` +
		`{"source_id":"notes/x3","target_id":"notes/moved-x3"}` +
		`]}`
	msgs := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"concept_move","arguments":` + moveArgs + `}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"search","arguments":{"query":"uniqueKeywordX1Zephyr"}}}`,
	}
	resps := runMCPSequence(t, s, msgs)
	if len(resps) != 3 {
		t.Fatalf("expected 3 responses, got %d", len(resps))
	}

	trMove := decodeToolResult(t, resps[1])
	if trMove.IsError {
		t.Fatalf("concept_move: unexpected error: %v", trMove.Content)
	}

	var result struct {
		Moves []struct {
			SourceID string `json:"source_id"`
			TargetID string `json:"target_id"`
		} `json:"moves"`
		Rewritten []struct {
			ID           string `json:"id"`
			Replacements int    `json:"replacements"`
		} `json:"rewritten"`
	}
	if err := json.Unmarshal([]byte(trMove.Content[0].Text), &result); err != nil {
		t.Fatalf("decode concept_move result: %v\n%s", err, trMove.Content[0].Text)
	}
	if len(result.Moves) != 3 {
		t.Fatalf("expected 3 applied moves, got %d: %v", len(result.Moves), result.Moves)
	}

	foundLinker := false
	for _, r := range result.Rewritten {
		if r.ID == "notes/linker" {
			foundLinker = true
			if r.Replacements != 3 {
				t.Errorf("notes/linker: expected 3 replacements, got %d", r.Replacements)
			}
		}
	}
	if !foundLinker {
		t.Fatalf("expected notes/linker in rewritten list: %v", result.Rewritten)
	}

	linkerBody := readTestConceptContent(t, k, "notes/linker")
	for _, want := range []string{"[[notes/moved-x1]]", "[[notes/moved-x2#Section]]", "(moved-x3.md)"} {
		if !strings.Contains(linkerBody, want) {
			t.Errorf("notes/linker: expected %q in rewritten body, got:\n%s", want, linkerBody)
		}
	}
	for _, unwanted := range []string{"[[notes/x1]]", "[[notes/x2#Section]]", "(x3.md)"} {
		if strings.Contains(linkerBody, unwanted) {
			t.Errorf("notes/linker: stale link %q still present:\n%s", unwanted, linkerBody)
		}
	}

	trSearch := decodeToolResult(t, resps[2])
	if trSearch.IsError {
		t.Fatalf("search: unexpected error: %v", trSearch.Content)
	}
	searchText := trSearch.Content[0].Text
	if !strings.Contains(searchText, `"id": "notes/moved-x1"`) {
		t.Errorf("search: expected id 'notes/moved-x1' in results: %s", searchText)
	}
	if strings.Contains(searchText, `"id": "notes/x1"`) {
		t.Errorf("search: stale id 'notes/x1' still present: %s", searchText)
	}
}

// TestServer_ConceptMove_BatchInvalidEntry_NoMoveApplied verifies that
// validation runs over the whole batch before any move is applied: one
// invalid entry (a target that is already occupied) must abort the entire
// call, leaving the KB untouched.
func TestServer_ConceptMove_BatchInvalidEntry_NoMoveApplied(t *testing.T) {
	k := setupTestKB(t)
	writeTestConcept(t, k, "notes/p1", "# P1\n")
	writeTestConcept(t, k, "notes/p2", "# P2\n")
	writeTestConcept(t, k, "notes/occupied", "# Occupied\n")

	s := New("1.0.0")
	RegisterKBTools(s, k, Deps{})

	moveArgs := `{"moves":[` +
		`{"source_id":"notes/p1","target_id":"notes/moved-p1"},` +
		`{"source_id":"notes/p2","target_id":"notes/occupied"}` +
		`]}`
	msgs := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"concept_move","arguments":` + moveArgs + `}}`,
	}
	resps := runMCPSequence(t, s, msgs)
	tr := decodeToolResult(t, resps[1])
	if !tr.IsError {
		t.Fatalf("expected concept_move to fail on conflicting target, got: %v", tr.Content)
	}

	if _, err := os.Stat(filepath.Join(k.DataRoot(), "notes", "p1.md")); err != nil {
		t.Errorf("notes/p1.md should still exist: %v", err)
	}
	if _, err := os.Stat(filepath.Join(k.DataRoot(), "notes", "p2.md")); err != nil {
		t.Errorf("notes/p2.md should still exist: %v", err)
	}
	if _, err := os.Stat(filepath.Join(k.DataRoot(), "notes", "moved-p1.md")); !os.IsNotExist(err) {
		t.Errorf("notes/moved-p1.md should not exist (batch must be atomic), stat err=%v", err)
	}
}

// TestServer_ConceptMove_SingleFormBackCompat verifies that the top-level
// source_id/target_id form still works exactly as before batching was
// introduced, and that mixing it with 'moves' is rejected.
func TestServer_ConceptMove_SingleFormBackCompat(t *testing.T) {
	k := setupTestKB(t)
	writeTestConcept(t, k, "notes/single-src", "# Src\n")

	s := New("1.0.0")
	RegisterKBTools(s, k, Deps{})

	msgs := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"concept_move","arguments":{"source_id":"notes/single-src","target_id":"notes/single-dst"}}}`,
	}
	resps := runMCPSequence(t, s, msgs)
	tr := decodeToolResult(t, resps[1])
	if tr.IsError {
		t.Fatalf("concept_move (single form): unexpected error: %v", tr.Content)
	}
	if !strings.Contains(tr.Content[0].Text, `"notes/single-dst"`) {
		t.Errorf("expected 'notes/single-dst' in result: %s", tr.Content[0].Text)
	}
	if _, err := os.Stat(filepath.Join(k.DataRoot(), "notes", "single-dst.md")); err != nil {
		t.Errorf("notes/single-dst.md should exist: %v", err)
	}

	msgs2 := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"concept_move","arguments":{"source_id":"a","target_id":"b","moves":[{"source_id":"c","target_id":"d"}]}}}`,
	}
	resps2 := runMCPSequence(t, s, msgs2)
	tr2 := decodeToolResult(t, resps2[1])
	if !tr2.IsError {
		t.Fatalf("expected error when mixing 'moves' with top-level source_id/target_id, got: %v", tr2.Content)
	}
}

// TestServer_ConceptMove_RewriteLinksFalse verifies that rewrite_links=false
// leaves inbound links untouched and keeps the "not updated" warning.
func TestServer_ConceptMove_RewriteLinksFalse(t *testing.T) {
	k := setupTestKB(t)
	writeTestConcept(t, k, "notes/rf-target", "# Target\n")
	writeTestConcept(t, k, "notes/rf-linker", "See [[notes/rf-target]] for details.\n")

	s := New("1.0.0")
	RegisterKBTools(s, k, Deps{})

	msgs := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"concept_move","arguments":{"source_id":"notes/rf-target","target_id":"notes/rf-target-moved","rewrite_links":false}}}`,
	}
	resps := runMCPSequence(t, s, msgs)
	tr := decodeToolResult(t, resps[1])
	if tr.IsError {
		t.Fatalf("concept_move: unexpected error: %v", tr.Content)
	}
	if !strings.Contains(tr.Content[0].Text, "inbound links to notes/rf-target are not updated") {
		t.Errorf("expected 'inbound links...not updated' warning, got: %s", tr.Content[0].Text)
	}

	linkerBody := readTestConceptContent(t, k, "notes/rf-linker")
	if !strings.Contains(linkerBody, "[[notes/rf-target]]") {
		t.Errorf("rewrite_links=false: link should be untouched, got:\n%s", linkerBody)
	}
}

// TestServer_ConceptMove_ChainedLinksBetweenMovedConcepts verifies that when
// a moved concept links to another concept moved in the same batch, the
// backlink rewrite pass applies the full old→new map on the already-
// relocated file (post-move state), not the pre-move one.
func TestServer_ConceptMove_ChainedLinksBetweenMovedConcepts(t *testing.T) {
	k := setupTestKB(t)
	writeTestConcept(t, k, "dossier/a", "Links to [[dossier/b]].\n")
	writeTestConcept(t, k, "dossier/b", "# B\n")

	s := New("1.0.0")
	RegisterKBTools(s, k, Deps{})

	moveArgs := `{"moves":[` +
		`{"source_id":"dossier/a","target_id":"newdossier/moved-a"},` +
		`{"source_id":"dossier/b","target_id":"newdossier/moved-b"}` +
		`]}`
	msgs := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"concept_move","arguments":` + moveArgs + `}}`,
	}
	resps := runMCPSequence(t, s, msgs)
	tr := decodeToolResult(t, resps[1])
	if tr.IsError {
		t.Fatalf("concept_move: unexpected error: %v", tr.Content)
	}

	movedABody := readTestConceptContent(t, k, "newdossier/moved-a")
	if !strings.Contains(movedABody, "[[newdossier/moved-b]]") {
		t.Errorf("expected moved-a to link to moved-b's new id, got:\n%s", movedABody)
	}
	if strings.Contains(movedABody, "[[dossier/b]]") {
		t.Errorf("stale link to dossier/b still present:\n%s", movedABody)
	}
}

// TestEnsureSQLIndexFresh_ColdStart reproduces the cold-start bug: a KB with
// concepts already on disk (as after a fresh git clone) but a brand-new,
// empty SQLite index (.cartographer/index.db is gitignored). EnsureSQLIndexFresh
// must rebuild the FTS5 table from disk so keyword search finds the concepts
// without requiring a manual index_rebuild call.
func TestEnsureSQLIndexFresh_ColdStart(t *testing.T) {
	k := setupTestKB(t) // already has manutenzione/test-runbook

	os.MkdirAll(filepath.Join(k.DataRoot(), "manutenzione"), 0o755)
	os.WriteFile(filepath.Join(k.DataRoot(), "manutenzione", "second.md"),
		[]byte("---\ntype: Note\ntitle: Second\n---\n# Second\nkeywordAlpha\n"), 0o644)
	os.WriteFile(filepath.Join(k.DataRoot(), "manutenzione", "third.md"),
		[]byte("---\ntype: Note\ntitle: Third\n---\n# Third\nkeywordBeta\n"), 0o644)

	dbPath := filepath.Join(t.TempDir(), "index.db")
	sqlIdx, err := sqlindex.Open(dbPath)
	if err != nil {
		t.Fatalf("sqlindex.Open: %v", err)
	}
	defer sqlIdx.Close()

	if n, err := sqlIdx.Count(); err != nil || n != 0 {
		t.Fatalf("precondition: expected empty index, got count=%d err=%v", n, err)
	}

	n, err := EnsureSQLIndexFresh(k, sqlIdx)
	if err != nil {
		t.Fatalf("EnsureSQLIndexFresh: %v", err)
	}
	if n != 3 {
		t.Errorf("EnsureSQLIndexFresh: expected 3 concepts indexed, got %d", n)
	}

	s := New("1.0.0")
	RegisterKBTools(s, k, Deps{SQLIndex: sqlIdx})

	msgs := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"search","arguments":{"query":"keywordAlpha"}}}`,
	}
	resps := runMCPSequence(t, s, msgs)
	tr := decodeToolResult(t, resps[1])
	if tr.IsError {
		t.Fatalf("search: isError=true: %v", tr.Content)
	}
	if !strings.Contains(tr.Content[0].Text, "manutenzione/second") {
		t.Errorf("search after cold-start rebuild: expected 'manutenzione/second': %s", tr.Content[0].Text)
	}
	if !strings.Contains(tr.Content[0].Text, "keyword_fts5") {
		t.Errorf("search after cold-start rebuild: expected mode 'keyword_fts5': %s", tr.Content[0].Text)
	}

	// A second call on an already-populated index is a no-op.
	n2, err := EnsureSQLIndexFresh(k, sqlIdx)
	if err != nil {
		t.Fatalf("EnsureSQLIndexFresh (second call): %v", err)
	}
	if n2 != 0 {
		t.Errorf("EnsureSQLIndexFresh (second call): expected no-op (0), got %d", n2)
	}
}

// TestServer_SyncCheck verifies that sync_check returns a non-empty revision
// and that in_sync=true when the applied_revision matches the manifest's.
func TestServer_SyncCheck(t *testing.T) {
	k := setupTestKB(t)

	bundleFS := fstest.MapFS{
		"bundled/kb-create/SKILL.md": &fstest.MapFile{
			Data: []byte("---\nname: kb-create\ndescription: Guide KB creation\nversion: \"1.0\"\n---\nBody.\n"),
		},
	}

	s := New("1.0.0")
	RegisterKBTools(s, k, Deps{BundleFS: bundleFS})

	msgs := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"sync_check","arguments":{}}}`,
	}
	resps := runMCPSequence(t, s, msgs)

	if len(resps) != 2 {
		t.Fatalf("expected 2 responses, received %d", len(resps))
	}

	tr := decodeToolResult(t, resps[1])
	if tr.IsError {
		t.Fatalf("sync_check: isError=true: %v", tr.Content)
	}

	// Verify the response contains revision and artifacts.
	var result map[string]interface{}
	if err := json.Unmarshal([]byte(tr.Content[0].Text), &result); err != nil {
		t.Fatalf("sync_check: response is not JSON: %v", err)
	}
	rev, ok := result["revision"].(string)
	if !ok || rev == "" {
		t.Errorf("sync_check: revision empty or missing: %v", result)
	}
	if _, ok := result["in_sync"]; !ok {
		t.Error("sync_check: in_sync field missing")
	}
	if _, ok := result["artifacts"]; !ok {
		t.Error("sync_check: artifacts field missing")
	}

	// Second check with the correct revision → in_sync=true.
	argsInSync := fmt.Sprintf(`{"applied_revision":"%s"}`, rev)
	msgs2 := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"sync_check","arguments":` + argsInSync + `}}`,
	}
	resps2 := runMCPSequence(t, s, msgs2)
	tr2 := decodeToolResult(t, resps2[1])
	var result2 map[string]interface{}
	json.Unmarshal([]byte(tr2.Content[0].Text), &result2)
	if result2["in_sync"] != true {
		t.Errorf("sync_check: expected in_sync=true with the correct revision: %v", result2)
	}
}

// TestServer_SyncApply verifies that sync_apply writes the skill into base_dir
// and returns the lock with the updated revision.
// TestServer_Notify verifies that a tool wrapped with notifyWrap emits a
// JSON-RPC notification line on the stdio stream after successful completion.
func TestServer_Notify(t *testing.T) {
	s := New("1.0.0")

	// Register a dummy tool wrapped with notifyWrap.
	dummy := Tool{
		Name:        "dummy",
		Description: "A test tool that triggers a notification.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
		Handler: func(args json.RawMessage) (ToolResult, error) {
			return textResult("done"), nil
		},
	}
	s.RegisterTool(notifyWrap(s, dummy, "notifications/skills/list_changed"))

	// Build the stdio sequence.
	msgs := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"dummy","arguments":{}}}`,
	}
	input := strings.Join(msgs, "\n") + "\n"

	pr, pw := io.Pipe()
	done := make(chan struct{})
	var rawLines []string

	go func() {
		defer close(done)
		dec := json.NewDecoder(pr)
		for {
			var raw json.RawMessage
			if err := dec.Decode(&raw); err != nil {
				return
			}
			rawLines = append(rawLines, string(raw))
		}
	}()

	s.Run(strings.NewReader(input), pw)
	pw.Close()
	<-done

	// We expect three lines: initialize response, tools/call response, notification.
	if len(rawLines) < 3 {
		t.Fatalf("expected at least 3 output lines, got %d: %v", len(rawLines), rawLines)
	}

	var found bool
	for _, line := range rawLines {
		if strings.Contains(line, `"method":"notifications/skills/list_changed"`) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("notification line not found in output; lines: %s", strings.Join(rawLines, "\n"))
	}
}

func TestServer_Notify_NoNotificationOnError(t *testing.T) {
	s := New("1.0.0")

	// Register a dummy tool that returns an application error.
	dummy := Tool{
		Name:        "dummy_err",
		Description: "A test tool that returns an error.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
		Handler: func(args json.RawMessage) (ToolResult, error) {
			return errorResult("failure"), nil
		},
	}
	s.RegisterTool(notifyWrap(s, dummy, "notifications/skills/list_changed"))

	msgs := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"dummy_err","arguments":{}}}`,
	}
	input := strings.Join(msgs, "\n") + "\n"

	pr, pw := io.Pipe()
	done := make(chan struct{})
	var rawLines []string

	go func() {
		defer close(done)
		dec := json.NewDecoder(pr)
		for {
			var raw json.RawMessage
			if err := dec.Decode(&raw); err != nil {
				return
			}
			rawLines = append(rawLines, string(raw))
		}
	}()

	s.Run(strings.NewReader(input), pw)
	pw.Close()
	<-done

	// Expect exactly 2 lines (initialize + tools/call), no notification.
	if len(rawLines) != 2 {
		t.Fatalf("expected 2 output lines (no notification), got %d: %v", len(rawLines), rawLines)
	}

	for _, line := range rawLines {
		if strings.Contains(line, `"method":"notifications/skills/list_changed"`) {
			t.Errorf("unexpected notification on error result: %s", line)
		}
	}
}

func TestServer_SyncApply(t *testing.T) {
	k := setupTestKB(t)
	baseDir := t.TempDir()

	bundleFS := fstest.MapFS{
		"bundled/kb-create/SKILL.md": &fstest.MapFile{
			Data: []byte("---\nname: kb-create\ndescription: Guide KB creation\nversion: \"1.0\"\n---\nBody.\n"),
		},
	}

	s := New("1.0.0")
	RegisterKBTools(s, k, Deps{BundleFS: bundleFS})

	argsApply := fmt.Sprintf(`{"base_dir":%q}`, baseDir)
	msgs := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"sync_apply","arguments":` + argsApply + `}}`,
	}
	resps := runMCPSequence(t, s, msgs)

	if len(resps) != 2 {
		t.Fatalf("expected 2 responses, received %d", len(resps))
	}

	tr := decodeToolResult(t, resps[1])
	if tr.IsError {
		t.Fatalf("sync_apply: isError=true: %v", tr.Content)
	}

	var result map[string]interface{}
	if err := json.Unmarshal([]byte(tr.Content[0].Text), &result); err != nil {
		t.Fatalf("sync_apply: response is not JSON: %v", err)
	}

	// Verify new_revision is populated.
	newRev, ok := result["new_revision"].(string)
	if !ok || newRev == "" {
		t.Errorf("sync_apply: new_revision empty or missing: %v", result)
	}

	// Verify SKILL.md was written into base_dir.
	skillPath := filepath.Join(baseDir, ".claude", "skills", "kb-create", "SKILL.md")
	if _, err := os.Stat(skillPath); err != nil {
		t.Errorf("sync_apply: SKILL.md not found in %s: %v", skillPath, err)
	}

	// Verify the lockfile was written.
	lockPath := filepath.Join(baseDir, ".cartographer-sync.lock.json")
	if _, err := os.Stat(lockPath); err != nil {
		t.Errorf("sync_apply: lockfile not found in %s: %v", lockPath, err)
	}
}

// TestServer_SyncPull verifies that sync_pull returns a revision and each
// artifact's file contents (base64), decodable and consistent with the test bundle.
func TestServer_SyncPull(t *testing.T) {
	k := setupTestKB(t)

	bundleFS := fstest.MapFS{
		"bundled/kb-create/SKILL.md": &fstest.MapFile{
			Data: []byte("---\nname: kb-create\ndescription: Guide KB creation\nversion: \"1.0\"\n---\nBody sync_pull.\n"),
		},
	}

	s := New("1.0.0")
	RegisterKBTools(s, k, Deps{BundleFS: bundleFS})

	msgs := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"sync_pull","arguments":{}}}`,
	}
	resps := runMCPSequence(t, s, msgs)

	if len(resps) != 2 {
		t.Fatalf("expected 2 responses, received %d", len(resps))
	}

	tr := decodeToolResult(t, resps[1])
	if tr.IsError {
		t.Fatalf("sync_pull: isError=true: %v", tr.Content)
	}

	var result struct {
		Revision  string `json:"revision"`
		Artifacts []struct {
			Kind        string `json:"kind"`
			Name        string `json:"name"`
			Source      string `json:"source"`
			ContentHash string `json:"content_hash"`
			Signed      bool   `json:"signed"`
			Files       []struct {
				Path       string `json:"path"`
				ContentB64 string `json:"content_b64"`
			} `json:"files"`
		} `json:"artifacts"`
	}
	if err := json.Unmarshal([]byte(tr.Content[0].Text), &result); err != nil {
		t.Fatalf("sync_pull: response is not JSON: %v", err)
	}
	if result.Revision == "" {
		t.Error("sync_pull: empty revision")
	}
	// 2 artifacts expected: the bundled kb-create skill + the imprinting
	// "instructions" artifact that BuildManifest always generates, one per KB (D56).
	if len(result.Artifacts) != 2 {
		t.Fatalf("sync_pull: expected 2 artifacts (kb-create bundled + instructions), got %d: %+v", len(result.Artifacts), result.Artifacts)
	}
	var art, instr *struct {
		Kind        string `json:"kind"`
		Name        string `json:"name"`
		Source      string `json:"source"`
		ContentHash string `json:"content_hash"`
		Signed      bool   `json:"signed"`
		Files       []struct {
			Path       string `json:"path"`
			ContentB64 string `json:"content_b64"`
		} `json:"files"`
	}
	for i := range result.Artifacts {
		switch result.Artifacts[i].Kind {
		case "skill":
			art = &result.Artifacts[i]
		case "instructions":
			instr = &result.Artifacts[i]
		}
	}
	if art == nil {
		t.Fatalf("sync_pull: no skill artifact found: %+v", result.Artifacts)
	}
	if instr == nil {
		t.Fatalf("sync_pull: no instructions artifact found: %+v", result.Artifacts)
	}
	if instr.Source == "bundle" || len(instr.Files) != 1 || instr.Files[0].Path != "instructions.md" {
		t.Errorf("sync_pull: unexpected instructions artifact: %+v", instr)
	}
	if art.Name != "kb-create" || art.Source != "bundle" || !art.Signed {
		t.Errorf("sync_pull: unexpected artifact: %+v", art)
	}
	if len(art.Files) != 1 || art.Files[0].Path != "SKILL.md" {
		t.Fatalf("sync_pull: expected 1 SKILL.md file, got %+v", art.Files)
	}
	decoded, err := base64.StdEncoding.DecodeString(art.Files[0].ContentB64)
	if err != nil {
		t.Fatalf("sync_pull: content_b64 not decodable: %v", err)
	}
	if !strings.Contains(string(decoded), "Body sync_pull.") {
		t.Errorf("sync_pull: unexpected decoded content: %s", decoded)
	}
}

// listToolNames issues initialize + tools/list against s and returns the
// advertised tool names.
func listToolNames(t *testing.T, s *Server) map[string]bool {
	t.Helper()
	msgs := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
	}
	resps := runMCPSequence(t, s, msgs)
	if len(resps) != 2 || resps[1].Error != nil {
		t.Fatalf("tools/list: unexpected responses: %+v", resps)
	}
	resultBytes, _ := json.Marshal(resps[1].Result)
	var result struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(resultBytes, &result); err != nil {
		t.Fatalf("tools/list: decode: %v", err)
	}
	names := map[string]bool{}
	for _, tool := range result.Tools {
		names[tool.Name] = true
	}
	return names
}

// TestServer_ToolsProfile is the golden test for the tools profile (D65):
// under "agent" tools/list advertises exactly the core set below; everything
// else must be classified in advancedToolNames (visibility.go). Adding a tool
// without classifying it fails this test. Hidden tools stay callable via
// tools/call.
func TestServer_ToolsProfile(t *testing.T) {
	k := setupTestKB(t)
	s := New("test")
	bundleFS := fstest.MapFS{
		"bundled/kb-create/SKILL.md": &fstest.MapFile{
			Data: []byte("---\nname: kb-create\ndescription: Guide KB creation\nversion: \"1.0\"\n---\nBody here.\n"),
		},
	}
	RegisterKBTools(s, k, Deps{BundleFS: bundleFS})

	// Default (no SetToolsProfile): full list, historical behavior.
	all := listToolNames(t, s)
	if len(all) != len(s.Tools()) {
		t.Errorf("default profile: expected all %d tools advertised, got %d", len(s.Tools()), len(all))
	}

	// Agent profile: exactly the core set.
	s.SetToolsProfile("agent")
	agentVisible := []string{
		"atlas_overview", "index_get", "concept_read", "log_tail",
		"concept_write", "concept_patch", "map_create", "concept_expand", "log_append", "snapshot",
		"map_list", "concept_list", "graph_neighbors", "search",
		"supersede", "concept_move", "concept_delete",
		"conflicts_list", "git_conflict_resolve",
		"artifact_read",
	}
	got := listToolNames(t, s)
	for _, name := range agentVisible {
		if !got[name] {
			t.Errorf("agent profile: core tool %q not advertised", name)
		}
	}
	if len(got) != len(agentVisible) {
		for name := range got {
			if !ToolAdvanced(name) {
				continue
			}
			t.Errorf("agent profile: advanced tool %q must not be advertised", name)
		}
		t.Errorf("agent profile: expected %d tools, got %d", len(agentVisible), len(got))
	}

	// Classification completeness: every registered tool is either core or advanced.
	coreSet := map[string]bool{}
	for _, name := range agentVisible {
		coreSet[name] = true
	}
	for name := range s.Tools() {
		if !coreSet[name] && !ToolAdvanced(name) {
			t.Errorf("tool %q is neither in the agent core set nor in advancedToolNames: classify it (visibility.go)", name)
		}
		if coreSet[name] && ToolAdvanced(name) {
			t.Errorf("tool %q is both core and advanced", name)
		}
	}

	// Hidden tools stay callable via tools/call under the agent profile.
	callResps := runMCPSequence(t, s, []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"kb_status","arguments":{}}}`,
	})
	tr := decodeToolResult(t, callResps[1])
	if tr.IsError {
		t.Errorf("agent profile: hidden tool kb_status must stay callable, got error: %v", tr.Content)
	}

	// Full profile: everything advertised again.
	s.SetToolsProfile("full")
	if got := listToolNames(t, s); len(got) != len(s.Tools()) {
		t.Errorf("full profile: expected all %d tools advertised, got %d", len(s.Tools()), len(got))
	}
}

// --- concept_patch (D70) ---

// conceptHash decodes the content_hash out of a concept_write/concept_patch
// ToolResult.
func conceptHash(t *testing.T, tr ToolResult) string {
	t.Helper()
	var result struct {
		ContentHash string `json:"content_hash"`
	}
	if err := json.Unmarshal([]byte(tr.Content[0].Text), &result); err != nil {
		t.Fatalf("decode content_hash: %v", err)
	}
	if result.ContentHash == "" {
		t.Fatalf("empty content_hash in response: %s", tr.Content[0].Text)
	}
	return result.ContentHash
}

func TestServer_ConceptPatch_HappyPath(t *testing.T) {
	k := setupTestKB(t)
	s := New("1.0.0")
	RegisterKBTools(s, k, Deps{})

	msgs := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"concept_write","arguments":{"id":"note/patch-happy","frontmatter":{"type":"Note","title":"Patch Happy"},"body":"# Titolo\n\nRiga uno.\nRiga due.\n"}}}`,
	}
	resps := runMCPSequence(t, s, msgs)
	trWrite := decodeToolResult(t, resps[1])
	if trWrite.IsError {
		t.Fatalf("concept_write: unexpected error: %v", trWrite.Content)
	}
	hash := conceptHash(t, trWrite)

	patchArgs := fmt.Sprintf(`{"id":"note/patch-happy","old_string":"Riga uno.","new_string":"Riga modificata.","if_match":%q}`, hash)
	resps2 := runMCPSequence(t, s, []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"concept_patch","arguments":` + patchArgs + `}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"concept_read","arguments":{"id":"note/patch-happy"}}}`,
	})

	trPatch := decodeToolResult(t, resps2[1])
	if trPatch.IsError {
		t.Fatalf("concept_patch: unexpected error: %v", trPatch.Content)
	}
	if !strings.Contains(trPatch.Content[0].Text, "content_hash") {
		t.Errorf("concept_patch: response missing content_hash: %s", trPatch.Content[0].Text)
	}

	trRead := decodeToolResult(t, resps2[2])
	if trRead.IsError {
		t.Fatalf("concept_read: unexpected error: %v", trRead.Content)
	}
	if !strings.Contains(trRead.Content[0].Text, "Riga modificata.") {
		t.Errorf("concept_patch: expected patched line present: %s", trRead.Content[0].Text)
	}
	if strings.Contains(trRead.Content[0].Text, "Riga uno.") {
		t.Errorf("concept_patch: old_string should have been replaced: %s", trRead.Content[0].Text)
	}
	if !strings.Contains(trRead.Content[0].Text, "Riga due.") {
		t.Errorf("concept_patch: untouched line should still be present: %s", trRead.Content[0].Text)
	}
}

func TestServer_ConceptPatch_StaleWrite(t *testing.T) {
	k := setupTestKB(t)
	s := New("1.0.0")
	RegisterKBTools(s, k, Deps{})

	msgs := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"concept_write","arguments":{"id":"note/patch-stale","frontmatter":{"type":"Note"},"body":"# Corpo\n\nTesto originale.\n"}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"concept_patch","arguments":{"id":"note/patch-stale","old_string":"Testo originale.","new_string":"Testo nuovo.","if_match":"hash-sbagliato-xyz"}}}`,
	}
	resps := runMCPSequence(t, s, msgs)

	if tr := decodeToolResult(t, resps[1]); tr.IsError {
		t.Fatalf("concept_write: unexpected error: %v", tr.Content)
	}
	trPatch := decodeToolResult(t, resps[2])
	if !trPatch.IsError {
		t.Fatal("concept_patch with wrong if_match: expected isError=true")
	}
	if !strings.Contains(trPatch.Content[0].Text, "stale_write") {
		t.Errorf("concept_patch: expected 'stale_write': %s", trPatch.Content[0].Text)
	}
}

func TestServer_ConceptPatch_OldStringNotFound(t *testing.T) {
	k := setupTestKB(t)
	s := New("1.0.0")
	RegisterKBTools(s, k, Deps{})

	msgs := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"concept_write","arguments":{"id":"note/patch-notfound","frontmatter":{"type":"Note"},"body":"# Corpo\n\nTesto presente.\n"}}}`,
	}
	resps := runMCPSequence(t, s, msgs)
	trWrite := decodeToolResult(t, resps[1])
	if trWrite.IsError {
		t.Fatalf("concept_write: unexpected error: %v", trWrite.Content)
	}
	hash := conceptHash(t, trWrite)

	patchArgs := fmt.Sprintf(`{"id":"note/patch-notfound","old_string":"Testo assente","new_string":"x","if_match":%q}`, hash)
	resps2 := runMCPSequence(t, s, []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"concept_patch","arguments":` + patchArgs + `}}`,
	})
	trPatch := decodeToolResult(t, resps2[1])
	if !trPatch.IsError {
		t.Fatal("concept_patch with absent old_string: expected isError=true")
	}
	if !strings.Contains(trPatch.Content[0].Text, "old_string_not_found") {
		t.Errorf("concept_patch: expected 'old_string_not_found': %s", trPatch.Content[0].Text)
	}
}

func TestServer_ConceptPatch_OldStringAmbiguous(t *testing.T) {
	k := setupTestKB(t)
	s := New("1.0.0")
	RegisterKBTools(s, k, Deps{})

	msgs := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"concept_write","arguments":{"id":"note/patch-ambiguous","frontmatter":{"type":"Note"},"body":"# Corpo\n\nripetuto qui, ripetuto ancora, e ripetuto di nuovo.\n"}}}`,
	}
	resps := runMCPSequence(t, s, msgs)
	trWrite := decodeToolResult(t, resps[1])
	if trWrite.IsError {
		t.Fatalf("concept_write: unexpected error: %v", trWrite.Content)
	}
	hash := conceptHash(t, trWrite)

	// Without replace_all: ambiguous (3 occurrences of "ripetuto").
	patchArgs := fmt.Sprintf(`{"id":"note/patch-ambiguous","old_string":"ripetuto","new_string":"unico","if_match":%q}`, hash)
	resps2 := runMCPSequence(t, s, []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"concept_patch","arguments":` + patchArgs + `}}`,
	})
	trPatch := decodeToolResult(t, resps2[1])
	if !trPatch.IsError {
		t.Fatal("concept_patch with ambiguous old_string: expected isError=true")
	}
	if !strings.Contains(trPatch.Content[0].Text, "old_string_ambiguous") {
		t.Errorf("concept_patch: expected 'old_string_ambiguous': %s", trPatch.Content[0].Text)
	}
}

func TestServer_ConceptPatch_ReplaceAll(t *testing.T) {
	k := setupTestKB(t)
	s := New("1.0.0")
	RegisterKBTools(s, k, Deps{})

	msgs := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"concept_write","arguments":{"id":"note/patch-replaceall","frontmatter":{"type":"Note"},"body":"# Corpo\n\nripetuto qui, ripetuto ancora, e ripetuto di nuovo.\n"}}}`,
	}
	resps := runMCPSequence(t, s, msgs)
	trWrite := decodeToolResult(t, resps[1])
	if trWrite.IsError {
		t.Fatalf("concept_write: unexpected error: %v", trWrite.Content)
	}
	hash := conceptHash(t, trWrite)

	patchArgs := fmt.Sprintf(`{"id":"note/patch-replaceall","old_string":"ripetuto","new_string":"unico","replace_all":true,"if_match":%q}`, hash)
	resps2 := runMCPSequence(t, s, []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"concept_patch","arguments":` + patchArgs + `}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"concept_read","arguments":{"id":"note/patch-replaceall"}}}`,
	})
	trPatch := decodeToolResult(t, resps2[1])
	if trPatch.IsError {
		t.Fatalf("concept_patch replace_all: unexpected error: %v", trPatch.Content)
	}
	var patchResult struct {
		Replacements int `json:"replacements"`
	}
	if err := json.Unmarshal([]byte(trPatch.Content[0].Text), &patchResult); err != nil {
		t.Fatalf("decode concept_patch result: %v", err)
	}
	if patchResult.Replacements != 3 {
		t.Errorf("concept_patch replace_all: expected 3 replacements, got %d", patchResult.Replacements)
	}

	trRead := decodeToolResult(t, resps2[2])
	if trRead.IsError {
		t.Fatalf("concept_read: unexpected error: %v", trRead.Content)
	}
	if strings.Contains(trRead.Content[0].Text, "ripetuto") {
		t.Errorf("concept_patch replace_all: 'ripetuto' should have been fully replaced: %s", trRead.Content[0].Text)
	}
}

// --- concept_patch batch edits (D76 WP1) ---

func TestServer_ConceptPatch_BatchEdits(t *testing.T) {
	k := setupTestKB(t)
	s := New("1.0.0")
	RegisterKBTools(s, k, Deps{})

	msgs := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"concept_write","arguments":{"id":"note/patch-batch","frontmatter":{"type":"Note"},"body":"# Titolo\n\nRiga uno.\nRiga due.\nRiga tre.\n"}}}`,
	}
	resps := runMCPSequence(t, s, msgs)
	trWrite := decodeToolResult(t, resps[1])
	if trWrite.IsError {
		t.Fatalf("concept_write: unexpected error: %v", trWrite.Content)
	}
	hash := conceptHash(t, trWrite)

	// Third edit matches text introduced by the second edit, verifying
	// sequential application (each edit sees the previous edit's result).
	patchArgs := fmt.Sprintf(`{"id":"note/patch-batch","edits":[{"old_string":"Riga uno.","new_string":"Prima riga."},{"old_string":"Riga due.","new_string":"Seconda riga, con MARKER."},{"old_string":"MARKER","new_string":"segnaposto"}],"if_match":%q}`, hash)
	resps2 := runMCPSequence(t, s, []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"concept_patch","arguments":` + patchArgs + `}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"concept_read","arguments":{"id":"note/patch-batch"}}}`,
	})

	trPatch := decodeToolResult(t, resps2[1])
	if trPatch.IsError {
		t.Fatalf("concept_patch batch: unexpected error: %v", trPatch.Content)
	}
	var patchResult struct {
		Replacements int `json:"replacements"`
	}
	if err := json.Unmarshal([]byte(trPatch.Content[0].Text), &patchResult); err != nil {
		t.Fatalf("decode concept_patch result: %v", err)
	}
	if patchResult.Replacements != 3 {
		t.Errorf("concept_patch batch: expected 3 replacements, got %d", patchResult.Replacements)
	}

	trRead := decodeToolResult(t, resps2[2])
	if trRead.IsError {
		t.Fatalf("concept_read: unexpected error: %v", trRead.Content)
	}
	text := trRead.Content[0].Text
	for _, want := range []string{"Prima riga.", "Seconda riga, con segnaposto.", "Riga tre."} {
		if !strings.Contains(text, want) {
			t.Errorf("concept_patch batch: expected %q present: %s", want, text)
		}
	}
	for _, unwanted := range []string{"Riga uno.", "Riga due.", "MARKER"} {
		if strings.Contains(text, unwanted) {
			t.Errorf("concept_patch batch: unexpected %q still present: %s", unwanted, text)
		}
	}
}

func TestServer_ConceptPatch_BatchEdits_FailsMidway(t *testing.T) {
	k := setupTestKB(t)
	s := New("1.0.0")
	RegisterKBTools(s, k, Deps{})

	body := "# Titolo\n\nRiga uno.\nRiga due.\n"
	msgs := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"concept_write","arguments":{"id":"note/patch-batch-fail","frontmatter":{"type":"Note"},"body":"` + strings.ReplaceAll(body, "\n", `\n`) + `"}}}`,
	}
	resps := runMCPSequence(t, s, msgs)
	trWrite := decodeToolResult(t, resps[1])
	if trWrite.IsError {
		t.Fatalf("concept_write: unexpected error: %v", trWrite.Content)
	}
	hash := conceptHash(t, trWrite)

	// Second edit's old_string does not exist: the whole batch must fail and
	// nothing (not even the first edit) must be written.
	patchArgs := fmt.Sprintf(`{"id":"note/patch-batch-fail","edits":[{"old_string":"Riga uno.","new_string":"Prima riga."},{"old_string":"Testo assente","new_string":"x"}],"if_match":%q}`, hash)
	resps2 := runMCPSequence(t, s, []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"concept_patch","arguments":` + patchArgs + `}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"concept_read","arguments":{"id":"note/patch-batch-fail"}}}`,
	})

	trPatch := decodeToolResult(t, resps2[1])
	if !trPatch.IsError {
		t.Fatal("concept_patch batch with failing edit: expected isError=true")
	}
	if !strings.Contains(trPatch.Content[0].Text, "old_string_not_found") {
		t.Errorf("concept_patch batch: expected 'old_string_not_found': %s", trPatch.Content[0].Text)
	}
	if !strings.Contains(trPatch.Content[0].Text, "edit 2 of 2") {
		t.Errorf("concept_patch batch: expected failing edit index in message: %s", trPatch.Content[0].Text)
	}

	trRead := decodeToolResult(t, resps2[2])
	if trRead.IsError {
		t.Fatalf("concept_read: unexpected error: %v", trRead.Content)
	}
	if !strings.Contains(trRead.Content[0].Text, "Riga uno.") {
		t.Errorf("concept_patch batch: body should be unchanged after mid-batch failure: %s", trRead.Content[0].Text)
	}
	if strings.Contains(trRead.Content[0].Text, "Prima riga.") {
		t.Errorf("concept_patch batch: first edit must not have been applied after mid-batch failure: %s", trRead.Content[0].Text)
	}
}

func TestServer_ConceptPatch_EditsAndTopLevelMutuallyExclusive(t *testing.T) {
	k := setupTestKB(t)
	s := New("1.0.0")
	RegisterKBTools(s, k, Deps{})

	msgs := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"concept_write","arguments":{"id":"note/patch-mutex","frontmatter":{"type":"Note"},"body":"# Corpo\n\nTesto.\n"}}}`,
	}
	resps := runMCPSequence(t, s, msgs)
	trWrite := decodeToolResult(t, resps[1])
	if trWrite.IsError {
		t.Fatalf("concept_write: unexpected error: %v", trWrite.Content)
	}
	hash := conceptHash(t, trWrite)

	patchArgs := fmt.Sprintf(`{"id":"note/patch-mutex","old_string":"Testo.","new_string":"x","edits":[{"old_string":"Testo.","new_string":"y"}],"if_match":%q}`, hash)
	resps2 := runMCPSequence(t, s, []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"concept_patch","arguments":` + patchArgs + `}}`,
	})
	trPatch := decodeToolResult(t, resps2[1])
	if !trPatch.IsError {
		t.Fatal("concept_patch with both edits and top-level old_string: expected isError=true")
	}
	if !strings.Contains(trPatch.Content[0].Text, "mutually exclusive") {
		t.Errorf("concept_patch: expected mutual-exclusion error: %s", trPatch.Content[0].Text)
	}
}

func TestServer_ConceptPatch_EmptyEditsArray(t *testing.T) {
	k := setupTestKB(t)
	s := New("1.0.0")
	RegisterKBTools(s, k, Deps{})

	msgs := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"concept_write","arguments":{"id":"note/patch-empty-edits","frontmatter":{"type":"Note"},"body":"# Corpo\n\nTesto.\n"}}}`,
	}
	resps := runMCPSequence(t, s, msgs)
	trWrite := decodeToolResult(t, resps[1])
	if trWrite.IsError {
		t.Fatalf("concept_write: unexpected error: %v", trWrite.Content)
	}
	hash := conceptHash(t, trWrite)

	patchArgs := fmt.Sprintf(`{"id":"note/patch-empty-edits","edits":[],"if_match":%q}`, hash)
	resps2 := runMCPSequence(t, s, []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"concept_patch","arguments":` + patchArgs + `}}`,
	})
	trPatch := decodeToolResult(t, resps2[1])
	if !trPatch.IsError {
		t.Fatal("concept_patch with empty edits: expected isError=true")
	}
	if !strings.Contains(trPatch.Content[0].Text, "cannot be empty") {
		t.Errorf("concept_patch: expected 'cannot be empty' error: %s", trPatch.Content[0].Text)
	}
}

func TestServer_ConceptPatch_BatchEditsWithFrontmatter(t *testing.T) {
	k := setupTestKB(t)
	s := New("1.0.0")
	RegisterKBTools(s, k, Deps{})

	msgs := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"concept_write","arguments":{"id":"note/patch-batch-fm","frontmatter":{"type":"Note","status":"draft"},"body":"# Corpo\n\nRiga uno.\nRiga due.\n"}}}`,
	}
	resps := runMCPSequence(t, s, msgs)
	trWrite := decodeToolResult(t, resps[1])
	if trWrite.IsError {
		t.Fatalf("concept_write: unexpected error: %v", trWrite.Content)
	}
	hash := conceptHash(t, trWrite)

	patchArgs := fmt.Sprintf(`{"id":"note/patch-batch-fm","edits":[{"old_string":"Riga uno.","new_string":"Prima."},{"old_string":"Riga due.","new_string":"Seconda."}],"frontmatter":{"status":"final"},"if_match":%q}`, hash)
	resps2 := runMCPSequence(t, s, []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"concept_patch","arguments":` + patchArgs + `}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"concept_read","arguments":{"id":"note/patch-batch-fm"}}}`,
	})

	trPatch := decodeToolResult(t, resps2[1])
	if trPatch.IsError {
		t.Fatalf("concept_patch batch+frontmatter: unexpected error: %v", trPatch.Content)
	}

	trRead := decodeToolResult(t, resps2[2])
	if trRead.IsError {
		t.Fatalf("concept_read: unexpected error: %v", trRead.Content)
	}
	text := trRead.Content[0].Text
	if !strings.Contains(text, "Prima.") || !strings.Contains(text, "Seconda.") {
		t.Errorf("concept_patch batch+frontmatter: expected both edits applied: %s", text)
	}
	if !strings.Contains(text, "status: final") && !strings.Contains(text, `"status":"final"`) && !strings.Contains(text, "status:final") {
		t.Errorf("concept_patch batch+frontmatter: expected frontmatter status=final: %s", text)
	}
}

// --- search title/snippet (D70) ---

// TestServer_Search_TitleAndSnippet verifies that search results carry a
// title (from frontmatter) and a snippet (excerpt around the query term)
// without requiring a concept_read, across the in-memory keyword path.
func TestServer_Search_TitleAndSnippet(t *testing.T) {
	k := setupTestKB(t)
	s := New("1.0.0")
	RegisterKBTools(s, k, Deps{})

	uniqueKW := "flimwhistle4471"
	body := "# Introduzione\\n\\nTesto di riempimento prima del termine cercato: " + uniqueKW + " e testo dopo.\\n"

	msgs := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"concept_write","arguments":{"id":"notes/snippet-test","frontmatter":{"type":"Note","title":"Snippet Test Title"},"body":"` + body + `"}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"search","arguments":{"query":"` + uniqueKW + `"}}}`,
	}
	resps := runMCPSequence(t, s, msgs)

	trWrite := decodeToolResult(t, resps[1])
	if trWrite.IsError {
		t.Fatalf("concept_write: unexpected error: %v", trWrite.Content)
	}

	trSearch := decodeToolResult(t, resps[2])
	if trSearch.IsError {
		t.Fatalf("search: unexpected error: %v", trSearch.Content)
	}

	var result struct {
		Results []struct {
			ID      string `json:"id"`
			Title   string `json:"title"`
			Snippet string `json:"snippet"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(trSearch.Content[0].Text), &result); err != nil {
		t.Fatalf("decode search result: %v", err)
	}

	var hit *struct {
		ID      string `json:"id"`
		Title   string `json:"title"`
		Snippet string `json:"snippet"`
	}
	for i := range result.Results {
		if result.Results[i].ID == "notes/snippet-test" {
			hit = &result.Results[i]
		}
	}
	if hit == nil {
		t.Fatalf("search: expected hit for notes/snippet-test, got %+v", result.Results)
	}
	if hit.Title != "Snippet Test Title" {
		t.Errorf("search: expected title 'Snippet Test Title', got %q", hit.Title)
	}
	if !strings.Contains(hit.Snippet, uniqueKW) {
		t.Errorf("search: expected snippet to contain query term %q, got %q", uniqueKW, hit.Snippet)
	}
	if len(hit.Snippet) > 260 {
		t.Errorf("search: snippet too long (%d chars), expected ~200: %q", len(hit.Snippet), hit.Snippet)
	}
}
