package configurator

// Consistency tests for the provider registry (D137): the descriptors are the
// single source of provider identity, so a missing or duplicated one must fail
// here rather than degrade some caller at runtime.

import (
	"errors"
	"strings"
	"testing"
)

func TestRegistryHasOneDescriptorPerProvider(t *testing.T) {
	want := []Provider{ProviderClaudeCode, ProviderCodex, ProviderKiro, ProviderOpenCode, ProviderHermes}

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
		if d.Binary == "" && len(d.ConfigDirs) == 0 && d.BaseDirEnv == "" {
			t.Errorf("%q: no detection evidence at all", d.Provider)
		}
		// A provider whose MCP configuration Cartographer does not own (D141)
		// declares neither a config file nor an emitter — the two must agree,
		// or callers would emit into a path nobody writes (or vice versa).
		if (d.MCPConfigPath == "") != (d.emit == nil) {
			t.Errorf("%q: MCPConfigPath and emitter disagree (path=%q, emitter=%v)", d.Provider, d.MCPConfigPath, d.emit != nil)
		}
		if !d.ManagesMCPConfig() {
			if d.MCPServerKey != "" {
				t.Errorf("%q: no MCP config file but an MCP server key", d.Provider)
			}
			continue
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

// D141: a provider with a root of its own resolves it from the environment,
// and refuses to fall back to the shared base dir when the variable is unset —
// materializing into the home directory would put the files where the agent
// never looks.
func TestResolveBaseDir(t *testing.T) {
	shared := "/home/user"

	claude, _ := Lookup(ProviderClaudeCode)
	if got, err := claude.ResolveBaseDir(shared); err != nil || got != shared {
		t.Errorf("claude.ResolveBaseDir = %q, %v; want %q, nil", got, err, shared)
	}

	hermes, ok := Lookup(ProviderHermes)
	if !ok {
		t.Fatal("no descriptor for hermes")
	}
	t.Setenv(hermes.BaseDirEnv, "/opt/data")
	if got, err := hermes.ResolveBaseDir(shared); err != nil || got != "/opt/data" {
		t.Errorf("hermes.ResolveBaseDir = %q, %v; want /opt/data, nil", got, err)
	}

	t.Setenv(hermes.BaseDirEnv, "  ")
	_, err := hermes.ResolveBaseDir(shared)
	if !errors.Is(err, ErrBaseDirUnset) {
		t.Fatalf("unset base dir: got %v, want ErrBaseDirUnset", err)
	}
	if !strings.Contains(err.Error(), hermes.BaseDirEnv) {
		t.Errorf("error %q does not name %s", err, hermes.BaseDirEnv)
	}
}

// The MCP emitter must never be reached for a provider Cartographer does not
// configure (D141): a nil emitter is a bug, not a panic.
func TestEmitServerRejectsUnmanagedProvider(t *testing.T) {
	if ManagesMCPConfig(ProviderHermes) {
		t.Fatal("hermes must not be reported as an MCP-managed provider")
	}
	_, err := EmitServer("cartographer", ServerSpec{Type: "http", URL: "http://localhost:8080/mcp"}, ProviderHermes)
	if err == nil {
		t.Fatal("EmitServer(hermes) succeeded, want an error")
	}

	// EmitAll skips it instead of failing: connect must still configure every
	// other provider.
	results, err := EmitAll(&ServerConfig{Name: "cartographer", URL: "http://localhost:8080/mcp"})
	if err != nil {
		t.Fatalf("EmitAll: %v", err)
	}
	for _, r := range results {
		if r.Provider == ProviderHermes {
			t.Error("EmitAll emitted a config for hermes")
		}
	}
	if want := len(Providers()) - 1; len(results) != want {
		t.Errorf("EmitAll returned %d results, want %d", len(results), want)
	}
}
