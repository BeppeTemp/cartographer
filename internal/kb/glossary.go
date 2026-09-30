package kb

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"gopkg.in/yaml.v3"

	"github.com/BeppeTemp/cartographer/internal/search"
)

// GlossaryFile is the KB-root file in which a KB declares its terminology
// (D276): each term's canonical form, the aliases that mean the same thing,
// and the forbidden forms a page should no longer use. search expands a query
// through the aliases, lint flags the forbidden forms. Like paths.yaml it is
// data consumed by tools, KB-only, never materialized on a client.
const GlossaryFile = "glossary.yaml"

// glossaryTermMaxBytes bounds one term string: a term is a name, not a
// sentence, and a query variant is built by substituting it.
const glossaryTermMaxBytes = 80

// GlossaryTerm is one group of glossary.yaml. Strings are kept as authored;
// every comparison goes through search.Fold.
type GlossaryTerm struct {
	Canonical string
	Aliases   []string
	Forbidden []string
}

// Glossary is the parsed content of glossary.yaml, terms in file order.
type Glossary struct {
	Terms []GlossaryTerm
}

// GlossaryMalformed is one entry of glossary.yaml left out of the parsed
// glossary, and why. Entry is "terms[<i>]", a top-level key, or "" for the
// file as a whole.
type GlossaryMalformed struct {
	Entry  string
	Reason string
}

func (m GlossaryMalformed) String() string {
	if m.Entry == "" {
		return GlossaryFile + ": " + m.Reason
	}
	return GlossaryFile + ": " + m.Entry + ": " + m.Reason
}

// glossaryTermFields are the only keys a term accepts: a misspelt `alias:`
// is rejected rather than read as a group with no aliases.
var glossaryTermFields = map[string]bool{"canonical": true, "aliases": true, "forbidden": true}

// ParseGlossary parses glossary.yaml tolerantly, the stance of
// ParsePathRegistry: every well-formed term is returned, every malformed one
// is left out and listed. A term that reuses a folded string an earlier term
// already claimed is the malformed one, so the file's first word wins. err is
// non-nil only when the file is not a YAML mapping at all.
func ParseGlossary(data []byte) (Glossary, []GlossaryMalformed, error) {
	var g Glossary
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return g, nil, fmt.Errorf("%s: not valid YAML: %v", GlossaryFile, err)
	}
	if root.Kind == 0 || len(root.Content) == 0 {
		return g, nil, nil
	}
	doc := root.Content[0]
	if doc.Kind == yaml.ScalarNode && doc.Tag == "!!null" {
		return g, nil, nil
	}
	if doc.Kind != yaml.MappingNode {
		return g, nil, fmt.Errorf("%s: top level must be a mapping with terms:", GlossaryFile)
	}
	var bad []GlossaryMalformed
	seenTerms := false
	// claimed maps a folded string to the canonical of the term that owns it.
	claimed := map[string]string{}
	for i := 0; i+1 < len(doc.Content); i += 2 {
		k, v := doc.Content[i], doc.Content[i+1]
		if k.Value != "terms" {
			bad = append(bad, GlossaryMalformed{Entry: k.Value, Reason: "unknown top-level field (expected terms)"})
			continue
		}
		if seenTerms {
			bad = append(bad, GlossaryMalformed{Entry: "terms", Reason: "declared twice"})
			continue
		}
		seenTerms = true
		if v.Kind == yaml.ScalarNode && v.Tag == "!!null" {
			continue
		}
		if v.Kind != yaml.SequenceNode {
			bad = append(bad, GlossaryMalformed{Entry: "terms", Reason: "must be a list of {canonical, aliases, forbidden}"})
			continue
		}
		for j, entry := range v.Content {
			name := fmt.Sprintf("terms[%d]", j)
			term, reason := parseGlossaryTerm(entry)
			if reason == "" {
				reason = claimGlossaryTerm(term, claimed)
			}
			if reason != "" {
				bad = append(bad, GlossaryMalformed{Entry: name, Reason: reason})
				continue
			}
			g.Terms = append(g.Terms, term)
		}
	}
	return g, bad, nil
}

