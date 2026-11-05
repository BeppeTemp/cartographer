package provisioning

import (
	"fmt"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/BeppeTemp/cartographer/internal/clientvariant"
	"github.com/BeppeTemp/cartographer/internal/configurator"
)

// A client variant is a runtime provider whose destination cells are data a KB
// declared (internal/clientvariant), not rows of the static matrices. The static
// matrices stay built-in only: the completeness tests range over them and a
// variant is outside them by construction. Every read of a cell goes through
// cellFor, which answers for both.

// variantEntry is the provisioning view of one registered variant.
type variantEntry struct {
	base         configurator.Provider
	agentFormat  string
	cells        map[Scope]map[string]destination
	capabilities map[string]string
	invokeHint   string
	// roots are the prune boundaries the cells add (variantBoundaries).
	roots []string
}

var (
	variantCellsMu sync.RWMutex
	variantCells   = map[configurator.Provider]variantEntry{}
)

// cellFor is the one read of a destination cell: the static matrix of the scope,
// then the cells a registered variant declared. A kind the variant did not
// declare is not found, which every caller treats as unsupported.
func cellFor(scope Scope, kind string, provider configurator.Provider) (destination, bool) {
	if cell, ok := matrixFor(scope)[kind][provider]; ok {
		return cell, true
	}
	variantCellsMu.RLock()
	defer variantCellsMu.RUnlock()
	if v, ok := variantCells[provider]; ok {
		if cell, ok := v.cells[scope][kind]; ok {
			return cell, true
		}
	}
	return unsupportedDest, false
}

// variantFor returns the registered variant of provider.
func variantFor(provider configurator.Provider) (variantEntry, bool) {
	variantCellsMu.RLock()
	defer variantCellsMu.RUnlock()
	v, ok := variantCells[provider]
	return v, ok
}

// nativeProvider is the id an agent's `providers.<id>` frontmatter block is read
// under: a variant honours its base's block.
func nativeProvider(provider configurator.Provider) configurator.Provider {
	if v, ok := variantFor(provider); ok {
		return v.base
	}
	return provider
}

// variantAgentSuffix is the file suffix of each agent.format.
func variantAgentSuffix(format string) string {
	switch format {
	case "copilot":
		return ".agent.md"
	case "cline":
		return ".yaml"
	}
	return ".md"
}

// variantDestinations builds the cells of v. The hook cell is never built here:
// a hook destination without the base's registrar would copy the KB's hook
// files with nothing wiring them, so it is ignored until a later release
// enables it (the returned note says so).
func variantDestinations(v clientvariant.Variant) (map[Scope]map[string]destination, []string) {
	cells := map[Scope]map[string]destination{ScopeGlobal: {}, ScopeProject: {}}
	for scope, sc := range map[Scope]clientvariant.Scope{ScopeGlobal: v.Global, ScopeProject: v.Project} {
		if sc.Skill != nil {
			cells[scope]["skill"] = perName("", sc.Skill.Dir...)
		}
		if sc.Agent != nil {
			cells[scope]["agent"] = perName(variantAgentSuffix(sc.Agent.AgentFormat), sc.Agent.Dir...)
		}
		if sc.Instructions != nil {
			cells[scope]["instructions"] = at(sc.Instructions.File...)
		}
	}
	// Only mode file writes an MCP entry: the cli and seed modes have no mcp
	// cell, and an allow_call file is a permission file, not an MCP target.
	if v.MCPMode == "file" && v.MCPFile != nil {
		cells[ScopeGlobal]["mcp"] = at(v.MCPFile...)
	}
	var notes []string
	if v.HookDir != nil {
		notes = append(notes, fmt.Sprintf("%s: hook cell: applied by a later release of this client", v.Name))
	}
	return cells, notes
}

// variantRoots lists the global-scope directory roots a variant's cells own, as
// slash paths relative to the home, the way ManagedDestinationRoots does.
func variantRoots(cells map[string]destination) []string {
	var out []string
	for _, c := range cells {
		if rel := path.Join(c.dir...); rel != "" && rel != "." {
			out = append(out, rel)
		}
	}
	sort.Strings(out)
	return out
}

// globalRootRels returns the home-relative slash roots of provider's global cells.
func globalRootRels(provider configurator.Provider) []string {
	var out []string
	for _, abs := range ManagedDestinationRoots(provider, "") {
		out = append(out, filepath.ToSlash(abs))
	}
	return out
}

// pathsCollide reports whether a and b are the same path or one lies under the
// other. Case is folded off Linux, as clientvariant.Resolve does: the default
// macOS and Windows file systems do not tell the spellings apart.
func pathsCollide(a, b string) bool {
	if runtime.GOOS != "linux" {
		a, b = strings.ToLower(a), strings.ToLower(b)
	}
	return a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}

