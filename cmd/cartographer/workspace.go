package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// workspace.go (D193) — resolving a provider's projections, and the
// `cartographer workspace` subcommand.
//
// A provider in the default scope has exactly one projection: the global
// catalogue under $HOME, which is what every release before D193 had. A
// provider in workspace scope has one projection per bound workspace, plus a
// global one that carries **only** Cartographer's own transversal bundled
// skills — never a KB's content (decisions 4 and 5).

// syncProjection is one applied state to compute and materialize: a provider,
// where it writes, and which KBs it may receive there.
type syncProjection struct {
	Provider string
	// Workspace is the canonical path of the bound workspace, empty for the
	// global projection.
	Workspace string
	// BaseDir is where artifacts are materialized: the client base dir for the
	// global projection, the workspace path otherwise.
	BaseDir string
	// Remote is the workspace's recorded git remote guard, empty when there is
	// none or for the global projection.
	Remote string
	// KBs are the KBs this projection may receive. Empty is a legal, explicit
	// state and never means "every KB".
	KBs []string
	// BundleOnly marks the residual global projection of a workspace-scoped
	// provider: it carries the transversal bundled skills and nothing
	// KB-sourced. This is decision 5 made into a flag rather than a convention.
	BundleOnly bool
	// Scope is the destination matrix half this projection writes through.
	Scope provisioning.Scope
}

// Key is the lockfile projection this state is recorded under.
func (p syncProjection) Key() provisioning.Projection {
	return provisioning.Projection{Provider: p.Provider, Workspace: p.Workspace}
}

// Label names the projection for a user-facing line.
func (p syncProjection) Label() string {
	if p.Workspace == "" {
		return p.Provider
	}
	return p.Provider + " [" + p.Workspace + "]"
}

// projectionsFor resolves every projection a provider currently has.
//
// In the default scope this is exactly one entry, identical in every respect to
// what the code did before workspaces existed. In workspace scope it is one
// entry per bound workspace plus the bundle-only global one; a provider that
// cannot project into a workspace at all is an error naming the reason, never a
// silent fall back to the global catalogue — falling back is the exposure this
// plan closes.
func projectionsFor(cfg *clientconfig.Config, provider, clientBaseDir string) ([]syncProjection, error) {
	if cfg.WorkspaceScope(provider) != clientconfig.ScopeWorkspace {
		bound, _ := cfg.BoundKBs(provider)
		return []syncProjection{{
			Provider: provider,
			BaseDir:  clientBaseDir,
			KBs:      bound,
			Scope:    provisioning.ScopeGlobal,
		}}, nil
	}

	p := configurator.Provider(provider)
	if !provisioning.SupportsProjectScope(p) {
		return nil, fmt.Errorf("%s cannot be scoped to a workspace: %s — leave it in the default scope, or run it under a separate profile",
			provider, provisioning.ProjectScopeUnsupportedReason(p))
	}

	// The residual global projection: transversal bundle only. It exists so an
	// upgrade to workspace scope does not take `cartographer-ops` and its
	// siblings away from a session that is not in any bound workspace.
	out := []syncProjection{{
		Provider:   provider,
		BaseDir:    clientBaseDir,
		BundleOnly: true,
		Scope:      provisioning.ScopeGlobal,
	}}

	bindings := cfg.WorkspaceBindings(provider)
	sort.Slice(bindings, func(i, j int) bool { return bindings[i].Path < bindings[j].Path })
	for _, w := range bindings {
		if info, err := os.Stat(w.Path); err != nil || !info.IsDir() {
			return nil, fmt.Errorf("%s: bound workspace %s is gone — rebind it, or remove the binding with `cartographer workspace unbind %s %s`",
				provider, w.Path, provider, w.Path)
		}
		out = append(out, syncProjection{
			Provider:  provider,
			Workspace: w.Path,
			BaseDir:   w.Path,
			Remote:    w.Remote,
			KBs:       w.KBs,
			Scope:     provisioning.ScopeProject,
		})
	}
	return out, nil
}

