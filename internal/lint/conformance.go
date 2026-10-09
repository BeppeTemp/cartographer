package lint

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

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
	// FixRewriteLinkItem: Field = the exact list line, To = that line without
	// the links the text already carries (D307). An item holding other links
	// keeps them; one left with none is dropped (FixDropLinkItem instead).
	FixRewriteLinkItem = "rewrite_link_item"
	// FixStripToolPrefix: Field = a pre-D288 prefixed tool name in an
	// artifact file, To = the bare name it becomes (D316). The path is the
	// artifact's, not a concept's.
	FixStripToolPrefix = "strip_tool_prefix"
	// FixReplacePrefix: Field = a legacy path prefix declared in
	// instructions.md, To = its replacement (D316). Every occurrence in the
	// body is rewritten; several on one concept apply longest first, in one
	// pass.
	FixReplacePrefix = "replace_prefix"
	// FixRewriteWikiLink: Field = the ID a wiki-link names, To = the ID it
	// becomes (index_link_form, D310). Alias and anchor stay as written.
	FixRewriteWikiLink = "rewrite_wiki_link"
	// FixListifyField: Field = a list-valued key stored as a string that
	// looks like a list (stringified_list, D314). The repair parses the
	// current value with ListItems and stores the items as a real list.
	FixListifyField = "listify_field"
	// FixSyncH1: To = the concept's frontmatter title (title_h1_mismatch,
	// D315). The repair overwrites the body's first level-1 heading with it:
	// the title is the source of truth, the H1 its rendering.
	FixSyncH1 = "sync_h1"
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
var FixableChecks = []string{"broken_link", "duplicate_link", "index_link_form", "invalid_field_value", "legacy_path", "legacy_tool_name", "nonstandard_field", "prose_value", "reciprocal_link_item", "stringified_list", "title_h1_mismatch", "tool_param_field"}

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
	"old_string", "new_string", "replace_all", "edits", "unset",
	"frontmatter_append", "frontmatter_remove",
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
	Sections       []string        // the H2 sections of the concept's template (D297)
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

	// --- stringified_list (warning, D314) ---
	if parsed != nil {
		out = append(out, detectStringifiedLists(in.RelPath, parsed)...)
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

	// --- status_semantics (warning, D321) ---
	if parsed != nil {
		if status, _ := frontmatterValue(parsed, "status").(string); activeNotOpen(status, in.Contract) {
			out = append(out, Finding{Path: in.RelPath, Check: "status_semantics", Severity: SevWarning,
				Message: fmt.Sprintf("status %q in a journal means the page is valid, not that work is open — use open, in-progress, blocked or another work status; if this journal reads it as open, list it in open_statuses", status)})
		}
	}

	// --- machine_path (warning, D75 WP6 / D124) ---
	if all := disallowedMachinePaths(in.Body, in.AllowPrefixes); len(all) > 0 {
		disallowed := all[0]
		msg := fmt.Sprintf("client-local path %q — use {{repo:<key>}}/{{path:<nome>}} instead (D75); operational paths on containers/remote hosts are not client-local (D124)", disallowed)
		// One finding per concept, but naming every path: fixing the first
		// must not be how the author learns about the second.
		if rest := all[1:]; len(rest) > 0 {
			shown := rest
			if len(shown) > 5 {
				shown = shown[:5]
			}
			msg += fmt.Sprintf(" — and %d more here: %s", len(rest), strings.Join(shown, ", "))
		}
		// A declared key whose default covers the path is the answer,
		// not a generic hint (D263).
		if s := in.Registry.suggestion(disallowed); s != "" {
			msg += fmt.Sprintf(" — use `%s`, declared in %s", s, kb.PathRegistryFile)
		}
		out = append(out, Finding{Path: in.RelPath, Check: "machine_path", Severity: SevWarning, Message: msg})
	}

	// --- mangled_placeholder (warning, D314) ---
	if f, ok := detectMangledPlaceholders(in.RelPath, in.Body); ok {
		out = append(out, f)
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

	// --- title_h1_mismatch (warning, D315) ---
	// Both exist and differ: the title is what listings, search and the
	// Atlas show, so the heading is the one that is wrong.
	if parsed != nil {
		title := titleOf(parsed)
		if h1 := firstH1(in.Body); title != "" && h1 != "" && h1 != title {
			out = append(out, Finding{
				Path:     in.RelPath,
				Check:    "title_h1_mismatch",
				Severity: SevWarning,
				Message:  fmt.Sprintf("title %q and first heading %q differ: the title is the label shown in concept_list, search and the Atlas; the heading should match", title, h1),
				Fix:      &Fix{Kind: FixSyncH1, To: title},
			})
		}
		out = append(out, titleQualityFindings(in, parsed, title)...)
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
		out = append(out, proseValueFindings(in)...)
		out = append(out, decayFindings(in, in.Sections)...)
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
			if contract.TemplateSections && parsed != nil {
				in.Sections = k.TemplateSections(parsed.Type())
			}
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
		"waiting_on": true, // free text (D321)
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
		// D313: a field whose every value is date-shaped (claimed_at, due) holds
		// instants, not a vocabulary: proposing one is noise nobody can accept.
		if field != "status" && allDateShaped(counts) {
			continue
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
		// D296: status folds its synonyms onto one member per family.
		var proposed []string
		var mapping map[string]string
		if field == "status" {
			// A prose value proposes its leading token (prose_value moves
			// the rest into the body).
			tokens := map[string]int{}
			proseTo := map[string]string{}
			for v, n := range counts {
				if tok, _, prose := SplitProse(v); prose {
					tokens[tok] += n
					proseTo[v] = tok
				} else {
					tokens[v] += n
				}
			}
			proposed, mapping = foldVocabulary(tokens, familiesFor(&contract))
			for v, tok := range proseTo {
				if mapping == nil {
					mapping = map[string]string{}
				}
				if canon, ok := mapping[tok]; ok {
					tok = canon
				}
				mapping[v] = tok
			}
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
		if proposed == nil {
			proposed = values
		}
		msg := fmt.Sprintf("field %q takes %d distinct value(s) across %d concepts (%s) and has no value contract — declare it in _map.md: %s: [%s]",
			field, len(counts), len(carriers), strings.Join(observed, ", "), key, strings.Join(proposed, ", "))
		if len(mapping) > 0 {
			from := make([]string, 0, len(mapping))
			for m := range mapping {
				from = append(from, m)
			}
			sort.Strings(from)
			pairs := make([]string, len(from))
			for i, m := range from {
				pairs[i] = fmt.Sprintf("%s→%s ×%d", m, mapping[m], counts[m])
			}
			msg += "; then kb_repair invalid_field_value converges " + strings.Join(pairs, ", ")
		}
		p := &Proposal{Key: key, Field: field, Values: proposed, Mapping: mapping}
		if typed {
			p.Type = dominant
		}
		out = append(out, Finding{
			Path:     mapName + "/_map.md",
			Check:    "missing_value_contract",
			Severity: SevInfo,
			Message:  msg,
			Proposal: p,
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
	return f.Severity != SevError && f.Check != "tool_param_field" && f.Check != "malformed_frontmatter" && f.Check != "stringified_list" && ignores[f.Check]
}

// dateShaped reports whether v is a scalar string that parses as YYYY-MM-DD or
// RFC3339. Anything else (other text, a list) is not a timestamp.
func allDateShaped(counts map[string]int) bool {
	for v := range counts {
		if !dateShaped(v) {
			return false
		}
	}
	return len(counts) > 0
}

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

// listFields are the frontmatter keys whose value is a list of strings. A
// string stored under one of them is the shape stringified_list reports.
var listFields = map[string]bool{
	"provenance": true, "tags": true, "related": true,
	"lint_ignore": true, "open": true, "secrets_source": true,
}

// listFieldOrder fixes the order findings are emitted in (map iteration is
// random, and lint output is compared by tests and diffed by people).
var listFieldOrder = []string{"provenance", "tags", "related", "lint_ignore", "open", "secrets_source"}

// looksStringified reports whether a string value was meant as a list: a
// bracketed flow list, several of them joined by "; " ("[a]; [b]"), or a
// block-list item that lost its siblings ("- a"). The OKF parser sends a
// quoted "[a, b]" to the scalar branch (a leading quote is not "["), so the
// value reaches lint as a string, not a []string (D314).
func looksStringified(v string) bool {
	v = strings.TrimSpace(v)
	if strings.HasPrefix(v, "- ") {
		return true
	}
	if strings.HasPrefix(v, "[") && strings.HasSuffix(v, "]") {
		return true
	}
	return strings.Contains(v, "; [")
}

// ListItems extracts the items of a stringified list: "[a, b]", "[a]; [b]"
// and "- a" all give their elements, trimmed of brackets, quotes and spaces.
func ListItems(v string) []string {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "- ")
	var items []string
	for _, group := range strings.Split(v, "; ") {
		group = strings.TrimSpace(group)
		group = strings.TrimPrefix(group, "[")
		group = strings.TrimSuffix(group, "]")
		for _, it := range strings.Split(group, ",") {
			it = strings.Trim(strings.TrimSpace(it), `"'`)
			if it = strings.TrimSpace(it); it != "" {
				items = append(items, it)
			}
		}
	}
	return items
}

// detectStringifiedLists implements stringified_list (D314): a list field
// whose parsed value is a string that looks like a list. The data type is
// wrong, and no other check sees it.
func detectStringifiedLists(relPath string, fm *okf.Frontmatter) []Finding {
	var out []Finding
	for _, key := range listFieldOrder {
		raw, ok := fm.Get(key)
		if !ok {
			continue
		}
		str, isStr := raw.(string)
		if !isStr || !looksStringified(str) {
			continue
		}
		out = append(out, Finding{
			Path:     relPath,
			Check:    "stringified_list",
			Severity: SevWarning,
			Message:  fmt.Sprintf("key %q is a string that looks like a list — rewrite it as a proper YAML list", key),
			Fix:      &Fix{Kind: FixListifyField, Field: key},
		})
	}
	return out
}

// mangledPlaceholderRe matches a placeholder an import unwrapped into prose:
// a `repo:key` / `path:key` code span followed, within 40 characters, by the
// words "between double braces" in one of the languages seen in the field.
var mangledPlaceholderRe = regexp.MustCompile("(?i)`(?:repo|path):[a-z][a-z0-9_-]*`.{0,40}?(?:fra doppie graffe|tra doppie graffe|between double braces|between double curly|in double braces|in double curly|entre doubles accolades)")

// detectMangledPlaceholders implements mangled_placeholder (D314): one finding
// per concept naming every match. Fenced blocks are skipped, and so is any
// line holding "{{": that is documentation of the syntax, not a casualty of it.
// MaskCodeSpans is deliberately not used: it would blank the very code span
// the pattern is anchored on.
func detectMangledPlaceholders(relPath, body string) (Finding, bool) {
	var matches []string
	fence := ""
	for _, line := range strings.Split(body, "\n") {
		t := strings.TrimSpace(line)
		if fence == "" {
			if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
				fence = t[:3]
				continue
			}
		} else {
			if strings.HasPrefix(t, fence) {
				fence = ""
			}
			continue
		}
		if strings.Contains(line, "{{") {
			continue
		}
		matches = append(matches, mangledPlaceholderRe.FindAllString(line, -1)...)
	}
	if len(matches) == 0 {
		return Finding{}, false
	}
	return Finding{
		Path:     relPath,
		Check:    "mangled_placeholder",
		Severity: SevWarning,
		Message:  fmt.Sprintf("body contains what looks like a placeholder rewritten as prose: %s — restore the {{…}} syntax", strings.Join(matches, "; ")),
	}, true
}

// titleOf is a concept's frontmatter title with its whitespace collapsed, or
// "" when it has none or it is not a scalar.
func titleOf(fm *okf.Frontmatter) string {
	v, _ := frontmatterValue(fm, "title").(string)
	return strings.Join(strings.Fields(v), " ")
}

// defaultTitleMaxLength is the length above which a title is a sentence, not
// a label (title_quality). A map overrides it with title_max_length.
const defaultTitleMaxLength = 100

// titleStatusWords are lifecycle words that decay when embedded in a title.
var titleStatusWords = []string{"attivo", "active", "dismesso", "deprecated", "draft", "superseded", "preparazione", "archiviato", "archived", "declassato"}

var datePrefixedSlug = regexp.MustCompile(`^\d{4}-\d{2}`)

// titleQualityFindings implements title_quality (info, D315): decorative
// characters, over-long titles, a status word where the status field already
// says it, a term the map forbids, and a date-prefixed slug outside a
// journal. No fix: each is a judgement about wording.
func titleQualityFindings(in conceptInput, fm *okf.Frontmatter, title string) []Finding {
	if title == "" {
		return nil
	}
	var out []Finding
	add := func(msg string) {
		out = append(out, Finding{Path: in.RelPath, Check: "title_quality", Severity: SevInfo, Message: msg})
	}
	var deco []string
	for _, r := range title {
		if unicode.Is(unicode.So, r) || unicode.Is(unicode.Sk, r) || r == '\uFE0F' || r == '\u200D' {
			deco = append(deco, string(r))
		}
	}
	if len(deco) > 0 {
		add(fmt.Sprintf("title contains decorative characters (%s); titles are labels shown in listings and the Atlas: prefer plain text", strings.Join(deco, " ")))
	}
	limit := defaultTitleMaxLength
	if in.Contract != nil && in.Contract.TitleMaxLength != nil {
		limit = *in.Contract.TitleMaxLength
	}
	if n := len([]rune(title)); limit > 0 && n > limit {
		add(fmt.Sprintf("title is %d characters (limit %d for this map); a title is a label, not a sentence", n, limit))
	}
	lower := strings.ToLower(title)
	if _, ok := fm.Get("status"); ok {
		for _, w := range titleStatusWords {
			if containsWord(lower, w) {
				add(fmt.Sprintf("title contains status word %q; the status field tracks lifecycle — a status in the title decays with the page", w))
				break
			}
		}
	}
	if in.Contract != nil {
		for _, term := range in.Contract.ForbiddenTitleTerms {
			if t := strings.ToLower(strings.TrimSpace(term)); t != "" && strings.Contains(lower, t) {
				add(fmt.Sprintf("title contains forbidden term %q (declared in the map contract)", term))
			}
		}
	}
	slug := strings.TrimSuffix(path.Base(in.RelPath), ".md")
	if in.MapName != "" && datePrefixedSlug.MatchString(slug) && (in.Contract == nil || in.Contract.Kind != "journal") {
		add("date-prefixed ID in a non-journal map; journal entries belong in a journal, map concepts use a descriptive slug")
	}
	return out
}

// containsWord reports whether word occurs in s delimited by non-letters, so
// "draft" matches "Draft plan" and "(draft)" but "active" not "proactive".
func containsWord(s, word string) bool {
	for from := 0; ; {
		i := strings.Index(s[from:], word)
		if i < 0 {
			return false
		}
		i += from
		end := i + len(word)
		before := i == 0 || !unicode.IsLetter([]rune(s[:i])[len([]rune(s[:i]))-1])
		after := end == len(s) || !unicode.IsLetter([]rune(s[end:])[0])
		if before && after {
			return true
		}
		from = end
	}
}
