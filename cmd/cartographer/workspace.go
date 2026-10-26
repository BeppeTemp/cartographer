package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/mcpbridge"
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
	// Derived marks a projection whose binding comes from a KB's paths.yaml
	// (D1019), not from `workspace bind`. It never edits a file git tracks:
	// InstructionsRel redirects the instructions block to an untracked file,
	// and InstructionsSkipped names the tracked file that made it write none.
	Derived             bool
	InstructionsRel     string
	InstructionsSkipped string
	// SharedSkipped lists the tracked shared files (MCP and registration
	// files) a derived projection leaves alone, and SkipKinds the artifact
	// kinds it therefore does not apply (D1181).
	SharedSkipped []string
	SkipKinds     []string
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
			if w.Derived {
				// Derived state, recomputed by the next sync: a clone that
				// moved is no reason to refuse every other projection, and
				// its files are pruned once it stops being derived.
				continue
			}
			return nil, fmt.Errorf("%s: bound workspace %s is gone — rebind it, or remove the binding with `cartographer workspace unbind %s %s`",
				provider, w.Path, provider, w.Path)
		}
		sp := syncProjection{
			Provider:  provider,
			Workspace: w.Path,
			BaseDir:   w.Path,
			Remote:    w.Remote,
			KBs:       w.KBs,
			Scope:     provisioning.ScopeProject,
			Derived:   w.Derived,
		}
		if w.Derived {
			sp.InstructionsRel, sp.InstructionsSkipped = derivedInstructions(provider, w.Path)
			sp.SharedSkipped, sp.SkipKinds = derivedSharedSkips(provider, w.Path)
		}
		out = append(out, sp)
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

// --- `cartographer workspace` ---

func cmdWorkspace(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, workspaceUsage)
		return 2
	}
	if len(args) == 1 && isHelpArg(args[0]) {
		fmt.Fprintln(os.Stdout, workspaceUsage)
		return 0
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "bind":
		return cmdWorkspaceBind(rest)
	case "unbind":
		return cmdWorkspaceUnbind(rest)
	case "list":
		return cmdWorkspaceList(rest)
	case "resolve":
		return cmdWorkspaceResolve(rest)
	default:
		fmt.Fprintln(os.Stderr, workspaceUsage)
		return 2
	}
}

const workspaceUsage = `usage: cartographer workspace bind <provider> <path> --kb <name>[,<name>...]
       cartographer workspace bind codex <path> --kb <name>[,<name>...] [--trust]
       cartographer workspace bind <provider> <path> --no-kb
       cartographer workspace unbind <provider> <path>
       cartographer workspace list [provider]
       cartographer workspace resolve [<path>] [--provider <name>]`

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
	noKB, trust := false, false
	positional := make([]string, 0, 2)
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--no-kb":
			noKB = true
		case args[i] == "--trust":
			trust = true
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
	if trust && p != configurator.ProviderCodex {
		fmt.Fprintf(os.Stderr, "Error: --trust only applies to the codex provider, not %s\n", provider)
		return 2
	}
	if cfg.Clients[provider].Access != "" {
		fmt.Fprintf(os.Stderr, "Error: %s has the read-only profile (clients.%s.access), which the workspace bridge does not carry: remove that key from .cartographer.yaml to bind workspaces, or keep the profile and skip the binding.\n", provider, provider)
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
	if p == configurator.ProviderCodex {
		return codexTrustStep(dir, canonical, trust)
	}
	return 0
}

