package pluginserver

import (
	"fmt"
	"io"
	"path"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/branding"
	"github.com/BeppeTemp/cartographer/internal/plugin/core"
	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
)

func init() {
	core.Register(core.Point{Dir: "branding", Side: core.SideAtlas, Load: loadBranding, Preview: previewBranding})
}

// loadBranding judges the branding/ folder as one contribution. The accent is
// derived here too, so plugin validate, vendor and the install preview report
// an unusable one before anything is pinned.
func loadBranding(files []core.TreeFile) ([]core.Contribution, []string) {
	if len(files) == 0 {
		return nil, nil
	}
	in := make(map[string]branding.InFile, len(files))
	var probs []string
	var cfiles []contract.ArtifactFile
	for _, f := range files {
		name := strings.TrimPrefix(f.Path, "branding/")
		if strings.Contains(name, "/") {
			probs = append(probs, f.Path+": the folder is flat: no sub-folder")
			continue
		}
		in[name] = branding.InFile{Content: f.Content, Executable: f.Executable}
		cfiles = append(cfiles, contract.ArtifactFile{Path: path.Base(f.Path), Content: f.Content})
	}
	spec, sp := branding.Parse(in)
	probs = append(probs, sp...)
	if spec == nil {
		sort.Strings(probs)
		return nil, probs
	}
	if spec.Accent != "" {
		if _, err := branding.Derive(spec.Accent); err != nil {
			probs = append(probs, "branding/"+branding.DeclFile+": "+err.Error())
		}
	}
	if len(probs) > 0 {
		sort.Strings(probs)
		return nil, probs
	}
	return []core.Contribution{{Point: "branding", Kind: "branding", Name: "branding", Files: cfiles, Branding: spec}}, nil
}

// previewBranding prints plain facts about what the Atlas would wear.
func previewBranding(w io.Writer, c core.Contribution) {
	s := c.Branding
	var parts []string
	if s.Accent != "" {
		p, _ := branding.Derive(s.Accent)
		parts = append(parts, fmt.Sprintf("accent %s (dark: %s, light: %s)", s.Accent, themeNote(p.Dark), themeNote(p.Light)))
	}
	file := func(label string, f *branding.File) {
		if f != nil {
			parts = append(parts, fmt.Sprintf("%s %s %.1f KiB", label, f.Name, float64(len(f.Data))/1024))
		}
	}
	file("logo light", s.LogoLight)
	if s.LogoDark != nil {
		file("dark", s.LogoDark)
	}
	file("favicon", s.Favicon)
	if s.Company != "" {
		parts = append(parts, fmt.Sprintf("company %q", s.Company))
	}
	fmt.Fprintf(w, "branding: %s\n", strings.Join(parts, ", "))
	if s.Accent != "" {
		p, _ := branding.Derive(s.Accent)
		fmt.Fprintf(w, "  contrast: dark accent on panels %.1f:1, text on accent %.1f:1; light accent on panels %.1f:1, text on accent %.1f:1 (needs 4.5)\n",
			p.Dark.PanelRatio, p.Dark.TextRatio, p.Light.PanelRatio, p.Light.TextRatio)
	}
	fmt.Fprintln(w, `  data only: nothing runs; the Atlas header still reads "Cartographer"`)
}

func themeNote(t branding.Theme) string {
	if !t.Adjusted {
		return "as given"
	}
	return fmt.Sprintf("moved %d points to %s", t.Shift, t.Accent)
}

// ActiveBranding is the branding of the active plugins: none, or the one
// plugin that contributes it. Two plugins providing one is an error, so no
// order of the pins decides which logo the Atlas wears. The accent is derived
// again here and an error names the plugin and the key.
func ActiveBranding(plugins []*core.Plugin) (*branding.Active, error) {
	var names []string
	var spec *branding.Spec
	for _, p := range plugins {
		for _, c := range p.Contributions["branding"] {
			names = append(names, p.Manifest.Name)
			spec = c.Branding
		}
	}
	switch len(names) {
	case 0:
		return nil, nil
	case 1:
		return branding.NewActive(names[0], spec)
	}
	sort.Strings(names)
	return nil, fmt.Errorf("two plugins provide a branding: %s; keep one or disable the other with enabled: false", strings.Join(names, " and "))
}
