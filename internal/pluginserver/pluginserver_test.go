package pluginserver

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	pl "github.com/BeppeTemp/cartographer/internal/plugin"
	"github.com/BeppeTemp/cartographer/internal/plugin/core"
)

const goodDashboard = "title: Planner\ndescription: Open work.\nwidgets:\n  - id: open\n    type: stat\n  - id: notes\n    type: note\n    text: hello\n"

const dashOnlyPreset = "description: Dashboards only.\n"

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
		"kb in a role":               {one("roles/x.yaml", "description: d.\nrules:\n  - kb: kb-a\n    access: r\n"), "kb is not allowed"},
		"no rules":                   {one("roles/x.yaml", "description: d.\nrules: []\n"), "at least one rule"},
		"empty selector":             {one("roles/x.yaml", "description: d.\nrules:\n  - access: r\n    maps: []\n"), "declared but empty"},
		"artifacts rw on r":          {one("roles/x.yaml", "description: d.\nrules:\n  - access: r\n    artifacts: rw\n"), "artifacts"},
		"null selector":              {one("roles/x.yaml", "description: d.\nrules:\n  - access: r\n    maps:\n"), "declared but empty"},
		"tilde selector":             {one("roles/x.yaml", "description: d.\nrules:\n  - access: r\n    journals: ~\n"), "declared but empty"},
		"unknown role key":           {one("roles/x.yaml", "description: d.\nrules:\n  - access: r\n    admin: true\n"), "admin"},
		"executable role":            {[]core.TreeFile{exec}, "executable"},
		"script in a family":         {one("roles/x.sh", "echo"), "expected a file roles/<name>.yaml"},
		"template x-template":        {[]core.TreeFile{tf("presets/p/preset.yaml", goodPreset), tf("presets/p/templates/runbook.md", "---\ntype: Runbook\nx-template.nope: [a]\n---\n# H\n")}, "templates/runbook.md"},
		"unknown contract":           {[]core.TreeFile{tf("presets/p/preset.yaml", goodPreset+"stale_after: 3\n"), tf("presets/p/templates/runbook.md", goodTemplate)}, "stale_after"},
		"missing slug":               {one("presets/p/preset.yaml", goodPreset), "not a template of this preset"},
		"no preset.yaml":             {one("presets/p/templates/runbook.md", goodTemplate), "preset.yaml: missing"},
		"dashboard unknown key":      {[]core.TreeFile{tf("presets/p/preset.yaml", dashOnlyPreset), tf("presets/p/dashboards/x.yaml", "widgets:\n  - id: a\n    type: stat\n    colour: red\n")}, "presets/p/dashboards/x.yaml"},
		"dashboard bad name":         {[]core.TreeFile{tf("presets/p/preset.yaml", dashOnlyPreset), tf("presets/p/dashboards/Bad Name.yaml", goodDashboard)}, "dashboards/<name>.yaml"},
		"dashboard nested":           {[]core.TreeFile{tf("presets/p/preset.yaml", dashOnlyPreset), tf("presets/p/dashboards/a/b.yaml", goodDashboard)}, "dashboards/<name>.yaml"},
		"dashboard wrong ext":        {[]core.TreeFile{tf("presets/p/preset.yaml", dashOnlyPreset), tf("presets/p/dashboards/a.yml", goodDashboard)}, "dashboards/<name>.yaml"},
		"empty preset":               {one("presets/p/preset.yaml", dashOnlyPreset), "needs a map contract, dashboards or both"},
		"dashboards with a template": {[]core.TreeFile{tf("presets/p/preset.yaml", dashOnlyPreset), tf("presets/p/dashboards/a.yaml", goodDashboard), tf("presets/p/templates/runbook.md", goodTemplate)}, "templates need a map contract"},
		"too many dashboards":        {manyDashboards(33), "at most"},
		"stray file":                 {[]core.TreeFile{tf("presets/p/preset.yaml", goodPreset), tf("presets/p/run.sh", "x")}, "run.sh"},
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

func manyDashboards(n int) []core.TreeFile {
	files := []core.TreeFile{tf("presets/p/preset.yaml", dashOnlyPreset)}
	for i := 0; i < n; i++ {
		files = append(files, tf(fmt.Sprintf("presets/p/dashboards/d%02d.yaml", i), goodDashboard))
	}
	return files
}

