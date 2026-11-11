package config

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/BeppeTemp/cartographer/internal/plugin/core"
)

var pluginRoleRef = regexp.MustCompile(`^([a-z][a-z0-9-]*)/([a-z0-9][a-z0-9-]*)$`)

// validatePluginRole judges the shape of a role that takes its rules from a
// plugin: exclusive with rules, with the KBs it applies to named. A role that
// writes rules names no kbs.
func validatePluginRole(r RoleSpec) error {
	if r.From == "" {
		if len(r.KBs) > 0 {
			return fmt.Errorf("role %q: kbs only applies to a role with from", r.Name)
		}
		return nil
	}
	if !pluginRoleRef.MatchString(r.From) {
		return fmt.Errorf("role %q: from %q must be <plugin>/<role>", r.Name, r.From)
	}
	if len(r.Rules) > 0 {
		return fmt.Errorf("role %q: from and rules are exclusive", r.Name)
	}
	if len(r.KBs) == 0 {
		return fmt.Errorf("role %q: from needs kbs, the KBs the plugin's rules apply to (a plugin does not know them)", r.Name)
	}
	for _, k := range r.KBs {
		if strings.TrimSpace(k) == "" {
			return fmt.Errorf("role %q: kbs has an empty name", r.Name)
		}
	}
	return nil
}

// RuleFromPlugin is the rule a plugin's role template yields for one KB.
func RuleFromPlugin(r core.RoleRule, kb string) RuleSpec {
	return RuleSpec{KB: kb, Access: r.Access, Maps: r.Maps, Journals: r.Journals, Types: r.Types, Artifacts: r.Artifacts, Secrets: r.Secrets, Declared: r.Declared}
}

// ExpandPluginRoles replaces every role that has from by the rules of the
// plugin's role template, one copy per KB of kbs, and validates the result with
// the same rules as one the operator wrote. A plugin only supplies the template:
// tokens still reference the operator's role name, and a token gets the role
// only if the operator's config names it, so a plugin never widens a principal.
// An unknown plugin (not pinned in plugins) or role is an error, never an empty
// role. The whole auth validation (ValidateAuthAt at now, proxy ceilings
// included) runs on the expanded roles: before the expansion a plugin role has no
// rules, so nothing that reads rules could judge it. The input is not modified.
func ExpandPluginRoles(a AuthConfig, plugins []*core.Plugin, now time.Time) (AuthConfig, error) {
	roles := make([]RoleSpec, len(a.Roles))
	for i, r := range a.Roles {
		roles[i] = r
		if r.From == "" {
			continue
		}
		name, role, _ := strings.Cut(r.From, "/")
		var tpl *core.Role
		for _, p := range plugins {
			if p.Manifest.Name != name {
				continue
			}
			for _, c := range p.Contributions["roles"] {
				if c.Name == role {
					tpl = c.Role
				}
			}
		}
		if tpl == nil {
			return a, fmt.Errorf("auth.roles: role %q: from %q is not a role of a plugin pinned in plugins", r.Name, r.From)
		}
		roles[i].Rules, roles[i].From, roles[i].KBs = nil, "", nil
		for _, k := range r.KBs {
			for _, rule := range tpl.Rules {
				roles[i].Rules = append(roles[i].Rules, RuleFromPlugin(rule, k))
			}
		}
	}
	a.Roles = roles
	if err := ValidateAuthAt(a, now); err != nil {
		return a, fmt.Errorf("auth: %w", err)
	}
	return a, nil
}