// parseGlossaryTerm validates one term's own shape; a non-empty reason means
// it is malformed.
func parseGlossaryTerm(entry *yaml.Node) (GlossaryTerm, string) {
	if entry.Kind != yaml.MappingNode {
		return GlossaryTerm{}, "must be a mapping with at least canonical:"
	}
	var t GlossaryTerm
	seen := map[string]bool{}
	for i := 0; i+1 < len(entry.Content); i += 2 {
		f, v := entry.Content[i].Value, entry.Content[i+1]
		if !glossaryTermFields[f] {
			return GlossaryTerm{}, fmt.Sprintf("unknown field %q", f)
		}
		if seen[f] {
			return GlossaryTerm{}, fmt.Sprintf("field %q declared twice", f)
		}
		seen[f] = true
		if f == "canonical" {
			if v.Kind != yaml.ScalarNode || v.Tag == "!!null" {
				return GlossaryTerm{}, "canonical must be a string"
			}
			s, reason := glossaryTermString(v.Value)
			if reason != "" {
				return GlossaryTerm{}, "canonical " + reason
			}
			t.Canonical = s
			continue
		}
		if v.Kind == yaml.ScalarNode && v.Tag == "!!null" {
			continue
		}
		if v.Kind != yaml.SequenceNode {
			return GlossaryTerm{}, fmt.Sprintf("field %q must be a list of strings", f)
		}
		var list []string
		for _, item := range v.Content {
			if item.Kind != yaml.ScalarNode || item.Tag == "!!null" {
				return GlossaryTerm{}, fmt.Sprintf("field %q must be a list of strings", f)
			}
			s, reason := glossaryTermString(item.Value)
			if reason != "" {
				return GlossaryTerm{}, fmt.Sprintf("%s: %q %s", f, item.Value, reason)
			}
			list = append(list, s)
		}
		if f == "aliases" {
			t.Aliases = list
		} else {
			t.Forbidden = list
		}
	}
	if !seen["canonical"] {
		return GlossaryTerm{}, "canonical is required"
	}
	return t, ""
}

func glossaryTermString(raw string) (string, string) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", "must not be empty"
	}
	if len(s) > glossaryTermMaxBytes {
		return "", fmt.Sprintf("is longer than %d bytes", glossaryTermMaxBytes)
	}
	return s, ""
}

// claimGlossaryTerm checks the cross-term invariants and, when they hold,
// records every folded string of t as belonging to it. A folded string may be
// repeated inside one group's canonical and aliases (harmless), but may not be
// both a member and forbidden, nor belong to two canonicals.
func claimGlossaryTerm(t GlossaryTerm, claimed map[string]string) string {
	own := map[string]bool{}
	members := map[string]bool{search.Fold(t.Canonical): true}
	for _, a := range t.Aliases {
		members[search.Fold(a)] = true
	}
	for _, f := range t.Forbidden {
		if members[search.Fold(f)] {
			return fmt.Sprintf("%q is both an alias and forbidden", f)
		}
	}
	all := append([]string{t.Canonical}, t.Aliases...)
	all = append(all, t.Forbidden...)
	for _, s := range all {
		if owner, ok := claimed[search.Fold(s)]; ok && !own[search.Fold(s)] {
			return fmt.Sprintf("%q already belongs to the term %q", s, owner)
		}
		own[search.Fold(s)] = true
	}
	for _, s := range all {
		claimed[search.Fold(s)] = t.Canonical
	}
	return ""
}

// ValidateGlossary is the strict form artifact_write applies: any malformed
// entry rejects the whole file, every reason named.
func ValidateGlossary(data []byte) error {
	_, bad, err := ParseGlossary(data)
	if err != nil {
		return err
	}
	if len(bad) == 0 {
		return nil
	}
	msgs := make([]string, len(bad))
	for i, m := range bad {
		msgs[i] = m.String()
	}
	return errors.New(strings.Join(msgs, "; "))
}

// GlossaryState is what ReadGlossary found at the KB root; the fields mean
// what PathRegistryState's do.
type GlossaryState struct {
	Present     bool
	Unparseable bool
	Glossary    Glossary
	Malformed   []GlossaryMalformed
}

// ReadGlossary reads the KB's glossary.yaml tolerantly (see ParseGlossary).
// A symlinked glossary.yaml is an error, as every KB-root artifact is (D148).
func (kb *KB) ReadGlossary() (GlossaryState, error) {
	path := filepath.Join(kb.Root, GlossaryFile)
	fi, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return GlossaryState{}, nil
	}
	if err != nil {
		return GlossaryState{}, err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return GlossaryState{}, fmt.Errorf("%s: symlink not allowed", GlossaryFile)
	}
	if !fi.Mode().IsRegular() {
		return GlossaryState{}, fmt.Errorf("%s: not a regular file", GlossaryFile)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return GlossaryState{}, err
	}
	g, bad, perr := ParseGlossary(data)
	if perr != nil {
		msg := strings.TrimPrefix(perr.Error(), GlossaryFile+": ")
		return GlossaryState{Present: true, Unparseable: true, Malformed: []GlossaryMalformed{{Reason: msg}}}, nil
	}
	return GlossaryState{Present: true, Glossary: g, Malformed: bad}, nil
}

