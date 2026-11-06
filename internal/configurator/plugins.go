package configurator

import (
	"fmt"
	"path"
	"slices"
	"strings"
	"sync"
)

// MCPModeNone is the Descriptor.MCPMode of a plugin provider that reaches
// Cartographer through neither an MCP entry nor `cartographer call`: nothing is
// written for it.
const MCPModeNone = "none"

// MCP modes a PluginSpec may carry ("" is native).
const mcpModeNative = "native"

// PluginSpec is what the registry needs to know about a client a plugin
// declares as data: a fork of a built-in provider. It is plain data built by the
// caller from a validated and resolved descriptor (internal/plugin): this package
// cannot import that one, which imports this.
type PluginSpec struct {
	Name, DisplayName, Base string
	// Origin is the name of the plugin that declares the provider.
	Origin string
	// Active is false for a provider whose plugin left the active set: it stays
	// resolvable, so what it installed can be retired, and it is never reported
	// installed by agent detection.
	Active   bool
	Binaries []string
	// DetectDirs are home-relative segment lists; a list with a segment holding a
	// glob character is a filepath.Match pattern on that segment
	// (Descriptor.ConfigDirGlobs), any other is a plain directory
	// (Descriptor.ConfigDirs).
	DetectDirs [][]string
	// MCPMode is "native" (or empty), "cli" or "none".
	MCPMode string
	// MCPConfig is the fork's own MCP file for mode native, home-relative
	// segments, one of them possibly holding a `*` (a version folder); nil keeps
	// the base's file. A pattern is carried in Descriptor.MCPConfigGlob and never
	// in MCPConfigPath, which callers join literally.
	MCPConfig []string
	// MCPStale are the home-relative files the client derives from MCPConfig and
	// regenerates; nil for none.
	MCPStale [][]string
}

// pluginEntry is one registered provider: its descriptor and the spec it came from.
type pluginEntry struct {
	desc Descriptor
	spec PluginSpec
}

var (
	pluginsMu sync.RWMutex
	plugins   []pluginEntry
)

// RegisterPlugin adds spec as a provider that Lookup, EmitServer and agent
// detection see, beside the built-ins and never inside their lists: Providers(),
// ProviderList(), DetectionOrder() and EmitAll stay built-in (every completeness
// test and "every provider" loop keeps its meaning), and a caller that needs the
// registered ones too asks Plugins() or AllProviders(). It refuses a name that is
// a built-in or already registered, a base that is not a built-in, and an MCP
// file whose extension disagrees with the base's format (Apply picks TOML or
// JSON from the extension).
func RegisterPlugin(spec PluginSpec) error {
	e, err := buildPlugin(spec)
	if err != nil {
		return err
	}
	pluginsMu.Lock()
	defer pluginsMu.Unlock()
	for _, p := range plugins {
		if p.desc.Provider == e.desc.Provider {
			return fmt.Errorf("plugin provider %q: already registered", spec.Name)
		}
	}
	plugins = append(plugins, e)
	return nil
}

// ReplacePlugins swaps the whole registered set for specs in one step, so a
// concurrent Lookup never sees the set empty between a reset and the register
// that follows. A name claimed by two or more specs (two plugins declaring the
// same provider) is refused for every claimant, as DropPluginCollisions does for
// artifacts, and returned sorted for the caller to report; every other spec still
// registers. Nothing changes when a spec is invalid (the error). nil empties the
// set.
func ReplacePlugins(specs []PluginSpec) (refused []string, err error) {
	count := map[Provider]int{}
	entries := make([]pluginEntry, 0, len(specs))
	for _, s := range specs {
		e, err := buildPlugin(s)
		if err != nil {
			return nil, err
		}
		count[e.desc.Provider]++
		entries = append(entries, e)
	}
	next := make([]pluginEntry, 0, len(entries))
	for _, e := range entries {
		if count[e.desc.Provider] > 1 {
			continue
		}
		next = append(next, e)
	}
	for name, n := range count {
		if n > 1 {
			refused = append(refused, string(name))
		}
	}
	slices.Sort(refused)
	pluginsMu.Lock()
	plugins = next
	pluginsMu.Unlock()
	return refused, nil
}

