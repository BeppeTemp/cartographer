package main

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/BeppeTemp/cartographer/internal/agents"
	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/clientvariant"
	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// variantPending is a declared variant the client config does not authorize.
type variantPending struct {
	Name, KB string
	// Moved: the bytes are approved but resolve to other paths here (D1699).
	Moved bool
}

// variantSyncResult is what one reconcile of the KBs' variant declarations with
// the cache did. Cached are the names stored or refreshed, Retired the ones
// dropped, Detected the cached variants whose client is installed here.
type variantSyncResult struct {
	Cached     []string
	Pending    []variantPending
	Collisions []string
	Retired    []string
	Detected   []string
	Warnings   []string
}

// reconcileVariants is the sync side of the variant cache (D1702). arts are the
// variant artifacts the KBs served this run (the client lock is held, as for the
// state file), unfiltered: they decide collisions and "still declared". reach
// (source+NUL+name) is the subset that reached a projection, the only one that
// may be cached, refreshed or registered: an opted-out KB (`artifacts: false`) or
// one outside a projection's workspace scoping keeps what is cached, as it is,
// and gets nothing new. nil means every declaration reached one. pulled names the KBs whose pull answered, and complete says the
// server was reachable: a cached variant is retired only when its own KB
// answered and no longer declares it, because a partial view proves no absence.
//
// A variant is cached only when sync's own gate accepts it (ArtifactAuthorized:
// descriptor hash and resolved-path fingerprint). A declared but unapproved
// variant is reported with the command that approves it and cached nothing; a
// cached one whose KB now serves other, unapproved bytes keeps its old entry,
// which stays authorized by the approval of the old bytes until the operator
// decides. A name two KBs declare is registered by neither (nothing says which
// one the operator approved) and its cached entry is left as it is.
//
// Registration is redone from the final cache through RegisterCachedVariants,
// the one gated path. A retired variant is disconnected first, from the entry
// registered at start, so its files are pruned through the lock; an entry that
// was never registered this run (not authorized) has nothing of ours to prune
// and just leaves the cache. cfg.Agents loses the retired names.
func reconcileVariants(dir string, cfg *clientconfig.Config, arts []provisioning.Artifact, reach map[string]bool, pulled map[string]bool, complete, dryRun bool) variantSyncResult {
	var res variantSyncResult
	cache, err := clientvariant.LoadCache(dir)
	if err != nil {
		res.Warnings = append(res.Warnings, "client variant cache ignored: "+err.Error())
		cache = clientvariant.Cache{}
	}
	opts := provisioning.ApplyOptions{ApprovedMCP: cfg.ApprovedMCPHashes(), ApprovedVariantPaths: cfg.ApprovedPathFingerprints()}

	bySource := map[string]map[string]provisioning.Artifact{} // name -> source -> artifact
	for _, a := range arts {
		if a.Kind != "variant" {
			continue
		}
		if bySource[a.Name] == nil {
			bySource[a.Name] = map[string]provisioning.Artifact{}
		}
		bySource[a.Name][a.Source] = a
	}
	names := make([]string, 0, len(bySource))
	for n := range bySource {
		names = append(names, n)
	}
	sort.Strings(names)

	next := make(clientvariant.Cache, len(cache))
	for n, e := range cache {
		next[n] = e
	}
	declared := map[string]bool{}
	for _, name := range names {
		declared[name] = true
		sources := bySource[name]
		if len(sources) > 1 {
			res.Collisions = append(res.Collisions, name)
			continue
		}
		var a provisioning.Artifact
		for _, s := range sources {
			a = s
		}
		kb := strings.TrimPrefix(a.Source, "kb:")
		if reach != nil && !reach[a.Source+"\x00"+a.Name] {
			continue
		}
		if len(a.Files) != 1 || provisioning.ContentHashFiles(a.Files) != a.ContentHash {
			res.Warnings = append(res.Warnings, fmt.Sprintf("client variant %s from KB %s: the served bytes do not match their hash, left out", name, kb))
			continue
		}
		if !provisioning.ArtifactAuthorized(a, opts) {
			res.Pending = append(res.Pending, variantPending{Name: name, KB: kb, Moved: variantPathsMoved(cfg, a)})
			continue
		}
		if old, ok := next[name]; ok && old.SourceKB == kb && old.ContentHash == a.ContentHash {
			continue
		}
		next[name] = clientvariant.NewCacheEntry(kb, a.ContentHash, a.Files[0].Content, time.Now())
		res.Cached = append(res.Cached, name)
	}

	// A cached name is only provably gone when its own KB answered.
	for name, e := range cache {
		if !pulled[e.SourceKB] {
			declared[name] = true
		}
	}
	retired := cache.Retired(declared, complete)
	for _, name := range retired {
		res.Retired = append(res.Retired, name)
		delete(next, name)
	}

	if dryRun {
		return res
	}
	for _, name := range retired {
		if _, ok := cache[name]; !ok {
			continue
		}
		if _, registered := configurator.VariantOf(configurator.Provider(name)); registered {
			dres, err := doDisconnectLocked(disconnectOptions{Providers: []string{name}, Dir: dir, KeepToken: true})
			if err == nil {
				// A symlink refusal is a result, not an error: the lock section
				// stays and so must the cache entry, or nothing would own the files.
				for _, pr := range dres.Providers {
					if len(pr.Refused) > 0 {
						err = fmt.Errorf("refused: %s", strings.Join(pr.Refused, "; "))
					}
				}
			}
			if err != nil {
				// Not retired: the entry stays so the next sync tries again.
				res.Warnings = append(res.Warnings, fmt.Sprintf("client variant %s is no longer declared but could not be disconnected: %v", name, err))
				next[name] = cache[name]
				res.Retired = removeStrings(res.Retired, []string{name})
				continue
			}
		}
		cfg.Agents = removeStrings(cfg.Agents, []string{name})
		delete(cfg.Workspaces, name)
		delete(cfg.Scopes, name)
	}
	if len(res.Cached) > 0 || len(res.Retired) > 0 {
		if err := clientvariant.SaveCache(dir, next); err != nil {
			res.Warnings = append(res.Warnings, "client variant cache not saved: "+err.Error())
			return res
		}
	}
	reg := provisioning.RegisterCachedVariants(next, opts)
	res.Warnings = append(res.Warnings, reg.Warnings...)
	for _, a := range agents.Detect() {
		if a.Variant && a.Installed && next[string(a.Provider)].SourceKB != "" {
			res.Detected = append(res.Detected, string(a.Provider))
		}
	}
	return res
}

