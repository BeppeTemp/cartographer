package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/BeppeTemp/cartographer/internal/blocktext"
	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/config"
	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/gitx"
	"github.com/BeppeTemp/cartographer/internal/mcpserver"
	"github.com/BeppeTemp/cartographer/internal/safewrite"
)

// mcpEntry is one Cartographer-owned MCP entry emitted for a provider.
type mcpEntry struct {
	Name string
	URL  string
	// KBName is the KB this entry selects, empty for the single-KB entry that
	// reaches the server's default endpoint. Carried so a client-side check can
	// pair an entry with the KB facts /health reports for it (D152).
	KBName string
	// Bridge marks the entry as the local stdio bridge (D737): no URL, the
	// command is `cartographer mcp --provider <p>`.
	Bridge bool
	// Reach is every KB the entry writes to today, and Open marks one that
	// also reaches the KBs the server mounts later (no ?kbs= on a routed
	// server, or a server that names none yet). For a bridge, the HTTP entry
	// it falls back to. The author the entry names must hold for all (D904).
	Reach []string
	Open  bool
	// Author is the writer identity the entry sends as Cartographer-Author,
	// "" for none; set by setEntryAuthors.
	Author string
}

// kbTarget is one resolved remote-KB target for a top-level administrative
// client operation (fetchMergedManifest, cmdReindex): Name is the KB name to
// select via WithKB (empty selects the server's default single-KB endpoint,
// see MultiKBServer.Handler), ToolPrefix is that KB's currently effective
// tool-name prefix (D102), as advertised by /health (mcpserver.KBInfo.ToolPrefix,
// D120) — never re-derived client-side from config.ResolveToolPrefix, which
// would require reading the server's own YAML. Every direct call against a
// target must go through qualifyTool/callTool so a discovery mismatch is
// impossible.
type kbTarget struct {
	Name       string
	ToolPrefix string
}

// qualifyTool returns the tool name to call for a target's advertised
// prefix: base unchanged for an empty prefix, "<prefix>__<base>" otherwise —
// the client-side mirror of Server.RegisterTool's own renaming (D102).
func qualifyTool(prefix, base string) string {
	if prefix == "" {
		return base
	}
	return prefix + "__" + base
}

// callTool issues one direct administrative tools/call against target using
// c (already scoped to target.Name via WithKB), qualifying base with
// target.ToolPrefix. This is the one seam every Cartographer-owned direct
// tool call must go through (D120) — see TestNoUnqualifiedProductionToolCalls.
func callTool(c *client.MCPClient, target kbTarget, base string, args any) (json.RawMessage, error) {
	return c.Call(qualifyTool(target.ToolPrefix, base), args)
}

// resolveKBTargets resolves the KB targets for one top-level client
// operation from a live /health snapshot (D120): the KB names it selects are
// always the ones the caller already asked for (selectedNames — typically
// cfg.KBs, or an operator's explicit --kb override), qualified with each
// KB's currently advertised tool-name prefix rather than a client-side
// re-derivation. Rules:
//
//   - selectedNames non-empty: every name must appear in health.KBs (when
//     health carries KB metadata at all) — a persisted/explicit name absent
//     from current health metadata is a stale-selection error naming the KB;
//     a legacy health response with no kbs field at all preserves the
//     pre-D120 unprefixed behaviour verbatim (no way to check or qualify);
//   - selectedNames empty and health advertises exactly one KB: the bare
//     endpoint, qualified with that KB's advertised prefix;
//   - selectedNames empty and health advertises zero or 2+ KBs, or omits KB
//     metadata entirely: the bare endpoint, unqualified — the server's own
//     "kb parameter required"/"unknown kb" response is the explicit-selection
//     error that surfaces, exactly as it did before D120; this function never
//     guesses a KB.
//
// A non-empty advertised prefix is validated (config.ValidateToolPrefixShape)
// before use: malformed server metadata is a protocol error, never silently
// re-sanitized or retried.
func resolveKBTargets(health *client.Health, selectedNames []string) ([]kbTarget, error) {
	haveMetadata := health != nil && health.KBs != nil
	prefixByName := make(map[string]string, len(selectedNames))
	if haveMetadata {
		for _, kb := range *health.KBs {
			if kb.ToolPrefix != "" {
				if err := config.ValidateToolPrefixShape(kb.ToolPrefix); err != nil {
					return nil, fmt.Errorf("server advertised an invalid tool_prefix %q for KB %q: %w", kb.ToolPrefix, kb.Name, err)
				}
			}
			prefixByName[kb.Name] = kb.ToolPrefix
		}
	}

	if len(selectedNames) > 0 {
		targets := make([]kbTarget, 0, len(selectedNames))
		for _, name := range selectedNames {
			if !haveMetadata {
				targets = append(targets, kbTarget{Name: name})
				continue
			}
			prefix, ok := prefixByName[name]
			if !ok {
				return nil, fmt.Errorf("configured KB %q is not among the KBs currently advertised by the server (stale selection: check .cartographer.yaml or the server config; the server lists only the KBs the token may read)", name)
			}
			targets = append(targets, kbTarget{Name: name, ToolPrefix: prefix})
		}
		return targets, nil
	}

	if haveMetadata && len(*health.KBs) == 1 {
		return []kbTarget{{ToolPrefix: (*health.KBs)[0].ToolPrefix}}, nil
	}
	return []kbTarget{{}}, nil
}

