// Package clientvariant defines the descriptor of a client variant: a fork of
// a supported client that a KB declares as data (variants/<name>.yaml), and the
// pure functions that validate it and resolve it for one operating system.
//
// Nothing here touches the disk or the process environment: Resolve reads an
// Env the caller supplies, so every OS block is testable on any host. A
// descriptor only names paths, which Resolve keeps under the home directory.
package clientvariant

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"gopkg.in/yaml.v3"

	"github.com/BeppeTemp/cartographer/internal/configurator"
)

// MaxSize caps a descriptor. It mirrors provisioning.ArtifactMaxFileSize,
// which this package must not import; a test pins the two together.
const MaxSize = 256 * 1024

// MCP modes of a variant.
const (
	MCPFile = "file"
	MCPCLI  = "cli"
	MCPSeed = "seed"
)

var (
	nameRe       = regexp.MustCompile(`^[a-z][a-z0-9-]{1,30}$`)
	capabilityRe = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,63}$`)
	// kiro and codex write JSON/TOML files and are not offered as agent formats.
	agentFormats   = []string{"claude", "opencode", "antigravity", "copilot", "cline"}
	allowCallBases = []string{"crush"}
	osKeys         = []string{"darwin", "linux", "windows", "default"}
)

// perOS is a value given once for every OS or as a map keyed by OS (osKeys).
type perOS[T any] struct{ byOS map[string]T }

func (p *perOS[T]) UnmarshalYAML(n *yaml.Node) error {
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
			return fmt.Errorf("line %d: unknown OS key %q (want darwin, linux, windows or default)", n.Content[i].Line, k)
		}
		var v T
		if err := n.Content[i+1].Decode(&v); err != nil {
			return err
		}
		p.byOS[k] = v
	}
	return nil
}

func (p perOS[T]) pick(goos string) (T, bool) {
	if v, ok := p.byOS[goos]; ok {
		return v, true
	}
	v, ok := p.byOS["default"]
	return v, ok
}

func (p perOS[T]) each(f func(osKey string, v T)) {
	for _, k := range osKeys {
		if v, ok := p.byOS[k]; ok {
			f(k, v)
		}
	}
}

func (p perOS[T]) isSet() bool { return len(p.byOS) > 0 }

// Spec is a descriptor as written. It is built only by Parse.
type Spec struct {
	Schema      int    `yaml:"schema"`
	Name        string `yaml:"name"`
	DisplayName string `yaml:"display_name"`
	Base        string `yaml:"base"`
	Detect      struct {
		Binaries []string        `yaml:"binaries"`
		Dirs     perOS[[]string] `yaml:"dirs"`
	} `yaml:"detect"`
	MCP struct {
		Mode      string `yaml:"mode"`
		AllowCall bool   `yaml:"allow_call"`
		Seed      *struct {
			TemplateGlob perOS[string] `yaml:"template_glob"`
			DerivedGlob  perOS[string] `yaml:"derived_glob"`
		} `yaml:"seed"`
	} `yaml:"mcp"`
	Paths struct {
		Global  cellsSpec `yaml:"global"`
		Project cellsSpec `yaml:"project"`
	} `yaml:"paths"`
	Capabilities map[string]string `yaml:"capabilities"`
	InvokeHint   string            `yaml:"invoke_hint"`
}

type (
	dirCell struct {
		Dir perOS[string] `yaml:"dir"`
	}
	fileCell struct {
		File perOS[string] `yaml:"file"`
	}
	agentCell struct {
		Dir    perOS[string] `yaml:"dir"`
		Format string        `yaml:"format"`
	}
	hookCell struct {
		Dir  perOS[string] `yaml:"dir"`
		Scan string        `yaml:"scan"`
	}
)

// cellsSpec holds the cells of one scope; a project takes no mcp or hook cell.
type cellsSpec struct {
	Skill        *dirCell   `yaml:"skill"`
	Agent        *agentCell `yaml:"agent"`
	Instructions *fileCell  `yaml:"instructions"`
	MCP          *fileCell  `yaml:"mcp"`
	Hook         *hookCell  `yaml:"hook"`
}

// walk visits every single-path field of the descriptor (not detect.dirs, a list).
func (s *Spec) walk(f func(key string, p perOS[string], project bool, sh shape)) {
	if m := s.MCP.Seed; m != nil {
		f("mcp.seed.template_glob", m.TemplateGlob, false, shSeed)
		f("mcp.seed.derived_glob", m.DerivedGlob, false, shSeed)
	}
	for _, sc := range []struct {
		name string
		c    *cellsSpec
	}{{"global", &s.Paths.Global}, {"project", &s.Paths.Project}} {
		k, c, proj := "paths."+sc.name+".", sc.c, sc.name == "project"
		if c.Skill != nil {
			f(k+"skill.dir", c.Skill.Dir, proj, shPlain)
		}
		if c.Agent != nil {
			f(k+"agent.dir", c.Agent.Dir, proj, shPlain)
		}
		if c.Instructions != nil {
			f(k+"instructions.file", c.Instructions.File, proj, shPlain)
		}
		if c.MCP != nil {
			f(k+"mcp.file", c.MCP.File, proj, shPlain)
		}
		if c.Hook != nil {
			f(k+"hook.dir", c.Hook.Dir, proj, shPlain)
		}
	}
}

// Parse validates descriptor bytes read from variants/<name>.yaml. Errors name
// the offending key.
func Parse(name string, data []byte) (Spec, error) {
	var s Spec
	if len(data) > MaxSize {
		return s, fmt.Errorf("descriptor is %d bytes, over the %d cap", len(data), MaxSize)
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&s); err != nil {
		if errors.Is(err, io.EOF) {
			return Spec{}, errors.New("descriptor is empty")
		}
		return Spec{}, fmt.Errorf("descriptor: %w", err)
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return Spec{}, errors.New("descriptor holds more than one YAML document")
	}
	if err := s.validate(name); err != nil {
		return Spec{}, err
	}
	return s, nil
}

func (s *Spec) validate(stem string) error {
	switch {
	case s.Schema != 1:
		return fmt.Errorf("schema: %d is not supported (want 1)", s.Schema)
	case !nameRe.MatchString(s.Name):
		return fmt.Errorf("name: %q does not match %s", s.Name, nameRe)
	case s.Name != stem:
		return fmt.Errorf("name: %q differs from the file name %q", s.Name, stem)
	case s.Name == "all" || isBuiltin(s.Name):
		return fmt.Errorf("name: %q is a built-in provider id or reserved", s.Name)
	case !isBuiltin(s.Base):
		return fmt.Errorf("base: %q is not a built-in provider", s.Base)
	case len(s.Detect.Binaries) == 0 && !s.Detect.Dirs.isSet():
		return fmt.Errorf("detect: needs binaries or dirs")
	}
	if err := printable("display_name", s.DisplayName, 40); err != nil {
		return err
	}
	for i, b := range s.Detect.Binaries {
		if b == "" || strings.ContainsAny(b, `/\`) || hasControl(b) {
			return fmt.Errorf("detect.binaries[%d]: %q is not a bare binary name", i, b)
		}
	}
	var errs []error
	s.Detect.Dirs.each(func(os string, l []string) {
		for i, p := range l {
			errs = append(errs, checkPath(fmt.Sprintf("detect.dirs.%s[%d]", os, i), p, false, shDetect))
		}
	})
	s.walk(func(key string, p perOS[string], project bool, sh shape) {
		p.each(func(os, v string) { errs = append(errs, checkPath(key+"."+os, v, project, sh)) })
	})
	errs = append(errs, s.validateMCP(), s.validateCells())
	for id, v := range s.Capabilities {
		if !capabilityRe.MatchString(id) || !slices.Contains([]string{"verified", "unverified", "absent"}, v) {
			errs = append(errs, fmt.Errorf("capabilities.%s: %q is not verified, unverified or absent (or the id is malformed)", id, v))
		}
	}
	if s.InvokeHint != "" {
		errs = append(errs, printable("invoke_hint", s.InvokeHint, 200))
	}
	return errors.Join(errs...)
}

func (s *Spec) validateMCP() error {
	m, g := &s.MCP, &s.Paths.Global
	if m.Mode == "" {
		m.Mode = MCPFile
	}
	switch {
	case !slices.Contains([]string{MCPFile, MCPCLI, MCPSeed}, m.Mode):
		return fmt.Errorf("mcp.mode: %q (want file, cli or seed)", m.Mode)
	case m.AllowCall && (m.Mode != MCPCLI || !slices.Contains(allowCallBases, s.Base)):
		return fmt.Errorf("mcp.allow_call: only a cli variant of %v has a known permission syntax", allowCallBases)
	case (m.Mode == MCPSeed) != (m.Seed != nil):
		return errors.New("mcp.seed: required by mode seed and refused by any other")
	case m.Seed != nil && (!m.Seed.TemplateGlob.isSet() || !m.Seed.DerivedGlob.isSet()):
		return errors.New("mcp.seed: needs template_glob and derived_glob")
	}
	// The MCP file is the target of mode file and the permission file of allow_call.
	if has := g.MCP != nil && g.MCP.File.isSet(); has != (m.Mode == MCPFile || m.AllowCall) {
		return errors.New("paths.global.mcp.file: required by mode file and by allow_call, refused otherwise")
	}
	return nil
}

func (s *Spec) validateCells() error {
	var errs []error
	for name, c := range map[string]*cellsSpec{"global": &s.Paths.Global, "project": &s.Paths.Project} {
		if a := c.Agent; a != nil {
			if a.Format == "" {
				a.Format = "claude"
			}
			if !slices.Contains(agentFormats, a.Format) {
				errs = append(errs, fmt.Errorf("paths.%s.agent.format: unknown %q (want one of %v)", name, a.Format, agentFormats))
			}
		}
	}
	if p := s.Paths.Project; p.MCP != nil || p.Hook != nil {
		errs = append(errs, errors.New("paths.project: mcp and hook cells are global only"))
	}
	if h := s.Paths.Global.Hook; h != nil && (h.Scan != "base" || !h.Dir.isSet()) {
		errs = append(errs, fmt.Errorf("paths.global.hook: needs dir and scan \"base\" (got %q): a fork reading another hooks directory needs the base's shared dispatcher to take a directory argument, which it does not", h.Scan))
	}
	return errors.Join(errs...)
}

func isBuiltin(id string) bool {
	return slices.Contains(configurator.ProviderList(), configurator.Provider(id))
}

func hasControl(s string) bool { return strings.IndexFunc(s, unicode.IsControl) >= 0 }

func printable(key, v string, max int) error {
	if v == "" || utf8.RuneCountInString(v) > max || strings.IndexFunc(v, func(r rune) bool { return !unicode.IsPrint(r) }) >= 0 {
		return fmt.Errorf("%s: must be 1 to %d printable characters on one line", key, max)
	}
	return nil
}
