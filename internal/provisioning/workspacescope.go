package provisioning

import (
	"fmt"
	"path"
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
//
//   - Claude Code: .claude/skills/, .claude/agents/, .claude/settings.json,
//     .mcp.json, ./CLAUDE.md — https://code.claude.com/docs/en/skills,
//     /memory, /sub-agents, /hooks, /mcp.
//
//   - Codex: .agents/skills and .codex/{agents,hooks.json,config.toml} along
//     cwd→repository root — https://developers.openai.com/codex/skills,
//     /subagents, /hooks, /mcp. The project must be *trusted* or Codex ignores
//     the .codex/ layer entirely, which is why the projection reports itself
//     inactive there rather than claiming success (see CodexProjectTrusted).
//
//   - Kiro: .kiro/skills/ (workspace wins over global) —
//     https://kiro.dev/docs/skills/.
//
//   - OpenCode: .opencode/skills, .opencode/agent, .opencode/plugins, project
//     opencode.json and AGENTS.md — https://opencode.ai/docs/skills, /rules,
//     /agents, /plugins.
//
//   - Crush: .crush/skills, one of the project skill directories it scans by
//     default — https://github.com/charmbracelet/crush/tree/main/docs/config.
//     Its project `.crush.json` was probed on v0.98.0 (D363): its `mcp` and
//     `hooks` entries are used by the session, so the mcp and hook cells are
//     supported (hooks live in .crush/hooks/, registered in .crush.json). The
//     agent and instructions cells are not probed and still fail closed
//     (docs/harnesses.md, ## crush).
//
//   - Antigravity (D364, `agy` 1.3.2): the workspace `.agents/` layer — skills,
//     agents, hooks.json, mcp_config.json — plus AGENTS.md, all probed against a
//     negative control. Its rules directory is deliberately not used: a rule is
//     read only with `trigger: always_on` frontmatter, which is why the
//     instructions cell is the AGENTS.md block instead.
//
//   - Hermes (D364, v0.21.5): `.hermes/skills` only, and it loads only after the
//     workspace is listed in `skills.trusted_project_dirs` (HermesProjectTrusted).
//     Its config.yaml and gateway hooks stay operator-owned (D141), so the other
//     kinds remain unsupported.
//
// A provider in workspace scope that cannot project a kind reports it as
// unsupported, never silently degraded to the global catalogue — degrading is
// exactly the exposure this plan exists to prevent.
var projectDestinationMatrix = map[string]map[configurator.Provider]destination{
	"mcp": {
		configurator.ProviderClaudeCode: at(".mcp.json"),
		configurator.ProviderCodex:      at(".codex", "config.toml"),
		configurator.ProviderOpenCode:   at("opencode.json"),
		configurator.ProviderKiro:       at(".kiro", "settings", "mcp.json"),
		configurator.ProviderHermes:     unsupportedDest,
		// antigravity: the global shape (`mcpServers`) in the workspace's
		// .agents/mcp_config.json; `agy mcp list` shows only the global servers
		// but the session has the project ones (probed on 1.3.2, D364).
		configurator.ProviderAntigravity: at(".agents", "mcp_config.json"),
		configurator.ProviderCrush:       at(".crush.json"),
	},
	"instructions": {
		// The project root's own CLAUDE.md is the file Claude Code reads for a
		// project; .claude/CLAUDE.md is the alternative. The root file is
		// chosen because it is the one a repository already has, and the block
		// is marker-delimited so it coexists with the user's own text.
		// D293: creating this file hides a project's AGENTS.md from the
		// built-in agents-md@builtin plugin (on by default since 2.1.277),
		// so the managed block prepends an @AGENTS.md import when one exists.
		configurator.ProviderClaudeCode: at("CLAUDE.md"),
		configurator.ProviderCodex:      at("AGENTS.md"),
		configurator.ProviderOpenCode:   at("AGENTS.md"),
		configurator.ProviderKiro:       at(".kiro", "steering", "cartographer.md"),
		configurator.ProviderHermes:     unsupportedDest,
		// antigravity: AGENTS.md, read by `agy` (D364). Not .agents/rules/: a
		// rule is read only with `trigger: always_on` in its frontmatter, so a
		// plain rule file would be written and silently ignored.
		configurator.ProviderAntigravity: at("AGENTS.md"),
		configurator.ProviderCrush:       unsupportedDest,
	},
	"agent": {
		configurator.ProviderClaudeCode: perName(".md", ".claude", "agents"),
		configurator.ProviderCodex:      perName(".toml", ".codex", "agents"),
		configurator.ProviderOpenCode:   perName(".md", ".opencode", "agent"),
		// kiro: the same JSON agent config as the global cell, in the
		// workspace's own agent directory, which `kiro-cli agent list` reports
		// as "Workspace" and which wins over the global one (D195).
		configurator.ProviderKiro:        perName(".json", ".kiro", "agents"),
		configurator.ProviderHermes:      unsupportedDest,
		configurator.ProviderAntigravity: perName(".md", ".agents", "agents"),
		configurator.ProviderCrush:       unsupportedDest,
	},
	"hook": {
		configurator.ProviderClaudeCode: perName("", ".claude", "hooks"),
		configurator.ProviderCodex:      perName("", ".codex", "hooks"),
		configurator.ProviderOpenCode:   perName("", ".opencode", "hooks"),
		// kiro: a workspace .kiro/hooks/ fires in exactly the sessions the
		// global ~/.kiro/hooks/ does, `kiro-cli chat --v3 --tui` (probed on
		// 2.26.1), so a project-local copy of the global registration (D300)
		// would reach no session the global one misses. Kept unsupported until
		// a mode fires one and not the other.
		configurator.ProviderKiro:   unsupportedDest,
		configurator.ProviderHermes: unsupportedDest,
		// antigravity: the files live in .agents/hooks/<n>/ and the entry in
		// .agents/hooks.json (same schema as the global file); the registration
		// file is picked from the hook's path (antigravitySettingsRel).
		configurator.ProviderAntigravity: perName("", ".agents", "hooks"),
		configurator.ProviderCrush:       perName("", ".crush", "hooks"),
	},
	"skill": {
		configurator.ProviderClaudeCode: perName("", ".claude", "skills"),
		// The repository path Codex documents for skills. The global cell still
		// points at .codex/skills for the reason D192 records; this is the
		// target that only became reachable once a workspace scope existed.
		configurator.ProviderCodex:    perName("", ".agents", "skills"),
		configurator.ProviderKiro:     perName("", ".kiro", "skills"),
		configurator.ProviderOpenCode: perName("", ".opencode", "skills"),
		// hermes: loaded only once the workspace is in skills.trusted_project_dirs
		// (HermesProjectTrusted); .hermes/skills rather than .agents/skills, which
		// Codex and Antigravity already project into (D364). Written, not
		// delivered to the inbox: a project skill has no curator to fight.
		configurator.ProviderHermes: perName("", ".hermes", "skills"),
		// The .agents/skills directory `agy` scans in a workspace, shared with
		// Codex: both write the same bytes, and a prune keeps what another
		// projection of the workspace still records (ApplyOptions.CoOwnedPaths).
		configurator.ProviderAntigravity: perName("", ".agents", "skills"),
		configurator.ProviderCrush:       perName("", ".crush", "skills"),
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
	// A destination is a slash path, not a host path: it is written into
	// provider configuration and into .git/info/exclude, both of which are
	// read on every platform and both of which want forward slashes.
	if !cell.named {
		return path.Join(cell.dir...)
	}
	segments := append(append([]string{}, cell.dir...), name+cell.suffix)
	return path.Join(append(segments, cell.tail...)...)
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
	return "this provider declares no project-local destination"
}

// projectRegistrationFiles are files a projection writes into without a cell
// of their own: the registration an artifact kind needs beside its directory.
// They are owned (excluded from git, checked for collisions, listed by doctor)
// exactly like a cell path.
var projectRegistrationFiles = map[configurator.Provider][]string{
	// The hook entries (D364); the same schema as the global hooks.json.
	configurator.ProviderAntigravity: {antigravityProjectHooksRel},
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
		rel := path.Join(cell.dir...)
		if rel == "" || rel == "." {
			continue
		}
		abs := filepath.Join(workspaceDir, filepath.FromSlash(rel))
		if seen[abs] {
			continue
		}
		seen[abs] = true
		out = append(out, abs)
	}
	for _, rel := range projectRegistrationFiles[provider] {
		abs := filepath.Join(workspaceDir, filepath.FromSlash(rel))
		if !seen[abs] {
			seen[abs] = true
			out = append(out, abs)
		}
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
		rel := path.Join(cell.dir...)
		if rel == "" || rel == "." || seen[rel] {
			continue
		}
		seen[rel] = true
		out = append(out, rel)
	}
	for _, rel := range projectRegistrationFiles[provider] {
		if !seen[rel] {
			seen[rel] = true
			out = append(out, rel)
		}
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

// CoOwnedPaths returns the managed paths (slash form) that projections of p's
// workspace other than p itself still record (D364). Two providers can project
// the same path into one workspace — `.agents/skills/<n>/` for Codex and
// Antigravity, the AGENTS.md block for Codex, OpenCode and Antigravity — and
// write it identically, so one of them leaving must not delete what the other
// still depends on. Empty for the global projection, which shares nothing.
func (lf LockFile) CoOwnedPaths(p Projection) map[string]bool {
	if p.Global() {
		return nil
	}
	out := map[string]bool{}
	for provider, lock := range lf.Workspaces[p.Workspace].Providers {
		if provider == p.Provider {
			continue
		}
		for _, mf := range lock.Managed {
			out[filepath.ToSlash(mf.Path)] = true
		}
	}
	return out
}

// WithoutCoOwned drops from managed every file whose path is in coOwned: the
// files a prune must leave because another projection still records them.
func WithoutCoOwned(managed []ManagedFile, coOwned map[string]bool) []ManagedFile {
	if len(coOwned) == 0 {
		return managed
	}
	out := make([]ManagedFile, 0, len(managed))
	for _, mf := range managed {
		if !coOwned[filepath.ToSlash(mf.Path)] {
			out = append(out, mf)
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
		if destDirScoped(a.Kind, a.Name, provider, scope) != "" && !strictAgentSkipped(a, provider) {
			out.Artifacts = append(out.Artifacts, a)
		}
	}
	out.Revision = computeRevision(out.Artifacts)
	return out
}