// codexTrustStep is the Codex project-trust part of `workspace bind` (D1074),
// run after the binding is saved. With --trust it records the folder in the
// user's Codex config (the flag is the consent: no prompt, never implied by
// anything else); without it, it only prints the line to add when the folder is
// not trusted yet. A refused or failed write is non-zero, and says the binding
// was saved.
func codexTrustStep(dir, workspace string, trust bool) int {
	cfgPath := provisioning.CodexConfigPath(dir)
	if !trust {
		if !provisioning.CodexProjectTrusted(cfgPath, workspace) {
			fmt.Printf("codex ignores this folder's .codex/ layer until it is trusted: add to %s\n%s\n(or rerun with --trust, or accept the prompt in a codex session there)\n",
				cfgPath, provisioning.ManualLine(workspace))
		}
		return 0
	}
	res, err := provisioning.TrustCodexProject(cfgPath, workspace)
	switch {
	case err != nil:
		fmt.Fprintf(os.Stderr, "warning: the binding was saved, but trusting %s failed: %v\nadd to %s\n%s\n",
			workspace, err, cfgPath, res.Line)
		return 2
	case res.Status == provisioning.TrustRefused:
		fmt.Fprintf(os.Stderr, "warning: the binding was saved, but %s was not trusted: %s\nadd to %s\n%s\n",
			workspace, res.Reason, cfgPath, res.Line)
		return 2
	case res.Status == provisioning.TrustAlreadyTrusted:
		fmt.Printf("%s is already trusted in %s\n", workspace, cfgPath)
	default:
		fmt.Printf("trusted %s in %s\n", workspace, cfgPath)
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
	derivedHere := false
	if canonical, err := clientconfig.CanonicalWorkspacePath(path); err == nil {
		explicit := false
		for _, w := range cfg.Workspaces[provider] {
			explicit = explicit || w.Path == canonical
		}
		for _, w := range cfg.WorkspaceBindings(provider) {
			derivedHere = derivedHere || (w.Derived && w.Path == canonical && !explicit)
		}
	}
	if err := cfg.UnbindWorkspace(provider, path); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 2
	}
	if derivedHere {
		fmt.Fprintf(os.Stderr, "note: %s is bound by a KB's paths.yaml, not by a `workspace bind`, so there is nothing to unbind; "+
			"`cartographer workspace bind %s %s --no-kb` opts it out on this machine, `bind: false` in the KB's paths.yaml for everyone\n", path, provider, path)
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
		bindings := cfg.WorkspaceBindings(provider)
		sort.Slice(bindings, func(i, j int) bool { return bindings[i].Path < bindings[j].Path })
		for _, w := range bindings {
			state := ""
			if info, err := os.Stat(w.Path); err != nil || !info.IsDir() {
				state = "  [gone]"
			}
			kbs := "no KBs"
			if len(w.KBs) > 0 {
				kbs = strings.Join(w.KBs, ", ")
			}
			if src := sourcesLabel(w); src != "" {
				state += "  [derived: " + src + "]"
			}
			fmt.Printf("  %s  →  %s%s\n", w.Path, kbs, state)
			if w.Remote != "" {
				fmt.Printf("      remote %s\n", w.Remote)
			}
		}
		for _, u := range cfg.DerivedUnresolved(provider) {
			fmt.Printf("  repository %s of %s  [%s]\n", u.Key, u.KB, u.Reason)
		}
	}
	if !any {
		fmt.Println("no provider is in workspace scope (bind one with `cartographer workspace bind`)")
	}
	return 0
}

// cmdWorkspaceResolve answers "why does a session here see these KBs": the
// binding that wins for a folder, per provider, as the bridge would resolve it.
func cmdWorkspaceResolve(args []string) int {
	var path, provider string
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--provider" && i+1 < len(args):
			i++
			provider = args[i]
		case strings.HasPrefix(args[i], "--provider="):
			provider = strings.TrimPrefix(args[i], "--provider=")
		case strings.HasPrefix(args[i], "-"):
			fmt.Fprintf(os.Stderr, "Error: unknown flag %q\n%s\n", args[i], workspaceUsage)
			return 2
		case path == "":
			path = args[i]
		default:
			fmt.Fprintln(os.Stderr, workspaceUsage)
			return 2
		}
	}
	if path == "" {
		// The process working directory, never $PWD (the bridge does the same).
		wd, err := os.Getwd()
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			return 2
		}
		path = wd
	}
	_, cfg, err := loadWorkspaceConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 2
	}
	providers := append([]string(nil), cfg.Agents...)
	if provider != "" {
		providers = []string{provider}
	}
	sort.Strings(providers)
	fmt.Print(renderWorkspaceResolve(cfg, path, providers))
	return 0
}

