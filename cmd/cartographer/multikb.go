package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/config"
	"github.com/BeppeTemp/cartographer/internal/configurator"
)

// mcpEntry is one Cartographer-owned MCP entry emitted for a provider.
type mcpEntry struct {
	Name string
	URL  string
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
				return nil, fmt.Errorf("configured KB %q is not among the KBs currently advertised by the server (stale selection: check .cartographer.yaml or the server config)", name)
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

// enumerateKBs obtains the mounted KB names from /health. present is false
// when a healthy single-KB (or pre-multi-KB) server omits kbs; callers retain
// the old bare-entry behaviour in that case.
func enumerateKBs(serverURL string, auth bool, tokenEnv string) (names []string, present bool, err error) {
	token := ""
	if auth && tokenEnv != "" {
		token = resolveToken(&clientconfig.Config{Auth: auth, TokenEnv: tokenEnv})
	}
	health, err := client.New(serverURL, token).Health(probeTimeout)
	if err != nil {
		return nil, false, err
	}
	if health.KBs == nil {
		return nil, false, nil
	}
	names = make([]string, 0, len(*health.KBs))
	for _, kb := range *health.KBs {
		if kb.Name == "" {
			return nil, true, fmt.Errorf("health response contains a KB without a name")
		}
		names = append(names, kb.Name)
	}
	return names, true, nil
}

// kiroFlatNamespaceWarning returns a non-empty warning when providers
// includes kiro and entries has 2 or more entries (a multi-KB connection):
// Kiro's MCP tool namespace is flat across servers — unlike Claude Code,
// Codex and OpenCode, which namespace tools per server (verified empirically,
// see the D102 issue thread) — so without a server-side tool_prefix on the
// extra KBs, only one of them stays reachable in a Kiro session, silently.
// enumerateKBs returns names only, so the client cannot tell whether the
// server mounted those KBs with prefixes; making this warning prefix-aware
// would mean plumbing GET /health's KBInfo.ToolPrefix through it to avoid one
// stderr line of false positive. It stays unconditional on the precondition:
// the server itself warns from what it actually mounted (D144,
// flatNamespaceMountWarning).
func kiroFlatNamespaceWarning(providers []string, entries []mcpEntry) string {
	if len(entries) < 2 {
		return ""
	}
	for _, p := range providers {
		if d, ok := configurator.Lookup(configurator.Provider(p)); ok && d.FlatToolNamespace {
			return fmt.Sprintf("%s has a flat MCP tool namespace across servers: writing %d MCP entries "+
				"means only one KB's tools will be reachable in a %s session unless the server mounts the "+
				"others with a tool_prefix (see docs/deployment.md §MCP tool-name prefix)",
				d.Provider, len(entries), d.DisplayName)
		}
	}
	return ""
}

// flatNamespaceMountWarning returns a non-empty warning when this server
// mounts 2 or more KBs and 2 or more of them registered their tools
// unprefixed: those KBs advertise identical tool names, and a client with a
// flat MCP tool namespace (Kiro) silently keeps only one of them — answering
// questions about one KB from another (D144). Warning only, never a startup
// failure and never an implicit prefix: a Claude Code/Codex/OpenCode
// deployment namespaces per server and must keep working untouched (D102).
// names and prefixes are positionally paired, as built by the mount loop.
func flatNamespaceMountWarning(names, prefixes []string) string {
	if len(names) < 2 {
		return ""
	}
	var unprefixed []string
	for i, name := range names {
		if i < len(prefixes) && prefixes[i] != "" {
			continue
		}
		unprefixed = append(unprefixed, name)
	}
	if len(unprefixed) < 2 {
		return ""
	}
	return fmt.Sprintf("KBs %s register identical MCP tool names: clients with a flat MCP tool namespace "+
		"(kiro) silently keep only one of them, and answer from the wrong KB. Set kbs[].tool_prefix or "+
		"mcp.tool_prefix_mode: kb-name (see docs/deployment.md §MCP tool-name prefix)",
		strings.Join(quoteAll(unprefixed), ", "))
}

// quoteAll quotes each name for a log line listing KBs.
func quoteAll(names []string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = strconv.Quote(n)
	}
	return out
}

