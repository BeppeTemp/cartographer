package lint

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// Conformance checks (D289): lint compares a KB with the standard fields the
// server reads, not only with the contracts the KB declared for itself. They
// are all warning or info, so no gate turns red on a KB that predates them.

// Fix kinds a Finding may carry. A fix is mechanical: applying it needs no
// judgement, so a repair tool never parses the message.
const (
	FixRenameField  = "rename_field"   // Field → To
	FixDropField    = "drop_field"     // Field
	FixRebaseLink   = "rebase_link"    // Field = old href, To = new href (D295 WP2)
	FixDropLinkItem = "drop_link_item" // Field = the exact list line to remove from the links section (D295 WP4)
)

// Fix is the machine-readable remedy of a finding whose repair is mechanical.
type Fix struct {
	Kind  string `json:"kind"`
	Field string `json:"field"`
	To    string `json:"to,omitempty"`
}

// FixableChecks are the checks whose findings carry a Fix, which is what
// kb_repair accepts (D290). A check that gains a Fix is added here: the list
// is the repair tool's contract, and a test pins it to what the checks emit.
var FixableChecks = []string{"broken_link", "duplicate_link", "nonstandard_field", "tool_param_field"}

// StandardFieldSynonyms maps each standard frontmatter field to the synonyms
// KBs are known to use for it (nonstandard_field). Keys are matched
// case-insensitively. docs/data-plane.md lists every entry: the table is the
// documentation, so a new entry needs its line there. No synonym may appear
// under two standard fields.
var StandardFieldSynonyms = map[string][]string{
	"timestamp":     {"updated", "updated_at", "last_updated", "modified", "date", "aggiornato", "data"},
	"provenance":    {"sources", "source", "refs", "fonti", "fonte"},
	"status":        {"state", "stato"},
	"description":   {"summary", "sommario"},
	"tags":          {"keywords", "parole_chiave"},
	"review_after":  {"review_by"},
	"superseded_by": {"replaced_by"},
}

// synonymOf is StandardFieldSynonyms inverted: synonym → standard field.
var synonymOf = func() map[string]string {
	m := map[string]string{}
	for std, syns := range StandardFieldSynonyms {
		for _, s := range syns {
			m[s] = std
		}
	}
	return m
}()

// ToolParamFields are the parameter names of the concept-write tools
// (concept_write, concept_new, concept_patch, concept_batch and its operation
// entries). A frontmatter key with one of these names is an agent that passed
// a tool argument inside the frontmatter object: never intentional, so it is
// reported (tool_param_field) and rejected on write. A test in mcpserver fails
// when a schema property is missing here. "id" carries no frontmatter meaning
// anywhere in the code base (D289), so it is included.
var ToolParamFields = []string{
	"id", "frontmatter", "body", "if_match",
	"template", "vars",
	"old_string", "new_string", "replace_all", "edits",
	"operations", "op",
}

// IsToolParamField reports whether key is a concept-write tool parameter name.
func IsToolParamField(key string) bool {
	for _, p := range ToolParamFields {
		if p == key {
			return true
		}
	}
	return false
}

// Thresholds of missing_value_contract.
const (
	valueContractMinConcepts = 5
	valueContractMaxValues   = 8
	valueContractTypedShare  = 0.9
)

// conceptInput is what the frontmatter-driven checks of one concept need.
type conceptInput struct {
	RelPath        string
	Body           string
	FrontmatterRaw string           // raw frontmatter block (between ---), for malformed detection
	Parsed         *okf.Frontmatter // nil when the concept has no readable frontmatter
	AllowPrefixes  []string
	Registry       registryLint
	MapName        string          // "" outside a map
	Contract       *kb.MapContract // nil outside a map
}