// serverFacts is what a single /health request tells the client: the mounted
// KB names, whether the server listed them at all, and the server's own
// version. One request answers both questions — the version is recorded in the
// lockfile (D142) and a second round trip on every sync would buy nothing.
type serverFacts struct {
	// Names are the mounted KB names; Listed is false when a healthy
	// single-KB (or pre-multi-KB) server omits kbs, in which case callers
	// retain the old bare-entry behaviour.
	Names   []string
	Listed  bool
	Version string
	// KBs carries the full per-KB health payload, including each KB's effective
	// tool prefix and capability map (D151): reading a value /health already
	// serves beats re-deriving it client-side.
	KBs []client.HealthKB
	// RoutedPath is the routed mount's path when the server serves one (D187),
	// empty otherwise. A client that sees it writes ONE MCP entry pointing at
	// that endpoint instead of one per KB.
	RoutedPath string
}

// enumerateKBs obtains the mounted KB names and the server version from
// /health.
func enumerateKBs(serverURL string, auth bool, tokenSpec string, allowInsecure bool) (serverFacts, error) {
	token, name := "", ""
	if c := (&clientconfig.Config{Auth: auth, Token: tokenSpec}); auth && strings.TrimSpace(tokenSpec) != "" {
		token = resolveToken(c)
		name = tokenEnvName(c)
	}
	health, err := client.New(serverURL, token).WithInsecureHTTP(allowInsecure).WithTokenEnv(name).Health(probeTimeout)
	if err != nil {
		return serverFacts{}, err
	}
	facts := serverFacts{Version: health.Version}
	if health.Routed() {
		facts.RoutedPath = health.RoutedPath
	}
	if health.KBs == nil {
		return facts, nil
	}
	facts.Listed = true
	facts.Names = make([]string, 0, len(*health.KBs))
	for _, kb := range *health.KBs {
		if kb.Name == "" {
			return facts, fmt.Errorf("health response contains a KB without a name")
		}
		facts.Names = append(facts.Names, kb.Name)
		facts.KBs = append(facts.KBs, kb)
	}
	return facts, nil
}

// effectiveToolPrefixes turns the /health facts a caller already fetched into a
// KB name -> effective prefix map (D120). Returns nil when the server was
// unreachable or advertises no KB list, so a caller can tell "verified" from
// "unknown" instead of assuming (D152). Pure: no extra round trip.
func effectiveToolPrefixes(facts serverFacts, healthErr error) map[string]string {
	if healthErr != nil || !facts.Listed {
		return nil
	}
	out := make(map[string]string, len(facts.KBs))
	for _, kb := range facts.KBs {
		out[kb.Name] = kb.ToolPrefix
	}
	return out
}

// kiroFlatNamespaceWarning returns a non-empty warning when a provider with a
// flat MCP tool namespace would receive two or more KBs that register their tools
// **unprefixed**. Kiro's namespace is flat across servers — unlike Claude Code,
// Codex and OpenCode, which namespace per server (verified empirically, see the
// D102 issue thread) — so unprefixed KBs collide there and only one stays
// reachable, silently.
//
// It adopts the server-side predicate (>= 2 unprefixed) and reads the effective
// prefixes from GET /health, which advertises them per KB since D120. The old
// comment here recorded that the client "cannot tell whether the server mounted
// those KBs with prefixes" and that plumbing them through would cost "one stderr
// line of false positive": the reasoning was sound but the premise no longer
// holds, and the unconditional version fired on every sync of a deployment where
// nothing was wrong — sending a maintenance session after a non-problem, on the
// warning an operator actually reads (D152).
//
// prefixes maps KB name -> effective prefix. When it is nil — an unreachable
// server, or one too old to advertise them — the warning stays unconditional and
// says the prefixes could not be verified: a missing signal is not evidence that
// everything is fine.
func kiroFlatNamespaceWarning(providers []string, entriesByProvider map[string][]mcpEntry, prefixes map[string]string, prefixErr error) string {
	var flat *configurator.Descriptor
	for _, p := range providers {
		if d, ok := configurator.Lookup(configurator.Provider(p)); ok && d.FlatToolNamespace {
			flat = &d
			break
		}
	}
	if flat == nil {
		return ""
	}
	// Evaluated on that provider's own entries (D170): binding it to a single
	// KB removes the flat-namespace conflict, and the warning must stop firing
	// rather than describe entries it no longer holds.
	entries := entriesByProvider[string(flat.Provider)]
	if len(entries) < 2 {
		return ""
	}
	remedies := "set kbs[].tool_prefix on them or mcp.tool_prefix_mode: kb-name (see docs/deployment.md §MCP tool names)"

	if prefixes == nil {
		reason := "the server did not advertise them"
		if prefixErr != nil {
			reason = prefixErr.Error()
		}
		return fmt.Sprintf("%s has a flat MCP tool namespace across servers: writing %d MCP entries "+
			"means only one KB's tools will be reachable in a %s session unless the server mounts the "+
			"others with a tool_prefix — could not read the server's effective prefixes (%s), so "+
			"%s", flat.Provider, len(entries), flat.DisplayName, reason, remedies)
	}

	var unprefixed []string
	for _, e := range entries {
		if prefixes[e.KBName] == "" {
			unprefixed = append(unprefixed, e.KBName)
		}
	}
	if len(unprefixed) < 2 {
		return ""
	}
	sort.Strings(unprefixed)
	return fmt.Sprintf("%s has a flat MCP tool namespace across servers and KBs %s register identical tool "+
		"names: only one of them stays reachable in a %s session, answering from the wrong KB — %s",
		flat.Provider, strings.Join(quoteAll(unprefixed), ", "), flat.DisplayName, remedies)
}

