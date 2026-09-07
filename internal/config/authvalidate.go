package config

// Strict validation of the effective HTTP authentication configuration (D179).
//
// It runs on the CONFIGURATION THAT WILL ACTUALLY BE SERVED — after YAML,
// environment and flags have been merged — because that is the only shape whose
// mistakes reach a listener. Validating only the YAML left every
// environment- or flag-supplied token unchecked.
//
// The rule throughout: an invalid declaration is an error, never a silently
// narrower or wider interpretation. Every diagnostic names the offending token
// by index or principal ID and the category of the problem, and never contains a
// token value or a raw scope string — a scope field is where a credential gets
// pasted by mistake, and startup output is where it must not appear.

import (
	"fmt"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/auth"
)

// ValidateAuth rejects an auth configuration that would grant more than the
// operator wrote. It is called for every mode, including "off": a latent typo
// must not become an exposure on the day someone enables authentication.
func ValidateAuth(a AuthConfig) error {
	switch a.Mode {
	case "", "auto", "on", "off":
	default:
		return fmt.Errorf("mode %q: want \"auto\", \"on\" or \"off\"", a.Mode)
	}
	if err := ValidateAuthRoles(a); err != nil {
		return err
	}
	for i, tok := range a.Tokens {
		where := fmt.Sprintf("token %d", i)
		if tok.ID != "" {
			where = fmt.Sprintf("token %q", tok.ID)
		}
		if strings.TrimSpace(tok.Token) == "" {
			// resolveAuth counts configured records while the store keeps only
			// usable ones; an empty credential is exactly where those two
			// numbers diverged, and a store that silently ended up empty
			// stopped enforcing.
			return fmt.Errorf("%s: empty token value", where)
		}
		for _, scope := range tok.Scopes {
			if _, err := auth.ParseScopesStrict(scope); err != nil {
				return fmt.Errorf("%s: %w", where, err)
			}
		}
	}
	return nil
}