// artifactsForProjection narrows the fetched candidates to what one projection
// may receive. This is WP4's scoping: the strict collision check downstream then
// answers "do two KBs collide **here**", which is the only place a collision can
// actually confuse an agent. Two KBs owning a same-named skill in two different
// workspaces is legal; the same two bound to one workspace is not.
func (cs candidateSet) forProjection(cfg *clientconfig.Config, p syncProjection) []provisioning.Artifact {
	if p.BundleOnly {
		out := make([]provisioning.Artifact, 0, len(cs.Bare))
		for _, a := range cs.all() {
			if a.Source == "bundle" {
				out = append(out, a)
			}
		}
		return out
	}
	if p.Workspace == "" {
		return cs.forProvider(cfg, p.Provider)
	}
	if cs.HasBare {
		// No KB names to match: a workspace binding cannot be expressed against
		// a server that does not identify its KBs, so the projection receives
		// nothing rather than everything. Fail closed (decision 8).
		return nil
	}
	// KB artifacts only. Cartographer's own transversal bundled skills belong
	// to no perimeter and stay in the global catalogue (decision 4); copying
	// them into every workspace would multiply them by the number of bindings
	// and put `cartographer-ops` inside a repository that has nothing to do
	// with it.
	permitted := make(map[string]bool, len(p.KBs))
	for _, kb := range p.KBs {
		permitted["kb:"+kb] = true
	}
	var out []provisioning.Artifact
	for _, a := range cs.forKBs(p.KBs) {
		if permitted[a.Source] {
			out = append(out, a)
		}
	}
	return out
}

// workspaceKBUnion is boundKBUnion's workspace half: every KB any bound
// workspace of any listed provider asks for. A KB no workspace is bound to is
// never fetched.
func workspaceKBUnion(cfg *clientconfig.Config, providers []string) []string {
	seen := map[string]bool{}
	var names []string
	for _, provider := range providers {
		if cfg.WorkspaceScope(provider) != clientconfig.ScopeWorkspace {
			continue
		}
		for _, w := range cfg.WorkspaceBindings(provider) {
			for _, kb := range w.KBs {
				if !seen[kb] {
					seen[kb] = true
					names = append(names, kb)
				}
			}
		}
	}
	sort.Strings(names)
	return names
}

// --- `cartographer workspace` ---

func cmdWorkspace(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, workspaceUsage)
		return 2
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "bind":
		return cmdWorkspaceBind(rest)
	case "unbind":
		return cmdWorkspaceUnbind(rest)
	case "list":
		return cmdWorkspaceList(rest)
	default:
		fmt.Fprintln(os.Stderr, workspaceUsage)
		return 2
	}
}

const workspaceUsage = `usage: cartographer workspace bind <provider> <path> --kb <name>[,<name>...]
       cartographer workspace bind <provider> <path> --no-kb
       cartographer workspace unbind <provider> <path>
       cartographer workspace list [provider]`

// loadWorkspaceConfig is the shared prologue: the config dir and the config.
func loadWorkspaceConfig() (string, *clientconfig.Config, error) {
	dir, err := clientconfig.TargetDir()
	if err != nil {
		return "", nil, err
	}
	cfg, err := clientconfig.Load(dir)
	if err != nil {
		return "", nil, fmt.Errorf("no client config found in %s (run `cartographer connect` first): %w", dir, err)
	}
	return dir, cfg, nil
}