// toolIdentifierBudgetWarning returns a non-empty warning when a provider
// with a tool identifier limit (Descriptor.ToolIdentifierLimit: Antigravity
// shows each tool as "mcp_<server>_<tool>" and drops any over 64 characters)
// would receive an MCP entry whose longest tool identifier exceeds it (D201).
// Nothing fails on the wire: the tools are simply missing from the session.
//
// The longest identifier is computed, not listed: the entry name, the KB's
// effective tool_prefix as /health advertises it (D120), and the longest
// registered tool name (mcpserver.MaxBareToolNameLen). A routed entry carries
// unprefixed tools (D187); a bare entry on a one-KB server reaches that KB, so
// it carries that KB's prefix. Without /health facts the prefixes are unknown
// and the warning stays silent: it is evidence-only, like the rest of D152.
func toolIdentifierBudgetWarning(providers []string, entriesByProvider map[string][]mcpEntry, facts serverFacts, healthErr error) string {
	if healthErr != nil || !facts.Listed {
		return ""
	}
	prefixes := effectiveToolPrefixes(facts, healthErr)
	var parts []string
	for _, p := range providers {
		d, ok := configurator.Lookup(configurator.Provider(p))
		if !ok || d.ToolIdentifierLimit == 0 {
			continue
		}
		var over []string
		for _, e := range entriesByProvider[p] {
			prefix := prefixes[e.KBName]
			switch {
			case facts.RoutedPath != "":
				prefix = ""
			case e.KBName == "" && len(facts.KBs) == 1:
				prefix = facts.KBs[0].ToolPrefix
			}
			tool := mcpserver.MaxBareToolNameLen
			if prefix != "" {
				tool += len(prefix) + len("__")
			}
			id := len("mcp_") + len(e.Name) + len("_") + tool
			if id > d.ToolIdentifierLimit {
				over = append(over, fmt.Sprintf("%q (up to %d)", e.Name, id))
			}
		}
		if len(over) == 0 {
			continue
		}
		sort.Strings(over)
		parts = append(parts, fmt.Sprintf("%s drops MCP tools whose identifier (mcp_<server>_<tool>) exceeds %d "+
			"characters, and entries %s would exceed it: their longest tools will be missing from a %s session "+
			"— shorten the KB's tool_prefix (kbs[].tool_prefix) or set mcp.mount_mode: routed, whose single entry "+
			"carries unprefixed tools (see docs/deployment.md §MCP tool names)",
			d.Provider, d.ToolIdentifierLimit, strings.Join(over, ", "), d.DisplayName))
	}
	return strings.Join(parts, "; ")
}

// quoteAll quotes each name for a log line listing KBs.
func quoteAll(names []string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = strconv.Quote(n)
	}
	return out
}

// entriesForKBs implements D92's compatibility rule, split across two inputs
// since D170: the entry SHAPE comes from what the server mounts, the entry SET
// from what the provider is bound to.
//
// Conflating them breaks the client. A bare /mcp auto-routes only when the
// server mounts exactly one KB (mcpserver.MultiKBServer.Handler); with zero or
// two or more it answers "kb parameter required". So a server with four KBs and
// a client bound to one still needs an explicitly-scoped ?kb= entry, even
// though that client's list has a single name.
//
// url.URL is used rather than concatenation so an existing query survives.
func entriesForKBs(baseName, serverURL string, mounted, bound []string, routedPath string, explicit bool) ([]mcpEntry, error) {
	return entriesForKBsAccess(baseName, serverURL, mounted, bound, routedPath, explicit, "")
}

