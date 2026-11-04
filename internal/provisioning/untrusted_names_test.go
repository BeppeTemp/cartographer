package provisioning

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
)

const hookingSkill = "---\nname: s\ndescription: d\nhooks:\n  Stop: []\n---\nbody\n"

func TestArtifactRequiresApproval_NonCanonicalPathsAreNotAWayAround(t *testing.T) {
	for _, p := range []string{"SKILL.md", "./SKILL.md", "a/../SKILL.md", "skill.md/"} {
		a := Artifact{Kind: "skill", Name: "s", Source: "kb:kb-a", ContentHash: "h",
			Files: []ArtifactFile{{Path: p, Content: []byte(hookingSkill)}}}
		if !ArtifactRequiresApproval(a, ApplyOptions{}) {
			t.Errorf("path %q: a skill declaring hooks must require approval", p)
		}
	}
}

func TestApply_NonCanonicalFilePathsAreRefusedAndWriteNothing(t *testing.T) {
	cases := map[string][]ArtifactFile{
		"dot-slash": {{Path: "./SKILL.md", Content: []byte(hookingSkill)}},
		"dotdot":    {{Path: "a/../SKILL.md", Content: []byte(hookingSkill)}},
		"duplicate": {{Path: "SKILL.md", Content: []byte("---\nname: s\ndescription: d\n---\n")}, {Path: "./SKILL.md", Content: []byte(hookingSkill)}},
		"case-dup":  {{Path: "SKILL.md", Content: []byte("x")}, {Path: "skill.md", Content: []byte("y")}},
	}
	for name, files := range cases {
		t.Run(name, func(t *testing.T) {
			p := configurator.ProviderClaudeCode
			base := t.TempDir()
			a := Artifact{Kind: "skill", Name: "s", Source: "kb:kb-a", ContentHash: "h", Files: files}
			_, _ = Apply(Manifest{Revision: "h", Artifacts: []Artifact{a}}, ApplyOptions{Provider: p, BaseDir: base})
			for _, f := range []string{"SKILL.md", "skill.md"} {
				if exists(filepath.Join(installedAt(p, base, a), f)) {
					t.Fatalf("%s: %s was written", name, f)
				}
			}
		})
	}
	if err := CheckArtifactFilePaths([]ArtifactFile{{Path: "a/b.md"}, {Path: "SKILL.md"}}); err != nil {
		t.Fatalf("canonical paths: %v", err)
	}
}

func TestValidArtifactName_RefusesDotsSeparatorsAndReservedNames(t *testing.T) {
	for _, n := range []string{"", ".", "..", "a/b", `a\b`, "con", "NUL", "com1", "LPT9", "aux.txt"} {
		if validArtifactName(n) || ownableName(n) {
			t.Errorf("%q must not be a valid artifact name", n)
		}
	}
	for _, n := range []string{"my-skill", "console", "comm", "a.b"} {
		if !validArtifactName(n) {
			t.Errorf("%q must stay valid", n)
		}
	}
}