// renderWorkspaceResolve is the text of `workspace resolve`, split out so its
// shape is pinned by a golden test.
func renderWorkspaceResolve(cfg *clientconfig.Config, path string, providers []string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "folder %s\n", path)
	for _, p := range providers {
		scope := mcpbridge.Resolve(cfg, p, path)
		entry := "http"
		if cfg.UsesBridge(p, cfg.ServerRoutedPath) {
			entry = "bridge"
		}
		fmt.Fprintf(&sb, "%s  (global MCP entry: %s)\n", p, entry)
		switch {
		case scope.Err != nil:
			fmt.Fprintf(&sb, "  unusable binding: %v\n  no KB is served here until it is fixed\n", scope.Err)
		case scope.Binding != nil:
			kbs := "no KB"
			if len(scope.KBs) > 0 {
				kbs = strings.Join(scope.KBs, ", ")
			}
			if scope.Binding.Derived {
				fmt.Fprintf(&sb, "  bound by %s  ->  %s  (derived from %s; `workspace bind %s %s --no-kb` opts out here)\n",
					scope.Binding.Path, kbs, sourcesLabel(*scope.Binding), p, scope.Binding.Path)
			} else {
				fmt.Fprintf(&sb, "  bound by %s  ->  %s\n", scope.Binding.Path, kbs)
			}
		case scope.All:
			fmt.Fprintf(&sb, "  unbound: the provider's default set (every KB)\n")
		default:
			kbs := "no KB"
			if len(scope.KBs) > 0 {
				kbs = strings.Join(scope.KBs, ", ")
			}
			fmt.Fprintf(&sb, "  unbound: the provider's binding  ->  %s\n", kbs)
		}
		// Only a bridge entry carries the mode: an http entry is not narrowed
		// by any binding, so it is not made read-only either.
		if entry == "bridge" && scope.ReadOnly && scope.Err == nil {
			fmt.Fprintf(&sb, "  read-only: no write tool and no KB artifact here; bind the folder to write\n")
		}
		if entry == "bridge" && scope.Binding == nil && scope.Err == nil {
			fmt.Fprintf(&sb, "  a client that declares MCP roots can still bind this session through them\n")
		}
		if entry == "http" {
			fmt.Fprintf(&sb, "  this client has no bridge entry, so bindings do not narrow its session\n")
		}
	}
	return sb.String()
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
		ps, err := projectionsFor(cfg, provider, clientBaseDir)
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
	out, _, err := buildProjectionManifests(cfg, projections, false, nil)
	return out, err
}

// manifestsForProjectionsPartial is manifestsForProjections for sync (#350):
// a KB whose pull fails does not stop the run. Every projection that would
// receive that KB is left out of the returned map — so it is not applied and
// keeps its previous state — and the failures come back in KB order for the
// caller to report. A projection that needs no failed KB is built exactly as
// manifestsForProjections builds it.
func manifestsForProjectionsPartial(cfg *clientconfig.Config, projections []syncProjection, pre *prefetched) (map[string]provisioning.Manifest, []kbPullFailure, error) {
	return buildProjectionManifests(cfg, projections, true, pre)
}

// buildProjectionManifests builds one manifest per projection. pre, when it
// covers every KB the projections ask for, replaces the pulls (D1019: the
// derivation already made them); nil pulls here.
func buildProjectionManifests(cfg *clientconfig.Config, projections []syncProjection, partial bool, pre *prefetched) (map[string]provisioning.Manifest, []kbPullFailure, error) {
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
		return out, nil, nil
	}

	var cs candidateSet
	var failed []kbPullFailure
	var err error
	if !anyDefault && pre.covers(union) {
		cs = pre.cs
		failed = pre.failedFor(union)
		if len(failed) > 0 && len(failed) == len(union) {
			err = failed[0].Err
		}
	} else if partial {
		cs, failed, err = fetchCandidatesPartial(cfg, union)
		if err == nil && len(failed) > 0 && len(failed) == len(union) {
			// Nothing answered: there is no run to continue, only the first
			// failure to report, as the all-or-nothing path would.
			err = failed[0].Err
		}
	} else {
		cs, err = fetchCandidates(cfg, union)
	}
	if err != nil {
		return nil, nil, annotateStaleBinding(err, requiredBy)
	}
	unavailable := make(map[string]bool, len(failed))
	for _, f := range failed {
		unavailable[f.KB] = true
	}
	if cs.HasBare {
		fmt.Fprintln(os.Stderr, "warning: the server does not identify its KBs by name, so per-client KB bindings cannot be applied; every connected client receives everything it serves")
	}
	pins, err := pinnedPublicKeys(cfg)
	if err != nil {
		return nil, nil, err
	}
	for _, p := range projections {
		if needsAny(p.KBs, unavailable) {
			continue
		}
		merged, err := provisioning.MergeArtifactsStrict(cs.forProjection(cfg, p))
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", p.Label(), err)
		}
		verified, err := provisioning.VerifiedManifest(merged, pins)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", p.Label(), err)
		}
		// Set after verification, which copies only the signed catalogue.
		verified.Placeholders = cs.placeholdersForProjection(cfg, p)
		verified.PathRegistries = cs.pathRegistriesForProjection(cfg, p)
		out[projectionKey(p)] = verified
	}
	return out, failed, nil
}