// entriesForKBsAccess is entriesForKBs for a provider with an access profile
// (D1184): on a routed server the URL carries access=<access> after kbs= and
// before provisioned=1. Single-KB entries have no such endpoint and ignore it
// (connect refuses that case before it gets here).
func entriesForKBsAccess(baseName, serverURL string, mounted, bound []string, routedPath string, explicit bool, access string) ([]mcpEntry, error) {
	if routedPath != "" {
		// D187/D288: a routed server exposes one endpoint for every KB, so one
		// entry named after the server is the whole configuration. An explicit
		// binding travels in the URL (?kbs=, sorted, comma-joined): the server
		// narrows the connection to it, and with a single KB drops the `kb`
		// argument from the schemas. The default binding ("every KB the server
		// mounts") writes no kbs at all, so a KB added later reaches the
		// provider without a rewrite. A name the server no longer mounts is
		// left out: the server refuses an unknown name in kbs with a 400, which
		// would break every call of the entry.
		u, err := url.Parse(serverURL)
		if err != nil {
			return nil, fmt.Errorf("parse server URL %q: %w", serverURL, err)
		}
		u.Path = routedPath
		u.RawQuery = ""
		reach, open := mounted, true
		if explicit {
			names := routedBinding(mounted, bound)
			if len(names) > 0 {
				escaped := make([]string, len(names))
				for i, n := range names {
					escaped[i] = url.QueryEscape(n)
				}
				u.RawQuery = "kbs=" + strings.Join(escaped, ",")
				reach, open = names, false
			}
		}
		if access != "" {
			if u.RawQuery != "" {
				u.RawQuery += "&"
			}
			u.RawQuery += "access=" + url.QueryEscape(access)
		}
		// D1097: the entry declares itself provisioned (this client writes the
		// managed instructions block), so the server sends it no operating
		// guide. Always sent: a server that predates it ignores the parameter.
		if u.RawQuery != "" {
			u.RawQuery += "&"
		}
		u.RawQuery += mcpserver.ProvisionedParam + "=" + mcpserver.ProvisionedValue
		return []mcpEntry{{Name: baseName, URL: u.String(), Reach: reach, Open: open}}, nil
	}
	if len(mounted) <= 1 {
		// Nothing to select against: the server routes the bare endpoint, and
		// no binding can name a KB the server does not identify.
		return []mcpEntry{{Name: baseName, URL: serverURL, Reach: mounted, Open: len(mounted) == 0}}, nil
	}
	u, err := url.Parse(serverURL)
	if err != nil {
		return nil, fmt.Errorf("parse server URL %q: %w", serverURL, err)
	}
	entries := make([]mcpEntry, 0, len(bound))
	for _, kb := range bound {
		entryURL := *u
		q := entryURL.Query()
		q.Set("kb", kb)
		entryURL.RawQuery = q.Encode()
		entries = append(entries, mcpEntry{Name: baseName + "-" + kb, URL: entryURL.String(), KBName: kb, Reach: []string{kb}})
	}
	return entries, nil
}

// routedBinding is the explicit binding of a provider as it goes into ?kbs=;
// the rule lives in clientconfig so the stdio bridge applies the same one (D737).
func routedBinding(mounted, bound []string) []string {
	return clientconfig.RoutedBinding(mounted, bound)
}

// managedEntryNames returns every name this client may have owned for the
// persisted KB set. Including the bare name supports 1→N migration; including
// all suffixed names supports N→1 and disappeared KBs without touching any
// unrelated MCP entry.
func managedEntryNames(baseName string, kbs []string) []string {
	names := []string{baseName}
	seen := map[string]bool{baseName: true}
	for _, kb := range kbs {
		name := baseName + "-" + kb
		if !seen[name] {
			names = append(names, name)
			seen[name] = true
		}
	}
	return names
}

// entryRefusal is the refusal of removeMCPEntries for one MCP entry, which
// remembers the entry's name so a caller can tell which KBs it was written for.
// Its text is the refusal's own.
type entryRefusal struct {
	Name string
	Err  error
}

func (e *entryRefusal) Error() string { return e.Err.Error() }
func (e *entryRefusal) Unwrap() error { return e.Err }

// refusedEntryKBs are the KBs, among kbs, whose per-KB entry removeMCPEntries
// refused to remove (err is its error). Those KBs must stay in known_kbs: the
// entry is only recognised as ours through them (managedEntryNames).
func refusedEntryKBs(err error, baseName string, kbs []string) []string {
	var names []string
	var walk func(error)
	walk = func(e error) {
		if j, ok := e.(interface{ Unwrap() []error }); ok {
			for _, inner := range j.Unwrap() {
				walk(inner)
			}
			return
		}
		var r *entryRefusal
		if errors.As(e, &r) {
			names = append(names, r.Name)
		}
	}
	walk(err)
	var out []string
	for _, kb := range kbs {
		if slices.Contains(names, baseName+"-"+kb) {
			out = append(out, kb)
		}
	}
	return out
}

// withRetainedKBs is current followed by the KBs of retained it does not hold:
// the persisted list of a run that could not remove every stale per-KB entry.
func withRetainedKBs(current, retained []string) []string {
	out := append([]string(nil), current...)
	for _, kb := range retained {
		if !slices.Contains(out, kb) {
			out = append(out, kb)
		}
	}
	return out
}