func cmdWorkspaceBind(args []string) int {
	var provider, path string
	var kbs []string
	noKB := false
	positional := make([]string, 0, 2)
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--no-kb":
			noKB = true
		case args[i] == "--kb" && i+1 < len(args):
			i++
			kbs = append(kbs, splitCommaList([]string{args[i]})...)
		case strings.HasPrefix(args[i], "--kb="):
			kbs = append(kbs, splitCommaList([]string{strings.TrimPrefix(args[i], "--kb=")})...)
		case strings.HasPrefix(args[i], "-"):
			fmt.Fprintf(os.Stderr, "Error: unknown flag %q\n%s\n", args[i], workspaceUsage)
			return 2
		default:
			positional = append(positional, args[i])
		}
	}
	if len(positional) != 2 {
		fmt.Fprintln(os.Stderr, workspaceUsage)
		return 2
	}
	provider, path = positional[0], positional[1]
	kbs = nonEmpty(kbs)
	if len(kbs) == 0 && !noKB {
		// Silence here would mean "every KB", which is precisely what a
		// workspace binding exists to stop. Make the empty case explicit.
		fmt.Fprintf(os.Stderr, "Error: say which KBs this workspace receives with --kb, or --no-kb for none.\n"+
			"A workspace binding is never implicitly 'every KB' — that is the exposure it exists to prevent.\n")
		return 2
	}
	if noKB && len(kbs) > 0 {
		fmt.Fprintln(os.Stderr, "Error: --no-kb and --kb are mutually exclusive")
		return 2
	}

	dir, cfg, err := loadWorkspaceConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 2
	}
	p := configurator.Provider(provider)
	if _, ok := configurator.Lookup(p); !ok {
		fmt.Fprintf(os.Stderr, "Error: unknown provider %q\n", provider)
		return 2
	}
	if !provisioning.SupportsProjectScope(p) {
		fmt.Fprintf(os.Stderr, "Error: %s cannot be scoped to a workspace: %s\n",
			provider, provisioning.ProjectScopeUnsupportedReason(p))
		return 2
	}
	if err := cfg.BindWorkspace(provider, path, kbs); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 2
	}
	switched := cfg.WorkspaceScope(provider) != clientconfig.ScopeWorkspace
	if switched {
		if err := cfg.SetWorkspaceScope(provider, clientconfig.ScopeWorkspace); err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			return 2
		}
	}
	if err := clientconfig.Save(dir, cfg); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 2
	}

	canonical, _ := clientconfig.CanonicalWorkspacePath(path)
	if len(kbs) == 0 {
		fmt.Printf("bound %s in %s to no KBs\n", provider, canonical)
	} else {
		fmt.Printf("bound %s in %s to %s\n", provider, canonical, strings.Join(kbs, ", "))
	}
	if switched {
		fmt.Printf("%s switched to workspace scope: its KB artifacts now live in each bound workspace, not in your home.\n", provider)
		fmt.Println("run `cartographer sync` to move them — the previous global copies are pruned as part of it.")
	} else {
		fmt.Println("run `cartographer sync` to project it")
	}
	return 0
}

func cmdWorkspaceUnbind(args []string) int {
	if len(args) != 2 {
		fmt.Fprintln(os.Stderr, workspaceUsage)
		return 2
	}
	provider, path := args[0], args[1]
	dir, cfg, err := loadWorkspaceConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 2
	}
	if err := cfg.UnbindWorkspace(provider, path); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 2
	}
	if len(cfg.WorkspaceBindings(provider)) == 0 {
		// The last workspace: the provider goes back to the global catalogue,
		// which is the only other state that exists. Leaving it in workspace
		// scope with no bindings would silently deliver nothing.
		if err := cfg.SetWorkspaceScope(provider, clientconfig.ScopeProvider); err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			return 2
		}
		fmt.Printf("%s has no bound workspace left: it returns to the global scope.\n", provider)
	}
	if err := clientconfig.Save(dir, cfg); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 2
	}
	fmt.Printf("unbound %s from %s\n", provider, path)
	fmt.Println("run `cartographer sync` to remove what was projected there")
	return 0
}

func cmdWorkspaceList(args []string) int {
	if len(args) > 1 {
		fmt.Fprintln(os.Stderr, workspaceUsage)
		return 2
	}
	_, cfg, err := loadWorkspaceConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 2
	}
	providers := cfg.Agents
	if len(args) == 1 {
		providers = []string{args[0]}
	}
	sort.Strings(providers)

	any := false
	for _, provider := range providers {
		if cfg.WorkspaceScope(provider) != clientconfig.ScopeWorkspace {
			continue
		}
		any = true
		fmt.Printf("%s  (workspace scope)\n", provider)
		for _, w := range cfg.WorkspaceBindings(provider) {
			state := ""
			if info, err := os.Stat(w.Path); err != nil || !info.IsDir() {
				state = "  [gone]"
			}
			kbs := "no KBs"
			if len(w.KBs) > 0 {
				kbs = strings.Join(w.KBs, ", ")
			}
			fmt.Printf("  %s  →  %s%s\n", w.Path, kbs, state)
			if w.Remote != "" {
				fmt.Printf("      remote %s\n", w.Remote)
			}
		}
	}
	if !any {
		fmt.Println("no provider is in workspace scope (bind one with `cartographer workspace bind`)")
	}
	return 0
}