// needsAny reports whether a projection bound to kbs would receive any KB in
// unavailable.
func needsAny(kbs []string, unavailable map[string]bool) bool {
	for _, kb := range kbs {
		if unavailable[kb] {
			return true
		}
	}
	return false
}

// prepareWorkspace runs the repository-hygiene checks and exclusions for one
// project-scoped projection, before anything is written there (WP5). A refusal
// here costs nothing: no file has been touched yet.
func prepareWorkspace(p syncProjection, providers []string, dryRun bool, owned ownedPathsFunc) error {
	if p.Scope != provisioning.ScopeProject {
		return nil
	}
	provider := configurator.Provider(p.Provider)
	ownedNow, err := owned(provider)
	if err != nil {
		return err
	}
	if err := provisioning.CheckWorkspaceHygiene(p.Provider, p.Workspace, ownedNow, workspaceBlockFiles(provider)); err != nil {
		return err
	}
	if dryRun {
		return nil
	}
	if err := writeWorkspaceExclusions(p.Workspace, providers, owned, p.InstructionsRel); err != nil {
		if provisioning.ExcludeWarning(err) {
			// The exclude block is a convenience, not what the sync is for: a
			// linked .git (D148) costs a warning and a dirty working tree, not
			// the projection.
			fmt.Fprintf(os.Stderr, "warning: workspace %s: git exclude left unchanged: %v\n", p.Workspace, err)
			return nil
		}
		return err
	}
	return nil
}

// ownedPathsFunc returns the workspace-relative paths a provider's projection
// owns in one workspace (D1181).
type ownedPathsFunc func(provider configurator.Provider) ([]string, error)

// workspaceOwnership resolves what each provider's projection of workspace owns:
// the artifacts this run plans for it (manifests, keyed as projectionKey) and
// the paths the lockfile records for it. A provider with neither owns only its
// shared files, so a repository's own artifacts are never claimed.
func workspaceOwnership(lockFile *provisioning.LockFile, projections []syncProjection, manifests map[string]provisioning.Manifest, workspace string) ownedPathsFunc {
	return func(provider configurator.Provider) ([]string, error) {
		var m provisioning.Manifest
		var notWritten []string
		for _, p := range projections {
			if p.Scope == provisioning.ScopeProject && p.Workspace == workspace && p.Provider == string(provider) {
				m = withoutKinds(manifests[projectionKey(p)], p.SkipKinds)
				notWritten = p.SharedSkipped
				break
			}
		}
		lock := lockFile.ForProjection(provisioning.Projection{Provider: string(provider), Workspace: workspace})
		paths, err := provisioning.ProjectOwnedArtifactPaths(provider, m, lock)
		if err != nil {
			return nil, err
		}
		// A shared file a derived projection leaves alone is not its to
		// refuse or to exclude (D1181).
		return slices.DeleteFunc(paths, func(p string) bool { return slices.Contains(notWritten, p) }), nil
	}
}

// workspaceBlockFiles are the files Cartographer writes a marker-delimited
// *block* into for provider: the user's own; a repository legitimately tracks
// them, and they are neither refused nor excluded.
func workspaceBlockFiles(provider configurator.Provider) []string {
	var blocks []string
	for _, kind := range []string{"instructions"} {
		if rel := provisioning.ProjectDestination(kind, "", provider); rel != "" {
			blocks = append(blocks, rel)
		}
	}
	return blocks
}