// frontmatterFindings computes the checks that depend only on one concept's
// own frontmatter, body and map contract: stale_claim, machine_path,
// missing_title, the map field contract and the two conformance checks. Run and
// CheckConcept both call it, so there is one implementation. It applies no
// lint_ignore: the caller does.
func frontmatterFindings(in conceptInput) []Finding {
	var out []Finding
	parsed := in.Parsed

	// --- malformed_frontmatter (warning, D295 WP5) ---
	// A scalar value followed by indented lines that look like block-list
	// continuations: the stdlib-only parser (D8) silently truncates the value.
	if in.FrontmatterRaw != "" {
		out = append(out, detectMalformedFrontmatter(in.RelPath, in.FrontmatterRaw)...)
	}

	// --- stale_claim (warning) ---
	if parsed != nil {
		if raVal, ok := parsed.Get("review_after"); ok {
			if dateStr, ok := raVal.(string); ok {
				t, parseErr := time.Parse("2006-01-02", dateStr)
				if parseErr == nil && t.Before(Now()) {
					out = append(out, Finding{
						Path:     in.RelPath,
						Check:    "stale_claim",
						Severity: SevWarning,
						Message:  fmt.Sprintf("review_after %s is in the past", dateStr),
					})
				}
			}
		}
	}

	// --- machine_path (warning, D75 WP6 / D124) ---
	if disallowed := firstDisallowedMachinePath(in.Body, in.AllowPrefixes); disallowed != "" {
		msg := fmt.Sprintf("client-local path %q — use {{repo:<key>}}/{{path:<nome>}} instead (D75); operational paths on containers/remote hosts are not client-local (D124)", disallowed)
		// A declared key whose default covers the path is the answer,
		// not a generic hint (D263).
		if s := in.Registry.suggestion(disallowed); s != "" {
			msg += fmt.Sprintf(" — use `%s`, declared in %s", s, kb.PathRegistryFile)
		}
		out = append(out, Finding{Path: in.RelPath, Check: "machine_path", Severity: SevWarning, Message: msg})
	}

	// --- missing_title (warning) ---
	// validate only requires a type, yet the title is the label concept_list,
	// search results and curated indexes show: an untitled concept is listed
	// with an empty one. The first H1 is the value an author almost always
	// meant, so the message offers it.
	if parsed == nil || emptyFrontmatterValue(frontmatterValue(parsed, "title")) {
		msg := "no title in frontmatter: concept_list and search show it with an empty label"
		if h1 := firstH1(in.Body); h1 != "" {
			msg += fmt.Sprintf(" — suggested: title: %q (its first heading)", h1)
		}
		out = append(out, Finding{Path: in.RelPath, Check: "missing_title", Severity: SevWarning, Message: msg})
	}

	// --- missing_required_field / invalid_field_value / forbidden_field ---
	if in.Contract != nil {
		conceptType := ""
		if parsed != nil {
			conceptType = parsed.Type()
		}
		for _, field := range in.Contract.RequiredFor(conceptType) {
			missing := parsed == nil
			if !missing {
				value, exists := parsed.Get(field)
				missing = !exists || emptyFrontmatterValue(value)
			}
			if missing {
				out = append(out, Finding{
					Path:     in.RelPath,
					Check:    "missing_required_field",
					Severity: SevError,
					Message:  fmt.Sprintf("missing required field %q required by map %q", field, in.MapName),
				})
			}
		}
		out = append(out, mapFieldContractFindings(in.RelPath, in.MapName, *in.Contract, parsed)...)
	}

	if parsed != nil {
		out = append(out, nonstandardFieldFindings(in)...)
		out = append(out, toolParamFieldFindings(in.RelPath, parsed)...)
	}
	return out
}

// nonstandardFieldFindings reports each frontmatter key that is a known
// synonym of a standard field (nonstandard_field, warning).
func nonstandardFieldFindings(in conceptInput) []Finding {
	var out []Finding
	conceptType := in.Parsed.Type()
	for _, key := range in.Parsed.Keys() {
		std, ok := synonymOf[strings.ToLower(key)]
		if !ok {
			continue
		}
		// A timestamp synonym such as "data" or "date" is an ordinary English
		// word: only a date-shaped value makes it a stand-in for the timestamp.
		if std == "timestamp" {
			if v, _ := in.Parsed.Get(key); !dateShaped(v) {
				continue
			}
		}
		f := Finding{Path: in.RelPath, Check: "nonstandard_field", Severity: SevWarning}
		if _, both := in.Parsed.Get(std); both {
			f.Message = fmt.Sprintf("has both %q and the standard field %q — merge the values by hand and drop %q", key, std, key)
		} else {
			f.Message = fmt.Sprintf("uses %q for the standard field %q — rename it, so the tools that read %q see it", key, std, std)
			f.Fix = &Fix{Kind: FixRenameField, Field: key, To: std}
		}
		if in.Contract != nil {
			for _, req := range in.Contract.RequiredFor(conceptType) {
				if req == key {
					f.Message += fmt.Sprintf("; map %q requires %q in its contract too", in.MapName, key)
					break
				}
			}
		}
		out = append(out, f)
	}
	return out
}

