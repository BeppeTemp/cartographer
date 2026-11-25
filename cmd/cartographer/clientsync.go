package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/BeppeTemp/cartographer/internal/agents"
	"github.com/BeppeTemp/cartographer/internal/artifactsig"
	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
)

// pulledFileJSON/pulledArtifactJSON/pulledManifestJSON mirror the sync_pull tool's
// response shape (internal/mcpserver/tools_sync.go), decoded client-side.
type pulledFileJSON struct {
	Path       string `json:"path"`
	ContentB64 string `json:"content_b64"`
	Executable bool   `json:"executable"`
}

type pulledArtifactJSON struct {
	Kind        string                 `json:"kind"`
	Name        string                 `json:"name"`
	Source      string                 `json:"source"`
	Version     string                 `json:"version,omitempty"`
	ContentHash string                 `json:"content_hash"`
	Signed      bool                   `json:"signed"`
	BuiltIn     bool                   `json:"built_in,omitempty"`
	Signature   *artifactsig.Signature `json:"signature,omitempty"`
	Files       []pulledFileJSON       `json:"files"`
}

type pulledManifestJSON struct {
	Revision  string               `json:"revision"`
	Artifacts []pulledArtifactJSON `json:"artifacts"`
	// Placeholders are the "kind:key" placeholders the KB cites (D262),
	// outside revision and unsigned. An older server sends none, and the
	// client falls back to the keys found in the artifacts themselves.
	Placeholders []string `json:"placeholders,omitempty"`
	// PathRegistry is the KB's declared placeholder vocabulary (paths.yaml,
	// D263), unsigned and outside revision. nil when the KB has none or the
	// server predates it.
	PathRegistry *contract.PathRegistry `json:"path_registry,omitempty"`
	// Issues are the artifacts the server left out of this response and why
	// (Manifest.Issues); server text, escaped wherever it is printed.
	Issues []string `json:"issues,omitempty"`
	// Feedback is the server's feedback.destination mode; absent means github
	// (an older server, or the default), so both ends agree.
	Feedback *struct {
		Destination string `json:"destination"`
	} `json:"feedback,omitempty"`
}

// lockFilePath returns the path to the v2 multi-provider lockfile inside targetDir.
func lockFilePath(targetDir string) string {
	return filepath.Join(targetDir, provisioning.LockFileName)
}

// resolveToken returns the bearer token for cfg when cfg.Auth is true: the
// literal `token`, or the environment variable a `${NAME}` token names when
// set, otherwise the private token file
// `connect` wrote next to .cartographer.yaml (D699), otherwise "" (no
// Authorization header is sent). The file exists because an unattended run
// (sync timer, scheduled doctor) never inherits a login shell's exports.
func resolveToken(cfg *clientconfig.Config) string {
	tok, _ := resolveTokenErr(cfg)
	return tok
}

// resolveTokenErr is resolveToken plus the reason the token file was not
// usable (unsafe mode, symlink), so the preflight can name it instead of
// reporting a plain "not set".
func resolveTokenErr(cfg *clientconfig.Config) (string, error) {
	if !cfg.Auth || strings.TrimSpace(cfg.Token) == "" {
		return "", nil
	}
	if lit := cfg.LiteralToken(); lit != "" {
		return lit, nil
	}
	if v := os.Getenv(cfg.TokenEnv()); v != "" {
		return v, nil
	}
	dir, err := clientconfig.TargetDir()
	if err != nil {
		return "", nil
	}
	return clientconfig.ReadToken(dir)
}

// tokenEnvName returns the environment variable resolveToken reads for cfg, or
// empty when this config sends no credential at all or a literal one. Passed to
// the client so a 401 names the variable instead of "the env var" (D222).
func tokenEnvName(cfg *clientconfig.Config) string {
	if !cfg.Auth || cfg.LiteralToken() != "" {
		return ""
	}
	return cfg.TokenEnv()
}

// tokenSpecValue is the value of a token spec ("${NAME}" or a literal), without
// the token file: what a probe made before anything is written can know.
func tokenSpecValue(spec string) string {
	c := &clientconfig.Config{Token: spec}
	if lit := c.LiteralToken(); lit != "" {
		return lit
	}
	if name := c.TokenEnv(); name != "" {
		return os.Getenv(name)
	}
	return ""
}

// candidateSet holds the sync_pull responses UNMERGED, one entry per KB, which
// is what makes a per-provider projection possible (D170): the selection has to
// happen on these responses, before any client-side merge, because
// MergeArtifacts discards candidates and the server has already merged
// KB-over-bundle inside each single-KB pull.
//
// Bare holds the response of a nameless endpoint — a server that advertises no
// KB metadata, or one asked before any KB name was known. No binding can name
// it, so it is handled separately rather than filed under an invented key.
type candidateSet struct {
	Named   map[string][]contract.Artifact
	Bare    []contract.Artifact
	HasBare bool
	// Placeholders is, per KB, the placeholder keys its sync_pull listed
	// (D262); BarePlaceholders the unnamed endpoint's. Selected per
	// projection exactly like the artifacts, by placeholdersForProjection.
	Placeholders     map[string][]string
	BarePlaceholders []string
	// Issues is, per KB, what its sync_pull said it left out of the response
	// (#1775); BareIssues the unnamed endpoint's. Selected per projection by
	// issuesForProjection, like the placeholder keys.
	Issues     map[string][]string
	BareIssues []string
	// PathRegistries is, per KB, the paths.yaml its sync_pull served (D263);
	// BarePathRegistry the unnamed endpoint's. Selected per projection by
	// pathRegistriesForProjection, like the placeholder keys.
	PathRegistries   map[string]contract.PathRegistry
	BarePathRegistry *contract.PathRegistry
	// Plugins are the skills, agents and hooks of the active plugins
	// (loadActivePlugins). They are not a KB's: no binding selects them and
	// kb_options does not apply, so they join each projection after the KB
	// selection (forProvider, forProjection) and before the collision drop.
	Plugins []contract.Artifact
	// FeedbackModes is, per KB, the feedback.destination mode its sync_pull
	// said ("" is github); BareFeedback the unnamed endpoint's. Selected per
	// projection by feedbackForProjection, like the placeholder keys, so a KB
	// whose pull failed never downgrades the block of a projection that does
	// not read it (a projection that does is skipped whole).
	FeedbackModes map[string]string
	BareFeedback  string
}

// feedbackForProjection returns the feedback mode of the KBs a projection
// reads, by placeholdersForProjection's selection. Across KBs off wins, then
// custom (an offer that names the wrong tracker is worse than none).
func (cs candidateSet) feedbackForProjection(cfg *clientconfig.Config, p syncProjection) string {
	if p.BundleOnly {
		return ""
	}
	if cs.HasBare {
		if p.Workspace != "" {
			return ""
		}
		if bound, explicit := cfg.BoundKBs(p.Provider); explicit && len(bound) == 0 {
			return ""
		}
		return cs.BareFeedback
	}
	kbs := p.KBs
	if p.Workspace == "" {
		kbs, _ = cfg.BoundKBs(p.Provider)
	}
	mode := ""
	for _, kb := range kbs {
		mode = mergeFeedbackMode(mode, cs.FeedbackModes[kb])
	}
	return mode
}

