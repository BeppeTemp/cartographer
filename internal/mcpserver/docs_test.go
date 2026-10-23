package mcpserver

// Guards the docs against tool-name drift: twice in a row, a rename left dead
// tool names behind in the markdown (D66 kb_overview→atlas_overview, D77
// archive_*/dossier_*→map_*), and both times it was found by hand, months
// later — the README was still advertising three tools that no longer existed.
// A rename that forgets a page now fails CI instead.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/BeppeTemp/cartographer/internal/lint"
)

// toolShaped matches a backticked snake_case identifier — the way the docs cite
// a tool (`concept_read`) as well as, unavoidably, lint rules, frontmatter
// fields and Go identifiers. toolPrefixes narrows it to the domains the MCP
// surface actually uses, and docsNonTools exempts the known homonyms.
var toolShaped = regexp.MustCompile(`\x60([a-z][a-z0-9_]*_[a-z0-9_]+)\x60`)

// toolPrefixes are the domain prefixes of the MCP tool names. A citation
// starting with one of these is expected to name a real tool.
var toolPrefixes = []string{
	"atlas_", "map_", "concept_", "index_", "log_", "graph_", "search_",
	"skill_", "service_", "secret_", "artifact_", "template_", "asset_", "sync_", "git_", "kb_", "archive_",
	"dossier_", "conflict_", "conflicts_", "commit_", "gate_", "changes_",
	"contradiction_", "source_",
}

// docsNonTools are identifiers that match toolShaped and a tool prefix but are
// not tools: frontmatter fields, and one deliberate mention of
// an operation that does not exist (documented as such). Lint check and
// observation names are exempt through the lint registry (lintNames), and an
// entry that is a tool or a lint name fails TestDocsToolNamesExist.
var docsNonTools = map[string]bool{
	"artifact_signing_seed": true,
	// kbs[] / settings.yaml keys (D746), not tools
	"git_base_branch": true, "git_forge": true, "git_working_branch": true, "git_branch": true,
	"kb_aliases":         true, // server config key (D772), not a tool
	"search_misses":      true, // kb_status field (D247), not a tool
	"changes_by_day":     true, // changes_since field (D1057), not a tool
	"template_unusable":  true, // policy denial code (D343), not a tool
	"sync_timer_opt_out": true, // client config key (D325), not a tool
	"open_gaps":          true, // kb_status field (D273), not a tool
	"index_entry":        true, // concept_write/concept_new/concept_batch parameter (D714, D1099), not a tool
	"commit_count":       true, // changes_since result field, not a tool
	"commit_sha":         true, // audit entry field (GET /kbs/{kb}/audit), not a tool
	"kb_path":            true, // GET /kbs/{kb}/artifacts kinds field (D864), not a tool
	"kb_only":            true, // GET /kbs/{kb}/artifacts kinds field (D864), not a tool
	"missing_context":    true, // reserved contradiction_kind (D273)
	"sync_h1":            true, // kb_repair fix kind (D315), not a tool
	"open_question":      true, // reserved contradiction_kind (D273)
	// audit configuration keys (D119), not tools
	"archive_dir":       true,
	"retention_days":    true,
	"max_segment_bytes": true,
	// template_proposal review item fields (D352), not tools
	"map_without_templates": true, "template_slug": true,
	"template_sections": true,
	"map_updates":       true, // `template_proposal` review item field (internal/lint/review.go:205), not a tool
	// gate_check response field (D186), not a tool
	"gate_blockers": true,
	// frontmatter fields and schema keys
	"concept_id": true, "concept_types": true, "archive_type": true,
	// tool argument names, cited where the docs explain which argument
	// identifies the resource a write touched (commit subjects)
	"contradiction_id": true, "source_id": true, "source_kind": true,
	"contradiction_kind": true, "service_ref": true, "secret_refs": true,
	"secrets_source": true, "source_of": true, "review_after": true, "asserted_by": true,
	"last_indexed_commit": true, "content_hash": true, "applied_revision": true,
	"sops_age_key_file": true, "token_env": true,
	"search_roots": true, "search_depth": true, "resolution_strategy": true, "resolution_body": true,
	// kb_status / sync_status output fields and server config keys that D903 removed,
	// named in the docs that say they are gone: not tools
	"git_workflow": true, "git_profile": true, "git_sync": true,
}

