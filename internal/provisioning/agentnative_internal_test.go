package provisioning

import (
	"encoding/json"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
	"gopkg.in/yaml.v3"
)

// Valid YAML scalars that are not valid TOML are rendered from the decoded
// value: True, .5, .inf, 017, 1e3 and a whole-number float.
func TestRenderNativeTOML_ScalarsAreRenderedFromTheDecodedValue(t *testing.T) {
	var doc yaml.Node
	src := "flag: True\nratio: .5\nlim: .inf\nneg: -.inf\nnope: .nan\noct: 017\nbig: 1e3\nwhole: 2.0\nname: x\n"
	if err := yaml.Unmarshal([]byte(src), &doc); err != nil {
		t.Fatal(err)
	}
	m := doc.Content[0]
	var fields []contract.NativeField
	for i := 0; i+1 < len(m.Content); i += 2 {
		fields = append(fields, contract.NativeField{Key: m.Content[i].Value, Value: m.Content[i+1]})
	}
	got, err := renderNativeTOML(fields)
	if err != nil {
		t.Fatal(err)
	}
	want := "flag = true\nratio = 0.5\nlim = inf\nneg = -inf\nnope = nan\noct = 15\nbig = 1000.0\nwhole = 2.0\nname = \"x\"\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// The shape real Codex transcripts carry: an object whose body lists one
// "- name: description (file: …)" line per skill, after the root lines.
func TestHostSkillNames_RealBodyShapeAndLists(t *testing.T) {
	body := "## Skills\n### Roots\n- `r0` = `/h/.codex/skills`\n### Available\n- ops-tool: does ops (file: r0/ops-tool/SKILL.md)\n- plug:other: x (file: r0/o/SKILL.md)\n"
	raw, _ := json.Marshal(map[string]any{"body": body, "includeInstructions": true})
	got := hostSkillNames(raw)
	if !got["ops-tool"] || !got["plug:other"] || got["r0"] || len(got) != 2 {
		t.Errorf("body shape: %v", got)
	}
	got = hostSkillNames(json.RawMessage(`["a", {"name": "b"}, 3]`))
	if !got["a"] || !got["b"] || len(got) != 2 {
		t.Errorf("list shape: %v", got)
	}
	if got := hostSkillNames(json.RawMessage(`42`)); len(got) != 0 {
		t.Errorf("unknown shape credited %v", got)
	}
}
