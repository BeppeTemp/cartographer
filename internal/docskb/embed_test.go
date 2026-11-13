package docskb

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestEmbeddedDocsEqualTheDisk: the embedded set generates the same KB as the
// files on disk. A docs subdirectory or file type the embed patterns of
// docsfs.go miss would change the output (or fail the generation) here.
func TestEmbeddedDocsEqualTheDisk(t *testing.T) {
	disk, err := Generate(os.DirFS("../.."), Ref("dev"))
	if err != nil {
		t.Fatal(err)
	}
	emb, err := Embedded("dev")
	if err != nil {
		t.Fatal(err)
	}
	if len(disk) != len(emb) {
		t.Errorf("embedded KB has %d files, disk KB %d", len(emb), len(disk))
	}
	for n, b := range disk {
		if !bytes.Equal(b, emb[n]) {
			t.Errorf("%s differs between the embedded docs and the disk", n)
		}
	}
	// Every markdown file under docs/ is embedded (the embed lists directories).
	err = filepath.WalkDir("../../docs", func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".md") {
			rel, _ := filepath.Rel("../../docs", p)
			if d := filepath.ToSlash(filepath.Dir(rel)); d != "." && d != "brand" && d != "decisions" {
				t.Errorf("docs/%s is in a directory the embed patterns of docsfs.go do not list", rel)
			}
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestExport(t *testing.T) {
	root := t.TempDir()
	full := filepath.Join(root, "full")
	if err := os.Mkdir(full, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(full, "x"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Export(full, "dev"); err == nil || !strings.Contains(err.Error(), full) {
		t.Fatalf("a non-empty dir must be refused naming it, got %v", err)
	}
	empty := filepath.Join(root, "empty")
	if err := os.Mkdir(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(root, "a", "missing")
	for _, d := range []string{empty, missing} {
		if err := Export(d, "dev"); err != nil {
			t.Fatalf("Export(%s): %v", d, err)
		}
		if _, err := os.Stat(filepath.Join(d, "data", "index.md")); err != nil {
			t.Errorf("%s: %v", d, err)
		}
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			t.Errorf("%s: export must write no git repository", d)
		}
	}
}

func call(t *testing.T, r *Runner, method string, params map[string]any) json.RawMessage {
	t.Helper()
	res, err := r.Do(context.Background(), method, params)
	if err != nil {
		t.Fatalf("%s: %v", method, err)
	}
	return res
}

// One runner serves every test that only reads: materializing the tree is
// the cost of a runner (about a second on a Windows runner), so the tests that
// do not check creation, cleanup or the environment share one per test binary.
// TestMain closes it.
var (
	sharedOnce sync.Once
	sharedRun  *Runner
	sharedErr  error
)

func sharedRunner(t *testing.T) *Runner {
	t.Helper()
	sharedOnce.Do(func() { sharedRun, sharedErr = NewRunner("dev") })
	if sharedErr != nil {
		t.Fatal(sharedErr)
	}
	return sharedRun
}

func closeSharedRunner() {
	if sharedRun != nil {
		_ = sharedRun.Close()
	}
}

func TestRunnerToolsAndSearch(t *testing.T) {
	r := sharedRunner(t)
	var list struct{ Tools []struct{ Name string } }
	if err := json.Unmarshal(call(t, r, "tools/list", nil), &list); err != nil {
		t.Fatal(err)
	}
	want := []string{"search", "concept_read", "index_get", "graph_context", "graph_neighbors", "concept_list", "atlas_overview", "artifact_read", "kb_guide"}
	var got []string
	for _, tl := range list.Tools {
		got = append(got, tl.Name)
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("tools/list = %v, want %v", got, want)
	}
	res := call(t, r, "tools/call", map[string]any{"name": "search", "arguments": map[string]any{"query": "workspace scope"}})
	if !strings.Contains(string(res), "configurator") {
		t.Errorf("search did not find the configurator workspace section: %.300s", res)
	}
	// A write is refused by the read-only profile.
	res, err := r.Do(context.Background(), "tools/call", map[string]any{"name": "concept_write", "arguments": map[string]any{"id": "using-cartographer/x", "content": "x"}})
	if err == nil && !strings.Contains(string(res), "read_only_profile") {
		t.Errorf("concept_write was not refused with read_only_profile: %.300s", res)
	}
	if err != nil && !strings.Contains(err.Error(), "read_only_profile") {
		t.Errorf("concept_write refusal = %v, want read_only_profile", err)
	}
}

func TestRunnersAreIndependentAndCloseCleans(t *testing.T) {
	a, err := NewRunner("dev")
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewRunner("dev")
	if err != nil {
		t.Fatal(err)
	}
	if a.dir == b.dir {
		t.Fatal("two runners share a directory")
	}
	for _, r := range []*Runner{a, b} {
		call(t, r, "tools/call", map[string]any{"name": "search", "arguments": map[string]any{"query": "sync"}})
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(a.dir); !os.IsNotExist(err) {
		t.Errorf("Close left %s behind", a.dir)
	}
	call(t, b, "tools/call", map[string]any{"name": "search", "arguments": map[string]any{"query": "sync"}})
	_ = b.Close()
}

func TestRunnerWithAReadOnlyHome(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory modes do not make a Windows directory read-only")
	}
	home := t.TempDir()
	if err := os.Chmod(home, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(home, 0o700) })
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	r, err := NewRunner("dev")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	call(t, r, "tools/call", map[string]any{"name": "search", "arguments": map[string]any{"query": "sync"}})
	entries, _ := os.ReadDir(home)
	if len(entries) != 0 {
		t.Errorf("the runner wrote into $HOME: %v", entries)
	}
}

func TestSweepRemovesOnlyOldRunnerDirs(t *testing.T) {
	root := t.TempDir()
	old := filepath.Join(root, tempPrefix+"old")
	fresh := filepath.Join(root, tempPrefix+"fresh")
	other := filepath.Join(root, "other-old")
	for _, d := range []string{old, fresh, other} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	past := time.Now().Add(-48 * time.Hour)
	for _, d := range []string{old, other} {
		if err := os.Chtimes(d, past, past); err != nil {
			t.Fatal(err)
		}
	}
	sweep(root, time.Now())
	for d, wantGone := range map[string]bool{old: true, fresh: false, other: false} {
		if _, err := os.Stat(d); os.IsNotExist(err) != wantGone {
			t.Errorf("%s: gone=%v, want %v", d, os.IsNotExist(err), wantGone)
		}
	}
}

// BenchmarkRunnerCold is the cold start of `cartographer docs`: generate,
// materialize, open and the first search.
func BenchmarkRunnerCold(b *testing.B) {
	for i := 0; i < b.N; i++ {
		r, err := NewRunner("dev")
		if err != nil {
			b.Fatal(err)
		}
		if _, err := r.Do(context.Background(), "tools/call", map[string]any{"name": "search", "arguments": map[string]any{"query": "workspace"}}); err != nil {
			b.Fatal(err)
		}
		_ = r.Close()
	}
}
