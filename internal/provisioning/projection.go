package provisioning

import (
	"fmt"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
)

// ProjectManifest is the client-side projection step: it replaces the filter
// that kept only the artifacts a provider can materialize in a scope. It keeps
// what FilterForProviderScoped keeps and, for each KB agent the client cannot
// run as a subagent, appends a derived skill (D1656, WP4). Requirement
// evaluation arrives behind this same function.
//
// A derived skill is a verified artifact transformed locally: it runs after
// VerifiedManifest, copies Signed/BuiltIn/Signature from its origin and keeps
// the origin in DerivedFrom, which approval and provenance resolve to.

// WorkspaceLacksKinds lists the artifact kinds that provider carries in the
// global scope and has no workspace destination for, the ones ProjectOne
// reports as skipped. An agent a workspace degrades to a skill is not one.
func WorkspaceLacksKinds(provider configurator.Provider) []string {
	var out []string
	for _, kind := range artifactKinds {
		if contract.DestDirScoped(kind, "x", provider, contract.ScopeProject) != "" || contract.DestDirScoped(kind, "x", provider, contract.ScopeGlobal) == "" {
			continue
		}
		if kind == "agent" && contract.DestDirScoped("skill", "x", provider, contract.ScopeProject) != "" {
			continue
		}
		out = append(out, kind)
	}
	return out
}

// ProjectManifest returns what provider receives in scope from m, and one
// note per agent that was degraded or could not be.
func ProjectManifest(m contract.Manifest, provider configurator.Provider, scope contract.Scope, opts contract.ProjectOptions) (contract.Manifest, []contract.ProjectionNote) {
	out, notes, _ := contract.ProjectOne(m, provider, scope, opts)
	return out, notes
}

// derivedFromLabel is the ManagedFile.DerivedFrom spelling.
func derivedFromLabel(a contract.Artifact) string {
	if a.DerivedFrom == nil {
		return ""
	}
	return a.DerivedFrom.Kind + "/" + a.DerivedFrom.Name
}

// degradedWidenedWarnings is widenedAgentWarnings for the agents installed as
// skills: they lose their `tools` allow-list like on any non-Claude client.
func degradedWidenedWarnings(m contract.Manifest, opts ApplyOptions) []string {
	var out []string
	for _, a := range m.Artifacts {
		if a.DerivedFrom == nil || a.DerivedFrom.Restriction == nil || a.DerivedFrom.Restriction.Strict {
			continue
		}
		if !artifactAuthorized(a, opts) {
			continue
		}
		out = append(out, fmt.Sprintf(
			"agent %[1]q declares tools: %[2]s; %[3]s has no subagents, so it receives the agent as a degraded skill with no tool restriction (add `strict_tools: true` to skip it there instead)",
			a.Name, a.DerivedFrom.Restriction.Tools, opts.Provider))
	}
	return out
}
