package mcpserver

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestArtifactTools_BinaryAndExecutableRoundTrip(t *testing.T) {
	k := setupTestKB(t)
	k.AllowArtifactWrite = true
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	data := []byte{0x00, 0xff, 0x80, 0x01}
	path := "skills/bin/assets/icon.bin"
	resps := runMCPSequence(t, s, []string{initMsg, artifactCallMsg(t, 2, "artifact_write", map[string]any{
		"path": path, "content": base64.StdEncoding.EncodeToString(data), "encoding": "base64", "executable": true,
	})})
	if tr := decodeToolResult(t, resps[1]); tr.IsError {
		t.Fatalf("binary write: %+v", tr.Content)
	}
	resps = runMCPSequence(t, s, []string{initMsg, artifactCallMsg(t, 2, "artifact_read", map[string]any{"path": path})})
	tr := decodeToolResult(t, resps[1])
	if tr.IsError {
		t.Fatalf("binary read: %+v", tr.Content)
	}
	var read struct{ Content, Encoding string }
	if err := json.Unmarshal([]byte(tr.Content[0].Text), &read); err != nil {
		t.Fatal(err)
	}
	if read.Encoding != "base64" || read.Content != base64.StdEncoding.EncodeToString(data) {
		t.Fatalf("binary read = %#v", read)
	}
	info, err := os.Stat(filepath.Join(k.Root, filepath.FromSlash(path)))
	if err != nil || info.Mode()&0o111 == 0 {
		t.Fatalf("executable mode = %v, %v", info.Mode(), err)
	}
}

func TestArtifactTools_ExecutableTriState(t *testing.T) {
	k := setupTestKB(t)
	k.AllowArtifactWrite = true
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	path := "skills/mode/assets/run.sh"
	call := func(args map[string]any) ToolResult {
		resps := runMCPSequence(t, s, []string{initMsg, artifactCallMsg(t, 2, "artifact_write", args)})
		return decodeToolResult(t, resps[1])
	}
	tr := call(map[string]any{"path": path, "content": "one", "executable": true})
	if tr.IsError {
		t.Fatalf("create: %+v", tr.Content)
	}
	var first struct {
		SHA256 string `json:"sha256"`
	}
	if err := json.Unmarshal([]byte(tr.Content[0].Text), &first); err != nil {
		t.Fatal(err)
	}
	mode := func() os.FileMode {
		info, err := os.Stat(filepath.Join(k.Root, filepath.FromSlash(path)))
		if err != nil {
			t.Fatal(err)
		}
		return info.Mode()
	}
	if mode()&0o111 == 0 {
		t.Fatal("explicit true did not set executable")
	}
	tr = call(map[string]any{"path": path, "content": "two", "if_match": first.SHA256})
	if tr.IsError || mode()&0o111 == 0 {
		t.Fatalf("omitted executable did not preserve mode: %+v", tr.Content)
	}
	var second struct {
		SHA256 string `json:"sha256"`
	}
	if err := json.Unmarshal([]byte(tr.Content[0].Text), &second); err != nil {
		t.Fatal(err)
	}
	tr = call(map[string]any{"path": path, "content": "three", "if_match": second.SHA256, "executable": false})
	if tr.IsError || mode()&0o111 != 0 {
		t.Fatalf("explicit false did not clear executable: %+v", tr.Content)
	}
}

