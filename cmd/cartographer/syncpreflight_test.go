package main

import (
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/configurator"
)

// hermesBaseDirEnv is the one provider in the registry that owns its root
// through an environment variable (D141); the preflight's base-dir branch is
// only reachable through such a provider.
func hermesBaseDirEnv(t *testing.T) string {
	t.Helper()
	d, ok := configurator.Lookup(configurator.ProviderHermes)
	if !ok || d.BaseDirEnv == "" {
		t.Fatalf("hermes descriptor has no BaseDirEnv; pick another env-rooted provider for this test")
	}
	return d.BaseDirEnv
}

// TestPreflightEnvironment_MissingBaseDirOnly: a provider whose base directory
// variable is unset is reported, and nothing else is.
func TestPreflightEnvironment_MissingBaseDirOnly(t *testing.T) {
	baseDirEnv := hermesBaseDirEnv(t)
	t.Setenv(baseDirEnv, "")

	cfg := &clientconfig.Config{ServerURL: "https://example.test/mcp", Agents: []string{"hermes"}}
	err := preflightEnvironment(cfg, cfg.Agents, t.TempDir())
	if err == nil {
		t.Fatal("expected an error when the provider base directory variable is unset")
	}
	if !strings.Contains(err.Error(), "$"+baseDirEnv) {
		t.Errorf("error %q does not name $%s", err, baseDirEnv)
	}
	if !strings.Contains(err.Error(), "(no configuration was modified)") {
		t.Errorf("error %q lost the no-write guarantee suffix", err)
	}
}

// TestPreflightEnvironment_MissingTokenOnly: auth is on and the token variable
// is unset, so no credential would be sent — reported before the server says 401.
func TestPreflightEnvironment_MissingTokenOnly(t *testing.T) {
	t.Setenv("CARTOGRAPHER_TEST_TOKEN", "")

	cfg := &clientconfig.Config{
		ServerURL: "https://example.test/mcp",
		Auth:      true,
		TokenEnv:  "CARTOGRAPHER_TEST_TOKEN",
		Agents:    []string{"claude"},
	}
	err := preflightEnvironment(cfg, cfg.Agents, t.TempDir())
	if err == nil {
		t.Fatal("expected an error when the bearer token variable is unset")
	}
	if !strings.Contains(err.Error(), "$CARTOGRAPHER_TEST_TOKEN") {
		t.Errorf("error %q does not name $CARTOGRAPHER_TEST_TOKEN", err)
	}
}

// TestPreflightEnvironment_BothMissing_ReportedTogether is the trap this
// preflight exists for: two missing prerequisites used to cost two full runs,
// one error each. One run, one error, both variables named.
func TestPreflightEnvironment_BothMissing_ReportedTogether(t *testing.T) {
	baseDirEnv := hermesBaseDirEnv(t)
	t.Setenv(baseDirEnv, "")
	t.Setenv("CARTOGRAPHER_TEST_TOKEN", "")

	cfg := &clientconfig.Config{
		ServerURL: "https://example.test/mcp",
		Auth:      true,
		TokenEnv:  "CARTOGRAPHER_TEST_TOKEN",
		Agents:    []string{"hermes"},
	}
	err := preflightEnvironment(cfg, cfg.Agents, t.TempDir())
	if err == nil {
		t.Fatal("expected an error when both prerequisites are missing")
	}
	for _, want := range []string{"$" + baseDirEnv, "$CARTOGRAPHER_TEST_TOKEN"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("single error %q does not name %s", err, want)
		}
	}
}

// TestPreflightEnvironment_AuthDisabled_IgnoresToken: with auth off no
// credential is sent, so the token variable is not a prerequisite — the same
// guard resolveToken applies.
func TestPreflightEnvironment_AuthDisabled_IgnoresToken(t *testing.T) {
	t.Setenv("CARTOGRAPHER_TEST_TOKEN", "")

	cfg := &clientconfig.Config{
		ServerURL: "https://example.test/mcp",
		Auth:      false,
		TokenEnv:  "CARTOGRAPHER_TEST_TOKEN",
		Agents:    []string{"claude"},
	}
	if err := preflightEnvironment(cfg, cfg.Agents, t.TempDir()); err != nil {
		t.Fatalf("auth is disabled, expected no error, got %v", err)
	}
}

// TestPreflightEnvironment_RestrictedRun_IgnoresUnselectedProvider: a
// --client run must not fail on a provider it is not going to touch (D170).
func TestPreflightEnvironment_RestrictedRun_IgnoresUnselectedProvider(t *testing.T) {
	baseDirEnv := hermesBaseDirEnv(t)
	t.Setenv(baseDirEnv, "")
	t.Setenv("CARTOGRAPHER_TEST_TOKEN", "a-token")

	cfg := &clientconfig.Config{
		ServerURL: "https://example.test/mcp",
		Auth:      true,
		TokenEnv:  "CARTOGRAPHER_TEST_TOKEN",
		Agents:    []string{"claude", "hermes"},
	}
	targets, err := selectProviders(cfg.Agents, []string{"claude"})
	if err != nil {
		t.Fatalf("selectProviders: %v", err)
	}
	if err := preflightEnvironment(cfg, targets, t.TempDir()); err != nil {
		t.Fatalf("hermes is not selected, expected no error, got %v", err)
	}
}

// TestPreflightEnvironment_AllPresent_Silent: the happy path adds no output
// and no error.
func TestPreflightEnvironment_AllPresent_Silent(t *testing.T) {
	baseDirEnv := hermesBaseDirEnv(t)
	t.Setenv(baseDirEnv, t.TempDir())
	t.Setenv("CARTOGRAPHER_TEST_TOKEN", "a-token")

	cfg := &clientconfig.Config{
		ServerURL: "https://example.test/mcp",
		Auth:      true,
		TokenEnv:  "CARTOGRAPHER_TEST_TOKEN",
		Agents:    []string{"claude", "hermes"},
	}
	if err := preflightEnvironment(cfg, cfg.Agents, t.TempDir()); err != nil {
		t.Fatalf("every prerequisite is present, expected no error, got %v", err)
	}
}

// TestRunSync_DryRun_StopsOnPreflight: --dry-run writes nothing but must still
// report the missing prerequisites, and must do so without reaching the network
// (the server URL below points nowhere).
func TestRunSync_DryRun_StopsOnPreflight(t *testing.T) {
	t.Setenv("CARTOGRAPHER_TEST_TOKEN", "")

	cfg := &clientconfig.Config{
		ServerURL: "http://127.0.0.1:1/mcp",
		Auth:      true,
		TokenEnv:  "CARTOGRAPHER_TEST_TOKEN",
		Agents:    []string{"claude"},
	}
	_, err := runSync(t.TempDir(), cfg, syncOptions{DryRun: true})
	if err == nil {
		t.Fatal("expected the dry run to fail the preflight")
	}
	if !strings.Contains(err.Error(), "$CARTOGRAPHER_TEST_TOKEN") {
		t.Errorf("error %q does not name $CARTOGRAPHER_TEST_TOKEN", err)
	}
}
