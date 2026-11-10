package plugin

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"
	"unicode"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
	"gopkg.in/yaml.v3"
)

// ProvidersDir is the contribution point holding providers/<id>.yaml: an agent
// client described as data, a fork or in-house build of a built-in client.
const ProvidersDir = "providers"

const (
	maxProviderDisplay = 40
	maxInvokeHint      = 200
	maxCapabilities    = 64
	maxStale           = 3
)

var (
	providerNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{1,30}$`)
	// kiro and codex write JSON/TOML files and are not offered as agent formats.
	agentFormats   = []string{"claude", "opencode", "antigravity", "copilot", "cline"}
	capabilityVals = []string{"verified", "unverified", "absent"}
	osKeys         = []string{"darwin", "linux", "windows", "default"}
	// reservedDevice is a Windows device name, with or without an extension.
	reservedDevice = regexp.MustCompile(`(?i)^(con|prn|aux|nul|conin\$|conout\$|(com|lpt)[1-9¹²³]) *(\..*)?$`)
)

// osValue is a value given once for every OS or as a map keyed by OS (osKeys).
type osValue[T any] struct{ byOS map[string]T }

func (p *osValue[T]) UnmarshalYAML(n *yaml.Node) error {
	p.byOS = map[string]T{}
	if n.Kind != yaml.MappingNode {
		var v T
		if err := n.Decode(&v); err != nil {
			return err
		}
		p.byOS["default"] = v
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k := n.Content[i].Value
		if !slices.Contains(osKeys, k) {
			return fmt.Errorf("line %d: unknown OS key %s (want darwin, linux, windows or default)", n.Content[i].Line, q(k))
		}
		if _, dup := p.byOS[k]; dup {
			return fmt.Errorf("line %d: OS key %s is given twice", n.Content[i].Line, q(k))
		}
		var v T
		if err := n.Content[i+1].Decode(&v); err != nil {
			return err
		}
		p.byOS[k] = v
	}
	return nil
}

// pick is the value for goos, else the default one.
func (p osValue[T]) pick(goos string) (T, bool) {
	if v, ok := p.byOS[goos]; ok {
		return v, true
	}
	v, ok := p.byOS["default"]
	return v, ok
}

// each visits the values in osKeys order, so messages are stable.
func (p osValue[T]) each(f func(osKey string, v T)) {
	for _, k := range osKeys {
		if v, ok := p.byOS[k]; ok {
			f(k, v)
		}
	}
}

func (p osValue[T]) isSet() bool { return len(p.byOS) > 0 }

// ProviderSpec is a providers/<id>.yaml as written. It is built only by ParseProvider.
type ProviderSpec struct {
	Schema      int    `yaml:"schema"`
	Name        string `yaml:"name"`
	DisplayName string `yaml:"display_name"`
	Base        string `yaml:"base"`
	Detect      struct {
		Binaries []string          `yaml:"binaries"`
		Dirs     osValue[[]string] `yaml:"dirs"`
	} `yaml:"detect"`
	Paths struct {
		Global  globalCells  `yaml:"global"`
		Project projectCells `yaml:"project"`
	} `yaml:"paths"`
	Capabilities map[string]string `yaml:"capabilities"`
	MCP          struct {
		Mode   string            `yaml:"mode"`
		Config osValue[string]   `yaml:"config"`
		Stale  osValue[[]string] `yaml:"stale"`
	} `yaml:"mcp"`
	InvokeHint string `yaml:"invoke_hint"`
}

type (
	dirCell struct {
		Dir osValue[string] `yaml:"dir"`
	}
	fileCell struct {
		File osValue[string] `yaml:"file"`
	}
	agentCell struct {
		Dir    osValue[string] `yaml:"dir"`
		Format string          `yaml:"format"`
	}
	hookCell struct {
		Dir  osValue[string] `yaml:"dir"`
		Scan string          `yaml:"scan"`
	}
)

// projectCells are the cells of the project scope: no hook, and no MCP file at
// all (the MCP file is the `mcp` block's, global).
type projectCells struct {
	Skill        *dirCell   `yaml:"skill"`
	Agent        *agentCell `yaml:"agent"`
	Instructions *fileCell  `yaml:"instructions"`
}

// globalCells add the hook directory.
type globalCells struct {
	Skill        *dirCell   `yaml:"skill"`
	Agent        *agentCell `yaml:"agent"`
	Instructions *fileCell  `yaml:"instructions"`
	Hook         *hookCell  `yaml:"hook"`
}

// pathShape says where a `*` may stand in a path.
type pathShape int

const (
	shPlain pathShape = iota // no `*`
	shDir                    // one `*` segment, not the first (a version folder)
	shFile                   // as shDir and not the last: a file name stays literal
)

// providerAnchors are the placeholders a global path starts with.
var providerAnchors = []string{"home", "xdg_config", "app_data", "local_app_data", "documents"}

// pathTpl is a parsed path: an anchor (empty for a project path) and its segments.
type pathTpl struct {
	anchor string
	segs   []string
}

// parsePathTpl checks a path template: "{anchor}/seg/..." (global) or "seg/..."
// (project). allowDocs says whether {documents} may start it: only a detect dir
// takes it, since nothing but Cline's own dispatcher may be written there
// (internal/provisioning/AGENTS.md). Every segment is also kept off Cartographer's
// own state, as a manifest glob is (reachesOwnState).
// shortName matches a Windows 8.3 short-name marker (`SSH~1`): on a volume that
// keeps short names it reaches .ssh or .claude under another spelling, so a
// descriptor refuses it on every OS (it is portable).
var shortName = regexp.MustCompile(`~[0-9]`)

func parsePathTpl(s string, project bool, sh pathShape, allowDocs bool) (pathTpl, error) {
	t := pathTpl{segs: strings.Split(s, "/")}
	if project {
		if strings.HasPrefix(s, "{") {
			return t, fmt.Errorf("%s: a project path is relative and takes no anchor", q(s))
		}
	} else {
		for _, a := range providerAnchors {
			if p := "{" + a + "}/"; strings.HasPrefix(s, p) {
				if a == "documents" && !allowDocs {
					return t, fmt.Errorf("%s: {documents} is not allowed here (a path of this key stays under the home directory)", q(s))
				}
				t = pathTpl{anchor: a, segs: strings.Split(s[len(p):], "/")}
				break
			}
		}
		if t.anchor == "" {
			return t, fmt.Errorf("%s must start with {home}/, {xdg_config}/, {app_data}/, {local_app_data}/ or {documents}/", q(s))
		}
	}
	stars := 0
	for i, seg := range t.segs {
		switch {
		case seg == "" || seg == "." || seg == ".." || strings.HasSuffix(seg, ".") || strings.HasSuffix(seg, " ") || reservedDevice.MatchString(seg):
			return t, fmt.Errorf("%s: segment %s is empty, dot-only, ends in a dot or space, or is a Windows device name", q(s), q(seg))
		case strings.HasPrefix(seg, "~") || shortName.MatchString(seg) || strings.ContainsAny(seg, `\:?[]`) || strings.IndexFunc(seg, unicode.IsControl) >= 0:
			return t, fmt.Errorf("%s: segment %s starts with ~, holds ~ and a digit (a Windows 8.3 short name such as SSH~1) or holds a character that is not allowed (\\ : ? [ ] or a control character)", q(s), q(seg))
		case strings.Contains(seg, "*"):
			stars++
			// A plain `*` or `prefix*`, in one segment, never the first.
			form := strings.Count(seg, "*") == 1 && strings.HasSuffix(seg, "*")
			last := i == len(t.segs)-1
			if sh == shPlain || stars > 1 || !form || i == 0 || (sh == shFile && last) {
				return t, fmt.Errorf("%s: the * is not allowed in segment %s here (one segment other than the first, as * or prefix*, and never the file name of an MCP path)", q(s), q(seg))
			}
		}
	}
	if reachesOwnState(t.anchor, strings.Join(t.segs, "/")) {
		return t, fmt.Errorf("%s: the path reaches Cartographer's own state (the client config file or the cartographer config, state and cache directories), by name or through a wildcard", q(s))
	}
	return t, nil
}

// ParseProvider reads providers/<stem>.yaml: strict (size cap, no alias, one
// document, unknown key = error) and complete, every problem listed as
// "path: reason", sorted.
func ParseProvider(stem string, data []byte) (ProviderSpec, []string) {
	var s ProviderSpec
	if len(data) > contract.ArtifactMaxFileSize {
		return s, []string{fmt.Sprintf("provider: %d bytes, over the %d cap", len(data), contract.ArtifactMaxFileSize)}
	}
	// Aliases are refused before the strict decode: a custom UnmarshalYAML resets
	// yaml.v3's alias accounting, so one anchored scalar could be repeated at will.
	scan := yaml.NewDecoder(bytes.NewReader(data))
	var root yaml.Node
	if err := scan.Decode(&root); err != nil {
		if errors.Is(err, io.EOF) {
			return s, []string{"provider: the file is empty"}
		}
		return s, []string{"provider: " + err.Error()}
	}
	if hasAlias(&root) {
		return s, []string{"provider: YAML aliases are not allowed"}
	}
	if err := scan.Decode(&yaml.Node{}); !errors.Is(err, io.EOF) {
		return s, []string{"provider: more than one YAML document"}
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&s); err != nil {
		var te *yaml.TypeError
		if errors.As(err, &te) {
			var out []string
			for _, e := range te.Errors {
				if f := unknownField.FindStringSubmatch(e); f != nil {
					e = fmt.Sprintf("unknown key %q", f[1])
				}
				out = append(out, "provider: "+e)
			}
			sort.Strings(out)
			return ProviderSpec{}, out
		}
		return ProviderSpec{}, []string{"provider: " + err.Error()}
	}
	var v validator
	s.validate(&v, stem)
	sort.Strings(v.problems)
	return s, v.problems
}

func (s *ProviderSpec) validate(v *validator, stem string) {
	if s.Schema != 1 {
		v.add("schema", "%d is not supported (want 1)", s.Schema)
	}
	switch {
	case !providerNameRe.MatchString(s.Name):
		v.add("name", "%s does not match %s", q(s.Name), providerNameRe)
	case s.Name != stem:
		v.add("name", "%s differs from the file name %s", q(s.Name), q(stem))
	case s.Name == "all" || isProviderID(s.Name):
		v.add("name", "%s is a built-in provider id or reserved", q(s.Name))
	}
	v.text("display_name", s.DisplayName, maxProviderDisplay, false)
	base := isProviderID(s.Base)
	if !base {
		v.add("base", "%s is not a built-in provider", q(s.Base))
	}
	s.validateDetect(v)
	g, p := &s.Paths.Global, &s.Paths.Project
	s.validateCells(v, "paths.global.", g.Skill, g.Agent, g.Instructions, false)
	s.validateCells(v, "paths.project.", p.Skill, p.Agent, p.Instructions, true)
	if h := s.Paths.Global.Hook; h != nil {
		s.dirKey(v, "paths.global.hook.dir", h.Dir, false, shPlain, false)
		if h.Scan != "base" {
			v.add("paths.global.hook.scan", "%s is not supported, only \"base\": a fork reading another hooks directory needs the base's shared dispatcher to take a directory argument, which it does not", q(h.Scan))
		}
	}
	s.validateMCP(v, base)
	if len(s.Capabilities) > maxCapabilities {
		v.add("capabilities", "more than %d entries", maxCapabilities)
	} else {
		for id, val := range s.Capabilities {
			if !contract.IsCapability(id) || !slices.Contains(capabilityVals, val) {
				v.add("capabilities."+q(id), "%s is not verified, unverified or absent (or the id is not a capability id)", q(val))
			}
		}
	}
	if s.InvokeHint != "" {
		v.text("invoke_hint", s.InvokeHint, maxInvokeHint, false)
	}
}

func (s *ProviderSpec) validateDetect(v *validator) {
	dirs := 0
	s.Detect.Dirs.each(func(_ string, l []string) { dirs += len(l) })
	if len(s.Detect.Binaries) == 0 && dirs == 0 {
		v.add("detect", "needs a binary or a dir")
	}
	if len(s.Detect.Binaries) > maxList {
		v.add("detect.binaries", "more than %d entries", maxList)
	}
	for i, b := range s.Detect.Binaries[:min(len(s.Detect.Binaries), maxList)] {
		if b == "" || b == "." || b == ".." || b[0] == '-' || strings.ContainsAny(b, `/\`) || strings.IndexFunc(b, unicode.IsControl) >= 0 {
			v.add(fmt.Sprintf("detect.binaries[%d]", i), "%s is not a bare binary name", q(b))
		}
	}
	s.Detect.Dirs.each(func(os string, l []string) {
		if len(l) > maxList {
			v.add("detect.dirs."+os, "more than %d entries", maxList)
		}
		for i, p := range l[:min(len(l), maxList)] {
			if _, err := parsePathTpl(p, false, shDir, true); err != nil {
				v.add(fmt.Sprintf("detect.dirs.%s[%d]", os, i), "%v", err)
			}
		}
	})
}

// dirKey checks every per-OS value of one path key.
func (s *ProviderSpec) dirKey(v *validator, key string, p osValue[string], project bool, sh pathShape, docs bool) {
	if !p.isSet() {
		v.add(key, "required")
		return
	}
	p.each(func(os, val string) {
		if _, err := parsePathTpl(val, project, sh, docs); err != nil {
			v.add(key+"."+os, "%v", err)
		}
	})
}

func (s *ProviderSpec) validateCells(v *validator, k string, skill *dirCell, a *agentCell, instr *fileCell, project bool) {
	if skill != nil {
		s.dirKey(v, k+"skill.dir", skill.Dir, project, shPlain, false)
	}
	if a != nil {
		s.dirKey(v, k+"agent.dir", a.Dir, project, shPlain, false)
		if a.Format == "" {
			a.Format = "claude"
		}
		if !slices.Contains(agentFormats, a.Format) {
			v.add(k+"agent.format", "unknown %s (want one of %v)", q(a.Format), agentFormats)
		}
	}
	if instr != nil {
		// A global instructions file may lie under {documents}: it is a Markdown
		// file nothing runs. A skill, agent or hook directory there would be
		// executable content next to the Cline dispatchers.
		s.dirKey(v, k+"instructions.file", instr.File, project, shPlain, !project)
		// Every built-in provider's instructions file is Markdown: a shell rc
		// file or a dotfile can never be one.
		instr.File.each(func(os, val string) {
			if !strings.EqualFold(path.Ext(val), ".md") || strings.Contains(val, "*") {
				v.add(k+"instructions.file."+os, "%s must be a .md file", q(val))
			}
		})
	}
}

func (s *ProviderSpec) validateMCP(v *validator, base bool) {
	m := &s.MCP
	if m.Mode == "" {
		m.Mode = "native"
	}
	if !slices.Contains([]string{"native", "cli", "none"}, m.Mode) {
		v.add("mcp.mode", "%s (want native, cli or none)", q(m.Mode))
		return
	}
	if m.Mode != "native" {
		if m.Config.isSet() {
			v.add("mcp.config", "not allowed with mode %s: no entry is written", m.Mode)
		}
		if m.Stale.isSet() {
			v.add("mcp.stale", "not allowed with mode %s: no entry is written", m.Mode)
		}
		return
	}
	var want string
	if d, ok := configurator.Lookup(configurator.Provider(s.Base)); ok && base {
		want = strings.ToLower(path.Ext(d.MCPConfigPath))
	}
	if m.Config.isSet() {
		m.Config.each(func(os, val string) {
			if _, err := parsePathTpl(val, false, shFile, false); err != nil {
				v.add("mcp.config."+os, "%v", err)
			} else if want != "" && strings.ToLower(path.Ext(val)) != want {
				v.add("mcp.config."+os, "%s must have the extension of the base's MCP file (%s): the format is picked from it", q(val), want)
			}
		})
	}
	m.Stale.each(func(os string, l []string) {
		if len(l) > maxStale {
			v.add("mcp.stale."+os, "more than %d entries", maxStale)
		}
		for i, p := range l[:min(len(l), maxStale)] {
			if _, err := parsePathTpl(p, false, shFile, false); err != nil {
				v.add(fmt.Sprintf("mcp.stale.%s[%d]", os, i), "%v", err)
			}
		}
	})
}

// ProviderNameClashes lists the providers of one plugin that share a name.
func ProviderNameClashes(specs []ProviderSpec) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range specs {
		if seen[s.Name] {
			out = append(out, fmt.Sprintf("providers/%s.yaml: the provider name is declared twice in this plugin", s.Name))
		}
		seen[s.Name] = true
	}
	sort.Strings(out)
	return out
}