// writeWorkspaceExclusions writes the workspace's exclude block for EVERY
// provider bound to it, not only the one being prepared: the block is one per
// workspace, so writing one provider's paths would drop the others' (#656).
// No provider left removes the block.
//
// Decision 10: exclude Cartographer's OWN untracked paths. A shared file the
// repository already tracks (a CLAUDE.md the team wrote) is the user's:
// Cartographer writes a marker-delimited block in it and must not tell git to
// ignore their own file. One it created itself, because the repository had
// none, is untracked and is ours to exclude — otherwise every sync leaves the
// working tree dirty.
func writeWorkspaceExclusions(workspace string, providers []string, owned ownedPathsFunc, extra ...string) error {
	if len(providers) == 0 {
		return provisioning.RemoveWorkspaceExclusions(workspace)
	}
	tracked, err := provisioning.TrackedPaths(workspace)
	if err != nil {
		return err
	}
	// The untracked file a derived projection puts its instructions block in
	// (D1019) is Cartographer's own, so it is excluded like its directories:
	// the one this run is about to write (extra) and the ones earlier runs
	// wrote, which the block they hold gives away.
	excluded := slices.Clone(extra)
	for _, name := range providers {
		provider := configurator.Provider(name)
		excluded = append(excluded, provisioning.OwnedLocalInstructions(workspace, provider)...)
		blocks := workspaceBlockFiles(provider)
		ownedPaths, err := owned(provider)
		if err != nil {
			return err
		}
		for _, o := range ownedPaths {
			if slices.Contains(blocks, o) && tracked[filepath.ToSlash(o)] {
				continue
			}
			excluded = append(excluded, o)
		}
	}
	return provisioning.EnsureWorkspaceExcluded(workspace, excluded)
}

