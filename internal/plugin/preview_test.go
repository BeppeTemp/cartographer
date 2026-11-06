package plugin

import (
	"bytes"
	"strings"
	"testing"
)

func TestPreviewEscapesPluginStrings(t *testing.T) {
	p, probs := ReadTree(goodTree(), testOpts)
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
