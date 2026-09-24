package lint

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// The path placeholder registry checks (D263). All of them are opt-in by
// presence: a KB with no paths.yaml gets none of these findings, so upgrading
// does not flood an existing KB with warnings about a file it never wrote.

// registryLint is what the checks need from the KB's paths.yaml.
type registryLint struct {
	state kb.PathRegistryState
	// enforced is false when there is no registry to check against — no
	// file, or one that is not a YAML mapping at all. An unparseable file
	// makes every key look undeclared, which is noise, not a finding: it is
	// reported once as contract_malformed instead.
	enforced bool
}

func loadRegistryLint(k *kb.KB) (registryLint, []Finding) {
	st, err := k.ReadPathRegistry()
	if err != nil {
		return registryLint{}, []Finding{{
			Path:     kb.PathRegistryFile,
			Check:    "contract_malformed",
			Severity: SevInfo,
			Message:  fmt.Sprintf("path placeholder registry unreadable: %v", err),
		}}
	}
	var findings []Finding
	for _, m := range st.Malformed {
		msg := "malformed path placeholder registry"
		if m.Entry != "" {
			msg += fmt.Sprintf(" entry %q", m.Entry)
		}
		findings = append(findings, Finding{
			Path:     kb.PathRegistryFile,
			Check:    "contract_malformed",
			Severity: SevInfo,
			Message:  msg + ": " + m.Reason,
		})
	}
	return registryLint{state: st, enforced: st.Present && !st.Unparseable}, findings
}

// undeclared returns the "kind:key" ids a concept cites that the registry
// does not declare under the matching kind.
func (r registryLint) undeclared(ids []string) []string {
	if !r.enforced {
		return nil
	}
	var out []string
	for _, id := range ids {
		kind, key, ok := okf.SplitPlaceholderID(id)
		if !ok {
			continue
		}
		if _, declared := r.state.Registry.Declared(kind, key); !declared {
			out = append(out, id)
		}
	}
	return out
}

// unknownPlaceholderFinding is the per-concept warning: one finding naming
// every undeclared key, not one per key.
func unknownPlaceholderFinding(relPath string, ids []string) Finding {
	quoted := make([]string, len(ids))
	for i, id := range ids {
		quoted[i] = "{{" + id + "}}"
	}
	return Finding{
		Path:     relPath,
		Check:    "unknown_placeholder",
		Severity: SevWarning,
		Message: fmt.Sprintf("cites %s, not declared in %s — reuse a declared key, or declare this one in the same change (D263)",
			strings.Join(quoted, ", "), kb.PathRegistryFile),
	}
}

// unused returns the declared ids that nothing cites, given every id cited by
// a concept or an artifact.
func (r registryLint) unused(cited map[string]bool) []string {
	if !r.enforced {
		return nil
	}
	var out []string
	for _, id := range r.state.Registry.IDs() {
		if !cited[id] {
			out = append(out, id)
		}
	}
	return out
}

// homeAnchorRe matches the home-directory head of a machine_path candidate:
// the forms machinePathRe flags, reduced to "~/" so a registry default can be
// compared with them whatever user wrote the concept.
var homeAnchorRe = regexp.MustCompile(`^(?:/Users/[^/]+|/home/[^/]+|C:\\Users\\[^\\]+|~)(?:[/\\]|$)`)

// suggestion returns the placeholder form of a flagged machine path when a
// declared default is its prefix (e.g. ~/.claude/settings.json with
// claude-home: ~/.claude -> "{{path:claude-home}}/settings.json"), or "" when
// none is. The longest matching default wins.
func (r registryLint) suggestion(flagged string) string {
	if !r.enforced {
		return ""
	}
	loc := homeAnchorRe.FindStringIndex(flagged)
	if loc == nil {
		return ""
	}
	rest := strings.ReplaceAll(flagged[loc[1]:], `\`, "/")
	normalized := "~/" + rest
	best, bestLen := "", -1
	consider := func(kind string, decls map[string]kb.PathDecl) {
		keys := make([]string, 0, len(decls))
		for k := range decls {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, key := range keys {
			def := decls[key].Default
			if def == "" {
				continue
			}
			if tail, ok := strings.CutPrefix(def, "$HOME"); ok {
				def = "~" + tail
			}
			def = strings.TrimSuffix(def, "/")
			var tail string
			switch {
			case def == "~":
				tail = "/" + rest
			case pathHasPrefix(normalized, def):
				tail = normalized[len(def):]
			default:
				continue
			}
			if len(def) > bestLen {
				best, bestLen = "{{"+kind+":"+key+"}}"+tail, len(def)
			}
		}
	}
	consider("path", r.state.Registry.Paths)
	consider("repo", r.state.Registry.Repos)
	return best
}
