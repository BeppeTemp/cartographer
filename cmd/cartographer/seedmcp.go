package main

import (
	"fmt"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/configurator"
)

// seedMCPEntries writes Cartographer's entry into the plaintext template of a
// seed-mode variant (D1700). The entry is always the stdio bridge, never the
// HTTP one: the bridge is the shape verified end to end, and it resolves
// workspace bindings from the folder it starts in. One entry is enough (the
// bridge serves every bound KB), so the first one names it. Where the bridge
// cannot be started nothing is written: a seed never widens to a bare HTTP entry.
func seedMCPEntries(provider string, entries []mcpEntry, home string, auth bool, tokenEnv string, dryRun bool) ([]string, error) {
	if len(entries) == 0 {
		return nil, nil
	}
	spec, err := bridgeServerSpec(provider, auth, tokenEnv)
	if err != nil {
		return []string{fmt.Sprintf("%s: %v; nothing is seeded until `cartographer` is on PATH and `cartographer sync` runs again", provider, err)}, nil
	}
	res, err := configurator.SeedMCP(configurator.Provider(provider), entries[0].Name, spec, home, dryRun)
	if err != nil {
		return nil, fmt.Errorf("seed %s: %w", provider, err)
	}
	warnings := res.Warnings
	if len(res.Templates) > 0 {
		// TRAP: the bridge starts with no roots and cwd "/", so until the folder
		// is bound it answers read-only (providerScope, mcpbridge/resolve.go).
		warnings = append(warnings, fmt.Sprintf("%s: seeded %d template(s); the entry is read-only until the folder is bound (`cartographer workspace bind`) or `unbound_access: {%s: write}` is set in %s", provider, len(res.Templates), provider, clientconfig.FileName))
	}
	return warnings, nil
}
