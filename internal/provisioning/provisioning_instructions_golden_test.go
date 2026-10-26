package provisioning_test

// Golden pins of the brief managed instructions section (D56/D182, brief since
// D1182). The server-generated section and its ContentHash are pinned on
// purpose: changing them re-syncs every provisioned client's steering file.
// D1182 changed them once; the golden values below are the new baseline.

import (
	"path/filepath"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

const goldenRouting = "The \"kb-a\" KB is served via MCP by the \"cartographer\" server. Its tools are not prefixed: where a tool takes a `kb` argument, pass `kb: \"kb-a\"` — that connection serves several KBs through one set of tools and never infers which one you mean."

const goldenScope = "The following directives are the \"kb-a\" KB's own and govern work in its perimeter; where they conflict with another KB's directives or with a repository's own instruction file, the more specific source wins.\n\n"

func goldenBlock(t *testing.T, setup func(root string)) provisioning.Artifact {
	t.Helper()
	root := makeKBWithArchives(t, nil)
	setup(root)
	m, err := provisioning.BuildManifest(nil, map[string]string{"kb-a": root}, provisioning.BuildOptions{RoutedMount: true})
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}
	return findInstructionsArtifact(t, m, "kb-a")
}

func TestGolden_Block_RoutedNoArchive(t *testing.T) {
	a := goldenBlock(t, func(string) {})
	want := goldenRouting + " No archives detected yet.\n\n"
	if got := string(a.Files[0].Content); got != want {
		t.Errorf("block changed:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestGolden_Block_BriefWithCuratedAndSkills(t *testing.T) {
	a := goldenBlock(t, func(root string) {
		writeFile(t, filepath.Join(root, "data", "journal", "a.md"), "# a\n")
		writeFile(t, filepath.Join(root, "data", "entities", "b.md"), "# b\n")
		writeFile(t, filepath.Join(root, "instructions.md"), "Read the index first.\n")
		writeBriefSkill(t, root, "zeta-skill")
		writeBriefSkill(t, root, "alpha-skill")
	})
	want := goldenRouting + " Archives: entities/, journal/.\n\n" + "Skills: alpha-skill, zeta-skill.\n"
	if got := string(a.Files[0].Content); got != want {
		t.Errorf("block changed:\ngot:\n%s\nwant:\n%s", got, want)
	}
	const wantHash = "8aee4fa52e7684b7f685f551be10659975c44ce609bb95fc1c60cfb72537a3bd"
	if a.ContentHash != wantHash {
		t.Errorf("ContentHash = %s, want %s", a.ContentHash, wantHash)
	}
}

func TestGolden_Block_FullKeepsCuratedBody(t *testing.T) {
	a := goldenBlock(t, func(root string) {
		writeFile(t, filepath.Join(root, "data", "entities", "b.md"), "# b\n")
		writeFile(t, filepath.Join(root, "instructions.md"), "<!-- cartographer: block: full -->\nRead the index first.\n")
	})
	want := goldenRouting + " Archives: entities/.\n\n" + "\n" + goldenScope + "Read the index first.\n"
	if got := string(a.Files[0].Content); got != want {
		t.Errorf("block changed:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestGolden_Block_PreambleNone(t *testing.T) {
	a := goldenBlock(t, func(root string) {
		writeFile(t, filepath.Join(root, "data", "entities", "b.md"), "# b\n")
		writeFile(t, filepath.Join(root, "instructions.md"), "<!-- cartographer: preamble: none -->\n<!-- cartographer: block: full -->\nOnly this.\n")
	})
	want := "<!-- cartographer: preamble: none -->\n" + goldenRouting + " Archives: entities/.\n\n" + "\n" + goldenScope + "Only this.\n"
	if got := string(a.Files[0].Content); got != want {
		t.Errorf("block changed:\ngot:\n%s\nwant:\n%s", got, want)
	}
}
