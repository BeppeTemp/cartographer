package provisioning

import (
	"fmt"
	"path/filepath"
	"sort"

	"github.com/BeppeTemp/cartographer/internal/configurator"
)

// workspacescope.go (D193) — the project-local half of the kind × provider
// matrix.
//
// D137 made the destinations data; this adds the third axis. A provider in
// workspace scope materializes a bound workspace's KB artifacts into that
// workspace's own project-local directories, and materializes **nothing**
// KB-sourced globally (decisions 3 and 5). Cartographer's own transversal
// bundled skills stay global (decision 4): they are not a perimeter's content.
//
// Sources for each cell were verified during the D193 audit and are external
// contracts that change outside this project's release cycle — re-verify before
// changing one, and keep the citation next to it.

// Scope selects which half of the matrix a lookup reads.
type Scope int

const (
	// ScopeGlobal is the historical destination set, under the user's home.
	ScopeGlobal Scope = iota
	// ScopeProject is the per-workspace destination set, under a bound
	// workspace's own directory.
	ScopeProject
)

// projectDestinationMatrix is destinationMatrix's project-local counterpart.
//
// Sources (D193 audit):
//   - Claude Code: .claude/skills/, .claude/agents/, .claude/settings.json,
//     .mcp.json, ./CLAUDE.md — https://code.claude.com/docs/en/skills,
//     /memory, /sub-agents, /hooks, /mcp.
//   - Codex: .agents/skills and .codex/{agents,hooks.json,config.toml} along
//     cwd→repository root — https://developers.openai.com/codex/skills,
//     /subagents, /hooks, /mcp. The project must be *trusted* or Codex ignores
//     the .codex/ layer entirely, which is why the projection reports itself
//     inactive there rather than claiming success (see CodexProjectTrusted).
//   - Kiro: .kiro/skills/ (workspace wins over global) —
//     https://kiro.dev/docs/skills/.
//   - OpenCode: .opencode/skills, .opencode/agent, .opencode/plugins, project
//     opencode.json and AGENTS.md — https://opencode.ai/docs/skills, /rules,
//     /agents, /plugins.
//
// Two providers have **no** project-local cells at all, and say so rather than
// pretending (decision 11): hermes renders its configuration from an Ansible
// role and delivers skills to an inbox with no per-directory notion, and
// antigravity documents only a global configuration root. A provider in
// workspace scope that cannot project is reported as
// "strict isolation unsupported", never silently degraded to the global
// catalogue — degrading is exactly the exposure this plan exists to prevent.
var projectDestinationMatrix = map[string]map[configurator.Provider]destination{
	"mcp": {
		configurator.ProviderClaudeCode: at(".mcp.json"),
		configurator.ProviderCodex:      at(".codex", "config.toml"),
		configurator.ProviderOpenCode:   at("opencode.json"),
		configurator.ProviderKiro:       at(".kiro", "settings", "mcp.json"),
		configurator.ProviderHermes:     unsupportedDest,
		// antigravity documents a single global configuration root
		// (~/.gemini/config); no project-local equivalent was found in the
		// D193 audit, so the cell fails closed rather than inventing a path.
		configurator.ProviderAntigravity: unsupportedDest,
	},
	"instructions": {
		// The project root's own CLAUDE.md is the file Claude Code reads for a
		// project; .claude/CLAUDE.md is the alternative. The root file is
		// chosen because it is the one a repository already has, and the block
		// is marker-delimited so it coexists with the user's own text.
		configurator.ProviderClaudeCode:  at("CLAUDE.md"),
		configurator.ProviderCodex:       at("AGENTS.md"),
		configurator.ProviderOpenCode:    at("AGENTS.md"),
		configurator.ProviderKiro:        at(".kiro", "steering", "cartographer.md"),
		configurator.ProviderHermes:      unsupportedDest,
		configurator.ProviderAntigravity: unsupportedDest,
	},
	"agent": {
		configurator.ProviderClaudeCode: perName(".md", ".claude", "agents"),
		configurator.ProviderCodex:      perName(".toml", ".codex", "agents"),
		configurator.ProviderOpenCode:   perName(".md", ".opencode", "agent"),
		// kiro: unchanged from the global cell, and for the same reason (D140):
		// its agents are top-level personas the user selects, not delegates a
		// main agent invokes. Kiro 3.0 may change that — #248 tracks the work
		// and is blocked on an empirical verification. Giving it a cell here
		// would be shipping that finding without its evidence.
		configurator.ProviderKiro:        unsupportedDest,
		configurator.ProviderHermes:      unsupportedDest,
		configurator.ProviderAntigravity: unsupportedDest,
	},
	"hook": {
		configurator.ProviderClaudeCode: perName("", ".claude", "hooks"),
		configurator.ProviderCodex:      perName("", ".codex", "hooks"),
		configurator.ProviderOpenCode:   perName("", ".opencode", "hooks"),
		// kiro: see the "agent" cell above — same D140 reasoning, same #248.
		configurator.ProviderKiro:        unsupportedDest,
		configurator.ProviderHermes:      unsupportedDest,
		configurator.ProviderAntigravity: unsupportedDest,
	},
	"skill": {
		configurator.ProviderClaudeCode: perName("", ".claude", "skills"),
		// The repository path Codex documents for skills. The global cell still
		// points at .codex/skills for the reason D192 records; this is the
		// target that only became reachable once a workspace scope existed.
		configurator.ProviderCodex:       perName("", ".agents", "skills"),
		configurator.ProviderKiro:        perName("", ".kiro", "skills"),
		configurator.ProviderOpenCode:    perName("", ".opencode", "skills"),
		configurator.ProviderHermes:      unsupportedDest,
		configurator.ProviderAntigravity: unsupportedDest,
	},
}