func buildPlugin(spec PluginSpec) (pluginEntry, error) {
	name := Provider(spec.Name)
	if spec.Name == "" {
		return pluginEntry{}, fmt.Errorf("plugin provider: empty name")
	}
	base, ok := builtin(Provider(spec.Base))
	if !ok {
		return pluginEntry{}, fmt.Errorf("plugin provider %q: base %q is not a built-in provider", spec.Name, spec.Base)
	}
	if _, ok := builtin(name); ok {
		return pluginEntry{}, fmt.Errorf("plugin provider %q: the name is a built-in provider", spec.Name)
	}
	d := base
	d.MCPConfigGlob = ""
	d.Provider = name
	d.DisplayName = spec.DisplayName
	if d.DisplayName == "" {
		d.DisplayName = spec.Name
	}
	switch spec.MCPMode {
	case "", mcpModeNative:
		d.MCPMode = ""
	case MCPModeCLI, MCPModeNone:
		d.MCPMode = spec.MCPMode
		if len(spec.MCPConfig) > 0 || len(spec.MCPStale) > 0 {
			return pluginEntry{}, fmt.Errorf("plugin provider %q: mode %s takes no MCP file", spec.Name, spec.MCPMode)
		}
		d.MCPConfigPath = ""
	default:
		return pluginEntry{}, fmt.Errorf("plugin provider %q: unknown MCP mode %q", spec.Name, spec.MCPMode)
	}
	if len(spec.MCPConfig) > 0 {
		// The fork's own file. A `*` segment makes it a pattern, kept out of
		// MCPConfigPath (left empty) so that no caller joins it literally.
		file := strings.Join(spec.MCPConfig, "/")
		if toml := strings.EqualFold(path.Ext(file), ".toml"); toml != (base.MCPFormat == FormatTOMLBlock) {
			return pluginEntry{}, fmt.Errorf("plugin provider %q: MCP file %q has the wrong extension for base %q's format", spec.Name, file, spec.Base)
		}
		if strings.Contains(file, "*") {
			d.MCPConfigPath, d.MCPConfigGlob = "", file
		} else {
			d.MCPConfigPath = file
		}
	}
	d.Binaries = slices.Clone(spec.Binaries)
	d.ConfigDirs, d.ConfigDirGlobs = nil, nil
	for _, segs := range spec.DetectDirs {
		if len(segs) == 0 {
			continue
		}
		segs = slices.Clone(segs)
		if slices.ContainsFunc(segs, func(s string) bool { return strings.ContainsAny(s, "*?[") }) {
			d.ConfigDirGlobs = append(d.ConfigDirGlobs, segs)
		} else {
			d.ConfigDirs = append(d.ConfigDirs, segs)
		}
	}
	// The base's own evidence is not the fork's: a name or a directory of the
	// base would make the fork look installed.
	d.EnvConfigDirs, d.AppDirs, d.InstructionsPrecedence, d.HookPluginMajors = nil, nil, nil, nil
	baseEmit := base.emit
	d.emit = func(n string, s ServerSpec) (*EmitResult, error) {
		if d.MCPConfigPath == "" && d.MCPConfigGlob == "" {
			return nil, fmt.Errorf("plugin provider %q writes no MCP entry (mode %s)", name, d.MCPMode)
		}
		r, err := baseEmit(n, s)
		if err != nil {
			return nil, err
		}
		if d.MCPConfigGlob != "" {
			// Apply expands the pattern; FilePath stays empty so no caller joins it.
			r.Provider, r.FilePath, r.FileGlob = name, "", d.MCPConfigGlob
			return r, nil
		}
		// The base's emitter names the base's file: the result is the fork's, so
		// Apply writes the overridden path and Lookup(r.Provider) finds it.
		r.Provider, r.FilePath = name, d.ConfigPath()
		return r, nil
	}
	return pluginEntry{d, clonePluginSpec(spec)}, nil
}

func clonePluginSpec(s PluginSpec) PluginSpec {
	s.Binaries = slices.Clone(s.Binaries)
	s.MCPConfig = slices.Clone(s.MCPConfig)
	s.MCPStale = cloneDirs(s.MCPStale)
	s.DetectDirs = cloneDirs(s.DetectDirs)
	return s
}

func cloneDirs(in [][]string) [][]string {
	if in == nil {
		return nil
	}
	out := make([][]string, len(in))
	for i, d := range in {
		out[i] = slices.Clone(d)
	}
	return out
}

// copyOf returns d with the slices the registry owns copied, so a caller that
// edits a result cannot change the registered descriptor.
func (d Descriptor) copyOf() Descriptor {
	d.Binaries = slices.Clone(d.Binaries)
	d.ConfigDirs = cloneDirs(d.ConfigDirs)
	d.ConfigDirGlobs = cloneDirs(d.ConfigDirGlobs)
	return d
}

// lookupPlugin finds a registered provider; Lookup calls it after the built-ins.
func lookupPlugin(p Provider) (Descriptor, bool) {
	pluginsMu.RLock()
	defer pluginsMu.RUnlock()
	for _, e := range plugins {
		if e.desc.Provider == p {
			return e.desc.copyOf(), true
		}
	}
	return Descriptor{}, false
}

// Plugins returns the registered providers' descriptors, in registration order.
func Plugins() []Descriptor {
	pluginsMu.RLock()
	defer pluginsMu.RUnlock()
	out := make([]Descriptor, len(plugins))
	for i, e := range plugins {
		out[i] = e.desc.copyOf()
	}
	return out
}

// RegisteredPlugin is a registered provider's descriptor with the spec it came from.
type RegisteredPlugin struct {
	Descriptor Descriptor
	Spec       PluginSpec
}

// RegisteredPlugins returns every registered provider with its spec, read under one lock.
func RegisteredPlugins() []RegisteredPlugin {
	pluginsMu.RLock()
	defer pluginsMu.RUnlock()
	out := make([]RegisteredPlugin, len(plugins))
	for i, e := range plugins {
		out[i] = RegisteredPlugin{e.desc.copyOf(), clonePluginSpec(e.spec)}
	}
	return out
}

// PluginOf returns the spec a registered provider came from.
func PluginOf(p Provider) (PluginSpec, bool) {
	pluginsMu.RLock()
	defer pluginsMu.RUnlock()
	for _, e := range plugins {
		if e.desc.Provider == p {
			return clonePluginSpec(e.spec), true
		}
	}
	return PluginSpec{}, false
}

// AllProviders returns the built-in descriptors, then the registered ones. Use
// it only where the question is "which provider owns this", never "every
// provider".
func AllProviders() []Descriptor {
	return append(Providers(), Plugins()...)
}

func builtin(p Provider) (Descriptor, bool) {
	for _, d := range descriptors {
		if d.Provider == p {
			return d, true
		}
	}
	return Descriptor{}, false
}
