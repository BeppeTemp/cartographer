package main

import (
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
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
// KBs' variants), a name no registry entry carries, or --approve-variants.
func wantsVariantDiscovery(target, csv string, approve bool) bool {
	if approve {
		return true // the flag asks for the KBs' variants whatever the target
	}
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
	KBs                  []string // the --kb selection: what is pulled, approved and connected; empty or "all" is every mounted KB
	Out, Err             io.Writer
}

// variantKBs is the KBs a discovered variant is connected with (D1702): the KB
// that declared it, narrowed by the operator's --kb selection when there is
// one, never every mounted KB. nil means the selection leaves it out.
func variantKBs(source string, selection []string) []string {
	if source == "" {
		return nil
	}
	if len(selection) == 0 || slices.Contains(selection, kbSelectionAll) || slices.Contains(selection, source) {
		return []string{source}
	}
	return nil
}

// doConnectScoped is doConnect with each registered client variant connected on
// its own, bound to variantKBs of its source KB (D1702): one shared --kb would
// bind it, and its token, to KBs that never asked for it.
func doConnectScoped(opts connectOptions) (connectResult, error) {
	cache, _ := clientvariant.LoadCache(opts.Dir)
	var plain []string
	var variants []string
	for _, p := range opts.Providers {
		if _, ok := configurator.VariantOf(configurator.Provider(p)); ok {
			variants = append(variants, p)
		} else {
			plain = append(plain, p)
		}
	}
	if len(variants) == 0 {
		return doConnect(opts)
	}
	var total connectResult
	ran := 0
	run := func(o connectOptions) error {
		ran++
		res, err := doConnect(o)
		total.Providers = append(total.Providers, res.Providers...)
		total.ConfigsWritten = append(total.ConfigsWritten, res.ConfigsWritten...)
		total.MCPEntries = append(total.MCPEntries, res.MCPEntries...)
		total.Warnings = append(total.Warnings, res.Warnings...)
		if total.Applied == nil {
			total.Applied = map[string]provisioning.AppliedResult{}
		}
		maps.Copy(total.Applied, res.Applied)
		if res.Deferred {
			total.Deferred, total.DeferredErr = true, res.DeferredErr
		}
		return err
	}
	if len(plain) > 0 {
		o := opts
		o.Providers = plain
		if err := run(o); err != nil {
			return total, err
		}
	}
	for _, v := range variants {
		kbs := variantKBs(cache[v].SourceKB, opts.KBs)
		if kbs == nil {
			fmt.Fprintf(os.Stderr, "client variant %s: declared by KB %s, which is not among the selected KBs: not connected\n", escapeControl(v), escapeControl(cache[v].SourceKB))
			continue
		}
		o := opts
		o.Providers, o.KBs = []string{v}, kbs
		if err := run(o); err != nil {
			return total, err
		}
	}
	if ran == 0 {
		var decl []string
		for _, v := range variants {
			decl = append(decl, fmt.Sprintf("%s (declared by KB %s)", escapeControl(v), escapeControl(cache[v].SourceKB)))
		}
		return total, fmt.Errorf("no client variant is connected: the selected KBs (%s) do not include the KB of %s", strings.Join(opts.KBs, ", "), strings.Join(decl, ", "))
	}
	return total, nil
}

// mergeDiscovered adds the variants discovery found to an explicit provider
// list (connect --agents with --approve-variants), keeping its order.
func mergeDiscovered(providers, discovered []string) []string {
	for _, v := range discovered {
		if !slices.Contains(providers, v) {
			providers = append(providers, v)
		}
	}
	return providers
}

// detectedVariants are the registered variants whose client is installed here
// and whose declaring KB the selection covers: what a connect may add (D1702).
func detectedVariants(dir string, selection []string) []string {
	cache, _ := clientvariant.LoadCache(dir)
	var found []string
	for _, a := range agents.Detect() {
		if a.Variant && a.Installed && variantKBs(cache[string(a.Provider)].SourceKB, selection) != nil {
			found = append(found, string(a.Provider))
		}
	}
	return found
}

// discoverVariants pulls the variant declarations of the KBs the server mounts
// (verified manifest), records the approvals the operator gave and caches and
// registers the authorized ones through reconcileVariants, so the detection
// that follows sees them (D1698). A server that does not answer leaves the
// registry as registerCachedVariants built it: the cache only. It never
// retires: a connect proves no absence. It returns the cached variants whose
// client is installed here and whose KB the selection covers, for the caller to
// connect (D1702); every failure to look is printed, never swallowed.
func discoverVariants(base *clientconfig.Config, dir string, o variantDiscovery) []string {
	skip := func(why error) []string {
		fmt.Fprintf(o.Err, "note: client variants of the KBs not discovered, the cached ones stay: %s\n", escapeControl(why.Error()))
		return nil
	}
	fc := *base
	facts, err := enumerateKBs(fc.ServerURL, fc.Auth, fc.Token, fc.InsecureHTTPAllowed())
	if err != nil {
		return skip(err)
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
		return skip(err)
	}
	cfg, err := clientconfig.Load(dir)
	if err != nil {
		return skip(err)
	}
	opts := provisioning.ApplyOptions{ApprovedMCP: cfg.ApprovedMCPHashes(), ApprovedVariantPaths: cfg.ApprovedPathFingerprints()}
	var arts, approve []provisioning.Artifact
	pulled := map[string]bool{}
	optedOut := map[string]bool{}
	for _, a := range m.Artifacts {
		if a.Kind != "variant" {
			continue
		}
		kb := strings.TrimPrefix(a.Source, "kb:")
		if !cfg.KBArtifacts(kb) {
			// kb_options artifacts:false: sync installs nothing from this KB, and
			// neither may discovery preview, approve or cache its variants.
			if !optedOut[kb] {
				optedOut[kb] = true
				fmt.Fprintf(o.Out, "note: KB %s has artifacts turned off (kb_options): its client variants are left out\n", escapeControl(kb))
			}
			continue
		}
		arts = append(arts, a)
		pulled[kb] = true
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
			return nil
		}
	}
	if len(arts) == 0 {
		fmt.Fprintln(o.Out, "no client variant is declared by the selected KBs")
		return detectedVariants(dir, o.KBs) // the ones cached and approved earlier
	}
	release, err := lockClient(dir)
	if err != nil {
		fmt.Fprintln(o.Err, "warning: client variants not cached:", err)
		return nil
	}
	defer release()
	if cfg, err = clientconfig.Load(dir); err != nil {
		return skip(err)
	}
	res := reconcileVariants(dir, cfg, arts, nil, pulled, false, false)
	for _, w := range res.Warnings {
		fmt.Fprintf(o.Err, "warning: %s\n", escapeControl(w))
	}
	printVariantSync(o.Out, res, false)
	return detectedVariants(dir, o.KBs)
}

