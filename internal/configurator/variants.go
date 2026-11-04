package configurator

import (
	"fmt"
	"strings"
	"sync"
)

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
	// MCPMode and the seed globs are carried for the later steps; the registry
	// does not act on them.
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
// a name that is a built-in or already registered, and an unknown base.
func RegisterVariant(spec VariantSpec) error {
	name := Provider(spec.Name)
	if spec.Name == "" {
		return fmt.Errorf("variant: empty name")
	}
	base, ok := builtin(Provider(spec.Base))
	if !ok {
		return fmt.Errorf("variant %q: base %q is not a built-in provider", spec.Name, spec.Base)
	}
	if _, ok := builtin(name); ok {
		return fmt.Errorf("variant %q: the name is a built-in provider", spec.Name)
	}
	d := base
	d.Provider = name
	d.DisplayName = spec.DisplayName
	if d.DisplayName == "" {
		d.DisplayName = spec.Name
	}
	d.MCPConfigPath = strings.Join(spec.MCPFile, "/")
	d.Binaries = append([]string(nil), spec.Binaries...)
	d.ConfigDirs, d.ConfigDirGlobs = nil, nil
	for _, segs := range spec.DetectDirs {
		if len(segs) == 0 {
			continue
		}
		if strings.ContainsAny(segs[len(segs)-1], "*?[") {
			d.ConfigDirGlobs = append(d.ConfigDirGlobs, segs)
		} else {
			d.ConfigDirs = append(d.ConfigDirs, segs)
		}
	}
	d.EnvConfigDirs, d.AppDirs, d.InstructionsPrecedence, d.HookPluginMajors = nil, nil, nil, nil
	baseEmit := base.emit
	d.emit = func(n string, s ServerSpec) (*EmitResult, error) {
		r, err := baseEmit(n, s)
		if err != nil {
			return nil, err
		}
		// The base's emitter names the base's file: the result is the variant's,
		// so Apply writes the overridden path and Lookup(r.Provider) finds it.
		r.Provider, r.FilePath = name, d.ConfigPath()
		return r, nil
	}

	variantsMu.Lock()
	defer variantsMu.Unlock()
	for _, v := range variants {
		if v.desc.Provider == name {
			return fmt.Errorf("variant %q: already registered", spec.Name)
		}
	}
	variants = append(variants, variantEntry{d, spec})
	return nil
}

// ResetVariants forgets every registered variant: tests, and a refresh that
// registers the current set again.
func ResetVariants() {
	variantsMu.Lock()
	variants = nil
	variantsMu.Unlock()
}

// Variants returns the registered variants' descriptors, in registration order.
func Variants() []Descriptor {
	variantsMu.RLock()
	defer variantsMu.RUnlock()
	out := make([]Descriptor, len(variants))
	for i, v := range variants {
		out[i] = v.desc
	}
	return out
}

// VariantOf returns the spec a registered variant came from.
func VariantOf(p Provider) (VariantSpec, bool) {
	variantsMu.RLock()
	defer variantsMu.RUnlock()
	for _, v := range variants {
		if v.desc.Provider == p {
			return v.spec, true
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
