package lint

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/kb"
)

// artifactFindingsFixture is a KB whose skills trip several artifact checks.
func artifactFindingsFixture(t *testing.T) (k *kb.KB, files []string) {
	t.Helper()
	k = tempKB(t)
	writeFile(t, k.Root, "instructions.md", "---\nperimeter: ops\n---\nKB.\n")
	writeFile(t, k.Root, "skills/loose/SKILL.md", skillMD("loose", "Loose one", "Run `git commit -m x`, then call kb_a__search.\n"))
	writeFile(t, k.Root, "skills/bad/SKILL.md", skillMD("other-name", "Mismatched", "# B\n"))
	writeFile(t, k.Root, "skills/long/SKILL.md", skillMD("long", "Long one", strings.Repeat("line\n", 600)))
	writeFile(t, k.Root, "skills/loose/.DS_Store", "x")
	// A concept with problems of its own: none of them is an artifact finding.
	writeFile(t, k.Root, "data/ops/broken.md", "no frontmatter at all\n")
	return k, []string{"skills/loose/SKILL.md", "skills/loose/.DS_Store"}
}

func keyed(findings []Finding) []string {
	var out []string
	for _, f := range findings {
		out = append(out, f.Check+"|"+f.Path+"|"+f.Message)
	}
	sort.Strings(out)
	return out
}

// The artifact checks are the same ones an unscoped Run reports: the scoped entry
// point only skips what has nothing to do with artifacts.
func TestArtifactFindings_SameArtifactChecksAsRun(t *testing.T) {
	t.Parallel()
	k, files := artifactFindingsFixture(t)
	all, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	// What a scoped run adds on top is outside checkArtifacts: the git-tree junk
	// pass, hooks, and the concept count that missing_instructions needs.
	var want []Finding
	for _, f := range all {
		switch f.Check {
		case "junk_file", "junk_asset", "hook_invalid", "missing_instructions":
			continue
		}
		if f.Artifact {
			want = append(want, f)
		}
	}
	var got []Finding
	for _, f := range ArtifactFindings(k, files) {
		if f.Check != "junk_file" {
			got = append(got, f)
		}
	}
	if len(want) == 0 {
		t.Fatal("the fixture produced no artifact finding: the comparison proves nothing")
	}
	if !reflect.DeepEqual(keyed(got), keyed(want)) {
		t.Errorf("ArtifactFindings differs from the artifact findings of Run\n got: %v\nwant: %v", keyed(got), keyed(want))
	}
}

func TestArtifactFindings_OnlyArtifactFindings(t *testing.T) {
	t.Parallel()
	k, files := artifactFindingsFixture(t)
	for _, f := range ArtifactFindings(k, files) {
		if !f.Artifact || f.Path == "" {
			t.Errorf("%s on %q is not an artifact finding", f.Check, f.Path)
		}
		if strings.HasPrefix(f.Path, "data/") {
			t.Errorf("a concept was linted: %v", f)
		}
	}
}

func TestArtifactFindings_JunkOnlyForTheGivenFiles(t *testing.T) {
	t.Parallel()
	k, files := artifactFindingsFixture(t)
	writeFile(t, k.Root, "skills/other/.DS_Store", "x")
	got := ArtifactFindings(k, files)
	if !hasCheck(got, "skills/loose/.DS_Store", "junk_file") {
		t.Errorf("want junk_file on the file asked about: %v", keyed(got))
	}
	if hasCheck(got, "skills/other/.DS_Store", "junk_file") {
		t.Errorf("junk_file on a file nobody asked about: %v", keyed(got))
	}
}