// matrixFor returns the destination matrix for a scope.
func matrixFor(scope Scope) map[string]map[configurator.Provider]destination {
	if scope == ScopeProject {
		return projectDestinationMatrix
	}
	return destinationMatrix
}

// destDirScoped is destDir with the scope axis. destDir is the ScopeGlobal
// case and keeps its signature, because every caller that has no workspace to
// speak of is asking exactly that question.
func destDirScoped(kind, name string, provider configurator.Provider, scope Scope) string {
	cell, ok := matrixFor(scope)[kind][provider]
	if !ok || cell.unsupported {
		return ""
	}
	if !cell.named {
		return filepath.Join(cell.dir...)
	}
	segments := append(append([]string{}, cell.dir...), name+cell.suffix)
	return filepath.Join(append(segments, cell.tail...)...)
}

// SupportsProjectScope reports whether provider can project any artifact kind
// into a workspace. False is an honest answer, not a degradation: a provider
// that cannot project must be reported as unsupported rather than quietly
// served from the global catalogue (decision 11).
func SupportsProjectScope(provider configurator.Provider) bool {
	for kind := range projectDestinationMatrix {
		if cell, ok := projectDestinationMatrix[kind][provider]; ok && !cell.unsupported {
			return true
		}
	}
	return false
}

// ProjectScopeUnsupportedReason explains, for a user, why a provider cannot be
// bound to a workspace. Empty when it can.
func ProjectScopeUnsupportedReason(provider configurator.Provider) string {
	if SupportsProjectScope(provider) {
		return ""
	}
	switch provider {
	case configurator.ProviderHermes:
		return "hermes has no per-directory configuration: its MCP endpoints and instructions are rendered by its own Ansible role, and skills are delivered to a single inbox its curator owns"
	case configurator.ProviderAntigravity:
		return "antigravity documents only a global configuration root (~/.gemini/config); no project-local scope was found"
	default:
		return "this provider declares no project-local destination"
	}
}

