package main

import (
	"fmt"
	"net/url"

	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/configurator"
)

// mcpEntry is one Cartographer-owned MCP entry emitted for a provider.
type mcpEntry struct {
	Name string
	URL  string
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
// bodies are joined before Apply replaces that block.
func applyMCPEntries(entries []mcpEntry, providers []string, dir string, auth bool, tokenEnv string, dryRun bool) ([]string, error) {
	written := make([]string, 0, len(providers))
	seenPaths := map[string]bool{}
	for _, provider := range providers {
		results := make([]*configurator.EmitResult, 0, len(entries))
		for _, entry := range entries {
			r, err := configurator.Emit(&configurator.ServerConfig{Name: entry.Name, URL: entry.URL, AuthEnabled: auth, TokenEnv: tokenEnv}, configurator.Provider(provider))
			if err != nil {
				return nil, fmt.Errorf("emit %s: %w", provider, err)
			}
			results = append(results, r)
		}
		if configurator.Provider(provider) == configurator.ProviderCodex && len(results) > 1 {
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
			return nil, fmt.Errorf("write config for %s: %w", provider, err)
		}
		for _, path := range paths {
			if !seenPaths[path] {
				written = append(written, path)
				seenPaths[path] = true
			}
		}
	}
	return written, nil
}

func removeMCPEntries(baseName string, kbs []string, providers []string, dir string, auth bool, tokenEnv string, dryRun bool) (map[string]bool, error) {
	removed := make(map[string]bool, len(providers))
	for _, provider := range providers {
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