// docsFiles are the markdown files whose tool citations must resolve: the docs,
// the bundled skills, .claude/ and the area AGENTS.md files. The
// decision registers are excluded because they intentionally retain historical
// tool names. Walk recursively so a new nested current-state page is covered.
func docsFiles(t *testing.T) []string {
	t.Helper()
	root := "../.."
	var files []string
	for _, p := range []string{"README.md", "AGENTS.md", "CONTRIBUTING.md"} {
		files = append(files, filepath.Join(root, p))
	}
	docsRoot := filepath.Join(root, "docs")
	err := filepath.WalkDir(docsRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path == filepath.Join(docsRoot, "decisions") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(entry.Name(), ".md") && entry.Name() != "decisions.md" {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk docs/: %v", err)
	}
	// The shipped skills, .claude/ and the area AGENTS.md files name tools too.
	// Each pattern must match something, or a moved directory would turn the
	// scan into a no-op (docs/testing.md §Documentation gates).
	// Only the versioned part of .claude/: the rest is local, git-ignored state.
	for _, dir := range []string{"internal/skillbundle/bundled", ".claude/skills", ".claude/agents"} {
		before := len(files)
		base := filepath.Join(root, dir)
		err := filepath.WalkDir(base, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".md") {
				files = append(files, path)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
		if len(files) == before {
			t.Fatalf("the docs corpus matches no .md under %s: the scan would pass without reading anything", dir)
		}
	}
	areas, err := filepath.Glob(filepath.Join(root, "internal", "*", "AGENTS.md"))
	if err != nil || len(areas) == 0 {
		t.Fatalf("the docs corpus matches no internal/*/AGENTS.md (err %v)", err)
	}
	files = append(files, areas...)
	return files
}

// fullRegistry builds the whole tool surface. The BundleFS must be non-nil
// (tools.go:184): with nil, skill_install and sync_check/apply/pull are not
// registered and every guard built on this registry would silently shrink.
func fullRegistry(t *testing.T) map[string]*Tool {
	t.Helper()
	k := setupTestKB(t)
	s := New("test")
	RegisterKBTools(s, k, Deps{BundleFS: fstest.MapFS{}})
	return s.tools
}

// lintNames are the names of the lint checks and observations: the registry
// knows them, so the docs may cite them without a docsNonTools entry.
func lintNames() map[string]bool {
	names := map[string]bool{}
	for _, c := range lint.Checks() {
		names[c.Name] = true
	}
	for _, o := range lint.Observations() {
		names[o.Name] = true
	}
	return names
}

func TestDocsToolNamesExist(t *testing.T) {
	t.Parallel()
	tools := fullRegistry(t)
	registered := make(map[string]bool, len(tools))
	for name := range tools {
		registered[name] = true
	}
	lintReg := lintNames()
	for name := range docsNonTools {
		switch {
		case registered[name]:
			t.Errorf("remove %q from docsNonTools: it is a registered tool", name)
		case lintReg[name]:
			t.Errorf("remove %q from docsNonTools: it is a lint check or observation, exempt through the lint registry", name)
		}
	}

	for _, path := range docsFiles(t) {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for i, line := range strings.Split(string(data), "\n") {
			for _, m := range toolShaped.FindAllStringSubmatch(line, -1) {
				name := m[1]
				if registered[name] || docsNonTools[name] || lintReg[name] {
					continue
				}
				hasPrefix := false
				for _, p := range toolPrefixes {
					if strings.HasPrefix(name, p) {
						hasPrefix = true
						break
					}
				}
				if !hasPrefix {
					continue
				}
				t.Errorf("%s:%d cites %q, which is not a registered MCP tool — "+
					"rename it, or add it to docsNonTools if it is not a tool",
					filepath.Base(path), i+1, name)
			}
		}
	}
}

// toolRow is one row of the tool tables of docs/control-plane.md §MCP API.
type toolRow struct {
	name    string
	params  string   // raw text between the parentheses
	markers []string // "R", "A", ... in the order written
	line    int
}

var (
	toolRowStart = regexp.MustCompile("^\\| \x60[a-z][a-z0-9_]*\\(")
	toolRowFull  = regexp.MustCompile("^\\| \x60([a-z][a-z0-9_]*)\\((.*?)\\)\x60((?: \\*\\*\\[[A-Za-z]+\\]\\*\\*)*) \\|.+\\|\\s*$")
	toolMarker   = regexp.MustCompile("\\*\\*\\[([A-Za-z]+)\\]\\*\\*")
	paramName    = regexp.MustCompile("^[a-z][a-z0-9_]*$")
)

// parseToolTable reads the rows of the section that runs from the "## MCP API"
// heading to the next "## " heading. A missing heading fails the test (a
// renamed heading must not turn the guard into a no-op), and so does a line
// that looks like a row and does not match the row grammar.
func parseToolTable(t *testing.T) []toolRow {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "docs", "control-plane.md"))
	if err != nil {
		t.Fatalf("read docs/control-plane.md: %v", err)
	}
	lines := strings.Split(string(data), "\n")
	start, end := -1, len(lines)
	for i, l := range lines {
		switch {
		case start < 0 && l == "## MCP API":
			start = i
		case start >= 0 && strings.HasPrefix(l, "## "):
			end = i
		}
		if end != len(lines) {
			break
		}
	}
	if start < 0 {
		t.Fatal("docs/control-plane.md has no \"## MCP API\" heading: the tool table guard would check nothing — update docs_test.go if the section was renamed")
	}
	var rows []toolRow
	for i := start + 1; i < end; i++ {
		if !toolRowStart.MatchString(lines[i]) {
			continue
		}
		m := toolRowFull.FindStringSubmatch(lines[i])
		if m == nil {
			t.Errorf("docs/control-plane.md:%d: line looks like a tool row but does not match the row grammar "+
				"`| `name(params)` **[R]** **[A]** | description |` — fix it", i+1)
			continue
		}
		r := toolRow{name: m[1], params: m[2], line: i + 1}
		for _, mk := range toolMarker.FindAllStringSubmatch(m[3], -1) {
			r.markers = append(r.markers, mk[1])
		}
		rows = append(rows, r)
	}
	if len(rows) == 0 {
		t.Fatal("docs/control-plane.md §MCP API has no tool row: the guard would check nothing")
	}
	return rows
}

