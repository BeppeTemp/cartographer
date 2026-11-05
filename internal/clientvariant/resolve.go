package clientvariant

import (
	"fmt"
	"path"
	"regexp"
	"strings"
)

// shape says where a glob `*` may stand in a path.
type shape int

const (
	shPlain  shape = iota // no `*`
	shDetect              // one `*` segment at most, the last
	shSeed                // one `*` segment at most, never the last
)

var anchorEnv = map[string]string{"home": "", "xdg_config": "XDG_CONFIG_HOME", "local_app_data": "LOCALAPPDATA", "app_data": "APPDATA"}

var reservedName = regexp.MustCompile(`(?i)^(con|prn|aux|nul|conin\$|conout\$|(com|lpt)[1-9¹²³]) *(\..*)?$`)

// tpl is a parsed path: an anchor (empty for a project path) and its segments.
type tpl struct {
	anchor string
	segs   []string
}

// parseTpl checks a path template: "{anchor}/seg" (home scope) or "seg" (project).
func parseTpl(s string, project bool, sh shape) (tpl, error) {
	t := tpl{segs: strings.Split(s, "/")}
	if project {
		if strings.HasPrefix(s, "{") {
			return t, fmt.Errorf("%s: a project path is relative and takes no anchor", q(s))
		}
	} else {
		for a := range anchorEnv {
			if p := "{" + a + "}/"; strings.HasPrefix(s, p) {
				t = tpl{anchor: a, segs: strings.Split(s[len(p):], "/")}
				break
			}
		}
		if t.anchor == "" {
			return t, fmt.Errorf("%s must start with {home}/, {xdg_config}/, {local_app_data}/ or {app_data}/", q(s))
		}
	}
	stars := 0
	for i, seg := range t.segs {
		switch {
		case seg == "" || seg == "." || seg == ".." || strings.HasSuffix(seg, ".") || strings.HasSuffix(seg, " ") || reservedName.MatchString(seg):
			return t, fmt.Errorf("%s: segment %s is empty, dot-only, ends in a dot or space, or is a Windows device name", q(s), q(seg))
		case strings.HasPrefix(seg, "~") || strings.ContainsAny(seg, `\:?[]`) || hasControl(seg):
			return t, fmt.Errorf("%s: segment %s starts with ~ or holds a character that is not allowed (\\ : ? [ ] or a control character)", q(s), q(seg))
		case strings.Contains(seg, "*"):
			stars++
			last := i == len(t.segs)-1
			if sh == shPlain || stars > 1 || (sh == shDetect && !last) || (sh == shSeed && last) {
				return t, fmt.Errorf("%s: the * is not allowed in segment %s here", q(s), q(seg))
			}
		}
	}
	return t, nil
}

func checkPath(key, v string, project bool, sh shape) error {
	if _, err := parseTpl(v, project, sh); err != nil {
		return fmt.Errorf("%s: %w", key, err)
	}
	return nil
}

// Env is what Resolve reads from the machine; tests inject it.
type Env struct {
	GOOS, Home string
	Getenv     func(string) string
}

// Cell is one destination of the variant.
type Cell struct {
	Dir, File   []string // skill and agent directory; instructions file
	AgentFormat string   // agent only; the file suffix follows from it
}

// Scope is the cells of one scope; paths are slash segments, home- or project-relative.
type Scope struct{ Skill, Agent, Instructions *Cell }

// Variant is a descriptor resolved for one OS. Data only.
type Variant struct {
	Name, DisplayName, Base string
	Binaries                []string
	DetectDirs              [][]string // home-relative; a last segment may hold *
	Global, Project         Scope
	MCPFile, HookDir        []string // home-relative; nil when absent
	MCPMode                 string
	SeedTemplate            []string // home-relative; one middle segment holds *
	SeedDerived             []string
	Capabilities            map[string]string
	InvokeHint              string
}

// Resolve picks the per-OS values of spec, expands the anchors against env and
// returns the variant with the reasons paths were left out. A path outside
// env.Home is dropped with a reason, never an error, so the rest still resolves.
func Resolve(spec Spec, env Env) (Variant, []string) {
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
	anchor := func(a string) ([]string, error) {
		if env.Home == "" {
			return nil, fmt.Errorf("the home directory is unknown")
		}
		base := ""
		if env.Getenv != nil {
			base = env.Getenv(anchorEnv[a])
		}
		switch {
		case a == "home":
			return nil, nil
		case a == "xdg_config" && base == "":
			return []string{".config"}, nil
		case a != "xdg_config" && env.GOOS != "windows":
			return nil, fmt.Errorf("{%s} exists on windows only", a)
		case base == "":
			return nil, fmt.Errorf("{%s} is not set", a)
		}
		segs, ok := under(slash(base))
		if !ok {
			return nil, fmt.Errorf("{%s} resolves outside the home directory", a)
		}
		return segs, nil
	}
	one := func(key, v string, project bool, sh shape) []string {
		t, err := parseTpl(v, project, sh)
		var pre []string
		if err == nil && !project {
			pre, err = anchor(t.anchor)
		}
		if err != nil {
			reasons = append(reasons, key+": "+err.Error())
			return nil
		}
		return append(pre, t.segs...)
	}
	res := map[string][]string{}
	spec.walk(func(key string, p perOS[string], project bool, sh shape) {
		if v, ok := p.pick(env.GOOS); ok {
			res[key] = one(key, v, project, sh)
		}
	})
	v := Variant{
		Name: spec.Name, DisplayName: spec.DisplayName, Base: spec.Base,
		Binaries: spec.Detect.Binaries, MCPMode: spec.MCP.Mode,
		Capabilities: spec.Capabilities, InvokeHint: spec.InvokeHint,
		MCPFile: res["paths.global.mcp.file"], HookDir: res["paths.global.hook.dir"],
		SeedTemplate: res["mcp.seed.template_glob"], SeedDerived: res["mcp.seed.derived_glob"],
	}
	if l, ok := spec.Detect.Dirs.pick(env.GOOS); ok {
		for i, d := range l {
			if segs := one(fmt.Sprintf("detect.dirs[%d]", i), d, false, shDetect); segs != nil {
				v.DetectDirs = append(v.DetectDirs, segs)
			}
		}
	}
	scope := func(name string, c *cellsSpec) (sc Scope) {
		k := "paths." + name + "."
		if d := res[k+"skill.dir"]; d != nil {
			sc.Skill = &Cell{Dir: d}
		}
		if d := res[k+"agent.dir"]; d != nil {
			sc.Agent = &Cell{Dir: d, AgentFormat: c.Agent.Format}
		}
		if f := res[k+"instructions.file"]; f != nil {
			sc.Instructions = &Cell{File: f}
		}
		return sc
	}
	v.Global, v.Project = scope("global", &spec.Paths.Global), scope("project", &spec.Paths.Project)
	return v, reasons
}
