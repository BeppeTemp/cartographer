package lint

import (
	"fmt"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/search"
)

// Value fixes (D296).
const (
	FixSetValue   = "set_value"   // Field ← To
	FixSplitValue = "split_value" // Field ← To; the rest of the old value moves into the body
)

// ValueSynonymFamilies groups the status values KBs are known to use for the
// same state (D296). The family key only names the family: the canonical
// member is always the KB's — the one its contract declares, or the most
// frequent one observed. A value appears in at most one family (a test
// enforces it), and docs/data-plane.md lists every row. States that are
// distinct or KB-specific (open, decision-needed, reference, accepted,
// rejected, superseded, mitigated, monitoring) are deliberately in none. The
// table ships English plus the languages contributors add; a map contract
// extends it with value_synonyms.<canonical>: [...] in any language.
var ValueSynonymFamilies = map[string][]string{
	"done":        {"done", "completed", "complete", "completato", "completata", "finito", "finita", "chiuso", "chiusa"},
	"resolved":    {"resolved", "risolto", "risolta", "closed", "fixed"},
	"in-progress": {"in-progress", "in-corso", "wip", "ongoing", "doing"},
	"blocked":     {"blocked", "bloccato", "bloccata", "on-hold", "in-attesa", "waiting"},
	"proposed":    {"proposed", "proposto", "proposta"},
	"draft":       {"draft", "bozza"},
	"active":      {"active", "attivo", "attiva", "current"},
	"deprecated":  {"deprecated", "dismesso", "dismessa", "retired"},
	"suspended":   {"suspended", "sospeso", "sospesa"},
}

// NormValue folds a value for comparison: case and accents folded, trimmed,
// runs of whitespace and '_' turned into '-' ("In corso" ≡ "in-corso").
func NormValue(s string) string {
	f := search.Fold(strings.ToLower(strings.TrimSpace(s)))
	f = strings.ReplaceAll(f, "_", " ")
	return strings.Join(strings.Fields(f), "-")
}

// valueFamilies is the family index for one map: normalised member → family
// key, built from the table plus the contract's value_synonyms.
type valueFamilies map[string]string

func familiesFor(contract *kb.MapContract) valueFamilies {
	idx := valueFamilies{}
	for fam, members := range ValueSynonymFamilies {
		for _, m := range members {
			idx[NormValue(m)] = fam
		}
	}
	if contract == nil {
		return idx
	}
	canon := make([]string, 0, len(contract.ValueSynonyms))
	for c := range contract.ValueSynonyms {
		canon = append(canon, c)
	}
	sort.Strings(canon)
	for _, c := range canon {
		fam, ok := idx[NormValue(c)]
		if !ok {
			fam = "kb:" + NormValue(c)
			idx[NormValue(c)] = fam
		}
		for _, s := range contract.ValueSynonyms[c] {
			if _, taken := idx[NormValue(s)]; !taken {
				idx[NormValue(s)] = fam
			}
		}
	}
	return idx
}

// member returns the family of v, if any.
func (f valueFamilies) member(v string) (string, bool) {
	fam, ok := f[NormValue(v)]
	return fam, ok
}

// canonicalIn returns the single allowed value that is v up to normalisation
// or shares v's family. Two candidates (or none) mean no mechanical answer.
func (f valueFamilies) canonicalIn(v string, allowed []string) (string, bool) {
	var hits []string
	fam, inFam := f.member(v)
	for _, a := range allowed {
		if NormValue(a) == NormValue(v) {
			return a, true
		}
		if inFam {
			if af, ok := f.member(a); ok && af == fam {
				hits = append(hits, a)
			}
		}
	}
	if len(hits) == 1 {
		return hits[0], true
	}
	return "", false
}

// proseSeparators split a vocabulary token from the sentence an author
// appended to it.
var proseSeparators = []string{" — ", " – ", " - ", "; ", ": ", " (", ", "}