// entriesByProviderForKBs builds each provider's own entry set from its binding
// (D170). A provider explicitly bound to NO KBs gets no entry at all — that is
// the one case where an empty list is a declaration rather than an absence.
func entriesByProviderForKBs(cfg *clientconfig.Config, providers []string, baseName, serverURL string, mounted []string, routedPath string) (map[string][]mcpEntry, error) {
	out := make(map[string][]mcpEntry, len(providers))
	for _, p := range providers {
		if cfg.ProfileIgnoresWorkspaces(p, routedPath) {
			fmt.Fprintf(os.Stderr, "warning: %s has the read-only profile (clients.%s.access), so its workspace bindings are ignored and it keeps the HTTP entry; remove the key to use them\n", p, p)
		}
		if cfg.UsesBridge(p, routedPath) {
			// D737: a provider with a workspace binding gets the bridge, which
			// resolves the session's folder to a KB set itself. It replaces the
			// HTTP entry whatever the provider's global binding says: the global
			// binding is only the bridge's answer for an unbound folder.
			//
			// URL is the entry the provider had before: where the bridge cannot
			// be started (no `cartographer` on PATH) it is written instead of
			// failing the whole run. It is empty when the provider's own binding
			// said "no KB" or named only unmounted KBs: no entry is better than
			// one that widens.
			e := mcpEntry{Name: baseName, Bridge: true}
			bound, explicit := cfg.BoundKBs(p)
			if !explicit {
				bound = mounted
			}
			if !explicit || len(routedBinding(mounted, bound)) > 0 || len(mounted) == 0 {
				fallback, err := entriesForKBsAccess(baseName, serverURL, mounted, bound, routedPath, explicit, "") // UsesBridge is false when access is set (D1184)
				if err != nil {
					return nil, err
				}
				if len(fallback) == 1 {
					e.URL, e.Reach, e.Open = fallback[0].URL, fallback[0].Reach, fallback[0].Open
				}
			}
			out[p] = []mcpEntry{e}
			continue
		}
		bound, explicit := cfg.BoundKBs(p)
		if !explicit {
			// The default is "every KB the server currently mounts", so it
			// resolves against the live discovery rather than the persisted
			// cache: connect runs before that cache is refreshed, and a dry run
			// never refreshes it at all.
			bound = mounted
		}
		if explicit && len(bound) == 0 && len(mounted) > 1 {
			out[p] = nil
			continue
		}
		if explicit && routedPath != "" && len(mounted) > 0 && len(routedBinding(mounted, bound)) == 0 {
			// Bound only to KBs the server no longer mounts: nothing to point
			// the provider at, the same declaration as an empty binding.
			out[p] = nil
			continue
		}
		entries, err := entriesForKBsAccess(baseName, serverURL, mounted, bound, routedPath, explicit, cfg.Clients[p].Access)
		if err != nil {
			return nil, err
		}
		out[p] = entries
	}
	return out, nil
}

// globalIdentFn reads the global git identity for setEntryAuthors; tests stub
// it so no run depends on the developer's git config.
var globalIdentFn = gitx.GlobalIdent

// setEntryAuthors gives each entry the writer identity it sends as
// Cartographer-Author (D904). An entry is written once into a file every
// folder shares, so its author comes from what no folder changes: each reached
// KB's identities entry, else the global git identity, asked at most once. It
// names one only when every KB it reaches agrees, so a value chosen for one KB
// never authors another KB's commits; an open entry also reaches every KB
// identities names and any other. A bridge entry names none while the bridge
// can start, since the bridge names the writer per call; it gets one only for
// the HTTP entry it falls back to. Returns why entries name none where a writer
// was at hand.
func setEntryAuthors(cfg *clientconfig.Config, byProvider map[string][]mcpEntry, global func() (string, bool)) []string {
	var asked, perFolder, fellToGit bool
	var gitIdent string
	bridgeStarts := sync.OnceValue(func() bool {
		_, err := bridgeCommand()
		return err == nil
	})
	authorOf := func(kb string) string {
		if v, found, _ := cfg.Identity(kb); found {
			return v
		}
		if !asked {
			gitIdent, perFolder = global()
			asked = true
		}
		fellToGit = true
		return gitIdent
	}
	var named []string
	for kb := range cfg.Identities {
		if kb != clientconfig.IdentityDefault {
			named = append(named, kb)
		}
	}
	sort.Strings(named)

	var warnings []string
	seen := map[string]bool{}
	providers := make([]string, 0, len(byProvider))
	for p := range byProvider {
		providers = append(providers, p)
	}
	sort.Strings(providers)
	for _, p := range providers {
		entries := byProvider[p]
		for i := range entries {
			e := &entries[i]
			e.Author = ""
			if e.Bridge && bridgeStarts() {
				continue
			}
			kbs := e.Reach
			if e.Open {
				kbs = append(append(append([]string{}, kbs...), named...), "")
			}
			authors := map[string]bool{}
			for _, kb := range kbs {
				authors[authorOf(kb)] = true
			}
			if len(authors) == 1 {
				for a := range authors {
					e.Author = a
				}
				continue
			}
			if len(authors) > 1 {
				var listed []string
				for _, kb := range kbs {
					if kb != "" && !slices.Contains(listed, kb) {
						listed = append(listed, kb)
					}
				}
				sort.Strings(listed)
				if e.Open {
					listed = append(listed, "any other KB")
				}
				w := fmt.Sprintf("MCP entry %q reaches KBs with different writers (%s), so it names none and their writes are authored by the token: bind a workspace so the bridge names the writer per call, or bind the client to KBs that share one", e.Name, strings.Join(listed, ", "))
				if !seen[w] {
					seen[w] = true
					warnings = append(warnings, w)
				}
			}
		}
	}
	if fellToGit && perFolder {
		warnings = append(warnings, fmt.Sprintf("the global git config picks the identity by folder (includeIf), which an MCP entry written once cannot follow, so writes through it are authored by the token: set identities.default in %s (empty to keep sending none), or bind a workspace so the bridge reads each folder's identity", clientconfig.FileName))
	}
	return warnings
}

