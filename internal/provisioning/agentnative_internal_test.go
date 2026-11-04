package provisioning

import (
	"testing"

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
	var fields []nativeField
	for i := 0; i+1 < len(m.Content); i += 2 {
		fields = append(fields, nativeField{Key: m.Content[i].Value, Value: m.Content[i+1]})
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
