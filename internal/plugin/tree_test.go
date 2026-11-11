package plugin

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/plugin/core"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
)

const (
	skillMD = "---\nname: helper\ndescription: Helps with a thing, use it when asked.\n---\n\n# Helper\n\nDo the thing.\n"
	agentMD = "---\nname: scout\ndescription: Looks around.\n---\n\nLook around.\n"
	hookJS  = `{"event":"Stop","command":"./run.sh"}`
)

func tf(path, content string) core.TreeFile {
	return core.TreeFile{Path: path, Content: []byte(content)}
}

func goodTree() []core.TreeFile {
	return []core.TreeFile{
		tf(core.ManifestFile, validManifest),
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
	p, probs := core.ReadTree(goodTree(), testOpts)
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
func RegisteredPointsPreview(buf *bytes.Buffer, p core.Plugin) {
	for _, pt := range core.RegisteredPoints() {
		for _, c := range p.Contributions[pt.Dir] {
			pt.Preview(buf, c)
		}
	}
}

func TestReadTreeProblems(t *testing.T) {
	big := strings.Repeat("x", contract.ArtifactMaxFileSize+1)
	cases := []struct {
		name  string
		extra []core.TreeFile
		want  string
	}{
		{"unknown entry", []core.TreeFile{tf("foo/page.txt", "x")}, "unknown entry `foo`: a plugin holds cartographer-plugin.yaml, plugin.star, lib/, agents/, hooks/, providers/, skills/ (README*, LICENSE*, CHANGELOG*, docs/ and .git* are ignored)"},
		{"mcp", []core.TreeFile{tf("mcp/x.json", "{}")}, `"mcp/x.json": not a plugin contribution point in this version`},
		{"instructions", []core.TreeFile{tf("instructions.md", "x")}, `"instructions.md": not a plugin contribution point`},
		{"lib without plugin.star", []core.TreeFile{tf("lib/a.star", "x")}, "a plugin with lib files needs a plugin.star"},
		{"lib subdirectory", []core.TreeFile{tf("plugin.star", ""), tf("lib/sub/a.star", "x")}, `"lib/sub/a.star": a plugin holds plugin.star`},
		{"lib other extension", []core.TreeFile{tf("plugin.star", ""), tf("lib/a.py", "x")}, `"lib/a.py": a plugin holds plugin.star`},
		{"nested plugin.star", []core.TreeFile{tf("plugin.star/x", "x")}, `"plugin.star/x": a plugin holds plugin.star`},
		{"script over the cap", []core.TreeFile{tf("plugin.star", strings.Repeat("#", core.MaxScriptBytes+1))}, "over the 262144 cap"},
		{"symlink", []core.TreeFile{{Path: "skills/helper/link", Special: true}}, `"skills/helper/link": a symlink or non-regular file`},
		{"oversized", []core.TreeFile{tf("skills/helper/big.txt", big)}, "over the 262144 cap of a file"},
		{"bad hook.json", []core.TreeFile{tf("hooks/bad/hook.json", "{")}, `hook "bad": hook.json:`},
		{"hook without hook.json", []core.TreeFile{tf("hooks/bad/run.sh", "x")}, `hook "bad": no hook.json`},
		{"reserved hook", []core.TreeFile{tf("hooks/cartographer-bootstrap/hook.json", hookJS)}, `hook "cartographer-bootstrap": the name is reserved`},
		{"bundled skill", []core.TreeFile{tf("skills/cartographer-cli/SKILL.md", strings.Replace(skillMD, "helper", "cartographer-cli", 1))}, "a plugin cannot replace a bundled skill"},
		{"skill without SKILL.md", []core.TreeFile{tf("skills/empty/x.md", "x")}, `skill "empty": no SKILL.md`},
		{"bundled feedback skill", []core.TreeFile{tf("skills/cartographer-feedback/SKILL.md", strings.Replace(skillMD, "helper", "cartographer-feedback", 1))}, "a plugin cannot replace a bundled skill"},
		{"skill name mismatch", []core.TreeFile{tf("skills/other/SKILL.md", skillMD)}, `skill "other":`},
		{"hook case alias", []core.TreeFile{tf("hooks/Cartographer-Bootstrap/hook.json", hookJS)}, "the name is reserved"},
		{"skill case alias of a bundled one", []core.TreeFile{tf("skills/Cartographer-Feedback/SKILL.md", skillMD)}, "a plugin cannot replace a bundled skill"},
		{"cli skill case alias", []core.TreeFile{tf("skills/CARTOGRAPHER-CLI/SKILL.md", skillMD)}, "a plugin cannot replace a bundled skill"},
		{"control character", []core.TreeFile{tf("skills/helper/a\x1b[2Jb", "x")}, "printable characters"},
		{"bidi override", []core.TreeFile{tf("skills/helper/a\u202eb", "x")}, "printable characters"},
		{"bad agent", []core.TreeFile{tf("agents/bad.md", "no frontmatter")}, `agent "bad":`},
		{"file in skills root", []core.TreeFile{tf("skills/README.txt", "x")}, "a file directly in skills/"},
		{"nested agent", []core.TreeFile{tf("agents/a/b.md", "x")}, "expected a file agents/<name>.md"},
		{"bad path", []core.TreeFile{tf("skills/helper/../x", "x")}, "not a canonical relative path"},
		{"case clash in a skill", []core.TreeFile{tf("skills/helper/NOTES.md", "x")}, "duplicate path"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, probs := core.ReadTree(append(goodTree(), c.extra...), testOpts)
			joined := strings.Join(probs, "\n")
			if !strings.Contains(joined, c.want) {
				t.Errorf("problems %q lack %q", joined, c.want)
			}
		})
	}
}

func TestHookHashUsesEffectiveExecutable(t *testing.T) {
	files := []core.TreeFile{
		tf(core.ManifestFile, validManifest),
		{Path: "hooks/guard/hook.json", Content: []byte(hookJS), Executable: true},
		{Path: "hooks/guard/run.sh", Content: []byte("#!/bin/sh\n")},
	}
	p, probs := core.ReadTree(files, testOpts)
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
	files := append(goodTree(), core.TreeFile{Path: "skills/helper/go.sh", Content: []byte("x"), Executable: true})
	p, _ := core.ReadTree(files, testOpts)
	var buf bytes.Buffer
	RegisteredPointsPreview(&buf, p)
	if !strings.Contains(buf.String(), `runs: "go.sh"`) || !strings.Contains(buf.String(), `command: "./run.sh" on event "Stop"`) {
		t.Errorf("preview: %q", buf.String())
	}
}

func TestRegisteredPoints(t *testing.T) {
	var got []string
	for _, p := range core.RegisteredPoints() {
		got = append(got, p.Dir)
	}
	if !reflect.DeepEqual(got, []string{"agents", "hooks", "providers", "reports", "skills"}) {
		t.Errorf("registered points: %v", got)
	}
}

func TestRegisterPanics(t *testing.T) {
	p := core.Point{Dir: "skills", Side: core.SideClient, Load: loadSkills, Preview: previewArtifact}
	for name, pt := range map[string]core.Point{
		"duplicate": p,
		"bad dir":   {Dir: "Bad/Dir", Side: core.SideClient, Load: loadSkills, Preview: previewArtifact},
		"bad side":  {Dir: "extra", Side: "both", Load: loadSkills, Preview: previewArtifact},
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s: Register did not panic", name)
				}
			}()
			core.Register(pt)
		}()
	}
}

// goldenGoodTreeDigest is the digest of goodTree(). A tree gets this digest in
// every binary, and changing it invalidates every approval ever made.
const goldenGoodTreeDigest = "2462899a395dd829eeb57df5860292931a035aff40a91e47c786b220c5ce3d06"

func TestDigestGolden(t *testing.T) {
	p, probs := core.ReadTree(goodTree(), testOpts)
	if len(probs) != 0 {
		t.Fatal(probs)
	}
	if p.Digest != goldenGoodTreeDigest {
		t.Errorf("digest = %s, want %s", p.Digest, goldenGoodTreeDigest)
	}
}
