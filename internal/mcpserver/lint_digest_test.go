package mcpserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/lint"
)

// setupTwoMapKB adds broken links in two maps on top of setupTestKB, so a
// by_map digest has something to break down: one in manutenzione, two in a
// second map "arch".
func setupTwoMapKB(t *testing.T, k *kb.KB) {
	t.Helper()
	root := k.DataRoot()
	write := func(rel, body string) {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, rel)), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(filepath.Join(root, rel), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	write("manutenzione/broken-a.md", "---\ntype: Note\ntitle: A\n---\nSee [missing](nonexistent.md).\n")
	write("arch/broken-b.md", "---\ntype: Note\ntitle: B\n---\nSee [missing](nonexistent.md).\n")
	write("arch/broken-c.md", "---\ntype: Note\ntitle: C\n---\nSee [missing](nonexistent.md).\n")
}

// lintDigest calls the lint tool with the given arguments and returns the
// parsed JSON object.
func lintDigest(t *testing.T, k *kb.KB, args map[string]any) map[string]any {
	t.Helper()
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	resps := runMCPSequence(t, s, []string{
		initMsg,
		artifactCallMsg(t, 2, "lint", args),
	})
	tr := decodeToolResult(t, resps[1])
	if tr.IsError {
		t.Fatalf("lint %v: isError=true: %v", args, tr.Content)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(tr.Content[0].Text), &out); err != nil {
		t.Fatalf("decode lint %v: %v", args, err)
	}
	return out
}

func TestServer_Lint_DigestByMap(t *testing.T) {
	t.Parallel()
	k := setupTestKB(t)
	setupTwoMapKB(t, k)

	out := lintDigest(t, k, map[string]any{"digest": "by_map", "check": "broken_link"})

	raw, ok := out["counts_by_check_and_map"]
	if !ok {
		t.Fatalf("by_map digest must carry counts_by_check_and_map: %v", out)
	}
	byMap := raw.(map[string]any)
	// arch has two broken links, manutenzione one. The breakdown is per map.
	arch, ok := byMap["arch"].(map[string]any)
	if !ok {
		t.Fatalf("expected an arch entry: %v", byMap)
	}
	if got := arch["broken_link"]; got != float64(2) {
		t.Errorf("arch broken_link: want 2, got %v", got)
	}
	man, ok := byMap["manutenzione"].(map[string]any)
	if !ok {
		t.Fatalf("expected a manutenzione entry: %v", byMap)
	}
	if got := man["broken_link"]; got != float64(1) {
		t.Errorf("manutenzione broken_link: want 1, got %v", got)
	}
	// The plain page is still there: by_map is additive, not a replacement.
	if _, ok := out["counts_by_check"]; !ok {
		t.Errorf("by_map must not drop counts_by_check: %v", out)
	}
}

func TestServer_Lint_DigestPaths(t *testing.T) {
	t.Parallel()
	k := setupTestKB(t)
	setupTwoMapKB(t, k)

	out := lintDigest(t, k, map[string]any{"digest": "paths", "check": "broken_link"})

	raw, ok := out["distinct_paths"]
	if !ok {
		t.Fatalf("paths digest must carry distinct_paths: %v", out)
	}
	paths := raw.([]any)
	if len(paths) != 3 {
		t.Errorf("want 3 distinct paths, got %d: %v", len(paths), paths)
	}
	// Sorted, so arch/… comes before manutenzione/…
	if paths[0] != "arch/broken-b.md" {
		t.Errorf("distinct_paths must be sorted, got %v", paths)
	}
}

func TestServer_Lint_DigestPathsNeedsOneCheck(t *testing.T) {
	t.Parallel()
	k := setupTestKB(t)
	setupTwoMapKB(t, k)

	s := New("test")
	RegisterKBTools(s, k, Deps{})
	resps := runMCPSequence(t, s, []string{
		initMsg,
		// No check: ambiguous, must be refused.
		artifactCallMsg(t, 2, "lint", map[string]any{"digest": "paths"}),
	})
	tr := decodeToolResult(t, resps[1])
	if !tr.IsError {
		t.Errorf("digest=paths without a check must error, got %v", tr.Content)
	}
}