// toolParamFieldFindings reports each frontmatter key named like a concept-write
// tool parameter (tool_param_field, warning, drop_field). Not suppressible.
func toolParamFieldFindings(relPath string, parsed *okf.Frontmatter) []Finding {
	var out []Finding
	for _, key := range parsed.Keys() {
		if !IsToolParamField(key) {
			continue
		}
		out = append(out, Finding{
			Path:     relPath,
			Check:    "tool_param_field",
			Severity: SevWarning,
			Message:  fmt.Sprintf("frontmatter key %q is a write-tool parameter, not a field — it was probably passed inside the frontmatter object by mistake", key),
			Fix:      &Fix{Kind: FixDropField, Field: key},
		})
	}
	return out
}

// CheckConcept computes the frontmatter-driven checks for one concept without
// walking the KB (D289): the ones a write response can return. lint_ignore is
// applied; errors are never suppressed. Graph checks are out of scope, they
// need the whole KB.
func CheckConcept(k *kb.KB, id okf.ConceptID, content string) []Finding {
	fmRaw, body, hasFM := okf.SplitFrontmatter(content)
	var parsed *okf.Frontmatter
	if hasFM {
		parsed, _ = okf.ParseFrontmatter(fmRaw)
	}
	in := conceptInput{RelPath: okf.IDToPath(id), Body: body, FrontmatterRaw: fmRaw, Parsed: parsed}
	in.Registry, _ = loadRegistryLint(k)
	if parts := strings.Split(string(id), "/"); len(parts) > 1 {
		if contract, err := k.ReadMapContract(parts[0]); err == nil {
			in.MapName, in.Contract = parts[0], &contract
			in.AllowPrefixes = contract.MachinePathAllowPrefixes
		}
	}
	ignores := lintIgnoreSet(parsed)
	var out []Finding
	for _, f := range frontmatterFindings(in) {
		if suppressed(f, ignores) {
			continue
		}
		out = append(out, f)
	}
	return out
}

// valueContractFindings reports, per map, the scalar fields that look like a
// vocabulary and have no field_values contract (missing_value_contract, info).
func valueContractFindings(mapName string, contract kb.MapContract, concepts map[okf.ConceptID]string) []Finding {
	type carrier struct{ typ, value string }
	byField := map[string][]carrier{}
	prefix := mapName + "/"
	for id, content := range concepts {
		if !strings.HasPrefix(string(id), prefix) {
			continue
		}
		fmRaw, _, ok := okf.SplitFrontmatter(content)
		if !ok {
			continue
		}
		parsed, _ := okf.ParseFrontmatter(fmRaw)
		if parsed == nil {
			continue
		}
		for _, key := range parsed.Keys() {
			if v, isStr := frontmatterValue(parsed, key).(string); isStr && strings.TrimSpace(v) != "" {
				byField[key] = append(byField[key], carrier{parsed.Type(), strings.TrimSpace(v)})
			}
		}
	}
	skip := map[string]bool{
		"title": true, "type": true, "description": true, "timestamp": true,
		"review_after": true, "superseded_by": true, "lint_ignore": true,
		// D295 WP3: free-form, source-like and list-valued fields are never
		// vocabulary candidates.
		"provenance": true, "tags": true, "resource": true, "secrets_source": true,
	}
	var fields []string
	for f := range byField {
		fields = append(fields, f)
	}
	sort.Strings(fields)
	var out []Finding
	for _, field := range fields {
		if skip[field] {
			continue
		}
		if _, syn := synonymOf[strings.ToLower(field)]; syn {
			continue
		}
		carriers := byField[field]
		if len(carriers) < valueContractMinConcepts {
			continue
		}
		counts, types := map[string]int{}, map[string]int{}
		for _, c := range carriers {
			counts[c.value]++
			types[c.typ]++
		}
		// D295 WP3: status is always eligible (vocabulary every reader
		// assumes), with no distinct-value cap.
		if field != "status" && len(counts) > valueContractMaxValues {
			continue
		}
		dominant := ""
		for t, n := range types {
			if n > types[dominant] || (n == types[dominant] && t < dominant) {
				dominant = t
			}
		}
		typed := dominant != "" && float64(types[dominant])/float64(len(carriers)) >= valueContractTypedShare
		if _, declared := contract.FieldValues[field]; declared {
			continue
		}
		if _, declared := contract.FieldValuesByType[dominant][field]; declared && dominant != "" {
			continue
		}
		values := make([]string, 0, len(counts))
		for v := range counts {
			values = append(values, v)
		}
		sort.Slice(values, func(i, j int) bool {
			if counts[values[i]] != counts[values[j]] {
				return counts[values[i]] > counts[values[j]]
			}
			return values[i] < values[j]
		})
		observed := make([]string, len(values))
		for i, v := range values {
			observed[i] = fmt.Sprintf("%s ×%d", v, counts[v])
		}
		sort.Strings(values)
		key := "field_values." + field
		if typed {
			key = "field_values." + dominant + "." + field
		}
		out = append(out, Finding{
			Path:     mapName + "/_map.md",
			Check:    "missing_value_contract",
			Severity: SevInfo,
			Message: fmt.Sprintf("field %q takes %d distinct value(s) across %d concepts (%s) and has no value contract — declare it in _map.md: %s: [%s]",
				field, len(counts), len(carriers), strings.Join(observed, ", "), key, strings.Join(values, ", ")),
		})
	}
	return out
}