// isGlossaryWordRune is what a term may not touch on a side where it starts
// or ends with a word rune: "ha" must not match inside "sha" or "ha2".
func isGlossaryWordRune(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

// containsWholeWord reports whether the folded text contains the folded
// phrase at word boundaries. A boundary is only required on a side where the
// phrase itself ends in a word rune, so "c++" still matches "c++ build".
func containsWholeWord(text, phrase string) bool {
	return len(wholeWordIndexes(text, phrase)) > 0
}

// wholeWordIndexes returns the byte offsets of every non-overlapping
// whole-word occurrence of phrase in text.
func wholeWordIndexes(text, phrase string) []int {
	if phrase == "" {
		return nil
	}
	first, _ := utf8.DecodeRuneInString(phrase)
	last, _ := utf8.DecodeLastRuneInString(phrase)
	var out []int
	for from := 0; from <= len(text)-len(phrase); {
		i := strings.Index(text[from:], phrase)
		if i < 0 {
			break
		}
		start, end := from+i, from+i+len(phrase)
		ok := true
		if isGlossaryWordRune(first) && start > 0 {
			prev, _ := utf8.DecodeLastRuneInString(text[:start])
			ok = !isGlossaryWordRune(prev)
		}
		if ok && isGlossaryWordRune(last) && end < len(text) {
			next, _ := utf8.DecodeRuneInString(text[end:])
			ok = !isGlossaryWordRune(next)
		}
		if ok {
			out = append(out, start)
			from = end
			continue
		}
		_, size := utf8.DecodeRuneInString(text[start:])
		from = start + size
	}
	return out
}

// replaceWholeWord replaces every whole-word occurrence of phrase in text.
func replaceWholeWord(text, phrase, with string) string {
	idx := wholeWordIndexes(text, phrase)
	if len(idx) == 0 {
		return text
	}
	var b strings.Builder
	prev := 0
	for _, i := range idx {
		b.WriteString(text[prev:i])
		b.WriteString(with)
		prev = i + len(phrase)
	}
	b.WriteString(text[prev:])
	return b.String()
}

// Variants returns the queries search runs for foldedQuery (D276): the query
// itself first, then, for every glossary group whose canonical or alias
// occurs in it as a whole-word phrase, the query with that phrase replaced by
// each other member of the group. Forbidden terms neither trigger nor appear
// in a variant. Duplicates are dropped, the list is capped at max, and a
// variant is never re-expanded. With no match the result is [foldedQuery].
func (g Glossary) Variants(foldedQuery string, max int) []string {
	out := []string{foldedQuery}
	seen := map[string]bool{foldedQuery: true}
	for _, t := range g.Terms {
		members := foldedMembers(t)
		// Longest first, so "home assistant" is replaced as a phrase before a
		// shorter member that happens to be one of its words.
		matched := make([]string, 0, len(members))
		for _, m := range members {
			if containsWholeWord(foldedQuery, m) {
				matched = append(matched, m)
			}
		}
		sort.SliceStable(matched, func(i, j int) bool { return len(matched[i]) > len(matched[j]) })
		for _, m := range matched {
			for _, other := range members {
				if other == m {
					continue
				}
				v := replaceWholeWord(foldedQuery, m, other)
				if seen[v] {
					continue
				}
				if len(out) >= max {
					return out
				}
				seen[v] = true
				out = append(out, v)
			}
		}
	}
	return out
}

// foldedMembers is a group's canonical and aliases, folded and deduplicated,
// canonical first.
func foldedMembers(t GlossaryTerm) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range append([]string{t.Canonical}, t.Aliases...) {
		f := search.Fold(s)
		if !seen[f] {
			seen[f] = true
			out = append(out, f)
		}
	}
	return out
}

// ForbiddenUse is one forbidden term a text uses, with the canonical form the
// glossary asks for instead.
type ForbiddenUse struct {
	Term      string
	Canonical string
}

// ForbiddenUses returns, in glossary order and once each, the forbidden terms
// text contains as whole words, compared folded. The caller masks what it
// does not want checked (code spans, for lint).
func (g Glossary) ForbiddenUses(text string) []ForbiddenUse {
	folded := search.Fold(text)
	var out []ForbiddenUse
	seen := map[string]bool{}
	for _, t := range g.Terms {
		for _, f := range t.Forbidden {
			ff := search.Fold(f)
			if seen[ff] {
				continue
			}
			seen[ff] = true
			if containsWholeWord(folded, ff) {
				out = append(out, ForbiddenUse{Term: f, Canonical: t.Canonical})
			}
		}
	}
	return out
}
