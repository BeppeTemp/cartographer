package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/gitx"
	"github.com/BeppeTemp/cartographer/internal/mcpbridge"
	"github.com/BeppeTemp/cartographer/internal/procattr"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
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
	// state and never means "every KB". For a bundle-only projection they are
	// the provider's transversal KBs, of which it carries the instructions
	// artifact only (forProjection): no skill, agent, hook or MCP descriptor,
	// which stay tied to a binding.
	KBs []string
	// OwnKBs is KBs without the ones brought by lineage: what a printed
	// `workspace bind` command names (a lineage KB is never bound by hand).
	OwnKBs []string
	// BundleOnly marks the residual global projection of a workspace-scoped
	// provider: it carries the transversal bundled skills, the plugins and the
	// instructions artifact of each transversal KB (its KBs), and nothing else
	// KB-sourced. This is decision 5 made into a flag rather than a convention.
	BundleOnly bool
	// Scope is the destination matrix half this projection writes through.
	Scope contract.Scope
	// WorktreesUnlisted marks the projection of a `--worktrees` binding whose
	// linked worktrees could not be listed this run: its worktrees' lock entries
	// are not known to be gone, so the orphan prune leaves that provider's
	// undeclared workspaces alone.
	WorktreesUnlisted bool
	// Unreadable is why the bound directory cannot be listed (#2135), empty
	// when it can. Such a projection is not applied and keeps its recorded
	// state: sync warns, goes on with the others and exits 3.
	Unreadable string
	// Worktree marks the projection into a linked worktree of a `--worktrees`
	// binding; it is recorded on the lock entry (provisioning.Lock.Worktree).
	Worktree bool
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
	// HookSettingsRel is the untracked settings file a derived Claude Code
	// projection registers its hooks in instead of .claude/settings.json
	// (#1198); empty otherwise.
	HookSettingsRel string
	// LineageHints maps each KB this projection holds by lineage to the
	// sentence a collision involving it ends with: where it comes from and the
	// command that opts out. Its keys are the projection's lineage KBs.
	LineageHints map[string]string
}

// ownKBs is the KB list a printed `workspace bind` command names: OwnKBs when
// the projection was built with lineage, else its own KBs (nil OwnKBs means no
// lineage KBs).
func (p syncProjection) ownKBs() []string {
	if p.OwnKBs != nil {
		return p.OwnKBs
	}
	return p.KBs
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

// withholdLocalPaths decides, once for a projection, whether its instructions
// block keeps this machine's absolute paths out — the "Local paths" rows and the
// KB's own placeholders left unexpanded — because the file it goes into is
// tracked by git (#1897): sync (materializeForProviders) and status
// (workspaceProviderDiff) both ask it, so the paths-section hash they judge
// agrees. Only a workspace projection can answer yes; a workspace in no
// repository keeps the paths, and a git that fails withholds them.
func withholdLocalPaths(p syncProjection) bool {
	if p.Scope != contract.ScopeProject || p.Workspace == "" {
		return false
	}
	return provisioning.InstructionsDestTracked(provisioning.ApplyOptions{
		Provider:        configurator.Provider(p.Provider),
		BaseDir:         p.BaseDir,
		Scope:           p.Scope,
		InstructionsRel: p.InstructionsRel,
	})
}

// linkedWorktrees lists a bound folder's linked git worktrees; a variable so a
// test can make the listing fail.
var linkedWorktrees = func(dir string) ([]string, error) { return clientconfig.LinkedWorktrees(dir) }

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
			Provider:     provider,
			BaseDir:      clientBaseDir,
			KBs:          bound,
			Scope:        contract.ScopeGlobal,
			LineageHints: providerLineageHints(cfg, provider),
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
		// The transversal KBs: their instructions only (forProjection).
		KBs:   cfg.TransversalKBs(provider),
		Scope: contract.ScopeGlobal,
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
		unreadable := ""
		if !w.Derived {
			unreadable = clientconfig.WorkspaceUnreadable(w.Path)
		}
		if !w.Derived && unreadable == "" {
			// The recorded remote is the binding's identity (docs/sync.md):
			// a directory now holding another repository is refused like a
			// missing one. A derived binding was matched on its remote when
			// it was computed, in this same run.
			if err := clientconfig.CheckWorkspaceRemote(w); err != nil {
				return nil, fmt.Errorf("%s: %w — rebind it, or remove the binding with `cartographer workspace unbind %s %s`",
					provider, err, provider, w.Path)
			}
		}
		sp := syncProjection{
			Provider:  provider,
			Workspace: w.Path,
			BaseDir:   w.Path,
			Remote:    w.Remote,
			KBs:       w.KBs,
			OwnKBs:    w.OwnKBs(),
			Scope:     contract.ScopeProject,
			Derived:   w.Derived,

			LineageHints: workspaceLineageHints(provider, w),
		}
		if unreadable != "" {
			// Nothing in it can be read: no git, no worktree listing. Its
			// lock entries stay (it is declared), and so do its worktrees'.
			sp.Unreadable = unreadable
			sp.WorktreesUnlisted = w.Worktrees
			out = append(out, sp)
			continue
		}
		if w.Derived {
			sp.InstructionsRel, sp.InstructionsSkipped = derivedInstructions(provider, w.Path)
			sp.SharedSkipped, sp.SkipKinds = derivedSharedSkips(provider, w.Path)
			sp.HookSettingsRel = derivedHookSettings(provider, sp.SkipKinds)
		}
		out = append(out, sp)
		if w.Worktrees && !w.Derived {
			// The same projection, one per linked worktree (same remote, KBs
			// and scope; the remote guard is not re-checked: a linked worktree
			// shares the repository's remote). A failed listing costs only the
			// worktrees, never the main projection.
			wts, err := linkedWorktrees(w.Path)
			if err != nil {
				fmt.Fprintf(os.Stderr, "warning: %s: linked worktrees of %s not listed: %v\n", provider, w.Path, err)
				out[len(out)-1].WorktreesUnlisted = true
			}
			for _, wt := range wts {
				wsp := sp
				wsp.Workspace, wsp.BaseDir = wt, wt
				wsp.Worktree = true
				out = append(out, wsp)
			}
		}
	}
	return out, nil
}

