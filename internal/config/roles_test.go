package config

import "testing"

// TestValidateAuthRolesRejectsSilentlyBrokenPolicy covers the configurations
// that would otherwise compile into broader or empty access than written.
func TestValidateAuthRolesRejectsSilentlyBrokenPolicy(t *testing.T) {
	ok := RuleSpec{KB: "docs", Access: "r"}
	cases := map[string]AuthConfig{
		"duplicate role": {Roles: []RoleSpec{
			{Name: "a", Rules: []RuleSpec{ok}},
			{Name: "a", Rules: []RuleSpec{ok}},
		}},
		"role without name":  {Roles: []RoleSpec{{Rules: []RuleSpec{ok}}}},
		"role without rules": {Roles: []RoleSpec{{Name: "a"}}},
		"empty kb":           {Roles: []RoleSpec{{Name: "a", Rules: []RuleSpec{{Access: "r"}}}}},
		"invalid access":     {Roles: []RoleSpec{{Name: "a", Rules: []RuleSpec{{KB: "docs", Access: "write"}}}}},
		"empty selector":     {Roles: []RoleSpec{{Name: "a", Rules: []RuleSpec{{KB: "docs", Access: "r", Maps: []string{""}}}}}},
		"traversal selector": {Roles: []RoleSpec{{Name: "a", Rules: []RuleSpec{{KB: "docs", Access: "r", Maps: []string{"../etc"}}}}}},
		"parent selector":    {Roles: []RoleSpec{{Name: "a", Rules: []RuleSpec{{KB: "docs", Access: "r", Journals: []string{".."}}}}}},
		"map and journal": {Roles: []RoleSpec{{Name: "a", Rules: []RuleSpec{
			{KB: "docs", Access: "r", Maps: []string{"x"}, Journals: []string{"x"}},
		}}}},
		"unknown role reference": {
			Roles:  []RoleSpec{{Name: "a", Rules: []RuleSpec{ok}}},
			Tokens: []TokenSpec{{Token: "t", Roles: []string{"b"}}},
		},
		"duplicate principal id": {
			Tokens: []TokenSpec{{Token: "t1", ID: "same"}, {Token: "t2", ID: "same"}},
		},
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			if err := ValidateAuthRoles(cfg); err == nil {
				t.Fatal("accepted an invalid configuration")
			}
		})
	}
}

// TestValidateAuthRolesAcceptsLegacyAndFineGrained keeps both the pre-D118
// shape and the new one valid.
func TestValidateAuthRolesAcceptsLegacyAndFineGrained(t *testing.T) {
	for name, cfg := range map[string]AuthConfig{
		"legacy scopes only": {Tokens: []TokenSpec{{Token: "t", Scopes: []string{"kb:docs:rw"}}}},
		"unscoped admin":     {Tokens: []TokenSpec{{Token: "t"}}},
		"roles": {
			Roles: []RoleSpec{{Name: "editor", Rules: []RuleSpec{
				{KB: "docs", Access: "rw", Maps: []string{"infra"}, Types: []string{"Runbook"}},
			}}},
			Tokens: []TokenSpec{{Token: "t", ID: "ci", Roles: []string{"editor"}}},
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := ValidateAuthRoles(cfg); err != nil {
				t.Fatalf("rejected a valid configuration: %v", err)
			}
		})
	}
}

// TestValidateAuthRolesErrorsNeverContainTokens keeps bearer secrets out of
// startup diagnostics an operator may paste into an issue.
func TestValidateAuthRolesErrorsNeverContainTokens(t *testing.T) {
	cfg := AuthConfig{Tokens: []TokenSpec{{Token: "super-secret", Roles: []string{"missing"}}}}
	err := ValidateAuthRoles(cfg)
	if err == nil {
		t.Fatal("expected an error")
	}
	if contains(err.Error(), "super-secret") {
		t.Fatalf("error leaks the token: %v", err)
	}
}

func contains(h, n string) bool {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return true
		}
	}
	return false
}
