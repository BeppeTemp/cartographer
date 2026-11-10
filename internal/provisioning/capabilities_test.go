package provisioning

import (
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
)

var capScopes = []contract.Scope{contract.ScopeGlobal, contract.ScopeProject}

func TestCapabilityTableIsComplete(t *testing.T) {
	ids := contract.CapabilityIDs()
	for _, e := range contract.HookEventNames() {
		if !contract.IsCapability(contract.HookCapPrefix + e) {
			t.Errorf("hook event %q has no capability id", e)
		}
	}
	want := len(configurator.Providers()) * len(capScopes) * len(ids)
	cells := CapabilityCells()
	if len(cells) != want {
		t.Fatalf("CapabilityCells() = %d cells, want %d (provider x scope x id)", len(cells), want)
	}
	for _, c := range cells {
		switch c.Status {
		case contract.CapVerified, contract.CapUnverified, contract.CapAbsent:
		default:
			t.Errorf("%s %s %s: status %q is not one of the three", c.Provider, ScopeName(c.Scope), c.ID, c.Status)
		}
	}
}

// The destination matrix stays the source of truth for WHERE; a capability of
// a kind with no destination in that scope is absent, and the converse holds
// for every cell nothing probed.
func TestCapabilityTableFollowsDestinationMatrix(t *testing.T) {
	for _, d := range configurator.Providers() {
		for _, scope := range capScopes {
			p := d.Provider
			for _, kind := range []string{"skill", "mcp"} {
				noDest := contract.DestDirScoped(kind, "x", p, scope) == ""
				if got := contract.CapabilityStatus(p, scope, contract.Capability(kind)) == contract.CapAbsent; got != noDest {
					t.Errorf("%s %s %s: absent=%v, destination missing=%v", p, ScopeName(scope), kind, got, noDest)
				}
			}
			noAgent := contract.DestDirScoped("agent", "x", p, scope) == ""
			for _, id := range []contract.Capability{contract.CapAgentDelegate, contract.CapAgentInvoke} {
				if got := contract.CapabilityStatus(p, scope, id) == contract.CapAbsent; got != noAgent {
					t.Errorf("%s %s %s: absent=%v, agent cell unsupported=%v", p, ScopeName(scope), id, got, noAgent)
				}
			}
			for _, e := range contract.HookEventNames() {
				id := contract.Capability(contract.HookCapPrefix + e)
				want := contract.DestDirScoped("hook", "x", p, scope) == "" || !contract.HookEventReaches(e, p)
				if got := contract.CapabilityStatus(p, scope, id) == contract.CapAbsent; got != want {
					t.Errorf("%s %s %s: absent=%v, want %v", p, ScopeName(scope), id, got, want)
				}
			}
		}
	}
}

func TestCapabilityTableCoversPiAndCline(t *testing.T) {
	// Pi was probed on 2026-11-05, Cline was not.
	for p, want := range map[configurator.Provider]contract.CapStatus{configurator.ProviderPi: contract.CapVerified, configurator.ProviderCline: contract.CapUnverified} {
		if got := contract.CapabilityStatus(p, contract.ScopeGlobal, contract.CapSkill); got != want {
			t.Errorf("%s global skill = %q, want %s", p, got, want)
		}
		if got := contract.AgentMode(p, contract.ScopeProject); got != contract.AgentDegraded {
			t.Errorf("%s project agent mode = %q, want degraded (no project cell)", p, got)
		}
	}
	// Pi has no subagents and a hook extension (#1614).
	if got := contract.AgentMode(configurator.ProviderPi, contract.ScopeGlobal); got != contract.AgentDegraded {
		t.Errorf("pi global agent mode = %q, want degraded", got)
	}
	if got := contract.CapabilityStatus(configurator.ProviderPi, contract.ScopeGlobal, "hook.PreToolUse"); got != contract.CapVerified {
		t.Errorf("pi global hook.PreToolUse = %q, want verified", got)
	}
	if got := contract.AgentMode(configurator.ProviderCline, contract.ScopeGlobal); got != contract.AgentNative {
		t.Errorf("cline global agent mode = %q, want native", got)
	}
}

func TestAgentModeIsNativeWhereAgentCellExists(t *testing.T) {
	for _, d := range configurator.Providers() {
		for _, scope := range capScopes {
			noCell := contract.DestDirScoped("agent", "x", d.Provider, scope) == ""
			if got := contract.AgentMode(d.Provider, scope) == contract.AgentDegraded; got != noCell {
				t.Errorf("%s %s: degraded=%v, agent cell missing=%v", d.Provider, ScopeName(scope), got, noCell)
			}
		}
	}
}

func TestCapabilityOverrideSeam(t *testing.T) {
	p := configurator.ProviderClaudeCode
	t.Cleanup(func() { contract.CapabilityOverride = nil })
	contract.CapabilityOverride = func(_ configurator.Provider, _ contract.Scope, id contract.Capability) (contract.CapStatus, bool) {
		switch id {
		case contract.CapAgentDelegate, contract.CapAgentInvoke:
			return contract.CapAbsent, true
		case contract.CapSkill:
			return contract.CapVerified, true
		}
		return "", false
	}
	if got := contract.AgentMode(p, contract.ScopeGlobal); got != contract.AgentDegraded {
		t.Errorf("overridden client without agents: mode %q, want degraded", got)
	}
	if got := contract.CapabilityStatus(p, contract.ScopeGlobal, contract.CapSkill); got != contract.CapVerified {
		t.Errorf("overridden skill = %q, want verified", got)
	}
	if got := contract.CapabilityStatus(p, contract.ScopeGlobal, contract.CapMCP); got == contract.CapAbsent {
		t.Errorf("an id the override does not name keeps the table value, got %q", got)
	}
}

func TestCapabilityUnknownIDIsAbsent(t *testing.T) {
	if contract.CapabilityStatus(configurator.ProviderClaudeCode, contract.ScopeGlobal, "agent.teleport") != contract.CapAbsent {
		t.Error("an undeclared id must be absent")
	}
	if contract.IsCapability("hook.") || strings.Contains(string(contract.CapSkill), " ") {
		t.Error("malformed ids must not be capabilities")
	}
}

func TestInvokeHintEmptyWhereInvokeAbsent(t *testing.T) {
	t.Cleanup(func() { capabilityInvokeHint = map[configurator.Provider]map[contract.Scope]string{} })
	capabilityInvokeHint = map[configurator.Provider]map[contract.Scope]string{
		configurator.ProviderPi: {contract.ScopeGlobal: "stale"},
	}
	if got := InvokeHint(configurator.ProviderPi, contract.ScopeGlobal); got != "" {
		t.Errorf("pi has no agent cell: hint %q, want empty", got)
	}
}