// suppressed reports whether a concept's lint_ignore silences f. Errors never
// are, and neither is tool_param_field: a concept cannot declare a tool
// argument a legitimate field.

// detectMalformedFrontmatter checks for a scalar value followed by indented
// block-list-like lines that the OKF parser (D8) silently ignores, truncating
// the value. The shape: `key: "- a"` then `  - b` (indented, unquoted).
func detectMalformedFrontmatter(relPath, fmRaw string) []Finding {
	lines := strings.Split(strings.ReplaceAll(fmRaw, "\r\n", "\n"), "\n")
	var out []Finding
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		// Only a top-level key can own a value; an indented or list line is
		// itself a continuation and is reported under its key.
		if line[0] == ' ' || line[0] == '\t' || line[0] == '-' {
			continue
		}
		colonIdx := strings.Index(line, ":")
		if colonIdx < 0 {
			continue
		}
		key := strings.TrimSpace(line[:colonIdx])
		valueRaw := strings.TrimSpace(line[colonIdx+1:])
		// Only scalar values: not empty (block list), not flow list.
		if valueRaw == "" || strings.HasPrefix(valueRaw, "[") || strings.HasPrefix(valueRaw, "|") || strings.HasPrefix(valueRaw, ">") {
			continue
		}
		// Check if the next non-blank line is indented and starts with "- ".
		for j := i + 1; j < len(lines); j++ {
			next := lines[j]
			if strings.TrimSpace(next) == "" {
				continue
			}
			// Indented (leading spaces) and starts with "- " after trimming.
			if len(next) > 0 && (next[0] == ' ' || next[0] == '\t') && strings.HasPrefix(strings.TrimSpace(next), "- ") {
				out = append(out, Finding{
					Path:     relPath,
					Check:    "malformed_frontmatter",
					Severity: SevWarning,
					Message:  fmt.Sprintf("key %q has a scalar value followed by indented list lines (line %d): the parser silently truncates the value", key, j+1),
				})
			}
			break // only check the immediately following non-blank line
		}
	}
	return out
}

func suppressed(f Finding, ignores map[string]bool) bool {
	return f.Severity != SevError && f.Check != "tool_param_field" && f.Check != "malformed_frontmatter" && ignores[f.Check]
}

// dateShaped reports whether v is a scalar string that parses as YYYY-MM-DD or
// RFC3339. Anything else (other text, a list) is not a timestamp.
func dateShaped(v interface{}) bool {
	str, ok := v.(string)
	if !ok {
		return false
	}
	str = strings.TrimSpace(str)
	if _, err := time.Parse("2006-01-02", str); err == nil {
		return true
	}
	_, err := time.Parse(time.RFC3339, str)
	return err == nil
}
