package provisioning

import (
	"fmt"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
)

// A plugin provider is a runtime provider whose destination cells are data a
// plugin declared (providers/<id>.yaml, internal/plugin), not rows of the static
// matrices. The static matrices stay built-in only: the completeness tests range
// over them and a plugin provider is outside them by construction. Every read of
// a cell goes through CellFor, which answers for both.
//
// This package cannot import internal/plugin (which imports it): the caller
// hands over PluginCells, plain data built from a validated and resolved
// descriptor (plugin.ResolvedProvider.Cells).

// PluginCell is one destination of a plugin provider: path segments relative to
// the home directory (global) or to a project root (project). Dir for a skill,
// an agent or a hook directory, File for the instructions file.
type PluginCell struct {
	Dir, File []string
	// AgentFormat is the agent file layout (claude, opencode, antigravity, copilot,
	// cline); agent only.
	AgentFormat string
}

// PluginScopeCells is the cells of one scope. Hook is global only.
type PluginScopeCells struct{ Skill, Agent, Instructions, Hook *PluginCell }

// PluginCells is what provisioning needs to know about one plugin provider.
type PluginCells struct {
	Name, Base string
	// Active is false for a provider of a plugin that left the active set: its
	// cells stay resolvable so what it installed can be retired, but its hook
	// directory is no longer a root of the base's dispatcher.
	Active          bool
	Global, Project PluginScopeCells
	// MCPFile is the provider's own literal MCP file, home-relative segments;
	// nil when it writes the base's file or one that holds a `*` (those have no
	// "mcp" cell: the KB's MCP descriptors are not written into a file the
	// provider shares with its base or into every match of a pattern).
	MCPFile []string
	// MCPStale are the home-relative files the client derives from its MCP file
	// and regenerates; removed after a changed write. They are no cell, but may
	// lie outside the directory of the MCP file, so they obey the overlap rule.
	MCPStale     [][]string
	Capabilities map[string]string
	InvokeHint   string
}

var (
	// pluginRegMu serialises the registrations (validate against the registered
	// set, then swap): two colliding providers registered at once cannot both be
	// accepted. PluginRegistryMu guards the map and is held only briefly, because
	// the validation reads cells through CellFor, which takes its read lock.
	pluginRegMu sync.Mutex
)

// pluginAgentSuffix is the file suffix of each agent.format.
func pluginAgentSuffix(format string) string {
	switch format {
	case "copilot":
		return ".agent.md"
	case "cline":
		return ".yaml"
	}
	return ".md"
}

// pluginDestinations builds the cells of c. The hook cell is built only when the
// base wires hooks through the shared dispatcher (hookMechanism.sharedDispatcher):
// a hook destination without a registrar would copy the plugin's hook files with
// nothing wiring them, so for any other base the cell is dropped and the
// returned note says why.
func pluginDestinations(c PluginCells) (map[contract.Scope]map[string]contract.Destination, []string) {
	cells := map[contract.Scope]map[string]contract.Destination{contract.ScopeGlobal: {}, contract.ScopeProject: {}}
	for scope, sc := range map[contract.Scope]PluginScopeCells{contract.ScopeGlobal: c.Global, contract.ScopeProject: c.Project} {
		if sc.Skill != nil {
			cells[scope]["skill"] = contract.PerName("", sc.Skill.Dir...)
		}
		if sc.Agent != nil {
			cells[scope]["agent"] = contract.PerName(pluginAgentSuffix(sc.Agent.AgentFormat), sc.Agent.Dir...)
		}
		if sc.Instructions != nil {
			cells[scope]["instructions"] = contract.At(sc.Instructions.File...)
		}
	}
	if c.MCPFile != nil {
		cells[contract.ScopeGlobal]["mcp"] = contract.At(c.MCPFile...)
	}
	var notes []string
	if c.Global.Hook != nil {
		if hookMechanisms[configurator.Provider(c.Base)].sharedDispatcher {
			cells[contract.ScopeGlobal]["hook"] = contract.PerName("", c.Global.Hook.Dir...)
		} else {
			notes = append(notes, fmt.Sprintf("%s: hook cell ignored: the base %s wires hooks with no shared dispatcher a fork's directory could join", c.Name, c.Base))
		}
	}
	return cells, notes
}

