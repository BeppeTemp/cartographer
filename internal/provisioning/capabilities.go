package provisioning

import (
	"github.com/BeppeTemp/cartographer/internal/configurator"
)

// The capability table says WHETHER an artifact kind works on a client, beside
// destinationMatrix, which says WHERE it installs (D1656). Nothing consumes it
// yet: the degradation and `requires` slices read it through CapabilityStatus
// and AgentMode.

// Capability is one thing a client can do with an artifact Cartographer projects.
type Capability string

const (
	// CapSkill: the client loads a projected skill.
	CapSkill Capability = "skill"
	// CapMCP: the client reads the MCP entry Cartographer writes.
	CapMCP Capability = "mcp"
	// CapAgentDelegate: the model delegates to a projected agent on its own,
	// from its description.
	CapAgentDelegate Capability = "agent.delegate"
	// CapAgentInvoke: the model delegates to a projected agent when told how
	// (InvokeHint).
	CapAgentInvoke Capability = "agent.invoke"
	// hookCapPrefix starts every per-event hook capability, `hook.<Event>`,
	// for each key of hookEventReach.
	hookCapPrefix = "hook."
)

// CapStatus is the state of one cell of the table.
type CapStatus string

const (
	// CapVerified: probed, with the date and the recipe recorded in
	// docs/harnesses.md. The only status a `requires` capability accepts.
	CapVerified CapStatus = "verified"
	// CapUnverified: the destination exists, nobody probed that it works.
	// Installation is unchanged; it is reported as unverified.
	CapUnverified CapStatus = "unverified"
	// CapAbsent: the client cannot do it in that scope.
	CapAbsent CapStatus = "absent"
)

// AgentModeKind says how a KB agent reaches a client in a scope.
type AgentModeKind string

const (
	// AgentNative: the agent cell exists and the client can delegate to it
	// (agent.delegate or agent.invoke is not absent).
	AgentNative AgentModeKind = "native"
	// AgentDegraded: it cannot; an agent can only be offered as a skill.
	AgentDegraded AgentModeKind = "degraded"
)

// capabilityProbed holds the cells a probe moved off the derived value, by
// provider, scope and id. A `verified` entry is backed by a ledger line in
// docs/harnesses.md (date and recipe); TestCapabilityTableMatchesLedger keeps
// the two aligned. Filled by the probes of 2026-11-05 (probes.md); a cell
// nobody probed is absent from the map and stays unverified.
var capabilityProbed = map[configurator.Provider]map[Scope]map[Capability]CapStatus{
	configurator.ProviderClaudeCode: {
		ScopeProject: {
			CapSkill:       CapVerified,
			CapMCP:         CapVerified,
			CapAgentInvoke: CapVerified,
			Capability(hookCapPrefix + "SessionStart"):     CapVerified,
			Capability(hookCapPrefix + "UserPromptSubmit"): CapVerified,
			Capability(hookCapPrefix + "PreToolUse"):       CapVerified,
			Capability(hookCapPrefix + "PostToolUse"):      CapVerified,
			Capability(hookCapPrefix + "Stop"):             CapVerified,
			Capability(hookCapPrefix + "SubagentStop"):     CapVerified,
		},
	},
	configurator.ProviderCodex: {
		ScopeGlobal: {
			CapSkill:         CapVerified,
			CapAgentDelegate: CapVerified,
			CapAgentInvoke:   CapVerified,
			Capability(hookCapPrefix + "SessionStart"):     CapVerified,
			Capability(hookCapPrefix + "UserPromptSubmit"): CapVerified,
			Capability(hookCapPrefix + "PreToolUse"):       CapVerified,
			Capability(hookCapPrefix + "PostToolUse"):      CapVerified,
			Capability(hookCapPrefix + "Stop"):             CapVerified,
		},
		ScopeProject: {
			CapSkill:         CapVerified,
			CapMCP:           CapVerified,
			CapAgentDelegate: CapVerified,
			CapAgentInvoke:   CapVerified,
			Capability(hookCapPrefix + "SessionStart"):     CapVerified,
			Capability(hookCapPrefix + "UserPromptSubmit"): CapVerified,
			Capability(hookCapPrefix + "PreToolUse"):       CapVerified,
			Capability(hookCapPrefix + "PostToolUse"):      CapVerified,
			Capability(hookCapPrefix + "Stop"):             CapVerified,
		},
	},
	configurator.ProviderOpenCode: {
		ScopeGlobal: {
			CapSkill:         CapVerified,
			CapAgentDelegate: CapVerified,
			CapAgentInvoke:   CapVerified,
			Capability(hookCapPrefix + "SessionStart"): CapVerified,
			Capability(hookCapPrefix + "PreToolUse"):   CapVerified,
			Capability(hookCapPrefix + "PostToolUse"):  CapVerified,
		},
		ScopeProject: {
			CapSkill:         CapVerified,
			CapMCP:           CapVerified,
			CapAgentDelegate: CapVerified,
			CapAgentInvoke:   CapVerified,
			Capability(hookCapPrefix + "SessionStart"): CapVerified,
			Capability(hookCapPrefix + "PreToolUse"):   CapVerified,
			Capability(hookCapPrefix + "PostToolUse"):  CapVerified,
		},
	},
	configurator.ProviderKiro: {
		ScopeProject: {
			CapSkill:         CapVerified,
			CapMCP:           CapVerified,
			CapAgentDelegate: CapVerified,
			CapAgentInvoke:   CapVerified,
		},
	},
	configurator.ProviderPi: {
		ScopeGlobal: {
			CapSkill: CapVerified,
			CapMCP:   CapVerified,
			Capability(hookCapPrefix + "SessionStart"): CapVerified,
			Capability(hookCapPrefix + "PreToolUse"):   CapVerified,
			Capability(hookCapPrefix + "PostToolUse"):  CapVerified,
		},
	},
}

