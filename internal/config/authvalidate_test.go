package config_test

import (
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/config"
)

// sentinelToken is a stand-in for a real credential. Every negative case
// asserts it never appears in a diagnostic: a scope field is exactly where a
// token gets pasted by mistake, and startup output is where it must not show up.
const sentinelToken = "s3cr3t-do-not-print"

func TestValidateAuthAcceptsValidConfigurations(t *testing.T) {
	role := config.RoleSpec{Name: "reader", Rules: []config.RuleSpec{{KB: "docs", Access: "r"}}}
	cases := []struct {
		name string
		cfg  config.AuthConfig
	}{
		{"no auth at all", config.AuthConfig{}},
		{"legacy admin token: no scopes, no roles", config.AuthConfig{
			Mode: "on", Tokens: []config.TokenSpec{{Token: sentinelToken}}}},
		{"scoped token", config.AuthConfig{
			Mode: "on", Tokens: []config.TokenSpec{{Token: sentinelToken, Scopes: []string{"kb:docs:rw"}}}}},
		{"role-only token", config.AuthConfig{
			Mode: "on", Roles: []config.RoleSpec{role},
			Tokens: []config.TokenSpec{{Token: sentinelToken, Roles: []string{"reader"}}}}},
		{"roles and scopes together", config.AuthConfig{
			Mode: "on", Roles: []config.RoleSpec{role},
			Tokens: []config.TokenSpec{{Token: sentinelToken, Roles: []string{"reader"}, Scopes: []string{"kb:notes:r"}}}}},
		{"several scopes in one string", config.AuthConfig{
			Mode: "on", Tokens: []config.TokenSpec{{Token: sentinelToken, Scopes: []string{"kb:a:r kb:b:rw"}}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := config.ValidateAuth(tc.cfg); err != nil {
				t.Errorf("ValidateAuth rejected a valid configuration: %v", err)
			}
		})
	}
}

func TestValidateAuthRejectsWideningConfigurations(t *testing.T) {
	cases := []struct {
		name string
		cfg  config.AuthConfig
		want string
	}{
		{
			// The whole point: this used to become an unrestricted admin token.
			name: "scope missing its access segment",
			cfg:  config.AuthConfig{Mode: "on", Tokens: []config.TokenSpec{{Token: sentinelToken, Scopes: []string{"kb:finance"}}}},
			want: "invalid scope",
		},
		{
			name: "unknown access value",
			cfg:  config.AuthConfig{Mode: "on", Tokens: []config.TokenSpec{{Token: sentinelToken, Scopes: []string{"kb:finance:write"}}}},
			want: "invalid scope",
		},
		{
			// One good scope must not excuse a broken one.
			name: "mixed valid and invalid scopes",
			cfg:  config.AuthConfig{Mode: "on", Tokens: []config.TokenSpec{{Token: sentinelToken, Scopes: []string{"kb:a:r", "kb:b"}}}},
			want: "invalid scope",
		},
		{
			// Valid roles must not rescue a malformed scope on the same token.
			name: "valid roles plus a malformed scope",
			cfg: config.AuthConfig{Mode: "on",
				Roles:  []config.RoleSpec{{Name: "reader", Rules: []config.RuleSpec{{KB: "docs", Access: "r"}}}},
				Tokens: []config.TokenSpec{{Token: sentinelToken, Roles: []string{"reader"}, Scopes: []string{"kb:b"}}}},
			want: "invalid scope",
		},
		{
			// resolveAuth counted this record; the store dropped it.
			name: "empty token value",
			cfg:  config.AuthConfig{Mode: "on", Tokens: []config.TokenSpec{{Token: "", Scopes: []string{"kb:a:r"}}}},
			want: "empty token value",
		},
		{
			name: "whitespace-only token value",
			cfg:  config.AuthConfig{Mode: "on", Tokens: []config.TokenSpec{{Token: "   "}}},
			want: "empty token value",
		},
		{
			name: "unknown role",
			cfg:  config.AuthConfig{Mode: "on", Tokens: []config.TokenSpec{{Token: sentinelToken, Roles: []string{"nonesuch"}}}},
			want: "unknown role",
		},
		{
			name: "unknown mode",
			cfg:  config.AuthConfig{Mode: "onn", Tokens: []config.TokenSpec{{Token: sentinelToken}}},
			want: "mode",
		},
		{
			// A latent typo must not become an exposure the day auth is turned on.
			name: "invalid scope is refused even with auth off",
			cfg:  config.AuthConfig{Mode: "off", Tokens: []config.TokenSpec{{Token: sentinelToken, Scopes: []string{"kb:finance"}}}},
			want: "invalid scope",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := config.ValidateAuth(tc.cfg)
			if err == nil {
				t.Fatal("ValidateAuth accepted a configuration that grants more than declared")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to mention %q", err, tc.want)
			}
			if strings.Contains(err.Error(), sentinelToken) {
				t.Errorf("the diagnostic leaked the token value: %q", err)
			}
		})
	}
}

// TestValidateAuthNamesTheToken: an operator with several tokens needs to know
// which one, and the ID is the non-secret way to say it.
func TestValidateAuthNamesTheToken(t *testing.T) {
	err := config.ValidateAuth(config.AuthConfig{Mode: "on", Tokens: []config.TokenSpec{
		{Token: "fine", Scopes: []string{"kb:a:r"}},
		{Token: sentinelToken, ID: "ci-runner", Scopes: []string{"kb:b"}},
	}})
	if err == nil {
		t.Fatal("expected a rejection")
	}
	if !strings.Contains(err.Error(), "ci-runner") {
		t.Errorf("error = %q, want it to name the offending principal", err)
	}
}
