package mcpserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/BeppeTemp/cartographer/internal/skillbundle"
)

func installBundle() fstest.MapFS {
	return fstest.MapFS{
		"bundled/cartographer-ops/SKILL.md": &fstest.MapFile{
			Data: []byte("---\nname: cartographer-ops\ndescription: Operate\n---\nBody.\n"),
		},
		"bundled/cartographer-ops/references/guide.md": &fstest.MapFile{Data: []byte("guide\n")},
	}
}

// outsideDir returns a directory outside the KB holding one file, and the
// check that nothing in it changed.
func outsideDir(t *testing.T, rel string) (dir string, unchanged func()) {
	t.Helper()
	dir = t.TempDir()
	target := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := listFiles(t, dir)
	return dir, func() {
		t.Helper()
		if after := listFiles(t, dir); after != before {
			t.Errorf("the directory outside the KB changed:\nbefore %s\nafter  %s", before, after)
		}
		if data, err := os.ReadFile(target); err != nil || string(data) != "outside" {
			t.Errorf("a file outside the KB changed: %q %v", data, err)
		}
	}
}

func listFiles(t *testing.T, dir string) string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		out = append(out, strings.TrimPrefix(p, dir))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join(out, ",")
}

// skill_install writes only files that are inside the KB: a link on skills/,
// on skills/<name> or on any file or directory the bundled skill would write
// is refused, with and without force, and nothing is written before the
// refusal.
func TestSkillInstall_RefusesALinkOnItsDestination(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		setup func(t *testing.T, root, outside string)
		file  string   // a file of the outside directory
		kept  bool     // the skill directory itself exists, so a call without force is "already installed"
		clean []string // KB paths that must not exist after the refusal: nothing is written before it
	}{
		{"skills", func(t *testing.T, root, outside string) {
			if err := os.RemoveAll(filepath.Join(root, "skills")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, filepath.Join(root, "skills")); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
		}, "cartographer-ops/SKILL.md", false, nil},
		{"skill directory", func(t *testing.T, root, outside string) {
			if err := os.MkdirAll(filepath.Join(root, "skills"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, filepath.Join(root, "skills", "cartographer-ops")); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
		}, "SKILL.md", false, nil},
		{"skill file", func(t *testing.T, root, outside string) {
			if err := os.MkdirAll(filepath.Join(root, "skills", "cartographer-ops"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(outside, "SKILL.md"), filepath.Join(root, "skills", "cartographer-ops", "SKILL.md")); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
		}, "SKILL.md", true, []string{"skills/cartographer-ops/references"}},
		{"skill subdirectory", func(t *testing.T, root, outside string) {
			if err := os.MkdirAll(filepath.Join(root, "skills", "cartographer-ops"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, filepath.Join(root, "skills", "cartographer-ops", "references")); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
		}, "guide.md", true, []string{"skills/cartographer-ops/SKILL.md"}},
	}
	for _, tc := range cases {
		for _, force := range []bool{false, true} {
			name := tc.name + "/force=false"
			if force {
				name = tc.name + "/force=true"
			}
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				k := setupTestKB(t)
				outside, unchanged := outsideDir(t, tc.file)
				tc.setup(t, k.Root, outside)
				s := New("1.0.0")
				RegisterKBTools(s, k, Deps{BundleFS: installBundle()})

				res := callTool(t, s, "skill_install", `{"name":"cartographer-ops","force":`+boolJSON(force)+`}`)
				if !res.IsError {
					t.Fatalf("want a refusal, got %+v", res.Content)
				}
				if !(tc.kept && !force) && !containsText(res, "symlink") {
					t.Fatalf("want a symlink refusal, got %+v", res.Content)
				}
				unchanged()
				for _, rel := range tc.clean {
					if _, err := os.Lstat(filepath.Join(k.Root, filepath.FromSlash(rel))); err == nil {
						t.Errorf("%s was written before the refusal", rel)
					}
				}
			})
		}
	}
}

func boolJSON(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// The name is a slug: a separator, a dot segment or an uppercase letter is
// refused before the bundle is looked up, so the destination is always
// skills/<slug>.
func TestSkillInstall_NameIsASlug(t *testing.T) {
	t.Parallel()
	for _, name := range []string{
		"../hooks", "..", ".", "cartographer-ops/references", "cartographer-ops/", "/cartographer-ops",
		"skills/../cartographer-ops", "Cartographer-Ops", "cartographer_ops", `cartographer-ops\references`,
	} {
		k := setupTestKB(t)
		s := New("1.0.0")
		RegisterKBTools(s, k, Deps{BundleFS: installBundle()})
		args, _ := json.Marshal(map[string]any{"name": name, "force": true})
		res := callTool(t, s, "skill_install", string(args))
		if !res.IsError {
			t.Errorf("name %q installed: %+v", name, res.Content)
		}
		if entries, err := os.ReadDir(filepath.Join(k.Root, "skills")); err == nil && len(entries) != 0 {
			t.Errorf("name %q created %v under skills/", name, entries)
		}
	}
}

// Every name the bundle carries, and every name it carried before it was
// renamed, is a slug, so the check above never refuses a real skill.
func TestSkillInstall_EveryBundledNameIsASlug(t *testing.T) {
	t.Parallel()
	entries, err := os.ReadDir(filepath.Join("..", "skillbundle", "bundled"))
	if err != nil || len(entries) == 0 {
		t.Fatalf("bundled skills: %d %v", len(entries), err)
	}
	for _, e := range entries {
		if e.IsDir() && !artifactSlugPattern.MatchString(e.Name()) {
			t.Errorf("bundled skill %q is not a slug", e.Name())
		}
	}
	for old, current := range skillbundle.LegacyNames() {
		if !artifactSlugPattern.MatchString(old) || !artifactSlugPattern.MatchString(current) {
			t.Errorf("legacy name %q -> %q is not a slug", old, current)
		}
	}
}
