package main

import (
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
)

// TestPreflightEnvironment_MissingTokenOnly: auth is on and the token variable
// is unset, so no credential would be sent — reported before the server says 401.
func TestPreflightEnvironment_MissingTokenOnly(t *testing.T) {
	setHome(t, t.TempDir()) // no token file (D699) from the real home
	t.Setenv("CARTOGRAPHER_TEST_TOKEN", "")

	cfg := &clientconfig.Config{
		ServerURL: "https://example.test/mcp",
		Auth:      true,
		Token:     "${CARTOGRAPHER_TEST_TOKEN}",
		Agents:    []string{"claude"},
	}
	err := preflightEnvironment(cfg)
	if err == nil {
		t.Fatal("expected an error when the bearer token variable is unset")
	}
	if !strings.Contains(err.Error(), "$CARTOGRAPHER_TEST_TOKEN") {
		t.Errorf("error %q does not name $CARTOGRAPHER_TEST_TOKEN", err)
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
		Token:     "${CARTOGRAPHER_TEST_TOKEN}",
		Agents:    []string{"claude"},
	}
	if err := preflightEnvironment(cfg); err != nil {
		t.Fatalf("auth is disabled, expected no error, got %v", err)
	}
}

// TestRunSync_DryRun_StopsOnPreflight: --dry-run writes nothing but must still
// report the missing prerequisites, and must do so without reaching the network
// (the server URL below points nowhere).
func TestRunSync_DryRun_StopsOnPreflight(t *testing.T) {
	t.Setenv("CARTOGRAPHER_TEST_TOKEN", "")
	// The token falls back to the private file in the home directory (D699):
	// a machine that ran connect has one, and the variable would not be missing.
	setHome(t, t.TempDir())

	cfg := &clientconfig.Config{
		ServerURL: "http://127.0.0.1:1/mcp",
		Auth:      true,
		Token:     "${CARTOGRAPHER_TEST_TOKEN}",
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
