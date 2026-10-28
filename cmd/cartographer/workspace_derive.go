package main

import (
	"fmt"
	"slices"
	"sort"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
	"github.com/BeppeTemp/cartographer/internal/repoindex"
)

// workspace_derive.go (D1019) — the bindings a KB's paths.yaml implies.
//
// A repository a KB declares in `repos:` is bound to that KB for every provider
// in workspace scope, so activating a KB in its own repositories needs no
// `workspace bind`. The list is derived state: `sync` recomputes it from the
// registries it pulls and from where the clones are on this machine, and stores
// it apart from `.cartographer.yaml`.

// prefetched is a set of sync_pull responses made once for the derivation and
// reused by the manifest build, so a sync pulls each KB once.
type prefetched struct {
	cs     candidateSet
	failed []kbPullFailure
	kbs    map[string]bool
}

// covers reports whether every KB in kbs was part of the prefetch.
func (p *prefetched) covers(kbs []string) bool {
	if p == nil {
		return false
	}
	for _, kb := range kbs {
		if !p.kbs[kb] {
			return false
		}
	}
	return true
}

// failedFor returns the prefetch's failures restricted to kbs.
func (p *prefetched) failedFor(kbs []string) []kbPullFailure {
	want := make(map[string]bool, len(kbs))
	for _, kb := range kbs {
		want[kb] = true
	}
	var out []kbPullFailure
	for _, f := range p.failed {
		if want[f.KB] {
			out = append(out, f)
		}
	}
	return out
}

// deriveWorkspaceBindings recomputes the derived bindings of every targeted
// provider and installs them in cfg, so projectionsFor sees them. It returns
// the pulls it made (for the manifest build to reuse) and a commit function
// that persists the new state; sync calls it once the manifests are in hand,
// because nothing is written before they are (D172).
//
// A provider whose KBs could not all be pulled keeps its previous derived
// list: a failed pull is not a declaration that a repository went away. A
// provider outside workspace scope loses its entries, as it has no use for
// them.
func deriveWorkspaceBindings(cfg *clientconfig.Config, targets []string, dir string, dryRun bool) (*prefetched, func() error, error) {
	providers, candidates := derivationScope(cfg, targets)
	var pre *prefetched
	if len(candidates) > 0 {
		cs, failed, err := fetchCandidatesPartial(cfg, candidates)
		if err != nil {
			return nil, nil, err
		}
		kbs := make(map[string]bool, len(candidates))
		for _, kb := range candidates {
			kbs[kb] = true
		}
		pre = &prefetched{cs: cs, failed: failed, kbs: kbs}
	}
	resolver := repoindex.NewResolver(cfg.Paths, cfg.ScanRoots(), repoindex.DefaultDepth)
	next := computeDerived(cfg, targets, providers, pre, resolver)
	cfg.SetDerived(next)

	commit := func() error {
		if dryRun {
			// The plan names every file the real run writes (D172 WP4).
			if path, changed, err := clientconfig.DerivedWouldChange(dir, next); err == nil && changed {
				fmt.Printf("[dry-run] would write %s\n", path)
			}
			return nil
		}
		return clientconfig.SaveDerived(dir, next)
	}
	return pre, commit, nil
}

// derivationScope returns the targeted providers that derive bindings (in
// workspace scope, able to project) and the KBs whose registries they need:
// the KBs each one receives.
func derivationScope(cfg *clientconfig.Config, targets []string) (providers, candidates []string) {
	seen := map[string]bool{}
	for _, p := range targets {
		if cfg.WorkspaceScope(p) != clientconfig.ScopeWorkspace || !provisioning.SupportsProjectScope(configurator.Provider(p)) {
			continue
		}
		providers = append(providers, p)
		bound, _ := cfg.BoundKBs(p)
		for _, kb := range bound {
			if !seen[kb] {
				seen[kb] = true
				candidates = append(candidates, kb)
			}
		}
	}
	sort.Strings(candidates)
	return providers, candidates
}

// computeDerived is the derivation itself, on KB registries already pulled:
// the state that replaces cfg's for the targeted providers.
func computeDerived(cfg *clientconfig.Config, targets, providers []string, pre *prefetched, resolver *repoindex.Resolver) clientconfig.DerivedState {
	prev := cfg.Derived()
	next := clientconfig.DerivedState{
		Bindings:   map[string][]clientconfig.DerivedBinding{},
		Unresolved: map[string][]clientconfig.DerivedUnresolved{},
	}
	targeted := map[string]bool{}
	for _, t := range targets {
		targeted[t] = true
	}
	// Providers this run does not touch (`sync --client`) keep their entries.
	for p, list := range prev.Bindings {
		if !targeted[p] {
			next.Bindings[p] = list
		}
	}
	for p, list := range prev.Unresolved {
		if !targeted[p] {
			next.Unresolved[p] = list
		}
	}

	for _, p := range providers {
		bound, _ := cfg.BoundKBs(p)
		if pre == nil || pre.cs.HasBare {
			// No KB to read a declaration from, or no KB names to bind to:
			// nothing derives (fail closed).
			continue
		}
		if len(pre.failedFor(bound)) > 0 {
			next.Bindings[p] = prev.Bindings[p]
			next.Unresolved[p] = prev.Unresolved[p]
			continue
		}
		var decls []clientconfig.DeclaredRepo
		decl := map[string]provisioning.PathDecl{}
		for _, kb := range bound {
			reg, ok := pre.cs.PathRegistries[kb]
			if !ok {
				continue
			}
			keys := make([]string, 0, len(reg.Repos))
			for k := range reg.Repos {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				d := reg.Repos[k]
				if !d.BindsRepo() {
					continue
				}
				decls = append(decls, clientconfig.DeclaredRepo{KB: kb, Key: k, Remote: d.Remote})
				decl[kb+"\x00"+k] = d
			}
		}
		bindings, unresolved := clientconfig.DeriveBindings(decls, func(d clientconfig.DeclaredRepo) (string, error) {
			path, _, err := provisioning.ResolveDeclaredRepo(resolver, d.Key, decl[d.KB+"\x00"+d.Key], true)
			return path, err
		})
		if len(bindings) > 0 {
			next.Bindings[p] = bindings
		}
		if len(unresolved) > 0 {
			next.Unresolved[p] = unresolved
		}
	}
	for p, list := range next.Bindings {
		if len(list) == 0 {
			delete(next.Bindings, p)
		}
	}
	for p, list := range next.Unresolved {
		if len(list) == 0 {
			delete(next.Unresolved, p)
		}
	}
	return next
}

