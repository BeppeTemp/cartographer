package plugin

import (
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/safewrite"
)

// anchorEnv names the environment variable an anchor reads; home and documents
// read none.
var anchorEnv = map[string]string{"xdg_config": "XDG_CONFIG_HOME", "local_app_data": "LOCALAPPDATA", "app_data": "APPDATA"}

// ProviderEnv is what ResolveProvider reads from the machine; tests inject it.
type ProviderEnv struct {
	GOOS, Home string
	Getenv     func(string) string
	// Documents answers the Documents folder of home; nil is
	// safewrite.DocumentsDir, the resolver Cline's hooks and rules use.
	Documents func(home string) string
}

// ProviderCell is one destination of a provider: a path relative to the home
// directory (global) or to a project root (project). No write cell lies under
// {documents}: only a detect dir takes that anchor.
type ProviderCell struct {
	Dir, File   []string // skill, agent and hook directory; instructions file
	AgentFormat string   // agent only; the file suffix follows from it
}

// ProviderScope is the cells of one scope. Hook is global only.
type ProviderScope struct{ Skill, Agent, Instructions, Hook *ProviderCell }

// ResolvedProvider is a providers/<id>.yaml resolved for one OS. Data only.
type ResolvedProvider struct {
	Name, DisplayName, Base string
	Binaries                []string
	DetectDirs              [][]string // home-relative; one segment may hold *
	Global, Project         ProviderScope
	MCPMode                 string
	MCPConfig               []string   // home-relative; nil keeps the base's file; one segment may hold *
	MCPStale                [][]string // home-relative
	Capabilities            map[string]string
	InvokeHint              string
}

