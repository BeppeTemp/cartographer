package plugin

import (
	"bytes"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/provisioning"
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

func TestReadTreeValid(t *testing.T) {
	p, probs := ReadTree(goodTree(), testOpts)
	if len(probs) != 0 {
		t.Fatalf("problems: %v", probs)
	}
	if p.Manifest.Name != "team-forks" {
		t.Errorf("manifest not read: %+v", p.Manifest)
	}
	if len(p.Files) != 6 {
		t.Errorf("stored %d files, want 6 (manifest + 5, ignored entries dropped)", len(p.Files))
	}
	if len(p.Artifacts) != 3 {
		t.Fatalf("artifacts: %+v", p.Artifacts)
	}
	for _, a := range p.Artifacts {
		if a.Source != "plugin:team-forks" || a.ContentHash != provisioning.ContentHashFiles(a.Files) {
			t.Errorf("artifact %s/%s: source %q hash %q", a.Kind, a.Name, a.Source, a.ContentHash)
		}
	}
	hooks := p.Contributions["hooks"]
	if len(hooks) != 1 || !reflect.DeepEqual(hooks[0].Executable, []string{"run.sh"}) {
		t.Errorf("hook executable content: %+v", hooks)
	}
	var buf bytes.Buffer
	RegisteredPointsPreview(&buf, p)
	if !strings.Contains(buf.String(), `runs: "run.sh"`) {
		t.Errorf("preview: %q", buf.String())
	}
}

// RegisteredPointsPreview prints each contribution through its point.
func RegisteredPointsPreview(buf *bytes.Buffer, p Plugin) {
	for _, pt := range RegisteredPoints() {
		for _, c := range p.Contributions[pt.Dir] {
			pt.Preview(buf, c)
		}
	}
}

func TestReadTreeProblems(t *testing.T) {
	big := strings.Repeat("x", contract.ArtifactMaxFileSize+1)
	cases := []struct {
		name  string
		extra []TreeFile
		want  string
	}{
		{"unknown entry", []TreeFile{tf("foo/page.txt", "x")}, "unknown entry `foo`: a plugin holds cartographer-plugin.yaml, plugin.star, lib/, agents/, hooks/, providers/, skills/ (README*, LICENSE*, CHANGELOG*, docs/ and .git* are ignored)"},
		{"mcp", []TreeFile{tf("mcp/x.json", "{}")}, `"mcp/x.json": not a plugin contribution point in this version`},
		{"instructions", []TreeFile{tf("instructions.md", "x")}, `"instructions.md": not a plugin contribution point`},
		{"lib without plugin.star", []TreeFile{tf("lib/a.star", "x")}, "a plugin with lib files needs a plugin.star"},
		{"lib subdirectory", []TreeFile{tf("plugin.star", ""), tf("lib/sub/a.star", "x")}, `"lib/sub/a.star": a plugin holds plugin.star`},
		{"lib other extension", []TreeFile{tf("plugin.star", ""), tf("lib/a.py", "x")}, `"lib/a.py": a plugin holds plugin.star`},
		{"nested plugin.star", []TreeFile{tf("plugin.star/x", "x")}, `"plugin.star/x": a plugin holds plugin.star`},
		{"script over the cap", []TreeFile{tf("plugin.star", strings.Repeat("#", MaxScriptBytes+1))}, "over the 262144 cap"},
		{"symlink", []TreeFile{{Path: "skills/helper/link", Special: true}}, `"skills/helper/link": a symlink or non-regular file`},
		{"oversized", []TreeFile{tf("skills/helper/big.txt", big)}, "over the 262144 cap of a file"},
		{"bad hook.json", []TreeFile{tf("hooks/bad/hook.json", "{")}, `hook "bad": hook.json:`},
		{"hook without hook.json", []TreeFile{tf("hooks/bad/run.sh", "x")}, `hook "bad": no hook.json`},
		{"reserved hook", []TreeFile{tf("hooks/cartographer-bootstrap/hook.json", hookJS)}, `hook "cartographer-bootstrap": the name is reserved`},
		{"bundled skill", []TreeFile{tf("skills/cartographer-cli/SKILL.md", strings.Replace(skillMD, "helper", "cartographer-cli", 1))}, "a plugin cannot replace a bundled skill"},
		{"skill without SKILL.md", []TreeFile{tf("skills/empty/x.md", "x")}, `skill "empty": no SKILL.md`},
		{"bundled feedback skill", []TreeFile{tf("skills/cartographer-feedback/SKILL.md", strings.Replace(skillMD, "helper", "cartographer-feedback", 1))}, "a plugin cannot replace a bundled skill"},
		{"skill name mismatch", []TreeFile{tf("skills/other/SKILL.md", skillMD)}, `skill "other":`},
		{"hook case alias", []TreeFile{tf("hooks/Cartographer-Bootstrap/hook.json", hookJS)}, "the name is reserved"},
		{"skill case alias of a bundled one", []TreeFile{tf("skills/Cartographer-Feedback/SKILL.md", skillMD)}, "a plugin cannot replace a bundled skill"},
		{"cli skill case alias", []TreeFile{tf("skills/CARTOGRAPHER-CLI/SKILL.md", skillMD)}, "a plugin cannot replace a bundled skill"},
		{"control character", []TreeFile{tf("skills/helper/a\x1b[2Jb", "x")}, "printable characters"},
		{"bidi override", []TreeFile{tf("skills/helper/a\u202eb", "x")}, "printable characters"},
		{"bad agent", []TreeFile{tf("agents/bad.md", "no frontmatter")}, `agent "bad":`},
		{"file in skills root", []TreeFile{tf("skills/README.txt", "x")}, "a file directly in skills/"},
		{"nested agent", []TreeFile{tf("agents/a/b.md", "x")}, "expected a file agents/<name>.md"},
		{"bad path", []TreeFile{tf("skills/helper/../x", "x")}, "not a canonical relative path"},
		{"case clash in a skill", []TreeFile{tf("skills/helper/NOTES.md", "x")}, "duplicate path"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, probs := ReadTree(append(goodTree(), c.extra...), testOpts)
			joined := strings.Join(probs, "\n")
			if !strings.Contains(joined, c.want) {
				t.Errorf("problems %q lack %q", joined, c.want)
			}
		})
	}
}