// pathRegistriesForProjection returns the declared vocabularies of the KBs a
// projection is bound to, KB name -> registry ("" for the unnamed endpoint),
// selected by the same rules as placeholdersForProjection. Apply merges them
// in the provider's KB order. nil when there is none.
func (cs candidateSet) pathRegistriesForProjection(cfg *clientconfig.Config, p syncProjection) map[string]contract.PathRegistry {
	if p.BundleOnly {
		return nil
	}
	out := map[string]contract.PathRegistry{}
	if cs.HasBare {
		if p.Workspace != "" || cs.BarePathRegistry == nil {
			return nil
		}
		if bound, explicit := cfg.BoundKBs(p.Provider); explicit && len(bound) == 0 {
			return nil
		}
		out[""] = *cs.BarePathRegistry
	} else {
		kbs := p.KBs
		if p.Workspace == "" {
			kbs, _ = cfg.BoundKBs(p.Provider)
		}
		for _, kb := range kbs {
			if reg, ok := cs.PathRegistries[kb]; ok {
				out[kb] = reg
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// placeholdersForProjection returns the placeholder keys a projection must
// resolve, "kind:key" -> the KBs citing it, following forProjection's rules:
// a bundle-only projection has no KB and no key, a workspace receives its own
// KBs' keys, the global projection its provider's bound KBs'. Keys from the
// unnamed endpoint carry no KB. nil when there is none.
func (cs candidateSet) placeholdersForProjection(cfg *clientconfig.Config, p syncProjection) map[string][]string {
	if p.BundleOnly {
		return nil
	}
	out := map[string][]string{}
	if cs.HasBare {
		if p.Workspace != "" {
			return nil
		}
		if bound, explicit := cfg.BoundKBs(p.Provider); explicit && len(bound) == 0 {
			return nil
		}
		for _, id := range cs.BarePlaceholders {
			out[id] = nil
		}
	} else {
		kbs := p.KBs
		if p.Workspace == "" {
			kbs, _ = cfg.BoundKBs(p.Provider)
		}
		sorted := append([]string(nil), kbs...)
		sort.Strings(sorted)
		for _, kb := range sorted {
			for _, id := range cs.Placeholders[kb] {
				out[id] = append(out[id], kb)
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// issuesForProjection returns the manifest issues of the KBs a projection
// reads, following placeholdersForProjection's selection, each with its KB
// ("" for the unnamed endpoint), and the KBs read. The KB is kept so that the
// run-level list can decide the prefix once (manifestIssues, #1799).
func (cs candidateSet) issuesForProjection(cfg *clientconfig.Config, p syncProjection) ([]contract.KBIssue, []string) {
	if p.BundleOnly {
		return nil, nil
	}
	var out []contract.KBIssue
	if cs.HasBare {
		if p.Workspace != "" {
			return nil, nil
		}
		if bound, explicit := cfg.BoundKBs(p.Provider); explicit && len(bound) == 0 {
			return nil, nil
		}
		for _, is := range cs.BareIssues {
			out = append(out, contract.KBIssue{Text: is})
		}
		return out, nil
	}
	kbs := p.KBs
	if p.Workspace == "" {
		kbs, _ = cfg.BoundKBs(p.Provider)
	}
	sorted := append([]string(nil), kbs...)
	sort.Strings(sorted)
	for _, kb := range sorted {
		for _, is := range cs.Issues[kb] {
			out = append(out, contract.KBIssue{KB: kb, Text: is})
		}
	}
	return out, sorted
}

// manifestIssues is the distinct issues of every manifest of a run, in
// projection-key order: the same KB read by three providers is one line, and
// the same issue reached through a global and a workspace projection too. A
// line carries its KB's name when the run as a whole reads more than one KB.
func manifestIssues(manifests map[string]contract.Manifest) []string {
	keys := make([]string, 0, len(manifests))
	for k := range manifests {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	read := map[string]bool{}
	var distinct []contract.KBIssue
	for _, k := range keys {
		for _, kb := range manifests[k].ReadKBs {
			read[kb] = true
		}
		for _, is := range manifests[k].KBIssues {
			if !slices.Contains(distinct, is) {
				distinct = append(distinct, is)
			}
		}
	}
	var out []string
	for _, is := range distinct {
		text := is.Text
		if is.KB != "" && len(read) > 1 {
			text = is.KB + ": " + text
		}
		if !slices.Contains(out, text) {
			out = append(out, text)
		}
	}
	return out
}

// all returns every candidate, in a deterministic order.
func (cs candidateSet) all() []contract.Artifact {
	out := append([]contract.Artifact(nil), cs.Bare...)
	names := make([]string, 0, len(cs.Named))
	for name := range cs.Named {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		out = append(out, cs.Named[name]...)
	}
	return append(out, cs.Plugins...)
}

// forKBs concatenates the responses of the named KBs, in a deterministic order.
// A name with no response contributes nothing — it is not an error here: the
// caller validated the binding against /health before pulling.
func (cs candidateSet) forKBs(names []string) []contract.Artifact {
	sorted := append([]string(nil), names...)
	sort.Strings(sorted)
	var out []contract.Artifact
	for _, name := range sorted {
		out = append(out, cs.Named[name]...)
	}
	return out
}

// forProvider returns the candidates one provider may receive.
//
// The bare endpoint is the one case bindings cannot express: there is no KB
// name to match, so every provider receives it, exactly as before D170 — a
// missing signal is not a reason to starve a client on a legacy or first-run
// server. The single exception is a provider explicitly bound to NO KBs, where
// the operator's declaration is unambiguous and is honoured.
func (cs candidateSet) forProvider(cfg *clientconfig.Config, provider string) []contract.Artifact {
	bound, explicit := cfg.BoundKBs(provider)
	if cs.HasBare {
		if explicit && len(bound) == 0 {
			return append([]contract.Artifact(nil), cs.Plugins...)
		}
		return append(append([]contract.Artifact(nil), cs.Bare...), cs.Plugins...)
	}
	// SelectForSources drops every plugin artifact and dropKBArtifacts only
	// touches kb: ones: the plugins are appended after both (decision 8).
	kept := dropKBArtifacts(cfg, provisioning.SelectForSources(cs.forKBs(bound), bound))
	return append(kept, cs.Plugins...)
}

// dropKBArtifacts removes the artifacts of every KB that has
// `knowledge_bases.<kb>.artifacts: false` (#1408), except its instructions artifact:
// the KB stays connected and its routing sentence must reach the block. It runs
// BEFORE the strict merge, so a name collision with an opted-out KB's skill no
// longer stops the sync; bundle artifacts belong to no KB and stay. The pruning
// of what was already installed is not done here: the artifacts leave the
// manifest, ComputeDiff lists them Removed and the lockfile-driven prune does
// the rest.
func dropKBArtifacts(cfg *clientconfig.Config, arts []contract.Artifact) []contract.Artifact {
	if len(cfg.KBOptions) == 0 {
		return arts
	}
	out := make([]contract.Artifact, 0, len(arts))
	for _, a := range arts {
		if kb, ok := strings.CutPrefix(a.Source, "kb:"); ok && a.Kind != "instructions" && !cfg.KBArtifacts(kb) {
			continue
		}
		out = append(out, a)
	}
	return out
}

// withoutOptedOutSkillsLines rewrites the instructions artifact of every KB
// with `artifacts: false` so its block no longer lists skills (#1408). It must
// run on the manifest VerifiedManifest returned: the rewrite changes the hash
// the signature covers, and Apply trusts the Signed flag and never re-verifies.
func withoutOptedOutSkillsLines(cfg *clientconfig.Config, m contract.Manifest) contract.Manifest {
	if len(cfg.KBOptions) == 0 {
		return m
	}
	for i, a := range m.Artifacts {
		if kb, ok := strings.CutPrefix(a.Source, "kb:"); ok && a.Kind == "instructions" && !cfg.KBArtifacts(kb) {
			m.Artifacts[i] = provisioning.WithoutSkillsLine(a)
		}
	}
	return m
}

// boundKBUnion is the set of KBs that must actually be pulled: the union of
// every connected provider's binding. A known KB nobody is bound to is not
// fetched at all.
//
// requiredBy maps each name to the providers that ask for it, so a stale
// binding can be reported with the provider that holds it rather than as an
// anonymous configuration error. anyDefault reports whether at least one
// provider has no explicit binding, which is what distinguishes "nothing is
// bound" from "nothing is known yet".
func boundKBUnion(cfg *clientconfig.Config, providers []string) (names []string, requiredBy map[string][]string, anyDefault bool) {
	requiredBy = make(map[string][]string)
	for _, p := range providers {
		bound, explicit := cfg.BoundKBs(p)
		if !explicit {
			anyDefault = true
		}
		for _, kb := range bound {
			if _, seen := requiredBy[kb]; !seen {
				names = append(names, kb)
			}
			requiredBy[kb] = append(requiredBy[kb], p)
		}
	}
	sort.Strings(names)
	return names, requiredBy, anyDefault
}

// projectionKBOrder is the KB order that sorts p's instructions sections. A
// workspace projection follows the order of the folder binding it serves
// (p.KBs: the explicit binding as written, a derived one's list), never the
// provider's global binding: Codex and OpenCode share a workspace AGENTS.md,
// and two different global orders made every sync rewrite it twice (#1809). A
// global projection keeps the provider's binding order (D182).
func projectionKBOrder(p syncProjection, global map[string][]string) []string {
	if p.Workspace != "" {
		return p.KBs
	}
	return global[p.Provider]
}

// kbOrderForProviders returns, for each provider in providers that has an
// EXPLICIT KB binding (D169/D170), that binding's declared KB order —
// clientconfig.ClientBinding.KBs is already an ordered YAML sequence. A
// provider with no explicit binding (bound to every known KB by default) is
// omitted, so materializeForProviders leaves its instructions block on the
// alphabetical fallback (D182 WP2).
func kbOrderForProviders(cfg *clientconfig.Config, providers []string) map[string][]string {
	out := make(map[string][]string, len(providers))
	for _, p := range providers {
		if kbs, explicit := cfg.BoundKBs(p); explicit {
			out[p] = kbs
		}
	}
	return out
}

// annotateStaleBinding appends the providers that require a KB the server no
// longer advertises. resolveKBTargets already names the KB; without this the
// operator still has to guess which client's binding to fix.
func annotateStaleBinding(err error, requiredBy map[string][]string) error {
	for kb, providers := range requiredBy {
		if strings.Contains(err.Error(), strconv.Quote(kb)) {
			sort.Strings(providers)
			return fmt.Errorf("%w (required by: %s)", err, strings.Join(providers, ", "))
		}
	}
	return err
}

// fetchMergedManifest is fetchMergedManifestFor without a KB: every projection
// is judged.
func fetchMergedManifest(cfg *clientconfig.Config) (contract.Manifest, error) {
	return fetchMergedManifestFor(cfg, "")
}

// fetchMergedManifestFor fetches the KBs' artifacts and merges them into one
// verified provisioning.Manifest, applying the same precedence rule (KB source
// wins over bundle) BuildManifest applies server-side for one KB.
//
// The merge is strict (D171) and judged per projection, as sync judges it
// (candidateSet.forProjection): two KBs claiming the same kind+name is an
// error only when one projection receives both, not the alphabetical coin flip
// preferArtifact would otherwise perform. Two KBs bound to disjoint workspaces
// may share a name (#2306). With kb set, only the projections that receive kb
// are judged, so a collision elsewhere cannot stop its approval. The result is
// the union of the projections' artifacts, one entry per source, kind and name.
// A KB no projection receives is merged alone: approve and mcp probe still see
// what it advertises.
func fetchMergedManifestFor(cfg *clientconfig.Config, kb string) (contract.Manifest, error) {
	kbs := slices.Clone(cfg.KnownKBs)
	perKB := client.PerKBName(cfg.ServerURL)
	projections := approveProjections(cfg)
	if perKB != "" {
		// A per-KB endpoint serves its own KB only: the other KBs of the
		// server are not this client's (#2172).
		kbs = []string{perKB}
	} else {
		// approve judges what sync will pull: the known KBs and the ones the
		// projections are bound to, not yet synced (#2349). With kb set, only
		// the KBs of the projections that receive it.
		bound := false
		for _, p := range projections {
			if kb != "" && !slices.Contains(p.KBs, kb) {
				continue
			}
			for _, k := range p.KBs {
				if k == kb {
					bound = true
				}
				if !slices.Contains(kbs, k) {
					kbs = append(kbs, k)
				}
			}
		}
		if kb != "" && len(kbs) > 0 && !slices.Contains(cfg.KnownKBs, kb) && !bound {
			// Neither synced nor bound: nothing here can see it (#2347). Say
			// so before any network call.
			return contract.Manifest{}, fmt.Errorf("KB %s is not bound to any folder and not synced yet (not in known_kbs); bind it with cartographer workspace bind or run cartographer sync first, then retry", escapeControl(kb))
		}
	}
	cs, failed, err := fetchCandidatesPartial(cfg, kbs)
	if err != nil {
		return contract.Manifest{}, err
	}
	// A failure of a known KB stays fatal; a bound-only KB (not mounted,
	// unreadable) leaves out the projections that need it, as sync does.
	unavailable := map[string]bool{}
	for _, f := range failed {
		if perKB != "" || slices.Contains(cfg.KnownKBs, f.KB) {
			return contract.Manifest{}, f.Err
		}
		unavailable[f.KB] = true
		fmt.Fprintf(os.Stderr, "warning: %s is bound but could not be pulled; collisions with it were not checked: %v\n", escapeControl(f.KB), f.Err)
	}
	pins, err := pinnedPublicKeys(cfg)
	if err != nil {
		return contract.Manifest{}, err
	}
	if cs.HasBare {
		// A server that names no KB has no binding to judge: one merge of
		// everything it serves, as sync's fail-closed projections cannot be
		// expressed against it.
		merged, err := mergeAcrossPlugins(cs.all(), nil)
		if err != nil {
			return contract.Manifest{}, err
		}
		return provisioning.VerifiedManifest(merged, pins)
	}
	var out contract.Manifest
	seen := map[string]bool{}
	first := true
	add := func(arts []contract.Artifact) error {
		merged, err := mergeAcrossPlugins(arts, nil)
		if err != nil {
			return err
		}
		m, err := provisioning.VerifiedManifest(merged, pins)
		if err != nil {
			return err
		}
		if first {
			out, out.Artifacts, first = m, nil, false
		}
		for _, a := range m.Artifacts {
			if key := a.Source + "\x00" + a.Kind + "\x00" + a.Name; !seen[key] {
				seen[key] = true
				out.Artifacts = append(out.Artifacts, a)
			}
		}
		return nil
	}
	reached := map[string]bool{}
	judged := 0
	for _, p := range projections {
		if kb != "" && !slices.Contains(p.KBs, kb) {
			continue
		}
		if needsAny(p.KBs, unavailable) {
			continue
		}
		for _, k := range p.KBs {
			reached[k] = true
		}
		judged++
		if err := add(cs.forProjection(cfg, p)); err != nil {
			if label := p.Label(); label != "" {
				err = fmt.Errorf("%s: %w", label, err)
			}
			return contract.Manifest{}, err
		}
	}
	for _, k := range kbs {
		if reached[k] || unavailable[k] || (kb != "" && k != kb) {
			continue
		}
		// A provider that failed to resolve its projections (allProjectionsPartial)
		// lands here too: its KBs are merged alone, with no collision check
		// among them; sync reports that provider.
		if err := add(dropKBArtifacts(cfg, cs.forKBs([]string{k}))); err != nil {
			return contract.Manifest{}, err
		}
	}
	if judged == 0 && first {
		return contract.Manifest{}, nil
	}
	out.Artifacts = kbShadowsBundle(out.Artifacts)
	return out, nil
}

// kbShadowsBundle drops a bundle artifact when a KB ships the same kind and
// name: the precedence one merge applies (a KB wins over the bundle) holds
// inside each projection, and the union of projections must keep it. The
// merge ranks a KB against the bundle only: a plugin claiming a KB's name
// never reaches the union (DropPluginCollisions drops both claimants).
func kbShadowsBundle(arts []contract.Artifact) []contract.Artifact {
	fromKB := map[string]bool{}
	for _, a := range arts {
		if strings.HasPrefix(a.Source, "kb:") {
			fromKB[a.Kind+"\x00"+a.Name] = true
		}
	}
	out := arts[:0:0]
	for _, a := range arts {
		if !strings.HasPrefix(a.Source, "kb:") && fromKB[a.Kind+"\x00"+a.Name] {
			continue
		}
		out = append(out, a)
	}
	return out
}

// approveProjections are the projections the connected providers have now: one
// per provider in the default scope, a bundle-only one and one per bound
// workspace in workspace scope. A provider whose workspaces cannot be resolved
// is left out (sync reports it); with no connected provider the whole known
// set is one projection, as before workspaces existed.
func approveProjections(cfg *clientconfig.Config) []syncProjection {
	ps, _ := allProjectionsPartial(cfg, cfg.Agents, "")
	if len(cfg.Agents) == 0 {
		ps = []syncProjection{globalProjection("", "", cfg.KnownKBs)}
	}
	return ps
}

// fetchCandidates connects to cfg.ServerURL, discovers the live per-KB
// tool-name prefixes from /health (D120: resolveKBTargets), and calls
// sync_pull — qualified with each target's advertised prefix — once per KB
// target (cfg.KnownKBs, or the server's default single-KB endpoint when empty),
// decoding each artifact's in-memory file contents (base64).
//
// It returns the artifacts UNMERGED, each still carrying the source it came
// from, so a caller can decide what to do with a kind+name several KBs claim
// (fetchMergedManifest refuses it; collisionsForProvider reports only the ones
// a given provider would actually be exposed to).
func fetchCandidates(cfg *clientconfig.Config, kbNames []string) (candidateSet, error) {
	cs, failed, err := fetchCandidatesPartial(cfg, kbNames)
	if len(failed) > 0 {
		// The failures precede any fatal error in target order, so the first
		// one is the error the all-or-nothing callers always reported.
		return cs, failed[0].Err
	}
	return cs, err
}

// kbPullFailure is one named KB whose sync_pull could not be used: the call
// failed, or its response did not decode or verify. Err is already worded for
// the operator and names the KB.
type kbPullFailure struct {
	KB  string
	Err error
}

// fetchCandidatesPartial is fetchCandidates for a caller that can go on
// without some KBs (#350): a named KB whose pull fails is recorded in the
// returned failures, in target order, and the others are still returned. What
// no single KB can be blamed for stays fatal: /health, target resolution, the
// server's unnamed endpoint, and two KBs serving one artifact with different
// signatures.
func fetchCandidatesPartial(cfg *clientconfig.Config, kbNames []string) (candidateSet, []kbPullFailure, error) {
	token := resolveToken(cfg)
	tokenEnv := tokenEnvName(cfg)
	health, err := client.New(cfg.ServerURL, token).WithInsecureHTTP(cfg.InsecureHTTPAllowed()).WithTokenEnv(tokenEnv).Health(probeTimeout)
	if err != nil {
		return candidateSet{Named: make(map[string][]contract.Artifact), Placeholders: make(map[string][]string), PathRegistries: make(map[string]contract.PathRegistry)}, nil, fmt.Errorf("health: %w", err)
	}
	return pullCandidatesPartial(cfg, health, kbNames)
}

// pullCandidatesPartial is fetchCandidatesPartial after /health: a caller that
// already holds the response (doctor, via serverFacts) passes it instead of
// probing the server again.
func pullCandidatesPartial(cfg *clientconfig.Config, health *client.Health, kbNames []string) (candidateSet, []kbPullFailure, error) {
	cs := candidateSet{Named: make(map[string][]contract.Artifact), Placeholders: make(map[string][]string), PathRegistries: make(map[string]contract.PathRegistry), Issues: make(map[string][]string), FeedbackModes: make(map[string]string), Plugins: loadActivePlugins(cfg).Artifacts}
	token := resolveToken(cfg)
	tokenEnv := tokenEnvName(cfg)
	// A selection the server no longer advertises costs only the providers that
	// name it: it is a failure of that KB (#2199), like a pull that failed, and
	// the others are still pulled.
	var stale []kbPullFailure
	if health != nil && health.KBs != nil {
		mounted := make(map[string]bool, len(*health.KBs))
		for _, kb := range *health.KBs {
			mounted[kb.Name] = true
		}
		var live []string
		for _, name := range kbNames {
			if mounted[name] {
				live = append(live, name)
			} else {
				stale = append(stale, kbPullFailure{KB: name, Err: staleSelectionError(escapeControl(name))})
			}
		}
		if len(stale) > 0 {
			kbNames = live
			if len(live) == 0 {
				return cs, stale, nil
			}
		}
	}
	targets, err := resolveKBTargets(health, kbNames)
	if err != nil {
		return cs, nil, err
	}

	// The pulls run concurrently (#362): each one can wait on a server-side
	// git fetch of its KB, and the server serializes git work per KB only, so
	// in series a sync costs the sum of those waits. Everything order-sensitive
	// — which error is reported, the signature dedup over seen — runs below,
	// in target order, so the outcome is the one the sequential loop gave.
	results := make([]pullResult, len(targets))
	sem := make(chan struct{}, maxConcurrentPulls)
	var wg sync.WaitGroup
	for i, target := range targets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			c := client.New(cfg.ServerURL, token).WithInsecureHTTP(cfg.InsecureHTTPAllowed()).WithTokenEnv(tokenEnv).WithKB(target.Name)
			results[i] = pullTarget(c, target)
		}()
	}
	wg.Wait()

	seen := make(map[string]contract.Artifact)
	failed := stale

	for i, target := range targets {
		res := results[i]
		if res.callErr != nil || res.err != nil {
			err := res.err
			if res.callErr != nil {
				if target.Name == "" {
					err = fmt.Errorf("sync_pull: %w", res.callErr)
				} else {
					err = fmt.Errorf("sync_pull (kb=%s): %w", escapeControl(target.Name), res.callErr)
				}
			} else if target.Name != "" {
				err = fmt.Errorf("kb=%s: %w", escapeControl(target.Name), res.err)
			}
			// The unnamed endpoint is not a KB a provider is bound to, so
			// there is nothing to skip around: it stays fatal.
			if target.Name == "" {
				return cs, failed, err
			}
			failed = append(failed, kbPullFailure{KB: target.Name, Err: err})
			continue
		}
		if target.Name == "" {
			cs.BareFeedback = mergeFeedbackMode(cs.BareFeedback, res.feedback)
		} else if res.feedback != "" {
			cs.FeedbackModes[target.Name] = res.feedback
		}
		for _, a := range res.artifacts {
			key := a.Kind + "\x00" + a.Name + "\x00" + a.Source
			if previous, exists := seen[key]; exists && !sameSignature(previous.Signature, a.Signature) {
				return cs, failed, fmt.Errorf("sync_pull: conflicting signatures for %s/%s", escapeControl(a.Kind), escapeControl(a.Name))
			}
			seen[key] = a
			if target.Name == "" {
				cs.Bare = append(cs.Bare, a)
				cs.HasBare = true
			} else {
				cs.Named[target.Name] = append(cs.Named[target.Name], a)
			}
		}
		if target.Name == "" {
			cs.BarePlaceholders = append(cs.BarePlaceholders, res.placeholders...)
			cs.BarePathRegistry = res.pathRegistry
			cs.BareIssues = append(cs.BareIssues, res.issues...)
		} else {
			if len(res.issues) > 0 {
				cs.Issues[target.Name] = res.issues
			}
			if len(res.placeholders) > 0 {
				cs.Placeholders[target.Name] = res.placeholders
			}
			if res.pathRegistry != nil {
				cs.PathRegistries[target.Name] = *res.pathRegistry
			}
		}
		// A KB that answered with no artifacts at all still counts as answered:
		// without this, a provider bound only to it would fall through the
		// "nothing was pulled" branch instead of correctly receiving nothing.
		if target.Name != "" {
			if _, ok := cs.Named[target.Name]; !ok {
				cs.Named[target.Name] = nil
			}
		} else {
			cs.HasBare = true
		}
	}

	return cs, failed, nil
}

// maxConcurrentPulls bounds the sync_pull calls fetchCandidates keeps in
// flight: enough that a handful of KBs cost the slowest one rather than the
// sum, few enough not to open a fetch against every remote of a large setup
// at once.
const maxConcurrentPulls = 4

// pullResult is one target's sync_pull outcome. callErr is the transport or
// tool error, which fetchCandidates wraps with the KB name; err is a decoding
// or integrity error, already worded for the operator.
type pullResult struct {
	artifacts    []contract.Artifact
	placeholders []string
	pathRegistry *contract.PathRegistry
	issues       []string
	feedback     string
	callErr      error
	err          error
}

// pullTarget calls sync_pull on one target and decodes and hash-checks its
// artifacts. It touches no state shared with other targets, so fetchCandidates
// can run it from several goroutines.
func pullTarget(c *client.MCPClient, target kbTarget) pullResult {
	raw, err := callTool(c, target, "sync_pull", syncPullArgs())
	if err != nil {
		return pullResult{callErr: err}
	}
	var pm pulledManifestJSON
	if err := json.Unmarshal(raw, &pm); err != nil {
		return pullResult{err: fmt.Errorf("sync_pull: decode response: %w", err)}
	}
	arts := make([]contract.Artifact, 0, len(pm.Artifacts))
	for _, pa := range pm.Artifacts {
		files := make([]contract.ArtifactFile, len(pa.Files))
		for i, pf := range pa.Files {
			data, err := base64.StdEncoding.DecodeString(pf.ContentB64)
			if err != nil {
				return pullResult{err: fmt.Errorf("sync_pull: decode file %s/%s/%s: %w", escapeControl(pa.Kind), escapeControl(pa.Name), escapeControl(pf.Path), err)}
			}
			files[i] = contract.ArtifactFile{Path: pf.Path, Content: data, Executable: pf.Executable}
		}
		if hasControlRune(pa.Kind) || hasControlRune(pa.Source) || hasControlRune(pa.Version) {
			return pullResult{err: fmt.Errorf("sync_pull: invalid metadata for artifact %q: control characters are refused", pa.Name)}
		}
		// A server cannot speak for a client-side source: "plugin:<name>" is
		// built here from an approved tree and never arrives over the wire
		// (WP7). Only the bundle and the pulled KB's own artifacts do.
		if pa.Source != "bundle" && !(target.Name == "" && strings.HasPrefix(pa.Source, "kb:")) && pa.Source != "kb:"+target.Name {
			return pullResult{err: fmt.Errorf("sync_pull: %s/%s has source %q, which is neither the bundle nor the KB %q", escapeControl(pa.Kind), escapeControl(pa.Name), pa.Source, target.Name)}
		}
		if !provisioning.ValidArtifactName(pa.Name) {
			return pullResult{err: fmt.Errorf("sync_pull: invalid artifact name %q for %s", pa.Name, pa.Kind)}
		}
		if err := provisioning.CheckArtifactFilePaths(files); err != nil {
			return pullResult{err: fmt.Errorf("sync_pull: %s/%s: %w", escapeControl(pa.Kind), escapeControl(pa.Name), err)}
		}
		if got := provisioning.ContentHashFiles(files); got != pa.ContentHash {
			return pullResult{err: fmt.Errorf("sync_pull: content hash mismatch for %s/%s", escapeControl(pa.Kind), escapeControl(pa.Name))}
		}
		art := contract.Artifact{
			Kind: pa.Kind, Name: pa.Name, Source: pa.Source, Version: pa.Version,
			ContentHash: pa.ContentHash, BuiltIn: pa.BuiltIn, Signature: pa.Signature, Files: files,
		}
		if art.Kind == "agent" && len(files) == 1 {
			// Derived here rather than sent (D283): a restriction the target
			// client cannot express is a client-side fact.
			art.Restriction = contract.ParseAgentRestriction(files[0].Content)
			art.Clients = contract.AgentClientsOf(files[0].Content)
		}
		arts = append(arts, art)
	}
	fb := ""
	if pm.Feedback != nil {
		fb = pm.Feedback.Destination
	}
	return pullResult{artifacts: arts, placeholders: pm.Placeholders, pathRegistry: pm.PathRegistry, issues: pm.Issues, feedback: fb}
}

// collisionsForProvider narrows DetectCollisions to the ones a single provider
// would actually be exposed to: a kind+name is only a problem for it when two
// or more of the KBs bound to *it* claim the name. Two colliding KBs bound to
// two different providers are not a conflict, and reporting them as one would
// train an operator to ignore the warning.
func collisionsForProvider(candidates []contract.Artifact, boundKBs []string) []provisioning.Collision {
	bound := make(map[string]bool, len(boundKBs))
	for _, kb := range boundKBs {
		bound["kb:"+kb] = true
	}
	var out []provisioning.Collision
	for _, c := range provisioning.DetectCollisions(candidates) {
		var claiming []string
		for _, source := range c.Sources {
			if bound[source] {
				claiming = append(claiming, source)
			}
		}
		if len(claiming) >= 2 {
			out = append(out, provisioning.Collision{Kind: c.Kind, Name: c.Name, Sources: claiming})
		}
	}
	return out
}

func sameSignature(a, b *artifactsig.Signature) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Algorithm == b.Algorithm && a.KeyID == b.KeyID && a.EnvelopeVersion == b.EnvelopeVersion && a.Value == b.Value
}

func pinnedPublicKeys(cfg *clientconfig.Config) (map[string][]ed25519.PublicKey, error) {
	pins := make(map[string][]ed25519.PublicKey)
	for kbName, encoded := range cfg.SigningKeys {
		for _, value := range encoded {
			key, err := artifactsig.ParsePublicKey(value)
			if err != nil {
				return nil, fmt.Errorf("invalid signing key pin for KB %q: %w", kbName, err)
			}
			pins[kbName] = append(pins[kbName], key)
		}
	}
	return pins, nil
}

// materializeForProviders applies each provider's own manifest (D170),
// persisting a single v2 LockFile at <targetDir>/.cartographer-sync.lock.json (one
// Lock entry per provider), stamped with serverVersion — the version of the
// server this state was materialized against (D142). An empty serverVersion
// means the server could not be asked, and leaves the recorded value
// unchanged: an offline sync must not erase what the client knew. The lockfile
// always lives in targetDir, but each
// provider materializes under its OWN base dir (a workspace projection writes
// into its repository), recorded in that provider's Lock so prune and verification later find the same files. There is no trust switch (D906): what a KB ships is
// installed, MCP descriptors only with an approval in approvalHashes.
// searchRoots/paths come from the loaded
// clientconfig.Config (cfg.SearchRoots/cfg.Paths) and drive placeholder expansion
// (D75 WP3) — this is the one place cmd/cartographer turns
// ApplyOptions.ExpandPlaceholders on; internal/mcpserver never does.
// portabilityOptions bundles the path-portability inputs (D75, D162): they travel
// together and a fourth positional argument in an eight-argument call is how the
// next bug gets written.
type portabilityOptions struct {
	SearchRoots []string
	Paths       map[string]string
	// Streams is where the pass reports its warnings; zero is the process's.
	Streams syncStreams
	// HeldPlugins are the plugins that cannot be read this run: their lock
	// entries are carried forward unchanged instead of pruned (heldPlugins).
	HeldPlugins map[string]bool
	// MCPTokenEnv is the token variable of the client configuration, passed on
	// to a launcher entry (Codex filters the environment).
	MCPTokenEnv string
	// PruneDenied, when set, collects the workspaces whose orphaned projection
	// could not be removed for lack of permission (#2178).
	PruneDenied *[]string
	// WriteDenied, when set, collects the providers whose write was denied
	// (#2242): the pass goes on with the others. Nil keeps the error fatal.
	WriteDenied *[]writeDenial
}

// kbOrder, keyed by provider, is the KB names in that provider's explicit
// binding order (D182 WP2) — omitted or nil for a provider entry means no
// explicit binding, so its instructions block keeps the alphabetical
// fallback. See kbOrderForProviders.
func materializeForProviders(manifests map[string]contract.Manifest, projections []syncProjection, targetDir, serverVersion string, dryRun, noHeal bool, portability portabilityOptions, kbOrder map[string][]string, approvalHashes ...map[string]string) (map[string]provisioning.AppliedResult, error) {
	lockPath := lockFilePath(targetDir)
	lockFile, err := provisioning.ReadLockFile(lockPath)
	if err != nil {
		return nil, fmt.Errorf("read lockfile: %w", err)
	}
	for _, d := range lockFile.Dropped {
		fmt.Fprintf(portability.Streams.err(), "warning: %s\n", droppedEntryMessage(d))
	}

	// The launcher a client starts for a descriptor with a secret reference or a
	// header it cannot carry. Without one such a descriptor is skipped, never
	// emitted half-working.
	mcpLauncher, launcherErr := bridgeCommand()
	if launcherErr != nil || !filepath.IsAbs(mcpLauncher) {
		mcpLauncher = ""
	}
	var mcpLauncherEnv []string
	if portability.MCPTokenEnv != "" {
		mcpLauncherEnv = []string{portability.MCPTokenEnv}
	}
	results := make(map[string]provisioning.AppliedResult, len(projections))
	var approvedMCP map[string]string
	if len(approvalHashes) > 0 {
		approvedMCP = approvalHashes[0]
	}
	// Validate every authorized local command for every destination before any
	// provider file or lockfile can change, and — for a workspace projection —
	// check the repository hygiene rules too (D193 WP5). Both refusals are free
	// here: nothing has been written yet. The projection is carried into errors
	// so a failed multi-projection sync identifies the configuration it
	// protected.
	skippedProjections := map[string]bool{}
	for _, p := range projections {
		if p.Unreadable != "" {
			fmt.Fprintf(portability.Streams.err(), "warning: workspace %s: %s projection skipped: %s\n", escapeControl(p.Workspace), p.Provider, escapeControl(p.Unreadable))
			skippedProjections[projectionKey(p)] = true
			continue
		}
		// Preflight the projection's OWN view: validating the whole candidate
		// set would fail a sync over a local command belonging to a KB this
		// projection is not even bound to (D170).
		if err := provisioning.PreflightStdioMCP(manifests[projectionKey(p)], provisioning.ApplyOptions{Provider: configurator.Provider(p.Provider), BaseDir: p.BaseDir, Scope: p.Scope, ApprovedMCP: approvedMCP, MCPLauncher: mcpLauncher}); err != nil {
			return nil, err
		}
		if err := prepareWorkspace(portability.Streams, p, workspaceProviders(lockFile, projections, p.Workspace), dryRun, workspaceOwnership(&lockFile, projections, manifests, p.Workspace), derivedLocalFiles(projections, p.Workspace)...); err != nil {
			if p.Derived {
				// Nobody asked for a derived binding on this machine (D1019):
				// one repository's clash is no reason to stop the others
				// (D1181), nor is an artifact name that cannot be projected, nor
				// a repository git cannot read (dubious ownership, a corrupt
				// index, a lock): whatever the error, the binding is skipped with
				// a warning (#1502). An explicit binding keeps the refusal.
				fmt.Fprintf(portability.Streams.err(), "warning: workspace %s: %s projection skipped: %v\n", p.Workspace, p.Provider, err)
				skippedProjections[projectionKey(p)] = true
				continue
			}
			return nil, err
		}
	}
	// A projection the lockfile still records but the configuration no longer
	// declares — a workspace that was unbound, or a provider that left
	// workspace scope — must have its files removed. Nothing else would ever
	// touch them: they are outside every remaining projection, so a later sync
	// would not see them and `doctor` would report them as residue forever.
	// This runs before the projections are applied, so the files are gone
	// before anything is written in their place.
	if !dryRun {
		orphans, err := pruneOrphanProjections(portability.Streams, &lockFile, projections, manifests, targetDir, portability.PruneDenied)
		if err != nil {
			return nil, err
		}
		if len(orphans) > 0 {
			if err := provisioning.WriteLockFile(lockPath, lockFile); err != nil {
				return nil, fmt.Errorf("write lockfile after pruning %s: %w", strings.Join(orphans, ", "), err)
			}
		}
	}

	// Every projection is resolved in one run, against the union of what the
	// client sees (D1656): a requirement is satisfied by the global projection
	// and the ancestor workspaces too. Status resolves the same full set, so the
	// projection skipped above (a transient failure) still counts here: it only
	// is not applied, and must not flip the requirements of the others.
	resolved := resolveProjectionSet(projections, manifests, contract.ProjectOptions{})

	var appliedSoFar []string
	for _, p := range projections {
		if skippedProjections[projectionKey(p)] {
			continue
		}
		previous := lockFile.ForProjection(p.Key())
		manifest := manifests[projectionKey(p)]
		opts := provisioning.ApplyOptions{
			Provider:           configurator.Provider(p.Provider),
			BaseDir:            p.BaseDir,
			Scope:              p.Scope,
			DryRun:             dryRun,
			NoHeal:             noHeal,
			OpenCodeMajor:      openCodeMajorFor(p.Provider),
			ApprovedMCP:        approvedMCP,
			MCPLauncher:        mcpLauncher,
			MCPLauncherEnv:     mcpLauncherEnv,
			Lock:               previous,
			CoOwnedPaths:       lockFile.CoOwnedPaths(p.Key()),
			SkipLockWrite:      true,
			ExpandPlaceholders: true,
			SearchRoots:        portability.SearchRoots,
			Paths:              portability.Paths,
			// Read off the projection's manifest before the filter below,
			// which — like every Manifest copy — does not carry it (D262).
			Placeholders:   manifest.Placeholders,
			PathRegistries: manifest.PathRegistries,
			FeedbackMode:   manifest.FeedbackMode,
			KBOrder:        projectionKBOrder(p, kbOrder),
			// The plugins that contribute to this manifest, built from
			// approved trees at this run (manifestPlugins).
			ActivePlugins: manifestPlugins(manifest),
		}
		// What a derived projection leaves alone because git tracks the file
		// is not its to prune either: those lock entries are held out of the
		// diff Apply computes and carried forward unchanged.
		// A plugin that cannot be read this run retires nothing: its entries
		// leave the diff and are carried forward (splitHeldPlugins).
		lockForApply, held := splitHeldPlugins(previous, portability.HeldPlugins)
		opts.Lock = lockForApply
		if p.Derived && len(p.SkipKinds) > 0 {
			var rest []provisioning.ManagedFile
			for _, mf := range lockForApply.Managed {
				if slices.Contains(p.SkipKinds, mf.Kind) {
					held = append(held, mf)
				} else {
					rest = append(rest, mf)
				}
			}
			lockCopy := lockForApply
			lockCopy.Managed = rest
			opts.Lock = lockCopy
		}
		if p.Derived {
			// A derived binding never edits a tracked file (D1019): the
			// instructions block goes to an untracked file, or nowhere.
			opts.InstructionsRel = p.InstructionsRel
			opts.HookSettingsRel = p.HookSettingsRel
		}
		opts.WithholdLocalPaths = withholdLocalPaths(p)
		// Apply only the artifacts the provider knows how to materialize in
		// this scope: unsupported kinds are neither drift nor pending, they
		// simply don't concern it.
		// The projection also derives a skill from each agent this client cannot
		// run as a subagent; status uses the same function, or drift is permanent.
		projected := resolved[projectionKey(p)].Manifest
		degraded, skipped := provisioning.ReportNotes(resolved[projectionKey(p)].Notes)
		// The client environment section names only the authorized ones: a
		// pending artifact is not announced (D1697).
		opts.Skipped = provisioning.AuthorizedSkipped(skipped, manifests[projectionKey(p)], opts)
		opts.Unplaced = provisioning.UnplacedArtifacts(manifests[projectionKey(p)], configurator.Provider(p.Provider), p.Scope)
		applied, err := provisioning.Apply(projected, opts)
		if err != nil && portability.WriteDenied != nil && isWriteDenied(err) {
			// Nothing of this provider is recorded: its lock entry stays what
			// the last run wrote, so the next run retries it and judges
			// whatever the partial write left against that entry (D178). The
			// providers already applied are recorded by their checkpoint.
			d := newWriteDenial(p.Label(), p.BaseDir, err)
			fmt.Fprintf(portability.Streams.err(), "warning: %s not synced: %s\n", p.Label(), escapeControl(d.String()))
			*portability.WriteDenied = append(*portability.WriteDenied, d)
			continue
		}
		if err != nil {
			// Name what is already recorded, so a rerun is informed rather
			// than a guess about how far the previous one got.
			if len(appliedSoFar) > 0 {
				return nil, fmt.Errorf("apply %s: %w (already applied and recorded: %s)", p.Label(), err, strings.Join(appliedSoFar, ", "))
			}
			return nil, fmt.Errorf("apply %s: %w", p.Label(), err)
		}
		// Degraded/Skipped are filled here, the only path that prints the summary.
		applied.Degraded, applied.Skipped = degraded, skipped
		// D737: the tool scope is the stdio bridge in the global entry, so
		// nothing is written into a workspace for it. What a pre-D737 client
		// (D717) left in a repository is removed here, on every project
		// projection, until it is gone.
		if p.Scope == contract.ScopeProject && !(p.Derived && slices.Contains(p.SkipKinds, "mcp")) {
			lock, err := provisioning.PruneServerEntry(p.BaseDir, configurator.Provider(p.Provider), applied.NewLock, dryRun)
			if err != nil {
				return nil, fmt.Errorf("apply %s: %w", p.Label(), err)
			}
			applied.NewLock = lock
		}
		applied.NewLock.Managed = append(applied.NewLock.Managed, held...)
		// Record the base dir only when it is not the lockfile's own
		// directory: every existing lockfile keeps its current meaning and no
		// migration runs (D141). SetProjection records it for a workspace.
		if p.BaseDir != targetDir {
			applied.NewLock.BaseDir = p.BaseDir
		}
		// An unknown live version preserves the recorded one rather than
		// blanking it (D142).
		applied.NewLock.ServerVersion = previous.ServerVersion
		// D320: an acknowledgement survives every sync; only connect sets it.
		applied.NewLock.SessionHookLimitAcked = previous.SessionHookLimitAcked
		if serverVersion != "" {
			applied.NewLock.ServerVersion = serverVersion
		}
		applied.NewLock.Worktree = p.Worktree
		// The plugin that declares the provider, so its removal can find the
		// section without reading the plugin's tree (#2136).
		if spec, ok := configurator.PluginOf(configurator.Provider(p.Provider)); ok {
			applied.NewLock.Plugin = spec.Origin
		}
		lockFile.SetProjection(p.Key(), applied.NewLock)
		results[projectionKey(p)] = applied

		// Checkpoint after every provider, not once at the end (D172). With a
		// single trailing write, a failure on provider N left providers
		// 1..N-1 with files on disk and NO lock entry: unmanaged files that
		// nothing prunes and doctor cannot see. This protects COMPLETED
		// providers; it does not make one Apply atomic, so the failed
		// provider's own partial files are still possible (D178).
		if !dryRun {
			if err := provisioning.WriteLockFile(lockPath, lockFile); err != nil {
				return nil, fmt.Errorf("write lockfile after %s: %w", p.Label(), err)
			}
			appliedSoFar = append(appliedSoFar, p.Label())
		}
	}
	return results, nil
}

// resolveProjectionSet resolves what each projection receives: the source
// manifest after what a derived binding leaves out (D1019), then the one
// resolver (degradation and `requires`). Sync and status both call it, or a
// requirement would flip in one and not in the other and drift would be
// permanent.
func resolveProjectionSet(ps []syncProjection, manifests map[string]contract.Manifest, opts contract.ProjectOptions) map[string]contract.ProjectionResult {
	inputs := make([]contract.ProjectionInput, 0, len(ps))
	for _, p := range ps {
		m := manifests[projectionKey(p)]
		if p.Derived {
			if p.InstructionsSkipped != "" {
				m = withoutInstructions(m)
			}
			m = withoutKinds(m, p.SkipKinds)
		}
		inputs = append(inputs, contract.ProjectionInput{Key: projectionKey(p), Provider: configurator.Provider(p.Provider), Scope: p.Scope, Workspace: p.Workspace, Manifest: m})
	}
	return contract.ResolveProjections(inputs, opts)
}

// ensureBootstrapForProviders ensures the cartographer-bootstrap hook (D60,
// provisioning.EnsureBootstrapHook) is materialized and registered for every
// provider in providers, merging its ManagedFile entries into the v2 lockfile and
// persisting it (unless dryRun). Called by both `connect` and `sync`, independent
// of whether the server manifest could be fetched — the bootstrap hook is purely
// local, and it's exactly what lets a session self-heal via `cartographer sync`
// once the server becomes reachable, so it must be ensured even when connect's own
// manifest fetch is deferred (server down at connect time).
//
// A provider whose client directory the write guard refuses (a symlinked
// $HOME/.codex, D148) does not stop the others: it is returned as a warning
// naming the refused path, left out of the lockfile so the next run retries it,
// and the loop goes on. Any other error stays fatal.
func ensureBootstrapForProviders(providers []string, targetDir string, dryRun bool) ([]string, error) {
	warnings, _, err := ensureBootstrapRefusals(providers, targetDir, dryRun)
	return warnings, err
}

// ensureBootstrapDenials is ensureBootstrapForProviders for `sync`: a provider
// whose hook cannot be written for lack of permission is returned as a denial
// instead of failing the others (#2242).
func ensureBootstrapDenials(providers []string, targetDir string, dryRun bool) ([]string, []writeDenial, error) {
	var denied []writeDenial
	warnings, _, err := ensureBootstrap(providers, targetDir, dryRun, &denied)
	return warnings, denied, err
}

// ensureBootstrapRefusals is ensureBootstrapForProviders that also names, per
// provider, the hooks the guard refused (#1693), for the connect summary.
func ensureBootstrapRefusals(providers []string, targetDir string, dryRun bool) ([]string, map[string][]string, error) {
	return ensureBootstrap(providers, targetDir, dryRun, nil)
}

func ensureBootstrap(providers []string, targetDir string, dryRun bool, denied *[]writeDenial) ([]string, map[string][]string, error) {
	lockPath := lockFilePath(targetDir)
	lockFile, err := provisioning.ReadLockFile(lockPath)
	if err != nil {
		return nil, nil, fmt.Errorf("read lockfile: %w", err)
	}

	var warnings []string
	refused := map[string][]string{}
	for _, p := range providers {
		lock := lockFile.ForProvider(p)
		if dryRun {
			for _, n := range provisioning.SyntheticHookDryRunNotes(targetDir, configurator.Provider(p)) {
				if !slices.Contains(warnings, n) {
					warnings = append(warnings, n)
				}
			}
		}
		newLock, err := provisioning.EnsureBootstrapHook(targetDir, configurator.Provider(p), lock, dryRun)
		if provisioning.IsRefusal(err) {
			warnings = append(warnings, fmt.Sprintf("%s: bootstrap hook not installed: %v", p, err))
			refused[p] = append(refused[p], partBootstrapHook)
			continue
		}
		if err != nil && denied != nil && isWriteDenied(err) {
			d := newWriteDenial(p, targetDir, err)
			warnings = append(warnings, fmt.Sprintf("%s: bootstrap hook not installed: %s", p, d))
			*denied = append(*denied, d)
			continue
		}
		if err != nil {
			return nil, nil, fmt.Errorf("ensure bootstrap hook (%s): %w", p, err)
		}
		withFindings, note, err := provisioning.EnsureWriteFindingsHookNote(targetDir, configurator.Provider(p), newLock, true, dryRun)
		if note != "" {
			warnings = append(warnings, note)
		}
		if provisioning.IsRefusal(err) {
			warnings = append(warnings, fmt.Sprintf("%s: write-findings hook not installed: %v", p, err))
			refused[p] = append(refused[p], partFindingsHook)
		} else if err != nil && denied != nil && isWriteDenied(err) {
			d := newWriteDenial(p, targetDir, err)
			warnings = append(warnings, fmt.Sprintf("%s: write-findings hook not installed: %s", p, d))
			*denied = append(*denied, d)
		} else if err != nil {
			return nil, nil, fmt.Errorf("ensure write-findings hook (%s): %w", p, err)
		} else {
			newLock = withFindings
		}
		lockFile.SetProvider(p, newLock)
	}

	if !dryRun {
		if err := provisioning.WriteLockFile(lockPath, lockFile); err != nil {
			return nil, nil, fmt.Errorf("write lockfile: %w", err)
		}
	}
	return warnings, refused, nil
}

// printApplySummary prints a one-line-per-file summary of a materialization pass.
// dir is the base-dir the artifacts were materialized into — used to print the
// resolved settings.json path in printHookRegistered (D57).
//
// dryRun is not cosmetic: Apply fills AppliedResult with what it *would* write
// when it changes nothing, so reporting those lines in the past tense describes
// a pass that never happened — precisely when the reader asked to see the plan
// before committing to it (D147).
//
// notReceived is explicit, not variadic: a caller that has the reasons of the
// descriptors a client does not receive must pass them, and one that has none
// says nil, so they cannot be dropped by omission.
func printApplySummary(wr io.Writer, dir string, results map[string]provisioning.AppliedResult, dryRun bool, notReceived map[string][]mcpNotReceived) {
	needsMCPApproval := false
	// "wrote" / "would write", chosen once so every line of the pass agrees.
	verb := func(done, planned string) string {
		if dryRun {
			return planned
		}
		return done
	}
	printLeftOutCells(wr)
	// An MCP descriptor a bound client does not receive says why (mcpreach.go).
	printMCPNotReceived(wr, notReceived, dryRun)
	for _, key := range sortedKeys(results) {
		r := results[key]
		p := projectionLabel(key)
		provider, workspace := splitProjectionKey(key)
		projDir := dir
		hookScope := contract.ScopeGlobal
		if workspace != "" {
			projDir = workspace
			hookScope = contract.ScopeProject
		}
		// One line per file written, not per artifact: the instructions block is
		// one physical write shared by every mounted KB, so reporting it once per
		// KB printed three identical lines for one file, which reads like a loop
		// (D154). Only the instructions kind is collapsed — elsewhere two
		// artifacts never share a path, and a blanket dedup would hide a real bug.
		reportedInstructions := map[string]bool{}
		for _, w := range r.Written {
			if w.Kind == "instructions" {
				if reportedInstructions[w.Path] {
					continue
				}
				reportedInstructions[w.Path] = true
			}
			fmt.Fprintf(wr, "[%s] %s %s\n", p, verb("wrote", "would write"), escapeControl(w.Path))
			// The registration line reports a side effect on a shared file
			// that a dry run does not perform either.
			if !dryRun && hookRegistrationManagedFile(provider, w) {
				printHookRegistered(wr, p, provider, projDir, w, r.HookSettings, hookScope)
			}
		}
		reportedPruned := map[string]bool{}
		for _, pr := range r.Pruned {
			cleanPath := filepath.ToSlash(pr.Path)
			if pr.Kind == "instructions" && reportedPruned[cleanPath] {
				continue
			}
			// A section that left the block is "pruned" in the lock's terms, but
			// when the same pass rewrote the file the block is still there: the
			// line would say the file went away right after saying it was
			// written (#2199).
			if pr.Kind == "instructions" && reportedInstructions[pr.Path] {
				continue
			}
			reportedPruned[cleanPath] = true
			fmt.Fprintf(wr, "[%s] %s %s\n", p, verb("pruned", "would prune"), escapeControl(cleanPath))
		}
		// A heal means a local change was discarded (D139): say so on its own
		// line instead of folding it into the ordinary writes above.
		healedArtifacts := map[string]bool{}
		for _, h := range r.Healed {
			key := h.Kind + "/" + h.Name
			if healedArtifacts[key] {
				continue
			}
			healedArtifacts[key] = true
			fmt.Fprintf(wr, "[%s] %s %s from the server (local changes discarded)\n", p, verb("restored", "would restore"), escapeControl(key))
		}
		reregisteredArtifacts := map[string]bool{}
		for _, h := range r.Reregistered {
			key := h.Kind + "/" + h.Name
			if reregisteredArtifacts[key] {
				continue
			}
			reregisteredArtifacts[key] = true
			fmt.Fprintf(wr, "[%s] %s %s (its registration was missing)\n", p, verb("re-registered", "would re-register"), escapeControl(key))
		}
		movedArtifacts := map[string]bool{}
		for _, h := range r.Relocated {
			key := h.Kind + "/" + h.Name
			if movedArtifacts[key] {
				continue
			}
			movedArtifacts[key] = true
			fmt.Fprintf(wr, "[%s] %s %s to its current location\n", p, verb("moved", "would move"), escapeControl(key))
		}
		for _, d := range r.Divergent {
			fmt.Fprintf(wr, "[%s] diverged locally (%s): %s/%s at %s — `cartographer sync` restores it\n", p, escapeControl(d.Reason), escapeControl(d.Kind), escapeControl(d.Name), escapeControl(d.Path))
		}
		for _, na := range r.NeedsApproval {
			// An MCP descriptor, a hook and an executable file wait for an approval
			// (D906, D1141): the rest of what a KB ships is installed.
			needsMCPApproval = true
			fmt.Fprintf(wr, "[%s] needs_approval: %s/%s [%s]\n", p, escapeControl(na.Kind), escapeControl(na.Name), escapeControl(na.Source))
		}
		for _, ua := range r.Unsupported {
			fmt.Fprintf(wr, "[%s] unsupported: %s/%s [%s] (not installed for this provider: its kind has no destination there, or a warning says why it was skipped)\n", p, escapeControl(ua.Kind), escapeControl(ua.Name), escapeControl(ua.Source))
		}
		// What the resolver decided for this client (D1656): an agent installed as
		// a skill, an artifact left out for a requirement, and the capabilities
		// the install leans on that nobody probed. A dry run has not installed
		// anything yet, so it says "would".
		for _, d := range r.Degraded {
			fmt.Fprintf(wr, "[%s] %s: %s -> %s\n", p, verb("degraded", "would degrade"), safeField(d.Artifact), safeField(d.As))
		}
		for _, sk := range r.Skipped {
			fmt.Fprintf(wr, "[%s] %s: %s %s\n", p, verb("skipped", "would skip"), safeField(sk.Artifact), safeField(sk.Reason))
		}
		if n := len(r.Unverified); n > 0 {
			ids := make([]string, n)
			for i, id := range r.Unverified {
				ids[i] = safeField(string(id))
			}
			fmt.Fprintf(wr, "[%s] unverified: %d capabilities (%s)\n", p, n, strings.Join(ids, ", "))
		}
		// A refusal is not one warning among others: the artifact is not
		// installed, and only the operator can clear the condition (D148).
		for _, rf := range append(slices.Clone(r.Refused), r.RefusedRemovals...) {
			if !refusedForSymlink(rf, r.Warnings) {
				continue // the warning below names its own cause
			}
			fmt.Fprintf(wr, "[%s] %s: %s/%s [%s] — its destination is a symlink, so the write would land outside the client (list the link under providers.<provider>.allow_symlinks to follow it)\n",
				p, verb("refused", "would refuse"), escapeControl(rf.Kind), escapeControl(rf.Name), escapeControl(rf.Source))
		}
		for _, w := range r.Warnings {
			fmt.Fprintf(wr, "[%s] warning: %s\n", p, escapeControl(w))
		}
	}
	// One line for every placeholder no provider could resolve (D262), not
	// one per provider and per occurrence: the same key missing for three
	// clients is one problem with one fix.
	var ignored []string
	if cfg, err := clientconfig.Load(dir); err == nil {
		ignored = cfg.IgnoredPaths
	}
	unresolved, fresh := unresolvedAcross(results, ignored)
	if w := provisioning.UnresolvedPlaceholdersWarning(unresolved, fresh); w != "" {
		fmt.Fprintf(wr, "warning: %s\n", escapeText(w))
	}
	// Two KBs declaring one key differently (D263): the same conflict for
	// every provider bound to both, so each is printed once.
	for _, w := range registryWarningsAcross(results) {
		fmt.Fprintf(wr, "warning: %s\n", escapeControl(w))
	}
	if needsMCPApproval {
		fmt.Fprintln(wr, "Pending artifacts (MCP descriptors, hooks, executable files) require an approval: cartographer approve <kind> <name> --kb <kb>, then cartographer sync")
	}
}

// refusedForSymlink reports whether a refusal is the symlink one: the warning
// the refusal recorded ("<kind>/<name> not installed: <cause>", or "not
// removed") names it. Another cause (a rules file with foreign frontmatter) is
// printed by that warning alone; a refusal with no such warning keeps the
// symlink wording it has always had.
func refusedForSymlink(rf contract.Artifact, warnings []string) bool {
	prefix := rf.Kind + "/" + rf.Name + " not "
	for _, w := range warnings {
		if strings.HasPrefix(w, prefix) {
			return strings.Contains(w, "symlink")
		}
	}
	return true
}

// unresolvedAcross unions the unresolved placeholders of every applied
// projection, leaving out the ones the operator marked absent on this machine
// (D282). The reasons agree for one key across providers (same `paths:`, same
// search roots), so which one is kept does not matter. fresh holds the keys
// that projection had not already recorded as unresolved at its previous sync:
// only those are worth a line of detail.
func unresolvedAcross(results map[string]provisioning.AppliedResult, ignored []string) (unresolved map[string]string, fresh map[string]bool) {
	unresolved, fresh = map[string]string{}, map[string]bool{}
	for _, r := range results {
		prior := map[string]bool{}
		for _, id := range r.PriorUnresolved {
			prior[id] = true
		}
		for id, reason := range r.NewLock.UnresolvedPlaceholders {
			if containsString(ignored, id) {
				continue
			}
			unresolved[id] = reason
			if !prior[id] {
				fresh[id] = true
			}
		}
	}
	return unresolved, fresh
}

// registryWarningsAcross is the sorted, de-duplicated union of every
// projection's paths.yaml conflict warnings (D263).
func registryWarningsAcross(results map[string]provisioning.AppliedResult) []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range results {
		for _, w := range r.RegistryWarnings {
			if !seen[w] {
				seen[w] = true
				out = append(out, w)
			}
		}
	}
	sort.Strings(out)
	return out
}

// hookRegistrationManagedFile reports whether w is the ManagedFile whose
// presence in AppliedResult.Written signals "this Apply (re)ran the hook's
// provider-native registration step" — the trigger differs by provider because
// claude/codex patch an existing shared file (settings.json/config.toml) as a
// side effect of materializing hook.json, so hook.json itself is the (always
// present) trigger for them; opencode instead generates its own dedicated
// registration artifact (the plugin wrapper, D59) as a separate ManagedFile,
// which is the trigger there — and is absent when the hook's event has no
// OpenCode equivalent (see registerOpenCodePlugin), correctly suppressing the
// message in that case.
func hookRegistrationManagedFile(provider string, w provisioning.ManagedFile) bool {
	if w.Kind != "hook" {
		return false
	}
	p := configurator.Provider(provider)
	if plugin := provisioning.HookPluginRelPath(p, w.Name, contract.ScopeGlobal); plugin != "" {
		return filepath.Base(w.Path) == filepath.Base(plugin)
	}
	if provisioning.HookRegistrationFile(p) != "" {
		return filepath.Base(w.Path) == "hook.json"
	}
	return false
}

// printHookRegistered prints the one-line confirmation that a materialized
// hook was also registered in the provider's own file: settings.json (D57),
// config.toml (D58), or its own generated plugin file (D59 — the plugin *is*
// the registration, so its own path is printed instead of a separate shared
// file).
func printHookRegistered(wr io.Writer, label, provider, dir string, w provisioning.ManagedFile, settingsRel string, scope contract.Scope) {
	registeredIn, _ := hookRegistrationTargetScoped(configurator.Provider(provider), w.Name, w.Path, settingsRel, scope)
	if registeredIn == "" {
		return
	}
	fmt.Fprintf(wr, "[%s] hook %q registered in %s\n", label, w.Name, filepath.Join(dir, registeredIn))
}

// hookRegistrationTargetScoped is the one resolution of where a hook is registered,
// relative to the scope's base directory, shared by what sync prints and what
// status predicts (#1300): the generated plugin (plugin true: the file is the
// registration itself, managedPath), else the untracked settings file of a
// derived Claude Code binding (settingsRel, #1275), else the provider's own
// file. Empty when the provider has no registration of its own.
func hookRegistrationTargetScoped(p configurator.Provider, hook, managedPath, settingsRel string, scope contract.Scope) (rel string, plugin bool) {
	switch {
	case provisioning.HookPluginRelPath(p, hook, scope) != "":
		// The generated plugin *is* the registration (D59): its own path.
		return managedPath, true
	case settingsRel != "":
		return settingsRel, false
	default:
		return provisioning.HookRegistrationFileScoped(p, scope), false
	}
}

// sortedKeys returns the keys of a map[string]provisioning.AppliedResult sorted,
// so command output is deterministic across runs.
func sortedKeys(m map[string]provisioning.AppliedResult) []string {
	return slices.Sorted(maps.Keys(m))
}

// resolveTargetProviders resolves connect's positional target and --agents CSV:
//   - no target or "all" → every detected agent;
//   - one explicit positional provider → that provider, regardless of detection;
//   - --agents → the selected validated provider subset.
//
// A positional target and --agents are deliberately mutually exclusive so a
// command cannot quietly ignore one of two conflicting selections.
func resolveTargetProviders(target, csv string) ([]string, error) {
	if csv != "" {
		if target != "" {
			return nil, fmt.Errorf("--agents cannot be used with positional provider %q", target)
		}
		return resolveProviderCSV(csv)
	}
	if target == "" || target == "all" {
		var out []string
		for _, a := range agents.Detect() {
			if a.Installed {
				out = append(out, string(a.Provider))
			}
		}
		return out, nil
	}
	return resolveProvider(target)
}

// splitProviderCSV splits a --agents value into its names, validating none:
// setup checks them after the plugins step, which may register a provider.
func splitProviderCSV(csv string) ([]string, error) {
	var names []string
	for _, part := range strings.Split(csv, ",") {
		name := strings.TrimSpace(part)
		if name == "" {
			return nil, fmt.Errorf("invalid --agents value %q (want comma-separated %s)", csv, providerNamesJoined())
		}
		if !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	return names, nil
}

// resolveProviderCSV validates a non-empty comma-separated provider list while
// preserving its order. Repeating a provider is harmless but redundant, so it
// is represented only once in the resulting operation.
func resolveProviderCSV(csv string) ([]string, error) {
	var providers []string
	seen := make(map[string]bool)
	for _, part := range strings.Split(csv, ",") {
		name := strings.TrimSpace(part)
		if name == "" {
			return nil, fmt.Errorf("invalid --agents value %q (want comma-separated %s)", csv, providerNamesJoined())
		}
		provider, err := resolveProvider(name)
		if err != nil {
			return nil, err
		}
		if !seen[provider[0]] {
			providers = append(providers, provider[0])
			seen[provider[0]] = true
		}
	}
	return providers, nil
}

func resolveProvider(target string) ([]string, error) {
	if _, ok := configurator.Lookup(configurator.Provider(target)); ok {
		return []string{target}, nil
	}
	return nil, fmt.Errorf("unknown provider %q (want %s)", target, providerNamesJoined())
}

// providerNamesJoined renders the supported provider identifiers for a usage
// message, from the registry rather than a hand-kept list (D137/D141: adding a
// provider must not leave stale help text behind).
func providerNamesJoined() string {
	names := make([]string, 0, len(configurator.ProviderList()))
	for _, p := range configurator.ProviderList() {
		names = append(names, string(p))
	}
	return strings.Join(names, "|")
}

// splitPositional extracts a single leading positional argument (one not starting
// with "-") from args, returning it (or def if none) and the remaining arguments to
// hand to flag.FlagSet.Parse. flag.Parse stops at the first non-flag token, so a
// positional target given before the flags (as in `connect claude --server-url …`)
// must be pulled out first.
func splitPositional(args []string, def string) (string, []string) {
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		return args[0], args[1:]
	}
	return def, args
}

// parseFlagsAnywhere parses args with fs and returns the positional arguments in
// the order written, wherever they appear among the flags (`connect claude --kb x`,
// `connect --kb x claude`, `connect --kb x claude --no-input`): flag.Parse stops at
// the first non-flag token, so the loop takes it out and parses on. After a literal
// "--" everything left is positional. Binding them to names, and rejecting the
// extras, is bindPositionals (#1266).
func parseFlagsAnywhere(fs *flag.FlagSet, args []string) []string {
	var pos []string
	for {
		fs.Parse(args)
		rest := fs.Args()
		if len(rest) == 0 {
			return pos
		}
		// Parse stopped at a literal "--" (it consumed it): the remainder is
		// positional, even when it starts with a bare word (`connect -- claude --kb x`).
		if consumed := len(args) - len(rest); consumed > 0 && args[consumed-1] == "--" {
			return append(pos, rest...)
		}
		if strings.HasPrefix(rest[0], "-") && rest[0] != "-" {
			return append(pos, rest...)
		}
		pos = append(pos, rest[0])
		args = rest[1:]
	}
}

// bindPositionals assigns pos to dst in order; a positional with no destination is
// a usage error naming it (exit 2), never silently ignored (#1266).
func bindPositionals(cmd string, pos []string, dst ...*string) int {
	for i, v := range pos {
		if i >= len(dst) {
			fmt.Fprintf(os.Stderr, "Error: %s: unexpected argument %q\n", cmd, v)
			return 2
		}
		*dst[i] = v
	}
	return 0
}

// bindTargetPositional is bindPositionals for connect, reconnect and disconnect,
// whose one positional is the server (or a provider name): a second word that
// looks like a provider name is the user listing agents, so the error names the
// flag that does it (#2242).
func bindTargetPositional(cmd string, pos []string, dst *string) int {
	if len(pos) > 1 && looksLikeAgentNames(pos) {
		fmt.Fprintf(os.Stderr, "Error: %s: unexpected argument %q: to pick several agents use --agents %s\n", cmd, pos[1], strings.Join(pos, ","))
		return 2
	}
	return bindPositionals(cmd, pos, dst)
}

// looksLikeAgentNames reports whether every word is a bare provider-style name
// (no URL, path or host), which is what a list of agents looks like.
func looksLikeAgentNames(words []string) bool {
	for _, w := range words {
		if w == "" || strings.ContainsAny(w, "/:.\\") {
			return false
		}
	}
	return true
}

// noExtraArgs parses a command that takes flags only: a positional is a usage
// error naming it (exit 2), not something to drop in silence (#1266).
func noExtraArgs(fs *flag.FlagSet, args []string) int {
	return bindPositionals(fs.Name(), parseFlagsAnywhere(fs, args))
}

// versionIsComparable rejects the two cases where a version difference means
// nothing: an unknown version (a lockfile from before D142, or a sync that
// could not reach /health) and a local "dev" build, which changes with every
// `go build`. Same rule the advisory client/server skew line already applies
// in `status`.
func versionIsComparable(v string) bool { return v != "" && v != "dev" }

// serverChangeVersions returns the server versions this client's provider
// state was materialized against (D142) when they are not the version
// answering now — sorted, or nil when there is nothing to say. It is the one
// place that decides whether a server change is worth a word; the two callers
// below only phrase it.
//
// Once per invocation, not once per provider: several providers recording
// different versions is one fact about the server, not three, so the versions
// come back as one deduplicated set rather than one answer per provider.
func serverChangeVersions(dir string, providers []string, liveVersion string) []string {
	if !versionIsComparable(liveVersion) {
		return nil
	}
	lockFile, err := provisioning.ReadLockFile(lockFilePath(dir))
	if err != nil {
		return nil
	}
	var recorded []string
	seen := map[string]bool{}
	for _, p := range providers {
		v := lockFile.ForProvider(p).ServerVersion
		if !versionIsComparable(v) || v == liveVersion || seen[v] {
			continue
		}
		seen[v] = true
		recorded = append(recorded, v)
	}
	if len(recorded) == 0 {
		return nil
	}
	sort.Strings(recorded)
	return recorded
}

// serverChangeNotice is the wording for `status` and `doctor`: both only
// observe, so naming the repairing command is the single actionable thing they
// can offer (D143). Returns "" when there is nothing to say.
//
// `sync` does not use it — it repairs as it reports, so it has its own wording
// below (D220). The two must not collapse back into one.
func serverChangeNotice(dir string, providers []string, liveVersion string) string {
	recorded := serverChangeVersions(dir, providers, liveVersion)
	if len(recorded) == 0 {
		return ""
	}
	return fmt.Sprintf("the server changed since this client was configured (was %s, now %s) — run `cartographer reconnect` to rebuild the client configuration",
		strings.Join(recorded, ", "), liveVersion)
}

// syncServerChangeNotice is the wording for `sync`, printed before the sync
// runs — or "" when there is nothing to say. It states the fact and what this
// run does about it, and carries no imperative: the run under way already
// re-applies the current manifest, so telling the reader to reconnect ahead of
// output they have not seen reads as a prerequisite it is not (D220). It
// reports; it never escalates: the ordinary sync runs either way, and
// `reconnect` stays the user's explicit call for the residue an incremental
// sync structurally cannot see.
func syncServerChangeNotice(dir string, providers []string, liveVersion string) string {
	recorded := serverChangeVersions(dir, providers, liveVersion)
	if len(recorded) == 0 {
		return ""
	}
	return fmt.Sprintf("the server changed since this client was configured (was %s, now %s); this sync re-applies the current manifest — `cartographer reconnect` is needed only for files an older version wrote under names no longer in the managed set",
		strings.Join(recorded, ", "), liveVersion)
}

// maxPrintedField caps one KB-derived field in a summary line.
const maxPrintedField = 200

// safeField is escapeControl (one sanitiser: internal/termsafe) with a field over
// maxPrintedField runes cut with an ellipsis.
func safeField(s string) string { return client.EscapeCap(s, maxPrintedField) }

// syncPullArgs always asks the server for the bundled cartographer-cli skill
// (D1700): a current client filters it per provider (withoutCLISkill), so only
// a provider whose MCPMode is cli keeps it, and asking unconditionally means
// such a provider registered by this very run gets it without a lag. A client
// older than the filter never sends the flag, so it never receives the skill.
func syncPullArgs() map[string]any {
	return map[string]any{"include_cli_skill": true}
}

// droppedEntryMessage says that ReadLockFile ignored a lockfile entry whose
// path leaves the client directory (#1824); sync and doctor share the wording.
func droppedEntryMessage(d provisioning.DroppedEntry) string {
	where := escapeControl(d.Provider)
	if d.Workspace != "" {
		where += " (workspace " + escapeControl(d.Workspace) + ")"
	}
	return fmt.Sprintf("lockfile entry %s/%s of %s ignored: path %s (or its base dir) is not inside the client directory",
		escapeControl(d.Kind), escapeControl(d.Name), where, escapeControl(d.Path))
}

// mergeFeedbackMode combines the modes two KBs' sync_pull reported: off wins,
// then custom, else github ("").
func mergeFeedbackMode(a, b string) string {
	rank := func(m string) int {
		switch m {
		case contract.FeedbackOff:
			return 2
		case contract.FeedbackCustom:
			return 1
		}
		return 0
	}
	if rank(b) > rank(a) {
		return b
	}
	return a
}
