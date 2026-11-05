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
	// Only mode file writes an MCP entry: the cli and seed modes have no mcp cell.
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

// fold lowercases a path off Linux, where the default file systems do not tell
// the spellings apart.
func fold(s string) string {
	if runtime.GOOS != "linux" {
		return strings.ToLower(s)
	}
	return s
}

// clientDir is the directory a global path belongs to a client: the first
// segment under the home, or the first two where one directory per client sits
// under a shared parent (.config, Documents), or the first three under the
// per-user data roots (.local/share, Library/Application Support, AppData/Local).
func clientDir(segs []string) []string {
	n := 1
	if len(segs) > 0 {
		switch strings.ToLower(segs[0]) {
		case ".config", "documents":
			n = 2
		case ".local", "library", "appdata":
			n = 3
		}
	}
	return segs[:min(n, len(segs))]
}

// The denylists below are defence in depth, not the control: a variant is
// applied only after the explicit, hash-bound approval of its descriptor, which
// shows every resolved cell path (client variants PR 5). They name the places
// where a write is code execution or credentials theft the user would not
// think to look for in a client's own directory. Matching is segment-wise and
// case-insensitive on every OS, after anchor resolution.
var (
	// deniedGlobalPrefixes: a global cell path starting with one of these
	// segment lists is refused (home-relative, so .config/git is ~/.config/git).
	deniedGlobalPrefixes = [][]string{
		{".ssh"}, {".gnupg"}, {".aws"}, {".kube"}, {".docker"}, {"desktop"},
		{"documents", "powershell"}, {"documents", "windowspowershell"},
		{"appdata", "roaming", "microsoft"}, {"library", "launchagents"}, {"library", "launchdaemons"},
		{".config", "git"}, {".config", "autostart"}, {".config", "systemd"}, {".config", "environment.d"},
		{".config", "fish"}, {".config", "powershell"}, {".config", "nushell"},
		{".oh-my-zsh"}, {".bashrc.d"}, {".zshrc.d"}, {".profile.d"}, {".config.d"},
		{".cargo"}, {".npm-global"}, {".local", "bin"},
	}
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

// deniedClientDir reports whether a global path is one no variant may write
// under: the lists above, git configuration (.git*), and Cartographer's own
// state and config (a client directory whose last segment starts with
// cartographer).
func deniedClientDir(segs, dir []string) bool {
	if len(dir) == 0 {
		return true
	}
	for _, p := range deniedGlobalPrefixes {
		if hasPrefixFold(segs, p) {
			return true
		}
	}
	first, last := strings.ToLower(dir[0]), strings.ToLower(dir[len(dir)-1])
	return strings.HasPrefix(first, ".git") || strings.HasPrefix(last, ".cartographer") || strings.HasPrefix(last, "cartographer")
}

// checkGlobalCells refuses a global cell outside a client directory of its own:
// one that sits directly in the home (an rc file), in a directory of a built-in
// client or a denied one (settings, hooks, credentials).
func checkGlobalCells(name string, cells map[string]destination) error {
	builtin := map[string]string{}
	for _, d := range configurator.Providers() {
		for _, r := range globalRootRels(d.Provider) {
			builtin[fold(strings.Join(clientDir(strings.Split(r, "/")), "/"))] = string(d.Provider)
		}
	}
	kinds := make([]string, 0, len(cells))
	for k := range cells {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	for _, kind := range kinds {
		segs := cells[kind].dir
		cd := clientDir(segs)
		switch {
		case len(segs) <= len(cd):
			return fmt.Errorf("variant %q: the %s cell %q names no path below its client directory (a cell is never a file or directory directly in the home or in a shared parent)", name, kind, strings.Join(segs, "/"))
		case deniedClientDir(segs, cd):
			return fmt.Errorf("variant %q: the %s cell %q is under %q, a directory no variant may write into", name, kind, strings.Join(segs, "/"), strings.Join(cd, "/"))
		}
		if owner, ok := builtin[fold(strings.Join(cd, "/"))]; ok {
			return fmt.Errorf("variant %q: the %s cell %q is under %q, the client directory of %s", name, kind, strings.Join(segs, "/"), strings.Join(cd, "/"), owner)
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
			return fmt.Errorf("variant %q: the project %s cell %q reaches %q, which a repository runs code from", name, kind, strings.Join(c.dir, "/"), denied)
		}
	}
	return nil
}

// ValidateVariantCells refuses a variant that could write outside a client
// directory of its own: a global cell directly in the home or under a built-in
// client's directory or a denied one (checkGlobalCells), a project cell that
// reaches .git, CI workflows or .husky (checkProjectCells), and a global root
// that overlaps the roots of a built-in provider or of another registered
// variant (the two would write into and prune the same directory). Project
// cells are otherwise exempt from the overlap rule, as among built-ins (several
// clients share .agents/skills and AGENTS.md). A variant is never compared with
// itself, so re-registering is not a collision. Containment under the home is
// the resolver's (clientvariant.Resolve) and safewrite's at write time.
func ValidateVariantCells(v clientvariant.Variant) error {
	cells, _ := variantDestinations(v)
	if err := checkGlobalCells(v.Name, cells[ScopeGlobal]); err != nil {
		return err
	}
	if err := checkProjectCells(v.Name, cells[ScopeProject]); err != nil {
		return err
	}
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
			if fold(dir) == fold(r) {
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
// to, "" for a base whose agent file is no format a variant may declare.
func baseAgentFormat(base configurator.Provider) string {
	switch base {
	case configurator.ProviderClaudeCode:
		return "claude"
	case configurator.ProviderOpenCode, configurator.ProviderAntigravity, configurator.ProviderCopilot, configurator.ProviderCline:
		return string(base)
	}
	return ""
}

// sameMCPFile reports whether the variant's mcp cell is the base's file.
func sameMCPFile(v variantEntry, scope Scope) bool {
	cell, ok := v.cells[scope]["mcp"]
	d, found := configurator.Lookup(v.base)
	return ok && found && path.Join(cell.dir...) == d.MCPConfigPath
}