// ManagedProjectRoots is ManagedDestinationRoots for the project scope: the
// directories a workspace projection owns under workspaceDir. `doctor` checks
// them, and the repository-hygiene pass adds them to .git/info/exclude.
func ManagedProjectRoots(provider configurator.Provider, workspaceDir string) []string {
	seen := map[string]bool{}
	var out []string
	for kind := range projectDestinationMatrix {
		cell, ok := projectDestinationMatrix[kind][provider]
		if !ok || cell.unsupported {
			continue
		}
		rel := filepath.Join(cell.dir...)
		if rel == "" || rel == "." {
			continue
		}
		abs := filepath.Join(workspaceDir, rel)
		if seen[abs] {
			continue
		}
		seen[abs] = true
		out = append(out, abs)
	}
	sort.Strings(out)
	return out
}

// ProjectOwnedPaths returns the workspace-relative paths a projection owns for
// provider: the per-kind roots for directory destinations, and the exact file
// for a shared-file destination. It is what the repository-hygiene pass (WP5)
// writes to .git/info/exclude and what the collision check consults, so it must
// name everything the projection can create and nothing it cannot.
func ProjectOwnedPaths(provider configurator.Provider) []string {
	seen := map[string]bool{}
	var out []string
	for _, kind := range artifactKinds {
		cell, ok := projectDestinationMatrix[kind][provider]
		if !ok || cell.unsupported {
			continue
		}
		rel := filepath.Join(cell.dir...)
		if rel == "" || rel == "." || seen[rel] {
			continue
		}
		seen[rel] = true
		out = append(out, rel)
	}
	sort.Strings(out)
	return out
}

// validateProjectMatrix asserts the project matrix covers every kind for every
// provider, the same completeness rule D137 imposes on the global one: a cell
// missing by omission is indistinguishable from one unsupported on purpose,
// and only the second is a decision.
func validateProjectMatrix() error {
	for _, kind := range artifactKinds {
		row, ok := projectDestinationMatrix[kind]
		if !ok {
			return fmt.Errorf("provisioning: project destination matrix has no row for kind %q", kind)
		}
		for _, d := range configurator.Providers() {
			if _, ok := row[d.Provider]; !ok {
				return fmt.Errorf("provisioning: project destination matrix has no cell for %s × %s", kind, d.Provider)
			}
		}
	}
	return nil
}

// --- Lockfile: one projection, one applied state (D193 WP3) ---
//
// The lockfile used to be keyed by provider alone, which is exactly one
// projection per provider. With workspaces there are many, and the danger is
// not subtle: prune deletes every managed file the lock names, so a key that
// resolves to the wrong projection deletes another workspace's files.
//
// Workspace locks therefore live in their **own namespace**, not in an extended
// provider key space. A workspace lookup can never return the global
// projection's entries, and a global lookup can never return a workspace's,
// because they are different maps — the mistake is not merely unlikely, it is
// unrepresentable.

// WorkspaceLocks is one workspace's applied state, per provider.
type WorkspaceLocks struct {
	// Remote is the normalized git remote recorded when the workspace was
	// bound, carried here so a prune can refuse a directory that has become a
	// different repository since — the same guard the binding holds, checked
	// where the deletion actually happens.
	Remote string `json:"remote,omitempty"`
	// Providers is keyed by provider name, exactly as LockFile.Providers is,
	// and every Lock in it carries BaseDir = this workspace's path.
	Providers map[string]Lock `json:"providers"`
}

// Projection identifies one applied state: a provider, and the workspace it was
// applied into. An empty Workspace is the global projection — the historical
// one, and the only one a provider-scoped installation has.
type Projection struct {
	Provider  string
	Workspace string
}

// Global reports whether p is the global projection.
func (p Projection) Global() bool { return p.Workspace == "" }

// String renders a projection for a user-facing message.
func (p Projection) String() string {
	if p.Global() {
		return p.Provider + " (global)"
	}
	return p.Provider + " in " + p.Workspace
}

// ForProjection returns the Lock for one projection (zero value if absent).
func (lf LockFile) ForProjection(p Projection) Lock {
	if p.Global() {
		return lf.ForProvider(p.Provider)
	}
	ws, ok := lf.Workspaces[p.Workspace]
	if !ok {
		return Lock{}
	}
	return ws.Providers[p.Provider]
}

