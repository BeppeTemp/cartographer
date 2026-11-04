package provisioning

import (
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
)

var capScopes = []Scope{ScopeGlobal, ScopeProject}

func TestCapabilityTableIsComplete(t *testing.T) {
	ids := CapabilityIDs()
	for _, e := range HookEventNames() {
		if !IsCapability(hookCapPrefix + e) {
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
		case CapVerified, CapUnverified, CapAbsent:
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
				noDest := destDirScoped(kind, "x", p, scope) == ""
				if got := CapabilityStatus(p, scope, Capability(kind)) == CapAbsent; got != noDest {
					t.Errorf("%s %s %s: absent=%v, destination missing=%v", p, ScopeName(scope), kind, got, noDest)
				}
			}
			noAgent := destDirScoped("agent", "x", p, scope) == ""
			for _, id := range []Capability{CapAgentDelegate, CapAgentInvoke} {
				if got := CapabilityStatus(p, scope, id) == CapAbsent; got != noAgent {
					t.Errorf("%s %s %s: absent=%v, agent cell unsupported=%v", p, ScopeName(scope), id, got, noAgent)
				}
			}
			for _, e := range HookEventNames() {
				id := Capability(hookCapPrefix + e)
				want := destDirScoped("hook", "x", p, scope) == "" || !hookEventReaches(e, p)
				if got := CapabilityStatus(p, scope, id) == CapAbsent; got != want {
					t.Errorf("%s %s %s: absent=%v, want %v", p, ScopeName(scope), id, got, want)
				}
			}
		}
	}
}

func TestCapabilityTableCoversPiAndCline(t *testing.T) {
	for _, p := range []configurator.Provider{configurator.ProviderPi, configurator.ProviderCline} {
		if got := CapabilityStatus(p, ScopeGlobal, CapSkill); got != CapUnverified {
			t.Errorf("%s global skill = %q, want unverified", p, got)
		}
		if got := AgentMode(p, ScopeProject); got != AgentDegraded {
			t.Errorf("%s project agent mode = %q, want degraded (no project cell)", p, got)
		}
	}
	// Pi has no subagents and a hook extension (#1614).
	if got := AgentMode(configurator.ProviderPi, ScopeGlobal); got != AgentDegraded {
		t.Errorf("pi global agent mode = %q, want degraded", got)
	}
	if got := CapabilityStatus(configurator.ProviderPi, ScopeGlobal, "hook.PreToolUse"); got != CapUnverified {
		t.Errorf("pi global hook.PreToolUse = %q, want unverified", got)
	}
	if got := AgentMode(configurator.ProviderCline, ScopeGlobal); got != AgentNative {
		t.Errorf("cline global agent mode = %q, want native", got)
	}
}

func TestAgentModeIsNativeWhereAgentCellExists(t *testing.T) {
	for _, d := range configurator.Providers() {
		for _, scope := range capScopes {
			noCell := destDirScoped("agent", "x", d.Provider, scope) == ""
			if got := AgentMode(d.Provider, scope) == AgentDegraded; got != noCell {
				t.Errorf("%s %s: degraded=%v, agent cell missing=%v", d.Provider, ScopeName(scope), got, noCell)
			}
		}
	}
}

func TestCapabilityOverrideSeam(t *testing.T) {
	p := configurator.ProviderClaudeCode
	t.Cleanup(func() { capabilityOverride = nil })
	capabilityOverride = func(_ configurator.Provider, _ Scope, id Capability) (CapStatus, bool) {
		switch id {
		case CapAgentDelegate, CapAgentInvoke:
			return CapAbsent, true
		case CapSkill:
			return CapVerified, true
		}
		return "", false
	}
	if got := AgentMode(p, ScopeGlobal); got != AgentDegraded {
		t.Errorf("overridden client without agents: mode %q, want degraded", got)
	}
	if got := CapabilityStatus(p, ScopeGlobal, CapSkill); got != CapVerified {
		t.Errorf("overridden skill = %q, want verified", got)
	}
	if got := CapabilityStatus(p, ScopeGlobal, CapMCP); got == CapAbsent {
		t.Errorf("an id the override does not name keeps the table value, got %q", got)
	}
}

func TestCapabilityUnknownIDIsAbsent(t *testing.T) {
	if CapabilityStatus(configurator.ProviderClaudeCode, ScopeGlobal, "agent.teleport") != CapAbsent {
		t.Error("an undeclared id must be absent")
	}
	if IsCapability("hook.") || strings.Contains(string(CapSkill), " ") {
		t.Error("malformed ids must not be capabilities")
	}
}

func TestInvokeHintEmptyWhereInvokeAbsent(t *testing.T) {
	t.Cleanup(func() { capabilityInvokeHint = map[configurator.Provider]map[Scope]string{} })
	capabilityInvokeHint = map[configurator.Provider]map[Scope]string{
		configurator.ProviderPi: {ScopeGlobal: "stale"},
	}
	if got := InvokeHint(configurator.ProviderPi, ScopeGlobal); got != "" {
		t.Errorf("pi has no agent cell: hint %q, want empty", got)
	}
}
