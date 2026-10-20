package kb

import (
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/okf"
)

// StandardFieldSynonyms maps each standard frontmatter field to the synonyms
// KBs are known to use for it (nonstandard_field). Keys are matched
// case-insensitively. docs/data-plane.md lists every entry: the table is the
// documentation, so a new entry needs its line there. No synonym may appear
// under two standard fields. It lives here so that the page repair (lint's
// nonstandard_field) and the contract realignment rename by one table (D907).
var StandardFieldSynonyms = map[string][]string{
	"timestamp":     {"updated", "updated_at", "last_updated", "modified", "date", "aggiornato", "data"},
	"provenance":    {"sources", "source", "refs", "fonti", "fonte"},
	"status":        {"state", "stato"},
	"description":   {"summary", "sommario"},
	"tags":          {"keywords", "parole_chiave"},
	"review_after":  {"review_by"},
	"superseded_by": {"replaced_by"},
}

// SynonymOf is StandardFieldSynonyms inverted: synonym → standard field.
var SynonymOf = func() map[string]string {
	m := map[string]string{}
	for std, syns := range StandardFieldSynonyms {
		for _, s := range syns {
			m[s] = std
		}
	}
	return m
}()

// ContractFieldCanonical is the standard name of a field a map contract names
// when the safe page repair renames it (updated → timestamp). "date" and
// "data" are not rewritten in a contract: the page repair renames them only
// when the value is date-shaped, so a contract naming them may mean the word.
func ContractFieldCanonical(name string) (string, bool) {
	std, ok := SynonymOf[strings.ToLower(name)]
	if !ok {
		return "", false
	}
	if std == "timestamp" && (strings.EqualFold(name, "date") || strings.EqualFold(name, "data")) {
		return "", false
	}
	return std, true
}

// ContractSynonymKeys lists, sorted, the contract keys of a descriptor that
// name a field under a synonym: a required_fields.<type> item, or the <field> of a field_values.<type>.<field> (D907). The realignment
// rewrites them to the standard name. forbidden_fields is left alone: forbidding
// a synonym ("state") is a deliberate rule, not a stale name.
func ContractSynonymKeys(meta *okf.Frontmatter) []string {
	if meta == nil {
		return nil
	}
	var out []string
	for _, key := range meta.Keys() {
		switch {
		case strings.HasPrefix(key, "required_fields."):
			v, _ := meta.Get(key)
			list, _ := v.([]string)
			for _, f := range list {
				if _, ok := ContractFieldCanonical(f); ok {
					out = append(out, key)
					break
				}
			}
		case strings.HasPrefix(key, "field_values."):
			parts := strings.Split(strings.TrimPrefix(key, "field_values."), ".")
			if len(parts) == 2 {
				if _, ok := ContractFieldCanonical(parts[1]); ok {
					out = append(out, key)
				}
			}
		}
	}
	sort.Strings(out)
	return out
}

// canonicalizeContractFields rewrites, in place, the synonym field names of the
// contract keys ContractSynonymKeys lists.
func canonicalizeContractFields(fm *okf.Frontmatter) {
	for _, key := range ContractSynonymKeys(fm) {
		if strings.HasPrefix(key, "field_values.") {
			parts := strings.Split(strings.TrimPrefix(key, "field_values."), ".")
			std, _ := ContractFieldCanonical(parts[1])
			newKey := "field_values." + parts[0] + "." + std
			if _, has := fm.Get(newKey); has {
				fm.Delete(key)
			} else {
				fm.Rename(key, newKey)
			}
			continue
		}
		v, _ := fm.Get(key)
		list, _ := v.([]string)
		out := make([]string, 0, len(list))
		for _, f := range list {
			if std, ok := ContractFieldCanonical(f); ok {
				f = std
			}
			out = append(out, f)
		}
		fm.Set(key, sortedUnique(out))
	}
}
