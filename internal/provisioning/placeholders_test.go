package provisioning

// D262: every key the bound KBs cite is resolved once per Apply, the result is
// recorded in the lock, and the instructions block follows the table even when
// no artifact changed.

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
)

// placeholderKB builds a one-KB manifest whose artifacts cite no placeholder:
// whatever the table shows then comes from ApplyOptions.Placeholders.
func placeholderKB(t *testing.T) (Manifest, string) {
	t.Helper()
	kbRoot := t.TempDir()
	agentsDir := filepath.Join(kbRoot, "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentsDir, "reviewer.md"), []byte("---\nname: reviewer\n---\nNo placeholder here.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := BuildManifest(nil, map[string]string{"kb": kbRoot}, BuildOptions{})
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}
	return m, kbRoot
}

func clientApplyOptions(kbRoot, baseDir string, lock Lock) ApplyOptions {
	return ApplyOptions{
		AutoTrust:          true,
		KBRoots:            map[string]string{"kb": kbRoot},
		Provider:           configurator.ProviderClaudeCode,
		BaseDir:            baseDir,
		Lock:               lock,
		ExpandPlaceholders: true,
	}
}

func readInstructions(t *testing.T, baseDir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(baseDir, ".claude", "CLAUDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func wroteInstructions(res AppliedResult) bool {
	for _, w := range res.Written {
		if w.Kind == "instructions" {
			return true
		}
	}
	return false
}

func TestApply_PathsTableListsConceptOnlyKey(t *testing.T) {
	m, kbRoot := placeholderKB(t)
	baseDir := t.TempDir()
	opts := clientApplyOptions(kbRoot, baseDir, Lock{})
	opts.Placeholders = map[string][]string{"path:concept-only": {"kb"}}
	opts.Paths = map[string]string{"concept-only": "/srv/concept-only"}

	res, err := Apply(m, opts)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if content := readInstructions(t, baseDir); !strings.Contains(content, "| `{{path:concept-only}}` | `/srv/concept-only` |") {
		t.Errorf("table must list a key only a concept cites:\n%s", content)
	}
	if got := res.NewLock.ResolvedPlaceholders["path:concept-only"]; got != "/srv/concept-only" {
		t.Errorf("lock resolved = %v", res.NewLock.ResolvedPlaceholders)
	}
	if got := res.NewLock.PlaceholderSources["path:concept-only"]; !reflect.DeepEqual(got, []string{"kb"}) {
		t.Errorf("lock sources = %v, want [kb]", res.NewLock.PlaceholderSources)
	}
}

// Two unresolved repo keys walk the search roots once: the unusable-root
// warning comes from the walk itself, so it counts the walks.
func TestApply_UnresolvedRepoKeysScanOnce(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m, kbRoot := placeholderKB(t)
	baseDir := t.TempDir()
	opts := clientApplyOptions(kbRoot, baseDir, Lock{})
	opts.SearchRoots = []string{filepath.Join(t.TempDir(), "does-not-exist")}
	opts.Placeholders = map[string][]string{"repo:alpha": {"kb"}, "repo:beta": {"kb"}}

	res, err := Apply(m, opts)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	walks := 0
	for _, w := range res.Warnings {
		if strings.Contains(w, "is not usable") {
			walks++
		}
	}
	if walks != 1 {
		t.Errorf("search roots walked %d times, want exactly 1: %v", walks, res.Warnings)
	}
	if len(res.NewLock.UnresolvedPlaceholders) != 2 {
		t.Errorf("lock unresolved = %v, want repo:alpha and repo:beta", res.NewLock.UnresolvedPlaceholders)
	}
}

// The lock records an unresolved key, a `paths:` entry added afterwards
// rewrites the block on the next sync with no artifact change, the key leaves
// the unresolved list, and a third identical sync writes nothing.
func TestApply_PathsEntryRewritesBlockAndClearsUnresolved(t *testing.T) {
	m, kbRoot := placeholderKB(t)
	baseDir := t.TempDir()

	opts := clientApplyOptions(kbRoot, baseDir, Lock{})
	opts.Placeholders = map[string][]string{"path:x": {"kb"}}
	first, err := Apply(m, opts)
	if err != nil {
		t.Fatalf("Apply 1: %v", err)
	}
	if _, ok := first.NewLock.UnresolvedPlaceholders["path:x"]; !ok {
		t.Fatalf("lock must record path:x as unresolved: %v", first.NewLock.UnresolvedPlaceholders)
	}
	if content := readInstructions(t, baseDir); strings.Contains(content, "| Placeholder |") || !strings.Contains(content, placeholderParagraph) {
		t.Fatalf("first block: want the paragraph and no table:\n%s", content)
	}

	opts.Lock = first.NewLock
	opts.Paths = map[string]string{"x": "/srv/x"}
	second, err := Apply(m, opts)
	if err != nil {
		t.Fatalf("Apply 2: %v", err)
	}
	if !wroteInstructions(second) {
		t.Error("a new paths: entry must rewrite the instructions block")
	}
	if content := readInstructions(t, baseDir); !strings.Contains(content, "| `{{path:x}}` | `/srv/x` |") {
		t.Errorf("second block must list path:x:\n%s", content)
	}
	if len(second.NewLock.UnresolvedPlaceholders) != 0 {
		t.Errorf("a fixed key must leave the unresolved list: %v", second.NewLock.UnresolvedPlaceholders)
	}

	opts.Lock = second.NewLock
	third, err := Apply(m, opts)
	if err != nil {
		t.Fatalf("Apply 3: %v", err)
	}
	if wroteInstructions(third) || len(third.Written) != 0 {
		t.Errorf("an unchanged sync must write nothing: %+v", third.Written)
	}
}

// The server lists the keys of concepts; the client adds those of the
// artifacts it holds, so the table is the same whether or not an artifact was
// rewritten in this run — and complete against a server that lists nothing.
func TestPlaceholderSources_UnionsListedAndArtifactKeys(t *testing.T) {
	m := Manifest{Artifacts: []Artifact{
		{Kind: "skill", Name: "s", Source: "kb:kb-a", Signed: true, Files: []ArtifactFile{{Path: "SKILL.md", Content: []byte("{{repo:tool}} {{\\path:doc}} {{path:<name>}}")}}},
		{Kind: "skill", Name: "b", Source: "bundle", BuiltIn: true, Signed: true, Files: []ArtifactFile{{Path: "SKILL.md", Content: []byte("{{path:bundled}}")}}},
	}}
	got := placeholderSources(m, ApplyOptions{AutoTrust: true, Placeholders: map[string][]string{"path:concept": {"kb-b"}, "repo:tool": {"kb-b"}}})
	want := map[string][]string{
		"path:concept": {"kb-b"},
		"repo:tool":    {"kb-a", "kb-b"},
		"path:bundled": nil,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("placeholderSources = %v, want %v", got, want)
	}
}
