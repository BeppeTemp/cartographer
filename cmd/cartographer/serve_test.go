package main

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/config"
	"github.com/BeppeTemp/cartographer/internal/service"
)

// --- D179: the effective configuration is validated, not just the YAML ---

// TestLoadServeConfigValidatesEnvSuppliedScopes: an invalid scope arriving from
// the environment must stop startup. Validating only at YAML load left every
// environment- and flag-supplied token unchecked, and an unparsed scope used to
// widen a token to full admin rather than fail.
func TestLoadServeConfigValidatesEnvSuppliedScopes(t *testing.T) {
	cases := []struct {
		name    string
		tokens  string
		wantErr bool
	}{
		{"valid scope", "tok|kb:docs:rw", false},
		{"bare token stays a legacy admin", "tok", false},
		{"scope missing its access segment", "tok|kb:docs", true},
		{"unknown access value", "tok|kb:docs:write", true},
		{"entry with no token half", "|kb:docs:r", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CARTOGRAPHER_TOKENS", tc.tokens)
			t.Setenv("CARTOGRAPHER_CONFIG", "")

			fs := flag.NewFlagSet("serve", flag.ContinueOnError)
			_, err := loadServeConfig(fs, config.FlagOverrides{}, "")
			if (err != nil) != tc.wantErr {
				t.Fatalf("loadServeConfig err = %v, wantErr = %v", err, tc.wantErr)
			}
			if err != nil && strings.Contains(err.Error(), "tok") && !strings.Contains(err.Error(), "token 0") {
				t.Errorf("the diagnostic leaked the token value: %q", err)
			}
		})
	}
}

// TestScopedTokensWithRolesDeclaresLegacyAdmin: the intent has to be expressed
// at this layer, because only it can tell "no restriction was written" apart
// from "the restrictions written produced nothing".
func TestScopedTokensWithRolesDeclaresLegacyAdmin(t *testing.T) {
	out := scopedTokensWithRoles([]config.TokenSpec{
		{Token: "legacy"},
		{Token: "scoped", Scopes: []string{"kb:docs:r"}},
	}, nil)

	if !out[0].Policy.Admin {
		t.Error("a token with neither scopes nor roles must be declared admin")
	}
	if out[1].Policy.Admin {
		t.Error("a scoped token must not be admin")
	}
}

// TestGeneratedLocalConfigIgnoresEnvTokens is the D268 contract end to end on
// the serve side: the server.yaml `service install` generates for a loopback
// address keeps auth off even with a CARTOGRAPHER_TOKENS in the environment
// (exported for another server), while CARTOGRAPHER_AUTH=on still turns it on
// deliberately, and a config without auth.mode keeps honouring the env tokens.
func TestGeneratedLocalConfigIgnoresEnvTokens(t *testing.T) {
	write := func(body string) string {
		p := filepath.Join(t.TempDir(), "server.yaml")
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	generated := write(service.DefaultServerYAML(t.TempDir(), "127.0.0.1:39273"))
	legacy := write("http: \"127.0.0.1:39273\"\n")

	cases := []struct {
		name     string
		config   string
		authEnv  string
		wantAuth bool
	}{
		{"generated local config ignores env tokens", generated, "", false},
		{"CARTOGRAPHER_AUTH=on still enables auth", generated, "on", true},
		{"a config without auth.mode honours env tokens", legacy, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CARTOGRAPHER_TOKENS", "remote-token")
			t.Setenv("CARTOGRAPHER_AUTH", tc.authEnv)
			t.Setenv("CARTOGRAPHER_CONFIG", "")
			fs := flag.NewFlagSet("serve", flag.ContinueOnError)
			cfg, err := loadServeConfig(fs, config.FlagOverrides{}, tc.config)
			if err != nil {
				t.Fatalf("loadServeConfig: %v", err)
			}
			on, err := resolveAuth(cfg.Auth)
			if err != nil {
				t.Fatalf("resolveAuth: %v", err)
			}
			if on != tc.wantAuth {
				t.Errorf("auth enabled = %v, want %v (mode %q, %d token(s))", on, tc.wantAuth, cfg.Auth.Mode, len(cfg.Auth.Tokens))
			}
		})
	}
}