// allEntryNames is the union of every provider's entry names, deduplicated and
// sorted — what the summary lines report when they are not per provider.
func allEntryNames(entriesByProvider map[string][]mcpEntry) []string {
	seen := map[string]bool{}
	var names []string
	for _, entries := range entriesByProvider {
		for _, e := range entries {
			if !seen[e.Name] {
				seen[e.Name] = true
				names = append(names, e.Name)
			}
		}
	}
	sort.Strings(names)
	return names
}

func entryNames(entries []mcpEntry) []string {
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name)
	}
	return names
}

// refusalWarnings renders the symlink refusals of configurator.Apply/Remove
// (possibly several, joined) as warnings. The text is the refusal's own, which
// names the path, so the same refusal met by the removal pass and by the write
// pass reads the same and appendUnique keeps one line.
func refusalWarnings(err error) []string {
	var out []string
	var walk func(error)
	walk = func(e error) {
		if j, ok := e.(interface{ Unwrap() []error }); ok {
			for _, inner := range j.Unwrap() {
				walk(inner)
			}
			return
		}
		out = append(out, "mcp config left unchanged: "+e.Error())
	}
	walk(err)
	return out
}

// appendUnique appends each of extra that list does not hold yet.
func appendUnique(list []string, extra ...string) []string {
	for _, e := range extra {
		if !slices.Contains(list, e) {
			list = append(list, e)
		}
	}
	return list
}

// applyMCPEntries emits each provider's OWN entries (D170: a provider must not
// hold an entry toward a KB it is not bound to). Codex represents all
// Cartographer MCP entries in one marker-delimited TOML block, so its emitted
// bodies are joined before Apply replaces that block. Returns the config paths
// written plus the non-fatal warnings Apply collected (a header the provider
// cannot represent, D69; a duplicate table adopted from a Codex rewrite, D99),
// for the caller to render.
// emitMCPEntry generates the configurator.EmitResult for one MCP entry and provider.
func emitMCPEntry(provider string, entry mcpEntry, auth bool, tokenEnv string) (*configurator.EmitResult, string, error) {
	if entry.Bridge {
		spec, err := bridgeServerSpec(provider, auth, tokenEnv)
		if err == nil {
			r, err := configurator.EmitServer(entry.Name, spec, configurator.Provider(provider))
			return r, "", err
		}
		w := fmt.Sprintf("%s: %v; its workspace bindings do not narrow the tools until `cartographer` is on PATH and `cartographer sync` runs again", provider, err)
		if entry.URL == "" {
			return nil, w, nil
		}
		r, err := configurator.Emit(&configurator.ServerConfig{Name: entry.Name, URL: entry.URL, AuthEnabled: auth, TokenEnv: tokenEnv, Author: entry.Author}, configurator.Provider(provider))
		return r, w, err
	}
	r, err := configurator.Emit(&configurator.ServerConfig{Name: entry.Name, URL: entry.URL, AuthEnabled: auth, TokenEnv: tokenEnv, Author: entry.Author}, configurator.Provider(provider))
	return r, "", err
}

// snapshotMCPConfigFiles reads and records the raw bytes of every configuration
// file the targeted providers manage, before any removal or rewrite.
func snapshotMCPConfigFiles(targets []string, dir string, claudeDirs []string) map[string][]byte {
	snap := make(map[string][]byte)
	for _, p := range targets {
		if d, ok := configurator.Lookup(configurator.Provider(p)); ok && d.MCPConfigPath != "" {
			for _, base := range providerBaseDirs(p, dir, claudeDirs) {
				fp := filepath.Join(base, d.ConfigPath())
				if _, seen := snap[fp]; !seen {
					data, _ := os.ReadFile(fp)
					snap[fp] = data
				}
			}
		}
	}
	return snap
}

// changedMCPEntries returns the names of the MCP entries that actually were
// written (or under dry-run, would be written) because the provider's config
// file either did not contain them, had them with different content, or was
// created/modified. Entries already present with byte-identical content in every
// destination file of the provider are not reported, so an unchanged sync is
// quiet (D147).
func changedMCPEntries(targets []string, entriesByProvider map[string][]mcpEntry, beforeSnap map[string][]byte, dir string, claudeDirs []string, auth bool, tokenEnv string) []string {
	var changed []string
	seen := map[string]bool{}
	for _, p := range targets {
		d, ok := configurator.Lookup(configurator.Provider(p))
		if !ok || d.MCPConfigPath == "" {
			continue
		}
		entries := entriesByProvider[p]
		for _, e := range entries {
			for _, base := range providerBaseDirs(p, dir, claudeDirs) {
				fp := filepath.Join(base, d.ConfigPath())
				if !isEntryUnchangedIn(p, d, entries, e, beforeSnap[fp], auth, tokenEnv) && !seen[e.Name] {
					seen[e.Name] = true
					changed = append(changed, e.Name)
					break
				}
			}
		}
	}
	sort.Strings(changed)
	return changed
}