func TestArtifactTools_SymlinksRejected(t *testing.T) {
	cases := []struct {
		name  string
		tool  string
		path  string
		args  map[string]any
		setup func(t *testing.T, root, target string)
	}{
		{
			name: "read final file", tool: "artifact_read", path: "skills/read/assets/link.bin",
			setup: func(t *testing.T, root, target string) {
				t.Helper()
				if err := os.MkdirAll(filepath.Join(root, "skills", "read", "assets"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, filepath.Join(root, "skills", "read", "assets", "link.bin")); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "write final file", tool: "artifact_write", path: "skills/write/assets/link.bin",
			args: map[string]any{"content": "replacement"},
			setup: func(t *testing.T, root, target string) {
				t.Helper()
				if err := os.MkdirAll(filepath.Join(root, "skills", "write", "assets"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, filepath.Join(root, "skills", "write", "assets", "link.bin")); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "delete final file", tool: "artifact_delete", path: "skills/delete/assets/link.bin",
			args: map[string]any{"if_match": sha256Hex([]byte("outside"))},
			setup: func(t *testing.T, root, target string) {
				t.Helper()
				if err := os.MkdirAll(filepath.Join(root, "skills", "delete", "assets"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, filepath.Join(root, "skills", "delete", "assets", "link.bin")); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "list tree", tool: "artifact_list",
			setup: func(t *testing.T, root, target string) {
				t.Helper()
				if err := os.MkdirAll(filepath.Join(root, "skills", "list"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, filepath.Join(root, "skills", "list", "link.bin")); err != nil {
					t.Fatal(err)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			k := setupTestKB(t)
			k.AllowArtifactWrite = true
			target := filepath.Join(t.TempDir(), "outside.bin")
			if err := os.WriteFile(target, []byte("outside"), 0o644); err != nil {
				t.Fatal(err)
			}
			tc.setup(t, k.Root, target)

			s := New("test")
			RegisterKBTools(s, k, Deps{})
			args := make(map[string]any, len(tc.args)+1)
			for key, value := range tc.args {
				args[key] = value
			}
			if tc.tool != "artifact_list" {
				args["path"] = tc.path
			}
			resps := runMCPSequence(t, s, []string{initMsg, artifactCallMsg(t, 2, tc.tool, args)})
			tr := decodeToolResult(t, resps[1])
			if !tr.IsError || !containsText(tr, "symlink") {
				t.Fatalf("%s: expected symlink rejection, got %+v", tc.tool, tr.Content)
			}
			if data, err := os.ReadFile(target); err != nil || string(data) != "outside" {
				t.Fatalf("%s changed symlink target: data=%q err=%v", tc.tool, data, err)
			}
		})
	}
}

func TestArtifactTools_ListReportsExecutableAuxiliarySkillFile(t *testing.T) {
	k := setupTestKB(t)
	skillDir := filepath.Join(k.Root, "skills", "aux")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: aux\ndescription: test\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "run.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	s := New("test")
	RegisterKBTools(s, k, Deps{})
	resps := runMCPSequence(t, s, []string{initMsg, artifactCallMsg(t, 2, "artifact_list", map[string]any{})})
	tr := decodeToolResult(t, resps[1])
	if tr.IsError {
		t.Fatalf("artifact_list: %+v", tr.Content)
	}
	var entries []artifactEntry
	if err := json.Unmarshal([]byte(tr.Content[0].Text), &entries); err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Kind != "skill" || entry.Name != "aux" {
			continue
		}
		for _, file := range entry.Files {
			if file.Path == "skills/aux/run.sh" {
				if !file.Executable {
					t.Fatal("artifact_list marked executable auxiliary file as non-executable")
				}
				return
			}
		}
	}
	t.Fatalf("artifact_list did not return skills/aux/run.sh: %+v", entries)
}

func TestArtifactTools_InvalidBase64DoesNotWrite(t *testing.T) {
	k := setupTestKB(t)
	k.AllowArtifactWrite = true
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	path := "skills/b64/assets/file.bin"
	resps := runMCPSequence(t, s, []string{initMsg, artifactCallMsg(t, 2, "artifact_write", map[string]any{"path": path, "content": "%%%", "encoding": "base64"})})
	tr := decodeToolResult(t, resps[1])
	if !tr.IsError || !containsText(tr, "invalid") {
		t.Fatalf("invalid base64: %+v", tr.Content)
	}
	if _, err := os.Stat(filepath.Join(k.Root, filepath.FromSlash(path))); !os.IsNotExist(err) {
		t.Fatalf("invalid base64 wrote a file: %v", err)
	}
}

func TestArtifactTools_TextEncodingAndDecodedLimits(t *testing.T) {
	k := setupTestKB(t)
	k.AllowArtifactWrite = true
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	call := func(args map[string]any) ToolResult {
		r := runMCPSequence(t, s, []string{initMsg, artifactCallMsg(t, 2, "artifact_write", args)})
		return decodeToolResult(t, r[1])
	}
	if tr := call(map[string]any{"path": "skills/text/assets/x", "content": "%%%", "encoding": "text"}); tr.IsError {
		t.Fatalf("text must remain text: %+v", tr.Content)
	}
	if got, _ := os.ReadFile(filepath.Join(k.Root, "skills", "text", "assets", "x")); string(got) != "%%%" {
		t.Fatalf("text changed: %q", got)
	}
	over := base64.StdEncoding.EncodeToString(make([]byte, artifactMaxFileSize+1))
	if tr := call(map[string]any{"path": "skills/text/assets/large", "content": over, "encoding": "base64"}); !tr.IsError || !containsText(tr, "decoded content") {
		t.Fatalf("oversize: %+v", tr.Content)
	}
	if tr := call(map[string]any{"path": "skills/bad/SKILL.md", "content": base64.StdEncoding.EncodeToString([]byte{0xff}), "encoding": "base64"}); !tr.IsError || !containsText(tr, "UTF-8") {
		t.Fatalf("structured binary: %+v", tr.Content)
	}
}

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
	if !names["template_list"] {
		t.Error("template_list: expected agent-visible when templates are KB-owned discovery data")
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

// --- D151: a KB's capabilities are visible from a session ---

func TestKBStatus_ReportsCapabilities(t *testing.T) {
	k := setupTestKB(t)
	s := New("test")
	RegisterKBTools(s, k, Deps{})

	read := func() map[string]map[string]string {
		t.Helper()
		res, err := s.Tools()["kb_status"].Handler(authLocalContext(), json.RawMessage(`{}`))
		if err != nil || res.IsError {
			t.Fatalf("kb_status = %+v, err=%v", res, err)
		}
		var out struct {
			Capabilities map[string]map[string]string `json:"capabilities"`
		}
		if err := json.Unmarshal([]byte(res.Content[0].Text), &out); err != nil {
			t.Fatal(err)
		}
		if out.Capabilities == nil {
			t.Fatalf("kb_status returned no capabilities section: %s", res.Content[0].Text)
		}
		return out.Capabilities
	}

	caps := read()
	if got := caps["artifact_write"]["state"]; got != "disabled" {
		t.Errorf("artifact_write state = %q, want disabled", got)
	}
	// A state without the name of the switch is only half an answer.
	if got := caps["artifact_write"]["setting"]; got != "kbs[].allow_artifact_write" {
		t.Errorf("artifact_write setting = %q", got)
	}
	if got := caps["mount"]["state"]; got != "configured" {
		t.Errorf("mount state = %q, want configured", got)
	}
	if got := caps["tool_prefix"]["state"]; got != "none" {
		t.Errorf("tool_prefix state = %q, want none", got)
	}

	k.AllowArtifactWrite = true
	k.Discovered = true
	k.ToolPrefix = "ai_team"
	caps = read()
	if got := caps["artifact_write"]["state"]; got != "enabled" {
		t.Errorf("after enabling, artifact_write state = %q", got)
	}
	if got := caps["mount"]["state"]; got != "discovered" {
		t.Errorf("mount state = %q, want discovered", got)
	}
	if got := caps["tool_prefix"]["state"]; got != "ai_team" {
		t.Errorf("tool_prefix state = %q, want ai_team", got)
	}
}

// The three cases a client cannot distinguish, and the one the server can.
func TestToolsCall_UnknownToolMessages(t *testing.T) {
	t.Run("a gated tool names its setting", func(t *testing.T) {
		got := unknownToolMessage("artifact_write")
		for _, want := range []string{"artifact_write", "allow_artifact_write"} {
			if !strings.Contains(got, want) {
				t.Errorf("message %q does not mention %q", got, want)
			}
		}
	})

	t.Run("a genuinely unknown tool keeps the plain message", func(t *testing.T) {
		if got := unknownToolMessage("nonsense_tool"); got != "tool not found: nonsense_tool" {
			t.Errorf("message = %q, want the plain form", got)
		}
	})

	// An advanced tool is registered and callable, merely absent from
	// tools/list — the distinction a client cannot see.
	t.Run("an advanced tool is still callable", func(t *testing.T) {
		k := setupTestKB(t)
		s := New("test")
		RegisterKBTools(s, k, Deps{})
		if _, ok := s.Tools()["reindex"]; !ok {
			t.Fatal("reindex is not registered, so the advanced/gated distinction is untestable")
		}
		if !advancedToolNames["reindex"] {
			t.Error("reindex is expected to be advanced")
		}
	})
}

func TestArtifactRead_DescriptionNamesTheGate(t *testing.T) {
	k := setupTestKB(t)
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	desc := s.Tools()["artifact_read"].Description
	if !strings.Contains(desc, "allow_artifact_write") {
		t.Errorf("artifact_read's description does not name the gate: %s", desc)
	}
}
