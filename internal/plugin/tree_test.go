package plugin

import (
	"bytes"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/provisioning"
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
	big := strings.Repeat("x", provisioning.ArtifactMaxFileSize+1)
	cases := []struct {
		name  string
		extra []TreeFile
		want  string
	}{
		{"unknown entry", []TreeFile{tf("docs/page.txt", "x")}, "unknown entry `docs`: a plugin holds agents/, hooks/, skills/"},
		{"mcp", []TreeFile{tf("mcp/x.json", "{}")}, `"mcp/x.json": not a plugin contribution point in this version`},
		{"instructions", []TreeFile{tf("instructions.md", "x")}, `"instructions.md": not a plugin contribution point`},
		{"plugin.star", []TreeFile{tf("plugin.star", "x")}, "plugin scripts are not supported yet"},
		{"lib", []TreeFile{tf("lib/a.star", "x")}, "plugin scripts are not supported yet"},
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
	files := append(goodTree(), TreeFile{Path: "README.txt", Special: true}, tf("CHANGELOG.md", strings.Repeat("x", provisioning.ArtifactMaxFileSize+1)))
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
	_, probs := ReadTree([]TreeFile{tf("docs/page", "x"), tf("mcp/b", "x")}, testOpts)
	joined := strings.Join(probs, "\n")
	for _, w := range []string{ManifestFile + ": missing", "unknown entry `docs`", "mcp/b"} {
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
	if !reflect.DeepEqual(got, []string{"agents", "hooks", "skills"}) {
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
