package contract

import (
	"github.com/BeppeTemp/cartographer/internal/configurator"
)

// Capability is one thing a client can do with an artifact Cartographer projects.
type Capability string

const // CapSkill: the client loads a projected skill.
CapSkill Capability = "skill"

const // CapMCP: the client reads the MCP entry Cartographer writes.
CapMCP Capability = "mcp"

const // CapAgentDelegate: the model delegates to a projected agent on its own,
// from its description.
CapAgentDelegate Capability = "agent.delegate"

const // CapAgentInvoke: the model delegates to a projected agent when told how
// (InvokeHint).
CapAgentInvoke Capability = "agent.invoke"

const // hookCapPrefix starts every per-event hook capability, `hook.<Event>`,
// for each key of HookEventReach.
HookCapPrefix = "hook."

// CapStatus is the state of one cell of the table.
type CapStatus string

const // CapVerified: probed, with the date and the recipe recorded in
// docs/harnesses.md. The only status a `requires` capability accepts.
CapVerified CapStatus = "verified"

const // CapUnverified: the destination exists, nobody probed that it works.
// Installation is unchanged; it is reported as unverified.
CapUnverified CapStatus = "unverified"

const // CapAbsent: the client cannot do it in that scope.
CapAbsent CapStatus = "absent"

// AgentModeKind says how a KB agent reaches a client in a scope.
type AgentModeKind string

const // AgentNative: the agent cell exists and the client can delegate to it
// (agent.delegate or agent.invoke is not absent).
AgentNative AgentModeKind = "native"

const // AgentDegraded: it cannot; an agent can only be offered as a skill.
AgentDegraded AgentModeKind = "degraded"

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
			Capability(HookCapPrefix + "SessionStart"):     CapVerified,
			Capability(HookCapPrefix + "UserPromptSubmit"): CapVerified,
			Capability(HookCapPrefix + "PreToolUse"):       CapVerified,
			Capability(HookCapPrefix + "PostToolUse"):      CapVerified,
			Capability(HookCapPrefix + "Stop"):             CapVerified,
			Capability(HookCapPrefix + "SubagentStop"):     CapVerified,
		},
	},
	configurator.ProviderCodex: {
		ScopeGlobal: {
			CapSkill:         CapVerified,
			CapAgentDelegate: CapVerified,
			CapAgentInvoke:   CapVerified,
			Capability(HookCapPrefix + "SessionStart"):     CapVerified,
			Capability(HookCapPrefix + "UserPromptSubmit"): CapVerified,
			Capability(HookCapPrefix + "PreToolUse"):       CapVerified,
			Capability(HookCapPrefix + "PostToolUse"):      CapVerified,
			Capability(HookCapPrefix + "Stop"):             CapVerified,
		},
		ScopeProject: {
			CapSkill:         CapVerified,
			CapMCP:           CapVerified,
			CapAgentDelegate: CapVerified,
			CapAgentInvoke:   CapVerified,
			Capability(HookCapPrefix + "SessionStart"):     CapVerified,
			Capability(HookCapPrefix + "UserPromptSubmit"): CapVerified,
			Capability(HookCapPrefix + "PreToolUse"):       CapVerified,
			Capability(HookCapPrefix + "PostToolUse"):      CapVerified,
			Capability(HookCapPrefix + "Stop"):             CapVerified,
		},
	},
	configurator.ProviderOpenCode: {
		ScopeGlobal: {
			CapSkill:         CapVerified,
			CapMCP:           CapVerified,
			CapAgentDelegate: CapVerified,
			CapAgentInvoke:   CapVerified,
			Capability(HookCapPrefix + "SessionStart"): CapVerified,
			Capability(HookCapPrefix + "PreToolUse"):   CapVerified,
			Capability(HookCapPrefix + "PostToolUse"):  CapVerified,
		},
		ScopeProject: {
			CapSkill:         CapVerified,
			CapMCP:           CapVerified,
			CapAgentDelegate: CapVerified,
			CapAgentInvoke:   CapVerified,
			Capability(HookCapPrefix + "SessionStart"): CapVerified,
			Capability(HookCapPrefix + "PreToolUse"):   CapVerified,
			Capability(HookCapPrefix + "PostToolUse"):  CapVerified,
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
			Capability(HookCapPrefix + "SessionStart"): CapVerified,
			Capability(HookCapPrefix + "PreToolUse"):   CapVerified,
			Capability(HookCapPrefix + "PostToolUse"):  CapVerified,
		},
	},
	configurator.ProviderAntigravity: {
		ScopeGlobal: {
			CapSkill:         CapVerified,
			CapMCP:           CapVerified,
			CapAgentDelegate: CapVerified,
			CapAgentInvoke:   CapVerified,
			Capability(HookCapPrefix + "SessionStart"):   CapVerified,
			Capability(HookCapPrefix + "PreInvocation"):  CapVerified,
			Capability(HookCapPrefix + "PostInvocation"): CapVerified,
			Capability(HookCapPrefix + "PreToolUse"):     CapVerified,
			Capability(HookCapPrefix + "PostToolUse"):    CapVerified,
			Capability(HookCapPrefix + "Stop"):           CapVerified,
		},
		ScopeProject: {
			CapSkill:         CapVerified,
			CapMCP:           CapVerified,
			CapAgentDelegate: CapVerified,
			CapAgentInvoke:   CapVerified,
			Capability(HookCapPrefix + "SessionStart"):   CapVerified,
			Capability(HookCapPrefix + "PreInvocation"):  CapVerified,
			Capability(HookCapPrefix + "PostInvocation"): CapVerified,
			Capability(HookCapPrefix + "PreToolUse"):     CapVerified,
			Capability(HookCapPrefix + "PostToolUse"):    CapVerified,
			Capability(HookCapPrefix + "Stop"):           CapVerified,
		},
	},
}

// CapabilityOverride replaces the table in tests (nil in production), so a
// later slice can simulate a client without agents or with a verified
// capability. The same pattern as other package-level tables swapped by tests.
var CapabilityOverride func(provider configurator.Provider, scope Scope, id Capability) (CapStatus, bool)

// CapabilityIDs returns every capability id: skill, mcp, agent.delegate,
// agent.invoke, then hook.<Event> for each event of the vocabulary, sorted.
func CapabilityIDs() []Capability {
	ids := []Capability{CapSkill, CapMCP, CapAgentDelegate, CapAgentInvoke}
	for _, e := range HookEventNames() {
		ids = append(ids, Capability(HookCapPrefix+e))
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
		absent = DestDirScoped("skill", "x", provider, scope) == ""
	case CapMCP:
		absent = DestDirScoped("mcp", "x", provider, scope) == ""
	case CapAgentDelegate, CapAgentInvoke:
		absent = DestDirScoped("agent", "x", provider, scope) == ""
	default:
		event := string(id)[len(HookCapPrefix):]
		absent = DestDirScoped("hook", "x", provider, scope) == "" || !HookEventReaches(event, provider)
	}
	if absent {
		return CapAbsent
	}
	return CapUnverified
}

// CapabilityStatus returns the status of id for provider in scope. An id
// outside the id set is absent: nothing is claimed that was never declared.
func CapabilityStatus(provider configurator.Provider, scope Scope, id Capability) CapStatus {
	if CapabilityOverride != nil {
		if s, ok := CapabilityOverride(provider, scope, id); ok {
			return s
		}
	}
	if !IsCapability(string(id)) {
		return CapAbsent
	}
	if v, ok := PluginFor(provider); ok {
		return pluginCapability(v, provider, scope, id)
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
