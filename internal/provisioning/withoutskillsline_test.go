package provisioning_test

import (
	"path/filepath"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// TestWithoutSkillsLineMatchesGenerate pins #1408: stripping the skills line of
// the generated block gives, byte for byte and hash for hash, the block of the
// same KB with no skill. The curated body of a `block: full` KB, which may carry
// a "Skills:" line of its own, is never touched.
func TestWithoutSkillsLineMatchesGenerate(t *testing.T) {
	for _, tc := range []struct{ name, instructions string }{
		{"brief", "Read the index first.\n"},
		{"no instructions file", ""},
		{"full", "<!-- cartographer: block: full -->\nSkills: curated, not generated.\n"},
		{"preamble none", "<!-- cartographer: preamble: none -->\n<!-- cartographer: block: full -->\nOnly this.\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			build := func(withSkills bool) provisioning.Artifact {
				return goldenBlock(t, func(root string) {
					writeFile(t, filepath.Join(root, "data", "entities", "b.md"), "# b\n")
					if tc.instructions != "" {
						writeFile(t, filepath.Join(root, "instructions.md"), tc.instructions)
					}
					if withSkills {
						writeBriefSkill(t, root, "zeta-skill")
						writeBriefSkill(t, root, "alpha-skill")
					}
				})
			}
			with, without := build(true), build(false)
			got := provisioning.WithoutSkillsLine(with)
			if string(got.Files[0].Content) != string(without.Files[0].Content) {
				t.Errorf("got:\n%q\nwant:\n%q", got.Files[0].Content, without.Files[0].Content)
			}
			if got.ContentHash != without.ContentHash {
				t.Errorf("hash %s, want %s", got.ContentHash, without.ContentHash)
			}
			if again := provisioning.WithoutSkillsLine(got); string(again.Files[0].Content) != string(got.Files[0].Content) {
				t.Error("not idempotent")
			}
			if string(with.Files[0].Content) == string(got.Files[0].Content) {
				t.Error("input was not rewritten into a copy")
			}
		})
	}
}

func TestWithoutSkillsLineLeavesOthersAlone(t *testing.T) {
	skill := provisioning.Artifact{Kind: "skill", Name: "s", Source: "kb:kb-a"}
	if got := provisioning.WithoutSkillsLine(skill); got.Name != "s" || got.Files != nil {
		t.Errorf("skill changed: %+v", got)
	}
	bundle := provisioning.Artifact{Kind: "instructions", Name: "x", Source: "bundle",
		Files: []provisioning.ArtifactFile{{Path: "instructions.md", Content: []byte("a\n\nSkills: x.\n")}}}
	if got := provisioning.WithoutSkillsLine(bundle); string(got.Files[0].Content) != "a\n\nSkills: x.\n" {
		t.Error("non-KB artifact rewritten")
	}
}