// ResolveProvider picks the per-OS values of spec, expands the anchors against
// env and returns the provider with the reasons paths were left out. A path
// outside its root, or one the walk from the root finds behind a link, is
// dropped with a reason, never an error, so the rest still resolves. Home-family
// anchors must stay inside the home directory (safewrite.CheckPath(home, path),
// the check the hook writers run); {documents} is read by detect dirs only, and
// counts when the Documents folder lies below home.
func ResolveProvider(spec ProviderSpec, env ProviderEnv) (ResolvedProvider, []string) {
	var reasons []string
	slash := func(s string) string {
		if env.GOOS == "windows" {
			s = strings.ReplaceAll(s, `\`, "/")
		}
		return strings.TrimSuffix(path.Clean(s), "/")
	}
	home := slash(env.Home)
	eq := func(a, b string) bool { return a == b || env.GOOS != "linux" && strings.EqualFold(a, b) }
	under := func(base string) ([]string, bool) {
		if eq(base, home) {
			return nil, true
		}
		if len(base) > len(home) && base[len(home)] == '/' && eq(base[:len(home)], home) {
			return strings.Split(base[len(home)+1:], "/"), true
		}
		return nil, false
	}
	docs := func() string {
		if env.Documents != nil {
			return slash(env.Documents(env.Home))
		}
		return slash(safewrite.DocumentsDir(env.Home))
	}
	// anchor returns the root ("" for home) and the segments below home it adds.
	anchor := func(a string) (root string, pre []string, err error) {
		if env.Home == "" {
			return "", nil, fmt.Errorf("the home directory is unknown")
		}
		base := ""
		if env.Getenv != nil {
			base = env.Getenv(anchorEnv[a])
		}
		switch {
		case a == "home":
			return "", nil, nil
		case a == "documents":
			d := docs()
			if d == "" || d == "." {
				return "", nil, fmt.Errorf("the Documents folder is unknown")
			}
			segs, _ := under(d)
			return d, segs, nil
		case a == "xdg_config" && base == "":
			return "", []string{".config"}, nil
		case a != "xdg_config" && env.GOOS != "windows":
			return "", nil, fmt.Errorf("{%s} exists on windows only", a)
		case base == "":
			return "", nil, fmt.Errorf("{%s} is not set", a)
		}
		segs, ok := under(slash(base))
		if !ok {
			return "", nil, fmt.Errorf("{%s} resolves outside the home directory", a)
		}
		return "", segs, nil
	}
	// contained reports whether segs below home walks through no link; a path
	// holding a `*` is checked up to the segment before the first one, the part
	// that is not a pattern.
	contained := func(key string, segs []string) bool {
		for i, seg := range segs {
			if strings.Contains(seg, "*") {
				segs = segs[:i]
				break
			}
		}
		if err := safewrite.CheckPath(env.Home, filepath.Join(append([]string{env.Home}, segs...)...)); err != nil {
			reasons = append(reasons, key+": "+err.Error())
			return false
		}
		return true
	}
	// cell resolves one path of a cell: the segments below home (global) or the
	// project root.
	cell := func(key, v string, project bool, sh pathShape) ([]string, bool) {
		t, err := parsePathTpl(v, project, sh, false)
		var pre []string
		if err == nil && !project {
			_, pre, err = anchor(t.anchor)
		}
		if err != nil {
			reasons = append(reasons, key+": "+err.Error())
			return nil, false
		}
		segs := append(append([]string(nil), pre...), t.segs...)
		if !project && !contained(key, segs) {
			return nil, false
		}
		return segs, true
	}
	// homePath resolves a path that must be home-relative (detect, MCP files): a
	// {documents} path counts when the Documents folder lies below home.
	homePath := func(key, v string, sh pathShape, check bool) []string {
		t, err := parsePathTpl(v, false, sh, true)
		var root string
		var pre []string
		if err == nil {
			root, pre, err = anchor(t.anchor)
		}
		if err == nil && root != "" {
			segs, ok := under(root)
			if !ok {
				err = fmt.Errorf("{documents} resolves outside the home directory")
			}
			pre = segs
		}
		if err != nil {
			reasons = append(reasons, key+": "+err.Error())
			return nil
		}
		segs := append(append([]string(nil), pre...), t.segs...)
		if check && !contained(key, segs) {
			return nil
		}
		return segs
	}

	r := ResolvedProvider{
		Name: spec.Name, DisplayName: spec.DisplayName, Base: spec.Base,
		Binaries: spec.Detect.Binaries, MCPMode: spec.MCP.Mode,
		Capabilities: spec.Capabilities, InvokeHint: spec.InvokeHint,
	}
	if l, ok := spec.Detect.Dirs.pick(env.GOOS); ok {
		for i, d := range l {
			// Detection only reads: a link is not checked for here.
			if segs := homePath(fmt.Sprintf("detect.dirs[%d]", i), d, shDir, false); segs != nil {
				r.DetectDirs = append(r.DetectDirs, segs)
			}
		}
	}
	if v, ok := spec.MCP.Config.pick(env.GOOS); ok && spec.MCP.Mode == "native" {
		r.MCPConfig = homePath("mcp.config", v, shFile, true)
	}
	if l, ok := spec.MCP.Stale.pick(env.GOOS); ok && spec.MCP.Mode == "native" {
		for i, p := range l {
			if segs := homePath(fmt.Sprintf("mcp.stale[%d]", i), p, shFile, true); segs != nil {
				r.MCPStale = append(r.MCPStale, segs)
			}
		}
	}
	dirOf := func(key string, p osValue[string], project bool) *ProviderCell {
		v, ok := p.pick(env.GOOS)
		if !ok {
			return nil
		}
		segs, ok := cell(key, v, project, shPlain)
		if !ok {
			return nil
		}
		return &ProviderCell{Dir: segs}
	}
	scope := func(name string, skill *dirCell, agent *agentCell, instr *fileCell, project bool) (sc ProviderScope) {
		k := "paths." + name + "."
		if skill != nil {
			sc.Skill = dirOf(k+"skill.dir", skill.Dir, project)
		}
		if agent != nil {
			if c := dirOf(k+"agent.dir", agent.Dir, project); c != nil {
				c.AgentFormat = agent.Format
				sc.Agent = c
			}
		}
		if instr != nil {
			if v, ok := instr.File.pick(env.GOOS); ok {
				if segs, ok := cell(k+"instructions.file", v, project, shPlain); ok {
					sc.Instructions = &ProviderCell{File: segs}
				}
			}
		}
		return sc
	}
	g, p := spec.Paths.Global, spec.Paths.Project
	r.Global = scope("global", g.Skill, g.Agent, g.Instructions, false)
	r.Project = scope("project", p.Skill, p.Agent, p.Instructions, true)
	if g.Hook != nil {
		r.Global.Hook = dirOf("paths.global.hook.dir", g.Hook.Dir, false)
	}
	return r, reasons
}

// Paths lists every path the provider resolved to on this machine, one
// "scope kind: path" line per cell, sorted: the global ones absolute, the project
// ones relative to a project root. It is what the install preview prints: the
// approval is of the tree, and these are the places it would write.
func (r ResolvedProvider) Paths(home string) []string {
	var out []string
	full := func(root string, segs []string) string {
		if root == "" {
			root = filepath.ToSlash(home)
		}
		return path.Join(append([]string{root}, segs...)...)
	}
	cell := func(scope, kind string, c *ProviderCell, project bool) {
		if c == nil {
			return
		}
		segs := c.Dir
		if segs == nil {
			segs = c.File
		}
		p := ""
		if project {
			p = "<project>/" + path.Join(segs...)
		} else {
			p = full("", segs)
		}
		if kind == "agent" {
			p += " (format " + c.AgentFormat + ")"
		}
		out = append(out, scope+" "+kind+": "+p)
	}
	for _, sc := range []struct {
		name    string
		s       ProviderScope
		project bool
	}{{"global", r.Global, false}, {"project", r.Project, true}} {
		cell(sc.name, "skill", sc.s.Skill, sc.project)
		cell(sc.name, "agent", sc.s.Agent, sc.project)
		cell(sc.name, "instructions", sc.s.Instructions, sc.project)
		cell(sc.name, "hook", sc.s.Hook, sc.project)
	}
	if r.MCPConfig != nil {
		out = append(out, "global mcp config: "+full("", r.MCPConfig))
	}
	for _, s := range r.MCPStale {
		out = append(out, "global mcp stale: "+full("", s))
	}
	sort.Strings(out)
	return out
}

// Registration is the registry entry of the provider, declared by plugin origin.
// An active provider is detected; an inactive one stays resolvable so what it
// installed can be retired.
func (r ResolvedProvider) Registration(origin string, active bool) configurator.PluginSpec {
	return configurator.PluginSpec{
		Name: r.Name, DisplayName: r.DisplayName, Base: r.Base, Origin: origin, Active: active,
		Binaries: r.Binaries, DetectDirs: r.DetectDirs,
		MCPMode: r.MCPMode, MCPConfig: r.MCPConfig, MCPStale: r.MCPStale,
	}
}
