package mcpserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// oracleConceptClass is conceptClass as it was before D241: it read and parsed
// the concept file on every call. Kept verbatim so the cached version is
// proved equivalent, not assumed so — this is authorization code.
func oracleConceptClass(k *kb.KB, id string) (mapName, journalName, typ string) {
	parts := strings.Split(id, "/")
	if len(parts) > 1 {
		mapName = parts[0]
		if meta, err := k.ReadArchiveMeta(mapName); err == nil {
			if kind, _ := meta.Get("kind"); kind == "journal" {
				journalName, mapName = mapName, ""
			}
		}
	}
	if conceptID, err := okf.PathToID(id + ".md"); err == nil {
		if c, err := k.ReadConcept(conceptID); err == nil {
			if fm, _, ok := okf.SplitFrontmatter(c.Content); ok {
				if parsed, err := okf.ParseFrontmatter(fm); err == nil {
					if t, ok := parsed.Get("type"); ok {
						typ, _ = t.(string)
					}
				}
			}
		}
	}
	return
}

func TestVisibleWithCachedTypesMatchesTheUncachedPolicy(t *testing.T) {
	k := setupTestKB(t)
	k.AuthName = "docs"
	abs := func(rel string) string {
		if strings.HasPrefix(rel, "services/") {
			return filepath.Join(k.Root, filepath.FromSlash(rel))
		}
		return filepath.Join(k.DataRoot(), filepath.FromSlash(rel))
	}
	write := func(rel, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(abs(rel)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs(rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	backdate := func() {
		old := time.Now().Add(-time.Hour)
		for _, root := range []string{k.DataRoot(), filepath.Join(k.Root, "services")} {
			_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
				if err == nil && !info.IsDir() {
					return os.Chtimes(p, old, old)
				}
				return nil
			})
		}
	}
	if err := k.CreateMap("ops", "Ops", "map", nil, ""); err != nil {
		t.Fatal(err)
	}
	if err := k.CreateMap("diary", "Diary", "journal", nil, ""); err != nil {
		t.Fatal(err)
	}
	write("ops/runbook.md", "---\ntype: Runbook\n---\nRun.\n")
	write("ops/note.md", "---\ntype: Note\n---\nNote.\n")
	write("ops/broken.md", "---\ntype: [Runbook\n---\nUnparseable.\n")
	write("ops/bare.md", "No frontmatter at all.\n")
	write("ops/untyped.md", "---\ntitle: Untyped\n---\nNo type.\n")
	write("ops/exp/index.md", "---\ntype: Runbook\n---\nExpanded.\n")
	write("ops/pair.md", "---\ntype: Note\n---\nDirect form.\n")
	write("ops/pair/index.md", "---\ntype: Runbook\n---\nExpanded twin.\n")
	write("diary/day.md", "---\ntype: Runbook\n---\nJournal entry.\n")
	write("services/keycloak.md", "---\ntype: Runbook\n---\nA service.\n")

	policy := auth.Policy{Permissions: []auth.Permission{{KB: "docs", Types: []string{"Runbook"}}}}
	ctx := restrictedContext(policy)
	ids := []string{
		"ops/runbook", "ops/note", "ops/broken", "ops/bare", "ops/untyped", "ops/exp",
		"ops/pair", "diary/day", "services/keycloak", "ops/missing", "nomap", "ops/../escape",
	}
	check := func(step string) {
		t.Helper()
		for _, id := range ids {
			gm, gj, gt := conceptClass(k, id)
			wm, wj, wt := oracleConceptClass(k, id)
			if gm != wm || gj != wj || gt != wt {
				t.Fatalf("%s: conceptClass(%q) = %q/%q/%q, oracle %q/%q/%q", step, id, gm, gj, gt, wm, wj, wt)
			}
			want := policy.Allows("docs", wm, wj, wt, false)
			if got := Visible(ctx, k, id); got != want {
				t.Fatalf("%s: Visible(%q) = %v, oracle %v", step, id, got, want)
			}
		}
	}

	check("initial")
	backdate()
	check("warm")
	for _, step := range []struct {
		name string
		do   func()
	}{
		{"retype", func() { write("ops/note.md", "---\ntype: Runbook\n---\nNow a runbook.\n") }},
		{"break the frontmatter", func() { write("ops/runbook.md", "---\ntype: [oops\n---\nBroken.\n") }},
		{"delete", func() { _ = os.Remove(abs("ops/untyped.md")) }},
		{"expand", func() {
			if err := k.ExpandConcept("ops/note"); err != nil {
				t.Fatal(err)
			}
		}},
		{"collapse the pair", func() { _ = os.RemoveAll(abs("ops/pair")) }},
		{"graph read in between", func() {
			if _, err := k.GraphSnapshot(kb.GraphSnapshotOptions{}); err != nil {
				t.Fatal(err)
			}
		}},
		{"services retype", func() { write("services/keycloak.md", "---\ntype: Note\n---\nA service.\n") }},
	} {
		step.do()
		check(step.name)
		backdate()
		check(step.name + " (warm)")
	}

	target := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(target, []byte("---\ntype: Note\n---\nOutside.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(abs("ops/bare.md"))
	if err := os.Symlink(target, abs("ops/bare.md")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	check("symlinked concept")
	backdate()
	if err := os.WriteFile(target, []byte("---\ntype: Runbook\n---\nOutside, retyped.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	check("symlink target retyped")
}