// workspaceRelPath renders a managed path for a message: relative to the
// workspace when it is inside one, absolute otherwise.
func workspaceRelPath(base, path string) string {
	if rel, err := filepath.Rel(base, path); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return path
}

// nonEmpty drops the empty elements splitCommaList preserves. It preserves them
// so a caller can reject `--kb ""`; here the rejection is the same message as
// giving no --kb at all, so dropping them first keeps one error path.
func nonEmpty(values []string) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

// --- Manifests and apply, per projection (D193) ---

// projectionKey is the map key a projection's manifest and result are stored
// under. Provider alone was the key before D193, which is exactly one
// projection per provider; a workspace-scoped provider has several, and they
// must not share a slot.
// The global projection keys on the bare provider name, which is what the map
// was keyed by before D193: every caller that has only ever had one projection
// per provider keeps working with no translation.
func projectionKey(p syncProjection) string {
	if p.Workspace == "" {
		return p.Provider
	}
	return p.Provider + "\x00" + p.Workspace
}

// globalProjection is the single projection a default-scoped provider has. It
// is the shape every caller had before workspaces existed.
func globalProjection(provider, baseDir string, kbs []string) syncProjection {
	return syncProjection{Provider: provider, BaseDir: baseDir, KBs: kbs, Scope: provisioning.ScopeGlobal}
}

// allProjections resolves every projection of every listed provider, in a
// deterministic order.
func allProjections(cfg *clientconfig.Config, providers []string, clientBaseDir string) ([]syncProjection, error) {
	var out []syncProjection
	sorted := append([]string(nil), providers...)
	sort.Strings(sorted)
	for _, provider := range sorted {
		base, err := provisioning.BaseDirFor(configurator.Provider(provider), clientBaseDir)
		if err != nil {
			return nil, err
		}
		ps, err := projectionsFor(cfg, provider, base)
		if err != nil {
			return nil, err
		}
		out = append(out, ps...)
	}
	return out, nil
}

// manifestsForProjections is manifestsForProviders with the workspace axis: one
// manifest per projection, each merged strictly over **that projection's** KBs.
//
// That narrowing is WP4's whole content. The strict merge (D171) then answers
// "do two KBs collide here", which is the only place a collision can actually
// confuse an agent: two KBs owning a same-named skill in two different
// workspaces is legal and must stay legal, while the same two bound to one
// workspace is still refused.
func manifestsForProjections(cfg *clientconfig.Config, projections []syncProjection) (map[string]provisioning.Manifest, error) {
	out := make(map[string]provisioning.Manifest, len(projections))

	// Every KB any projection asks for, pulled once.
	seen := map[string]bool{}
	var union []string
	anyDefault := false
	requiredBy := map[string][]string{}
	for _, p := range projections {
		if p.BundleOnly {
			continue
		}
		if p.Workspace == "" {
			if _, explicit := cfg.BoundKBs(p.Provider); !explicit {
				anyDefault = true
			}
		}
		for _, kb := range p.KBs {
			if !seen[kb] {
				seen[kb] = true
				union = append(union, kb)
			}
			requiredBy[kb] = append(requiredBy[kb], p.Label())
		}
	}
	sort.Strings(union)

	if len(union) == 0 && !anyDefault {
		for _, p := range projections {
			out[projectionKey(p)] = provisioning.MergeArtifacts(nil)
		}
		// A bundle-only projection still needs the bundle, which lives on the
		// server side of a sync_pull: with no KB to pull it from there is
		// nothing to fetch, and an empty manifest is the honest answer.
		return out, nil
	}

	cs, err := fetchCandidates(cfg, union)
	if err != nil {
		return nil, annotateStaleBinding(err, requiredBy)
	}
	if cs.HasBare {
		fmt.Fprintln(os.Stderr, "warning: the server does not identify its KBs by name, so per-client KB bindings cannot be applied; every connected client receives everything it serves")
	}
	pins, err := pinnedPublicKeys(cfg)
	if err != nil {
		return nil, err
	}
	for _, p := range projections {
		merged, err := provisioning.MergeArtifactsStrict(cs.forProjection(cfg, p))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p.Label(), err)
		}
		verified, err := provisioning.VerifiedManifest(merged, pins)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p.Label(), err)
		}
		out[projectionKey(p)] = verified
	}
	return out, nil
}

