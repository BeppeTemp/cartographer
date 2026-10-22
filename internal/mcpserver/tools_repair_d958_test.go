package mcpserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/BeppeTemp/cartographer/internal/kb"
)

const legacySkillText = "---\nname: kb-create\ndescription: Guide KB creation\n---\nLocal edits live here.\n"

func writeKBSkill(t *testing.T, k *kb.KB, dir, content string) {
	t.Helper()
	abs := filepath.Join(k.Root, "skills", dir, "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := k.CommitOp("test: skills/" + dir); err != nil {
		t.Fatal(err)
	}
}

// A KB copy under a pre-D958 name moves to the new name in one commit, keeps
// its body, takes the new frontmatter name, and a second run has nothing to do.
func TestRepairLegacySkillName_MovesTheCopyInOneCommit(t *testing.T) {
	t.Parallel()
	k, s := repairKB(t, 0)
	writeKBSkill(t, k, "kb-create", legacySkillText)
	if n := countFindings(t, k, "legacy_skill_name"); n != 1 {
		t.Fatalf("want one legacy_skill_name finding, got %d", n)
	}

	before := commitCount(t, k)
	out := repairApply(t, s, "legacy_skill_name")
	if out["applied"].(float64) != 1 {
		t.Fatalf("applied = %v, want 1: %v", out["applied"], out)
	}
	if got := commitCount(t, k); got != before+1 {
		t.Fatalf("commits = %d, want %d: the move is one commit", got, before+1)
	}
	if _, err := os.Stat(filepath.Join(k.Root, "skills", "kb-create")); !os.IsNotExist(err) {
		t.Fatalf("the legacy directory is still there: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(k.Root, "skills", "cartographer-kb-create", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(raw); !strings.Contains(got, "name: cartographer-kb-create") || !strings.Contains(got, "Local edits live here.") {
		t.Fatalf("moved skill must carry the new name and keep its body:\n%s", got)
	}
	if n := countFindings(t, k, "legacy_skill_name"); n != 0 {
		t.Fatalf("legacy_skill_name still reported after the repair: %d", n)
	}

	again := repairApply(t, s, "legacy_skill_name")
	if again["applied"].(float64) != 0 || commitCount(t, k) != before+1 {
		t.Fatalf("a second run must change nothing: %v", again)
	}
}

// With both copies present the repair reports a conflict and touches neither.
func TestRepairLegacySkillName_ConflictLeavesBothCopies(t *testing.T) {
	t.Parallel()
	k, s := repairKB(t, 0)
	current := "---\nname: cartographer-kb-create\ndescription: Guide KB creation\n---\nCurrent.\n"
	writeKBSkill(t, k, "kb-create", legacySkillText)
	writeKBSkill(t, k, "cartographer-kb-create", current)

	before := commitCount(t, k)
	out := repairApply(t, s, "legacy_skill_name")
	if out["applied"].(float64) != 0 {
		t.Fatalf("a conflict must not apply: %v", out)
	}
	skipped, _ := out["skipped"].([]any)
	if len(skipped) != 1 || !strings.Contains(skipped[0].(map[string]any)["reason"].(string), "conflict") {
		t.Fatalf("want one skip naming the conflict: %v", out["skipped"])
	}
	if commitCount(t, k) != before {
		t.Fatal("a conflict must not commit")
	}
	for dir, want := range map[string]string{"kb-create": legacySkillText, "cartographer-kb-create": current} {
		raw, err := os.ReadFile(filepath.Join(k.Root, "skills", dir, "SKILL.md"))
		if err != nil || string(raw) != want {
			t.Fatalf("skills/%s changed: %v %q", dir, err, raw)
		}
	}
}

// skill_install still answers to a name the bundle had before D958, installs
// under the current name, and says so.
func TestSkillInstall_LegacyNameInstallsUnderTheNewName(t *testing.T) {
	t.Parallel()
	k := setupTestKB(t)
	bundleFS := fstest.MapFS{
		"bundled/cartographer-kb-create/SKILL.md": &fstest.MapFile{
			Data: []byte("---\nname: cartographer-kb-create\ndescription: Guide KB creation\n---\nBody.\n"),
		},
	}
	s := New("1.0.0")
	RegisterKBTools(s, k, Deps{BundleFS: bundleFS})

	res := callTool(t, s, "skill_install", `{"name":"kb-create"}`)
	if res.IsError {
		t.Fatalf("skill_install: %v", res.Content)
	}
	text := res.Content[0].Text
	if !strings.Contains(text, `"renamed_from": "kb-create"`) || !strings.Contains(text, "cartographer-kb-create") {
		t.Fatalf("the result must name the rename: %s", text)
	}
	if _, err := os.Stat(filepath.Join(k.Root, "skills", "cartographer-kb-create", "SKILL.md")); err != nil {
		t.Fatalf("not installed under the new name: %v", err)
	}
	if _, err := os.Stat(filepath.Join(k.Root, "skills", "kb-create")); !os.IsNotExist(err) {
		t.Fatalf("the legacy directory must not appear: %v", err)
	}
}
