package clientconfig

import (
	"fmt"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/gitx"
)

// IdentityDefault is the identities key for every KB without an entry of its
// own. A KB named "default" shares it.
const IdentityDefault = "default"

// Identity is the configured author of a write to kb, as "Name <email>"
// (D904): the KB's own identities entry, else the default one. found is false
// when neither is present, and only then may the caller ask git. A present
// entry always decides: an empty or blank one means no author at all, and so
// does an invalid one, with problem saying why, so a typo never lets another
// identity reach a KB the entry was meant to keep apart.
func (c *Config) Identity(kb string) (value string, found bool, problem string) {
	v, ok := "", false
	if kb != "" {
		v, ok = c.Identities[kb]
	}
	if !ok {
		v, ok = c.Identities[IdentityDefault]
	}
	if !ok {
		return "", false, ""
	}
	if strings.TrimSpace(v) == "" {
		return "", true, ""
	}
	name, email, problem := gitx.ParseIdent(v)
	if problem != "" {
		return "", true, problem
	}
	return name + " <" + email + ">", true, ""
}

// identityWarnings names each identities entry that is not a valid identity:
// it sends no author.
func (c *Config) identityWarnings() []string {
	keys := make([]string, 0, len(c.Identities))
	for k := range c.Identities {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []string
	for _, k := range keys {
		v := c.Identities[k]
		if strings.TrimSpace(v) == "" {
			continue
		}
		if _, _, problem := gitx.ParseIdent(v); problem != "" {
			out = append(out, fmt.Sprintf("identities.%s: %s, so writes to it send no author", k, problem))
		}
	}
	return out
}