func TestServer_Lint_DigestCompact(t *testing.T) {
	t.Parallel()
	k := setupTestKB(t)
	setupTwoMapKB(t, k)

	out := lintDigest(t, k, map[string]any{"digest": "compact", "check": "broken_link"})

	findings := out["findings"].([]any)
	if len(findings) == 0 {
		t.Fatal("compact digest returned no findings")
	}
	seen := map[string]bool{}
	for _, f := range findings {
		m := f.(map[string]any)
		path, ok := m["path"].(string)
		if !ok {
			t.Errorf("compact row must carry path: %v", m)
		}
		// One row per path (#1273), with the finding count per check.
		if seen[path] {
			t.Errorf("path %q appears in two compact rows", path)
		}
		seen[path] = true
		checks, ok := m["checks"].(map[string]any)
		if !ok || checks["broken_link"] == nil {
			t.Errorf("compact row must carry checks: %v", m)
		}
		if _, ok := m["message"]; ok {
			t.Errorf("compact row must drop message: %v", m)
		}
		if _, ok := m["check"]; ok {
			t.Errorf("compact row names checks, not check: %v", m)
		}
	}
}

// A path with several findings of one check is one row with a count.
func TestServer_Lint_DigestCompactGroupsByPath(t *testing.T) {
	t.Parallel()
	got := findingsOutMode([]lint.Finding{
		{Path: "a/x.md", Check: "broken_link"}, {Path: "a/x.md", Check: "broken_link"},
		{Path: "a/x.md", Check: "orphan"}, {Path: "a/y.md", Check: "orphan"},
	}, true)
	b, _ := json.Marshal(got)
	want := `[{"path":"a/x.md","checks":{"broken_link":2,"orphan":1}},{"path":"a/y.md","checks":{"orphan":1}}]`
	if string(b) != want {
		t.Errorf("compact = %s, want %s", b, want)
	}
}

func TestServer_Lint_LimitClamped(t *testing.T) {
	t.Parallel()
	k := setupTestKB(t)
	setupTwoMapKB(t, k)

	// A limit over the 200 cap is clamped, and the response now says so.
	out := lintDigest(t, k, map[string]any{"limit": 500})
	if out["limit_clamped"] != true {
		t.Errorf("a limit over the cap must set limit_clamped: %v", out["limit_clamped"])
	}

	// A limit within the cap does not set the flag.
	out = lintDigest(t, k, map[string]any{"limit": 10})
	if _, ok := out["limit_clamped"]; ok {
		t.Errorf("a limit within the cap must not set limit_clamped: %v", out)
	}
}

func TestServer_Lint_DigestRejectsUnknown(t *testing.T) {
	t.Parallel()
	k := setupTestKB(t)

	s := New("test")
	RegisterKBTools(s, k, Deps{})
	resps := runMCPSequence(t, s, []string{
		initMsg,
		artifactCallMsg(t, 2, "lint", map[string]any{"digest": "bogus"}),
	})
	tr := decodeToolResult(t, resps[1])
	if !tr.IsError {
		t.Errorf("an unknown digest must error, got %v", tr.Content)
	}
}

func TestServer_Lint_DigestPathsPages(t *testing.T) {
	t.Parallel()
	k := setupTestKB(t)
	setupTwoMapKB(t, k)

	out := lintDigest(t, k, map[string]any{"digest": "paths", "check": "broken_link", "limit": 2})
	if got := len(out["distinct_paths"].([]any)); got != 2 {
		t.Errorf("limit 2: want 2 paths, got %d", got)
	}
	if out["count"] != float64(3) || out["next_offset"] != float64(2) || out["truncated"] != true {
		t.Errorf("first page must report count 3 and next_offset 2: %v", out)
	}
	out = lintDigest(t, k, map[string]any{"digest": "paths", "check": "broken_link", "limit": 2, "offset": 2})
	p := out["distinct_paths"].([]any)
	if len(p) != 1 || p[0] != "manutenzione/broken-a.md" {
		t.Errorf("second page: %v", p)
	}
	if _, ok := out["truncated"]; ok {
		t.Errorf("last page must not be truncated: %v", out)
	}
	out = lintDigest(t, k, map[string]any{"digest": "paths", "check": "broken_link", "limit": 500})
	if out["limit_clamped"] != true {
		t.Errorf("limit over the cap must set limit_clamped: %v", out)
	}
}
