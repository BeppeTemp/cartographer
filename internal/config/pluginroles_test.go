package config

import (
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/plugin/core"
)

func pluginWithRole(name string, rules ...core.RoleRule) *core.Plugin {
	p := &core.Plugin{Contributions: map[string][]core.Contribution{"roles": {{Name: "editor", Role: &core.Role{Rules: rules}}}}}
	p.Manifest.Name = name
	return p
}

func TestExpandPluginRoles(t *testing.T) {
	rules := []core.RoleRule{
		{Access: "rw", Maps: []string{"infra"}, Declared: map[string]bool{"maps": true}},
		{Access: "r", Secrets: "none"},
	}
	plugins := []*core.Plugin{pluginWithRole("team-data", rules...)}
	a := AuthConfig{
		Roles:  []RoleSpec{{Name: "ops-editor", From: "team-data/editor", KBs: []string{"kb-a", "kb-b"}}, {Name: "own", Rules: []RuleSpec{{KB: "kb-a", Access: "r"}}}},
		Tokens: []TokenSpec{{Token: "t", ID: "ci", Roles: []string{"ops-editor"}}},
	}
	if err := ValidateAuthRoles(a); err != nil {
		t.Fatalf("an unexpanded role is valid: %v", err)
	}
	got, err := ExpandPluginRoles(a, plugins, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	r := got.Roles[0]
	if r.Name != "ops-editor" || r.From != "" || r.KBs != nil || len(r.Rules) != 4 {
		t.Fatalf("expanded = %+v", r)
	}
	if r.Rules[0].KB != "kb-a" || r.Rules[0].Maps[0] != "infra" || !r.Rules[0].Declared["maps"] || r.Rules[3].KB != "kb-b" || r.Rules[3].Secrets != "none" {
		t.Errorf("rules = %+v", r.Rules)
	}
	if a.Roles[0].From == "" || got.Roles[1].Name != "own" || len(got.Roles[1].Rules) != 1 {
		t.Error("the input is untouched and the operator's own roles stay as written")
	}
}

func TestExpandPluginRolesRefuses(t *testing.T) {
	good := core.RoleRule{Access: "r"}
	plugins := []*core.Plugin{pluginWithRole("team-data", good)}
	bad := core.RoleRule{Access: "r", Maps: []string{}, Declared: map[string]bool{"maps": true}}
	for name, tc := range map[string]struct {
		role    RoleSpec
		plugins []*core.Plugin
		want    string
	}{
		"plugin not pinned": {RoleSpec{Name: "r", From: "other/editor", KBs: []string{"kb-a"}}, plugins, "not a role of a plugin pinned"},
		"role not in it":    {RoleSpec{Name: "r", From: "team-data/other", KBs: []string{"kb-a"}}, plugins, "not a role of a plugin pinned"},
		"no plugins":        {RoleSpec{Name: "r", From: "team-data/editor", KBs: []string{"kb-a"}}, nil, "not a role of a plugin pinned"},
		"failing rule":      {RoleSpec{Name: "r", From: "team-data/editor", KBs: []string{"kb-a"}}, []*core.Plugin{pluginWithRole("team-data", bad)}, "declared but empty"},
		"shadows an own":    {RoleSpec{Name: "own", From: "team-data/editor", KBs: []string{"kb-a"}}, plugins, "duplicate role"},
	} {
		a := AuthConfig{Roles: []RoleSpec{{Name: "own", Rules: []RuleSpec{{KB: "kb-a", Access: "r"}}}, tc.role}}
		if name != "shadows an own" {
			a.Roles = a.Roles[1:]
		}
		if _, err := ExpandPluginRoles(a, tc.plugins, time.Now()); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", name, err, tc.want)
		}
	}
	for name, r := range map[string]RoleSpec{
		"no kbs":         {Name: "r", From: "team-data/editor"},
		"from and rules": {Name: "r", From: "team-data/editor", KBs: []string{"kb-a"}, Rules: []RuleSpec{{KB: "kb-a", Access: "r"}}},
		"bad reference":  {Name: "r", From: "team-data", KBs: []string{"kb-a"}},
		"kbs on a rules": {Name: "r", KBs: []string{"kb-a"}, Rules: []RuleSpec{{KB: "kb-a", Access: "r"}}},
	} {
		if err := ValidateAuthRoles(AuthConfig{Roles: []RoleSpec{r}}); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// A proxy token's roles are its ceiling and must select whole KBs only: a role
// from a plugin is judged on its expanded rules, so a template that selects maps
// is refused for a proxy token like the same rule written by hand.
func TestExpandPluginRolesAppliesTheProxyCeiling(t *testing.T) {
	plugins := []*core.Plugin{pluginWithRole("team-data", core.RoleRule{Access: "r", Maps: []string{"infra"}, Declared: map[string]bool{"maps": true}})}
	a := AuthConfig{
		Roles:  []RoleSpec{{Name: "ops", From: "team-data/editor", KBs: []string{"kb-a"}}},
		Tokens: []TokenSpec{{Token: "t", ID: "sso", Roles: []string{"ops"}, Proxy: true}},
	}
	if _, err := ExpandPluginRoles(a, plugins, time.Now()); err == nil || !strings.Contains(err.Error(), "sso") {
		t.Fatalf("a proxy token with a role selecting maps must be refused, err = %v", err)
	}
	a.Tokens[0].Proxy = false
	if _, err := ExpandPluginRoles(a, plugins, time.Now()); err != nil {
		t.Fatalf("the same role on a plain token is valid: %v", err)
	}
}

// A token whose expires_at passed while configured must not stop the start:
// serve expands the plugin roles with the zero time (D-new-expired-token-at-start),
// while a caller that passes a real instant still refuses a past expiry.
func TestExpandPluginRolesZeroTimeKeepsAnExpiredToken(t *testing.T) {
	plugins := []*core.Plugin{pluginWithRole("team-data", core.RoleRule{Access: "r"})}
	a := AuthConfig{
		Roles: []RoleSpec{{Name: "r", From: "team-data/editor", KBs: []string{"kb-a"}}},
		Tokens: []TokenSpec{{Token: "tok-aaaa-0123456789", ID: "old", Kind: "client", Roles: []string{"r"},
			ExpiresAt: "2020-01-01T00:00:00Z"}},
	}
	out, err := ExpandPluginRoles(a, plugins, time.Time{})
	if err != nil {
		t.Fatalf("zero time must accept a past expires_at: %v", err)
	}
	if len(out.Tokens) != 1 {
		t.Errorf("the expired token must stay in the set, got %d", len(out.Tokens))
	}
	if _, err := ExpandPluginRoles(a, plugins, time.Now()); err == nil || !strings.Contains(err.Error(), "already past") {
		t.Errorf("a real instant must still refuse it, err = %v", err)
	}
}
