package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestValidateAuthRefusesDuplicateTokenValues(t *testing.T) {
	dup := config.AuthConfig{Mode: "on", Tokens: []config.TokenSpec{
		{ID: "admin", Token: sentinelToken},
		{ID: "ci", Token: sentinelToken, Scopes: []string{"kb:docs:r"}},
	}}
	err := config.ValidateAuth(dup)
	if err == nil {
		t.Fatal("two entries with one secret were accepted")
	}
	if strings.Contains(err.Error(), sentinelToken) {
		t.Errorf("diagnostic prints the token value: %v", err)
	}
	if !strings.Contains(err.Error(), `"ci"`) || !strings.Contains(err.Error(), `"admin"`) {
		t.Errorf("diagnostic does not name both entries: %v", err)
	}
	distinct := config.AuthConfig{Mode: "on", Tokens: []config.TokenSpec{
		{ID: "admin", Token: sentinelToken},
		{ID: "ci", Token: sentinelToken + "-2", Scopes: []string{"kb:docs:r"}},
	}}
	if err := config.ValidateAuth(distinct); err != nil {
		t.Errorf("distinct tokens rejected: %v", err)
	}
}

func TestValidateAuthKindAndExpiry(t *testing.T) {
	now := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	future := "2027-06-01T00:00:00Z"
	sc := []string{"kb:docs:r"}
	tok := func(kind, exp string, scopes []string) config.AuthConfig {
		return config.AuthConfig{Mode: "on", Tokens: []config.TokenSpec{{Token: sentinelToken, ID: "t1", Kind: kind, ExpiresAt: exp, Scopes: scopes, ScopesDeclared: len(scopes) > 0}}}
	}
	ok := []struct {
		name string
		cfg  config.AuthConfig
	}{
		{"released token, no new key", tok("", "", nil)},
		{"human with expiry", tok("human", future, nil)},
		{"surveyor scoped and expiring", tok("surveyor", future, sc)},
		{"client with offset timestamp", tok("client", "2027-06-01T02:00:00+02:00", sc)},
	}
	for _, c := range ok {
		t.Run(c.name, func(t *testing.T) {
			if err := config.ValidateAuthAt(c.cfg, now); err != nil {
				t.Errorf("rejected: %v", err)
			}
		})
	}
	bad := []struct {
		name string
		cfg  config.AuthConfig
		want string
	}{
		{"unknown kind", tok("robot", "", sc), "kind"},
		{"unparsable expiry", tok("", "next tuesday", nil), "RFC 3339"},
		{"expiry in the past", tok("", "2026-12-31T23:59:59Z", nil), "already past"},
		{"expiry at the instant", tok("", "2027-01-01T00:00:00Z", nil), "already past"},
		{"surveyor without expiry", tok("surveyor", "", sc), "expires_at"},
		{"client without expiry", tok("client", "", sc), "expires_at"},
		{"surveyor admin-shaped", tok("surveyor", future, nil), "never admin"},
		{"client admin-shaped", tok("client", future, nil), "never admin"},
	}
	for _, c := range bad {
		t.Run(c.name, func(t *testing.T) {
			err := config.ValidateAuthAt(c.cfg, now)
			if err == nil {
				t.Fatal("accepted")
			}
			if !strings.Contains(err.Error(), c.want) || !strings.Contains(err.Error(), `"t1"`) {
				t.Errorf("error %q should mention %q and the token id", err, c.want)
			}
			if strings.Contains(err.Error(), sentinelToken) {
				t.Error("diagnostic leaks the token value")
			}
		})
	}
}

// auth.reload_interval: absent is the default poll, 0 stops it, a duration sets
// it, a negative or unparsable value refuses startup.
func TestAuthReloadInterval(t *testing.T) {
	load := func(body string) (*config.Config, error) {
		path := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return config.Load(path)
	}
	for body, want := range map[string]time.Duration{
		"auth:\n  mode: off\n":                            config.DefaultAuthReloadInterval,
		"auth:\n  mode: off\n  reload_interval: 0\n":      0,
		"auth:\n  mode: off\n  reload_interval: 5s\n":     5 * time.Second,
		"auth:\n  mode: off\n  reload_interval: \"2m\"\n": 2 * time.Minute,
	} {
		cfg, err := load(body)
		if err != nil {
			t.Fatalf("%q: %v", body, err)
		}
		if cfg.Auth.ReloadInterval != want {
			t.Errorf("%q: interval = %s, want %s", body, cfg.Auth.ReloadInterval, want)
		}
	}
	if _, err := load("auth:\n  reload_interval: soon\n"); err == nil {
		t.Error("an unparsable interval loaded")
	}
	if err := config.ValidateAuth(config.AuthConfig{ReloadInterval: -time.Second}); err == nil {
		t.Error("a negative interval validated")
	}
}

func TestValidateProxyIdentity(t *testing.T) {
	good := func() config.AuthConfig {
		return config.AuthConfig{
			Roles:  []config.RoleSpec{{Name: "reader", Rules: []config.RuleSpec{{KB: "kb-a", Access: "r"}}}},
			Tokens: []config.TokenSpec{{Token: "t1", ID: "sso", Proxy: true, Roles: []string{"reader"}, ScopesDeclared: true}},
			ProxyIdentity: &config.ProxyIdentitySpec{
				TrustedAddresses: []string{"192.0.2.0/24"}, UserHeader: "X-User",
				GroupRoles: map[string][]string{"staff": {"reader"}},
			},
		}
	}
	if err := config.ValidateAuth(good()); err != nil {
		t.Fatalf("good config: %v", err)
	}
	cases := map[string]func(*config.AuthConfig){
		"/0":                func(a *config.AuthConfig) { a.ProxyIdentity.TrustedAddresses = []string{"0.0.0.0/0"} },
		"no trusted":        func(a *config.AuthConfig) { a.ProxyIdentity.TrustedAddresses = nil },
		"no user header":    func(a *config.AuthConfig) { a.ProxyIdentity.UserHeader = "" },
		"authorization":     func(a *config.AuthConfig) { a.ProxyIdentity.UserHeader = "authorization" },
		"unknown role":      func(a *config.AuthConfig) { a.ProxyIdentity.GroupRoles = map[string][]string{"g": {"nope"}} },
		"no group roles":    func(a *config.AuthConfig) { a.ProxyIdentity.GroupRoles = nil },
		"no proxy token":    func(a *config.AuthConfig) { a.Tokens[0].Proxy = false },
		"admin proxy token": func(a *config.AuthConfig) { a.Tokens[0].Roles, a.Tokens[0].ScopesDeclared = nil, false },
		"surveyor proxy": func(a *config.AuthConfig) {
			a.Tokens[0].Kind, a.Tokens[0].ExpiresAt = "surveyor", "2999-01-01T00:00:00Z"
		},
		"selector role":       func(a *config.AuthConfig) { a.Roles[0].Rules[0].Maps = []string{"x"} },
		"reserved id":         func(a *config.AuthConfig) { a.Tokens[0].ID = "user:alice" },
		"proxy without block": func(a *config.AuthConfig) { a.ProxyIdentity = nil },
	}
	for name, mut := range cases {
		t.Run(name, func(t *testing.T) {
			a := good()
			mut(&a)
			if err := config.ValidateAuth(a); err == nil {
				t.Fatal("accepted")
			}
		})
	}
}