func TestApply_ArtifactNamedDotNeverTouchesTheUsersOwnSkill(t *testing.T) {
	p := configurator.ProviderClaudeCode
	base := t.TempDir()
	own := filepath.Join(base, ".claude", "skills", "my-own-skill", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(own), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(own, []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{".", ".."} {
		a := Artifact{Kind: "skill", Name: name, Source: "kb:kb-a", ContentHash: "h",
			Files: []ArtifactFile{{Path: "my-own-skill/SKILL.md", Content: []byte("evil")}}}
		res, _ := Apply(Manifest{Revision: "h", Artifacts: []Artifact{a}}, ApplyOptions{Provider: p, BaseDir: base})
		if len(res.Refused) != 1 {
			t.Errorf("name %q must be refused: %+v", name, res)
		}
		if b, _ := os.ReadFile(own); string(b) != "mine" {
			t.Fatalf("name %q overwrote the user's skill: %q", name, b)
		}
	}
}

func TestValidArtifactFilePath_RefusesWindowsSpellings(t *testing.T) {
	for _, p := range []string{"SKILL.md.", "SKILL.md ", "SKILL.md./x", "SKILL.md /x", "a/SKILL.md..", "SKILL.md:stream", "SKILL.md::$DATA", "con", "a/NUL.txt", "notes/com1/x.txt", "a/aux.txt"} {
		if ValidArtifactFilePath(p) {
			t.Errorf("%q must be refused", p)
		}
		if CheckArtifactFilePaths([]ArtifactFile{{Path: p}}) == nil {
			t.Errorf("%q must fail CheckArtifactFilePaths", p)
		}
	}
	for _, p := range []string{"SKILL.md", "a/b.md", "notes/console.txt", ".hidden/x", "a.b/c"} {
		if !ValidArtifactFilePath(p) {
			t.Errorf("%q must stay valid", p)
		}
	}
}

func TestValidArtifactName_RefusesTrailingDotBlankAndStream(t *testing.T) {
	for _, n := range []string{"foo.", "foo..", "foo ", "foo:bar"} {
		if validArtifactName(n) || ownableName(n) {
			t.Errorf("%q must not be a valid artifact name", n)
		}
	}
}

func TestApply_WindowsSpellingsOfSkillMDAreRefusedAndWriteNothing(t *testing.T) {
	for _, path := range []string{"SKILL.md.", "SKILL.md ", "SKILL.md./x"} {
		p := configurator.ProviderClaudeCode
		base := t.TempDir()
		a := Artifact{Kind: "skill", Name: "s", Source: "kb:kb-a", ContentHash: "h",
			Files: []ArtifactFile{{Path: path, Content: []byte(hookingSkill)}}}
		if !ArtifactRequiresApproval(a, ApplyOptions{}) && ValidArtifactFilePath(path) {
			t.Errorf("%q: neither gated nor refused", path)
		}
		res, err := Apply(Manifest{Revision: "h", Artifacts: []Artifact{a}}, ApplyOptions{Provider: p, BaseDir: base})
		if err != nil || len(res.Refused) != 1 {
			t.Errorf("%q: want one refusal and no error, got %v %+v", path, err, res)
		}
		if exists(installedAt(p, base, a)) {
			t.Errorf("%q: left %s behind", path, installedAt(p, base, a))
		}
	}
}

func TestApply_RefusedFilePathSkipsOnlyThatArtifactAndLeavesNoDir(t *testing.T) {
	p := configurator.ProviderClaudeCode
	base := t.TempDir()
	bad := Artifact{Kind: "skill", Name: "bad", Source: "kb:kb-a", ContentHash: "h1",
		Files: []ArtifactFile{{Path: "../SKILL.md", Content: []byte("x")}}}
	good := Artifact{Kind: "skill", Name: "good", Source: "kb:kb-a", ContentHash: "h2",
		Files: []ArtifactFile{{Path: "SKILL.md", Content: []byte("---\nname: good\ndescription: d\n---\n")}}}
	res, err := Apply(Manifest{Revision: "h", Artifacts: []Artifact{bad, good}}, ApplyOptions{Provider: p, BaseDir: base})
	if err != nil {
		t.Fatalf("a refused artifact must not abort the sync: %v", err)
	}
	if len(res.Refused) != 1 || res.Refused[0].Name != "bad" {
		t.Fatalf("refused = %+v", res.Refused)
	}
	if exists(installedAt(p, base, bad)) {
		t.Fatalf("empty destination directory left behind")
	}
	if !exists(filepath.Join(installedAt(p, base, good), "SKILL.md")) {
		t.Fatal("the other artifact was not installed")
	}
	if !IsRefusal(CheckArtifactFilePaths(bad.Files)) {
		t.Fatal("a refused file path must be a refusal")
	}
}