// SetProjection updates (or adds) one projection's Lock. A workspace lock always
// records BaseDir: pruning or verifying against the wrong base dir deletes the
// wrong files, and for a workspace the base dir is never the lockfile's own
// directory.
func (lf *LockFile) SetProjection(p Projection, lock Lock, remote string) {
	if p.Global() {
		lf.SetProvider(p.Provider, lock)
		return
	}
	if lf.Workspaces == nil {
		lf.Workspaces = map[string]WorkspaceLocks{}
	}
	ws, ok := lf.Workspaces[p.Workspace]
	if !ok {
		ws = WorkspaceLocks{Providers: map[string]Lock{}}
	}
	if ws.Providers == nil {
		ws.Providers = map[string]Lock{}
	}
	if remote != "" {
		ws.Remote = remote
	}
	lock.Provider = p.Provider
	lock.BaseDir = p.Workspace
	lock.ProjectScope = true
	ws.Providers[p.Provider] = lock
	lf.Workspaces[p.Workspace] = ws
}

// RemoveProjection drops one projection's applied state, and the workspace
// entry itself once its last provider is gone — so an unbound workspace leaves
// no residue to be matched later.
func (lf *LockFile) RemoveProjection(p Projection) {
	if p.Global() {
		delete(lf.Providers, p.Provider)
		return
	}
	ws, ok := lf.Workspaces[p.Workspace]
	if !ok {
		return
	}
	delete(ws.Providers, p.Provider)
	if len(ws.Providers) == 0 {
		delete(lf.Workspaces, p.Workspace)
		return
	}
	lf.Workspaces[p.Workspace] = ws
}

// Projections lists every applied state the lockfile holds, global first and
// then workspaces, each in a deterministic order. `status`, `doctor` and the
// prune path iterate this rather than reaching into either map.
func (lf LockFile) Projections() []Projection {
	var out []Projection
	for provider := range lf.Providers {
		out = append(out, Projection{Provider: provider})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Provider < out[j].Provider })

	workspaces := make([]string, 0, len(lf.Workspaces))
	for ws := range lf.Workspaces {
		workspaces = append(workspaces, ws)
	}
	sort.Strings(workspaces)
	for _, ws := range workspaces {
		providers := make([]string, 0, len(lf.Workspaces[ws].Providers))
		for provider := range lf.Workspaces[ws].Providers {
			providers = append(providers, provider)
		}
		sort.Strings(providers)
		for _, provider := range providers {
			out = append(out, Projection{Provider: provider, Workspace: ws})
		}
	}
	return out
}

// WorkspaceProjections lists the projections of one workspace only. This is the
// set a sync of that workspace may touch, and nothing outside it: a prune that
// iterated more than this is the defect WP3 exists to make impossible.
func (lf LockFile) WorkspaceProjections(workspace string) []Projection {
	var out []Projection
	for _, p := range lf.Projections() {
		if p.Workspace == workspace {
			out = append(out, p)
		}
	}
	return out
}

// WorkspaceRemote returns the git remote recorded for a workspace's applied
// state, empty when there is none.
func (lf LockFile) WorkspaceRemote(workspace string) string {
	return lf.Workspaces[workspace].Remote
}

// ProjectDestination is destDirScoped's project half, exported for the client,
// which needs to know which paths a projection owns before it writes any.
func ProjectDestination(kind, name string, provider configurator.Provider) string {
	return destDirScoped(kind, name, provider, ScopeProject)
}

// FilterForProviderScoped is FilterForProvider with the scope axis: a kind the
// provider cannot materialize **in this scope** is not missing, it simply does
// not apply here.
func FilterForProviderScoped(m Manifest, provider configurator.Provider, scope Scope) Manifest {
	if scope == ScopeGlobal {
		return FilterForProvider(m, provider)
	}
	var out Manifest
	for _, a := range m.Artifacts {
		if destDirScoped(a.Kind, a.Name, provider, scope) != "" {
			out.Artifacts = append(out.Artifacts, a)
		}
	}
	out.Revision = computeRevision(out.Artifacts)
	return out
}