// forgetCachedVariant drops a variant from the cache of dir and deregisters it,
// for `approve revoke variant`: what is installed stays, nothing refreshes it.
// The caller holds the client lock.
func forgetCachedVariant(dir, name string) error {
	cache, err := clientvariant.LoadCache(dir)
	if err != nil {
		return err
	}
	if _, ok := cache[name]; !ok {
		return nil
	}
	if err := clientvariant.SaveCache(dir, cache.Without(name)); err != nil {
		return err
	}
	provisioning.UnregisterVariant(name)
	return nil
}

// printVariantSync reports what reconcileVariants did and found.
func printVariantSync(w io.Writer, res variantSyncResult, dryRun bool) {
	e := escapeControl
	for _, p := range res.Pending {
		note := ""
		if p.Moved {
			note = " (approved for other paths: the descriptor resolves differently in this environment)"
		}
		fmt.Fprintf(w, "client variant %s [kb:%s]: needs_approval%s: cartographer approve variant %s --kb %s\n", e(p.Name), e(p.KB), note, e(p.Name), e(p.KB))
	}
	for _, n := range res.Collisions {
		fmt.Fprintf(w, "client variant %s: declared by more than one KB, registered from none\n", e(n))
	}
	verb := func(done, plan string) string {
		if dryRun {
			return "[dry-run] would " + plan
		}
		return done
	}
	for _, n := range res.Cached {
		fmt.Fprintf(w, "client variant %s: %s\n", e(n), verb("cached and registered", "cache and register it"))
	}
	for _, n := range res.Retired {
		fmt.Fprintf(w, "client variant %s: no longer declared, %s\n", e(n), verb("retired", "retire it"))
	}
}

// syncVariants runs reconcileVariants from the variant declarations the pulled
// KBs served and prints the outcome; it returns the names retired, which the
// caller leaves out of the run. Under dry-run it retires nothing and returns
// nil. The declarations come from the pull itself, not from the projections'
// manifests: kb_options artifacts:false and workspace scoping drop a KB's
// artifacts there, and a variant filtered out is not a variant undeclared.
func syncVariants(out, errw io.Writer, cfg *clientconfig.Config, dir string, projections []syncProjection, declared []provisioning.Artifact, manifests map[string]provisioning.Manifest, failures []kbPullFailure, reachable, dryRun bool) []string {
	var arts []provisioning.Artifact
	seen := map[string]bool{}
	for _, a := range declared {
		if a.Kind == "variant" && !seen[a.Source+"\x00"+a.Name] {
			seen[a.Source+"\x00"+a.Name] = true
			arts = append(arts, a)
		}
	}
	reach := map[string]bool{}
	for _, m := range manifests {
		for _, a := range m.Artifacts {
			if a.Kind == "variant" {
				reach[a.Source+"\x00"+a.Name] = true
			}
		}
	}
	pulled := map[string]bool{}
	for _, p := range projections {
		if !p.BundleOnly {
			for _, kb := range p.KBs {
				pulled[kb] = true
			}
		}
	}
	for _, f := range failures {
		delete(pulled, f.KB)
	}
	res := reconcileVariants(dir, cfg, arts, reach, pulled, reachable, dryRun)
	for _, w := range res.Warnings {
		fmt.Fprintf(errw, "warning: %s\n", w)
	}
	printVariantSync(out, res, dryRun)
	if dryRun {
		return nil
	}
	return res.Retired
}