func hasMarker(r toolRow, m string) bool {
	for _, x := range r.markers {
		if x == m {
			return true
		}
	}
	return false
}

func TestToolTableMatchesRegistry(t *testing.T) {
	t.Parallel()
	tools := fullRegistry(t)
	rows := parseToolTable(t)
	const doc = "docs/control-plane.md §MCP API"
	var errs []string
	seen := map[string]int{}
	for _, r := range rows {
		seen[r.name]++
		tool, ok := tools[r.name]
		if !ok {
			errs = append(errs, fmt.Sprintf("%s: row %q names no registered tool — rename or delete the row", doc, r.name))
			continue
		}
		if seen[r.name] == 2 {
			errs = append(errs, fmt.Sprintf("%s: tool %q has more than one row — keep one", doc, r.name))
		}
		written := map[string]bool{}
		for _, mk := range r.markers {
			if written[mk] {
				errs = append(errs, fmt.Sprintf("row %q: marker **[%s]** is written twice; keep one", r.name, mk))
			}
			written[mk] = true
		}
		got := strings.Join(r.markers, ",")
		want := ""
		if tool.ReadOnly {
			want = "R"
		}
		if ToolAdvanced(r.name) {
			if want != "" {
				want += ","
			}
			want += "A"
		}
		if got == want {
			continue
		}
		for _, mk := range r.markers {
			if mk != "R" && mk != "A" {
				errs = append(errs, fmt.Sprintf("%s: row %q carries marker **[%s]**; only **[R]** and **[A]** exist — delete it", doc, r.name, mk))
			}
		}
		if hasMarker(r, "R") != tool.ReadOnly {
			errs = append(errs, fmt.Sprintf("row %q: Tool.ReadOnly is %v but the table says [R]=%v — fix the marker, or the tool's ReadOnly flag (readonly.go)",
				r.name, tool.ReadOnly, hasMarker(r, "R")))
		}
		if hasMarker(r, "A") != ToolAdvanced(r.name) {
			errs = append(errs, fmt.Sprintf("row %q is marked [A]=%v but ToolAdvanced(%q) is %v — fix the marker, or add/remove the tool in advancedToolNames (visibility.go)",
				r.name, hasMarker(r, "A"), r.name, ToolAdvanced(r.name)))
		}
		if got == "A,R" {
			errs = append(errs, fmt.Sprintf("row %q: markers are in the order **[A]** **[R]**; write **[R]** first", r.name))
		}
	}
	for name := range tools {
		if seen[name] == 0 {
			errs = append(errs, fmt.Sprintf("%s: tool %q is registered but has no row — add one to the table of its family", doc, name))
		}
	}
	for name := range advancedToolNames {
		if _, ok := tools[name]; !ok {
			errs = append(errs, fmt.Sprintf("advancedToolNames (visibility.go) lists %q, which is not a registered tool — remove the key", name))
		}
	}
	sort.Strings(errs)
	for _, e := range errs {
		t.Error(e)
	}
}

