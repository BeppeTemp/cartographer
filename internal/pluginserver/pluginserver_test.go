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

const atlasManifest = "schema: 2\nname: acme-brand\nversion: 1.0.0\ndisplay_name: Acme brand\ndescription: Acme branding.\ncartographer: \">=0.29.0\"\ncomponents:\n  atlas:\n    sdk: {branding: 1}\n"

const brandSVG = `<svg xmlns="http://www.w3.org/2000/svg"><rect width="1" height="1"/></svg>`

func readAtlas(t *testing.T, manifest string, files ...core.TreeFile) (core.Plugin, []string) {
	t.Helper()
	tree := append([]core.TreeFile{tf(core.ManifestFile, manifest)}, files...)
	return core.ReadTree(tree, core.Options{RunningVersion: "0.29.0", Side: core.SideAtlas})
}

func TestBrandingLoads(t *testing.T) {
	p, probs := readAtlas(t, atlasManifest,
		tf("branding/branding.yaml", "company: Acme\nlogo: {light: logo.svg}\n"), tf("branding/logo.svg", brandSVG))
	if len(probs) != 0 {
		t.Fatal(probs)
	}
	c := p.Contributions["branding"]
	if len(c) != 1 || c[0].Branding == nil || c[0].Branding.LogoLight.Name != "logo.svg" {
		t.Fatalf("contributions = %+v", c)
	}
	var out strings.Builder
	previewBranding(&out, c[0])
	for _, want := range []string{"logo light logo.svg", `company "Acme"`, `still reads "Cartographer"`} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("preview lacks %q:\n%s", want, out.String())
		}
	}
}

func TestBrandingRefuses(t *testing.T) {
	for name, tc := range map[string]struct {
		manifest string
		files    []core.TreeFile
		want     string
	}{
		"no sdk declaration": {strings.Replace(atlasManifest, "sdk: {branding: 1}", "sdk: {}", 1), []core.TreeFile{tf("branding/branding.yaml", "logo: {light: f.svg}\n")}, "declare sdk.branding under components.atlas"},
		"an accent":          {atlasManifest, []core.TreeFile{tf("branding/branding.yaml", "accent: \"#365d50\"\n")}, "accent: removed; the branding carries logos and a company name only"},
		"a favicon":          {atlasManifest, []core.TreeFile{tf("branding/branding.yaml", "logo: {light: f.svg}\nfavicon: f.svg\n"), tf("branding/f.svg", brandSVG)}, "favicon: removed; the branding carries logos and a company name only"},
		"script in the svg":  {atlasManifest, []core.TreeFile{tf("branding/branding.yaml", "logo: {light: f.svg}\n"), tf("branding/f.svg", `<svg xmlns="http://www.w3.org/2000/svg"><script/></svg>`)}, "element script"},
		"a sub-folder":       {atlasManifest, []core.TreeFile{tf("branding/branding.yaml", "logo: {light: x.svg}\n"), tf("branding/x/y.svg", brandSVG)}, "the folder is flat"},
	} {
		_, probs := readAtlas(t, tc.manifest, tc.files...)
		if !strings.Contains(strings.Join(probs, "\n"), tc.want) {
			t.Errorf("%s: %v", name, probs)
		}
	}
}

func plugin(t *testing.T, name string, branded bool) *core.Plugin {
	t.Helper()
	files := []core.TreeFile{tf(core.ManifestFile, strings.Replace(atlasManifest, "acme-brand", name, 1))}
	if branded {
		files = append(files, tf("branding/branding.yaml", "logo: {light: f.svg}\n"), tf("branding/f.svg", brandSVG))
	}
	p, probs := core.ReadTree(files, core.Options{RunningVersion: "0.29.0", Side: core.SideAtlas})
	if len(probs) != 0 {
		t.Fatal(probs)
	}
	return &p
}

// Exactly one active branding: none is no branding, two is a start error naming both.
func TestActiveBrandingExactlyOne(t *testing.T) {
	if a, err := ActiveBranding(nil); a != nil || err != nil {
		t.Errorf("none: %v %v", a, err)
	}
	if a, err := ActiveBranding([]*core.Plugin{plugin(t, "no-brand", false)}); a != nil || err != nil {
		t.Errorf("a plugin without branding: %v %v", a, err)
	}
	a, err := ActiveBranding([]*core.Plugin{plugin(t, "acme-brand", true), plugin(t, "no-brand", false)})
	if err != nil || a == nil || a.Plugin != "acme-brand" {
		t.Errorf("one: %+v %v", a, err)
	}
	_, err = ActiveBranding([]*core.Plugin{plugin(t, "other-brand", true), plugin(t, "acme-brand", true)})
	if err == nil || !strings.Contains(err.Error(), "acme-brand and other-brand") || !strings.Contains(err.Error(), "enabled: false") {
		t.Errorf("two: %v", err)
	}
}