// prepareWorkspace runs the repository-hygiene checks and exclusions for one
// project-scoped projection, before anything is written there (WP5). A refusal
// here costs nothing: no file has been touched yet.
func prepareWorkspace(p syncProjection, dryRun bool) error {
	if p.Scope != provisioning.ScopeProject {
		return nil
	}
	provider := configurator.Provider(p.Provider)
	owned := provisioning.ProjectOwnedPaths(provider)
	// The files Cartographer writes a marker-delimited *block* into are the
	// user's own; a repository legitimately tracks them, and they are neither
	// refused nor excluded.
	var blocks []string
	for _, kind := range []string{"instructions"} {
		if rel := provisioning.ProjectDestination(kind, "", provider); rel != "" {
			blocks = append(blocks, rel)
		}
	}
	if err := provisioning.CheckWorkspaceHygiene(p.Provider, p.Workspace, owned, blocks); err != nil {
		return err
	}
	if dryRun {
		return nil
	}
	// Decision 10: exclude Cartographer's OWN untracked paths. A shared file
	// the repository already tracks (a CLAUDE.md the team wrote) is the user's:
	// Cartographer writes a marker-delimited block in it and must not tell git
	// to ignore their own file. One it created itself, because the repository
	// had none, is untracked and is ours to exclude — otherwise every sync
	// leaves the working tree dirty.
	tracked, err := provisioning.TrackedPaths(p.Workspace)
	if err != nil {
		return err
	}
	var excluded []string
	for _, o := range owned {
		isBlock := false
		for _, b := range blocks {
			if o == b {
				isBlock = true
			}
		}
		if isBlock && tracked[filepath.ToSlash(o)] {
			continue
		}
		excluded = append(excluded, o)
	}
	return provisioning.EnsureWorkspaceExcluded(p.Workspace, excluded)
}

// workspaceStatuses reports one provider's workspace projections for `status`
// and `doctor` (D193 WP7).
//
// Every state it can report is a fact it checked, and none of them is healed
// here: a bound directory that is gone, one whose git remote has changed, and a
// Codex project that is not trusted are three different problems with three
// different fixes, and a projection that silently fell back to the global
// catalogue is exactly the exposure the scope exists to prevent.
func workspaceStatuses(cfg *clientconfig.Config, provider, clientBaseDir string) []workspaceStatus {
	if cfg.WorkspaceScope(provider) != clientconfig.ScopeWorkspace {
		return nil
	}
	bindings := cfg.WorkspaceBindings(provider)
	sort.Slice(bindings, func(i, j int) bool { return bindings[i].Path < bindings[j].Path })

	out := make([]workspaceStatus, 0, len(bindings))
	for _, w := range bindings {
		st := workspaceStatus{Path: w.Path, KBs: w.KBs, State: "ok"}
		if info, err := os.Stat(w.Path); err != nil || !info.IsDir() {
			st.State, st.Detail = "gone", "the bound directory is not there any more"
			out = append(out, st)
			continue
		}
		if _, _, err := cfg.ResolveWorkspace(provider, w.Path); err != nil {
			st.State, st.Detail = "moved", err.Error()
			out = append(out, st)
			continue
		}
		// Codex ignores a project's .codex/ layer entirely unless the project
		// is trusted, so the files can be correct and the projection still
		// inactive. Saying "installed" there is the false-positive class D189
		// exists to eliminate.
		if provider == string(configurator.ProviderCodex) &&
			!provisioning.CodexProjectTrusted(provisioning.CodexConfigPath(clientBaseDir), w.Path) {
			st.State = "inactive"
			st.Detail = "codex does not trust this project, so it ignores its .codex/ layer — trust it from a codex session in this directory"
		}
		out = append(out, st)
	}
	return out
}

// printWorkspaceLines renders workspaceStatuses under a provider's status card.
func printWorkspaceLines(ws []workspaceStatus) {
	for _, w := range ws {
		kbs := "no KBs"
		if len(w.KBs) > 0 {
			kbs = strings.Join(w.KBs, ", ")
		}
		suffix := ""
		if w.State != "ok" {
			suffix = "  [" + w.State + "]"
		}
		fmt.Printf("  workspace %s → %s%s\n", w.Path, kbs, suffix)
		if w.Detail != "" {
			fmt.Printf("    %s\n", w.Detail)
		}
	}
}

