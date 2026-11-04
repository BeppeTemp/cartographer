package skill

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// A SKILL.md that is a symlink, and a skills/ directory that is one, are not
// read: the file on the other side is not the KB's.
func TestLoad_DoesNotReadThroughALink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on Windows")
	}
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "SKILL.md"), []byte(validSkillContent), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "skills", "linked"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "SKILL.md"), filepath.Join(root, "skills", "linked", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "skills", "real"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeSkillMD(t, filepath.Join(root, "skills", "real"), validSkillContent)

	skills, errs := LoadAllSkills(root)
	if len(skills) != 1 || skills[0].DirPath != "skills/real" {
		t.Fatalf("skills = %+v, want only skills/real", skills)
	}
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "symlink") {
		t.Fatalf("errs = %v, want one naming the symlink", errs)
	}

	linkedRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(outside, "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeSkillMD(t, filepath.Join(outside, "x"), validSkillContent)
	if err := os.Symlink(outside, filepath.Join(linkedRoot, "skills")); err != nil {
		t.Fatal(err)
	}
	skills, errs = LoadAllSkills(linkedRoot)
	if len(skills) != 0 || len(errs) != 1 || !strings.Contains(errs[0].Error(), "symlink") {
		t.Fatalf("a skills/ that is a link: skills=%+v errs=%v", skills, errs)
	}
}
