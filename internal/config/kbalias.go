package config

import (
	"fmt"
	"regexp"
)

// KBAlias records that a KB was renamed: a routed `?kbs=<From>` still names it
// after `kb rename`, and the server resolves it to the KB mounted as To (D772).
// It selects which KB a URL names and never widens what a token may do.
type KBAlias struct {
	From string `yaml:"from" json:"from"`
	To   string `yaml:"to" json:"to"`
}

// kbNameRe validates a KB name as directory-safe: letters, digits, '-', '_'
// only — no '/', '.', or whitespace, so the name can never escape
// <dataDir>/<name> (no ".." is even expressible) and matches the kebab-case
// convention already documented for KB names (D53, skillbundle kb-create
// SKILL.md).
var kbNameRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// ValidateKBName returns an error describing why name is not a usable KB
// name, or nil if it is.
func ValidateKBName(name string) error {
	if name == "" {
		return fmt.Errorf("KB name must not be empty")
	}
	if !kbNameRe.MatchString(name) {
		return fmt.Errorf("invalid KB name %q: only letters, digits, '-', and '_' are allowed", name)
	}
	return nil
}

// ValidateKBAliases rejects an alias list the server could not resolve
// unambiguously: every diagnostic names the item.
func ValidateKBAliases(aliases []KBAlias) error {
	seen := make(map[string]bool, len(aliases))
	for i, a := range aliases {
		if err := ValidateKBName(a.From); err != nil {
			return fmt.Errorf("kb_aliases[%d].from: %w", i, err)
		}
		if err := ValidateKBName(a.To); err != nil {
			return fmt.Errorf("kb_aliases[%d].to: %w", i, err)
		}
		if a.From == a.To {
			return fmt.Errorf("kb_aliases[%d]: %q is aliased to itself", i, a.From)
		}
		if seen[a.From] {
			return fmt.Errorf("kb_aliases[%d]: duplicate from %q", i, a.From)
		}
		seen[a.From] = true
	}
	return nil
}
