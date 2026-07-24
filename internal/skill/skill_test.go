package skill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/BeppeTemp/cartographer/internal/skillbundle"
)

// writeSkillMD creates a SKILL.md file in the given directory with the provided content.
func writeSkillMD(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
}

const validSkillContent = `---
name: my-skill
description: Activates when doing something useful
license: MIT
version: 1.2.3
service_ref: services/my-service
---
# My Skill

This is the skill body.
`

func TestLoadSkill(t *testing.T) {
	dir := t.TempDir()
	writeSkillMD(t, dir, validSkillContent)

	s, err := LoadSkill(dir)
	if err != nil {
		t.Fatalf("LoadSkill: %v", err)
	}

	if s.Name != "my-skill" {
		t.Errorf("Name = %q; want %q", s.Name, "my-skill")
	}
	if s.Description != "Activates when doing something useful" {
		t.Errorf("Description = %q; want %q", s.Description, "Activates when doing something useful")
	}
	if s.License != "MIT" {
		t.Errorf("License = %q; want %q", s.License, "MIT")
	}
	if s.Version != "1.2.3" {
		t.Errorf("Version = %q; want %q", s.Version, "1.2.3")
	}
	if s.ServiceRef != "services/my-service" {
		t.Errorf("ServiceRef = %q; want %q", s.ServiceRef, "services/my-service")
	}
	if !strings.Contains(s.Body, "# My Skill") {
		t.Errorf("Body does not contain heading; got %q", s.Body)
	}
	if s.DirPath != dir {
		t.Errorf("DirPath = %q; want %q", s.DirPath, dir)
	}
}

func TestLoadSkillMissingVersion(t *testing.T) {
	// Test that metadata.version nested field is handled via flat key.
	content := `---
name: nested-skill
description: Uses metadata.version
metadata.version: 2.0.0
---
Body here.
`
	dir := t.TempDir()
	writeSkillMD(t, dir, content)

	s, err := LoadSkill(dir)
	if err != nil {
		t.Fatalf("LoadSkill: %v", err)
	}
	if s.Version != "2.0.0" {
		t.Errorf("Version = %q; want %q", s.Version, "2.0.0")
	}
}

func TestLoadSkillMissing(t *testing.T) {
	dir := t.TempDir()
	// No SKILL.md written.

	_, err := LoadSkill(dir)
	if err == nil {
		t.Fatal("expected error for missing SKILL.md, got nil")
	}
}

func TestLoadAllSkills(t *testing.T) {
	kbRoot := t.TempDir()
	skillsDir := filepath.Join(kbRoot, "skills")
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create two skill directories.
	for _, name := range []string{"myns--skill-one", "myns--skill-two"} {
		d := filepath.Join(skillsDir, name)
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
		content := "---\nname: " + name + "\ndescription: Test skill\n---\nBody.\n"
		writeSkillMD(t, d, content)
	}

	skills, errs := LoadAllSkills(kbRoot)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(skills) != 2 {
		t.Fatalf("len(skills) = %d; want 2", len(skills))
	}

	// Verify DirPaths are relative to kbRoot.
	for _, s := range skills {
		if !strings.HasPrefix(s.DirPath, "skills/") {
			t.Errorf("DirPath %q does not start with skills/", s.DirPath)
		}
	}
}

func TestLoadAllSkillsPartialError(t *testing.T) {
	kbRoot := t.TempDir()
	skillsDir := filepath.Join(kbRoot, "skills")
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		t.Fatal(err)
	}

	// One valid skill, one directory without SKILL.md.
	good := filepath.Join(skillsDir, "ns--good")
	if err := os.MkdirAll(good, 0755); err != nil {
		t.Fatal(err)
	}
	writeSkillMD(t, good, "---\nname: good\ndescription: Good skill\n---\nBody.\n")

	bad := filepath.Join(skillsDir, "ns--bad")
	if err := os.MkdirAll(bad, 0755); err != nil {
		t.Fatal(err)
	}
	// No SKILL.md in bad.

	skills, errs := LoadAllSkills(kbRoot)
	if len(skills) != 1 {
		t.Errorf("len(skills) = %d; want 1", len(skills))
	}
	if len(errs) != 1 {
		t.Errorf("len(errs) = %d; want 1", len(errs))
	}
}

func TestCatalog(t *testing.T) {
	skills := []Skill{
		{Name: "alpha", Description: "First skill", Version: "1.0.0", DirPath: "skills/ns--alpha"},
		{Name: "beta", Description: "Second skill", DirPath: "skills/ns--beta"},
	}

	entries := Catalog(skills)
	if len(entries) != 2 {
		t.Fatalf("len(entries) = %d; want 2", len(entries))
	}
	if entries[0].Name != "alpha" {
		t.Errorf("entries[0].Name = %q; want %q", entries[0].Name, "alpha")
	}
	if entries[0].Version != "1.0.0" {
		t.Errorf("entries[0].Version = %q; want %q", entries[0].Version, "1.0.0")
	}
	if entries[1].Version != "" {
		t.Errorf("entries[1].Version = %q; want empty", entries[1].Version)
	}
	if entries[0].Path != "skills/ns--alpha" {
		t.Errorf("entries[0].Path = %q; want %q", entries[0].Path, "skills/ns--alpha")
	}
}

