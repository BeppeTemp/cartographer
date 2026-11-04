package mcpserver

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// oversizedKB has a skill "x" with a valid SKILL.md and a file one byte over
// the artifact file cap, a small skill "ok", and, with a symlink, a skill "linked" that holds one (left out whole).
func oversizedKB(t *testing.T, withSymlink bool) *Server {
	t.Helper()
	k := setupTestKB(t)
	k.AuthName = "docs"
	write := func(rel string, data []byte) {
		t.Helper()
		p := filepath.Join(k.Root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	skill := func(n string) []byte {
		return []byte("---\nname: " + n + "\ndescription: " + n + "\n---\nBody.\n")
	}
	write("skills/x/SKILL.md", skill("x"))
	write("skills/x/big.bin", make([]byte, artifactMaxFileSize+1))
	write("skills/ok/SKILL.md", skill("ok"))
	if withSymlink {
		write("skills/linked/SKILL.md", skill("linked"))
		outside := filepath.Join(t.TempDir(), "secret.txt")
		if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(k.Root, "skills", "linked", "link.txt")); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
	}
	commitFixtures(t, k.Root)
	s := New("test")
	RegisterKBTools(s, k, Deps{BundleFS: fstest.MapFS{}})
	return s
}

func TestOversizedArtifactFile(t *testing.T) {
	s := oversizedKB(t, false)

	// artifact_list: an oversized entry with no sha256.
	text, isErr := callJSON(t, s, adminCtx, "artifact_list", `{}`)
	if isErr {
		t.Fatalf("artifact_list: %s", text)
	}
	var list []artifactEntry
	if err := json.Unmarshal([]byte(text), &list); err != nil {
		t.Fatal(err)
	}
	seen := map[string]artifactFileEntry{}
	for _, a := range list {
		for _, f := range a.Files {
			seen[f.Path] = f
		}
	}
	big, ok := seen["skills/x/big.bin"]
	if !ok || big.Oversized != int64(artifactMaxFileSize+1) || big.SHA256 != "" {
		t.Fatalf("big.bin entry = %+v (present %v)", big, ok)
	}
	if f := seen["skills/ok/SKILL.md"]; f.SHA256 == "" || f.Oversized != 0 {
		t.Fatalf("a small file must be listed whole: %+v", f)
	}

	// artifact_read refuses it.
	text, isErr = callJSON(t, s, adminCtx, "artifact_read", `{"path":"skills/x/big.bin"}`)
	if !isErr || !strings.HasPrefix(text, "oversized:") {
		t.Fatalf("artifact_read = %v %s", isErr, text)
	}
	if text, isErr = callJSON(t, s, adminCtx, "artifact_read", `{"path":"skills/x/SKILL.md"}`); isErr {
		t.Fatalf("a small file must still be readable: %s", text)
	}

	pullShipped(t, s)
}

// sync_pull leaves the artifact with an oversized file out and says so; a
// symlink in another artifact is not followed.
func TestOversizedAndSymlinkInSyncPull(t *testing.T) {
	pullShipped(t, oversizedKB(t, true))
}

func pullShipped(t *testing.T, s *Server) {
	t.Helper()
	text, isErr := callJSON(t, s, adminCtx, "sync_pull", `{}`)
	if isErr {
		t.Fatalf("sync_pull: %s", text)
	}
	var pull struct {
		Artifacts []struct {
			Kind  string `json:"kind"`
			Name  string `json:"name"`
			Files []struct {
				Path string `json:"path"`
			} `json:"files"`
		} `json:"artifacts"`
		Issues []string `json:"issues"`
	}
	if err := json.Unmarshal([]byte(text), &pull); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, a := range pull.Artifacts {
		if a.Kind == "skill" {
			names = append(names, a.Name)
		}
		for _, f := range a.Files {
			if strings.Contains(f.Path, "link.txt") || strings.Contains(f.Path, "big.bin") {
				t.Fatalf("%s was shipped", f.Path)
			}
		}
	}
	if strings.Join(names, ",") != "ok" {
		t.Fatalf("shipped artifacts = %v, want only ok", names)
	}
	found := false
	for _, i := range pull.Issues {
		if strings.Contains(i, `"x" excluded from sync_pull`) && strings.Contains(i, "big.bin") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no issue line for the excluded skill: %v", pull.Issues)
	}
}

// A skill of many small files is bounded by the directory budget, not only by
// the per-file cap: it is excluded from sync_pull, nothing is read past it.
func TestSyncPullExcludesSkillOverFileBudget(t *testing.T) {
	k := setupTestKB(t)
	k.AuthName = "docs"
	dir := filepath.Join(k.Root, "skills", "many")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: many\ndescription: many\n---\nBody.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < provisioning.ArtifactMaxFiles+5; i++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%04d.txt", i)), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	commitFixtures(t, k.Root)
	s := New("test")
	RegisterKBTools(s, k, Deps{BundleFS: fstest.MapFS{}})
	text, isErr := callJSON(t, s, adminCtx, "sync_pull", `{}`)
	if isErr {
		t.Fatalf("sync_pull: %s", text)
	}
	if !strings.Contains(text, `skill \"many\" excluded from sync_pull`) {
		t.Fatalf("no exclusion line: %.400s", text)
	}
}

// A tracked symlink as an agent, an mcp descriptor or a template leaves out
// that artifact, never the manifest of the whole KB. (artifact_list and
// template_list refuse any symlink in the tree by design, before this.)
func TestSymlinkedSingleFileArtifactsDoNotBreakTheKB(t *testing.T) {
	k := setupTestKB(t)
	k.AuthName = "docs"
	outside := filepath.Join(t.TempDir(), "t.md")
	if err := os.WriteFile(outside, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"agents/a.md", "mcp/m.json", "templates/t.md"} {
		p := filepath.Join(k.Root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, p); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
	}
	commitFixtures(t, k.Root)
	s := New("test")
	RegisterKBTools(s, k, Deps{BundleFS: fstest.MapFS{}})
	for _, tool := range []string{"sync_check", "sync_pull"} {
		if text, isErr := callJSON(t, s, adminCtx, tool, `{}`); isErr {
			t.Errorf("%s: %s", tool, text)
		}
	}
	text, _ := callJSON(t, s, adminCtx, "sync_pull", `{}`)
	if !strings.Contains(text, "symlink at") {
		t.Errorf("no issue for the symlinked artifacts: %.400s", text)
	}
}

// artifact_write and artifact_delete never read or clobber an existing file
// over the cap.
func TestArtifactWriteDeleteRefuseOversizedExisting(t *testing.T) {
	s := oversizedKB(t, false)
	if text, isErr := callJSON(t, s, adminCtx, "artifact_write", `{"path":"skills/x/big.bin","content":"y","if_match":"deadbeef"}`); !isErr || !strings.HasPrefix(text, "oversized:") {
		t.Errorf("artifact_write = %v %s", isErr, text)
	}
	if text, isErr := callJSON(t, s, adminCtx, "artifact_delete", `{"path":"skills/x/big.bin","if_match":"deadbeef"}`); !isErr || !strings.HasPrefix(text, "oversized:") {
		t.Errorf("artifact_delete = %v %s", isErr, text)
	}
}
