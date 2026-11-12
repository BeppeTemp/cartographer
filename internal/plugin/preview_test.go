package plugin

import (
	"bytes"
	"strconv"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/plugin/core"
)

func TestPreviewEscapesPluginStrings(t *testing.T) {
	p, probs := core.ReadTree(goodTree(), testOpts)
	if len(probs) != 0 {
		t.Fatal(probs)
	}
	for _, c := range p.Contributions["skills"] {
		if c.Description != "Helps with a thing, use it when asked." {
			t.Errorf("skill description not carried: %q", c.Description)
		}
	}
	if a := p.Contributions["agents"]; len(a) != 1 || a[0].Description != "Looks around." {
		t.Errorf("agent description not carried: %+v", a)
	}
	p.Contributions["skills"][0].Description = "d\x1b[0m"
	// ReadTree refuses control characters in a manifest; the preview must not
	// depend on that, so the fields are set directly.
	p.Manifest.Description = "x\x1b[31mred\nforged line"
	p.Manifest.DisplayName = "name\u202eevil"
	p.Manifest.Depends = map[string]string{"other-plugin": ">=1.0.0"}
	var buf bytes.Buffer
	Preview(&buf, p, PreviewInfo{Source: "https://example.com/x.git", Ref: "v\x1b1", SHA: strings.Repeat("a", 40),
		Have: func(string) (string, bool) { return "1.2.0", true }}, testOpts)
	out := buf.String()
	for _, r := range out {
		if r == 0x1b || r == 0x202e {
			t.Fatalf("a raw control character reached the preview:\n%q", out)
		}
	}
	for _, want := range []string{`\x1b[31mred\nforged line`, "depends on other-plugin >=1.0.0: installed 1.2.0, met", "needs Cartographer", `description: d\x1b[0m`, "description: Looks around.", "runs code on this machine", `"run.sh"`} {
		if !strings.Contains(out, want) {
			t.Errorf("preview lacks %q:\n%s", want, out)
		}
	}
	if strings.Count(out, "\n  forged line") != 0 {
		t.Errorf("a newline in the manifest forged a line:\n%s", out)
	}
}

// surveyor.star is code the Surveyor runs: the preview of the surveyor side lists
// it with its size, as it lists the executables of an exec recipe.
func TestPreviewListsTheSurveyorScript(t *testing.T) {
	script := "def l(r):\n    return {}\nrecipe(\"feed\", l)\n"
	manifest := "schema: 2\nname: acme-wiki\nversion: 1.0.0\ndisplay_name: Wiki\ndescription: A wiki reader.\ncartographer: \">=0.29.0\"\ncomponents:\n  surveyor:\n    sdk: {recipes: 1}\n"
	p, probs := core.ReadTree([]core.TreeFile{
		{Path: core.ManifestFile, Content: []byte(manifest)},
		{Path: core.SurveyorScript, Content: []byte(script)},
		{Path: "recipes/feed/instructions.md", Content: []byte("x")},
	}, core.Options{RunningVersion: "0.29.0"})
	_ = probs // the surveyor loaders are not linked in this test binary; the files are stored regardless
	var buf bytes.Buffer
	Preview(&buf, p, PreviewInfo{Side: core.SideSurveyor}, core.Options{RunningVersion: "0.29.0"})
	want := "surveyor.star (" + strconv.Itoa(len(script)) + " bytes) registers Starlark recipes (runs code on the Surveyor)"
	if !strings.Contains(buf.String(), want) {
		t.Errorf("preview lacks %q:\n%s", want, buf.String())
	}
}

// A declared private-network host is shown with the Surveyor key that grants it
// and with what the grant does not hold.
func TestPreviewNamesThePrivateGrant(t *testing.T) {
	manifest := "schema: 2\nname: acme-wiki\nversion: 1.0.0\ndisplay_name: Wiki\ndescription: A wiki reader.\ncartographer: \">=0.29.0\"\ncomponents:\n  surveyor:\n    sdk: {recipes: 1}\n    permissions: {net: [\"*.example.com\"], net_private: [wiki.example.com]}\n"
	p, _ := core.ReadTree([]core.TreeFile{{Path: core.ManifestFile, Content: []byte(manifest)}}, core.Options{RunningVersion: "0.29.0"})
	var buf bytes.Buffer
	Preview(&buf, p, PreviewInfo{Side: core.SideSurveyor}, core.Options{RunningVersion: "0.29.0"})
	for _, want := range []string{
		"may reach private-network hosts (once the Surveyor grants each): wiki.example.com",
		"under plugins.acme-wiki.allow_private_hosts: wiki.example.com",
		"exec recipes and connector processes are not network-checked",
	} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("preview lacks %q:\n%s", want, buf.String())
		}
	}
}