// derivedInstructions decides, for a derived projection, where its instructions
// go: a derived binding never edits a file git tracks (D1019), because it would
// do it in every declared repository with no command run there, and a KB name in
// a tracked file of a public repository is a leak. rel is the project-scope
// instructions file; when it is tracked, Claude Code gets its block in the
// untracked CLAUDE.local.md and every other client gets none.
func derivedInstructions(provider, workspace string) (override, skipped string) {
	rel := provisioning.ProjectDestination("instructions", "", configurator.Provider(provider))
	if rel == "" {
		return "", ""
	}
	tracked, err := provisioning.TrackedPaths(workspace)
	if err != nil || !tracked[rel] {
		return "", ""
	}
	if provider == string(configurator.ProviderClaudeCode) && !tracked[provisioning.ClaudeLocalInstructions] {
		return provisioning.ClaudeLocalInstructions, ""
	}
	return "", rel
}

// withoutInstructions drops the instructions artifacts from m.
func withoutInstructions(m provisioning.Manifest) provisioning.Manifest {
	out := m
	out.Artifacts = nil
	for _, a := range m.Artifacts {
		if a.Kind != "instructions" {
			out.Artifacts = append(out.Artifacts, a)
		}
	}
	return out
}

// sourcesLabel is the human form of where a derived binding comes from.
func sourcesLabel(w clientconfig.WorkspaceBinding) string {
	if !w.Derived {
		return ""
	}
	if len(w.Sources) == 0 {
		return "paths.yaml"
	}
	return fmt.Sprintf("paths.yaml of %s", joinKBs(w.Sources))
}

func joinKBs(kbs []string) string {
	out := ""
	seen := map[string]bool{}
	for _, kb := range kbs {
		if seen[kb] {
			continue
		}
		seen[kb] = true
		if out != "" {
			out += ", "
		}
		out += kb
	}
	return out
}

// derivedSharedSkips names the tracked shared files a derived projection of
// provider must leave alone, and the artifact kinds that would be written into
// them: the MCP file (a crush hook registers in it too) and the registration
// files of projectRegistrationFiles. Instructions have their own rule above.
func derivedSharedSkips(provider, workspace string) (files, kinds []string) {
	prov := configurator.Provider(provider)
	tracked, err := provisioning.TrackedPaths(workspace)
	if err != nil {
		return nil, nil
	}
	if rel := provisioning.ProjectDestination("mcp", "", prov); rel != "" && tracked[rel] {
		files = append(files, rel)
		kinds = append(kinds, "mcp")
		if prov == configurator.ProviderCrush {
			kinds = append(kinds, "hook")
		}
	}
	for _, rel := range provisioning.ProjectRegistrationFiles(prov) {
		if tracked[rel] {
			files = append(files, rel)
			kinds = append(kinds, "hook")
		}
	}
	// A derived Claude Code projection registers its hooks in the untracked
	// settings.local.json, never in settings.json (#1198): one the repository
	// tracks after all leaves the hooks out.
	if prov == configurator.ProviderClaudeCode && tracked[provisioning.ClaudeLocalSettings] && !slices.Contains(files, provisioning.ClaudeLocalSettings) {
		files = append(files, provisioning.ClaudeLocalSettings)
		kinds = append(kinds, "hook")
	}
	// A hook registration file the client dedicates to hooks (Codex's hooks.json)
	// is patched by every hook, so a tracked one is left alone like the others.
	for _, rel := range provisioning.ProjectSharedHookFiles(prov) {
		if tracked[rel] {
			files = append(files, rel)
			if !slices.Contains(kinds, "hook") {
				kinds = append(kinds, "hook")
			}
		}
	}
	return files, kinds
}

// derivedHookSettings is the settings file a derived projection of provider
// registers Claude Code hooks in (#1198): ClaudeLocalSettings, unless the
// projection applies no hook (skipKinds, from derivedSharedSkips).
func derivedHookSettings(provider string, skipKinds []string) string {
	if provider != string(configurator.ProviderClaudeCode) || slices.Contains(skipKinds, "hook") {
		return ""
	}
	return provisioning.ClaudeLocalSettings
}

// withoutKinds drops the artifacts of the given kinds from m.
func withoutKinds(m provisioning.Manifest, kinds []string) provisioning.Manifest {
	if len(kinds) == 0 {
		return m
	}
	out := m
	out.Artifacts = nil
	for _, a := range m.Artifacts {
		if !slices.Contains(kinds, a.Kind) {
			out.Artifacts = append(out.Artifacts, a)
		}
	}
	return out
}