func TestServerPresetDashboardsLoad(t *testing.T) {
	p, probs := read(t, tf("presets/runbooks/preset.yaml", goodPreset), tf("presets/runbooks/templates/runbook.md", goodTemplate), tf("presets/runbooks/dashboards/planner.yaml", goodDashboard))
	if len(probs) != 0 {
		t.Fatal(probs)
	}
	pr := p.Contributions["presets"]
	if len(pr) != 1 || len(pr[0].Preset.Dashboards) != 1 || pr[0].Preset.Dashboards[0].Name != "planner" {
		t.Fatalf("presets = %+v", pr)
	}
	p, probs = read(t, tf("presets/only/preset.yaml", dashOnlyPreset), tf("presets/only/dashboards/planner.yaml", goodDashboard))
	if len(probs) != 0 {
		t.Fatal(probs)
	}
	d := p.Contributions["presets"][0].Preset
	if d.Kind != "" || d.Create != nil || len(d.Dashboards) != 1 {
		t.Fatalf("dashboard-only preset = %+v", d)
	}
}

// AC-plugdash-2: a dashboard with a bad widget stops the load and names the file
// and the widget. The count given to CheckPreset is of the dashboards that
// loaded and validated, so a preset whose only dashboard is invalid, or one
// that declares dashboards it does not carry, is refused.
func TestServerPresetDashboardRefused(t *testing.T) {
	bad := "widgets:\n  - id: a\n    type: stat\n  - id: a\n    type: stat\n"
	_, probs := read(t, tf("presets/p/preset.yaml", dashOnlyPreset), tf("presets/p/dashboards/x.yaml", bad))
	out := strings.Join(probs, "\n")
	for _, want := range []string{"presets/p/dashboards/x.yaml", `widgets[1] (id "a")`, "needs a map contract, dashboards or both"} {
		if !strings.Contains(out, want) {
			t.Errorf("problems lack %q: %q", want, probs)
		}
	}
	_, probs = read(t, tf("presets/p/preset.yaml", dashOnlyPreset+"dashboards: 3\n"))
	if len(probs) == 0 {
		t.Error("a preset declaring dashboards it does not carry loaded")
	}
}

// AC-plugdash-4: the preview lists the dashboards with title and widget counts.
func TestPreviewListsPresetDashboards(t *testing.T) {
	p, probs := read(t, tf("presets/only/preset.yaml", dashOnlyPreset), tf("presets/only/dashboards/planner.yaml", goodDashboard), tf("presets/only/dashboards/second.yaml", "title: Second\nwidgets: []\n"))
	if len(probs) != 0 {
		t.Fatal(probs)
	}
	var buf bytes.Buffer
	pl.Preview(&buf, p, pl.PreviewInfo{Side: core.SideServer}, core.Options{RunningVersion: "0.29.0"})
	for _, want := range []string{`preset "only": dashboards only`, `dashboard planner: "Planner", 2 widgets (1 note, 1 stat)`, `dashboard second: "Second", 0 widgets`} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("preview lacks %q:\n%s", want, buf.String())
		}
	}
}

// AC-plugdash-5: the presets family is at level 2.
func TestPresetsFamilyLevel2(t *testing.T) {
	if why := core.Supported(core.SideServer, "presets", 2); why != "" {
		t.Errorf("presets 2: %s", why)
	}
	if why := core.Supported(core.SideServer, "presets", 3); why != "needs sdk presets 3 (this build has 2)" {
		t.Errorf("presets 3: %q", why)
	}
}

// AC-plugdash-3: the approval is the pinned digest, so one changed byte of a
// preset's dashboard changes it.
func TestDigestCoversPresetDashboards(t *testing.T) {
	digest := func(dash string) string {
		p, probs := read(t, tf("presets/only/preset.yaml", dashOnlyPreset), tf("presets/only/dashboards/a.yaml", dash))
		if len(probs) != 0 {
			t.Fatal(probs)
		}
		return p.Digest
	}
	a, b := digest("widgets: []\n"), digest("widgets: [] \n")
	if a == "" || a == b {
		t.Errorf("digest ignores a changed dashboard byte: %q %q", a, b)
	}
}