// artifactsForProjection narrows the fetched candidates to what one projection
// may receive. This is WP4's scoping: the strict collision check downstream then
// answers "do two KBs collide **here**", which is the only place a collision can
// actually confuse an agent. Two KBs owning a same-named skill in two different
// workspaces is legal; the same two bound to one workspace is not.
func (cs candidateSet) forProjection(cfg *clientconfig.Config, p syncProjection) []contract.Artifact {
	if p.BundleOnly {
		out := make([]contract.Artifact, 0, len(cs.Bare))
		for _, a := range cs.all() {
			// The bundle and the plugins: a workspace-scoped provider still
			// receives its plugins in the global scope (decision 8).
			if a.Source == "bundle" || contract.IsPluginSource(a.Source) {
				out = append(out, a)
			}
		}
		// A transversal KB's instructions and nothing else of it: its skills,
		// agents, hooks and MCP descriptors stay tied to a binding. A server
		// that does not identify its KBs has no name to match: nothing is
		// added, fail closed as for a workspace.
		if !cs.HasBare {
			permitted := make(map[string]bool, len(p.KBs))
			for _, kb := range p.KBs {
				permitted["kb:"+kb] = true
			}
			for _, a := range cs.forKBs(p.KBs) {
				if a.Kind == "instructions" && permitted[a.Source] {
					out = append(out, a)
				}
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
	var out []contract.Artifact
	for _, a := range cs.forKBs(p.KBs) {
		if permitted[a.Source] {
			out = append(out, a)
		}
	}
	return dropKBArtifacts(cfg, out)
}

// --- `cartographer workspace` ---

func cmdWorkspace(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, workspaceUsage)
		return 2
	}
	if hasHelpFlag(args) || (len(args) == 1 && isHelpArg(args[0])) {
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
       cartographer workspace bind codex <path> --kb <name>[,<name>...] [--trust]    (--trust applies to codex only)
       cartographer workspace bind <provider> <path> --no-kb
       cartographer workspace bind <provider> <path> --kb <name>[,<name>...] --worktrees
       cartographer workspace bind <provider> <path> --kb <name>[,<name>...] --no-lineage | --exclude <name>[,<name>...]
       cartographer workspace unbind <provider> <path>
       cartographer workspace unbind <provider> --all
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
		return "", nil, errors.New(describeClientConfigError(dir, err))
	}
	return dir, cfg, nil
}

// loadWorkspaceConfigLocked is loadWorkspaceConfig for a command that saves: it
// takes the client lock first and loads after it (D172); the caller defers the
// release.
func loadWorkspaceConfigLocked() (string, *clientconfig.Config, func(), error) {
	dir, err := clientconfig.TargetDir()
	if err != nil {
		return "", nil, nil, err
	}
	release, err := lockClient(dir)
	if err != nil {
		return "", nil, nil, err
	}
	cfg, err := clientconfig.Load(dir)
	if err != nil {
		release()
		return "", nil, nil, errors.New(describeClientConfigError(dir, err))
	}
	return dir, cfg, release, nil
}

func cmdWorkspaceBind(args []string) int {
	var provider, path string
	var kbs []string
	var exclude []string
	noKB, trust, worktrees, noLineage := false, false, false, false
	positional := make([]string, 0, 2)
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--no-kb":
			noKB = true
		case args[i] == "--trust":
			trust = true
		case args[i] == "--worktrees":
			worktrees = true
		case args[i] == "--no-lineage":
			noLineage = true
		case args[i] == "--exclude" && i+1 < len(args):
			i++
			exclude = append(exclude, splitCommaList([]string{args[i]})...)
		case strings.HasPrefix(args[i], "--exclude="):
			exclude = append(exclude, splitCommaList([]string{strings.TrimPrefix(args[i], "--exclude=")})...)
		case args[i] == "--exclude": // the one with no value left
			fmt.Fprintln(os.Stderr, "Error: --exclude needs a value: the KB names lineage must not bring")
			return 2
		case args[i] == "--kb" && i+1 < len(args):
			i++
			kbs = append(kbs, splitCommaList([]string{args[i]})...)
		case strings.HasPrefix(args[i], "--kb="):
			kbs = append(kbs, splitCommaList([]string{strings.TrimPrefix(args[i], "--kb=")})...)
		case args[i] == "--kb": // the one with no value left (the case above takes the others)
			fmt.Fprintln(os.Stderr, "Error: --kb needs a value: a KB name, comma-separated names, or --no-kb for none")
			return 2
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
	kbs = dedupe(nonEmpty(kbs))
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
	exclude = dedupe(nonEmpty(exclude))
	if code := checkLineageFlags(kbs, exclude, noKB, noLineage); code != 0 {
		return code
	}

	dir, cfg, release, err := loadWorkspaceConfigLocked()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 2
	}
	defer release()
	p := configurator.Provider(provider)
	if _, ok := configurator.Lookup(p); !ok {
		fmt.Fprintf(os.Stderr, "Error: unknown provider %q\n", provider)
		return 2
	}
	if code := requireConnected(cfg, provider); code != 0 {
		return code
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
		fmt.Fprintf(os.Stderr, "Error: %s has the read-only profile (providers.%s.access with providers.%s.tools), which the workspace bridge does not carry: remove both keys from .cartographer.yaml to bind workspaces, or keep the profile and skip the binding.\n", provider, provider, provider)
		return 2
	}
	if len(kbs) > 0 {
		facts, listErr, refused := checkMountedKBs(cfg, kbs)
		if refused {
			return 2
		}
		if listErr != nil {
			fmt.Fprintf(os.Stderr, "warning: could not check that the server mounts %s (%v); binding anyway\n", strings.Join(kbs, ", "), listErr)
		} else if !facts.Listed {
			fmt.Fprintf(os.Stderr, "warning: the server does not list its KBs; could not check that it mounts %s\n", strings.Join(kbs, ", "))
		}
	}
	if err := cfg.BindWorkspaceWith(provider, path, kbs, clientconfig.BindOptions{Worktrees: worktrees, NoLineage: noLineage, Exclude: exclude}); err != nil {
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
	if switched {
		warnNoWorkspaceDestination(os.Stderr, provider)
	}
	warnBindsHome(os.Stderr, provider, canonical)
	suffix := ""
	if worktrees {
		suffix = " (and its linked worktrees)"
	}
	if len(kbs) == 0 {
		fmt.Printf("bound %s in %s to no KBs%s\n", provider, canonical, suffix)
	} else {
		fmt.Printf("bound %s in %s to %s%s%s\n", provider, canonical, strings.Join(kbs, ", "), lineageSuffix(cfg, provider, canonical), suffix)
		for _, w := range cfg.WorkspaceBindings(provider) {
			if note := lineageInstructionsNote(provider, w); w.Path == canonical && note != "" {
				fmt.Println(note)
			}
		}
		if !noLineage && !lineageKnown(cfg, kbs) {
			fmt.Println("lineage is computed by sync")
		}
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

// checkLineageFlags is the usage check of the lineage flags of a bind (exit 2):
// they are the opt-outs of a binding that names KBs, so a combination that has
// nothing to opt out of, or says the same thing twice, is refused.
func checkLineageFlags(kbs, exclude []string, noKB, noLineage bool) int {
	switch {
	case noLineage && len(exclude) > 0:
		fmt.Fprintln(os.Stderr, "Error: --exclude is redundant with --no-lineage (which follows no relation): give one of the two")
	case noKB && (noLineage || len(exclude) > 0):
		fmt.Fprintln(os.Stderr, "Error: --no-lineage and --exclude need --kb: --no-kb has no KB whose lineage there is to opt out of")
	default:
		for _, k := range exclude {
			if slices.Contains(kbs, k) {
				fmt.Fprintf(os.Stderr, "Error: %s is in both --kb and --exclude: drop it from one of the two\n", k)
				return 2
			}
		}
		return 0
	}
	return 2
}

// lineageKnown reports whether the stored lineage has an entry for one of kbs.
func lineageKnown(cfg *clientconfig.Config, kbs []string) bool {
	for _, k := range kbs {
		if _, ok := cfg.Derived().Lineage[k]; ok {
			return true
		}
	}
	return false
}

// lineageSuffix is the ` (and core-kb by lineage)` of the bind line: the KBs
// the stored lineage adds to the binding just saved at canonical.
func lineageSuffix(cfg *clientconfig.Config, provider, canonical string) string {
	for _, w := range cfg.WorkspaceBindings(provider) {
		if w.Path != canonical {
			continue
		}
		var added []string
		for _, k := range w.KBs {
			if _, ok := w.Lineage[k]; ok {
				added = append(added, k)
			}
		}
		if len(added) > 0 {
			return " (and " + strings.Join(added, ", ") + " by lineage)"
		}
	}
	return ""
}

// bindCommand is the `workspace bind` command that rebinds path as w is bound:
// the own KBs only (a rebind that wrote the effective set would turn lineage
// KBs into hand-bound ones), and the lineage options when w carries them, since
// a rebind that does not repeat them turns them off. A derived binding has none.
func bindCommand(provider, path string, w clientconfig.WorkspaceBinding) string {
	cmd := "cartographer workspace bind " + provider + " " + path
	own := w.OwnKBs()
	if len(own) == 0 {
		return cmd + " --no-kb"
	}
	cmd += " --kb " + strings.Join(own, ",")
	switch {
	case w.NoLineage:
		cmd += " --no-lineage"
	case len(w.Exclude) > 0:
		cmd += " --exclude " + strings.Join(w.Exclude, ",")
	}
	return cmd
}

// lineageLabel is the list column's `+ core-kb (lineage of client-kb)`.
func lineageLabel(w clientconfig.WorkspaceBinding) string {
	var sb strings.Builder
	for _, k := range w.KBs {
		if seeds, ok := w.Lineage[k]; ok {
			fmt.Fprintf(&sb, "  + %s (lineage of %s)", k, strings.Join(seeds, ", "))
		}
	}
	return sb.String()
}

// resolveLineageLine says which KBs of the winning binding came by lineage and
// the command that opts out of them: an explicit binding on the path that
// repeats the options the binding already has.
func resolveLineageLine(provider string, w clientconfig.WorkspaceBinding) string {
	var from, names []string
	for _, k := range w.KBs {
		if seeds, ok := w.Lineage[k]; ok {
			from = append(from, k+" from "+strings.Join(seeds, ", "))
			names = append(names, k)
		}
	}
	if len(from) == 0 {
		return ""
	}
	opt := w
	opt.NoLineage = false
	opt.Exclude = dedupe(append(slices.Clone(w.Exclude), names...))
	return fmt.Sprintf("  lineage: %s (opt out: %s)\n", strings.Join(from, ", "), strings.TrimPrefix(bindCommand(provider, w.Path, opt), "cartographer "))
}

// warnNoWorkspaceDestination tells, when provider enters workspace scope, which
// artifact kinds the client has globally but no workspace destination for: the
// global projection is bundle-only, so the KBs' artifacts of those kinds are
// installed nowhere (#1785). Shared by `workspace bind` and `connect --workspace`.
func warnNoWorkspaceDestination(w io.Writer, provider string) {
	kinds := provisioning.WorkspaceLacksKinds(configurator.Provider(provider))
	if len(kinds) == 0 {
		return
	}
	fmt.Fprintf(w, "warning: %s has no workspace destination for %s: the KBs' artifacts of those kinds are not installed in workspace scope\n",
		provider, strings.Join(kinds, ", "))
}

// warnBindsHome warns when canonical, the bound folder, is the home directory
// itself (#1825): the binding is legal (D737), but everything the KB distributes
// then reaches every session of provider. No home, or one that does not resolve,
// means no warning.
func warnBindsHome(w io.Writer, provider, canonical string) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return
	}
	if resolved, err := filepath.EvalSymlinks(home); err == nil {
		home = resolved
	}
	if canonical == "" || filepath.Clean(canonical) != filepath.Clean(home) {
		return
	}
	fmt.Fprintf(w, "warning: %s is bound to your home directory: everything the KB distributes now reaches every session of %s, whatever the folder; bind a narrower folder (for example $HOME/projects/<area>) to keep it there; to have only a KB's instructions and read access everywhere, run cartographer client kb <kb> --transversal=true\n", provider, provider)
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
	if len(args) == 2 && args[1] == "--all" {
		return cmdWorkspaceUnbindAll(args[0])
	}
	if len(args) != 2 {
		fmt.Fprintln(os.Stderr, workspaceUsage)
		return 2
	}
	provider, path := args[0], args[1]
	dir, cfg, release, err := loadWorkspaceConfigLocked()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 2
	}
	defer release()
	// The same path UnbindWorkspace compares against: canonical, else absolute.
	canonical, err := clientconfig.CanonicalWorkspacePath(path)
	if err != nil {
		canonical, _ = filepath.Abs(path)
	}
	explicit, derivedHere := false, false
	for _, w := range cfg.Workspaces[provider] {
		explicit = explicit || w.Path == canonical
	}
	for _, w := range cfg.WorkspaceBindings(provider) {
		derivedHere = derivedHere || (w.Derived && w.Path == canonical && !explicit)
	}
	if !explicit && !derivedHere {
		// Nothing to remove: saying "unbound" would hide a mistyped path or provider.
		fmt.Fprintf(os.Stderr, "Error: %s has no workspace binding for %s (see `cartographer workspace list %s`)\n", provider, path, provider)
		return 1
	}
	if derivedHere {
		// Nothing is removed, so nothing is saved and no success line follows:
		// the binding stays and a sync would remove nothing.
		fmt.Fprintf(os.Stderr, "note: %s is bound by a KB's paths.yaml, not by a `workspace bind`, so there is nothing to unbind; "+
			"`cartographer workspace bind %s %s --no-kb` opts it out on this machine, `bind: false` in the KB's paths.yaml for everyone\n", path, provider, path)
		return 1
	}
	if err := cfg.UnbindWorkspace(provider, path); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 2
	}
	if len(cfg.Workspaces[provider]) == 0 && len(cfg.WorkspaceBindings(provider)) > 0 {
		// Derived bindings are the KB's own ask: the scope stays (#1784).
		fmt.Printf("note: %s stays in workspace scope through %d bindings derived from KB paths.yaml; run cartographer workspace unbind %s --all to return it to the global scope\n",
			provider, len(cfg.WorkspaceBindings(provider)), provider)
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

// cmdWorkspaceUnbindAll removes every explicit binding of provider and returns
// it to the global scope; derived bindings exist only in workspace scope, so
// they go with it.
func cmdWorkspaceUnbindAll(provider string) int {
	dir, cfg, release, err := loadWorkspaceConfigLocked()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 2
	}
	defer release()
	if _, ok := configurator.Lookup(configurator.Provider(provider)); !ok {
		fmt.Fprintf(os.Stderr, "Error: unknown provider %q\n", provider)
		return 1
	}
	inScope := cfg.WorkspaceScope(provider) == clientconfig.ScopeWorkspace
	if !inScope && len(cfg.Workspaces[provider]) == 0 {
		fmt.Printf("%s is not in workspace scope: nothing to unbind\n", provider)
		return 0
	}
	if !inScope {
		// A hand-edited file: explicit entries outside workspace scope would
		// come alive again at the next bind.
		fmt.Printf("%s is not in workspace scope, but holds %d explicit bindings: removing them\n", provider, len(cfg.Workspaces[provider]))
	}
	for _, w := range append([]clientconfig.WorkspaceBinding(nil), cfg.Workspaces[provider]...) {
		if err := cfg.UnbindWorkspace(provider, w.Path); err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			return 2
		}
	}
	if err := cfg.SetWorkspaceScope(provider, clientconfig.ScopeProvider); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 2
	}
	if err := clientconfig.Save(dir, cfg); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 2
	}
	fmt.Printf("unbound every workspace of %s: it returns to the global scope.\n", provider)
	fmt.Println("run `cartographer sync` to prune the workspace projections and restore the global catalogue")
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
		if _, ok := configurator.Lookup(configurator.Provider(args[0])); !ok {
			fmt.Fprintf(os.Stderr, "Error: unknown provider %q\n", args[0])
			return 2
		}
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
			if own := w.OwnKBs(); len(own) > 0 {
				kbs = strings.Join(own, ", ")
			}
			if src := sourcesLabel(w); src != "" {
				state += "  [derived: " + src + "]"
			}
			kbs += lineageLabel(w)
			if w.NoLineage {
				kbs += "  [no lineage]"
			} else if len(w.Exclude) > 0 {
				kbs += "  [exclude " + strings.Join(w.Exclude, ", ") + "]"
			}
			if w.Worktrees {
				kbs += "  [worktrees]"
			}
			fmt.Printf("  %s  →  %s%s\n", w.Path, kbs, state)
			if w.Remote != "" {
				fmt.Printf("      remote %s\n", gitx.RedactRemoteURL(w.Remote))
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
		if _, ok := configurator.Lookup(configurator.Provider(provider)); !ok {
			fmt.Fprintf(os.Stderr, "Error: unknown provider %q\n", provider)
			return 2
		}
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
		entry, label := "http", "global MCP entry: http"
		switch {
		case configurator.UsesCLI(configurator.Provider(p)):
			// No MCP entry: `cartographer call` runs the bridge itself, so the
			// folder's bindings narrow it (#1804).
			entry, label = "cli", "MCP mode: cli, served by `cartographer call`"
		case cfg.UsesBridge(p, cfg.ServerRoutedPath):
			entry, label = "bridge", "global MCP entry: bridge"
		}
		fmt.Fprintf(&sb, "%s  (%s)\n", p, label)
		switch {
		case scope.Err != nil:
			fmt.Fprintf(&sb, "  unusable binding: %v\n  no KB is served here until it is fixed\n", scope.Err)
		case scope.Binding != nil:
			kbs := "no KB"
			if own := without(scope.KBs, scope.Transversal); len(own) > 0 {
				kbs = strings.Join(own, ", ")
			}
			if scope.Binding.Derived {
				fmt.Fprintf(&sb, "  bound by %s  ->  %s  (derived from %s; `workspace bind %s %s --no-kb` opts out here)\n",
					scope.Binding.Path, kbs, sourcesLabel(*scope.Binding), p, scope.Binding.Path)
			} else {
				fmt.Fprintf(&sb, "  bound by %s  ->  %s\n", scope.Binding.Path, kbs)
			}
			sb.WriteString(resolveLineageLine(p, *scope.Binding))
		case scope.All:
			fmt.Fprintf(&sb, "  unbound: the provider's default set (every KB)\n")
		default:
			kbs := "no KB"
			if own := without(scope.KBs, scope.Transversal); len(own) > 0 {
				kbs = strings.Join(own, ", ")
			}
			fmt.Fprintf(&sb, "  unbound: the provider's binding  ->  %s\n", kbs)
		}
		if len(scope.Transversal) > 0 && scope.Err == nil {
			fmt.Fprintf(&sb, "  + %s read-only (transversal: `client kb <kb> --transversal=false` clears it)\n", strings.Join(scope.Transversal, ", "))
		}
		// Only a bridge entry carries the mode: an http entry is not narrowed
		// by any binding, so it is not made read-only either.
		if entry == "bridge" && scope.Unbound && scope.Err == nil {
			if scope.ReadOnly {
				fmt.Fprintf(&sb, "  access: read-only (%s)\n  no write tool and no KB artifact here; bind the folder to write, or set `defaults.unbound_access` to write\n", scope.AccessOrigin)
			} else {
				fmt.Fprintf(&sb, "  access: read-write (%s)\n  no KB artifact here; bind the folder for the KB's skills and agents\n", scope.AccessOrigin)
			}
		}
		if entry == "bridge" && cfg.PluginOnlyBridge(p, cfg.ServerRoutedPath) {
			fmt.Fprintf(&sb, "  the bridge is here for plugin tools only: this session has the access of the http entry\n")
			continue
		}
		if entry == "bridge" && scope.Binding == nil && scope.Err == nil {
			fmt.Fprintf(&sb, "  a client that declares MCP roots can still bind this session through them\n")
		}
		if entry == "http" {
			fmt.Fprintf(&sb, "  this client has no bridge entry, so bindings do not narrow its session\n")
			if names := transversalWithoutAccess(cfg, p); len(names) > 0 {
				fmt.Fprintf(&sb, "  transversal %s: instructions only, no read access here (the HTTP entry serves the provider's own KBs; bind a folder to give the provider the bridge)\n", strings.Join(names, ", "))
			}
		}
	}
	return sb.String()
}