// staleRels turns stale files into slash paths cut before the first `*`.
func staleRels(stale [][]string) []string {
	out := make([]string, 0, len(stale))
	for _, s := range stale {
		fixed := s
		for i, seg := range s {
			if strings.Contains(seg, "*") {
				fixed = s[:i]
				break
			}
		}
		out = append(out, path.Join(fixed...))
	}
	return out
}

// pluginRoots lists the global-scope directory roots a plugin provider's cells
// own, as slash paths relative to the home, the way ManagedDestinationRoots does.
func pluginRoots(cells map[string]contract.Destination) []string {
	var out []string
	for _, c := range cells {
		if rel := path.Join(c.Dir...); rel != "" && rel != "." {
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
// other. Case is folded off Linux: the default macOS and Windows file systems do
// not tell the spellings apart.
func pathsCollide(a, b string) bool {
	a, b = fold(a), fold(b)
	return a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}

// fold lowercases a path off Linux, where the default file systems do not tell
// the spellings apart.
func fold(s string) string {
	if runtime.GOOS != "linux" {
		return strings.ToLower(s)
	}
	return s
}

// The project denylists are defence in depth, not the control: a plugin provider
// is applied only after the explicit, digest-bound approval of its plugin, whose
// preview prints every resolved cell path. The global ones live in
// configurator (DeniedClientDir). Matching is segment-wise and case-insensitive
// on every OS.
var (
	// deniedProjectFirst: a project cell whose first segment is one of these
	// (a file or a directory) is refused; .git is refused at any depth.
	deniedProjectFirst = []string{
		".envrc", ".pre-commit-config.yaml", ".lefthook.yml", "lefthook.yml", ".gitlab-ci.yml", ".gitattributes", ".gitmodules",
		".circleci", ".githooks", ".devcontainer", ".vscode", ".idea", ".husky",
	}
	deniedProjectPrefixes = [][]string{{".github", "workflows"}, {".github", "actions"}}
)

func hasPrefixFold(segs, prefix []string) bool {
	if len(segs) < len(prefix) {
		return false
	}
	for i, p := range prefix {
		if !strings.EqualFold(segs[i], p) {
			return false
		}
	}
	return true
}

// checkGlobalCells refuses a global cell outside a client directory of its own:
// one that sits directly in the home (an rc file), in a directory of a built-in
// client or a denied one (settings, hooks, credentials).
func checkGlobalCells(name string, cells map[string]contract.Destination) error {
	builtin := map[string]string{}
	for _, d := range configurator.Providers() {
		for _, r := range globalRootRels(d.Provider) {
			builtin[fold(strings.Join(configurator.ClientDir(strings.Split(r, "/")), "/"))] = string(d.Provider)
		}
	}
	kinds := make([]string, 0, len(cells))
	for k := range cells {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	for _, kind := range kinds {
		segs := cells[kind].Dir
		cd := configurator.ClientDir(segs)
		switch {
		case len(segs) <= len(cd):
			return fmt.Errorf("plugin provider %q: the %s cell %q names no path below its client directory (a cell is never a file or directory directly in the home or in a shared parent)", name, kind, strings.Join(segs, "/"))
		case configurator.DeniedClientDir(segs, cd):
			return fmt.Errorf("plugin provider %q: the %s cell %q is under %q, a directory no plugin provider may write into", name, kind, strings.Join(segs, "/"), strings.Join(cd, "/"))
		}
		if owner, ok := builtin[fold(strings.Join(cd, "/"))]; ok {
			return fmt.Errorf("plugin provider %q: the %s cell %q is under %q, the client directory of %s", name, kind, strings.Join(segs, "/"), strings.Join(cd, "/"), owner)
		}
	}
	return nil
}

// checkProjectCells refuses a project cell that reaches a place a repository
// executes or configures itself from: any .git directory, CI workflows, hooks
// managers.
func checkProjectCells(name string, cells map[string]contract.Destination) error {
	for kind, c := range cells {
		denied := ""
		for i, seg := range c.Dir {
			if strings.EqualFold(seg, ".git") {
				denied = seg
			}
			if i == 0 {
				for _, d := range deniedProjectFirst {
					if strings.EqualFold(seg, d) {
						denied = seg
					}
				}
			}
		}
		for _, p := range deniedProjectPrefixes {
			if hasPrefixFold(c.Dir, p) {
				denied = strings.Join(p, "/")
			}
		}
		if denied != "" {
			return fmt.Errorf("plugin provider %q: the project %s cell %q reaches %q, which a repository runs code from", name, kind, strings.Join(c.Dir, "/"), denied)
		}
	}
	return nil
}

// dispatcherDirRels are the home-relative slash paths of the folder holding
// Cline's shared hook dispatchers: the literal one, and the redirected Documents
// folder of a Windows home when it lies under it (clineDocumentsDir, OneDrive).
func dispatcherDirRels(home string) []string {
	out := []string{"Documents/Cline/Hooks"}
	if home == "" {
		return out
	}
	if rel, err := filepath.Rel(home, filepath.Join(clineDocumentsDir(home), "Cline", "Hooks")); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
		if rel = filepath.ToSlash(rel); !strings.EqualFold(rel, out[0]) {
			out = append(out, rel)
		}
	}
	return out
}

// checkDispatcherCells refuses a global cell of any kind that reaches the folder
// of the shared dispatchers: a file there named like an event is run by Cline as
// a hook, so a skill, instructions or mcp cell would turn plugin content into
// code. A hook cell is also kept out of the rest of Documents/Cline, Cline's own.
func checkDispatcherCells(name, home string, cells map[string]contract.Destination) error {
	for kind, c := range cells {
		p := path.Join(c.Dir...)
		for _, d := range dispatcherDirRels(home) {
			if pathsCollide(p, d) {
				return fmt.Errorf("plugin provider %q: the %s cell %q overlaps %s, where the shared hook dispatchers live", name, kind, p, d)
			}
		}
		if kind == "hook" && pathsCollide(p, "Documents/Cline") {
			return fmt.Errorf("plugin provider %q: the hook cell %q overlaps Documents/Cline, Cline's own folder", name, p)
		}
	}
	return nil
}

// validatePluginCells refuses a provider that could write outside a client
// directory of its own: a global cell directly in the home or under a built-in
// client's directory or a denied one (checkGlobalCells), a project cell that
// reaches .git, CI workflows or .husky (checkProjectCells), a cell on the
// dispatcher folder, two agent formats, and a global root that overlaps the roots
// of a built-in provider or of any of others (the two would write into and prune
// the same directory). Project cells are otherwise exempt from the overlap rule,
// as among built-ins (several clients share .agents/skills and AGENTS.md). A
// provider is never compared with itself. Containment under the home is the
// resolver's (core.ResolveProvider) and safewrite's at write time.
func validatePluginCells(c PluginCells, home string, others map[configurator.Provider]contract.PluginEntry) error {
	if c.Name == "" {
		return fmt.Errorf("plugin provider: empty name")
	}
	if isBuiltinProvider(c.Name) {
		return fmt.Errorf("plugin provider %q: the name is a built-in provider", c.Name)
	}
	if g, p := c.Global.Agent, c.Project.Agent; g != nil && p != nil && g.AgentFormat != p.AgentFormat {
		return fmt.Errorf("plugin provider %q: the agent format differs between the global (%s) and the project (%s) scope: one provider writes one layout", c.Name, g.AgentFormat, p.AgentFormat)
	}
	cells, _ := pluginDestinations(c)
	if err := checkGlobalCells(c.Name, cells[contract.ScopeGlobal]); err != nil {
		return err
	}
	if err := checkProjectCells(c.Name, cells[contract.ScopeProject]); err != nil {
		return err
	}
	if err := checkDispatcherCells(c.Name, home, cells[contract.ScopeGlobal]); err != nil {
		return err
	}
	mine := pluginRoots(cells[contract.ScopeGlobal])
	check := func(owner string, theirs []string) error {
		for _, m := range mine {
			for _, t := range theirs {
				if pathsCollide(m, t) {
					return fmt.Errorf("plugin provider %q: %q overlaps %q, which belongs to %s", c.Name, m, t, owner)
				}
			}
		}
		return nil
	}
	// A stale file is deleted, so it must not lie in, contain or equal a root of
	// a built-in or of another provider either (configurator checks it against the
	// client directories; the roots, which include a plugin's, are known here).
	stale := staleRels(c.MCPStale)
	checkStale := func(owner string, theirs []string) error {
		for _, m := range stale {
			for _, t := range theirs {
				if m != "" && pathsCollide(m, t) {
					return fmt.Errorf("plugin provider %q: mcp.stale %q overlaps %q, which belongs to %s", c.Name, m, t, owner)
				}
			}
		}
		return nil
	}
	for _, d := range configurator.Providers() {
		if err := check(string(d.Provider), globalRootRels(d.Provider)); err != nil {
			return err
		}
		if err := checkStale(string(d.Provider), globalRootRels(d.Provider)); err != nil {
			return err
		}
	}
	names := make([]string, 0, len(others))
	for p := range others {
		if string(p) != c.Name {
			names = append(names, string(p))
		}
	}
	sort.Strings(names)
	for _, n := range names {
		theirs := pluginRoots(others[configurator.Provider(n)].Cells[contract.ScopeGlobal])
		if err := check("plugin provider "+n, theirs); err != nil {
			return err
		}
		if err := checkStale("plugin provider "+n, theirs); err != nil {
			return err
		}
		// The other direction: my roots against their stale files.
		for _, m := range mine {
			for _, t := range others[configurator.Provider(n)].Stale {
				if t != "" && pathsCollide(m, t) {
					return fmt.Errorf("plugin provider %q: %q overlaps the mcp.stale file %q of plugin provider %s", c.Name, m, t, n)
				}
			}
		}
	}
	return nil
}

func isBuiltinProvider(name string) bool {
	for _, d := range configurator.Providers() {
		if string(d.Provider) == name {
			return true
		}
	}
	return false
}

// pluginBoundaries are the prune boundaries c's cells add: the first two
// segments of each cell's directory (a file cell's directory), so a prune never
// climbs into the user's own config directory (provisioningRootDirs, D63).
func pluginBoundaries(cells map[contract.Scope]map[string]contract.Destination) []string {
	seen := map[string]bool{}
	var out []string
	for _, byKind := range cells {
		for _, c := range byKind {
			dir := c.Dir
			if !c.Named && len(dir) > 0 {
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

func buildPluginEntry(c PluginCells, home string, others map[configurator.Provider]contract.PluginEntry) (contract.PluginEntry, []string, error) {
	if err := validatePluginCells(c, home, others); err != nil {
		return contract.PluginEntry{}, nil, err
	}
	cells, notes := pluginDestinations(c)
	format := "claude"
	for _, sc := range []PluginScopeCells{c.Global, c.Project} {
		if sc.Agent != nil {
			format = sc.Agent.AgentFormat
		}
	}
	return contract.PluginEntry{
		Base: configurator.Provider(c.Base), AgentFormat: format, Active: c.Active, Cells: cells,
		Capabilities: c.Capabilities, InvokeHint: c.InvokeHint, Roots: pluginBoundaries(cells), Stale: staleRels(c.MCPStale),
	}, notes, nil
}

// RegisterPluginCells makes c's destination cells, agent format, capability row
// and prune boundaries visible to every destination lookup, after
// validatePluginCells has accepted them against the built-ins and the providers
// already registered; nothing changes on a refusal. home is the user's home
// directory (the dispatcher folder is looked up under it). It returns the notes
// to show once (an ignored hook cell). Registering a name again replaces its
// entry.
func RegisterPluginCells(c PluginCells, home string) ([]string, error) {
	pluginRegMu.Lock()
	defer pluginRegMu.Unlock()
	// Validated on a copy, outside the map lock: the checks read the cells of the
	// built-ins through CellFor, which takes the read lock.
	contract.PluginRegistryMu.RLock()
	others := make(map[configurator.Provider]contract.PluginEntry, len(contract.PluginRegistry))
	for k, v := range contract.PluginRegistry {
		others[k] = v
	}
	contract.PluginRegistryMu.RUnlock()
	e, notes, err := buildPluginEntry(c, home, others)
	if err != nil {
		return nil, err
	}
	contract.PluginRegistryMu.Lock()
	contract.PluginRegistry[configurator.Provider(c.Name)] = e
	contract.PluginRegistryMu.Unlock()
	return notes, nil
}

// CheckPluginCells reports whether RegisterPluginCells would accept c against
// the built-ins (the dry run of `plugin validate`); it registers nothing and does
// not look at the providers already registered.
func CheckPluginCells(c PluginCells, home string) error {
	_, _, err := buildPluginEntry(c, home, nil)
	return err
}

// PluginCellsRefusal names a provider ReplacePluginCells left out and why.
type PluginCellsRefusal struct {
	Name string
	Err  error
}

// ReplacePluginCells swaps the whole registered set for cs in one step, so a
// concurrent lookup never sees it half built. Each provider is validated against
// the built-ins and against every other candidate: two that collide are both
// refused, whatever their order, and every other provider still registers. The
// refusals come back sorted by name, with the notes of the ones registered.
func ReplacePluginCells(cs []PluginCells, home string) (notes []string, refused []PluginCellsRefusal) {
	pluginRegMu.Lock()
	defer pluginRegMu.Unlock()
	cand := map[configurator.Provider]contract.PluginEntry{}
	for _, c := range cs {
		if e, _, err := buildPluginEntry(c, home, nil); err == nil {
			cand[configurator.Provider(c.Name)] = e
		}
	}
	next := map[configurator.Provider]contract.PluginEntry{}
	for _, c := range cs {
		// Against every other candidate, not only the ones accepted before it.
		e, n, err := buildPluginEntry(c, home, cand)
		if err != nil {
			refused = append(refused, PluginCellsRefusal{c.Name, err})
			continue
		}
		next[configurator.Provider(c.Name)] = e
		notes = append(notes, n...)
	}
	sort.Slice(refused, func(i, j int) bool { return refused[i].Name < refused[j].Name })
	contract.PluginRegistryMu.Lock()
	contract.PluginRegistry = next
	contract.PluginRegistryMu.Unlock()
	return notes, refused
}

// UnregisterPluginCells drops a plugin provider's cells and boundaries.
func UnregisterPluginCells(name string) {
	pluginRegMu.Lock()
	defer pluginRegMu.Unlock()
	contract.PluginRegistryMu.Lock()
	defer contract.PluginRegistryMu.Unlock()
	delete(contract.PluginRegistry, configurator.Provider(name))
}

// isPluginBoundary reports whether dir (OS separators, relative to a base
// directory) is a prune boundary a registered plugin provider added.
func isPluginBoundary(dir string) bool {
	contract.PluginRegistryMu.RLock()
	defer contract.PluginRegistryMu.RUnlock()
	for _, v := range contract.PluginRegistry {
		for _, r := range v.Roots {
			if fold(dir) == fold(r) {
				return true
			}
		}
	}
	return false
}

// pluginHookRoots returns the home-relative slash hook directories of the ACTIVE
// plugin providers whose base is base and that declared a hook cell, sorted. It
// is derived from the registry, never stored: a provider that leaves the registry
// or whose plugin is no longer active (retraction, disable) leaves the set at
// once, even with its hook files still on disk.
func pluginHookRoots(base configurator.Provider) []string {
	contract.PluginRegistryMu.RLock()
	defer contract.PluginRegistryMu.RUnlock()
	var out []string
	for _, v := range contract.PluginRegistry {
		if cell, ok := v.Cells[contract.ScopeGlobal]["hook"]; ok && v.Base == base && v.Active {
			out = append(out, path.Join(cell.Dir...))
		}
	}
	sort.Strings(out)
	return out
}

// pluginHookOwner returns the base provider whose registrar wires the hook files
// at slash path p, when p lies under the hook directory of a registered plugin
// provider, active or not ("" otherwise): a plugin provider has no registrar of
// its own, and what an inactive one installed must still be unwired.
func pluginHookOwner(p string) string {
	contract.PluginRegistryMu.RLock()
	defer contract.PluginRegistryMu.RUnlock()
	for _, v := range contract.PluginRegistry {
		if cell, ok := v.Cells[contract.ScopeGlobal]["hook"]; ok && strings.HasPrefix(fold(p), fold(path.Join(cell.Dir...))+"/") {
			return string(v.Base)
		}
	}
	return ""
}

// mechanismFor is the one read of a provider's hook mechanism: the table for a
// built-in, the BASE's for a plugin provider with a hook cell (it registers
// nothing itself: its hook directory is one more root of the base's shared
// dispatcher), none for a plugin provider without one.
func mechanismFor(provider configurator.Provider) (hookMechanism, bool) {
	if m, ok := hookMechanisms[provider]; ok {
		return m, true
	}
	v, ok := contract.PluginFor(provider)
	if !ok {
		return hookMechanism{}, false
	}
	if _, has := v.Cells[contract.ScopeGlobal]["hook"]; !has {
		return hookMechanism{}, false
	}
	m, ok := hookMechanisms[v.Base]
	return m, ok
}