func TestValidate(t *testing.T) {
	t.Run("valid skill", func(t *testing.T) {
		s := &Skill{Name: "ok", Description: "Good desc", DirPath: "skills/ns--ok"}
		issues := Validate(s)
		if len(issues) != 0 {
			t.Errorf("expected no issues, got %v", issues)
		}
	})

	t.Run("missing name", func(t *testing.T) {
		s := &Skill{Name: "", Description: "desc", DirPath: "skills/ns--x"}
		issues := Validate(s)
		if len(issues) != 1 {
			t.Fatalf("expected 1 issue, got %d", len(issues))
		}
		if issues[0].Warning {
			t.Error("missing name should be an error, not a warning")
		}
	})

	t.Run("missing description", func(t *testing.T) {
		s := &Skill{Name: "x", Description: "", DirPath: "skills/ns--x"}
		issues := Validate(s)
		if len(issues) != 1 {
			t.Fatalf("expected 1 issue, got %d", len(issues))
		}
		if issues[0].Warning {
			t.Error("missing description should be an error, not a warning")
		}
	})

	t.Run("body too long", func(t *testing.T) {
		lines := make([]string, 501)
		for i := range lines {
			lines[i] = "line"
		}
		s := &Skill{Name: "x", Description: "desc", Body: strings.Join(lines, "\n"), DirPath: "skills/ns--x"}
		issues := Validate(s)
		if len(issues) != 1 {
			t.Fatalf("expected 1 issue, got %d", len(issues))
		}
		if !issues[0].Warning {
			t.Error("long body should be a warning, not an error")
		}
	})

	t.Run("missing name and description", func(t *testing.T) {
		s := &Skill{DirPath: "skills/ns--x"}
		issues := Validate(s)
		if len(issues) != 2 {
			t.Fatalf("expected 2 issues, got %d", len(issues))
		}
	})
}

func TestLoadAllFromFS(t *testing.T) {
	fsys := fstest.MapFS{
		"bundled/kb-create/SKILL.md": &fstest.MapFile{
			Data: []byte("---\nname: kb-create\ndescription: Guide KB creation\nversion: \"1.0\"\n---\nBody here.\n"),
		},
		"bundled/other-skill/SKILL.md": &fstest.MapFile{
			Data: []byte("---\nname: other-skill\ndescription: Another skill\n---\nBody.\n"),
		},
	}

	skills, errs := LoadAllFromFS(fsys, "bundled")
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(skills) != 2 {
		t.Fatalf("len(skills) = %d; want 2", len(skills))
	}

	found := map[string]bool{}
	for _, s := range skills {
		found[s.Name] = true
	}
	if !found["kb-create"] {
		t.Error("kb-create skill not found")
	}
	if !found["other-skill"] {
		t.Error("other-skill not found")
	}
}

func TestBundledSkillsValidate(t *testing.T) {
	skills, errs := LoadAllFromFS(skillbundle.FS, "bundled")
	if len(errs) != 0 {
		t.Fatalf("load bundled skills: %v", errs)
	}

	found := make(map[string]bool, len(skills))
	for i := range skills {
		found[skills[i].Name] = true
		if issues := Validate(&skills[i]); len(issues) != 0 {
			t.Errorf("bundled skill %q failed validation: %v", skills[i].Name, issues)
		}
	}

	for _, name := range []string{"cartographer-ops", "kb-conflict-resolve", "kb-create", "kb-import"} {
		if !found[name] {
			t.Errorf("bundled skill %q not found", name)
		}
	}
}

func TestLoadAllFromFSPartialError(t *testing.T) {
	fsys := fstest.MapFS{
		"bundled/good/SKILL.md": &fstest.MapFile{
			Data: []byte("---\nname: good\ndescription: Good skill\n---\nBody.\n"),
		},
		// bad/ has no SKILL.md — simulate by not adding one
		"bundled/bad/other.txt": &fstest.MapFile{
			Data: []byte("not a skill"),
		},
	}

	skills, errs := LoadAllFromFS(fsys, "bundled")
	if len(skills) != 1 {
		t.Errorf("len(skills) = %d; want 1", len(skills))
	}
	if len(errs) != 1 {
		t.Errorf("len(errs) = %d; want 1", len(errs))
	}
}

func TestNamespaceExtraction(t *testing.T) {
	// Verify the <namespace>--<skill-name> convention parses correctly.
	cases := []struct {
		dirName   string
		wantNS    string
		wantSkill string
	}{
		{"myns--myskill", "myns", "myskill"},
		{"foo--bar-baz", "foo", "bar-baz"},
		{"noskill", "", ""},
	}

	for _, tc := range cases {
		parts := strings.SplitN(tc.dirName, "--", 2)
		if tc.wantNS == "" {
			// No separator expected.
			if len(parts) == 2 {
				t.Errorf("%q: unexpected split into %v", tc.dirName, parts)
			}
			continue
		}
		if len(parts) != 2 {
			t.Errorf("%q: expected split, got %v", tc.dirName, parts)
			continue
		}
		if parts[0] != tc.wantNS {
			t.Errorf("%q: namespace = %q; want %q", tc.dirName, parts[0], tc.wantNS)
		}
		if parts[1] != tc.wantSkill {
			t.Errorf("%q: skill = %q; want %q", tc.dirName, parts[1], tc.wantSkill)
		}
	}
}
