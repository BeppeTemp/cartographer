package provisioning

import (
	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
)

// The capability table says WHETHER an artifact kind works on a client, beside
// destinationMatrix, which says WHERE it installs (D1656). Nothing consumes it
// yet: the degradation and `requires` slices read it through CapabilityStatus
// and AgentMode.

// capabilityInvokeHint is the one-line "how to invoke an agent" per provider
// and scope, for the providers whose agent.invoke is not absent. Empty until
// a probe recorded the sentence that works.
var capabilityInvokeHint = map[configurator.Provider]map[contract.Scope]string{
	configurator.ProviderClaudeCode: {
		contract.ScopeProject: "Ask for it by name: \"use the <name> agent\".",
	},
	configurator.ProviderCodex: {
		contract.ScopeGlobal:  "Ask for it by name: \"spawn the <name> agent\".",
		contract.ScopeProject: "Ask for it by name: \"spawn the <name> agent\".",
	},
	configurator.ProviderOpenCode: {
		contract.ScopeGlobal:  "Ask for it by name: \"use the <name> subagent\".",
		contract.ScopeProject: "Ask for it by name: \"use the <name> subagent\".",
	},
	configurator.ProviderKiro: {
		contract.ScopeProject: "Ask for it by name: \"use the <name> subagent\".",
	},
}

// ScopeName is the scope's spelling in the ledger.
func ScopeName(scope contract.Scope) string {
	if scope == contract.ScopeProject {
		return "project"
	}
	return "global"
}

// InvokeHint is the one line the environment section gives for invoking a
// native agent on provider in scope; empty when agent.invoke is absent or no
// probe recorded one.
func InvokeHint(provider configurator.Provider, scope contract.Scope) string {
	if contract.CapabilityStatus(provider, scope, contract.CapAgentInvoke) == contract.CapAbsent {
		return ""
	}
	if v, ok := contract.PluginFor(provider); ok {
		if v.InvokeHint != "" {
			return v.InvokeHint
		}
		provider = v.Base
	}
	return capabilityInvokeHint[provider][scope]
}

// CapabilityCell is one row of the table, as docs/harnesses.md spells it.
type CapabilityCell struct {
	Provider configurator.Provider
	Scope    contract.Scope
	ID       contract.Capability
	Status   contract.CapStatus
}

// CapabilityCells lists every cell of the table, ordered by provider (registry
// order), scope (global first) and id, for the ledger parity test.
func CapabilityCells() []CapabilityCell {
	var out []CapabilityCell
	ids := contract.CapabilityIDs()
	for _, d := range configurator.Providers() {
		for _, scope := range []contract.Scope{contract.ScopeGlobal, contract.ScopeProject} {
			for _, id := range ids {
				out = append(out, CapabilityCell{d.Provider, scope, id, contract.CapabilityStatus(d.Provider, scope, id)})
			}
		}
	}
	return out
}
