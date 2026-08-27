package configurator

// Consistency tests for the provider registry (D137): the descriptors are the
// single source of provider identity, so a missing or duplicated one must fail
// here rather than degrade some caller at runtime.

import "testing"

func TestRegistryHasOneDescriptorPerProvider(t *testing.T) {
	want := []Provider{ProviderClaudeCode, ProviderCodex, ProviderKiro, ProviderOpenCode}

	if got := len(Providers()); got != len(want) {
		t.Fatalf("Providers() has %d descriptors, want %d", got, len(want))
	}
	seen := map[Provider]bool{}
	for _, d := range Providers() {
		if seen[d.Provider] {
			t.Errorf("duplicate descriptor for %q", d.Provider)
		}
		seen[d.Provider] = true

		if d.DisplayName == "" {
			t.Errorf("%q: empty DisplayName", d.Provider)
		}
		if d.MCPConfigPath == "" {
			t.Errorf("%q: empty MCPConfigPath", d.Provider)
		}
		if d.emit == nil {
			t.Errorf("%q: no emitter", d.Provider)
		}
		if d.Binary == "" && len(d.ConfigDirs) == 0 {
			t.Errorf("%q: no detection evidence at all", d.Provider)
		}
		switch d.MCPFormat {
		case FormatJSON:
			if d.MCPServerKey == "" {
				t.Errorf("%q: JSON provider without an MCP server key", d.Provider)
			}
		case FormatTOMLBlock:
			if d.MCPServerKey != "" {
				t.Errorf("%q: TOML provider must not declare an MCP server key", d.Provider)
			}
		}
	}
	for _, p := range want {
		if !seen[p] {
			t.Errorf("provider constant %q has no descriptor", p)
		}
	}

	// The two user-visible orders are preserved deliberately and must cover
	// exactly the same set.
	if len(DetectionOrder()) != len(want) {
		t.Errorf("DetectionOrder() has %d entries, want %d", len(DetectionOrder()), len(want))
	}
	if _, ok := Lookup(Provider("nope")); ok {
		t.Error("Lookup accepted an unknown provider")
	}
}

// .claude.json is Claude Code's own shared state file and is never deleted
// (D63): the invariant lives in the descriptor now, so assert it there.
func TestClaudeConfigIsNeverDeletable(t *testing.T) {
	d, ok := Lookup(ProviderClaudeCode)
	if !ok {
		t.Fatal("no descriptor for claude")
	}
	if d.DeletableWhenEmpty {
		t.Error("claude's config file must never be deletable when empty")
	}
	for _, other := range Providers() {
		if other.Provider != ProviderClaudeCode && !other.DeletableWhenEmpty {
			t.Errorf("%q: expected an MCP-only config file, deletable when empty", other.Provider)
		}
	}
}
