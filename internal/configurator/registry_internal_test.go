package configurator

// Consistency tests for the provider registry (D137): the descriptors are the
// single source of provider identity, so a missing or duplicated one must fail
// here rather than degrade some caller at runtime.

import "testing"

func TestRegistryHasOneDescriptorPerProvider(t *testing.T) {
	want := []Provider{ProviderClaudeCode, ProviderCodex, ProviderKiro, ProviderOpenCode, ProviderAntigravity, ProviderCrush, ProviderCopilot}

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
		if len(d.Binaries) == 0 && len(d.ConfigDirs) == 0 {
			t.Errorf("%q: no detection evidence at all", d.Provider)
		}
		// Every provider has a config file Cartographer writes and the emitter
		// that renders it (D898: there is no provider without either).
		if d.MCPConfigPath == "" || d.emit == nil {
			t.Errorf("%q: MCPConfigPath and emitter must both be set (path=%q, emitter=%v)", d.Provider, d.MCPConfigPath, d.emit != nil)
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

// .claude.json is Claude Code's own shared state file: deleted only when we
// created it (DeleteOnlyIfCreated, #1269), the invariant lives in the descriptor.
func TestEveryConfigIsDeletableWhenEmpty(t *testing.T) {
	// Claude's .claude.json is shared state, but a removal that leaves `{}` has
	// nothing of Claude's to keep either (#1269); one holding another key is
	// kept (TestRemove_ClaudeJSONKeepsOtherKeys).
	for _, d := range Providers() {
		if !d.DeletableWhenEmpty {
			t.Errorf("%q: expected the config file deletable when empty", d.Provider)
		}
	}
}

// EmitAll emits one config per provider.
func TestEmitAllCoversEveryProvider(t *testing.T) {
	results, err := EmitAll(&ServerConfig{Name: "cartographer", URL: "http://localhost:8080/mcp"})
	if err != nil {
		t.Fatalf("EmitAll: %v", err)
	}
	if want := len(Providers()); len(results) != want {
		t.Errorf("EmitAll returned %d results, want %d", len(results), want)
	}
}
