package main

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/BeppeTemp/cartographer/internal/agents"
	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/clientvariant"
	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// wantsVariantDiscovery says whether connect's target can name a client variant
// that is not registered yet: "all" or nothing (the detection must include the
// KBs' variants) or a name no registry entry carries.
func wantsVariantDiscovery(target, csv string) bool {
	unknown := func(n string) bool {
		_, ok := configurator.Lookup(configurator.Provider(strings.TrimSpace(n)))
		return !ok
	}
	if csv != "" {
		for _, n := range strings.Split(csv, ",") {
			if unknown(n) {
				return true
			}
		}
		return false
	}
	return target == "" || target == "all" || unknown(target)
}

// variantDiscovery is how discoverVariants treats a declared variant the client
// config does not authorize: Approve approves it after printing what it will
// write (the flag is the consent, the output the record), Interactive asks,
// neither reports it needs_approval and goes on.
type variantDiscovery struct {
	Approve, Interactive bool
	KBs                  []string // the --kb selection; empty or "all" is every mounted KB
	Out, Err             io.Writer
}

// discoverVariants pulls the variant declarations of the KBs the server mounts
// (verified manifest), records the approvals the operator gave and caches and
// registers the authorized ones through reconcileVariants, so the detection
// that follows sees them (D1702). A server that does not answer leaves the
// registry as registerCachedVariants built it: the cache only. It never
// retires: a connect proves no absence.
func discoverVariants(base *clientconfig.Config, dir string, o variantDiscovery) {
	fc := *base
	facts, err := enumerateKBs(fc.ServerURL, fc.Auth, fc.Token, fc.InsecureHTTPAllowed())
	if err != nil {
		return
	}
	fc.KnownKBs = nil
	if facts.Listed {
		fc.KnownKBs = facts.Names
	}
	if len(o.KBs) > 0 && !(len(o.KBs) == 1 && o.KBs[0] == "all") {
		fc.KnownKBs = o.KBs
	}
	m, err := fetchMergedManifest(&fc)
	if err != nil {
		return
	}
	cfg, err := clientconfig.Load(dir)
	if err != nil {
		return
	}
	opts := provisioning.ApplyOptions{ApprovedMCP: cfg.ApprovedMCPHashes(), ApprovedVariantPaths: cfg.ApprovedPathFingerprints()}
	var arts, approve []provisioning.Artifact
	pulled := map[string]bool{}
	for _, a := range m.Artifacts {
		if a.Kind != "variant" {
			continue
		}
		arts = append(arts, a)
		pulled[strings.TrimPrefix(a.Source, "kb:")] = true
		if provisioning.ArtifactAuthorized(a, opts) || (!o.Approve && !o.Interactive) {
			continue
		}
		if err := printApprovalPreview(o.Out, a); err != nil {
			fmt.Fprintf(o.Err, "warning: client variant %s: %v\n", escapeControl(a.Name), err)
			continue
		}
		if o.Approve || promptYesNo("Approve? [y/N] ") {
			approve = append(approve, a)
		}
	}
	if len(approve) > 0 {
		now := time.Now()
		if err := withClientConfig(dir, func(c *clientconfig.Config) error {
			for _, a := range approve {
				if err := c.ApproveVariant(strings.TrimPrefix(a.Source, "kb:"), provisioning.ApprovalName(a), a.ContentHash, provisioning.VariantPathFingerprint(a, provisioning.ApplyOptions{}), now); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			fmt.Fprintln(o.Err, "warning: client variants not approved:", err)
			return
		}
	}
	if len(arts) == 0 {
		return
	}
	release, err := lockClient(dir)
	if err != nil {
		fmt.Fprintln(o.Err, "warning: client variants not cached:", err)
		return
	}
	defer release()
	if cfg, err = clientconfig.Load(dir); err != nil {
		return
	}
	res := reconcileVariants(dir, cfg, arts, nil, pulled, false, false)
	for _, w := range res.Warnings {
		fmt.Fprintf(o.Err, "warning: %s\n", w)
	}
	printVariantSync(o.Out, res, false)
}

// connectNewVariants connects, one doConnectLocked each, the variants a sync
// just cached: the KB asked for them and the operator approved them. A failure
// is a warning with the command that finishes it; the sync goes on.
func connectNewVariants(out, errw io.Writer, cfg *clientconfig.Config, dir string, names []string) {
	for _, n := range names {
		_, err := doConnectLocked(connectOptions{Providers: []string{n}, Dir: dir, ServerURL: cfg.ServerURL, Name: cfg.ServerName, Auth: cfg.Auth, Token: cfg.Token, AllowInsecureHTTP: cfg.AllowInsecureHTTP})
		if err != nil {
			fmt.Fprintf(errw, "warning: client variant %s declared by the KB was not connected: %v (run `cartographer connect %s`)\n", escapeControl(n), err, escapeControl(n))
			continue
		}
		fmt.Fprintf(out, "client variant %s: connected\n", escapeControl(n))
	}
}

// agentLabel is how listings name an agent: a variant says what it forks.
func agentLabel(a agents.Agent) string {
	if a.Variant {
		return fmt.Sprintf("%s (variant of %s)", a.Provider, a.Base)
	}
	return string(a.Provider)
}

// variantsNeedingApproval names the cached variants that are not registered
// because the client config does not authorize them (as they are, here).
func variantsNeedingApproval(dir string) []string {
	cache, err := clientvariant.LoadCache(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, n := range cache.Names() {
		if _, ok := configurator.VariantOf(configurator.Provider(n)); !ok {
			out = append(out, n)
		}
	}
	return out
}

// checkClientVariants reports a cached variant that fails registration with its
// reason (doctor), and one waiting for approval with the command.
func checkClientVariants(dir string, cfg *clientconfig.Config) []doctorFinding {
	cache, err := clientvariant.LoadCache(dir)
	if err != nil {
		return []doctorFinding{{Check: "client_variant_cache", Severity: doctorWarning, Message: "client variant cache unreadable: " + err.Error()}}
	}
	if len(cache) == 0 {
		return nil
	}
	reg := provisioning.RegisterCachedVariants(cache, provisioning.ApplyOptions{ApprovedMCP: cfg.ApprovedMCPHashes(), ApprovedVariantPaths: cfg.ApprovedPathFingerprints()})
	var out []doctorFinding
	for _, w := range reg.Warnings {
		out = append(out, doctorFinding{Check: "client_variant", Severity: doctorWarning, Message: w})
	}
	for _, n := range reg.Pending {
		out = append(out, doctorFinding{Check: "client_variant", Severity: doctorWarning, Message: fmt.Sprintf("client variant %s is cached but not authorized here", n), Fix: "cartographer approve variant " + n + " --kb " + cache[n].SourceKB})
	}
	return out
}
