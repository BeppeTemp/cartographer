package provisioning

import (
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
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

// SupportsProjectScope reports whether provider can project any artifact kind
// into a workspace. False is an honest answer, not a degradation: a provider
// that cannot project must be reported as unsupported rather than quietly
// served from the global catalogue (decision 11).
func SupportsProjectScope(provider configurator.Provider) bool {
	for _, kind := range artifactKinds {
		if cell, ok := contract.CellFor(contract.ScopeProject, kind, provider); ok && !cell.Unsupported {
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

// projectSharedHookFiles are the hook registrations that a projection patches
// into a file of its own beside the hook directories, and that the client
// dedicates to hooks: Codex's hooks.json holds hooks and nothing else (D230).
// Unlike projectRegistrationFiles a repository may track such a file, and then
// it is the repository's: ProjectSharedHookFiles is how the client tells it
// from the files it owns whole, so it neither refuses nor excludes a tracked one.
// Claude Code's settings.json is not here: it carries the user's own settings,
// so excluding it would hide them.
var projectSharedHookFiles = map[configurator.Provider][]string{
	configurator.ProviderCodex: {".codex/hooks.json"},
}

// ProjectSharedHookFiles returns the hook registration files (slash paths, relative
// to the workspace) a projection of provider patches without owning them whole.
func ProjectSharedHookFiles(provider configurator.Provider) []string {
	return append([]string(nil), projectSharedHookFiles[provider]...)
}

// projectHookPlugin is the generated registration file of hook name in a
// workspace, "" when the provider has none or cannot hold a hook there.
func projectHookPlugin(provider configurator.Provider, name string) string {
	if contract.DestDirScoped("hook", name, provider, contract.ScopeProject) == "" {
		return ""
	}
	return HookPluginRelPath(provider, name, contract.ScopeProject)
}

// ProjectRegistrationFiles returns the registration files a projection of
// provider writes beside its directories.
func ProjectRegistrationFiles(provider configurator.Provider) []string {
	return append([]string(nil), projectRegistrationFiles[provider]...)
}

// ManagedProjectRoots is ManagedDestinationRoots for the project scope: the
// directories a workspace projection owns under workspaceDir. `doctor` checks
// them, and the repository-hygiene pass adds them to .git/info/exclude.
func ManagedProjectRoots(provider configurator.Provider, workspaceDir string) []string {
	seen := map[string]bool{}
	var out []string
	for _, kind := range artifactKinds {
		cell, ok := contract.CellFor(contract.ScopeProject, kind, provider)
		if !ok || cell.Unsupported {
			continue
		}
		rel := path.Join(cell.Dir...)
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

// ProjectOwnedArtifactPaths returns the workspace-relative paths a projection
// of provider owns: what it writes, not the directories it writes into (D1181).
//
// In a directory destination (skills, agents, hooks) that is
// <dir>/<name><suffix> for each artifact of m the provider receives in this
// scope, for each client-generated hook, and for each path lock records (what
// an earlier run wrote and a prune may remove). A repository's own artifact
// next to those is not owned and is neither refused nor excluded. A
// shared-file destination (.mcp.json, the instructions file) and a
// registration file are owned as a whole, as before.
//
// The result is written into .git/info/exclude before Apply checks anything,
// so it is validated here: an artifact name that is not a single local segment,
// or that holds a newline or a gitignore special character, fails the
// projection with the error Apply gives for it, and a lock path outside the
// provider's own destinations is dropped. Nothing is written either way.
func ProjectOwnedArtifactPaths(provider configurator.Provider, m contract.Manifest, lock Lock) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	var roots []string
	for _, kind := range artifactKinds {
		cell, ok := contract.CellFor(contract.ScopeProject, kind, provider)
		if !ok || cell.Unsupported {
			continue
		}
		if rel := path.Join(cell.Dir...); rel != "" && rel != "." {
			roots = append(roots, rel)
		}
	}
	roots = append(roots, projectRegistrationFiles[provider]...)
	// The hook companions sit outside the hook directories (Codex's hooks.json,
	// OpenCode's generated plugins): they are written by the same projection, so
	// they are as much its own as the hook's files (#1199).
	if contract.DestDirScoped("hook", "x", provider, contract.ScopeProject) != "" {
		roots = append(roots, projectSharedHookFiles[provider]...)
		if plugin := HookPluginRelPath(provider, "x", contract.ScopeProject); plugin != "" {
			roots = append(roots, path.Dir(plugin))
		}
	}
	contained := func(rel string) bool {
		for _, r := range roots {
			if rel == r || strings.HasPrefix(rel, r+"/") {
				return true
			}
		}
		return false
	}
	add := func(rel string) {
		rel = path.Clean(filepath.ToSlash(rel))
		if rel == "" || rel == "." || seen[rel] || strings.ContainsAny(rel, "\n\r") || !contained(rel) {
			return
		}
		seen[rel] = true
		out = append(out, rel)
	}
	for _, kind := range artifactKinds {
		cell, ok := contract.CellFor(contract.ScopeProject, kind, provider)
		if !ok || cell.Unsupported {
			continue
		}
		if !cell.Named {
			add(path.Join(cell.Dir...))
			continue
		}
		if kind == "hook" {
			for name := range contract.ReservedHookNames {
				add(contract.DestDirScoped(kind, name, provider, contract.ScopeProject))
				add(projectHookPlugin(provider, name))
			}
		}
	}
	// The shared hook file is owned only once a hook is registered in it: the
	// projection carries one, or the lock records one. Before that it is the
	// user's, and excluding it would hide it.
	projected, _ := ProjectManifest(m, provider, contract.ScopeProject, contract.ProjectOptions{})
	if contract.DestDirScoped("hook", "x", provider, contract.ScopeProject) != "" && hasHook(projected, lock) {
		for _, rel := range projectSharedHookFiles[provider] {
			add(rel)
		}
	}
	// The projection, not the filter: a degraded skill owns a destination too,
	// so the exclusion block and the prune include it.
	for _, a := range projected.Artifacts {
		if !ownableName(a.Name) {
			return nil, &InvalidArtifactNameError{Name: a.Name}
		}
		add(contract.DestDirScoped(a.Kind, a.Name, provider, contract.ScopeProject))
		if a.Kind == "hook" {
			add(projectHookPlugin(provider, a.Name))
		}
	}
	for _, mf := range lock.Managed {
		add(mf.Path)
	}
	for _, rel := range projectRegistrationFiles[provider] {
		add(rel)
	}
	sort.Strings(out)
	return out, nil
}

// hasHook reports whether m carries a hook or lock records one.
func hasHook(m contract.Manifest, lock Lock) bool {
	for _, a := range m.Artifacts {
		if a.Kind == "hook" {
			return true
		}
	}
	for _, mf := range lock.Managed {
		if mf.Kind == "hook" {
			return true
		}
	}
	return false
}

// InvalidArtifactNameError is the refusal of a name that cannot be projected
// into a workspace: stricter than Apply's rule, because the name is also
// written into .git/info/exclude.
type InvalidArtifactNameError struct{ Name string }

func (e *InvalidArtifactNameError) Error() string {
	return fmt.Sprintf("provisioning: artifact name %q cannot be projected into a workspace (separators, line breaks or gitignore metacharacters)", e.Name)
}

// ownableName is the rule Apply holds an artifact name to (a single local
// segment) plus what a path written into .git/info/exclude must not carry: a
// line break, a gitignore glob or negation, a comment, or edge spaces.
func ownableName(name string) bool {
	if !contract.ValidArtifactName(name) {
		return false
	}
	if strings.ContainsAny(name, "\n\r*?[!\\#") || strings.TrimSpace(name) != name {
		return false
	}
	return true
}

// validateProjectMatrix asserts the project matrix covers every kind for every
// provider, the same completeness rule D137 imposes on the global one: a cell
// missing by omission is indistinguishable from one unsupported on purpose,
// and only the second is a decision.
func validateProjectMatrix() error {
	for _, kind := range artifactKinds {
		row, ok := contract.ProjectDestinationMatrix[kind]
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
	// A lockfile written by an older client may carry a "remote" key here: it
	// is ignored on read and dropped on the next write. The guard against a
	// directory that became another repository is the binding's remote
	// (clientconfig.CheckWorkspaceRemote) and the hash verification in
	// disconnect.
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
func (lf *LockFile) SetProjection(p Projection, lock Lock) {
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

// ProjectDestination is DestDirScoped's project half, exported for the client,
// which needs to know which paths a projection owns before it writes any.
func ProjectDestination(kind, name string, provider configurator.Provider) string {
	return contract.DestDirScoped(kind, name, provider, contract.ScopeProject)
}

// ClaudeLocalInstructions is the untracked file Claude Code reads beside
// CLAUDE.md. A derived workspace binding (D1019) puts its instructions block
// there when the repository tracks CLAUDE.md, so no tracked file is edited.
const ClaudeLocalInstructions = "CLAUDE.local.md"

// OwnedLocalInstructions returns the workspace-relative local instructions
// file Cartographer wrote into workspaceDir for provider, to be excluded from
// git: ClaudeLocalInstructions when it holds a managed block, otherwise none.
// The block itself is the record, so the answer holds for a projection this
// run does not touch.
func OwnedLocalInstructions(workspaceDir string, provider configurator.Provider) []string {
	if provider != configurator.ProviderClaudeCode {
		return nil
	}
	begins, _, err := InstructionsBlockMarkers(filepath.Join(workspaceDir, ClaudeLocalInstructions))
	if err != nil || begins == 0 {
		return nil
	}
	return []string{ClaudeLocalInstructions}
}

// FilterForProviderScoped is FilterForProvider with the scope axis: a kind the
// provider cannot materialize **in this scope** is not missing, it simply does
// not apply here. It never degrades an agent to a skill: kept for API
// stability, the sync and status paths use ProjectManifest.
func FilterForProviderScoped(m contract.Manifest, provider configurator.Provider, scope contract.Scope) contract.Manifest {
	if scope == contract.ScopeGlobal {
		return FilterForProvider(m, provider)
	}
	var out contract.Manifest
	for _, a := range m.Artifacts {
		if contract.DestDirScoped(a.Kind, a.Name, provider, scope) != "" && !contract.AgentSkipped(a, provider) {
			out.Artifacts = append(out.Artifacts, a)
		}
	}
	out.Revision = contract.ComputeRevision(out.Artifacts)
	// Skipped carries the warnings VerifiedManifest left for Apply to
	// print; a real sync applies the filtered manifest, so they must survive.
	out.Skipped = m.Skipped
	return out
}