// signatureAllow exempts, per tool, the required-ness comparison of
// TestToolTableParametersMatchSchemas for a row whose required parameters the
// schema cannot express. The reason is mandatory; a key that is not a tool, or
// whose tool passes without it, fails. Names are never exempt.
var signatureAllow = map[string]string{
	// tools_write.go:1644-1670 has no `required`, the handler enforces the pair.
	"concept_move": "either the source_id/target_id pair or moves[]; the schema cannot express the exclusive alternative",
}

// rowParam is one item of a row's parameter list.
type rowParam struct {
	names    []string // one name, or the alternatives of a union
	optional bool
}

// parseRowParams reads the parameter notation: `[x]` optional, `x` required,
// `a \| b` alternatives, a trailing `[]` on an array parameter.
func parseRowParams(raw string) ([]rowParam, error) {
	raw = strings.TrimSpace(strings.ReplaceAll(raw, "\\|", "|"))
	if raw == "" {
		return nil, nil
	}
	var items []string
	depth, from := 0, 0
	for i, c := range raw {
		switch c {
		case '[':
			depth++
		case ']':
			depth--
		case ',':
			if depth == 0 {
				items = append(items, raw[from:i])
				from = i + 1
			}
		}
	}
	items = append(items, raw[from:])
	var out []rowParam
	for _, it := range items {
		it = strings.TrimSpace(it)
		p := rowParam{}
		if strings.HasPrefix(it, "[") && strings.HasSuffix(it, "]") && !strings.HasSuffix(it, "[]") ||
			strings.HasPrefix(it, "[") && strings.HasSuffix(it, "[]]") {
			p.optional = true
			it = strings.TrimSpace(it[1 : len(it)-1])
		}
		for _, alt := range strings.Split(it, "|") {
			alt = strings.TrimSuffix(strings.TrimSpace(alt), "[]")
			if strings.ContainsAny(alt, "?=") {
				return nil, fmt.Errorf("parameter %q uses a retired notation; write [name] for an optional one", alt)
			}
			if !paramName.MatchString(alt) {
				return nil, fmt.Errorf("parameter %q is not a name in the row grammar", alt)
			}
			p.names = append(p.names, alt)
		}
		out = append(out, p)
	}
	return out, nil
}

