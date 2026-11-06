package main

import (
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// exitPartiallyConnected is the exit code of connect (and of the connect step
// of setup) when at least one provider ended "partially connected" or "not
// connected" because the write guard refused a destination (D148, #1693). 0 and
// 1 keep their meaning and 2 stays configuration/operational: the other
// providers still proceed, the code is the signal for a script.
const exitPartiallyConnected = 3

// connectNotWired collects, across the connect runs of one process, a
// "<provider> (<parts> skipped)" entry for each provider that ended partially
// or not connected: `setup` runs connect in-process and names them in place of
// "Cartographer is set up." (#1693).
var connectNotWired []string

// The parts of a provider's wiring that connect names when the guard refused
// them. partMCPEntry is the one without which the agent cannot reach
// Cartographer at all.
const (
	partMCPEntry      = "MCP entry"
	partBootstrapHook = "bootstrap hook"
	partFindingsHook  = "write-findings hook"
)

// skippedParts returns, per provider, what this run refused to write (#1693):
// the MCP entry, the hooks, and the kinds of artifact that came back in
// AppliedResult.Refused (skills, agents, hooks, instructions…), each once.
func skippedParts(refusedMCP []string, hooks map[string][]string, applied map[string]provisioning.AppliedResult) map[string][]string {
	out := map[string][]string{}
	for _, p := range refusedMCP {
		out[p] = appendUnique(out[p], partMCPEntry)
	}
	for p, parts := range hooks {
		out[p] = appendUnique(out[p], parts...)
	}
	for p, r := range applied {
		var kinds []string
		for _, a := range r.Refused {
			kinds = appendUnique(kinds, a.Kind+"s")
		}
		sort.Strings(kinds)
		if len(kinds) > 0 {
			out[p] = appendUnique(out[p], kinds...)
		}
	}
	return out
}

// providerStatusLine is the line `connect` prints for one provider after a real
// run: connected (nothing refused), partially connected (some parts refused) or
// not connected (the MCP entry was refused, whatever else was written). The
// second result is false when the provider is not fully wired.
func providerStatusLine(p string, skipped []string) (string, bool) {
	switch {
	case len(skipped) == 0:
		return "connected: " + p, true
	case slices.Contains(skipped, partMCPEntry):
		return fmt.Sprintf("not connected: %s (%s skipped: destination is a symlink)", p, strings.Join(skipped, ", ")), false
	default:
		return fmt.Sprintf("partially connected: %s (skipped: %s: destination is a symlink)", p, strings.Join(skipped, ", ")), false
	}
}

// printProviderStatuses prints providerStatusLine for each provider and returns
// the exit code the run earns: exitPartiallyConnected when any is not fully
// wired, 0 otherwise.
func printProviderStatuses(w io.Writer, providers []string, skipped map[string][]string) int {
	code := 0
	for _, p := range providers {
		line, ok := providerStatusLine(p, skipped[p])
		fmt.Fprintln(w, line)
		if !ok {
			code = exitPartiallyConnected
			connectNotWired = append(connectNotWired, notWired([]string{p}, skipped))
		}
	}
	return code
}

// notWired names the providers of skipped that are not fully connected, with
// the parts refused, for the summary of setup. "" when there are none.
func notWired(providers []string, skipped map[string][]string) string {
	var parts []string
	for _, p := range providers {
		if s := skipped[p]; len(s) > 0 {
			parts = append(parts, fmt.Sprintf("%s (%s skipped)", p, strings.Join(s, ", ")))
		}
	}
	return strings.Join(parts, "; ")
}