// connectNewVariants connects, one doConnectLocked each, the variants a sync
// just cached: the KB asked for them and the operator approved them. A failure
// is a warning with the command that finishes it; the sync goes on.
func connectNewVariants(out, errw io.Writer, cfg *clientconfig.Config, dir string, names []string) {
	for _, n := range names {
		// Bound to the KB that declared it and to no other (D1702).
		cache, _ := clientvariant.LoadCache(dir)
		kb := cache[n].SourceKB
		_, err := doConnectLocked(connectOptions{Providers: []string{n}, Dir: dir, ServerURL: cfg.ServerURL, Name: cfg.ServerName, Auth: cfg.Auth, Token: cfg.Token, AllowInsecureHTTP: cfg.AllowInsecureHTTP, KBs: variantKBs(kb, nil)})
		if err != nil {
			fmt.Fprintf(errw, "warning: client variant %s declared by the KB was not connected: %v (run `cartographer connect %s --kb %s`)\n", escapeControl(n), err, escapeControl(n), escapeControl(kb))
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
		out = append(out, doctorFinding{Check: "client_variant", Severity: doctorWarning, Message: escapeControl(w)})
	}
	for _, n := range reg.Pending {
		out = append(out, doctorFinding{Check: "client_variant", Severity: doctorWarning, Message: fmt.Sprintf("client variant %s is cached but not authorized here", escapeControl(n)), Fix: "cartographer approve variant " + escapeControl(n) + " --kb " + escapeControl(cache[n].SourceKB)})
	}
	return out
}
