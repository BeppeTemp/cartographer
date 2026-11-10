package main

import (
	"testing"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/config"
)

// TestScopedTokensCompilesRolesIntoBoundedPolicy proves the D118 YAML surface
// reaches the resolver: a role with map and type selectors must produce a
// policy that allows exactly the declared perimeter and denies the rest.
func TestScopedTokensCompilesRolesIntoBoundedPolicy(t *testing.T) {
	roles := []config.RoleSpec{{
		Name: "runbook-editor",
		Rules: []config.RuleSpec{
			{KB: "docs", Access: "rw", Maps: []string{"infra"}, Types: []string{"Runbook"}},
			{KB: "reference", Access: "r"},
		},
	}}
	specs := []config.TokenSpec{{Token: "secret-token", ID: "ci", Roles: []string{"runbook-editor"}}}

	out := scopedTokensWithRoles(specs, roles)
	if len(out) != 1 {
		t.Fatalf("len = %d", len(out))
	}
	got := out[0]
	if got.Principal != "ci" {
		t.Errorf("principal = %q, want ci", got.Principal)
	}
	if got.Policy.Admin {
		t.Fatal("role-bound token must not be admin")
	}
	if !got.Policy.Allows("docs", "infra", "", "Runbook", true) {
		t.Error("declared perimeter denied")
	}
	if got.Policy.Allows("docs", "secrets", "", "Runbook", true) {
		t.Error("map outside the perimeter allowed")
	}
	if got.Policy.Allows("docs", "infra", "", "Service", true) {
		t.Error("type outside the perimeter allowed")
	}
	if got.Policy.Allows("reference", "any", "", "Note", true) {
		t.Error("read-only rule granted write")
	}
	if !got.Policy.Allows("reference", "any", "", "Note", false) {
		t.Error("read-only rule denied read")
	}
}

// TestPrincipalIDNeverLeaksTokenMaterial guards the audit/log identifier: a
// plaintext prefix of the bearer token must never appear.
func TestPrincipalIDNeverLeaksTokenMaterial(t *testing.T) {
	spec := config.TokenSpec{Token: "supersecrettokenvalue"}
	id := principalID(spec)
	if id == "" {
		t.Fatal("empty principal id")
	}
	for n := 4; n <= len(spec.Token); n++ {
		if contains(id, spec.Token[:n]) {
			t.Fatalf("principal id %q contains a token prefix", id)
		}
	}
	if principalID(config.TokenSpec{Token: "x", ID: "operator"}) != "operator" {
		t.Error("explicit id not honored")
	}
}

func contains(haystack, needle string) bool {
	return len(needle) > 0 && len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}

// TestScopedTokensUnionsRolesAndLegacyScopes proves a deployment can migrate
// one token at a time: scopes and roles coexist on the same token.
func TestScopedTokensUnionsRolesAndLegacyScopes(t *testing.T) {
	roles := []config.RoleSpec{{Name: "r1", Rules: []config.RuleSpec{{KB: "a", Access: "rw", Maps: []string{"m"}}}}}
	specs := []config.TokenSpec{{Token: "t", Scopes: []string{"kb:b:r"}, Roles: []string{"r1"}}}
	store := auth.NewScopedTokenStore(scopedTokensWithRoles(specs, roles))
	p, ok := store.PrincipalOf("t")
	if !ok {
		t.Fatal("token not found")
	}
	if p.Policy.Admin {
		t.Fatal("bounded token compiled to admin")
	}
	if !p.Policy.Allows("a", "m", "", "", true) {
		t.Error("role permission lost")
	}
	if !p.Policy.HasKBAccess("b", false) {
		t.Error("legacy scope lost")
	}
	if p.Policy.HasKBAccess("b", true) {
		t.Error("read scope escalated to write")
	}
}

// TestLegacyTokenWithoutRolesStaysAdmin is the retrocompatibility guard: an
// existing unscoped token must keep full access after D118.
func TestLegacyTokenWithoutRolesStaysAdmin(t *testing.T) {
	store := auth.NewScopedTokenStore(scopedTokensWithRoles([]config.TokenSpec{{Token: "legacy"}}, nil))
	p, ok := store.PrincipalOf("legacy")
	if !ok {
		t.Fatal("token not found")
	}
	if !p.Policy.Admin {
		t.Fatal("legacy unscoped token lost admin access")
	}
}

// TestSurveyorTokenCannotChangeArtifacts: a surveyor whose role says
// artifacts: rw still has artifacts withheld; a client keeps them; the kind
// and the expiry reach the store.
func TestSurveyorTokenCannotChangeArtifacts(t *testing.T) {
	roles := []config.RoleSpec{{Name: "w", Rules: []config.RuleSpec{{KB: "docs", Access: "rw", Artifacts: "rw"}}}}
	specs := []config.TokenSpec{
		{Token: "s", ID: "s", Kind: "surveyor", ExpiresAt: "2999-01-01T00:00:00Z", Roles: []string{"w"}},
		{Token: "c", ID: "c", Kind: "client", ExpiresAt: "2999-01-01T00:00:00Z", Roles: []string{"w"}},
		{Token: "h", ID: "h", Roles: []string{"w"}},
	}
	out := scopedTokensWithRoles(specs, roles)
	ts := auth.NewScopedTokenStore(out).RequireAuth()
	s, _ := ts.PrincipalOf("s")
	c, _ := ts.PrincipalOf("c")
	h, _ := ts.PrincipalOf("h")
	if s.Kind != auth.KindSurveyor || c.Kind != auth.KindClient || h.Kind != auth.KindHuman {
		t.Errorf("kinds: %q %q %q", s.Kind, c.Kind, h.Kind)
	}
	if s.Policy.AllowsArtifactWrite("docs") {
		t.Error("surveyor may change artifacts")
	}
	if !s.Policy.Allows("docs", "", "", "Note", true) {
		t.Error("surveyor lost its content writes")
	}
	if !c.Policy.AllowsArtifactWrite("docs") || !h.Policy.AllowsArtifactWrite("docs") {
		t.Error("client and human keep artifact writes")
	}
	if out[0].ExpiresAt.IsZero() || !out[2].ExpiresAt.IsZero() {
		t.Error("expiry not compiled")
	}
}

// A rule's secrets: none reaches the permission, for a human token as for a
// surveyor (which is forced to it), and survives the reload path, which
// compiles with the same function.
func TestSecretsNoneCompiles(t *testing.T) {
	roles := []config.RoleSpec{
		{Name: "n", Rules: []config.RuleSpec{{KB: "docs", Access: "rw", Secrets: "none"}}},
		{Name: "w", Rules: []config.RuleSpec{{KB: "docs", Access: "rw"}}},
	}
	specs := []config.TokenSpec{
		{Token: "n", ID: "n", Roles: []string{"n"}},
		{Token: "s", ID: "s", Kind: "surveyor", ExpiresAt: "2999-01-01T00:00:00Z", Roles: []string{"w"}},
		{Token: "h", ID: "h", Roles: []string{"w"}},
	}
	ts := auth.NewScopedTokenStore(scopedTokensWithRoles(specs, roles)).RequireAuth()
	n, _ := ts.PrincipalOf("n")
	s, _ := ts.PrincipalOf("s")
	h, _ := ts.PrincipalOf("h")
	if n.Policy.AllowsSecrets("docs") || s.Policy.AllowsSecrets("docs") {
		t.Error("secrets: none and the surveyor kind must withhold secrets")
	}
	if !h.Policy.AllowsSecrets("docs") {
		t.Error("a plain rw token keeps secrets")
	}
}
