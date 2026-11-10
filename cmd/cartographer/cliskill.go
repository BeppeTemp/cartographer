package main

import (
	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
)

// cliSkillName is the bundled skill that teaches an agent to reach the KB
// through `cartographer call`.
const cliSkillName = contract.CLISkillName

// usesCLI is the provider predicate, a variable so a test can inject one.
var usesCLI = configurator.UsesCLI

// usesNoMCP is the predicate of a provider that gets neither an MCP entry nor the
// command route (MCPModeNone), a variable so a test can inject one.
var usesNoMCP = configurator.UsesNoMCP

// skipsMCPEntry reports whether connect, sync, disconnect and doctor leave the
// provider's MCP entry alone, without a warning: it reaches Cartographer through
// the shell (cli) or not at all (none).
func skipsMCPEntry(provider string) bool {
	p := configurator.Provider(provider)
	return usesCLI(p) || usesNoMCP(p)
}

// withoutCLISkill drops the cartographer-cli bundle skill from a provider's
// manifest unless the provider reaches Cartographer through the command.
//
// Defense in depth: the server's BuildManifest already leaves the skill out
// (BuildOptions.IncludeCLISkill), so this only matters for a server that sets it.
//
// Trap: BuildManifest lists every directory of the bundle and the server serves
// it to every client, so a new bundled skill reaches every manifest. The
// filter is client side and after VerifiedManifest (bundle artifacts are
// BuiltIn, not signed, so dropping one is safe): ComputeDiff then lists
// nothing, the lockfile-driven prune removes a copy installed earlier, and the
// status counts are computed against the filtered manifest.
func withoutCLISkill(provider string, m contract.Manifest) contract.Manifest {
	if usesCLI(configurator.Provider(provider)) {
		return m
	}
	kept := make([]contract.Artifact, 0, len(m.Artifacts))
	for _, a := range m.Artifacts {
		if a.Kind == "skill" && a.Name == cliSkillName && a.Source == "bundle" {
			continue
		}
		kept = append(kept, a)
	}
	m.Artifacts = kept
	return m
}
