package mcpserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// artifactCallMsg builds a single-line JSON-RPC tools/call request for name
// with the given arguments (marshaled as JSON), safe for content containing
// quotes/newlines — unlike the raw string literals used elsewhere in this
// package for simple, static argument sets.
func artifactCallMsg(t *testing.T, id int, name string, args map[string]any) string {
	t.Helper()
	env := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      name,
			"arguments": args,
		},
	}
	b, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("artifactCallMsg: marshal: %v", err)
	}
	return string(b)
}

const initMsg = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`

// --- classifyArtifactPath: whitelist / traversal (D71 WP4) ---

func TestClassifyArtifactPath(t *testing.T) {
	cases := []struct {
		path    string
		wantOK  bool
		wantKnd string
		wantNm  string
	}{
		{"skills/my-skill/SKILL.md", true, "skill", "my-skill"},
		{"skills/kbinfra--query-rete/SKILL.md", true, "skill", "kbinfra--query-rete"},
		{"skills/my-skill/scripts/run.sh", true, "skill", "my-skill"},
		{"agents/my-agent.md", true, "agent", "my-agent"},
		{"hooks/my-hook/hook.json", true, "hook", "my-hook"},
		{"mcp/my-server.json", true, "mcp", "my-server"},
		{"instructions.md", true, "instructions", ""},
		// Traversal.
		{"../etc/passwd", false, "", ""},
		{"skills/../../etc/passwd", false, "", ""},
		{"/etc/passwd", false, "", ""},
		// Kind not allowed / malformed.
		{"data/some-concept.md", false, "", ""},
		{"skills/my-skill", false, "", ""}, // no file, directory only
		{"agents/my-agent.txt", false, "", ""},
		{"AGENTS.md", false, "", ""},
		{"", false, "", ""},
	}

	for _, c := range cases {
		info, err := classifyArtifactPath(c.path)
		if c.wantOK && err != nil {
			t.Errorf("classifyArtifactPath(%q): unexpected error: %v", c.path, err)
			continue
		}
		if !c.wantOK && err == nil {
			t.Errorf("classifyArtifactPath(%q): expected error, got kind=%q name=%q", c.path, info.Kind, info.Name)
			continue
		}
		if c.wantOK {
			if info.Kind != c.wantKnd || info.Name != c.wantNm {
				t.Errorf("classifyArtifactPath(%q): got kind=%q name=%q, want kind=%q name=%q",
					c.path, info.Kind, info.Name, c.wantKnd, c.wantNm)
			}
		}
	}
}

// --- flag disabled: write/delete not registered (D71 WP4) ---

func TestArtifactTools_FlagDisabled_WriteDeleteNotRegistered(t *testing.T) {
	k := setupTestKB(t) // AllowArtifactWrite defaults to false
	s := New("test")
	RegisterKBTools(s, k, Deps{})

	names := listToolNames(t, s)
	if !names["artifact_read"] {
		t.Error("artifact_read: expected to be registered regardless of AllowArtifactWrite")
	}
	if !names["artifact_list"] {
		t.Error("artifact_list: expected to be registered regardless of AllowArtifactWrite")
	}
	if names["artifact_write"] {
		t.Error("artifact_write: must not be registered when AllowArtifactWrite=false")
	}
	if names["artifact_delete"] {
		t.Error("artifact_delete: must not be registered when AllowArtifactWrite=false")
	}

	// Also unreachable via tools/call: a real registry, not just hidden from tools/list.
	resps := runMCPSequence(t, s, []string{
		initMsg,
		artifactCallMsg(t, 2, "artifact_write", map[string]any{"path": "instructions.md", "content": "x"}),
	})
	tr := decodeToolResult(t, resps[1])
	if !tr.IsError || !containsText(tr, "tool not found") {
		t.Fatalf("artifact_write: expected \"tool not found\" when unregistered, got: %+v", tr)
	}
}

// --- flag enabled: golden profile (D71 WP4) ---

func TestArtifactTools_FlagEnabled_ProfileClassification(t *testing.T) {
	k := setupTestKB(t)
	k.AllowArtifactWrite = true
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	s.SetToolsProfile("agent")

	names := listToolNames(t, s)
	if !names["artifact_write"] {
		t.Error("artifact_write: expected agent-visible (not advanced) when AllowArtifactWrite=true")
	}
	if names["artifact_delete"] {
		t.Error("artifact_delete: expected hidden (advanced) under the agent profile")
	}
	if names["artifact_list"] {
		t.Error("artifact_list: expected hidden (advanced) under the agent profile")
	}

	// artifact_delete stays callable via tools/call despite being hidden.
	resps := runMCPSequence(t, s, []string{
		initMsg,
		artifactCallMsg(t, 2, "artifact_delete", map[string]any{"path": "instructions.md", "if_match": "deadbeef"}),
	})
	tr := decodeToolResult(t, resps[1])
	if !tr.IsError {
		t.Fatal("artifact_delete: expected an application error (not_found), got success")
	}
}

// --- write/read/delete round-trip + stale_write/already_exists (D71 WP4) ---

func TestArtifactTools_Skill_WriteReadListDelete(t *testing.T) {
	k := setupTestKB(t)
	k.AllowArtifactWrite = true
	s := New("test")
	RegisterKBTools(s, k, Deps{})

	skillContent := "---\nname: my-test-skill\ndescription: A test skill\n---\nBody.\n"

	// Create (no if_match).
	resps := runMCPSequence(t, s, []string{
		initMsg,
		artifactCallMsg(t, 2, "artifact_write", map[string]any{
			"path": "skills/my-test-skill/SKILL.md", "content": skillContent,
		}),
	})
	tr := decodeToolResult(t, resps[1])
	if tr.IsError {
		t.Fatalf("artifact_write (create): unexpected error: %v", tr.Content)
	}
	var writeRes struct {
		SHA256 string `json:"sha256"`
	}
	if err := json.Unmarshal([]byte(tr.Content[0].Text), &writeRes); err != nil {
		t.Fatalf("decode artifact_write result: %v", err)
	}

	// Create again without if_match: already_exists.
	resps = runMCPSequence(t, s, []string{
		initMsg,
		artifactCallMsg(t, 2, "artifact_write", map[string]any{
			"path": "skills/my-test-skill/SKILL.md", "content": skillContent,
		}),
	})
	tr = decodeToolResult(t, resps[1])
	if !tr.IsError || !containsText(tr, "already_exists") {
		t.Fatalf("artifact_write (re-create, no if_match): expected already_exists error, got: %+v", tr)
	}

	// Overwrite with wrong if_match: stale_write.
	resps = runMCPSequence(t, s, []string{
		initMsg,
		artifactCallMsg(t, 2, "artifact_write", map[string]any{
			"path": "skills/my-test-skill/SKILL.md", "content": skillContent, "if_match": "0000",
		}),
	})
	tr = decodeToolResult(t, resps[1])
	if !tr.IsError || !containsText(tr, "stale_write") {
		t.Fatalf("artifact_write (wrong if_match): expected stale_write error, got: %+v", tr)
	}

	// Read back.
	resps = runMCPSequence(t, s, []string{
		initMsg,
		artifactCallMsg(t, 2, "artifact_read", map[string]any{"path": "skills/my-test-skill/SKILL.md"}),
	})
	tr = decodeToolResult(t, resps[1])
	if tr.IsError {
		t.Fatalf("artifact_read: unexpected error: %v", tr.Content)
	}
	var readRes struct {
		Content string `json:"content"`
		SHA256  string `json:"sha256"`
	}
	if err := json.Unmarshal([]byte(tr.Content[0].Text), &readRes); err != nil {
		t.Fatalf("decode artifact_read result: %v", err)
	}
	if readRes.Content != skillContent {
		t.Errorf("artifact_read: content mismatch: got %q want %q", readRes.Content, skillContent)
	}
	if readRes.SHA256 != writeRes.SHA256 {
		t.Errorf("artifact_read: sha256 mismatch with artifact_write result")
	}

	// List shows it.
	resps = runMCPSequence(t, s, []string{
		initMsg,
		artifactCallMsg(t, 2, "artifact_list", map[string]any{}),
	})
	tr = decodeToolResult(t, resps[1])
	if tr.IsError {
		t.Fatalf("artifact_list: unexpected error: %v", tr.Content)
	}
	var entries []artifactEntry
	if err := json.Unmarshal([]byte(tr.Content[0].Text), &entries); err != nil {
		t.Fatalf("decode artifact_list result: %v", err)
	}
	found := false
	for _, e := range entries {
		if e.Kind == "skill" && e.Name == "my-test-skill" {
			found = true
			if len(e.Files) != 1 || e.Files[0].Path != "skills/my-test-skill/SKILL.md" || e.Files[0].SHA256 != writeRes.SHA256 {
				t.Errorf("artifact_list: unexpected entry for my-test-skill: %+v", e)
			}
		}
	}
	if !found {
		t.Errorf("artifact_list: my-test-skill not found in %+v", entries)
	}

	// Overwrite with correct if_match: success.
	updated := "---\nname: my-test-skill\ndescription: Updated\n---\nBody v2.\n"
	resps = runMCPSequence(t, s, []string{
		initMsg,
		artifactCallMsg(t, 2, "artifact_write", map[string]any{
			"path": "skills/my-test-skill/SKILL.md", "content": updated, "if_match": writeRes.SHA256,
		}),
	})
	tr = decodeToolResult(t, resps[1])
	if tr.IsError {
		t.Fatalf("artifact_write (update, correct if_match): unexpected error: %v", tr.Content)
	}
	var updateRes struct {
		SHA256 string `json:"sha256"`
	}
	json.Unmarshal([]byte(tr.Content[0].Text), &updateRes)

	// Delete with stale if_match: rejected.
	resps = runMCPSequence(t, s, []string{
		initMsg,
		artifactCallMsg(t, 2, "artifact_delete", map[string]any{
			"path": "skills/my-test-skill/SKILL.md", "if_match": writeRes.SHA256, // now stale
		}),
	})
	tr = decodeToolResult(t, resps[1])
	if !tr.IsError || !containsText(tr, "stale_write") {
		t.Fatalf("artifact_delete (stale if_match): expected stale_write error, got: %+v", tr)
	}

	// Delete with correct if_match: success, and the now-empty skill dir is removed.
	resps = runMCPSequence(t, s, []string{
		initMsg,
		artifactCallMsg(t, 2, "artifact_delete", map[string]any{
			"path": "skills/my-test-skill/SKILL.md", "if_match": updateRes.SHA256,
		}),
	})
	tr = decodeToolResult(t, resps[1])
	if tr.IsError {
		t.Fatalf("artifact_delete: unexpected error: %v", tr.Content)
	}
	if _, err := os.Stat(filepath.Join(k.Root, "skills", "my-test-skill")); !os.IsNotExist(err) {
		t.Errorf("artifact_delete: expected skills/my-test-skill/ to be removed (now empty), stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(k.Root, "skills")); err != nil {
		t.Errorf("artifact_delete: skills/ (shared parent) must survive: %v", err)
	}
}

// --- invalid SKILL.md rejected before write (D71 WP4) ---

func TestArtifactTools_SkillWrite_InvalidRejected(t *testing.T) {
	k := setupTestKB(t)
	k.AllowArtifactWrite = true
	s := New("test")
	RegisterKBTools(s, k, Deps{})

	cases := []struct {
		name    string
		content string
	}{
		{"name mismatch", "---\nname: other-name\ndescription: desc\n---\nBody.\n"},
		{"missing description", "---\nname: bad-skill\n---\nBody.\n"},
		{"no frontmatter", "Just a body, no frontmatter.\n"},
	}

	for _, c := range cases {
		resps := runMCPSequence(t, s, []string{
			initMsg,
			artifactCallMsg(t, 2, "artifact_write", map[string]any{
				"path": "skills/bad-skill/SKILL.md", "content": c.content,
			}),
		})
		tr := decodeToolResult(t, resps[1])
		if !tr.IsError {
			t.Errorf("%s: expected artifact_write to reject invalid SKILL.md, got success", c.name)
		}
	}

	if _, err := os.Stat(filepath.Join(k.Root, "skills", "bad-skill", "SKILL.md")); !os.IsNotExist(err) {
		t.Error("invalid SKILL.md must not have been written to disk")
	}
}

// --- invalid mcp/*.json rejected before write ---

func TestArtifactTools_MCPWrite_InvalidRejected(t *testing.T) {
	k := setupTestKB(t)
	k.AllowArtifactWrite = true
	s := New("test")
	RegisterKBTools(s, k, Deps{})

	resps := runMCPSequence(t, s, []string{
		initMsg,
		artifactCallMsg(t, 2, "artifact_write", map[string]any{
			"path": "mcp/my-server.json", "content": `{"type":"http","headers":{"Authorization":"Bearer literal-secret"}}`,
		}),
	})
	tr := decodeToolResult(t, resps[1])
	if !tr.IsError {
		t.Fatal("artifact_write: expected rejection of a literal-secret header in mcp/*.json")
	}
}

// --- path whitelist enforced through the tool, not just the unit helper ---

func TestArtifactTools_PathTraversal_Rejected(t *testing.T) {
	k := setupTestKB(t)
	k.AllowArtifactWrite = true
	s := New("test")
	RegisterKBTools(s, k, Deps{})

	resps := runMCPSequence(t, s, []string{
		initMsg,
		artifactCallMsg(t, 2, "artifact_read", map[string]any{"path": "../../../etc/passwd"}),
	})
	tr := decodeToolResult(t, resps[1])
	if !tr.IsError {
		t.Fatal("artifact_read: expected rejection of a path-traversal path")
	}

	resps = runMCPSequence(t, s, []string{
		initMsg,
		artifactCallMsg(t, 2, "artifact_write", map[string]any{"path": "data/index.md", "content": "x"}),
	})
	tr = decodeToolResult(t, resps[1])
	if !tr.IsError {
		t.Fatal("artifact_write: expected rejection of a non-whitelisted kind (data/)")
	}
}

// containsText reports whether any content block of tr contains substr.
func containsText(tr ToolResult, substr string) bool {
	for _, c := range tr.Content {
		if strings.Contains(c.Text, substr) {
			return true
		}
	}
	return false
}
