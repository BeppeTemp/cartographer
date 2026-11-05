package configurator

import (
	"fmt"
	"path"
	"slices"
	"strings"
	"sync"
)

// MCPModeCLI is the Descriptor.MCPMode of a variant that reaches Cartographer
// through `cartographer call` and has no MCP entry.
const MCPModeCLI = "cli"

// VariantSpec is what the registry needs to know about a client variant: a fork
// of a built-in provider that a KB declares as data (D1655). It is plain data
// built by the caller from a validated internal/clientvariant.Variant: this
// package cannot import that one, which imports this.
type VariantSpec struct {
	Name, DisplayName, Base string
	Binaries                []string
	// DetectDirs are home-relative segment lists; a last segment holding a
	// glob character is a filepath.Match pattern (Descriptor.ConfigDirGlobs),
	// any other is a plain directory (Descriptor.ConfigDirs).
	DetectDirs [][]string
	// MCPFile is the variant's own MCP file, home-relative segments; nil for a
	// mode that writes none (cli, seed).
	MCPFile []string
	// MCPMode "cli" makes the variant reach Cartographer through `cartographer
	// call` (Descriptor.MCPMode); any other value leaves it an MCP provider. The
	// seed globs are carried for the later steps; the registry does not act on them.
	MCPMode      string
	SeedTemplate []string
	SeedDerived  []string
}

// variantEntry is one registered variant: its descriptor and the spec it came from.
type variantEntry struct {
	desc Descriptor
	spec VariantSpec
}

var (
	variantsMu sync.RWMutex
	variants   []variantEntry
)

// RegisterVariant adds spec as a provider that Lookup, EmitServer and agent
// detection see. Providers(), ProviderList() and DetectionOrder() stay built-in
// (every completeness test and "every provider" loop keeps its meaning): a
// caller that needs variants too asks Variants() or AllProviders(). It refuses
// a name that is a built-in or already registered, an unknown base, and an MCP
// file whose extension disagrees with the base's format (Apply picks TOML or
// JSON from the extension).
func RegisterVariant(spec VariantSpec) error {
	e, err := buildVariant(spec)
	if err != nil {
		return err
	}
	variantsMu.Lock()
	defer variantsMu.Unlock()
	for _, v := range variants {
		if v.desc.Provider == e.desc.Provider {
			return fmt.Errorf("variant %q: already registered", spec.Name)
		}
	}
	variants = append(variants, e)
	return nil
}

// ReplaceVariants swaps the whole registered set for specs in one step, so a
// concurrent Lookup never sees the set empty between a reset and the register
// that follows. Nothing changes when any spec is refused. nil empties the set.
func ReplaceVariants(specs []VariantSpec) error {
	next := make([]variantEntry, 0, len(specs))
	seen := map[Provider]bool{}
	for _, s := range specs {
		e, err := buildVariant(s)
		if err != nil {
			return err
		}
		if seen[e.desc.Provider] {
			return fmt.Errorf("variant %q: declared twice", s.Name)
		}
		seen[e.desc.Provider] = true
		next = append(next, e)
	}
	variantsMu.Lock()
	variants = next
	variantsMu.Unlock()
	return nil
}

func buildVariant(spec VariantSpec) (variantEntry, error) {
	name := Provider(spec.Name)
	if spec.Name == "" {
		return variantEntry{}, fmt.Errorf("variant: empty name")
	}
	base, ok := builtin(Provider(spec.Base))
	if !ok {
		return variantEntry{}, fmt.Errorf("variant %q: base %q is not a built-in provider", spec.Name, spec.Base)
	}
	if _, ok := builtin(name); ok {
		return variantEntry{}, fmt.Errorf("variant %q: the name is a built-in provider", spec.Name)
	}
	d := base
	d.Provider = name
	d.DisplayName = spec.DisplayName
	if d.DisplayName == "" {
		d.DisplayName = spec.Name
	}
	d.MCPMode = ""
	if spec.MCPMode == MCPModeCLI {
		d.MCPMode = MCPModeCLI
	}
	d.MCPConfigPath = strings.Join(spec.MCPFile, "/")
	if d.MCPConfigPath != "" {
		if toml := strings.EqualFold(path.Ext(d.MCPConfigPath), ".toml"); toml != (base.MCPFormat == FormatTOMLBlock) {
			return variantEntry{}, fmt.Errorf("variant %q: MCP file %q has the wrong extension for base %q's format", spec.Name, d.MCPConfigPath, spec.Base)
		}
	}
	d.Binaries = append([]string(nil), spec.Binaries...)
	d.ConfigDirs, d.ConfigDirGlobs = nil, nil
	for _, segs := range spec.DetectDirs {
		if len(segs) == 0 {
			continue
		}
		segs = append([]string(nil), segs...)
		if strings.ContainsAny(segs[len(segs)-1], "*?[") {
			d.ConfigDirGlobs = append(d.ConfigDirGlobs, segs)
		} else {
			d.ConfigDirs = append(d.ConfigDirs, segs)
		}
	}
	d.EnvConfigDirs, d.AppDirs, d.InstructionsPrecedence, d.HookPluginMajors = nil, nil, nil, nil
	baseEmit := base.emit
	d.emit = func(n string, s ServerSpec) (*EmitResult, error) {
		if d.MCPConfigPath == "" {
			return nil, fmt.Errorf("variant %q declares no MCP file: nothing to write", name)
		}
		r, err := baseEmit(n, s)
		if err != nil {
			return nil, err
		}
		// The base's emitter names the base's file: the result is the variant's,
		// so Apply writes the overridden path and Lookup(r.Provider) finds it.
		r.Provider, r.FilePath = name, d.ConfigPath()
		return r, nil
	}
	return variantEntry{d, cloneSpec(spec)}, nil
}

func cloneSpec(s VariantSpec) VariantSpec {
	s.Binaries = slices.Clone(s.Binaries)
	s.MCPFile = slices.Clone(s.MCPFile)
	s.SeedTemplate = slices.Clone(s.SeedTemplate)
	s.SeedDerived = slices.Clone(s.SeedDerived)
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

// Variants returns the registered variants' descriptors, in registration order.
func Variants() []Descriptor {
	variantsMu.RLock()
	defer variantsMu.RUnlock()
	out := make([]Descriptor, len(variants))
	for i, v := range variants {
		out[i] = v.desc.copyOf()
	}
	return out
}

// RegisteredVariant is a variant's descriptor with the spec it came from.
type RegisteredVariant struct {
	Descriptor Descriptor
	Spec       VariantSpec
}

// RegisteredVariants returns every variant with its spec, read under one lock.
func RegisteredVariants() []RegisteredVariant {
	variantsMu.RLock()
	defer variantsMu.RUnlock()
	out := make([]RegisteredVariant, len(variants))
	for i, v := range variants {
		out[i] = RegisteredVariant{v.desc.copyOf(), cloneSpec(v.spec)}
	}
	return out
}

// VariantOf returns the spec a registered variant came from.
func VariantOf(p Provider) (VariantSpec, bool) {
	variantsMu.RLock()
	defer variantsMu.RUnlock()
	for _, v := range variants {
		if v.desc.Provider == p {
			return cloneSpec(v.spec), true
		}
	}
	return VariantSpec{}, false
}

// AllProviders returns the built-in descriptors, then the variants'. Use it only
// where the question is "which provider owns this", never "every provider".
func AllProviders() []Descriptor {
	return append(Providers(), Variants()...)
}

func builtin(p Provider) (Descriptor, bool) {
	for _, d := range descriptors {
		if d.Provider == p {
			return d, true
		}
	}
	return Descriptor{}, false
}
