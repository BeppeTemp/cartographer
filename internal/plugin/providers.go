package plugin

import (
	"fmt"
	"io"
	"os"
	"path"
	"runtime"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/plugin/core"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
)

// DefaultProviderEnv is the machine ResolveProvider reads at run time.
func DefaultProviderEnv() core.ProviderEnv {
	home, _ := os.UserHomeDir()
	return core.ProviderEnv{GOOS: runtime.GOOS, Home: home, Getenv: os.Getenv}
}

// providerEnv is the environment the point's loader and preview resolve against;
// a test replaces it.
var providerEnv = DefaultProviderEnv

// loadProviders reads providers/<id>.yaml: each file is parsed (stem = name),
// resolved for this machine and dry-run against the registries, so `plugin
// validate` catches a provider that collides with a built-in or is refused by
// the registry offline. A path left out by the resolver is not a problem (it is
// shown by the preview); a provider the registries would refuse is.
func loadProviders(files []core.TreeFile) ([]core.Contribution, []string) {
	var problems []string
	var out []core.Contribution
	var specs []core.ProviderSpec
	env := providerEnv()
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	for _, f := range files {
		rest := strings.TrimPrefix(f.Path, "providers/")
		stem, ok := strings.CutSuffix(rest, ".yaml")
		if !ok || strings.Contains(rest, "/") {
			problems = append(problems, fmt.Sprintf("%s: expected a file providers/<id>.yaml", f.Path))
			continue
		}
		spec, probs := core.ParseProvider(stem, f.Content)
		for _, pr := range probs {
			problems = append(problems, f.Path+": "+pr)
		}
		if len(probs) > 0 {
			continue
		}
		if env.Home != "" {
			r, _ := core.ResolveProvider(spec, env)
			if err := configurator.CheckPlugin(r.Registration("check", true)); err != nil {
				problems = append(problems, f.Path+": "+err.Error())
				continue
			}
			if err := provisioning.CheckPluginCells(ProviderCells(r, true), env.Home); err != nil {
				problems = append(problems, f.Path+": "+err.Error())
				continue
			}
		}
		specs = append(specs, spec)
		s := spec
		out = append(out, core.Contribution{Point: "providers", Kind: "provider", Name: spec.Name, Files: []contract.ArtifactFile{{Path: path.Base(f.Path), Content: f.Content}}, Provider: &s})
	}
	problems = append(problems, core.ProviderNameClashes(specs)...)
	return out, problems
}

// previewProvider prints what a provider would do on this machine: the places it
// is detected, the files it writes, its MCP mode and hook root, and the
// capabilities it claims. Every string passes through esc.
func previewProvider(w io.Writer, c core.Contribution) {
	s := c.Provider
	if s == nil {
		return
	}
	env := providerEnv()
	r, reasons := core.ResolveProvider(*s, env)
	fmt.Fprintf(w, "provider %s (%s), a fork of %s\n", esc(s.Name), esc(s.DisplayName), esc(s.Base))
	home := env.Home
	abs := func(segs []string) string {
		return esc(path.Join(append([]string{strings.ReplaceAll(home, `\`, "/")}, segs...)...))
	}
	if len(r.Binaries) > 0 {
		fmt.Fprintf(w, "  detected by binaries: %s\n", escAll(r.Binaries))
	}
	for _, d := range r.DetectDirs {
		fmt.Fprintf(w, "  detected by directory: %s\n", abs(d))
	}
	for _, line := range r.Paths(home) {
		fmt.Fprintf(w, "  writes %s\n", esc(line))
	}
	mode := r.MCPMode
	if mode == "" {
		mode = "native"
	}
	fmt.Fprintf(w, "  mcp: %s\n", esc(mode))
	if len(r.MCPConfig) > 0 {
		fmt.Fprintf(w, "  mcp file: %s\n", abs(r.MCPConfig))
	}
	for _, st := range r.MCPStale {
		fmt.Fprintf(w, "  removes after a change (the client regenerates it): %s\n", abs(st))
	}
	if h := r.Global.Hook; h != nil {
		fmt.Fprintf(w, "  hook root: %s\n", abs(h.Dir))
	}
	ids := make([]string, 0, len(r.Capabilities))
	for id := range r.Capabilities {
		ids = append(ids, id+"="+r.Capabilities[id])
	}
	sort.Strings(ids)
	if len(ids) > 0 {
		fmt.Fprintf(w, "  capabilities claimed: %s\n", escAll(ids))
	}
	for _, why := range reasons {
		fmt.Fprintf(w, "  left out: %s\n", esc(why))
	}
}