// entriesForKBs implements D92's compatibility rule: a zero/one-KB server
// keeps one bare entry; a multi-KB server gets one explicitly-scoped entry per
// KB. url.URL is used rather than concatenation so an existing query survives.
func entriesForKBs(baseName, serverURL string, kbs []string) ([]mcpEntry, error) {
	if len(kbs) <= 1 {
		return []mcpEntry{{Name: baseName, URL: serverURL}}, nil
	}
	u, err := url.Parse(serverURL)
	if err != nil {
		return nil, fmt.Errorf("parse server URL %q: %w", serverURL, err)
	}
	entries := make([]mcpEntry, 0, len(kbs))
	for _, kb := range kbs {
		entryURL := *u
		q := entryURL.Query()
		q.Set("kb", kb)
		entryURL.RawQuery = q.Encode()
		entries = append(entries, mcpEntry{Name: baseName + "-" + kb, URL: entryURL.String()})
	}
	return entries, nil
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

func entryNames(entries []mcpEntry) []string {
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name)
	}
	return names
}

// applyMCPEntries emits all entries for each provider. Codex represents all
// Cartographer MCP entries in one marker-delimited TOML block, so its emitted
// bodies are joined before Apply replaces that block. Returns the config paths
// written plus the non-fatal warnings Apply collected (a header the provider
// cannot represent, D69; a duplicate table adopted from a Codex rewrite, D99),
// for the caller to render.
func applyMCPEntries(entries []mcpEntry, providers []string, dir string, auth bool, tokenEnv string, dryRun bool) (written []string, warnings []string, err error) {
	written = make([]string, 0, len(providers))
	seenPaths := map[string]bool{}
	for _, provider := range providers {
		if !configurator.ManagesMCPConfig(configurator.Provider(provider)) {
			// A provider whose MCP endpoints are configured outside
			// Cartographer (D141, hermes: its config.yaml is rendered by its
			// Ansible role). Say so — silently writing nothing would look
			// like a bug the first time someone connects it.
			warnings = append(warnings, unmanagedMCPConfigNote(configurator.Provider(provider)))
			continue
		}
		results := make([]*configurator.EmitResult, 0, len(entries))
		for _, entry := range entries {
			r, err := configurator.Emit(&configurator.ServerConfig{Name: entry.Name, URL: entry.URL, AuthEnabled: auth, TokenEnv: tokenEnv}, configurator.Provider(provider))
			if err != nil {
				return nil, nil, fmt.Errorf("emit %s: %w", provider, err)
			}
			results = append(results, r)
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
		paths, err := configurator.Apply(results, dir, dryRun)
		if err != nil {
			return nil, nil, fmt.Errorf("write config for %s: %w", provider, err)
		}
		for _, path := range paths {
			if !seenPaths[path] {
				written = append(written, path)
				seenPaths[path] = true
			}
		}
		for _, r := range results {
			warnings = append(warnings, r.Warnings...)
		}
	}
	return written, warnings, nil
}

func removeMCPEntries(baseName string, kbs []string, providers []string, dir string, auth bool, tokenEnv string, dryRun bool) (map[string]bool, error) {
	removed := make(map[string]bool, len(providers))
	for _, provider := range providers {
		if !configurator.ManagesMCPConfig(configurator.Provider(provider)) {
			// Nothing was ever written for it (D141), so there is nothing to
			// remove and no config file to look for.
			continue
		}
		for _, name := range managedEntryNames(baseName, kbs) {
			ok, err := configurator.Remove(&configurator.ServerConfig{Name: name, AuthEnabled: auth, TokenEnv: tokenEnv}, configurator.Provider(provider), dir, dryRun)
			if err != nil {
				return nil, fmt.Errorf("remove config for %s: %w", provider, err)
			}
			removed[provider] = removed[provider] || ok
		}
	}
	return removed, nil
}

// unmanagedMCPConfigNote is what `connect` reports for a provider whose MCP
// configuration Cartographer does not own (D141). It names the provider and
// what stays the operator's job, so the absence of a written config file reads
// as a decision rather than a failure.
func unmanagedMCPConfigNote(provider configurator.Provider) string {
	name := string(provider)
	if d, ok := configurator.Lookup(provider); ok {
		name = d.DisplayName
	}
	return fmt.Sprintf("%s: registered for artifact delivery; its MCP endpoint is NOT configured by Cartographer (it is rendered by %s' own deployment) — point it at this server yourself", name, provider)
}
