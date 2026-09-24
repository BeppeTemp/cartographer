package mcpserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// paths.yaml (D263) is authored through the artifact tools: the strict
// validator runs before anything reaches disk, and the error names the key.
func TestArtifactTools_PathRegistryWrite(t *testing.T) {
	k := setupTestKB(t)
	k.AllowArtifactWrite = true
	s := New("test")
	RegisterKBTools(s, k, Deps{})

	valid := "paths:\n  claude-home: {description: Claude Code's directory, default: ~/.claude}\n" +
		"repos:\n  kb-tools: {description: the tools repo, remote: example.com/owner/kb-tools}\n"
	resps := runMCPSequence(t, s, []string{initMsg, artifactCallMsg(t, 2, "artifact_write", map[string]any{"path": "paths.yaml", "content": valid})})
	if tr := decodeToolResult(t, resps[1]); tr.IsError {
		t.Fatalf("valid paths.yaml rejected: %+v", tr.Content)
	}
	if data, err := os.ReadFile(filepath.Join(k.Root, "paths.yaml")); err != nil || string(data) != valid {
		t.Fatalf("paths.yaml not written verbatim: %q %v", data, err)
	}

	// artifact_read serves it, with the sha256 an overwrite needs.
	resps = runMCPSequence(t, s, []string{initMsg, artifactCallMsg(t, 2, "artifact_read", map[string]any{"path": "paths.yaml"})})
	tr := decodeToolResult(t, resps[1])
	var read struct {
		Content string `json:"content"`
		SHA256  string `json:"sha256"`
	}
	if tr.IsError || json.Unmarshal([]byte(tr.Content[0].Text), &read) != nil || read.Content != valid {
		t.Fatalf("artifact_read paths.yaml: %+v", tr.Content)
	}

	cases := []struct{ name, content, want string }{
		{"absolute default", "paths:\n  claude-home: {description: d, default: /Users/x/.claude}\n", "paths.claude-home"},
		{"unknown field", "paths:\n  claude-home: {description: d, path: ~/.claude}\n", `unknown field "path"`},
		{"missing description", "paths:\n  claude-home: {default: ~/.claude}\n", "description is required"},
		{"bad slug", "paths:\n  Claude: {description: d}\n", "paths.Claude"},
		{"bad remote", "repos:\n  kb-tools: {description: d, remote: nope}\n", "repos.kb-tools"},
		{"not yaml", "paths: [\n", "not valid YAML"},
	}
	for _, c := range cases {
		resps := runMCPSequence(t, s, []string{initMsg, artifactCallMsg(t, 2, "artifact_write", map[string]any{
			"path": "paths.yaml", "content": c.content, "if_match": read.SHA256,
		})})
		tr := decodeToolResult(t, resps[1])
		if !tr.IsError || !containsText(tr, c.want) {
			t.Errorf("%s: want a rejection naming %q, got %+v", c.name, c.want, tr.Content)
		}
	}
	if data, _ := os.ReadFile(filepath.Join(k.Root, "paths.yaml")); string(data) != valid {
		t.Fatalf("a rejected write changed paths.yaml: %q", data)
	}

	resps = runMCPSequence(t, s, []string{initMsg, artifactCallMsg(t, 2, "artifact_delete", map[string]any{"path": "paths.yaml", "if_match": read.SHA256})})
	if tr := decodeToolResult(t, resps[1]); tr.IsError {
		t.Fatalf("artifact_delete paths.yaml: %+v", tr.Content)
	}
	if _, err := os.Stat(filepath.Join(k.Root, "paths.yaml")); !os.IsNotExist(err) {
		t.Fatal("paths.yaml still present after delete")
	}
	if _, err := os.Stat(k.Root); err != nil {
		t.Fatal("deleting paths.yaml removed the KB root")
	}
}

// A symlinked paths.yaml is refused by the write path and by the tree sweep
// artifact_list runs, like every other KB-root artifact (D148).
func TestArtifactTools_PathRegistrySymlinkRejected(t *testing.T) {
	for _, tool := range []string{"artifact_read", "artifact_write", "artifact_list"} {
		t.Run(tool, func(t *testing.T) {
			k := setupTestKB(t)
			k.AllowArtifactWrite = true
			target := filepath.Join(t.TempDir(), "outside.yaml")
			if err := os.WriteFile(target, []byte("outside"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, filepath.Join(k.Root, "paths.yaml")); err != nil {
				t.Skipf("symlink: %v", err)
			}
			s := New("test")
			RegisterKBTools(s, k, Deps{})
			args := map[string]any{}
			if tool != "artifact_list" {
				args["path"] = "paths.yaml"
			}
			if tool == "artifact_write" {
				args["content"] = "paths: {}\n"
				args["if_match"] = sha256Hex([]byte("outside"))
			}
			resps := runMCPSequence(t, s, []string{initMsg, artifactCallMsg(t, 2, tool, args)})
			tr := decodeToolResult(t, resps[1])
			if !tr.IsError || !containsText(tr, "symlink") {
				t.Fatalf("%s: expected a symlink rejection, got %+v", tool, tr.Content)
			}
			if data, _ := os.ReadFile(target); !strings.EqualFold(string(data), "outside") {
				t.Fatalf("%s changed the symlink target: %q", tool, data)
			}
		})
	}
}