func TestIgnoredFilesAreNotChecked(t *testing.T) {
	files := append(goodTree(), TreeFile{Path: "README.txt", Special: true}, tf("CHANGELOG.md", strings.Repeat("x", contract.ArtifactMaxFileSize+1)))
	if _, probs := ReadTree(files, testOpts); len(probs) != 0 {
		t.Errorf("an ignored file is checked: %v", probs)
	}
}

func TestHookHashUsesEffectiveExecutable(t *testing.T) {
	files := []TreeFile{
		tf(ManifestFile, validManifest),
		{Path: "hooks/guard/hook.json", Content: []byte(hookJS), Executable: true},
		{Path: "hooks/guard/run.sh", Content: []byte("#!/bin/sh\n")},
	}
	p, probs := ReadTree(files, testOpts)
	if len(probs) != 0 {
		t.Fatal(probs)
	}
	for _, f := range p.Artifacts[0].Files {
		if want := f.Path != "hook.json"; f.Executable != want {
			t.Errorf("%s executable %v, want %v", f.Path, f.Executable, want)
		}
	}
	if got := p.Contributions["hooks"][0]; got.Command != "./run.sh" || got.Event != "Stop" || !reflect.DeepEqual(got.Executable, []string{"run.sh"}) {
		t.Errorf("hook contribution: %+v", got)
	}
}

func TestPreviewListsSkillExecutable(t *testing.T) {
	files := append(goodTree(), TreeFile{Path: "skills/helper/go.sh", Content: []byte("x"), Executable: true})
	p, _ := ReadTree(files, testOpts)
	var buf bytes.Buffer
	RegisteredPointsPreview(&buf, p)
	if !strings.Contains(buf.String(), `runs: "go.sh"`) || !strings.Contains(buf.String(), `command: "./run.sh" on event "Stop"`) {
		t.Errorf("preview: %q", buf.String())
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

func TestRegisteredPoints(t *testing.T) {
	var got []string
	for _, p := range RegisteredPoints() {
		got = append(got, p.Dir)
	}
	if !reflect.DeepEqual(got, []string{"agents", "hooks", "providers", "reports", "skills"}) {
		t.Errorf("registered points: %v", got)
	}
}

func TestRegisterPanics(t *testing.T) {
	p := Point{Dir: "skills", Side: SideClient, Load: loadSkills, Preview: previewArtifact}
	for name, pt := range map[string]Point{
		"duplicate": p,
		"bad dir":   {Dir: "Bad/Dir", Side: SideClient, Load: loadSkills, Preview: previewArtifact},
		"bad side":  {Dir: "extra", Side: "both", Load: loadSkills, Preview: previewArtifact},
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s: Register did not panic", name)
				}
			}()
			Register(pt)
		}()
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
