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
)

// A plugin provider is a runtime provider whose destination cells are data a
// plugin declared (providers/<id>.yaml, internal/plugin), not rows of the static
// matrices. The static matrices stay built-in only: the completeness tests range
// over them and a plugin provider is outside them by construction. Every read of
// a cell goes through cellFor, which answers for both.
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
	MCPFile      []string
	Capabilities map[string]string
	InvokeHint   string
}

// pluginEntry is the provisioning view of one registered plugin provider.
type pluginEntry struct {
	base         configurator.Provider
	agentFormat  string
	active       bool
	cells        map[Scope]map[string]destination
	capabilities map[string]string
	invokeHint   string
	// roots are the prune boundaries the cells add (pluginBoundaries).
	roots []string
}

var (
	// pluginRegMu serialises the registrations (validate against the registered
	// set, then swap): two colliding providers registered at once cannot both be
	// accepted. pluginCellsMu guards the map and is held only briefly, because
	// the validation reads cells through cellFor, which takes its read lock.
	pluginRegMu   sync.Mutex
	pluginCellsMu sync.RWMutex
	pluginCells   = map[configurator.Provider]pluginEntry{}
)

// pluginCell is the plugin half of cellFor.
func pluginCell(scope Scope, kind string, provider configurator.Provider) (destination, bool) {
	pluginCellsMu.RLock()
	defer pluginCellsMu.RUnlock()
	if v, ok := pluginCells[provider]; ok {
		if cell, ok := v.cells[scope][kind]; ok {
			return cell, true
		}
	}
	return unsupportedDest, false
}

// pluginFor returns the registered plugin provider.
func pluginFor(provider configurator.Provider) (pluginEntry, bool) {
	pluginCellsMu.RLock()
	defer pluginCellsMu.RUnlock()
	v, ok := pluginCells[provider]
	return v, ok
}

// nativeProvider is the id an agent's `providers.<id>` frontmatter block is read
// under: a plugin provider honours its base's block.
func nativeProvider(provider configurator.Provider) configurator.Provider {
	if v, ok := pluginFor(provider); ok {
		return v.base
	}
	return provider
}

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
func pluginDestinations(c PluginCells) (map[Scope]map[string]destination, []string) {
	cells := map[Scope]map[string]destination{ScopeGlobal: {}, ScopeProject: {}}
	for scope, sc := range map[Scope]PluginScopeCells{ScopeGlobal: c.Global, ScopeProject: c.Project} {
		if sc.Skill != nil {
			cells[scope]["skill"] = perName("", sc.Skill.Dir...)
		}
		if sc.Agent != nil {
			cells[scope]["agent"] = perName(pluginAgentSuffix(sc.Agent.AgentFormat), sc.Agent.Dir...)
		}
		if sc.Instructions != nil {
			cells[scope]["instructions"] = at(sc.Instructions.File...)
		}
	}
	if c.MCPFile != nil {
		cells[ScopeGlobal]["mcp"] = at(c.MCPFile...)
	}
	var notes []string
	if c.Global.Hook != nil {
		if hookMechanisms[configurator.Provider(c.Base)].sharedDispatcher {
			cells[ScopeGlobal]["hook"] = perName("", c.Global.Hook.Dir...)
		} else {
			notes = append(notes, fmt.Sprintf("%s: hook cell ignored: the base %s wires hooks with no shared dispatcher a fork's directory could join", c.Name, c.Base))
		}
	}
	return cells, notes
}