// transversalWithoutAccess is the transversal KBs of provider that its HTTP
// entry does not serve: the entry carries the provider's own KBs, and only the
// bridge adds a transversal KB to a session.
func transversalWithoutAccess(cfg *clientconfig.Config, provider string) []string {
	own, explicit := cfg.BoundKBs(provider)
	if !explicit {
		return nil
	}
	var out []string
	for _, kb := range cfg.TransversalKBs(provider) {
		if !slices.Contains(own, kb) {
			out = append(out, kb)
		}
	}
	return out
}

// without returns list minus the names in drop, in order.
func without(list, drop []string) []string {
	var out []string
	for _, v := range list {
		if !slices.Contains(drop, v) {
			out = append(out, v)
		}
	}
	return out
}

// dedupe keeps the first occurrence of each value, in order.
func dedupe(values []string) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		if !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return out
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

// splitProjectionKey is projectionKey's inverse: the provider and the workspace
// ("" for the global projection) a results key stands for.
func splitProjectionKey(key string) (provider, workspace string) {
	provider, workspace, _ = strings.Cut(key, "\x00")
	return provider, workspace
}

// projectionLabel renders a results key for a user-facing line: the provider
// alone for the global projection, `<provider> <workspace>` for a workspace one,
// the home directory as $HOME. The key itself carries a NUL, which turns piped
// output into binary for grep (#1200).
func projectionLabel(key string) string {
	provider, workspace := splitProjectionKey(key)
	if workspace == "" {
		return provider
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if workspace == home {
			workspace = "$HOME"
		} else if rest, ok := strings.CutPrefix(filepath.ToSlash(workspace), filepath.ToSlash(home)+"/"); ok {
			workspace = "$HOME/" + rest
		}
	}
	return provider + " " + workspace
}

