package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
}

// lockFilePath returns the path to the v2 multi-provider lockfile inside targetDir.
func lockFilePath(targetDir string) string {
	return filepath.Join(targetDir, provisioning.LockFileName)
}

// resolveToken returns the bearer token for cfg, read from cfg.TokenEnv when
// cfg.Auth is true; empty otherwise (no Authorization header is sent).
func resolveToken(cfg *clientconfig.Config) string {
	if !cfg.Auth || cfg.TokenEnv == "" {
		return ""
	}
	return os.Getenv(cfg.TokenEnv)
}

// tokenEnvName returns the environment variable resolveToken reads for cfg, or
// empty when this config sends no credential at all. Passed to the client so a
// 401 names the variable instead of "the env var" (D222).
func tokenEnvName(cfg *clientconfig.Config) string {
	if !cfg.Auth {
		return ""
	}
	return cfg.TokenEnv
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
	Named   map[string][]provisioning.Artifact
	Bare    []provisioning.Artifact
	HasBare bool
	// Placeholders is, per KB, the placeholder keys its sync_pull listed
	// (D262); BarePlaceholders the unnamed endpoint's. Selected per
	// projection exactly like the artifacts, by placeholdersForProjection.
	Placeholders     map[string][]string
	BarePlaceholders []string
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

// all returns every candidate, in a deterministic order.
func (cs candidateSet) all() []provisioning.Artifact {
	out := append([]provisioning.Artifact(nil), cs.Bare...)
	names := make([]string, 0, len(cs.Named))
	for name := range cs.Named {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		out = append(out, cs.Named[name]...)
	}
	return out
}

// forKBs concatenates the responses of the named KBs, in a deterministic order.
// A name with no response contributes nothing — it is not an error here: the
// caller validated the binding against /health before pulling.
func (cs candidateSet) forKBs(names []string) []provisioning.Artifact {
	sorted := append([]string(nil), names...)
	sort.Strings(sorted)
	var out []provisioning.Artifact
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
func (cs candidateSet) forProvider(cfg *clientconfig.Config, provider string) []provisioning.Artifact {
	bound, explicit := cfg.BoundKBs(provider)
	if cs.HasBare {
		if explicit && len(bound) == 0 {
			return nil
		}
		return cs.Bare
	}
	return provisioning.SelectForSources(cs.forKBs(bound), bound)
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

// manifestsForProviders returns, per provider, the manifest it may receive:
// the candidates of its bound KBs, selected by source, merged strictly (so a
// cross-KB collision inside THAT provider's set stops the sync, D171) and
// signature-verified.
//
// Every KB is pulled once for all providers; the split happens afterwards.
// Verification pins are applied per provider only because VerifiedManifest
// takes a manifest — the work is over the same artifacts either way, and an
// artifact's verification result cannot differ between providers.
//
// It returns the GLOBAL projection of each provider, keyed by provider name —
// the view `status` and the TUI have always shown. A workspace-scoped provider
// has more projections than this one; `status` reports them separately (D193),
// and the sync path goes through manifestsForProjections instead, which is the
// only caller that materializes.
func manifestsForProviders(cfg *clientconfig.Config, providers []string) (map[string]provisioning.Manifest, error) {
	out := make(map[string]provisioning.Manifest, len(providers))

	union, requiredBy, anyDefault := boundKBUnion(cfg, providers)
	if len(union) == 0 && !anyDefault {
		// Every provider is explicitly bound to no KBs: there is nothing to
		// pull, and asking the server would only invite an error about a
		// selection nobody made.
		for _, p := range providers {
			out[p] = provisioning.MergeArtifacts(nil)
		}
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
	for _, p := range providers {
		merged, err := provisioning.MergeArtifactsStrict(cs.forProvider(cfg, p))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		verified, err := provisioning.VerifiedManifest(merged, pins)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		out[p] = verified
	}
	return out, nil
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

// fetchMergedManifest fetches every KB's artifacts and merges them into one
// verified provisioning.Manifest, applying the same precedence rule (KB source
// wins over bundle) BuildManifest applies server-side for one KB.
//
// The merge is strict (D171): two KBs claiming the same kind+name is an error,
// not the alphabetical coin flip preferArtifact would otherwise perform. It is
// deliberately evaluated here, before anything is materialized, so a collision
// stops the sync instead of silently deciding which KB an agent reads.
func fetchMergedManifest(cfg *clientconfig.Config) (provisioning.Manifest, error) {
	cs, err := fetchCandidates(cfg, cfg.KnownKBs)
	if err != nil {
		return provisioning.Manifest{}, err
	}
	merged, err := provisioning.MergeArtifactsStrict(cs.all())
	if err != nil {
		return provisioning.Manifest{}, err
	}
	pins, err := pinnedPublicKeys(cfg)
	if err != nil {
		return provisioning.Manifest{}, err
	}
	return provisioning.VerifiedManifest(merged, pins)
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
	cs := candidateSet{Named: make(map[string][]provisioning.Artifact), Placeholders: make(map[string][]string)}
	token := resolveToken(cfg)
	tokenEnv := tokenEnvName(cfg)
	health, err := client.New(cfg.ServerURL, token).WithTokenEnv(tokenEnv).Health(probeTimeout)
	if err != nil {
		return cs, nil, fmt.Errorf("health: %w", err)
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
			c := client.New(cfg.ServerURL, token).WithTokenEnv(tokenEnv).WithKB(target.Name)
			results[i] = pullTarget(c, target)
		}()
	}
	wg.Wait()

	seen := make(map[string]provisioning.Artifact)
	var failed []kbPullFailure

	for i, target := range targets {
		res := results[i]
		if res.callErr != nil || res.err != nil {
			err := res.err
			if res.callErr != nil {
				if target.Name == "" {
					err = fmt.Errorf("sync_pull: %w", res.callErr)
				} else {
					err = fmt.Errorf("sync_pull (kb=%s): %w", target.Name, res.callErr)
				}
			} else if target.Name != "" {
				err = fmt.Errorf("kb=%s: %w", target.Name, res.err)
			}
			// The unnamed endpoint is not a KB a provider is bound to, so
			// there is nothing to skip around: it stays fatal.
			if target.Name == "" {
				return cs, failed, err
			}
			failed = append(failed, kbPullFailure{KB: target.Name, Err: err})
			continue
		}
		for _, a := range res.artifacts {
			key := a.Kind + "\x00" + a.Name + "\x00" + a.Source
			if previous, exists := seen[key]; exists && !sameSignature(previous.Signature, a.Signature) {
				return cs, failed, fmt.Errorf("sync_pull: conflicting signatures for %s/%s", a.Kind, a.Name)
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
		} else if len(res.placeholders) > 0 {
			cs.Placeholders[target.Name] = res.placeholders
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
	artifacts    []provisioning.Artifact
	placeholders []string
	callErr      error
	err          error
}

// pullTarget calls sync_pull on one target and decodes and hash-checks its
// artifacts. It touches no state shared with other targets, so fetchCandidates
// can run it from several goroutines.
func pullTarget(c *client.MCPClient, target kbTarget) pullResult {
	raw, err := callTool(c, target, "sync_pull", map[string]any{})
	if err != nil {
		return pullResult{callErr: err}
	}
	var pm pulledManifestJSON
	if err := json.Unmarshal(raw, &pm); err != nil {
		return pullResult{err: fmt.Errorf("sync_pull: decode response: %w", err)}
	}
	arts := make([]provisioning.Artifact, 0, len(pm.Artifacts))
	for _, pa := range pm.Artifacts {
		files := make([]provisioning.ArtifactFile, len(pa.Files))
		for i, pf := range pa.Files {
			data, err := base64.StdEncoding.DecodeString(pf.ContentB64)
			if err != nil {
				return pullResult{err: fmt.Errorf("sync_pull: decode file %s/%s/%s: %w", pa.Kind, pa.Name, pf.Path, err)}
			}
			files[i] = provisioning.ArtifactFile{Path: pf.Path, Content: data, Executable: pf.Executable}
		}
		if got := provisioning.ContentHashFiles(files); got != pa.ContentHash {
			return pullResult{err: fmt.Errorf("sync_pull: content hash mismatch for %s/%s", pa.Kind, pa.Name)}
		}
		arts = append(arts, provisioning.Artifact{
			Kind: pa.Kind, Name: pa.Name, Source: pa.Source, Version: pa.Version,
			ContentHash: pa.ContentHash, BuiltIn: pa.BuiltIn, Signature: pa.Signature, Files: files,
		})
	}
	return pullResult{artifacts: arts, placeholders: pm.Placeholders}
}

// collisionsForProvider narrows DetectCollisions to the ones a single provider
// would actually be exposed to: a kind+name is only a problem for it when two
// or more of the KBs bound to *it* claim the name. Two colliding KBs bound to
// two different providers are not a conflict, and reporting them as one would
// train an operator to ignore the warning.
func collisionsForProvider(candidates []provisioning.Artifact, boundKBs []string) []provisioning.Collision {
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

// uniformManifests is the pre-D170 shape — every provider receiving the same
// manifest — kept for callers that legitimately have one: `reconnect`, which
// rebuilds from what the client already holds, and tests that do not exercise
// bindings. Production sync/connect paths build the map from the bindings via
// manifestsForProviders instead.
func uniformManifests(m provisioning.Manifest, providers []string) map[string]provisioning.Manifest {
	out := make(map[string]provisioning.Manifest, len(providers))
	for _, p := range providers {
		out[p] = m
	}
	return out
}

// materializeForProviders applies each provider's own manifest (D170),
// persisting a single v2 LockFile at <targetDir>/.cartographer-sync.lock.json (one
// Lock entry per provider), stamped with serverVersion — the version of the
// server this state was materialized against (D142). An empty serverVersion
// means the server could not be asked, and leaves the recorded value
// unchanged: an offline sync must not erase what the client knew. The lockfile
// always lives in targetDir, but each
// provider materializes under its OWN base dir (D141: every provider but
// hermes shares targetDir; hermes writes under $HERMES_HOME), recorded in that
// provider's Lock so prune and verification later find the same files. autoTrust is explicit authorization for eligible
// unsigned KB artifacts, passed to ApplyOptions.AutoTrust; it never changes an
// artifact's Signed verification result. searchRoots/paths come from the loaded
// clientconfig.Config (cfg.SearchRoots/cfg.Paths) and drive placeholder expansion
// (D75 WP3) — this is the one place cmd/cartographer turns
// ApplyOptions.ExpandPlaceholders on; internal/mcpserver never does.
// portabilityOptions bundles the path-portability inputs (D75, D162): they travel
// together and a fourth positional argument in an eight-argument call is how the
// next bug gets written.
type portabilityOptions struct {
	SearchRoots []string
	SearchDepth int
	Paths       map[string]string
}

// kbOrder, keyed by provider, is the KB names in that provider's explicit
// binding order (D182 WP2) — omitted or nil for a provider entry means no
// explicit binding, so its instructions block keeps the alphabetical
// fallback. See kbOrderForProviders.
func materializeForProviders(manifests map[string]provisioning.Manifest, projections []syncProjection, targetDir, serverVersion string, autoTrust, dryRun, noHeal bool, portability portabilityOptions, kbOrder map[string][]string, approvalHashes ...map[string]string) (map[string]provisioning.AppliedResult, error) {
	lockPath := lockFilePath(targetDir)
	lockFile, err := provisioning.ReadLockFile(lockPath)
	if err != nil {
		return nil, fmt.Errorf("read lockfile: %w", err)
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
	for _, p := range projections {
		// Preflight the projection's OWN view: validating the whole candidate
		// set would fail a sync over a local command belonging to a KB this
		// projection is not even bound to (D170).
		if err := provisioning.PreflightStdioMCP(manifests[projectionKey(p)], provisioning.ApplyOptions{Provider: configurator.Provider(p.Provider), BaseDir: p.BaseDir, Scope: p.Scope, AutoTrust: autoTrust, ApprovedMCP: approvedMCP}); err != nil {
			return nil, err
		}
		if err := prepareWorkspace(p, dryRun); err != nil {
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
		orphans, err := pruneOrphanProjections(&lockFile, projections, targetDir)
		if err != nil {
			return nil, err
		}
		if len(orphans) > 0 {
			if err := provisioning.WriteLockFile(lockPath, lockFile); err != nil {
				return nil, fmt.Errorf("write lockfile after pruning %s: %w", strings.Join(orphans, ", "), err)
			}
		}
	}

	var appliedSoFar []string
	for _, p := range projections {
		previous := lockFile.ForProjection(p.Key())
		manifest := manifests[projectionKey(p)]
		opts := provisioning.ApplyOptions{
			Provider:           configurator.Provider(p.Provider),
			BaseDir:            p.BaseDir,
			Scope:              p.Scope,
			DryRun:             dryRun,
			NoHeal:             noHeal,
			AutoTrust:          autoTrust,
			ApprovedMCP:        approvedMCP,
			Lock:               previous,
			SkipLockWrite:      true,
			ExpandPlaceholders: true,
			SearchRoots:        portability.SearchRoots,
			SearchDepth:        portability.SearchDepth,
			Paths:              portability.Paths,
			// Read off the projection's manifest before the filter below,
			// which — like every Manifest copy — does not carry it (D262).
			Placeholders: manifest.Placeholders,
			KBOrder:      kbOrder[p.Provider],
		}
		// Apply only the artifacts the provider knows how to materialize in
		// this scope: unsupported kinds are neither drift nor pending, they
		// simply don't concern it.
		applied, err := provisioning.Apply(provisioning.FilterForProviderScoped(manifest, configurator.Provider(p.Provider), p.Scope), opts)
		if err != nil {
			// Name what is already recorded, so a rerun is informed rather
			// than a guess about how far the previous one got.
			if len(appliedSoFar) > 0 {
				return nil, fmt.Errorf("apply %s: %w (already applied and recorded: %s)", p.Label(), err, strings.Join(appliedSoFar, ", "))
			}
			return nil, fmt.Errorf("apply %s: %w", p.Label(), err)
		}
		// Record the base dir only when it is not the lockfile's own
		// directory: every existing lockfile keeps its current meaning and no
		// migration runs (D141). SetProjection records it for a workspace.
		if p.BaseDir != targetDir {
			applied.NewLock.BaseDir = p.BaseDir
		}
		// An unknown live version preserves the recorded one rather than
		// blanking it (D142).
		applied.NewLock.ServerVersion = previous.ServerVersion
		if serverVersion != "" {
			applied.NewLock.ServerVersion = serverVersion
		}
		lockFile.SetProjection(p.Key(), applied.NewLock, p.Remote)
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

// ensureBootstrapForProviders ensures the cartographer-bootstrap hook (D60,
// provisioning.EnsureBootstrapHook) is materialized and registered for every
// provider in providers, merging its ManagedFile entries into the v2 lockfile and
// persisting it (unless dryRun). Called by both `connect` and `sync`, independent
// of whether the server manifest could be fetched — the bootstrap hook is purely
// local, and it's exactly what lets a session self-heal via `cartographer sync`
// once the server becomes reachable, so it must be ensured even when connect's own
// manifest fetch is deferred (server down at connect time).
func ensureBootstrapForProviders(providers []string, targetDir string, dryRun bool) error {
	lockPath := lockFilePath(targetDir)
	lockFile, err := provisioning.ReadLockFile(lockPath)
	if err != nil {
		return fmt.Errorf("read lockfile: %w", err)
	}

	for _, p := range providers {
		lock := lockFile.ForProvider(p)
		newLock, err := provisioning.EnsureBootstrapHook(targetDir, configurator.Provider(p), lock, dryRun)
		if err != nil {
			return fmt.Errorf("ensure bootstrap hook (%s): %w", p, err)
		}
		lockFile.SetProvider(p, newLock)
	}

	if !dryRun {
		if err := provisioning.WriteLockFile(lockPath, lockFile); err != nil {
			return fmt.Errorf("write lockfile: %w", err)
		}
	}
	return nil
}

// printApplySummary prints a one-line-per-file summary of a materialization pass.
// dir is the base-dir the artifacts were materialized into — used to print the
// resolved settings.json path in printHookRegistered (D57).
//
// dryRun is not cosmetic: Apply fills AppliedResult with what it *would* write
// when it changes nothing, so reporting those lines in the past tense describes
// a pass that never happened — precisely when the reader asked to see the plan
// before committing to it (D147).
func printApplySummary(dir string, results map[string]provisioning.AppliedResult, dryRun bool) {
	needsApproval := false
	needsMCPApproval := false
	// "wrote" / "would write", chosen once so every line of the pass agrees.
	verb := func(done, planned string) string {
		if dryRun {
			return planned
		}
		return done
	}
	for _, p := range sortedKeys(results) {
		r := results[p]
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
			fmt.Printf("[%s] %s %s\n", p, verb("wrote", "would write"), w.Path)
			// The registration line reports a side effect on a shared file
			// that a dry run does not perform either.
			if !dryRun && hookRegistrationManagedFile(p, w) {
				printHookRegistered(p, dir, w)
			}
		}
		for _, pr := range r.Pruned {
			fmt.Printf("[%s] %s %s\n", p, verb("pruned", "would prune"), pr.Path)
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
			fmt.Printf("[%s] %s %s from the server (local changes discarded)\n", p, verb("restored", "would restore"), key)
		}
		for _, d := range r.Divergent {
			fmt.Printf("[%s] diverged locally (%s): %s/%s at %s — `cartographer sync` restores it\n", p, d.Reason, d.Kind, d.Name, d.Path)
		}
		for _, na := range r.NeedsApproval {
			if na.Kind == "mcp" {
				needsMCPApproval = true
			} else {
				needsApproval = true
			}
			fmt.Printf("[%s] needs_approval: %s/%s [%s]\n", p, na.Kind, na.Name, na.Source)
		}
		for _, ua := range r.Unsupported {
			fmt.Printf("[%s] unsupported: %s/%s [%s] (kind has no destination for this provider)\n", p, ua.Kind, ua.Name, ua.Source)
		}
		// A refusal is not one warning among others: the artifact is not
		// installed, and only the operator can clear the condition (D148).
		for _, rf := range r.Refused {
			fmt.Printf("[%s] %s: %s/%s [%s] — its destination is a symlink, so the write would land outside the client\n",
				p, verb("refused", "would refuse"), rf.Kind, rf.Name, rf.Source)
		}
		for _, w := range r.Warnings {
			fmt.Printf("[%s] warning: %s\n", p, w)
		}
	}
	// One line for every placeholder no provider could resolve (D262), not
	// one per provider and per occurrence: the same key missing for three
	// clients is one problem with one fix.
	if w := provisioning.UnresolvedPlaceholdersWarning(unresolvedAcross(results)); w != "" {
		fmt.Printf("warning: %s\n", w)
	}
	if needsApproval {
		fmt.Printf("to approve the unsigned artifacts run: %s\n", autoTrustCommand())
	}
	if needsMCPApproval {
		fmt.Println("MCP artifacts require a point approval: cartographer approve mcp <name> --kb <kb>, then cartographer sync")
	}
}

// unresolvedAcross unions the unresolved placeholders of every applied
// projection. The reasons agree for one key across providers (same `paths:`,
// same search roots), so which one is kept does not matter.
func unresolvedAcross(results map[string]provisioning.AppliedResult) map[string]string {
	out := map[string]string{}
	for _, r := range results {
		for id, reason := range r.NewLock.UnresolvedPlaceholders {
			out[id] = reason
		}
	}
	return out
}

// autoTrustCommand returns the exact command line the user must run to approve
// unsigned KB-sourced artifacts, so every needs-approval message can print it
// verbatim instead of a vague "use --auto-trust" hint.
func autoTrustCommand() string {
	return "cartographer sync --auto-trust"
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
	if plugin := provisioning.HookPluginRelPath(p, w.Name); plugin != "" {
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
func printHookRegistered(provider, dir string, w provisioning.ManagedFile) {
	p := configurator.Provider(provider)
	var registeredIn string
	switch {
	case provisioning.HookPluginRelPath(p, w.Name) != "":
		// The generated plugin *is* the registration (D59): print its own path.
		registeredIn = filepath.Join(dir, w.Path)
	case provisioning.HookRegistrationFile(p) != "":
		registeredIn = filepath.Join(dir, provisioning.HookRegistrationFile(p))
	default:
		return
	}
	fmt.Printf("[%s] hook %q registered in %s\n", provider, w.Name, registeredIn)
}

// sortedKeys returns the keys of a map[string]provisioning.AppliedResult sorted,
// so command output is deterministic across runs.
func sortedKeys(m map[string]provisioning.AppliedResult) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j-1] > keys[j]; j-- {
			keys[j-1], keys[j] = keys[j], keys[j-1]
		}
	}
	return keys
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
