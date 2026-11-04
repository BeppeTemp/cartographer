package mcpserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An artifact path is canonical (slash-separated, no ".", ".." or doubled
// separator) and every filesystem access uses that one spelling: the symlink
// guard and the join must walk the same path, or a component the guard never
// visits is followed by the join.
func TestArtifactTools_PathThroughATrackedLinkIsRefusedInEverySpelling(t *testing.T) {
	t.Parallel()
	spellings := map[string]string{
		"direct":  "hooks/lnk/",
		"dot dot": "skills/nope/../../hooks/lnk/",
		"dot":     "hooks/./lnk/",
		"doubled": "hooks//lnk/",
	}
	for name, prefix := range spellings {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			k := setupTestKB(t)
			outside := t.TempDir()
			secret := filepath.Join(outside, "secret.txt")
			if err := os.WriteFile(secret, []byte("outside"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(k.Root, "hooks"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, filepath.Join(k.Root, "hooks", "lnk")); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
			s := New("test")
			RegisterKBTools(s, k, Deps{})
			call := func(tool string, args map[string]any) ToolResult {
				t.Helper()
				resps := runMCPSequence(t, s, []string{initMsg, artifactCallMsg(t, 2, tool, args)})
				return decodeToolResult(t, resps[1])
			}

			read := call("artifact_read", map[string]any{"path": prefix + "secret.txt"})
			if !read.IsError || containsText(read, "outside") {
				t.Errorf("artifact_read through the link: %+v", read.Content)
			}
			overwrite := call("artifact_write", map[string]any{
				"path": prefix + "secret.txt", "content": "replaced", "if_match": sha256Hex([]byte("outside")),
			})
			if !overwrite.IsError {
				t.Errorf("artifact_write over a file behind the link: %+v", overwrite.Content)
			}
			create := call("artifact_write", map[string]any{"path": prefix + "created.txt", "content": "new"})
			if !create.IsError {
				t.Errorf("artifact_write of a new file behind the link: %+v", create.Content)
			}
			del := call("artifact_delete", map[string]any{
				"path": prefix + "secret.txt", "if_match": sha256Hex([]byte("outside")),
			})
			if !del.IsError {
				t.Errorf("artifact_delete behind the link: %+v", del.Content)
			}

			if data, err := os.ReadFile(secret); err != nil || string(data) != "outside" {
				t.Errorf("a file outside the KB changed: %q %v", data, err)
			}
			entries, err := os.ReadDir(outside)
			if err != nil || len(entries) != 1 {
				t.Errorf("the directory outside the KB gained or lost entries: %v %v", entries, err)
			}
		})
	}
}

// A path that is not already in its cleaned form is refused with the form it
// should have had, for every tool that takes one, and the cleaned form still
// works.
func TestArtifactTools_RefuseANonCanonicalPath(t *testing.T) {
	t.Parallel()
	k := setupTestKB(t)
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	call := func(tool string, args map[string]any) ToolResult {
		t.Helper()
		resps := runMCPSequence(t, s, []string{initMsg, artifactCallMsg(t, 2, tool, args)})
		return decodeToolResult(t, resps[1])
	}
	body := "---\nname: canon\ndescription: A skill\n---\nbody\n"
	for _, p := range []string{
		"skills/canon/../canon/SKILL.md",
		"skills/./canon/SKILL.md",
		"skills//canon/SKILL.md",
		"skills/canon/SKILL.md/",
		"skills/x/../../instructions.md",
	} {
		for _, tool := range []string{"artifact_read", "artifact_write", "artifact_delete"} {
			args := map[string]any{"path": p}
			switch tool {
			case "artifact_write":
				args["content"] = body
			case "artifact_delete":
				args["if_match"] = sha256Hex([]byte(body))
			}
			tr := call(tool, args)
			if !tr.IsError || !containsText(tr, "canonical") {
				t.Errorf("%s %q: want a refusal naming the canonical form, got %+v", tool, p, tr.Content)
			}
		}
	}
	if _, err := os.Lstat(filepath.Join(k.Root, "skills", "canon")); err == nil {
		t.Error("a refused write created the skill")
	}
	if tr := call("artifact_write", map[string]any{"path": "skills/canon/SKILL.md", "content": body}); tr.IsError {
		t.Fatalf("the canonical form is refused: %+v", tr.Content)
	}
}

// The symlink guard cleans its argument itself, so a caller that hands it a
// spelling with ".." cannot make it stop at a component that the join later
// skips over.
func TestRejectArtifactSymlinks_WalksTheCleanedPath(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "hooks", "lnk")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	for _, rel := range []string{"hooks/lnk/x", "skills/nope/../../hooks/lnk/x", "hooks/./lnk", "hooks//lnk/x"} {
		if err := rejectArtifactSymlinks(root, rel); err == nil || !strings.Contains(err.Error(), "symlink") {
			t.Errorf("rejectArtifactSymlinks(%q) = %v, want a symlink refusal", rel, err)
		}
	}
	if err := rejectArtifactSymlinks(root, "skills/../../outside"); err == nil {
		t.Error("a path that leaves the root passed the guard")
	}
	if err := rejectArtifactSymlinks(root, "hooks/plain"); err != nil {
		t.Errorf("a missing path under a real directory is refused: %v", err)
	}
}

// artifact_move names its unit by the cleaned path and runs the symlink guard
// on that same string, so the ".." spelling of a linked hook is refused too.
func TestArtifactMove_RefusesALinkedHookInEverySpelling(t *testing.T) {
	t.Parallel()
	f := newTransferFixture(t)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "run.sh"), []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(f.a.Root, "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(f.a.Root, "hooks", "lnk")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	xferCommit(t, f.a)
	headA := xferHead(t, f.a)
	for _, p := range []string{"hooks/lnk", "skills/nope/../../hooks/lnk", "hooks/./lnk"} {
		res := f.move(t, `{"target_kb":"kb-b","paths":["`+p+`"]}`)
		if !res.IsError || !strings.Contains(xferText(res), "symlink") {
			t.Errorf("artifact_move %q: want a symlink refusal, got %q", p, xferText(res))
		}
	}
	if xferHead(t, f.a) != headA || amExists(f.b, "hooks/lnk") {
		t.Error("a refused move changed a KB")
	}
	if _, err := os.Stat(filepath.Join(outside, "run.sh")); err != nil {
		t.Errorf("the directory outside the KB lost a file: %v", err)
	}
}