// globalProjection is the single projection a default-scoped provider has. It
// is the shape every caller had before workspaces existed.
func globalProjection(provider, baseDir string, kbs []string) syncProjection {
	return syncProjection{Provider: provider, BaseDir: baseDir, KBs: kbs, Scope: contract.ScopeGlobal}
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

// providerFailure is a provider whose projections could not be resolved: an
// explicit workspace binding whose directory is gone, a provider that cannot be
// scoped to a workspace.
type providerFailure struct {
	Provider string
	Err      error
}

// allProjectionsPartial is allProjections for sync (#1502): a provider whose
// projections cannot be resolved costs only itself, as a KB that cannot be pulled
// costs only the providers bound to it (D257). It is left out of the result and
// reported; every other provider keeps its projections.
func allProjectionsPartial(cfg *clientconfig.Config, providers []string, clientBaseDir string) ([]syncProjection, []providerFailure) {
	var out []syncProjection
	var failed []providerFailure
	sorted := append([]string(nil), providers...)
	sort.Strings(sorted)
	for _, provider := range sorted {
		ps, err := projectionsFor(cfg, provider, clientBaseDir)
		if err != nil {
			failed = append(failed, providerFailure{Provider: provider, Err: err})
			continue
		}
		out = append(out, ps...)
	}
	return out, failed
}

// manifestsForProjections builds one manifest per projection, each merged
// strictly over **that projection's** KBs.
//
// That narrowing is WP4's whole content. The strict merge (D171) then answers
// "do two KBs collide here", which is the only place a collision can actually
// confuse an agent: two KBs owning a same-named skill in two different
// workspaces is legal and must stay legal, while the same two bound to one
// workspace is still refused.
func manifestsForProjections(cfg *clientconfig.Config, projections []syncProjection) (map[string]contract.Manifest, error) {
	out, _, err := manifestsWithRelations(cfg, projections)
	return out, err
}

// manifestsWithRelations is manifestsForProjections that also returns the
// `relations` of the pulls it made, for `status` to compare without pulling again.
func manifestsWithRelations(cfg *clientconfig.Config, projections []syncProjection) (map[string]contract.Manifest, map[string]contract.Relations, error) {
	out, _, rels, err := buildProjectionManifests(syncStreams{}, cfg, projections, false, nil)
	return out, rels, err
}

// manifestsForProjectionsPartial is manifestsForProjections for sync (#350):
// a KB whose pull fails does not stop the run. Every projection that would
// receive that KB is left out of the returned map — so it is not applied and
// keeps its previous state — and the failures come back in KB order for the
// caller to report. A projection that needs no failed KB is built exactly as
// manifestsForProjections builds it.
func manifestsForProjectionsPartial(st syncStreams, cfg *clientconfig.Config, projections []syncProjection, pre *prefetched) (map[string]contract.Manifest, []kbPullFailure, error) {
	out, failed, _, err := buildProjectionManifests(st, cfg, projections, true, pre)
	return out, failed, err
}

// buildProjectionManifests builds one manifest per projection. pre, when it
// covers every KB the projections ask for, replaces the pulls (D1019: the
// derivation already made them); nil pulls here.
func buildProjectionManifests(st syncStreams, cfg *clientconfig.Config, projections []syncProjection, partial bool, pre *prefetched) (map[string]contract.Manifest, []kbPullFailure, map[string]contract.Relations, error) {
	out := make(map[string]contract.Manifest, len(projections))

	// Every KB any projection asks for, pulled once.
	seen := map[string]bool{}
	var union []string
	anyDefault := false
	requiredBy := map[string][]string{}
	for _, p := range projections {
		// A bundle-only projection pulls nothing of its own but the transversal
		// KBs it carries the instructions of, which may be bound nowhere: they
		// join the pulls (and are no reason for the default-scope second pull).
		if p.Workspace == "" && !p.BundleOnly {
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

	reported := map[string]bool{}
	report := func(c provisioning.PluginCollision) {
		if line := describePluginCollision(c); !reported[line] {
			reported[line] = true
			fmt.Fprintln(st.err(), line)
		}
	}
	if len(union) == 0 && !anyDefault {
		// No KB to pull, but the machine's plugins still reach its global scope.
		plugins := loadActivePlugins(cfg).Artifacts
		for _, p := range projections {
			if p.Workspace != "" || len(plugins) == 0 {
				out[projectionKey(p)] = contract.MergeArtifacts(nil)
				continue
			}
			merged, err := mergeAcrossPlugins(plugins, report)
			if err != nil {
				return nil, nil, nil, fmt.Errorf("%s: %w", p.Label(), err)
			}
			verified, err := provisioning.VerifiedManifest(merged, nil)
			if err != nil {
				return nil, nil, nil, fmt.Errorf("%s: %w", p.Label(), err)
			}
			out[projectionKey(p)] = verified
		}
		// A bundle-only projection still needs the bundle, which lives on the
		// server side of a sync_pull: with no KB to pull it from there is
		// nothing to fetch, and an empty manifest is the honest answer.
		return out, nil, nil, nil
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
		return nil, nil, nil, annotateStaleBinding(err, requiredBy)
	}
	unavailable := make(map[string]bool, len(failed))
	for _, f := range failed {
		unavailable[f.KB] = true
	}
	if cs.HasBare {
		fmt.Fprintln(st.err(), "warning: the server does not identify its KBs by name, so per-client KB bindings cannot be applied; every connected client receives everything it serves")
	}
	pins, err := pinnedPublicKeys(cfg)
	if err != nil {
		return nil, nil, nil, err
	}
	for _, p := range projections {
		if needsAny(p.KBs, unavailable) {
			continue
		}
		arts, pi := cs.forProjection(cfg, p), p
		if lineageInstructionsWithheld(p) {
			// A KB that is not hand-bound never adds to a block git tracks:
			// its instructions and its feedback mode are left out, while its
			// skills, agents, hooks and MCP scope stay. Its path registries,
			// placeholders and issues are read from the whole projection p:
			// its artifacts are expanded with them and its pull issues are
			// the operator's to see; the block's "Local paths" table is
			// emptied by WithholdLocalPaths.
			arts = withoutInstructionsOf(arts, p.lineageKBs())
			pi.KBs = slices.DeleteFunc(slices.Clone(p.KBs), func(k string) bool { return slices.Contains(p.lineageKBs(), k) })
		}
		merged, err := mergeAcrossPlugins(arts, report)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("%s: %w%s", p.Label(), err, lineageCollisionHint(p, err))
		}
		verified, err := provisioning.VerifiedManifest(merged, pins)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("%s: %w", p.Label(), err)
		}
		verified = withoutCLISkill(p.Provider, withoutOptedOutSkillsLines(cfg, verified))
		// Set after verification, which copies only the signed catalogue.
		verified.Placeholders = cs.placeholdersForProjection(cfg, p)
		verified.PathRegistries = cs.pathRegistriesForProjection(cfg, p)
		verified.FeedbackMode = cs.feedbackForProjection(cfg, pi)
		verified.KBIssues, verified.ReadKBs = cs.issuesForProjection(cfg, p)
		out[projectionKey(p)] = verified
	}
	return out, failed, cs.Relations, nil
}

// lineageKBs are the KBs the projection holds by lineage, sorted.
func (p syncProjection) lineageKBs() []string {
	out := make([]string, 0, len(p.LineageHints))
	for k := range p.LineageHints {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// lineageInstructionsWithheld reports whether the lineage KBs of p leave the
// instructions block alone: it goes into a file git tracks, which only a KB the
// user bound by hand may add to (same test as withholdLocalPaths, so sync and
// status agree). A projection with no lineage KB, or unreadable, never asks git.
func lineageInstructionsWithheld(p syncProjection) bool {
	return len(p.LineageHints) > 0 && p.Unreadable == "" && withholdLocalPaths(p)
}

// withoutInstructionsOf drops the instructions artifacts of the named KBs.
func withoutInstructionsOf(arts []contract.Artifact, kbs []string) []contract.Artifact {
	out := make([]contract.Artifact, 0, len(arts))
	for _, a := range arts {
		if a.Kind == "instructions" && slices.Contains(kbs, strings.TrimPrefix(a.Source, "kb:")) && strings.HasPrefix(a.Source, "kb:") {
			continue
		}
		out = append(out, a)
	}
	return out
}

// lineageHint is the sentence a collision involving a lineage KB ends with.
func lineageHint(kb string, seeds []string, optOut string) string {
	return fmt.Sprintf("%s comes by lineage from %s; opt out with: %s", kb, strings.Join(seeds, ", "), optOut)
}

// workspaceLineageHints is the hint of each lineage KB of binding w.
func workspaceLineageHints(provider string, w clientconfig.WorkspaceBinding) map[string]string {
	var out map[string]string
	for _, k := range w.KBs {
		if seeds, ok := w.Lineage[k]; ok {
			opt := w
			opt.NoLineage = false
			opt.Exclude = dedupe(append(slices.Clone(w.Exclude), k))
			if out == nil {
				out = map[string]string{}
			}
			out[k] = lineageHint(k, seeds, bindCommand(provider, w.Path, opt))
		}
	}
	return out
}

// providerLineageHints is the same for a provider-scope binding.
func providerLineageHints(cfg *clientconfig.Config, provider string) map[string]string {
	var out map[string]string
	for k, seeds := range providerLineage(cfg, provider) {
		if out == nil {
			out = map[string]string{}
		}
		opt := *cfg
		opt.Clients = maps.Clone(cfg.Clients)
		b := opt.Clients[provider]
		b.NoLineage = false
		b.Exclude = dedupe(append(slices.Clone(b.Exclude), k))
		opt.Clients[provider] = b
		out[k] = lineageHint(k, seeds, providerBindCommand(&opt, provider))
	}
	return out
}

// lineageCollisionHint is the line a projection's collision error gains when it
// involves a KB the projection holds by lineage: the error and the projections
// it refuses are unchanged (D171), the operator learns where the KB came from.
func lineageCollisionHint(p syncProjection, err error) string {
	var ce *provisioning.CollisionError
	if len(p.LineageHints) == 0 || !errors.As(err, &ce) {
		return ""
	}
	var lines []string
	for _, k := range p.lineageKBs() {
		src := "kb:" + k
		if slices.ContainsFunc(ce.Collisions, func(c provisioning.Collision) bool { return slices.Contains(c.Sources, src) }) ||
			slices.ContainsFunc(ce.Directives, func(d provisioning.DirectiveCollision) bool {
				return slices.ContainsFunc(d.Values, func(v provisioning.DirectiveValue) bool { return v.Source == src })
			}) {
			lines = append(lines, p.LineageHints[k])
		}
	}
	if len(lines) == 0 {
		return ""
	}
	return "\n" + strings.Join(lines, "\n")
}

// lineageInstructionsNote says, for a binding that holds KBs by lineage, that
// their instructions are not written because the file is tracked by git ("" when
// they are, or the binding has none). Status and `workspace bind` print it.
func lineageInstructionsNote(provider string, w clientconfig.WorkspaceBinding) string {
	hints := workspaceLineageHints(provider, w)
	if len(hints) == 0 {
		return ""
	}
	sp := syncProjection{Provider: provider, Workspace: w.Path, BaseDir: w.Path, Scope: contract.ScopeProject, LineageHints: hints}
	if w.Derived {
		var skipped string
		sp.InstructionsRel, skipped = derivedInstructions(provider, w.Path)
		if skipped != "" {
			return "" // a derived binding writes no instructions at all, said elsewhere
		}
	}
	if !lineageInstructionsWithheld(sp) {
		return ""
	}
	file := sp.InstructionsRel
	if file == "" {
		file = provisioning.ProjectDestination("instructions", "", configurator.Provider(provider))
	}
	return fmt.Sprintf("instructions of %s (lineage) not written: %s is tracked by git", strings.Join(sp.lineageKBs(), ", "), file)
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
func prepareWorkspace(st syncStreams, p syncProjection, providers []string, dryRun bool, owned ownedPathsFunc, local ...string) error {
	if p.Scope != contract.ScopeProject {
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
	if err := writeWorkspaceExclusions(p.Workspace, providers, owned, local...); err != nil {
		if provisioning.ExcludeWarning(err) {
			// The exclude block is a convenience, not what the sync is for: a
			// linked .git (D148) costs a warning and a dirty working tree, not
			// the projection.
			fmt.Fprintf(st.err(), "warning: workspace %s: git exclude left unchanged: %v\n", p.Workspace, err)
			return nil
		}
		return err
	}
	return nil
}

// refreshWorkspaceExclusions rewrites, after Apply, the exclude block of every
// workspace a projection of this run was applied in, with the ownership the new
// lock and the manifests give (#2372). A failure is a warning like the one
// before Apply: the block is a convenience, not what the sync is for.
func refreshWorkspaceExclusions(st syncStreams, projections []syncProjection, skipped map[string]bool, lockFile *provisioning.LockFile, manifests map[string]contract.Manifest) {
	done := map[string]bool{}
	for _, p := range projections {
		if p.Scope != contract.ScopeProject || skipped[projectionKey(p)] || done[p.Workspace] {
			continue
		}
		done[p.Workspace] = true
		err := writeWorkspaceExclusions(p.Workspace, workspaceProviders(*lockFile, projections, p.Workspace), workspaceOwnership(lockFile, projections, manifests, p.Workspace), derivedLocalFiles(projections, p.Workspace)...)
		if err != nil {
			fmt.Fprintf(st.err(), "warning: workspace %s: git exclude left unchanged: %v\n", p.Workspace, err)
		}
	}
}

// derivedLocalFiles are the untracked files the derived projections of
// workspace write this run (their instructions block, D1019, and Claude Code
// hook registrations, D1198). The exclude block is rewritten whole by each
// projection of the workspace in turn, so every one lists all of them, not
// only its own (#1275).
func derivedLocalFiles(projections []syncProjection, workspace string) []string {
	var out []string
	for _, q := range projections {
		if q.Scope != contract.ScopeProject || q.Workspace != workspace {
			continue
		}
		for _, rel := range []string{q.InstructionsRel, q.HookSettingsRel} {
			if rel != "" && !slices.Contains(out, rel) {
				out = append(out, rel)
			}
		}
	}
	return out
}

// ownedPathsFunc returns the workspace-relative paths a provider's projection
// owns in one workspace (D1181).
type ownedPathsFunc func(provider configurator.Provider) ([]string, error)

// workspaceOwnership resolves what each provider's projection of workspace owns:
// the artifacts this run plans for it (manifests, keyed as projectionKey) and
// the paths the lockfile records for it. A provider with neither owns only its
// shared files, so a repository's own artifacts are never claimed.
func workspaceOwnership(lockFile *provisioning.LockFile, projections []syncProjection, manifests map[string]contract.Manifest, workspace string) ownedPathsFunc {
	return func(provider configurator.Provider) ([]string, error) {
		var m contract.Manifest
		var notWritten []string
		for _, p := range projections {
			if p.Scope == contract.ScopeProject && p.Workspace == workspace && p.Provider == string(provider) {
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
	// The hook registration file a repository may track (Codex's hooks.json) is
	// patched, not owned whole, so it is treated like the instructions file.
	return append(blocks, provisioning.ProjectSharedHookFiles(provider)...)
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
	// The untracked files a derived projection puts its instructions block
	// (D1019) and its Claude Code hook registrations (#1198) in are
	// Cartographer's own, so they are excluded like its directories:
	// the one this run is about to write (extra) and the ones earlier runs
	// wrote, which the block they hold gives away.
	excluded := slices.Clone(extra)
	for _, name := range providers {
		provider := configurator.Provider(name)
		excluded = append(excluded, provisioning.OwnedLocalInstructions(workspace, provider)...)
		// The record of what Cartographer created here (settings files, #1269;
		// the provider roots it made, any provider's) is its own file:
		// untracked, so excluded, never committed.
		excluded = append(excluded, configurator.CreatedLedgerName)
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
		if p.Scope == contract.ScopeProject && p.Workspace == workspace && !seen[p.Provider] {
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
		st := workspaceStatus{Path: w.Path, KBs: w.KBs, State: "ok", Derived: w.Derived, Source: sourcesLabel(w), Bind: bindCommand(provider, w.Path, w), Lineage: w.Lineage}
		if !w.NoLineage {
			st.LineageUnresolved = storedUnresolved(cfg, w.OwnKBs())
		}
		if info, err := os.Stat(w.Path); err != nil || !info.IsDir() {
			st.State, st.Detail = "gone", "the bound directory is not there any more"
			if w.Derived {
				st.Detail += "; the next sync drops this derived binding"
			}
			out = append(out, st)
			continue
		}
		// A directory the process may not list is not a moved one: the git remote
		// cannot be read, which says nothing about where it points (#2216).
		if why := clientconfig.WorkspaceUnreadable(w.Path); why != "" {
			st.State, st.Detail = "no access", why
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
				// Appended: for a Codex binding that is not trusted the detail
				// already says why it is inactive, and the fix points at it.
				note := fmt.Sprintf("instructions not written: %s is tracked by git and a derived binding never edits a tracked file — `%s` writes the block there",
					skipped, st.Bind)
				if st.Detail != "" {
					note = st.Detail + "; " + note
				}
				st.Detail = note
			}
		}
		if note := lineageInstructionsNote(provider, w); note != "" {
			if st.Detail != "" {
				note = st.Detail + "; " + note
			}
			st.Detail = note
		}
		if w.Derived {
			if files, kinds := derivedSharedSkips(provider, w.Path); len(files) > 0 {
				note := trackedMCPNote(files, provider, w.Path, w.OwnKBs())
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
		if w.Worktrees && !w.Derived {
			wts, err := linkedWorktrees(w.Path)
			if err != nil {
				// Not "no worktrees": say the listing is unknown.
				note := "linked worktrees not listed: " + err.Error()
				if d := out[len(out)-1].Detail; d != "" {
					note = d + "; " + note
				}
				out[len(out)-1].Detail = note
			}
			for _, wt := range wts {
				out = append(out, workspaceStatus{Path: wt, KBs: w.KBs, State: "ok", Source: "worktree of " + w.Path})
			}
		}
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
		printLineageLines(w.Lineage, w.LineageUnresolved)
	}
}

// checkWorkspaceProjections is `doctor`'s view of the workspace bindings
// (D193 WP7). Each state reported here has a distinct fix, and none of them is
// healed on the spot.
func checkWorkspaceProjections(dir string, cfg *clientconfig.Config, providers []string) []doctorFinding {
	lockFile, _ := provisioning.ReadLockFile(lockFilePath(dir))
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
			out = append(out, symlinkedDestinationFindings(provider, w.Path, provisioning.ManagedProjectRoots(configurator.Provider(provider), w.Path))...)
			if w.Derived && w.State == "ok" {
				out = append(out, trackedClaudeHookSettings(provider, w.Path, lockFile)...)
			}
			if w.Derived && w.State == "ok" && w.Detail != "" {
				out = append(out, doctorFinding{
					Check: "workspace", Severity: doctorWarning, Path: w.Path,
					Message: fmt.Sprintf("%s: %s", provider, w.Detail),
					Fix:     w.Bind,
				})
				continue
			}
			if w.Derived && (w.State == "gone" || w.State == "moved" || w.State == "unresolved" || w.State == "no access") {
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
			if w.State == "ok" || w.State == "inactive" {
				if files := uncoveredProjectionFiles(configurator.Provider(provider), w.Path, lockFile.ForProjection(provisioning.Projection{Provider: provider, Workspace: w.Path})); len(files) > 0 {
					out = append(out, doctorFinding{
						Check: "workspace", Severity: doctorWarning, Path: w.Path,
						Message: fmt.Sprintf("%s: %d untracked file(s) under the provider's directories are not in the exclude block and dirty the working tree: %s",
							provider, len(files), joinCapped(files, 5)),
						Fix: "cartographer sync (rewrites the block); a file that is yours stays yours: commit it or add it to .gitignore",
					})
				}
			}
			switch w.State {
			case "gone":
				out = append(out, doctorFinding{
					Check: "workspace", Severity: doctorError, Path: w.Path,
					Message: fmt.Sprintf("%s: the bound workspace is not there any more", provider),
					Fix:     fmt.Sprintf("cartographer workspace unbind %s %s", provider, w.Path),
				})
			case "no access":
				out = append(out, doctorFinding{
					Check: "workspace", Severity: doctorWarning, Path: w.Path,
					Message: fmt.Sprintf("%s: %s", provider, w.Detail),
					Fix:     clientconfig.GrantAccessHint,
				})
			case "moved":
				out = append(out, doctorFinding{
					Check: "workspace", Severity: doctorError, Path: w.Path,
					Message: fmt.Sprintf("%s: %s", provider, w.Detail),
					Fix:     w.Bind,
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

// trackedClaudeHookSettings is the doctor finding of a derived Claude Code
// workspace whose tracked .claude/settings.json holds hook entries Cartographer
// wrote (#1198): a derived binding registers in .claude/settings.local.json, so
// those are left over from an earlier version, edit a versioned file and fire
// beside the new registration. Removing them is the user's call: sync never
// edits a tracked file for a derived binding.
func trackedClaudeHookSettings(provider, workspace string, lockFile provisioning.LockFile) []doctorFinding {
	if provider != string(configurator.ProviderClaudeCode) {
		return nil
	}
	const rel = ".claude/settings.json"
	tracked, err := provisioning.TrackedPaths(workspace)
	if err != nil || !tracked[rel] {
		return nil
	}
	lock := lockFile.ForProjection(provisioning.Projection{Provider: provider, Workspace: workspace})
	n, err := provisioning.ClaudeSettingsHookEntries(workspace, managedHookNames(lock))
	if err != nil || n == 0 {
		return nil
	}
	entries := "entries"
	if n == 1 {
		entries = "entry"
	}
	return []doctorFinding{{
		Check: "workspace", Severity: doctorWarning, Path: filepath.Join(workspace, filepath.FromSlash(rel)),
		Message: fmt.Sprintf("%s: %s is tracked by git and holds %d Cartographer hook %s: a derived binding registers its hooks in %s and never edits a tracked file",
			provider, rel, n, entries, provisioning.ClaudeLocalSettings),
		Fix: "remove those entries from " + rel + " (`git diff` shows them; each command names .claude/hooks/<name>/ or carries `# cartographer-hook:`), then cartographer sync",
	}}
}

// uncoveredProjectionFiles lists the files git sees as untracked and not
// ignored under the directories a projection of provider writes into (#1199): the
// ones the exclude block, the repository's .gitignore and the user's global
// ignore all leave out, narrowed to the paths the projection owns (what the lock
// records, its companions included): a repository's own untracked skill beside
// them is not Cartographer's to report. The instructions file is the user's.
func uncoveredProjectionFiles(provider configurator.Provider, workspace string, lock provisioning.Lock) []string {
	if _, err := os.Lstat(filepath.Join(workspace, ".git")); err != nil {
		return nil
	}
	instructions := provisioning.ProjectDestination("instructions", "", provider)
	owned, err := provisioning.ProjectOwnedArtifactPaths(provider, contract.Manifest{}, lock)
	if err != nil {
		return nil
	}
	args := []string{"--literal-pathspecs", "-C", workspace, "ls-files", "--others", "--exclude-standard", "-z", "--"}
	n := len(args)
	for _, p := range owned {
		if p != instructions {
			args = append(args, p)
		}
	}
	if len(args) == n {
		return nil
	}
	cmd := procattr.Hide(exec.Command("git", args...))
	cmd.Env = gitx.Env() // -C names the repository: a hook's GIT_DIR must not
	raw, err := cmd.Output()
	if err != nil {
		return nil
	}
	var files []string
	for _, f := range strings.Split(string(raw), "\x00") {
		if f != "" {
			files = append(files, f)
		}
	}
	sort.Strings(files)
	return files
}

// joinCapped joins at most max items and says how many it left out.
func joinCapped(items []string, max int) string {
	if len(items) <= max {
		return strings.Join(items, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(items[:max], ", "), len(items)-max)
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
func pruneOrphanProjections(st syncStreams, lockFile *provisioning.LockFile, declared []syncProjection, manifests map[string]contract.Manifest, clientBaseDir string, denied *[]string) ([]string, error) {
	wanted := map[string]bool{}
	providersInPlay := map[string]bool{}
	unlisted := map[string]bool{}
	for _, p := range declared {
		wanted[projectionKey(p)] = true
		providersInPlay[p.Provider] = true
		if p.WorktreesUnlisted {
			unlisted[p.Provider] = true
		}
	}

	var pruned []string
	for _, key := range lockFile.Projections() {
		if key.Global() || !providersInPlay[key.Provider] {
			// Not a workspace projection, or a provider this run is not
			// touching at all (`sync --client` names a subset): leaving it
			// alone is the only safe answer.
			continue
		}
		if wanted[projectionKey(syncProjection{Provider: key.Provider, Workspace: key.Workspace})] {
			continue
		}
		if _, err := os.Stat(key.Workspace); os.IsNotExist(err) && lockFile.ForProjection(key).Worktree {
			// A linked worktree of a --worktrees binding went with its files
			// (`git worktree remove`): only the record is left. Silent, no
			// prune and no exclude rewrite (there is nothing to write in).
			// Any other undeclared key whose directory is missing (an
			// unmounted volume, a moved clone) takes the ordinary path below:
			// its files may still exist.
			lockFile.RemoveProjection(key)
			pruned = append(pruned, key.String()) // the caller rewrites the lockfile
			continue
		}
		if unlisted[key.Provider] {
			// A --worktrees listing failed: whether this workspace is still a
			// worktree is unknown, so its files stay until a listing succeeds.
			continue
		}
		lock := lockFile.ForProjection(key)
		base := provisioning.LockBaseDir(lock, clientBaseDir)
		// Not what another provider's projection of this workspace still
		// records (D364): .agents/skills/<n>/ is Codex's and Antigravity's.
		keep := lockFile.CoOwnedPaths(key)
		// The workspace key and base dir are lockfile data: a file goes only
		// while it still hashes to what the sync wrote, the rest is reported
		// and kept (the section is dropped either way).
		toPrune, kept, unverifiable := verifiedPrunable(provisioning.WithoutCoOwned(lock.Managed, keep), lock, key.Provider, base)
		for _, k := range kept {
			fmt.Fprintf(st.err(), "warning: %s: kept %s\n", key.String(), k)
		}
		rep, err := provisioning.PruneManagedReport(toPrune, base, false)
		removed := rep.Pruned
		// A root the prune removed is announced even when the pass is refused
		// further down: it is already gone.
		printPrunedRoots(st.out(), key.Provider, rep.RemovedRoots, false)
		if err == nil && unverifiable {
			// A file that could not even be read (an unreadable folder) is not
			// judged: it stays, like a denied removal, and the next run retries.
			err = fmt.Errorf("%w: files could not be verified", provisioning.ErrPruneDenied)
		}
		if err != nil {
			if provisioning.IsRefusal(err) {
				// Refused (D148): the projection stays in the lockfile, so the
				// next sync retries it; the other projections are still pruned.
				// What this pass did remove leaves the lock, and the hash of an
				// artifact left partly standing is rebased on the files that
				// remain: the retry must not judge the files sync itself took
				// away "changed since sync" (#2218).
				lockFile.SetProjection(key, provisioning.LockAfterPartialPrune(lock, removed, configurator.Provider(key.Provider), base))
				fmt.Fprintf(st.err(), "warning: %s not pruned: %v\n", key.String(), err)
				if denied != nil && errors.Is(err, provisioning.ErrPruneDenied) && !slices.Contains(*denied, key.Workspace) {
					// A permission error (#2178) is a refused removal that
					// the run exits 3 for, as for an unreadable workspace.
					*denied = append(*denied, key.Workspace)
				}
				continue
			}
			return nil, fmt.Errorf("prune %s: %w", key.String(), err)
		}
		// The exclusions go with the files, but only this provider's: another
		// provider still bound to the workspace keeps its paths excluded (#656).
		lockFile.RemoveProjection(key)
		if err := writeWorkspaceExclusions(key.Workspace, workspaceProviders(*lockFile, declared, key.Workspace), workspaceOwnership(lockFile, declared, manifests, key.Workspace)); err != nil {
			if !provisioning.ExcludeWarning(err) && !errors.Is(err, fs.ErrPermission) {
				return nil, fmt.Errorf("prune %s: %w", key.String(), err)
			}
			fmt.Fprintf(st.err(), "warning: workspace %s: git exclude left unchanged: %v\n", key.Workspace, err)
		}
		pruned = append(pruned, key.String())
		fmt.Fprintf(st.out(), "[%s] pruned the projection in %s\n", key.Provider, key.Workspace)
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
		msg += fmt.Sprintf("`%s --trust` records it, or ", bindCommand("codex", w.Path, w))
	}
	return msg + fmt.Sprintf("add %s to %s, or accept the prompt in a codex session in this directory", line, cfgPath)
}
