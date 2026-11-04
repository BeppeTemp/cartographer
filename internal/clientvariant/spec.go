// Package clientvariant defines the descriptor of a client variant: a fork of
// a supported client that a KB declares as data (variants/<name>.yaml), and the
// pure functions that validate it and resolve it for one operating system. Nothing
// here touches the disk or the environment: Resolve reads an Env the caller supplies.
package clientvariant

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"gopkg.in/yaml.v3"

	"github.com/BeppeTemp/cartographer/internal/configurator"
)

// MaxSize caps a descriptor. It mirrors provisioning.ArtifactMaxFileSize,
// which this package must not import; a test pins the two together.
const MaxSize = 256 * 1024

var (
	nameRe       = regexp.MustCompile(`^[a-z][a-z0-9-]{1,30}$`)
	capabilityRe = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,63}$`)
	// kiro and codex write JSON/TOML files and are not offered as agent formats.
	agentFormats   = []string{"claude", "opencode", "antigravity", "copilot", "cline"}
	allowCallBases = []string{"crush"}
	osKeys         = []string{"darwin", "linux", "windows", "default"}
)

const maxList = 16 // entries of a descriptor list

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
			return fmt.Errorf("line %d: unknown OS key %s (want darwin, linux, windows or default)", n.Content[i].Line, q(k))
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
	// Aliases are refused before the strict decode: a custom UnmarshalYAML resets
	// yaml.v3's alias accounting, so one anchored scalar could be repeated at will.
	scan := yaml.NewDecoder(bytes.NewReader(data))
	var root yaml.Node
	if err := scan.Decode(&root); err != nil {
		if errors.Is(err, io.EOF) {
			err = errors.New("the descriptor is empty")
		}
		return Spec{}, fmt.Errorf("descriptor: %w", err)
	}
	if hasAlias(&root) {
		return Spec{}, errors.New("descriptor: YAML aliases are not allowed")
	}
	if err := scan.Decode(&yaml.Node{}); !errors.Is(err, io.EOF) {
		return Spec{}, errors.New("descriptor holds more than one YAML document")
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&s); err != nil {
		return Spec{}, fmt.Errorf("descriptor: %w", err)
	}
	if err := s.validate(name); err != nil {
		return Spec{}, err
	}
	return s, nil
}

func hasAlias(n *yaml.Node) bool {
	return n.Kind == yaml.AliasNode || slices.ContainsFunc(n.Content, func(c *yaml.Node) bool { return hasAlias(c) })
}

// q quotes a value for an error message, cut so a hostile descriptor cannot inflate it.
func q(s string) string {
	if len(s) > 48 {
		s = s[:48] + "..."
	}
	return strconv.Quote(s)
}

type adder func(cond bool, format string, a ...any)

// validate collects every problem and reports them sorted, so the text is stable.
func (s *Spec) validate(stem string) error {
	var errs []string
	add := func(cond bool, f string, a ...any) {
		if cond {
			errs = append(errs, fmt.Sprintf(f, a...))
		}
	}
	addErr := func(err error) { add(err != nil, "%v", err) }
	dirs := 0
	s.Detect.Dirs.each(func(_ string, l []string) { dirs += len(l) })
	add(s.Schema != 1, "schema: %d is not supported (want 1)", s.Schema)
	add(!nameRe.MatchString(s.Name), "name: %s does not match %s", q(s.Name), nameRe)
	add(s.Name != stem, "name: %s differs from the file name %s", q(s.Name), q(stem))
	add(s.Name == "all" || isBuiltin(s.Name), "name: %s is a built-in provider id or reserved", q(s.Name))
	add(!isBuiltin(s.Base), "base: %s is not a built-in provider", q(s.Base))
	add(len(s.Detect.Binaries) == 0 && dirs == 0, "detect: needs a binary or a dir")
	addErr(printable("display_name", s.DisplayName, 40))
	add(len(s.Detect.Binaries) > maxList, "detect.binaries: more than %d entries", maxList)
	for i, b := range s.Detect.Binaries[:min(len(s.Detect.Binaries), maxList)] {
		add(b == "" || b == "." || b == ".." || b[0] == '-' || strings.ContainsAny(b, `/\`) || hasControl(b), "detect.binaries[%d]: %s is not a bare binary name", i, q(b))
	}
	s.Detect.Dirs.each(func(os string, l []string) {
		add(len(l) > maxList, "detect.dirs.%s: more than %d entries", os, maxList)
		for i, p := range l[:min(len(l), maxList)] {
			addErr(checkPath(fmt.Sprintf("detect.dirs.%s[%d]", os, i), p, false, shDetect))
		}
	})
	s.walk(func(key string, p perOS[string], project bool, sh shape) {
		add(!p.isSet(), "%s: required", key)
		p.each(func(os, v string) { addErr(checkPath(key+"."+os, v, project, sh)) })
	})
	s.validateMCP(add)
	s.validateCells(add)
	add(len(s.Capabilities) > 64, "capabilities: more than 64 entries")
	for id, v := range s.Capabilities {
		add(len(s.Capabilities) <= 64 && (!capabilityRe.MatchString(id) || !slices.Contains([]string{"verified", "unverified", "absent"}, v)), "capabilities.%s: %s is not verified, unverified or absent (or the id is malformed)", q(id), q(v))
	}
	if s.InvokeHint != "" {
		addErr(printable("invoke_hint", s.InvokeHint, 200))
	}
	if len(errs) == 0 {
		return nil
	}
	sort.Strings(errs)
	return errors.New(strings.Join(errs, "\n"))
}

func (s *Spec) validateMCP(add adder) {
	m := &s.MCP
	if m.Mode == "" {
		m.Mode = "file"
	}
	add(!slices.Contains([]string{"file", "cli", "seed"}, m.Mode), "mcp.mode: %s (want file, cli or seed)", q(m.Mode))
	add(m.AllowCall && (m.Mode != "cli" || !slices.Contains(allowCallBases, s.Base)), "mcp.allow_call: only a cli variant of %v has a known permission syntax", allowCallBases)
	add((m.Mode == "seed") != (m.Seed != nil), "mcp.seed: required by mode seed and refused by any other")
	// The MCP file is the target of mode file and the permission file of allow_call.
	add((s.Paths.Global.MCP != nil) != (m.Mode == "file" || m.AllowCall), "paths.global.mcp.file: required by mode file and by allow_call, refused otherwise")
}

func (s *Spec) validateCells(add adder) {
	for _, c := range []*cellsSpec{&s.Paths.Global, &s.Paths.Project} {
		if a := c.Agent; a != nil {
			if a.Format == "" {
				a.Format = "claude"
			}
			add(!slices.Contains(agentFormats, a.Format), "agent.format: unknown %s (want one of %v)", q(a.Format), agentFormats)
		}
	}
	add(s.Paths.Project.MCP != nil || s.Paths.Project.Hook != nil, "paths.project: mcp and hook cells are global only")
	if h := s.Paths.Global.Hook; h != nil {
		add(h.Scan != "base", "paths.global.hook.scan: %s is not supported, only \"base\": a fork reading another hooks directory needs the base's shared dispatcher to take a directory argument, which it does not", q(h.Scan))
	}
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
