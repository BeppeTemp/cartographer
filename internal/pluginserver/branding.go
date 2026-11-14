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

// loadBranding judges the branding/ folder as one contribution, so plugin validate,
// vendor and the install preview report a bad one before anything is pinned.
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
	file := func(label string, f *branding.File) {
		if f != nil {
			parts = append(parts, fmt.Sprintf("%s %s %.1f KiB", label, f.Name, float64(len(f.Data))/1024))
		}
	}
	file("logo light", s.LogoLight)
	if s.LogoDark != nil {
		file("dark", s.LogoDark)
	}
	if s.Company != "" {
		parts = append(parts, fmt.Sprintf("company %q", s.Company))
	}
	fmt.Fprintf(w, "branding: %s\n", strings.Join(parts, ", "))
	fmt.Fprintln(w, `  data only: nothing runs; the Atlas header still reads "Cartographer"`)
}

// ActiveBranding is the branding of the active plugins: none, or the one
// plugin that contributes it. Two plugins providing one is an error, so no
// order of the pins decides which logo the Atlas wears.
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
		return branding.NewActive(names[0], spec), nil
	}
	sort.Strings(names)
	return nil, fmt.Errorf("two plugins provide a branding: %s; keep one or disable the other with enabled: false", strings.Join(names, " and "))
}