func TestToolTableParametersMatchSchemas(t *testing.T) {
	t.Parallel()
	tools := fullRegistry(t)
	rows := parseToolTable(t)
	var errs []string
	requiredErr := map[string]bool{}
	for _, r := range rows {
		tool, ok := tools[r.name]
		if !ok {
			continue // TestToolTableMatchesRegistry names it
		}
		params, err := parseRowParams(r.params)
		if err != nil {
			errs = append(errs, fmt.Sprintf("row %q: %v", r.name, err))
			continue
		}
		var schema struct {
			Properties map[string]json.RawMessage `json:"properties"`
			Required   []string                   `json:"required"`
		}
		if len(tool.InputSchema) > 0 {
			if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
				errs = append(errs, fmt.Sprintf("tool %q: input schema is not valid JSON: %v", r.name, err))
				continue
			}
		}
		skip := func(n string) bool { return n == "kb" || (tool.GitWrapped && n == "reason") }
		props := map[string]bool{}
		for n := range schema.Properties {
			if !skip(n) {
				props[n] = true
			}
		}
		documented := map[string]bool{}
		single := map[string]bool{} // non-bracketed, non-union items
		for _, p := range params {
			for _, n := range p.names {
				if !skip(n) {
					documented[n] = true
				}
			}
			if !p.optional && len(p.names) == 1 {
				single[p.names[0]] = true
			}
		}
		var extra, missing []string
		for n := range documented {
			if !props[n] {
				extra = append(extra, n)
			}
		}
		for n := range props {
			if !documented[n] {
				missing = append(missing, n)
			}
		}
		sort.Strings(extra)
		sort.Strings(missing)
		if len(extra) > 0 {
			errs = append(errs, fmt.Sprintf("tool %q: documented but not in the schema: %s — fix the row (docs/control-plane.md:%d) or the schema", r.name, strings.Join(extra, ", "), r.line))
		}
		if len(missing) > 0 {
			errs = append(errs, fmt.Sprintf("tool %q: in the schema but not documented: %s — add them to the row as [name] (docs/control-plane.md:%d)", r.name, strings.Join(missing, ", "), r.line))
		}
		var reqErrs []string
		for n := range single {
			if skip(n) || !props[n] {
				continue
			}
			isReq := false
			for _, q := range schema.Required {
				isReq = isReq || q == n
			}
			if !isReq {
				reqErrs = append(reqErrs, fmt.Sprintf("tool %q: parameter %q is documented as required but the schema leaves it optional — write [%s] (docs/control-plane.md:%d)", r.name, n, n, r.line))
			}
		}
		for _, q := range schema.Required {
			if !skip(q) && !single[q] {
				reqErrs = append(reqErrs, fmt.Sprintf("tool %q: parameter %q is required by the schema but the row does not list it as required — write %s without brackets (docs/control-plane.md:%d)", r.name, q, q, r.line))
			}
		}
		sort.Strings(reqErrs)
		if len(reqErrs) > 0 {
			requiredErr[r.name] = true
			if _, allowed := signatureAllow[r.name]; !allowed {
				errs = append(errs, reqErrs...)
			}
		}
	}
	for name, reason := range signatureAllow {
		switch {
		case strings.TrimSpace(reason) == "":
			errs = append(errs, fmt.Sprintf("signatureAllow[%q] has no reason — say why the schema cannot express the row", name))
		case tools[name] == nil:
			errs = append(errs, fmt.Sprintf("signatureAllow[%q] is not a registered tool — remove the entry", name))
		case !requiredErr[name]:
			errs = append(errs, fmt.Sprintf("signatureAllow[%q] is a stale entry, remove it: the row now agrees with the schema", name))
		}
	}
	sort.Strings(errs)
	for _, e := range errs {
		t.Error(e)
	}
}
