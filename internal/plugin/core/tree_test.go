package core

import (
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
)

const (
	skillMD = "---\nname: helper\ndescription: Helps with a thing, use it when asked.\n---\n\n# Helper\n\nDo the thing.\n"
	agentMD = "---\nname: scout\ndescription: Looks around.\n---\n\nLook around.\n"
	hookJS  = `{"event":"Stop","command":"./run.sh"}`
)

func tf(path, content string) TreeFile { return TreeFile{Path: path, Content: []byte(content)} }

func goodTree() []TreeFile {
	return []TreeFile{
		tf(ManifestFile, validManifest),
		tf("skills/helper/SKILL.md", skillMD),
		tf("skills/helper/notes.md", "notes"),
		tf("agents/scout.md", agentMD),
		tf("hooks/guard/hook.json", hookJS),
		{Path: "hooks/guard/run.sh", Content: []byte("#!/bin/sh\n"), Executable: true},
		tf("README.md", "ignored"),
		tf("LICENSE", "ignored"),
		tf(".gitignore", "ignored"),
		tf(".github/ci.yml", "ignored"),
	}
}

func TestIgnoredFilesAreNotChecked(t *testing.T) {
	files := append(goodTree(), TreeFile{Path: "README.txt", Special: true}, tf("CHANGELOG.md", strings.Repeat("x", contract.ArtifactMaxFileSize+1)))
	if _, probs := ReadTree(files, testOpts); len(probs) != 0 {
		t.Errorf("an ignored file is checked: %v", probs)
	}
}

func TestReadTreeManifestMissingAndTwoProblems(t *testing.T) {
	_, probs := ReadTree([]TreeFile{tf("foo/page", "x"), tf("mcp/b", "x")}, testOpts)
	joined := strings.Join(probs, "\n")
	for _, w := range []string{ManifestFile + ": missing", "unknown entry `foo`", "mcp/b"} {
		if !strings.Contains(joined, w) {
			t.Errorf("problems %q lack %q", joined, w)
		}
	}
	if !sort.StringsAreSorted(probs) {
		t.Errorf("problems not sorted: %v", probs)
	}
}

func TestReadTreeCaps(t *testing.T) {
	files := goodTree()
	for i := 0; i < MaxFiles; i++ {
		files = append(files, tf("skills/helper/f"+strings.Repeat("a", i%5)+string(rune('a'+i%26))+string(rune('a'+i/26%26))+string(rune('a'+i/676))+".txt", "x"))
	}
	_, probs := ReadTree(files, testOpts)
	if !strings.Contains(strings.Join(probs, "\n"), "over the 2000 cap") {
		t.Errorf("file count cap not reported: %v", probs)
	}
}

func TestDigest(t *testing.T) {
	a, _ := ReadTree(goodTree(), testOpts)
	rev := goodTree()
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	b, _ := ReadTree(rev, testOpts)
	if a.Digest == "" || a.Digest != b.Digest {
		t.Errorf("digest depends on file order: %q vs %q", a.Digest, b.Digest)
	}
	if !reflect.DeepEqual(a.Artifacts, b.Artifacts) {
		t.Error("artifacts depend on file order")
	}
	one := goodTree()
	one[2].Content = []byte("notez")
	if c, _ := ReadTree(one, testOpts); c.Digest == a.Digest {
		t.Error("digest ignores one changed byte")
	}
	exe := goodTree()
	exe[2].Executable = true
	if c, _ := ReadTree(exe, testOpts); c.Digest == a.Digest {
		t.Error("digest ignores the executable bit")
	}
	ign := append(goodTree(), tf("README.md", "changed"))
	ign[len(ign)-1] = tf("CHANGELOG.md", "new")
	if c, _ := ReadTree(ign, testOpts); c.Digest != a.Digest {
		t.Error("an ignored file changes the digest")
	}
}

// A script file is part of the digest: one byte of plugin.star or of a lib file
// moves it, so an approval of the old digest does not cover the new tree.
func TestScriptBytesMoveTheDigest(t *testing.T) {
	read := func(entry, lib string) string {
		t.Helper()
		p, probs := ReadTree(append(goodTree(), tf("plugin.star", entry), tf("lib/util.star", lib)), testOpts)
		if len(probs) != 0 {
			t.Fatal(probs)
		}
		return p.Digest
	}
	base := read("x = 1\n", "y = 1\n")
	if read("x = 2\n", "y = 1\n") == base || read("x = 1\n", "y = 2\n") == base {
		t.Error("a changed script byte left the digest alone")
	}
	if read("x = 1\n", "y = 1\n") != base {
		t.Error("the digest is not stable")
	}
}