// capabilityInvokeHint is the one-line "how to invoke an agent" per provider
// and scope, for the providers whose agent.invoke is not absent. Empty until
// a probe recorded the sentence that works.
var capabilityInvokeHint = map[configurator.Provider]map[Scope]string{
	configurator.ProviderClaudeCode: {
		ScopeProject: "Ask for it by name: \"use the <name> agent\".",
	},
	configurator.ProviderCodex: {
		ScopeGlobal:  "Ask for it by name: \"spawn the <name> agent\".",
		ScopeProject: "Ask for it by name: \"spawn the <name> agent\".",
	},
	configurator.ProviderOpenCode: {
		ScopeGlobal:  "Ask for it by name: \"use the <name> subagent\".",
		ScopeProject: "Ask for it by name: \"use the <name> subagent\".",
	},
	configurator.ProviderKiro: {
		ScopeProject: "Ask for it by name: \"use the <name> subagent\".",
	},
}

// capabilityOverride replaces the table in tests (nil in production), so a
// later slice can simulate a client without agents or with a verified
// capability. The same pattern as other package-level tables swapped by tests.
var capabilityOverride func(provider configurator.Provider, scope Scope, id Capability) (CapStatus, bool)

// ScopeName is the scope's spelling in the ledger.
func ScopeName(scope Scope) string {
	if scope == ScopeProject {
		return "project"
	}
	return "global"
}

// CapabilityIDs returns every capability id: skill, mcp, agent.delegate,
// agent.invoke, then hook.<Event> for each event of the vocabulary, sorted.
func CapabilityIDs() []Capability {
	ids := []Capability{CapSkill, CapMCP, CapAgentDelegate, CapAgentInvoke}
	for _, e := range HookEventNames() {
		ids = append(ids, Capability(hookCapPrefix+e))
	}
	return ids
}

// IsCapability reports whether id is in the id set (not its status).
func IsCapability(id string) bool {
	for _, c := range CapabilityIDs() {
		if string(c) == id {
			return true
		}
	}
	return false
}

// derivedCapability is the rule that decides `absent` from the destination
// matrix and the hook vocabulary; every other cell starts unverified.
func derivedCapability(provider configurator.Provider, scope Scope, id Capability) CapStatus {
	absent := false
	switch id {
	case CapSkill:
		absent = destDirScoped("skill", "x", provider, scope) == ""
	case CapMCP:
		absent = destDirScoped("mcp", "x", provider, scope) == ""
	case CapAgentDelegate, CapAgentInvoke:
		absent = destDirScoped("agent", "x", provider, scope) == ""
	default:
		event := string(id)[len(hookCapPrefix):]
		absent = destDirScoped("hook", "x", provider, scope) == "" || !hookEventReaches(event, provider)
	}
	if absent {
		return CapAbsent
	}
	return CapUnverified
}

// CapabilityStatus returns the status of id for provider in scope. An id
// outside the id set is absent: nothing is claimed that was never declared.
func CapabilityStatus(provider configurator.Provider, scope Scope, id Capability) CapStatus {
	if capabilityOverride != nil {
		if s, ok := capabilityOverride(provider, scope, id); ok {
			return s
		}
	}
	if !IsCapability(string(id)) {
		return CapAbsent
	}
	if v, ok := variantFor(provider); ok {
		return variantCapability(v, provider, scope, id)
	}
	derived := derivedCapability(provider, scope, id)
	if derived == CapAbsent {
		return CapAbsent // the destination matrix wins over a probe
	}
	if s, ok := capabilityProbed[provider][scope][id]; ok {
		return s
	}
	return derived
}

// AgentMode is the one place that decides whether a KB agent is native on a
// client in a scope: its cell exists and at least one of agent.delegate and
// agent.invoke is not absent. Otherwise it is degradable.
func AgentMode(provider configurator.Provider, scope Scope) AgentModeKind {
	if CapabilityStatus(provider, scope, CapAgentDelegate) != CapAbsent ||
		CapabilityStatus(provider, scope, CapAgentInvoke) != CapAbsent {
		return AgentNative
	}
	return AgentDegraded
}

// InvokeHint is the one line the environment section gives for invoking a
// native agent on provider in scope; empty when agent.invoke is absent or no
// probe recorded one.
func InvokeHint(provider configurator.Provider, scope Scope) string {
	if CapabilityStatus(provider, scope, CapAgentInvoke) == CapAbsent {
		return ""
	}
	if v, ok := variantFor(provider); ok {
		if v.invokeHint != "" {
			return v.invokeHint
		}
		provider = v.base
	}
	return capabilityInvokeHint[provider][scope]
}

// CapabilityCell is one row of the table, as docs/harnesses.md spells it.
type CapabilityCell struct {
	Provider configurator.Provider
	Scope    Scope
	ID       Capability
	Status   CapStatus
}

// CapabilityCells lists every cell of the table, ordered by provider (registry
// order), scope (global first) and id, for the ledger parity test.
func CapabilityCells() []CapabilityCell {
	var out []CapabilityCell
	ids := CapabilityIDs()
	for _, d := range configurator.Providers() {
		for _, scope := range []Scope{ScopeGlobal, ScopeProject} {
			for _, id := range ids {
				out = append(out, CapabilityCell{d.Provider, scope, id, CapabilityStatus(d.Provider, scope, id)})
			}
		}
	}
	return out
}