func isEntryUnchangedIn(provider string, d configurator.Descriptor, entries []mcpEntry, e mcpEntry, data []byte, auth bool, tokenEnv string) bool {
	if len(data) == 0 {
		return false
	}
	if d.MCPFormat == configurator.FormatTOMLBlock {
		var parts []string
		for _, ent := range entries {
			if r, _, err := emitMCPEntry(provider, ent, auth, tokenEnv); err == nil && r != nil {
				parts = append(parts, string(r.Content))
			}
		}
		block := configurator.CodexMCPBlockBegin + "\n" + strings.TrimRight(strings.Join(parts, "\n"), "\n") + "\n" + configurator.CodexMCPBlockEnd + "\n"
		rep, ok := blocktext.ReplaceBetween(string(data), configurator.CodexMCPBlockBegin, configurator.CodexMCPBlockEnd, block)
		return ok && rep == string(data)
	}
	r, _, err := emitMCPEntry(provider, e, auth, tokenEnv)
	if err != nil || r == nil {
		return false
	}
	var existing, emit map[string]any
	if json.Unmarshal(data, &existing) != nil || json.Unmarshal(r.Content, &emit) != nil {
		return false
	}
	return reflect.DeepEqual(extractMCPServer(existing, e.Name), extractMCPServer(emit, e.Name))
}

func extractMCPServer(m map[string]any, name string) any {
	for _, key := range []string{"mcpServers", "mcp"} {
		if servers, ok := m[key].(map[string]any); ok {
			if v, found := servers[name]; found {
				return v
			}
		}
	}
	return nil
}

// applyMCPEntries applies every provider's MCP entries to its configuration
// file. Providers with a JSON config (claude, opencode, kiro) non-destructively
// merge their entry via configurator.Apply; codex puts all Cartographer MCP
// entries in one marker-delimited TOML block, so its emitted bodies are joined
// before Apply replaces that block. Returns the config paths written plus the
// non-fatal warnings Apply collected (a header the provider cannot represent,
// D69; a duplicate table adopted from a Codex rewrite, D99), for the caller to
// render.
func applyMCPEntries(entriesByProvider map[string][]mcpEntry, providers []string, dir string, claudeDirs []string, auth bool, tokenEnv string, dryRun bool) (written []string, warnings []string, err error) {
	written = make([]string, 0, len(providers))
	seenPaths := map[string]bool{}
	for _, provider := range providers {
		entries := entriesByProvider[provider]
		results := make([]*configurator.EmitResult, 0, len(entries))
		for _, entry := range entries {
			r, w, err := emitMCPEntry(provider, entry, auth, tokenEnv)
			if w != "" {
				warnings = append(warnings, w)
			}
			if err != nil {
				return nil, nil, fmt.Errorf("emit %s: %w", provider, err)
			}
			if r != nil {
				results = append(results, r)
			}
		}
		// TOML providers merge one marker-delimited block per file, so several
		// entries are concatenated into a single EmitResult.
		if d, ok := configurator.Lookup(configurator.Provider(provider)); ok && d.MCPFormat == configurator.FormatTOMLBlock && len(results) > 1 {
			joined := *results[0]
			for _, r := range results[1:] {
				joined.Content = append(joined.Content, '\n')
				joined.Content = append(joined.Content, r.Content...)
				joined.Warnings = append(joined.Warnings, r.Warnings...)
			}
			results = []*configurator.EmitResult{&joined}
		}
		for _, base := range providerBaseDirs(provider, dir, claudeDirs) {
			paths, err := configurator.Apply(results, base, dryRun)
			if errors.Is(err, safewrite.ErrSymlinkDestination) {
				// A symlinked config file or directory is refused for this
				// provider only (D148): the others are still written, and the
				// operator is told which path to replace with a real one.
				warnings = appendUnique(warnings, refusalWarnings(err)...)
				err = nil
			}
			if err != nil {
				return nil, nil, fmt.Errorf("write config for %s: %w", provider, err)
			}
			for _, path := range paths {
				if !seenPaths[path] {
					written = append(written, path)
					seenPaths[path] = true
				}
			}
		}
		for _, r := range results {
			warnings = append(warnings, r.Warnings...)
		}
	}
	return written, warnings, nil
}