// pluginRoots lists the global-scope directory roots a plugin provider's cells
// own, as slash paths relative to the home, the way ManagedDestinationRoots does.
func pluginRoots(cells map[string]destination) []string {
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
func checkGlobalCells(name string, cells map[string]destination) error {
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
		segs := cells[kind].dir
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
func checkProjectCells(name string, cells map[string]destination) error {
	for kind, c := range cells {
		denied := ""
		for i, seg := range c.dir {
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
			if hasPrefixFold(c.dir, p) {
				denied = strings.Join(p, "/")
			}
		}
		if denied != "" {
			return fmt.Errorf("plugin provider %q: the project %s cell %q reaches %q, which a repository runs code from", name, kind, strings.Join(c.dir, "/"), denied)
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
func checkDispatcherCells(name, home string, cells map[string]destination) error {
	for kind, c := range cells {
		p := path.Join(c.dir...)
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
// resolver's (plugin.ResolveProvider) and safewrite's at write time.
func validatePluginCells(c PluginCells, home string, others map[configurator.Provider]pluginEntry) error {
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
	if err := checkGlobalCells(c.Name, cells[ScopeGlobal]); err != nil {
		return err
	}
	if err := checkProjectCells(c.Name, cells[ScopeProject]); err != nil {
		return err
	}
	if err := checkDispatcherCells(c.Name, home, cells[ScopeGlobal]); err != nil {
		return err
	}
	mine := pluginRoots(cells[ScopeGlobal])
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
	for _, d := range configurator.Providers() {
		if err := check(string(d.Provider), globalRootRels(d.Provider)); err != nil {
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
		if err := check("plugin provider "+n, pluginRoots(others[configurator.Provider(n)].cells[ScopeGlobal])); err != nil {
			return err
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
func pluginBoundaries(cells map[Scope]map[string]destination) []string {
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

func buildPluginEntry(c PluginCells, home string, others map[configurator.Provider]pluginEntry) (pluginEntry, []string, error) {
	if err := validatePluginCells(c, home, others); err != nil {
		return pluginEntry{}, nil, err
	}
	cells, notes := pluginDestinations(c)
	format := "claude"
	for _, sc := range []PluginScopeCells{c.Global, c.Project} {
		if sc.Agent != nil {
			format = sc.Agent.AgentFormat
		}
	}
	return pluginEntry{
		base: configurator.Provider(c.Base), agentFormat: format, active: c.Active, cells: cells,
		capabilities: c.Capabilities, invokeHint: c.InvokeHint, roots: pluginBoundaries(cells),
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
	// built-ins through cellFor, which takes the read lock.
	pluginCellsMu.RLock()
	others := make(map[configurator.Provider]pluginEntry, len(pluginCells))
	for k, v := range pluginCells {
		others[k] = v
	}
	pluginCellsMu.RUnlock()
	e, notes, err := buildPluginEntry(c, home, others)
	if err != nil {
		return nil, err
	}
	pluginCellsMu.Lock()
	pluginCells[configurator.Provider(c.Name)] = e
	pluginCellsMu.Unlock()
	return notes, nil
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
	cand := map[configurator.Provider]pluginEntry{}
	for _, c := range cs {
		if e, _, err := buildPluginEntry(c, home, nil); err == nil {
			cand[configurator.Provider(c.Name)] = e
		}
	}
	next := map[configurator.Provider]pluginEntry{}
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
	pluginCellsMu.Lock()
	pluginCells = next
	pluginCellsMu.Unlock()
	return notes, refused
}

// UnregisterPluginCells drops a plugin provider's cells and boundaries.
func UnregisterPluginCells(name string) {
	pluginRegMu.Lock()
	defer pluginRegMu.Unlock()
	pluginCellsMu.Lock()
	defer pluginCellsMu.Unlock()
	delete(pluginCells, configurator.Provider(name))
}

// isPluginBoundary reports whether dir (OS separators, relative to a base
// directory) is a prune boundary a registered plugin provider added.
func isPluginBoundary(dir string) bool {
	pluginCellsMu.RLock()
	defer pluginCellsMu.RUnlock()
	for _, v := range pluginCells {
		for _, r := range v.roots {
			if fold(dir) == fold(r) {
				return true
			}
		}
	}
	return false
}

// pluginCapability is CapabilityStatus for a plugin provider: its declared value,
// else what the base's probes recorded, else unverified; forced absent where the
// provider has no cell for the kind in the scope. Hook capabilities stay absent:
// no probe of a fork's hooks exists, and the base's rows say nothing about them.
func pluginCapability(v pluginEntry, provider configurator.Provider, scope Scope, id Capability) CapStatus {
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
	// A probe of the base says nothing about a different agent file format or MCP
	// file: only the skill row is inherited across such a difference.
	if (kind == "agent" && v.agentFormat != baseAgentFormat(v.base)) || (kind == "mcp" && !sameMCPFile(v, scope)) {
		return CapUnverified
	}
	if s, ok := capabilityProbed[v.base][scope][id]; ok {
		return s
	}
	return CapUnverified
}

// baseAgentFormat is the agent.format a built-in's own agent file corresponds
// to, "" for a base whose agent file is no format a plugin provider may declare.
func baseAgentFormat(base configurator.Provider) string {
	switch base {
	case configurator.ProviderClaudeCode:
		return "claude"
	case configurator.ProviderOpenCode, configurator.ProviderAntigravity, configurator.ProviderCopilot, configurator.ProviderCline:
		return string(base)
	}
	return ""
}

// sameMCPFile reports whether the provider's mcp cell is the base's file.
func sameMCPFile(v pluginEntry, scope Scope) bool {
	cell, ok := v.cells[scope]["mcp"]
	d, found := configurator.Lookup(v.base)
	return ok && found && path.Join(cell.dir...) == d.MCPConfigPath
}

// pluginHookRoots returns the home-relative slash hook directories of the ACTIVE
// plugin providers whose base is base and that declared a hook cell, sorted. It
// is derived from the registry, never stored: a provider that leaves the registry
// or whose plugin is no longer active (retraction, disable) leaves the set at
// once, even with its hook files still on disk.
func pluginHookRoots(base configurator.Provider) []string {
	pluginCellsMu.RLock()
	defer pluginCellsMu.RUnlock()
	var out []string
	for _, v := range pluginCells {
		if cell, ok := v.cells[ScopeGlobal]["hook"]; ok && v.base == base && v.active {
			out = append(out, path.Join(cell.dir...))
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
	pluginCellsMu.RLock()
	defer pluginCellsMu.RUnlock()
	for _, v := range pluginCells {
		if cell, ok := v.cells[ScopeGlobal]["hook"]; ok && strings.HasPrefix(fold(p), fold(path.Join(cell.dir...))+"/") {
			return string(v.base)
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
	v, ok := pluginFor(provider)
	if !ok {
		return hookMechanism{}, false
	}
	if _, has := v.cells[ScopeGlobal]["hook"]; !has {
		return hookMechanism{}, false
	}
	m, ok := hookMechanisms[v.base]
	return m, ok
}
