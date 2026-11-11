package pluginserver

import (
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/plugin/core"
)

const manifest = "schema: 2\nname: team-data\nversion: 1.0.0\ndisplay_name: Team data\ndescription: Server data.\ncartographer: \">=0.29.0\"\ncomponents:\n  server:\n    sdk: {roles: 1, presets: 1}\n"

const goodRole = "description: Edit runbooks.\nrules:\n  - access: rw\n    maps: [infra]\n    types: [Runbook]\n  - access: r\n"

const goodTemplate = "---\ntype: Runbook\ntitle: {{title}}\n---\n# Procedure\n\nSteps.\n"

const goodPreset = "description: Runbook map.\ntitle: Runbooks\nkind: map\nconcept_types: [Runbook]\nrequired_fields:\n  Runbook: [owner]\ntemplates: [runbook]\ndefault_template: runbook\n"

func tf(path, content string) core.TreeFile {
	return core.TreeFile{Path: path, Content: []byte(content)}
}

func read(t *testing.T, files ...core.TreeFile) (core.Plugin, []string) {
	t.Helper()
	tree := append([]core.TreeFile{tf(core.ManifestFile, manifest)}, files...)
	return core.ReadTree(tree, core.Options{RunningVersion: "0.29.0", Side: core.SideServer})
}

func TestServerFamiliesLoad(t *testing.T) {
	p, probs := read(t, tf("roles/runbook-editor.yaml", goodRole), tf("presets/runbooks/preset.yaml", goodPreset), tf("presets/runbooks/templates/runbook.md", goodTemplate))
	if len(probs) != 0 {
		t.Fatal(probs)
	}
	r := p.Contributions["roles"]
	if len(r) != 1 || r[0].Name != "runbook-editor" || len(r[0].Role.Rules) != 2 || !r[0].Role.Rules[0].Declared["maps"] || r[0].Role.Rules[1].Declared["maps"] {
		t.Fatalf("roles = %+v", r)
	}
	pr := p.Contributions["presets"]
	if len(pr) != 1 || pr[0].Preset.Kind != "map" || len(pr[0].Preset.Templates) != 1 || !strings.Contains(string(pr[0].Preset.Create), `"default_template":"runbook"`) {
		t.Fatalf("presets = %+v", pr)
	}
}

func TestServerFamiliesRefuse(t *testing.T) {
	exec := core.TreeFile{Path: "roles/x.yaml", Content: []byte(goodRole), Executable: true}
	one := func(path, content string) []core.TreeFile { return []core.TreeFile{tf(path, content)} }
	for name, tc := range map[string]struct {
		files []core.TreeFile
		want  string
	}{
		"kb in a role":        {one("roles/x.yaml", "description: d.\nrules:\n  - kb: kb-a\n    access: r\n"), "kb is not allowed"},
		"no rules":            {one("roles/x.yaml", "description: d.\nrules: []\n"), "at least one rule"},
		"empty selector":      {one("roles/x.yaml", "description: d.\nrules:\n  - access: r\n    maps: []\n"), "declared but empty"},
		"artifacts rw on r":   {one("roles/x.yaml", "description: d.\nrules:\n  - access: r\n    artifacts: rw\n"), "artifacts"},
		"null selector":       {one("roles/x.yaml", "description: d.\nrules:\n  - access: r\n    maps:\n"), "declared but empty"},
		"tilde selector":      {one("roles/x.yaml", "description: d.\nrules:\n  - access: r\n    journals: ~\n"), "declared but empty"},
		"unknown role key":    {one("roles/x.yaml", "description: d.\nrules:\n  - access: r\n    admin: true\n"), "admin"},
		"executable role":     {[]core.TreeFile{exec}, "executable"},
		"script in a family":  {one("roles/x.sh", "echo"), "expected a file roles/<name>.yaml"},
		"template x-template": {[]core.TreeFile{tf("presets/p/preset.yaml", goodPreset), tf("presets/p/templates/runbook.md", "---\ntype: Runbook\nx-template.nope: [a]\n---\n# H\n")}, "templates/runbook.md"},
		"unknown contract":    {[]core.TreeFile{tf("presets/p/preset.yaml", goodPreset+"stale_after: 3\n"), tf("presets/p/templates/runbook.md", goodTemplate)}, "stale_after"},
		"missing slug":        {one("presets/p/preset.yaml", goodPreset), "not a template of this preset"},
		"no preset.yaml":      {one("presets/p/templates/runbook.md", goodTemplate), "preset.yaml: missing"},
		"stray file":          {[]core.TreeFile{tf("presets/p/preset.yaml", goodPreset), tf("presets/p/run.sh", "x")}, "run.sh"},
	} {
		_, probs := read(t, tc.files...)
		if !strings.Contains(strings.Join(probs, "\n"), tc.want) {
			t.Errorf("%s: problems %q lack %q", name, probs, tc.want)
		}
	}
}
