package pluginserver

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/mcpserver"
	"github.com/BeppeTemp/cartographer/internal/plugin/core"
	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
)

type presetFile struct {
	Description string                 `yaml:"description"`
	Contract    mcpserver.PresetCreate `yaml:",inline"`
}

// loadPresets reads presets/<name>/preset.yaml and presets/<name>/templates/<slug>.md.
// The contract is judged as map_create judges it and each template as artifact_write
// does, so a preset that loads can be applied by the tools that apply it.
func loadPresets(files []core.TreeFile) ([]core.Contribution, []string) {
	groups := map[string][]core.TreeFile{}
	var problems []string
	for _, f := range files {
		name, _, _ := strings.Cut(strings.TrimPrefix(f.Path, "presets/"), "/")
		groups[name] = append(groups[name], f)
	}
	names := make([]string, 0, len(groups))
	for n := range groups {
		names = append(names, n)
	}
	sort.Strings(names)
	var out []core.Contribution
	for _, name := range names {
		c, probs := loadPreset(name, groups[name])
		problems = append(problems, probs...)
		if len(probs) == 0 {
			out = append(out, c)
		}
	}
	return out, problems
}

func loadPreset(name string, files []core.TreeFile) (core.Contribution, []string) {
	var problems []string
	bad := func(f core.TreeFile, format string, a ...any) {
		problems = append(problems, f.Path+": "+fmt.Sprintf(format, a...))
	}
	var spec []byte
	var tpls []core.PresetTemplate
	var slugs []string
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	for _, f := range files {
		rest := strings.TrimPrefix(f.Path, "presets/"+name+"/")
		slug, isTpl := strings.CutSuffix(strings.TrimPrefix(rest, "templates/"), ".md")
		switch {
		case !contract.ValidArtifactName(name):
			bad(f, "not a valid preset name")
		case f.Executable:
			bad(f, "a server family holds data, not an executable file")
		case rest == "preset.yaml":
			spec = f.Content
		case strings.HasPrefix(rest, "templates/") && isTpl && !strings.Contains(slug, "/") && kb.ValidTemplateSlug(slug):
			if err := mcpserver.ValidateTemplate(f.Content); err != nil {
				bad(f, "%v", err)
				continue
			}
			tpls = append(tpls, core.PresetTemplate{Slug: slug, Content: f.Content})
			slugs = append(slugs, slug)
		default:
			bad(f, "expected presets/%s/preset.yaml or presets/%s/templates/<slug>.md", name, name)
		}
	}
	where := "presets/" + name + "/preset.yaml"
	if spec == nil {
		return core.Contribution{}, append(problems, where+": missing")
	}
	var pf presetFile
	if err := decode(spec, &pf); err != nil {
		return core.Contribution{}, append(problems, where+": "+err.Error())
	}
	if msg := oneLine(pf.Description); msg != "" {
		problems = append(problems, where+": "+msg)
	}
	if err := mcpserver.CheckPresetCreate(pf.Contract, slugs); err != nil {
		problems = append(problems, where+": "+err.Error())
	}
	if len(problems) > 0 {
		return core.Contribution{}, problems
	}
	create, _ := json.Marshal(pf.Contract)
	p := core.Preset{Description: pf.Description, Kind: pf.Contract.Kind, Create: create, Templates: tpls}
	var cf []contract.ArtifactFile
	for _, f := range files {
		cf = append(cf, contract.ArtifactFile{Path: strings.TrimPrefix(f.Path, "presets/"+name+"/"), Content: f.Content})
	}
	return core.Contribution{Point: "presets", Kind: "preset", Name: name, Files: cf, Description: pf.Description, Preset: &p}, nil
}

// previewPreset prints a preset's kind, templates and the keys of its contract.
func previewPreset(w io.Writer, c core.Contribution) {
	p := c.Preset
	var keys map[string]json.RawMessage
	_ = json.Unmarshal(p.Create, &keys)
	var ks, ts []string
	for k := range keys {
		ks = append(ks, k)
	}
	for _, t := range p.Templates {
		ts = append(ts, t.Slug)
	}
	sort.Strings(ks)
	fmt.Fprintf(w, "preset %q: a %s, contract keys %s, templates %s\n", c.Name, p.Kind, strings.Join(ks, ", "), strings.Join(ts, ", "))
}