func TestCheckScriptsRunsOnScriptTreesOnly(t *testing.T) {
	var got []string
	opts := testOpts
	opts.CheckScripts = func(_ Side, files []TreeFile) []string {
		for _, f := range files {
			got = append(got, f.Path)
		}
		return []string{"plugin.star:1:1: boom"}
	}
	if _, probs := ReadTree(goodTree(), opts); len(probs) != 0 || got != nil {
		t.Fatalf("a tree without scripts was checked: %v %v", probs, got)
	}
	_, probs := ReadTree(append(goodTree(), tf("plugin.star", "x"), tf("lib/a.star", "y")), opts)
	if strings.Join(got, ",") != "plugin.star,lib/a.star" || strings.Join(probs, "") != "plugin.star:1:1: boom" {
		t.Errorf("checked %v, problems %v", got, probs)
	}
}

func TestScriptsDoNotRunOnATreeWithOtherProblems(t *testing.T) {
	ran := false
	opts := testOpts
	opts.CheckScripts = func(Side, []TreeFile) []string { ran = true; return nil }
	script := tf("plugin.star", "x = 1\n")

	over := goodTree()
	for i := 0; i <= MaxFiles; i++ {
		over = append(over, tf(fmt.Sprintf("skills/s%d/SKILL.md", i), "---\nname: s\ndescription: A skill, use it when asked.\n---\n\n# S\n"))
	}
	if _, probs := ReadTree(append(over, script), opts); len(probs) == 0 || ran {
		t.Errorf("a tree over the file cap ran its script (ran=%v, problems %v)", ran, probs)
	}

	var noManifest []TreeFile
	for _, f := range goodTree() {
		if f.Path != ManifestFile {
			noManifest = append(noManifest, f)
		}
	}
	if _, probs := ReadTree(append(noManifest, script), opts); len(probs) == 0 || ran {
		t.Errorf("a tree without a manifest ran its script (ran=%v, problems %v)", ran, probs)
	}

	if _, probs := ReadTree(append(goodTree(), script), opts); len(probs) != 0 || !ran {
		t.Errorf("a valid tree did not run its script (ran=%v, problems %v)", ran, probs)
	}
}

func TestReadTreeDocsFolderIsIgnored(t *testing.T) {
	base, probs := ReadTree(goodTree(), testOpts)
	if len(probs) != 0 {
		t.Fatal(probs)
	}
	with, probs := ReadTree(append(goodTree(), tf("docs/page.txt", "x"), tf("Docs/img/b.png", "y")), testOpts)
	if len(probs) != 0 {
		t.Fatalf("a docs folder breaks validation: %v", probs)
	}
	if with.Digest != base.Digest || len(with.Files) != len(base.Files) {
		t.Errorf("docs/ is stored or digested: %s vs %s", with.Digest, base.Digest)
	}
}

// goldenGoodTreeDigest is the digest of goodTree(). A tree gets this digest in
// every binary, and changing it invalidates every approval ever made.
const goldenGoodTreeDigest = "2462899a395dd829eeb57df5860292931a035aff40a91e47c786b220c5ce3d06"

func TestDigestGolden(t *testing.T) {
	p, probs := ReadTree(goodTree(), testOpts)
	if len(probs) != 0 {
		t.Fatal(probs)
	}
	if p.Digest != goldenGoodTreeDigest {
		t.Errorf("digest = %s, want %s", p.Digest, goldenGoodTreeDigest)
	}
}

// ReadTree classifies a folder by family, so what it reports (digest, sides,
// unknown entries, problems) does not depend on which loaders a binary links;
// only the loaded contributions do. The registry is emptied, read, then given
// a loader for the agents folder and read again.
func TestReadTreeIndependentOfLoaders(t *testing.T) {
	cases := map[string]struct {
		files []TreeFile
		opts  Options
	}{
		"schema 1": {goodTree(), testOpts},
		"schema 2": {schema2Tree(validV2, tf("agents/scout.md", agentMD)), v2Opts},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			WithoutPoints(t)
			bare, bp := ReadTree(c.files, c.opts)
			Register(Point{
				Dir: "agents", Side: SideClient,
				Load: func([]TreeFile) ([]Contribution, []string) {
					return []Contribution{{Point: "agents", Kind: "agent", Name: "x"}}, nil
				},
				Preview: func(io.Writer, Contribution) {},
			})
			linked, lp := ReadTree(c.files, c.opts)
			sort.Strings(lp)
			sort.Strings(bp)
			if linked.Digest != bare.Digest || !reflect.DeepEqual(linked.Sides, bare.Sides) ||
				!reflect.DeepEqual(linked.Unknown, bare.Unknown) || !reflect.DeepEqual(linked.Unsupported, bare.Unsupported) {
				t.Errorf("linked %+v, bare %+v", linked, bare)
			}
			if len(bare.Contributions) != 0 || len(linked.Contributions["agents"]) == 0 {
				t.Errorf("contributions: bare %+v, linked %+v", bare.Contributions, linked.Contributions)
			}
			if !reflect.DeepEqual(lp, bp) {
				t.Errorf("problems differ: %v vs %v", lp, bp)
			}
		})
	}
}
