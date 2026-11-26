package main

import (
	"fmt"
	"io"
	"slices"
	"sort"
	"time"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
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
	if p == nil {
		return nil
	}
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
//
// It also stores the lineage of every seed KB (computeLineage). skipped names
// the KBs the server was told to serve and could not mount (nil when /health
// was not read): a lineage KB in it is held, not unbound.
func deriveWorkspaceBindings(cfg *clientconfig.Config, targets []string, dir string, dryRun bool, skipped []string, errw io.Writer) (*prefetched, widening, func() error, error) {
	before := bindingSnapshot(cfg, targets)
	providers, candidates := derivationScope(cfg, targets)
	var pre *prefetched
	if len(candidates) > 0 {
		cs, failed, err := fetchCandidatesPartial(cfg, candidates)
		if err != nil {
			return nil, widening{}, nil, err
		}
		kbs := make(map[string]bool, len(candidates))
		for _, kb := range candidates {
			kbs[kb] = true
		}
		pre = &prefetched{cs: cs, failed: failed, kbs: kbs}
	}
	resolver := repoindex.NewResolver(cfg.Paths, cfg.ScanRoots(), repoindex.DefaultDepth)
	next := computeDerived(cfg, targets, providers, pre, resolver)
	notes := computeLineage(cfg, pre, &next, skipped)
	for _, kb := range notes.Held {
		fmt.Fprintf(errw, "held %s: the server could not mount it\n", escapeControl(kb))
	}
	for _, kb := range notes.Unavailable {
		fmt.Fprintf(errw, "relations of %s unavailable on the server: lineage left as it was\n", escapeControl(kb))
	}
	for _, kb := range notes.Unlisted {
		fmt.Fprintf(errw, "KB list unknown: lineage of %s left as it was\n", escapeControl(kb))
	}
	if notes.Predates {
		fmt.Fprintln(errw, "server predates KB relations: lineage left as it was")
	}
	// The stored state before this run: what a failed pull puts back, read
	// before SetDerived replaces it.
	stored := cfg.Derived()
	cfg.SetDerived(next)

	// Lineage KBs are pulled once, here, for the manifest build to reuse. A
	// failed one skips its holders (its failure is in pre) while the stored
	// entry of its seeds is put back: stored state never names a KB nothing
	// could apply, whether the failed pull was this one or a candidate's.
	if extra := lineageKBsToPull(cfg, targets, pre); len(extra) > 0 {
		cs, failed, err := fetchCandidatesPartial(cfg, extra)
		if err != nil {
			return nil, widening{}, nil, err
		}
		pre = pre.merge(&prefetched{cs: cs, failed: failed, kbs: map[string]bool{}})
		for _, kb := range extra {
			pre.kbs[kb] = true
		}
	}
	if pre != nil {
		next.Lineage = restoredLineage(stored, next.Lineage, pre.failed, errw)
	}
	widened := diffSnapshots(before, bindingSnapshot(cfg, targets), pre)
	if len(widened.Added)+len(widened.Removed) > 0 {
		next.LastChange = widened.record(time.Now())
	}

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
	return pre, widened, commit, nil
}

// derivationScope returns the targeted providers that derive bindings (in
// workspace scope, able to project) and the KBs to pull for them: the KBs each
// one receives (whose registries derive bindings) plus the lineage seeds, whose
// `relations` are read from the same pulls (lineageSeedsOf).
func derivationScope(cfg *clientconfig.Config, targets []string) (providers, candidates []string) {
	seen := map[string]bool{}
	add := func(kbs []string) {
		for _, kb := range kbs {
			if !seen[kb] {
				seen[kb] = true
				candidates = append(candidates, kb)
			}
		}
	}
	defer func() { sort.Strings(candidates) }()
	for _, p := range targets {
		if own, explicit := cfg.BoundOwnKBs(p); explicit {
			add(own)
		}
		for _, w := range cfg.Workspaces[p] {
			if cfg.DerivesBindings(p) {
				add(w.KBs)
			}
		}
		// A cli provider derives whatever its scope: its bindings narrow the
		// bridge `call` runs, and need no project-local destination (#1804).
		inWorkspace := cfg.WorkspaceScope(p) == clientconfig.ScopeWorkspace && provisioning.SupportsProjectScope(configurator.Provider(p))
		if !inWorkspace && !configurator.UsesCLI(configurator.Provider(p)) {
			continue
		}
		providers = append(providers, p)
		// The transversal KBs are pulled with the rest (so the manifest build
		// reuses the pull) but are never seeds: their relations and paths.yaml
		// are read by nothing, unless the KB is also bound. The held ones are
		// out of the pull set (Config.TransversalKBs).
		add(cfg.TransversalKBs(p))
		// The own list: a lineage KB is never a seed of derivation.
		bound, _ := cfg.BoundOwnKBs(p)
		add(bound)
	}
	return providers, candidates
}

// lineageSeedsOf is every KB whose lineage may be read: the written list of
// every provider with an explicit one, the KBs of every explicit workspace
// binding of a provider that projects them, and the KBs of the derived
// bindings. A lineage KB is never a seed of anything (its closure is already
// inside its seed's), and a provider with no `kbs` (default) has none of its own.
func lineageSeedsOf(cfg *clientconfig.Config, derived map[string][]clientconfig.DerivedBinding) map[string]bool {
	seeds := map[string]bool{}
	for p := range cfg.Clients {
		if own, explicit := cfg.BoundOwnKBs(p); explicit {
			for _, kb := range own {
				seeds[kb] = true
			}
		}
	}
	for p, list := range cfg.Workspaces {
		if !cfg.DerivesBindings(p) {
			continue
		}
		for _, w := range list {
			for _, kb := range w.KBs {
				seeds[kb] = true
			}
		}
	}
	for _, list := range derived {
		for _, b := range list {
			for _, kb := range b.KBs {
				seeds[kb] = true
			}
		}
	}
	return seeds
}

// computeLineage stores in next the lineage of every seed KB, from the
// `relations` of the pulls it holds. An absence is never read as none:
//   - a seed not pulled this run (`sync --client`) or whose pull failed keeps
//     its entry;
//   - a seed pulled with no `relations` (an older server) keeps its entry, and
//     Predates reports it; one whose pull said `relations_unavailable` (the
//     server could not read them, e.g. a fresh clone with no origin/HEAD yet)
//     keeps its entry too, and Unavailable names it;
//   - only an explicit empty `relations` removes an entry;
//   - a stored closure name missing from the new one while it is in skipped (the
//     server could not mount it) is held: the seed's whole entry stays, so the
//     KB stays bound and its failed pull keeps every holder's state (#350).
//
// What the server newly sends is untrusted: names must be valid, mounted
// (cfg.KnownKBs), not the seed, and the closure is capped. Stored data is only
// cleaned on load (clientconfig.LoadDerived). Entries of KBs that are no longer
// a seed of anything are dropped. It also carries LastChange over.
func computeLineage(cfg *clientconfig.Config, pre *prefetched, next *clientconfig.DerivedState, skipped []string) (notes lineageNotes) {
	prev := cfg.Derived()
	next.LastChange = prev.LastChange
	out := map[string]clientconfig.DerivedLineage{}
	for seed := range lineageSeedsOf(cfg, next.Bindings) {
		old, hasOld := prev.Lineage[seed]
		keep := func() {
			if hasOld {
				out[seed] = old
			}
		}
		if !pre.answeredFor(seed) {
			keep()
			continue
		}
		rel, hasRel := pre.cs.Relations[seed]
		if len(cfg.KnownKBs) == 0 {
			// The mounted test below needs the server's KB list, which sync
			// refreshes only when /health answered with one: without it an empty
			// list would filter every name and unbind the stored lineage.
			if hasOld {
				notes.Unlisted = append(notes.Unlisted, seed)
			}
			keep()
			continue
		}
		if pre.cs.RelationsUnavailable[seed] {
			if hasOld {
				notes.Unavailable = append(notes.Unavailable, seed)
			}
			keep()
			continue
		}
		if !hasRel {
			if hasOld {
				notes.Predates = true
			}
			keep()
			continue
		}
		fresh := lineageFromRelations(cfg, seed, rel)
		var missing []string
		for _, k := range old.Closure {
			if !slices.Contains(fresh.Closure, k) && slices.Contains(skipped, k) {
				missing = append(missing, k)
			}
		}
		if len(missing) > 0 {
			for _, k := range missing {
				if !slices.Contains(notes.Held, k) {
					notes.Held = append(notes.Held, k)
				}
			}
			keep()
			continue
		}
		if len(fresh.Closure) > 0 || len(fresh.Unresolved) > 0 {
			out[seed] = fresh
		}
	}
	sort.Strings(notes.Held)
	sort.Strings(notes.Unavailable)
	sort.Strings(notes.Unlisted)
	next.Lineage = nil
	if len(out) > 0 {
		next.Lineage = out
	}
	return notes
}

// lineageNotes is what computeLineage leaves for sync to say.
type lineageNotes struct {
	Held        []string // lineage KBs kept because the server could not mount them
	Unavailable []string // seeds whose relations the server could not read
	Unlisted    []string // seeds kept because the server's KB list is not known this run
	Predates    bool     // a seed answered with no relations: an older server
}

// answeredFor reports whether seed was pulled by this run and answered (a KB
// that answered with no artifacts counts). A nil prefetch answered for nothing,
// and the nameless endpoint carries no KB names to follow.
func (p *prefetched) answeredFor(seed string) bool {
	if p == nil || p.cs.HasBare {
		return false
	}
	_, ok := p.cs.Named[seed]
	return ok
}

// lineageFromRelations is the entry stored for seed from what the server sent.
func lineageFromRelations(cfg *clientconfig.Config, seed string, rel contract.Relations) clientconfig.DerivedLineage {
	var l clientconfig.DerivedLineage
	for _, k := range rel.Closure {
		if len(l.Closure) == clientconfig.MaxLineageClosure {
			break
		}
		if clientconfig.ValidKBName(k) && k != seed && slices.Contains(cfg.KnownKBs, k) && !slices.Contains(l.Closure, k) {
			l.Closure = append(l.Closure, k)
		}
	}
	for _, u := range rel.Unresolved {
		if len(l.Unresolved) == clientconfig.MaxLineageClosure {
			break
		}
		if clientconfig.ValidKBName(u.Name) && clientconfig.ValidLineageReason(u.Reason) && !clientconfig.HasUnresolved(l.Unresolved, u.Name) {
			l.Unresolved = append(l.Unresolved, clientconfig.LineageUnresolved{Name: u.Name, Reason: u.Reason})
		}
	}
	return l
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
		bound, _ := cfg.BoundOwnKBs(p)
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
		decl := map[string]contract.PathDecl{}
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
// untracked CLAUDE.local.md, Antigravity in the untracked
// .agents/rules/cartographer.md (with `trigger: always_on`), and every other
// client gets none.
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
	if provider == string(configurator.ProviderAntigravity) && !tracked[provisioning.AntigravityLocalInstructions] {
		return provisioning.AntigravityLocalInstructions, ""
	}
	return "", rel
}

// withoutInstructions drops the instructions artifacts from m.
func withoutInstructions(m contract.Manifest) contract.Manifest {
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
func withoutKinds(m contract.Manifest, kinds []string) contract.Manifest {
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