// ValidateVariantCells refuses a variant whose global roots overlap the roots of
// a built-in provider or of another registered variant: the two would write into
// and prune the same directory. Project cells are exempt, as they already are
// among built-ins (several clients share .agents/skills and AGENTS.md); the
// project paths are relative and checked at write time. A variant is never
// compared with itself, so re-registering is not a collision.
func ValidateVariantCells(v clientvariant.Variant) error {
	cells, _ := variantDestinations(v)
	mine := variantRoots(cells[ScopeGlobal])
	check := func(owner string, theirs []string) error {
		for _, m := range mine {
			for _, t := range theirs {
				if pathsCollide(m, t) {
					return fmt.Errorf("variant %q: %q overlaps %q, which belongs to %s", v.Name, m, t, owner)
				}
			}
		}
		return nil
	}
	for _, d := range configurator.Providers() {
		if err := check(string(d.Provider), globalRootRels(d.Provider)); err != nil {
			return err
		}
	}
	variantCellsMu.RLock()
	defer variantCellsMu.RUnlock()
	names := make([]string, 0, len(variantCells))
	for p := range variantCells {
		if string(p) != v.Name {
			names = append(names, string(p))
		}
	}
	sort.Strings(names)
	for _, n := range names {
		if err := check("variant "+n, variantRoots(variantCells[configurator.Provider(n)].cells[ScopeGlobal])); err != nil {
			return err
		}
	}
	return nil
}

// variantBoundaries are the prune boundaries v's cells add: the first two
// segments of each cell's directory (a file cell's directory), so a prune never
// climbs into the user's own config directory (provisioningRootDirs, D63).
func variantBoundaries(cells map[Scope]map[string]destination) []string {
	seen := map[string]bool{}
	var out []string
	for _, byKind := range cells {
		for _, c := range byKind {
			dir := c.dir
			if !c.named && len(dir) > 0 {
				dir = dir[:len(dir)-1]
			}
			if len(dir) == 0 {
				continue
			}
			b := filepath.Join(dir[:min(len(dir), 2)]...)
			if !seen[b] {
				seen[b] = true
				out = append(out, b)
			}
		}
	}
	sort.Strings(out)
	return out
}

// RegisterVariantCells makes v's destination cells, agent format, capability row
// and prune boundaries visible to every destination lookup, after
// ValidateVariantCells has accepted them; nothing changes on a refusal. It
// returns the notes to show once (an ignored hook cell). Registering a name again
// replaces its entry.
func RegisterVariantCells(v clientvariant.Variant) ([]string, error) {
	if err := ValidateVariantCells(v); err != nil {
		return nil, err
	}
	cells, notes := variantDestinations(v)
	format := "claude"
	for _, sc := range []clientvariant.Scope{v.Global, v.Project} {
		if sc.Agent != nil {
			format = sc.Agent.AgentFormat
		}
	}
	variantCellsMu.Lock()
	defer variantCellsMu.Unlock()
	variantCells[configurator.Provider(v.Name)] = variantEntry{
		base: configurator.Provider(v.Base), agentFormat: format, cells: cells,
		capabilities: v.Capabilities, invokeHint: v.InvokeHint, roots: variantBoundaries(cells),
	}
	return notes, nil
}

// UnregisterVariantCells drops a variant's cells and boundaries.
func UnregisterVariantCells(name string) {
	variantCellsMu.Lock()
	defer variantCellsMu.Unlock()
	delete(variantCells, configurator.Provider(name))
}

// isVariantBoundary reports whether dir (OS separators, relative to a base
// directory) is a prune boundary a registered variant added.
func isVariantBoundary(dir string) bool {
	variantCellsMu.RLock()
	defer variantCellsMu.RUnlock()
	for _, v := range variantCells {
		for _, r := range v.roots {
			if dir == r {
				return true
			}
		}
	}
	return false
}

// variantCapability is CapabilityStatus for a variant: its declared value, else
// what the base's probes recorded, else unverified; forced absent where the
// variant has no cell for the kind in the scope. Hook capabilities are absent
// until the hook cell is enabled.
func variantCapability(v variantEntry, provider configurator.Provider, scope Scope, id Capability) CapStatus {
	var kind string
	switch id {
	case CapSkill, CapMCP:
		kind = string(id)
	case CapAgentDelegate, CapAgentInvoke:
		kind = "agent"
	default:
		return CapAbsent
	}
	if destDirScoped(kind, "x", provider, scope) == "" {
		return CapAbsent
	}
	if s, ok := v.capabilities[string(id)]; ok {
		return CapStatus(s)
	}
	if s, ok := capabilityProbed[v.base][scope][id]; ok {
		return s
	}
	return CapUnverified
}