// A config file or directory that is a symlink is refused (D148) for that
// provider and base only: every other one is still processed, and the refusals
// come back joined in the error after the complete results, so a caller that
// loops providers can report them (errors.Is safewrite.ErrSymlinkDestination)
// and carry on. Any other error returns at once, without results.
func removeMCPEntries(baseName string, kbs []string, providers []string, dir string, claudeDirs []string, auth bool, tokenEnv string, dryRun bool) (map[string][]string, map[string]bool, error) {
	removed := make(map[string]bool, len(providers))
	names := make(map[string][]string, len(providers))
	var refused []error
	for _, provider := range providers {
		for _, name := range managedEntryNames(baseName, kbs) {
			nameRemoved := false
			for _, base := range providerBaseDirs(provider, dir, claudeDirs) {
				ok, err := configurator.Remove(&configurator.ServerConfig{Name: name, AuthEnabled: auth, TokenEnv: tokenEnv}, configurator.Provider(provider), base, dryRun)
				if errors.Is(err, safewrite.ErrSymlinkDestination) {
					refused = append(refused, &entryRefusal{Name: name, Err: err})
					continue
				}
				if err != nil {
					return nil, nil, fmt.Errorf("remove config for %s: %w", provider, err)
				}
				nameRemoved = nameRemoved || ok
			}
			if nameRemoved {
				// Under dryRun configurator.Remove reports what it WOULD
				// remove without touching the file, which is what makes the
				// removals visible in a --dry-run plan (D172 WP4).
				names[provider] = append(names[provider], name)
			}
			removed[provider] = removed[provider] || nameRemoved
		}
	}
	return names, removed, errors.Join(refused...)
}

// providerBaseDirs is every directory a provider's MCP entry is written to:
// the client base dir, plus, for Claude Code only, the declared config dirs
// (D755). Every other provider has exactly one root.
func providerBaseDirs(provider, dir string, claudeDirs []string) []string {
	if provider != string(configurator.ProviderClaudeCode) {
		return []string{dir}
	}
	return append([]string{dir}, claudeDirs...)
}

// claudeConfigDirs resolves clients.claude.config_dirs (D755) into the extra
// directories the Claude MCP entry is written to besides home, plus the
// warnings for what was skipped. A dir is skipped, never created, when it is
// relative, does not exist, is not a directory, or holds a .claude.json that is
// a symlink (D148: a write would land in the link's target). An undeclared
// CLAUDE_CONFIG_DIR is only reported: sync also runs from session-start hooks
// that inherit one account's environment, so the env alone must not decide the
// target set.
func claudeConfigDirs(home string, cfg *clientconfig.Config) (dirs []string, warnings []string) {
	seen := map[string]bool{filepath.Clean(home): true}
	for _, raw := range cfg.Clients["claude"].ConfigDirs {
		d := raw
		if d == "~" || strings.HasPrefix(d, "~/") {
			d = filepath.Join(home, strings.TrimPrefix(d, "~"))
		}
		if !filepath.IsAbs(d) {
			warnings = append(warnings, fmt.Sprintf("clients.claude.config_dirs: %q is not an absolute path; skipped", raw))
			continue
		}
		d = filepath.Clean(d)
		if seen[d] {
			continue
		}
		seen[d] = true
		if info, err := os.Stat(d); err != nil || !info.IsDir() {
			warnings = append(warnings, fmt.Sprintf("clients.claude.config_dirs: %s does not exist or is not a directory; skipped (never created)", d))
			continue
		}
		if info, err := os.Lstat(filepath.Join(d, ".claude.json")); err == nil && !info.Mode().IsRegular() {
			warnings = append(warnings, fmt.Sprintf("clients.claude.config_dirs: %s/.claude.json is not a regular file (a symlink?); skipped", d))
			continue
		}
		dirs = append(dirs, d)
	}
	if env := os.Getenv("CLAUDE_CONFIG_DIR"); env != "" && !seen[filepath.Clean(env)] {
		warnings = append(warnings, fmt.Sprintf("CLAUDE_CONFIG_DIR=%s is not declared: its .claude.json is not updated; add it to clients.claude.config_dirs in %s", env, clientconfig.FileName))
	}
	return dirs, warnings
}

// bridgeCommand finds the command a client starts for the bridge (D737).
// TRAP: it is the path found on PATH, never os.Executable(): under Homebrew
// that resolves to a versioned Cellar directory that the next upgrade deletes,
// and every client's entry would then point at nothing. Indirected for tests.
var bridgeCommand = func() (string, error) {
	path, err := exec.LookPath("cartographer")
	if err != nil {
		return "", fmt.Errorf("the MCP bridge needs `cartographer` on PATH so a client can start it: %w", err)
	}
	return path, nil
}

// bridgeServerSpec is the stdio entry of the bridge for one provider. The token
// is not written anywhere: the bridge reads it from its own environment (or the
// private token file, D699); only Codex, which filters the environment of an
// MCP server, has to be told to pass the variable on.
func bridgeServerSpec(provider string, auth bool, tokenEnv string) (configurator.ServerSpec, error) {
	command, err := bridgeCommand()
	if err != nil {
		return configurator.ServerSpec{}, err
	}
	spec := configurator.ServerSpec{Type: "stdio", Command: command, Args: []string{"mcp", "--provider", provider}}
	if auth && tokenEnv != "" {
		spec.PassEnv = []string{tokenEnv}
	}
	return spec, nil
}
