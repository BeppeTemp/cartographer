package mcpserver

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/lint"
)

// generatedKB is a git KB with two maps that opted into `index: generated`
// (D301) and one curated map.
func generatedKB(t *testing.T) (*kb.KB, *Server) {
	t.Helper()
	k, _ := setupGitKB(t)
	k.AutoCommit = true
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	for _, m := range []string{"ga", "gb", "cur"} {
		if res := callTool(t, s, "map_create", fmt.Sprintf(`{"name":%q,"title":%q}`, m, m)); res.IsError {
			t.Fatalf("map_create %s: %v", m, res.Content)
		}
	}
	for _, m := range []string{"ga", "gb"} {
		if res := callTool(t, s, "map_update", fmt.Sprintf(`{"map":%q,"index":"generated"}`, m)); res.IsError {
			t.Fatalf("map_update %s: %v", m, res.Content)
		}
	}
	return k, s
}

func headFiles(t *testing.T, root string) []string {
	t.Helper()
	out, err := exec.Command("git", "-C", root, "show", "--name-only", "--format=", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.Fields(string(out))
}

func readIndex(t *testing.T, k *kb.KB, m string) string {
	t.Helper()
	s, err := k.ReadIndex(m)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestGeneratedIndex_FollowsWritesInTheSameCommit(t *testing.T) {
	k, s := generatedKB(t)
	curBefore := readIndex(t, k, "cur")

	if res := callTool(t, s, "concept_write", `{"id":"ga/one","frontmatter":{"type":"Note","title":"One"},"body":"x\n"}`); res.IsError {
		t.Fatalf("concept_write: %v", res.Content)
	}
	files := strings.Join(headFiles(t, k.Root), " ")
	if !strings.Contains(files, "data/ga/one.md") || !strings.Contains(files, "data/ga/index.md") {
		t.Fatalf("commit files: %s", files)
	}
	if !strings.Contains(readIndex(t, k, "ga"), "- [[ga/one]] — One\n") {
		t.Fatalf("ga index:\n%s", readIndex(t, k, "ga"))
	}

	if res := callTool(t, s, "concept_move", `{"source_id":"ga/one","target_id":"gb/one"}`); res.IsError {
		t.Fatalf("concept_move: %v", res.Content)
	}
	if strings.Contains(readIndex(t, k, "ga"), "[[ga/one]]") || !strings.Contains(readIndex(t, k, "gb"), "- [[gb/one]] — One\n") {
		t.Fatalf("after move:\nga:\n%s\ngb:\n%s", readIndex(t, k, "ga"), readIndex(t, k, "gb"))
	}

	if res := callTool(t, s, "concept_delete", `{"id":"gb/one"}`); res.IsError {
		t.Fatalf("concept_delete: %v", res.Content)
	}
	if strings.Contains(readIndex(t, k, "gb"), "gb/one") {
		t.Fatalf("after delete:\n%s", readIndex(t, k, "gb"))
	}
	if readIndex(t, k, "cur") != curBefore {
		t.Fatal("a curated map's index was touched")
	}
}

func TestGeneratedIndex_IndexPatchAndStaleLint(t *testing.T) {
	k, s := generatedKB(t)
	callTool(t, s, "concept_write", `{"id":"ga/one","frontmatter":{"type":"Note","title":"One"},"body":"x\n"}`)

	_, hash, err := k.IndexHash("ga")
	if err != nil {
		t.Fatal(err)
	}
	res := callTool(t, s, "index_patch", fmt.Sprintf(`{"path":"ga","old_string":"— One","new_string":"— Uno","if_match":%q}`, hash))
	if !res.IsError || !strings.Contains(res.Content[0].Text, "generated_index") {
		t.Fatalf("edit inside the block accepted: %+v", res)
	}
	res = callTool(t, s, "index_patch", fmt.Sprintf(`{"path":"ga","old_string":"# ga","new_string":"# Ga\n\nCurated intro.","if_match":%q}`, hash))
	if res.IsError {
		t.Fatalf("edit outside the block refused: %v\n%s", res.Content, readIndex(t, k, "ga"))
	}

	// An out-of-band edit makes the block stale; the next write heals it.
	p := filepath.Join(k.DataRoot(), "ga", "index.md")
	_ = os.WriteFile(p, []byte(strings.Replace(readIndex(t, k, "ga"), "— One", "— Uno", 1)), 0o644)
	stale := func() bool {
		fs, err := lint.Run(k, "", false)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range fs {
			if f.Check == "index_stale" && f.Path == "ga/index.md" {
				return true
			}
			if f.Check == "index_incomplete" && strings.HasPrefix(f.Path, "ga/") {
				t.Fatalf("index_incomplete on a generated map: %+v", f)
			}
		}
		return false
	}
	if !stale() {
		t.Fatal("index_stale not reported after an out-of-band edit")
	}
	callTool(t, s, "concept_write", `{"id":"cur/x","frontmatter":{"type":"Note","title":"X"},"body":"x\n"}`)
	if stale() {
		t.Fatal("index_stale still reported after the next write")
	}
	if !strings.Contains(readIndex(t, k, "ga"), "Curated intro.") {
		t.Fatal("curated text lost")
	}
}