// SplitProse splits a value like "accepted — implemented in commit x" into
// its leading token and the remainder. ok is false when the value is a plain
// token: one separator-free run of at most 3 words.
func SplitProse(v string) (token, rest string, ok bool) {
	v = strings.TrimSpace(v)
	cut := -1
	sepLen := 0
	for _, sep := range proseSeparators {
		if i := strings.Index(v, sep); i > 0 && (cut < 0 || i < cut) {
			cut, sepLen = i, len(sep)
		}
	}
	if cut > 0 {
		rest = strings.TrimSpace(v[cut+sepLen:])
		if strings.HasPrefix(v[cut:], " (") {
			rest = strings.TrimSpace(v[cut+1:])
		}
		head := strings.TrimSpace(v[:cut])
		if words := strings.Fields(head); len(words) > 3 {
			// "baseline di piattaforma registrata; ..." — the head itself is
			// a sentence: the token is its first word.
			rest = strings.Join(words[1:], " ") + v[cut:]
			return words[0], strings.TrimSpace(rest), true
		}
		return head, rest, rest != ""
	}
	if words := strings.Fields(v); len(words) > 3 {
		return words[0], strings.Join(words[1:], " "), true
	}
	return v, "", false
}

// proseValueFindings reports a vocabulary field holding a sentence
// (prose_value, warning, D296): status always, plus every field the map
// contract constrains. The fix splits the token from the prose when the token
// is a contract value or a known family member; the prose is kept, never lost.
func proseValueFindings(in conceptInput) []Finding {
	if in.Parsed == nil {
		return nil
	}
	fams := familiesFor(in.Contract)
	fields := map[string]bool{"status": true}
	if in.Contract != nil {
		for f := range in.Contract.FieldValues {
			fields[f] = true
		}
		for _, byField := range in.Contract.FieldValuesByType {
			for f := range byField {
				fields[f] = true
			}
		}
	}
	names := make([]string, 0, len(fields))
	for f := range fields {
		names = append(names, f)
	}
	sort.Strings(names)
	var out []Finding
	for _, field := range names {
		raw, ok := in.Parsed.Get(field)
		v, isStr := raw.(string)
		if !ok || !isStr {
			continue
		}
		token, _, prose := SplitProse(v)
		if !prose {
			continue
		}
		f := Finding{Path: in.RelPath, Check: "prose_value", Severity: SevWarning,
			Message: fmt.Sprintf("field %q holds a sentence (%q): a vocabulary field is one token, so filters on it miss this concept", field, v)}
		var allowed []string
		if in.Contract != nil {
			allowed, _ = in.Contract.AllowedValues(in.Parsed.Type(), field)
		}
		to := ""
		if len(allowed) > 0 {
			to, _ = fams.canonicalIn(token, allowed)
		} else if _, member := fams.member(token); member {
			to = token
		}
		if to != "" {
			f.Fix = &Fix{Kind: FixSplitValue, Field: field, To: to}
			f.Message += fmt.Sprintf(" — fix: keep %q and move the rest into the body", to)
		}
		out = append(out, f)
	}
	return out
}

// Proposal is the structured vocabulary a missing_value_contract finding
// suggests (D296): the values to declare and how observed synonyms map onto
// them, so a doctor or the Atlas need not parse the message.
type Proposal struct {
	Key     string            `json:"key"`
	Field   string            `json:"field"`
	Type    string            `json:"type,omitempty"`
	Values  []string          `json:"values"`
	Mapping map[string]string `json:"mapping,omitempty"`
}

// foldVocabulary folds observed values onto one canonical member per family:
// the most frequent member, ties to the shorter, then lexical. Values in no
// family stay as they are.
func foldVocabulary(counts map[string]int, fams valueFamilies) (values []string, mapping map[string]string) {
	byFam := map[string][]string{}
	var loose []string
	for v := range counts {
		if fam, ok := fams.member(v); ok {
			byFam[fam] = append(byFam[fam], v)
		} else {
			loose = append(loose, v)
		}
	}
	mapping = map[string]string{}
	for _, members := range byFam {
		sort.Slice(members, func(i, j int) bool {
			a, b := members[i], members[j]
			if counts[a] != counts[b] {
				return counts[a] > counts[b]
			}
			if len(a) != len(b) {
				return len(a) < len(b)
			}
			return a < b
		})
		values = append(values, members[0])
		for _, m := range members[1:] {
			mapping[m] = members[0]
		}
	}
	values = append(values, loose...)
	sort.Strings(values)
	if len(mapping) == 0 {
		mapping = nil
	}
	return values, mapping
}