// checkWorkspaceProjections is `doctor`'s view of the workspace bindings
// (D193 WP7). Each state reported here has a distinct fix, and none of them is
// healed on the spot.
func checkWorkspaceProjections(dir string, cfg *clientconfig.Config, providers []string) []doctorFinding {
	var out []doctorFinding
	for _, provider := range providers {
		if cfg.WorkspaceScope(provider) != clientconfig.ScopeWorkspace {
			continue
		}
		if !provisioning.SupportsProjectScope(configurator.Provider(provider)) {
			out = append(out, doctorFinding{
				Check: "workspace", Severity: doctorError, Path: filepath.Join(dir, clientconfig.FileName),
				Message: fmt.Sprintf("%s is in workspace scope but cannot project: %s",
					provider, provisioning.ProjectScopeUnsupportedReason(configurator.Provider(provider))),
				Fix: "cartographer workspace unbind " + provider + " <path>",
			})
			continue
		}
		for _, w := range workspaceStatuses(cfg, provider, dir) {
			switch w.State {
			case "gone":
				out = append(out, doctorFinding{
					Check: "workspace", Severity: doctorError, Path: w.Path,
					Message: fmt.Sprintf("%s: the bound workspace is not there any more", provider),
					Fix:     fmt.Sprintf("cartographer workspace unbind %s %s", provider, w.Path),
				})
			case "moved":
				out = append(out, doctorFinding{
					Check: "workspace", Severity: doctorError, Path: w.Path,
					Message: fmt.Sprintf("%s: %s", provider, w.Detail),
					Fix:     fmt.Sprintf("cartographer workspace bind %s %s --kb <name>", provider, w.Path),
				})
			case "inactive":
				// A warning, not an error: nothing is broken, the projection is
				// simply not being read yet, and the fix is the user's to make
				// in their own client.
				out = append(out, doctorFinding{
					Check: "workspace", Severity: doctorWarning, Path: w.Path,
					Message: fmt.Sprintf("%s: %s", provider, w.Detail),
					Fix:     "trust the project from a codex session in that directory",
				})
			}
		}
	}
	return out
}

// pruneOrphanProjections removes the files of every projection the lockfile
// still records but the configuration no longer declares, and drops its lock
// entry. It returns the labels of what it pruned.
//
// This is the other half of `workspace unbind`. Unbinding removes the
// declaration; without this, the files it had projected would stay in the
// repository forever, because every later sync iterates the *declared*
// projections and so would never look at them again.
//
// Only WORKSPACE projections are considered. A provider's global projection is
// removed by `disconnect`, which owns that decision — reaching it from here
// would delete a connected provider's catalogue because it was absent from one
// `sync --client` invocation.
func pruneOrphanProjections(lockFile *provisioning.LockFile, declared []syncProjection, clientBaseDir string) ([]string, error) {
	wanted := map[string]bool{}
	providersInPlay := map[string]bool{}
	for _, p := range declared {
		wanted[p.Provider+"\x00"+p.Workspace] = true
		providersInPlay[p.Provider] = true
	}

	var pruned []string
	for _, key := range lockFile.Projections() {
		if key.Global() || !providersInPlay[key.Provider] {
			// Not a workspace projection, or a provider this run is not
			// touching at all (`sync --client` names a subset): leaving it
			// alone is the only safe answer.
			continue
		}
		if wanted[key.Provider+"\x00"+key.Workspace] {
			continue
		}
		lock := lockFile.ForProjection(key)
		base := provisioning.LockBaseDir(lock, clientBaseDir)
		if _, err := provisioning.PruneManaged(lock.Managed, base, false); err != nil {
			return nil, fmt.Errorf("prune %s: %w", key.String(), err)
		}
		// The exclusions go with the files: leaving the block behind would keep
		// git ignoring paths nothing writes any more.
		if err := provisioning.RemoveWorkspaceExclusions(key.Workspace); err != nil {
			return nil, fmt.Errorf("prune %s: %w", key.String(), err)
		}
		lockFile.RemoveProjection(key)
		pruned = append(pruned, key.String())
		fmt.Printf("[%s] pruned the projection in %s\n", key.Provider, key.Workspace)
	}
	return pruned, nil
}