// workspaceProviders returns the providers bound to workspace after this run:
// those declared in it, plus those the lockfile records there that this run
// does not touch (`sync --client` names a subset). A provider this run touches
// but no longer declares there is leaving and is not included.
func workspaceProviders(lockFile provisioning.LockFile, declared []syncProjection, workspace string) []string {
	inPlay := map[string]bool{}
	seen := map[string]bool{}
	var out []string
	for _, p := range declared {
		inPlay[p.Provider] = true
		if p.Scope == provisioning.ScopeProject && p.Workspace == workspace && !seen[p.Provider] {
			seen[p.Provider] = true
			out = append(out, p.Provider)
		}
	}
	for _, key := range lockFile.WorkspaceProjections(workspace) {
		if !inPlay[key.Provider] && !seen[key.Provider] {
			seen[key.Provider] = true
			out = append(out, key.Provider)
		}
	}
	sort.Strings(out)
	return out
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
		st := workspaceStatus{Path: w.Path, KBs: w.KBs, State: "ok", Derived: w.Derived, Source: sourcesLabel(w)}
		if info, err := os.Stat(w.Path); err != nil || !info.IsDir() {
			st.State, st.Detail = "gone", "the bound directory is not there any more"
			if w.Derived {
				st.Detail += "; the next sync drops this derived binding"
			}
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
			st.Detail = codexUntrustedDetail(w, provisioning.CodexConfigPath(clientBaseDir))
		}
		if w.Derived {
			if _, skipped := derivedInstructions(provider, w.Path); skipped != "" {
				st.Detail = fmt.Sprintf("instructions not written: %s is tracked by git and a derived binding never edits a tracked file — `cartographer workspace bind %s %s --kb %s` writes the block there",
					skipped, provider, w.Path, strings.Join(w.KBs, ","))
			}
		}
		if w.Derived {
			if files, kinds := derivedSharedSkips(provider, w.Path); len(files) > 0 {
				note := fmt.Sprintf("not written: %s is tracked by git and a derived binding never edits a tracked file — the provider keeps its global entry; `cartographer workspace bind %s %s --kb %s` is the explicit alternative",
					strings.Join(files, ", "), provider, w.Path, strings.Join(w.KBs, ","))
				if slices.Contains(kinds, "hook") {
					note = "hooks not projected: " + strings.Join(files, ", ") + " is tracked by git; " + note
				}
				if st.Detail != "" {
					note = st.Detail + "; " + note
				}
				st.Detail = note
			}
		}
		out = append(out, st)
	}
	for _, u := range cfg.DerivedUnresolved(provider) {
		out = append(out, workspaceStatus{Path: "repo:" + u.Key, KBs: []string{u.KB}, State: "unresolved", Detail: u.Reason, Derived: true, Source: "paths.yaml of " + u.KB})
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
		if w.State == "unresolved" {
			fmt.Printf("  repository %s of %s  [%s]\n", strings.TrimPrefix(w.Path, "repo:"), kbs, w.Detail)
			continue
		}
		if w.State != "ok" {
			suffix = "  [" + w.State + "]"
		}
		if w.Source != "" {
			suffix += "  (" + w.Source + ")"
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
			if w.Derived && w.State == "ok" && w.Detail != "" {
				out = append(out, doctorFinding{
					Check: "workspace", Severity: doctorWarning, Path: w.Path,
					Message: fmt.Sprintf("%s: %s", provider, w.Detail),
					Fix:     fmt.Sprintf("cartographer workspace bind %s %s --kb %s", provider, w.Path, strings.Join(w.KBs, ",")),
				})
				continue
			}
			if w.Derived && (w.State == "gone" || w.State == "moved" || w.State == "unresolved") {
				// Derived state is rebuilt by sync: the fix is a sync, never an
				// unbind (there is no explicit binding to remove).
				if w.State == "unresolved" {
					continue
				}
				out = append(out, doctorFinding{
					Check: "workspace", Severity: doctorWarning, Path: w.Path,
					Message: fmt.Sprintf("%s: derived binding %s (%s)", provider, w.State, w.Detail),
					Fix:     "cartographer sync",
				})
				continue
			}
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
					Fix:     "accept the trust prompt in a codex session in that directory, or add the table named above",
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
func pruneOrphanProjections(lockFile *provisioning.LockFile, declared []syncProjection, manifests map[string]provisioning.Manifest, clientBaseDir string) ([]string, error) {
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
		// Not what another provider's projection of this workspace still
		// records (D364): .agents/skills/<n>/ is Codex's and Antigravity's.
		keep := lockFile.CoOwnedPaths(key)
		if _, err := provisioning.PruneManaged(provisioning.WithoutCoOwned(lock.Managed, keep), base, false); err != nil {
			if errors.Is(err, provisioning.ErrSymlinkDestination) {
				// Refused (D148): the projection stays in the lockfile, so the
				// next sync retries it; the other projections are still pruned.
				fmt.Fprintf(os.Stderr, "warning: %s not pruned: %v\n", key.String(), err)
				continue
			}
			return nil, fmt.Errorf("prune %s: %w", key.String(), err)
		}
		// The exclusions go with the files, but only this provider's: another
		// provider still bound to the workspace keeps its paths excluded (#656).
		lockFile.RemoveProjection(key)
		if err := writeWorkspaceExclusions(key.Workspace, workspaceProviders(*lockFile, declared, key.Workspace), workspaceOwnership(lockFile, declared, manifests, key.Workspace)); err != nil {
			if !provisioning.ExcludeWarning(err) {
				return nil, fmt.Errorf("prune %s: %w", key.String(), err)
			}
			fmt.Fprintf(os.Stderr, "warning: workspace %s: git exclude left unchanged: %v\n", key.Workspace, err)
		}
		pruned = append(pruned, key.String())
		fmt.Printf("[%s] pruned the projection in %s\n", key.Provider, key.Workspace)
	}
	return pruned, nil
}

// codexUntrustedDetail is the status/doctor text for a Codex workspace that is
// not trusted. The command it offers matches the binding: --no-kb for an empty
// one, and none for a derived one (D1019), where `workspace bind` would create
// an explicit binding the user never asked for.
func codexUntrustedDetail(w clientconfig.WorkspaceBinding, cfgPath string) string {
	line := strings.ReplaceAll(provisioning.ManualLine(w.Path), "\n", " + ")
	msg := "codex does not trust this project, so it ignores its .codex/ layer — "
	if !w.Derived {
		kbs := "--no-kb"
		if len(w.KBs) > 0 {
			kbs = "--kb " + strings.Join(w.KBs, ",")
		}
		msg += fmt.Sprintf("`cartographer workspace bind codex %s %s --trust` records it, or ", w.Path, kbs)
	}
	return msg + fmt.Sprintf("add %s to %s, or accept the prompt in a codex session in this directory", line, cfgPath)
}
