package mcpserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/okf"
	"github.com/BeppeTemp/cartographer/internal/sqlindex"
)

// --- concept_write ---

func toolConceptWrite(k *kb.KB, live *liveIndex, sqlIdx *sqlindex.Index) Tool {
	return Tool{
		Name: "concept_write",
		Description: "Creates or updates a concept. Requires frontmatter (YAML map) and markdown body. " +
			"Uses if_match (content-hash) for optimistic concurrency: fails with stale_write " +
			"if content was modified. Returns the new content_hash.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"required": ["id", "frontmatter", "body"],
			"properties": {
				"id": {
					"type": "string",
					"description": "ConceptID (path relative to KB root without .md)"
				},
				"frontmatter": {
					"type": "object",
					"description": "Frontmatter key-value map. The type field is required. Values can be strings or string arrays."
				},
				"body": {
					"type": "string",
					"description": "Markdown body"
				},
				"if_match": {
					"type": "string",
					"description": "Expected content-hash (optional, for optimistic concurrency)"
				}
			}
		}`),
		Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
			var params struct {
				ID          string                 `json:"id"`
				Frontmatter map[string]interface{} `json:"frontmatter"`
				Body        string                 `json:"body"`
				IfMatch     string                 `json:"if_match"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return errorResult("invalid params: " + err.Error()), nil
			}
			if params.ID == "" {
				return errorResult("'id' is required"), nil
			}
			if params.Frontmatter == nil {
				return errorResult("'frontmatter' is required"), nil
			}

			// Build a structured Frontmatter from a JSON map.
			fm, err := okf.ParseFrontmatter("")
			if err != nil {
				return errorResult("internal frontmatter error: " + err.Error()), nil
			}
			applyFrontmatterMap(fm, params.Frontmatter)

			newHash, err := writeConceptAndIndex(k, live, sqlIdx, "concept_write", params.ID, fm, params.Body, params.IfMatch)
			if err != nil {
				if errors.Is(err, okf.ErrStaleWrite) {
					return errorResult("stale_write: " + err.Error()), nil
				}
				return errorResult(fmt.Sprintf("concept_write %q: %v", params.ID, err)), nil
			}

			result := map[string]interface{}{
				"id":           params.ID,
				"content_hash": newHash,
			}
			out, _ := json.MarshalIndent(result, "", "  ")
			return textResult(string(out)), nil
		},
	}
}

// --- concept_new ---

// toolConceptNew creates a concept from a KB-owned template. Unlike
// concept_write it is deliberately create-only: rendering is a one-shot,
// literal substitution and never carries if_match overwrite semantics.
func toolConceptNew(k *kb.KB, live *liveIndex, sqlIdx *sqlindex.Index) Tool {
	return Tool{
		Name: "concept_new",
		Description: "Creates a new concept from a KB-only template (git commit). Template variables are substituted once, " +
			"literally, in frontmatter values and the body. It refuses an existing target; use concept_write or concept_patch " +
			"to update one. It does not pre-check strict-map ontology because a template may legitimately serve several maps, " +
			"and it does not curate map indexes.",
		InputSchema: json.RawMessage(`{
			"type":"object", "required":["template", "id"],
			"properties": {
				"template":{"type":"string","description":"Template slug from template_list"},
				"id":{"type":"string","description":"New ConceptID (path relative to KB root without .md)"},
				"vars":{"type":"object","additionalProperties":{"type":"string"},"description":"Optional template variable values"}
			}
		}`),
		Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
			var params struct {
				Template string            `json:"template"`
				ID       string            `json:"id"`
				Vars     map[string]string `json:"vars"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return errorResult("invalid params: " + err.Error()), nil
			}
			if params.Template == "" {
				return errorResult("'template' is required"), nil
			}
			if params.ID == "" {
				return errorResult("'id' is required"), nil
			}
			id, err := okf.PathToID(params.ID + ".md")
			if err != nil || string(id) != params.ID {
				return errorResult(fmt.Sprintf("concept_new %q: invalid ConceptID (use kebab-case path segments)", params.ID)), nil
			}
			segments := strings.Split(params.ID, "/")
			if !strings.HasPrefix(params.ID, "services/") && len(segments) > 3 {
				return errorResult(fmt.Sprintf("concept_new %q: invalid ConceptID: concept depth exceeds the max of 3 segments", params.ID)), nil
			}
			if _, err := k.ReadConcept(id); err == nil {
				return errorResult(fmt.Sprintf("concept_new %q: already exists — use concept_write or concept_patch to update it", params.ID)), nil
			} else if !errors.Is(err, okf.ErrNotFound) {
				return errorResult(fmt.Sprintf("concept_new %q: %v", params.ID, err)), nil
			}

			info, err := classifyArtifactPath("templates/" + params.Template + ".md")
			if err != nil || info.Kind != "template" {
				return errorResult(fmt.Sprintf("concept_new: invalid template %q", params.Template)), nil
			}
			if err := rejectArtifactSymlinks(k.Root, "templates/"+params.Template+".md"); err != nil {
				return errorResult("concept_new: " + err.Error()), nil
			}
			path, err := k.ResolveRootPath("templates/" + params.Template + ".md")
			if err != nil {
				return errorResult("concept_new: " + err.Error()), nil
			}
			content, err := os.ReadFile(path)
			if os.IsNotExist(err) {
				available, listErr := listTemplateSlugs(k)
				if listErr != nil {
					return errorResult("concept_new: " + listErr.Error()), nil
				}
				slugs := available
				truncated := len(slugs) > 20
				if truncated {
					slugs = slugs[:20]
				}
				return errorResult(fmt.Sprintf("concept_new: template %q not found; available templates: %s; truncated:%t", params.Template, strings.Join(slugs, ", "), truncated)), nil
			}
			if err != nil {
				return errorResult(fmt.Sprintf("concept_new: read template %q: %v", params.Template, err)), nil
			}
			fm, body, wanted, err := validateTemplateArtifact(content)
			if err != nil {
				return errorResult(fmt.Sprintf("concept_new: template %q is invalid: %v", params.Template, err)), nil
			}
			provided := make([]string, 0, len(params.Vars))
			for name := range params.Vars {
				provided = append(provided, name)
			}
			sort.Strings(provided)
			missing, extra := templateVariableDiff(wanted, provided)
			if len(missing) > 0 {
				return errorResult("concept_new: missing template vars: " + strings.Join(missing, ", ")), nil
			}
			if len(extra) > 0 {
				return errorResult("concept_new: unexpected template vars: " + strings.Join(extra, ", ")), nil
			}
			for _, key := range fm.Keys() {
				value, _ := fm.Get(key)
				switch v := value.(type) {
				case string:
					fm.Set(key, renderTemplateText(v, params.Vars))
				case []string:
					rendered := make([]string, len(v))
					for i, item := range v {
						rendered[i] = renderTemplateText(item, params.Vars)
					}
					fm.Set(key, rendered)
				}
			}
			body = renderTemplateText(body, params.Vars)
			newHash, err := writeConceptAndIndex(k, live, sqlIdx, "concept_new", params.ID, fm, body, "")
			if err != nil {
				return errorResult(fmt.Sprintf("concept_new %q: %v", params.ID, err)), nil
			}
			out, _ := json.MarshalIndent(map[string]string{"id": params.ID, "template": params.Template, "content_hash": newHash}, "", "  ")
			return textResult(string(out)), nil
		},
	}
}

func templateVariableDiff(wanted, provided []string) (missing, extra []string) {
	wantedSet := make(map[string]bool, len(wanted))
	providedSet := make(map[string]bool, len(provided))
	for _, name := range wanted {
		wantedSet[name] = true
	}
	for _, name := range provided {
		providedSet[name] = true
	}
	for _, name := range wanted {
		if !providedSet[name] {
			missing = append(missing, name)
		}
	}
	for _, name := range provided {
		if !wantedSet[name] {
			extra = append(extra, name)
		}
	}
	return missing, extra
}

// renderTemplateText scans only the source template, so values containing $,
// {{other}}, colons or newlines are inserted literally and never reinterpreted.
func renderTemplateText(source string, vars map[string]string) string {
	var rendered strings.Builder
	for pos := 0; pos < len(source); {
		open := strings.Index(source[pos:], "{{")
		if open < 0 {
			rendered.WriteString(source[pos:])
			break
		}
		open += pos
		rendered.WriteString(source[pos:open])
		start := open + 2
		end := strings.Index(source[start:], "}}")
		// The template was validated before this call, so an absent close is
		// unreachable; retaining the source is a defensive fail-closed fallback.
		if end < 0 {
			rendered.WriteString(source[open:])
			break
		}
		end += start
		rendered.WriteString(vars[source[start:end]])
		pos = end + 2
	}
	return rendered.String()
}

// applyFrontmatterMap shallow-applies a JSON-decoded frontmatter map onto fm,
// converting each value to the string/[]string forms okf.Frontmatter expects.
// A JSON null value unsets the key (D88): fm.Delete(key), rather than setting
// it to a literal nil value. Reserved/managed keys (e.g. "type") keep their
// existing protection downstream in kb.WriteConcept, which still fails the
// write if the required field ends up missing.
// Shared by concept_write (full frontmatter) and concept_patch (optional
// partial frontmatter merge, D70).
func applyFrontmatterMap(fm *okf.Frontmatter, m map[string]interface{}) {
	for key, val := range m {
		switch v := val.(type) {
		case string:
			fm.Set(key, v)
		case []interface{}:
			ss := make([]string, len(v))
			for i, item := range v {
				ss[i] = fmt.Sprintf("%v", item)
			}
			fm.Set(key, ss)
		case nil:
			fm.Delete(key)
		default:
			fm.Set(key, fmt.Sprintf("%v", val))
		}
	}
}

// writeConceptAndIndex writes a concept via k.WriteConcept and keeps the
// in-memory keyword index and SQLite FTS5 index in sync, so search reflects
// the new content immediately without requiring an index_rebuild call.
// Shared write-path for concept_write and concept_patch (D70). logPrefix
// labels the resulting log.md entry (e.g. "concept_write", "concept_patch").
func writeConceptAndIndex(k *kb.KB, live *liveIndex, sqlIdx *sqlindex.Index, logPrefix string, id string, fm *okf.Frontmatter, body string, ifMatch string) (string, error) {
	newHash, err := k.WriteConcept(okf.ConceptID(id), fm, body, ifMatch)
	if err != nil {
		return "", err
	}

	_ = k.AppendLog(logPrefix+": "+id, time.Now())

	// Best-effort: keep both search indexes in sync. Embedding is
	// intentionally not refreshed here; it stays the responsibility of
	// index_rebuild (with its content-hash cache).
	if data, readErr := k.ReadConcept(okf.ConceptID(id)); readErr == nil {
		live.add(id, data.Content)
		if sqlIdx != nil {
			if err := sqlIdx.Upsert(id, data.ContentHash, data.Content); err != nil {
				fmt.Fprintf(os.Stderr, "%s: sqlindex upsert %q: %v\n", logPrefix, id, err)
			}
		}
	}

	return newHash, nil
}

// --- concept_patch ---

// patchEditItem is a single old_string/new_string replacement, used both for
// the batch "edits" array and (conceptually) for the single top-level
// old_string/new_string/replace_all form (WP1, D76).
type patchEditItem struct {
	OldString  string `json:"old_string"`
	NewString  string `json:"new_string"`
	ReplaceAll bool   `json:"replace_all"`
}

// applyPatchEdit applies a single old_string/new_string replacement to body
// with Edit-tool semantics: old_string must match exactly once unless
// replaceAll is set, in which case every occurrence is replaced. Returns the
// resulting body and the number of replacements performed. Shared by the
// single-edit and batch ("edits") forms of concept_patch (D76 WP1) so the
// old_string_not_found/old_string_ambiguous logic is not duplicated.
func applyPatchEdit(body, oldString, newString string, replaceAll bool) (newBody string, replacements int, err error) {
	count := strings.Count(body, oldString)
	if count == 0 {
		return "", 0, errors.New("old_string_not_found: no match for old_string")
	}
	if count > 1 && !replaceAll {
		return "", 0, fmt.Errorf(
			"old_string_ambiguous: old_string matches %d times; pass replace_all=true or provide more surrounding context",
			count,
		)
	}
	if replaceAll {
		return strings.ReplaceAll(body, oldString, newString), count, nil
	}
	return strings.Replace(body, oldString, newString, 1), 1, nil
}

func toolConceptPatch(k *kb.KB, live *liveIndex, sqlIdx *sqlindex.Index) Tool {
	return Tool{
		Name: "concept_patch",
		Description: "Patches a concept's body with an old_string/new_string replacement " +
			"(Edit-tool semantics), without rewriting the whole content. Accepts either a single " +
			"top-level old_string/new_string/replace_all triple or an 'edits' array of {old_string, " +
			"new_string, replace_all?} objects applied atomically and in order (each edit sees the " +
			"result of the previous one); the two forms are mutually exclusive. if_match is required " +
			"(a patch only makes sense against an already-read concept): fails with stale_write " +
			"if content changed since. Fails with old_string_not_found or old_string_ambiguous " +
			"(pass replace_all to allow multiple matches); for a batch, the error names the failing " +
			"edit's index and nothing is written. frontmatter, if given, is shallow-merged onto the " +
			"existing frontmatter; set a key to null to remove it (fails if the key is required, e.g. " +
			"'type'). Returns the new content_hash.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"required": ["id", "if_match"],
			"properties": {
				"id": {
					"type": "string",
					"description": "ConceptID (path relative to KB root without .md)"
				},
				"old_string": {
					"type": "string",
					"description": "Exact substring to find in the concept's current body (single-edit form, mutually exclusive with 'edits')"
				},
				"new_string": {
					"type": "string",
					"description": "Replacement text (single-edit form, mutually exclusive with 'edits')"
				},
				"replace_all": {
					"type": "boolean",
					"description": "Replace all occurrences of old_string. Default false: old_string must match exactly once. (single-edit form, mutually exclusive with 'edits')"
				},
				"edits": {
					"type": "array",
					"description": "Batch form: list of {old_string, new_string, replace_all?} applied atomically and in order. Mutually exclusive with old_string/new_string/replace_all.",
					"items": {
						"type": "object",
						"required": ["old_string", "new_string"],
						"properties": {
							"old_string": {"type": "string"},
							"new_string": {"type": "string"},
							"replace_all": {"type": "boolean"}
						}
					}
				},
				"if_match": {
					"type": "string",
					"description": "Expected content-hash (required)"
				},
				"frontmatter": {
					"type": "object",
					"description": "Optional: frontmatter keys to shallow-merge onto the existing frontmatter (e.g. bump 'aggiornato'). Keys not listed are left untouched; set a key to null to remove it (fails if the key is required, e.g. 'type')."
				}
			}
		}`),
		Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
			var params struct {
				ID          string                 `json:"id"`
				OldString   string                 `json:"old_string"`
				NewString   string                 `json:"new_string"`
				ReplaceAll  bool                   `json:"replace_all"`
				Edits       []patchEditItem        `json:"edits"`
				IfMatch     string                 `json:"if_match"`
				Frontmatter map[string]interface{} `json:"frontmatter"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return errorResult("invalid params: " + err.Error()), nil
			}
			if params.ID == "" {
				return errorResult("'id' is required"), nil
			}
			if params.IfMatch == "" {
				return errorResult("'if_match' is required"), nil
			}

			// 'edits' presence is checked on the raw JSON (not len(params.Edits))
			// so that an explicit "edits": [] is distinguished from an absent
			// 'edits' key and reported as "cannot be empty" rather than silently
			// falling back to the single-edit form.
			var rawKeys map[string]json.RawMessage
			_ = json.Unmarshal(args, &rawKeys)
			_, hasEdits := rawKeys["edits"]
			hasSingle := params.OldString != "" || params.NewString != "" || params.ReplaceAll

			if hasEdits && hasSingle {
				return errorResult("'edits' is mutually exclusive with top-level 'old_string'/'new_string'/'replace_all'"), nil
			}
			if !hasEdits && !hasSingle {
				return errorResult("'old_string' is required (or provide 'edits' for a batch of edits)"), nil
			}
			if hasEdits && len(params.Edits) == 0 {
				return errorResult("'edits' cannot be empty"), nil
			}
			if !hasEdits && params.OldString == "" {
				return errorResult("'old_string' is required"), nil
			}

			data, err := k.ReadConcept(okf.ConceptID(params.ID))
			if err != nil {
				if errors.Is(err, okf.ErrNotFound) {
					return errorResult(fmt.Sprintf("concept_patch %q: not found", params.ID)), nil
				}
				return errorResult(fmt.Sprintf("concept_patch %q: %v", params.ID, err)), nil
			}

			// Apply every edit in memory first (sequentially, each seeing the
			// previous edit's result): nothing is written until all edits
			// succeed, so a failure mid-batch leaves the concept untouched.
			body := data.Body
			replacements := 0
			if hasEdits {
				for i, e := range params.Edits {
					if e.OldString == "" {
						return errorResult(fmt.Sprintf("edit %d of %d: 'old_string' is required", i+1, len(params.Edits))), nil
					}
					newBody, n, err := applyPatchEdit(body, e.OldString, e.NewString, e.ReplaceAll)
					if err != nil {
						return errorResult(fmt.Sprintf("edit %d of %d: %v", i+1, len(params.Edits), err)), nil
					}
					body = newBody
					replacements += n
				}
			} else {
				newBody, n, err := applyPatchEdit(body, params.OldString, params.NewString, params.ReplaceAll)
				if err != nil {
					return errorResult(fmt.Sprintf("%v in %s", err, params.ID)), nil
				}
				body = newBody
				replacements = n
			}

			fm, err := okf.ParseFrontmatter(data.FrontmatterRaw)
			if err != nil {
				return errorResult(fmt.Sprintf("concept_patch: parse frontmatter: %v", err)), nil
			}
			if params.Frontmatter != nil {
				applyFrontmatterMap(fm, params.Frontmatter)
			}

			newHash, err := writeConceptAndIndex(k, live, sqlIdx, "concept_patch", params.ID, fm, body, params.IfMatch)
			if err != nil {
				if errors.Is(err, okf.ErrStaleWrite) {
					return errorResult("stale_write: " + err.Error()), nil
				}
				return errorResult(fmt.Sprintf("concept_patch %q: %v", params.ID, err)), nil
			}

			result := map[string]interface{}{
				"id":           params.ID,
				"content_hash": newHash,
				"replacements": replacements,
			}
			out, _ := json.MarshalIndent(result, "", "  ")
			return textResult(string(out)), nil
		},
	}
}

// --- index_patch ---

// normalizeIndexPath mirrors kb.curatedIndexRelPath's path normalization
// (trim slashes, "." collapses to root) so index_patch's response reports
// the same canonical path kb.IndexHash/PatchIndex resolved against,
// regardless of how the caller wrote it ("", ".", "entities/").
func normalizeIndexPath(path string) string {
	clean := strings.Trim(filepath.ToSlash(filepath.Clean(strings.ReplaceAll(path, "\\", "/"))), "/")
	if clean == "." {
		clean = ""
	}
	return clean
}

// toolIndexPatch patches the root or a Map/Journal's curated index.md
// (D122 WP2) — the bounded write half of the data plane added in kb.IndexHash/
// kb.PatchIndex (D122 WP1). It reuses applyPatchEdit's Edit-tool semantics
// (single or batch 'edits') verbatim from concept_patch, applied to the raw
// index content instead of a concept body, and never touches the live/SQLite
// concept search indexes: root/Map indexes are curated prose, not indexed
// concepts.
func toolIndexPatch(k *kb.KB) Tool {
	return Tool{
		Name: "index_patch",
		Description: "Patches the root or a Map/Journal's curated index.md with an old_string/new_string " +
			"replacement (Edit-tool semantics), the same bounded primitive as concept_patch but for a " +
			"curated index rather than a concept. Accepts either a single top-level " +
			"old_string/new_string/replace_all triple or an 'edits' array of {old_string, new_string, " +
			"replace_all?} objects applied atomically and in order (each edit sees the result of the " +
			"previous one); the two forms are mutually exclusive. if_match is required (read the current " +
			"content_hash with index_get(with_hash=true) first): fails with stale_write if the index " +
			"changed since. Fails with old_string_not_found or old_string_ambiguous (pass replace_all to " +
			"allow multiple matches); for a batch, the error names the failing edit's index and nothing " +
			"is written. An expanded concept's own index.md (e.g. 'map/concept') is not a curated index " +
			"and is rejected with expanded_index — use concept_patch(id=<owner>) for that instead. " +
			"Returns the normalized path, new content_hash, and replacement count.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"required": ["if_match"],
			"properties": {
				"path": {
					"type": "string",
					"description": "Path relative to KB root: empty/root, or a Map/Journal name (e.g. 'maintenance')."
				},
				"old_string": {
					"type": "string",
					"description": "Exact substring to find in the index's current content (single-edit form, mutually exclusive with 'edits')"
				},
				"new_string": {
					"type": "string",
					"description": "Replacement text (single-edit form, mutually exclusive with 'edits')"
				},
				"replace_all": {
					"type": "boolean",
					"description": "Replace all occurrences of old_string. Default false: old_string must match exactly once. (single-edit form, mutually exclusive with 'edits')"
				},
				"edits": {
					"type": "array",
					"description": "Batch form: list of {old_string, new_string, replace_all?} applied atomically and in order. Mutually exclusive with old_string/new_string/replace_all.",
					"items": {
						"type": "object",
						"required": ["old_string", "new_string"],
						"properties": {
							"old_string": {"type": "string"},
							"new_string": {"type": "string"},
							"replace_all": {"type": "boolean"}
						}
					}
				},
				"if_match": {
					"type": "string",
					"description": "Expected content-hash (required)"
				}
			}
		}`),
		Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
			var params struct {
				Path       string          `json:"path"`
				OldString  string          `json:"old_string"`
				NewString  string          `json:"new_string"`
				ReplaceAll bool            `json:"replace_all"`
				Edits      []patchEditItem `json:"edits"`
				IfMatch    string          `json:"if_match"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return errorResult("invalid params: " + err.Error()), nil
			}
			if params.IfMatch == "" {
				return errorResult("'if_match' is required"), nil
			}

			// Same 'edits' vs top-level mutual-exclusion handling as
			// concept_patch (D76 WP1), applied on the raw JSON so an explicit
			// "edits": [] is distinguished from an absent 'edits' key.
			var rawKeys map[string]json.RawMessage
			_ = json.Unmarshal(args, &rawKeys)
			_, hasEdits := rawKeys["edits"]
			hasSingle := params.OldString != "" || params.NewString != "" || params.ReplaceAll

			if hasEdits && hasSingle {
				return errorResult("'edits' is mutually exclusive with top-level 'old_string'/'new_string'/'replace_all'"), nil
			}
			if !hasEdits && !hasSingle {
				return errorResult("'old_string' is required (or provide 'edits' for a batch of edits)"), nil
			}
			if hasEdits && len(params.Edits) == 0 {
				return errorResult("'edits' cannot be empty"), nil
			}
			if !hasEdits && params.OldString == "" {
				return errorResult("'old_string' is required"), nil
			}

			content, _, err := k.IndexHash(params.Path)
			if err != nil {
				return errorResult(fmt.Sprintf("index_patch %q: %v", params.Path, err)), nil
			}

			// Apply every edit in memory first (sequentially, each seeing the
			// previous edit's result): nothing is written until all edits
			// succeed, so a failure mid-batch leaves the index untouched.
			replacements := 0
			if hasEdits {
				for i, e := range params.Edits {
					if e.OldString == "" {
						return errorResult(fmt.Sprintf("edit %d of %d: 'old_string' is required", i+1, len(params.Edits))), nil
					}
					newContent, n, err := applyPatchEdit(content, e.OldString, e.NewString, e.ReplaceAll)
					if err != nil {
						return errorResult(fmt.Sprintf("edit %d of %d: %v", i+1, len(params.Edits), err)), nil
					}
					content = newContent
					replacements += n
				}
			} else {
				newContent, n, err := applyPatchEdit(content, params.OldString, params.NewString, params.ReplaceAll)
				if err != nil {
					return errorResult(fmt.Sprintf("%v in index %q", err, params.Path)), nil
				}
				content = newContent
				replacements = n
			}

			newHash, err := k.PatchIndex(params.Path, params.IfMatch, content)
			if err != nil {
				if errors.Is(err, okf.ErrStaleWrite) {
					return errorResult("stale_write: " + err.Error()), nil
				}
				return errorResult(fmt.Sprintf("index_patch %q: %v", params.Path, err)), nil
			}

			normalizedPath := normalizeIndexPath(params.Path)
			logPath := normalizedPath
			if logPath == "" {
				logPath = "(root)"
			}
			_ = k.AppendLog("index_patch: "+logPath, time.Now())

			result := map[string]interface{}{
				"path":         normalizedPath,
				"content_hash": newHash,
				"replacements": replacements,
			}
			out, _ := json.MarshalIndent(result, "", "  ")
			return textResult(string(out)), nil
		},
	}
}

// --- map_create ---

func toolMapCreate(k *kb.KB) Tool {
	return Tool{
		Name:        "map_create",
		Description: "Creates a new Map or Journal in the Atlas (directory with _map.md, index.md, log.md). A Map holds mixed concept types on a theme; a Journal is a chronological log (e.g. incidents, notes). Concepts grow into expanded concepts via concept_expand, not via a separate creation step.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"required": ["name", "title"],
			"properties": {
				"name": {
					"type": "string",
					"description": "Directory name in kebab-case"
				},
				"title": {
					"type": "string",
					"description": "Human-readable title"
				},
				"kind": {
					"type": "string",
					"description": "\"map\" (thematic, default) or \"journal\" (chronological log, e.g. incidents/notes)"
				},
				"concept_types": {
					"type": "array",
					"items": {"type": "string"},
					"description": "Allowed types when ontology_mode=strict"
				},
				"ontology_mode": {
					"type": "string",
					"description": "strict or flexible (default: flexible)"
				},
				"required_fields": {
					"type": "array",
					"items": {"type": "string"},
					"description": "Frontmatter fields required for every concept in this map"
				},
				"required_fields_by_type": {
					"type": "object",
					"additionalProperties": {"type": "array", "items": {"type": "string"}},
					"description": "Additional required fields keyed by exact concept type"
				},
				"require_index_entry": {
					"type": "boolean",
					"description": "Require each concept to be linked from its curated index"
				},
				"machine_path_allow_prefixes": {
					"type": "array",
					"items": {"type": "string"},
					"description": "Absolute path prefixes (POSIX or Windows) that the machine_path lint should treat as this map's operational target paths rather than client-local paths (D124)"
				}
			}
		}`),
		Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
			var params struct {
				Name                     string              `json:"name"`
				Title                    string              `json:"title"`
				Kind                     string              `json:"kind"`
				ConceptTypes             []string            `json:"concept_types"`
				OntologyMode             string              `json:"ontology_mode"`
				RequiredFields           []string            `json:"required_fields"`
				RequiredFieldsByType     map[string][]string `json:"required_fields_by_type"`
				RequireIndexEntry        bool                `json:"require_index_entry"`
				MachinePathAllowPrefixes []string            `json:"machine_path_allow_prefixes"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return errorResult("invalid params: " + err.Error()), nil
			}
			if params.Name == "" {
				return errorResult("'name' is required"), nil
			}
			if params.Title == "" {
				return errorResult("'title' is required"), nil
			}
			for _, field := range params.RequiredFields {
				if strings.TrimSpace(field) == "" {
					return errorResult("'required_fields' must not contain empty field names"), nil
				}
			}
			for typ, fields := range params.RequiredFieldsByType {
				if strings.TrimSpace(typ) == "" {
					return errorResult("'required_fields_by_type' must not contain an empty type name"), nil
				}
				for _, field := range fields {
					if strings.TrimSpace(field) == "" {
						return errorResult("'required_fields_by_type' must not contain empty field names"), nil
					}
				}
			}
			for _, prefix := range params.MachinePathAllowPrefixes {
				if strings.TrimSpace(prefix) == "" {
					return errorResult("'machine_path_allow_prefixes' must not contain empty entries"), nil
				}
			}

			contract := kb.MapContract{
				RequiredFields:           params.RequiredFields,
				RequiredFieldsByType:     params.RequiredFieldsByType,
				RequireIndexEntry:        params.RequireIndexEntry,
				MachinePathAllowPrefixes: params.MachinePathAllowPrefixes,
			}
			if err := k.CreateMapWithContract(params.Name, params.Title, params.Kind, params.ConceptTypes, params.OntologyMode, contract); err != nil {
				return errorResult(fmt.Sprintf("map_create %q: %v", params.Name, err)), nil
			}

			_ = k.AppendLog("map_create: "+params.Name, time.Now())
			result := map[string]interface{}{
				"map":    params.Name,
				"status": "created",
			}
			out, _ := json.MarshalIndent(result, "", "  ")
			return textResult(string(out)), nil
		},
	}
}

// --- map_delete ---

func toolMapDelete(k *kb.KB) Tool {
	return Tool{
		Name: "map_delete",
		Description: "Deletes a Map or Journal directory, but only if it is empty — i.e. it contains " +
			"nothing but the scaffold files written by map_create (_map.md, index.md, log.md). If any " +
			"concept remains under it, the map is left untouched and the error lists them: move them " +
			"first with concept_move, then retry.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"required": ["map"],
			"properties": {
				"map": {
					"type": "string",
					"description": "Map/journal directory name (as passed to map_create)"
				}
			}
		}`),
		Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
			var params struct {
				Map string `json:"map"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return errorResult("invalid params: " + err.Error()), nil
			}
			if params.Map == "" {
				return errorResult("'map' is required"), nil
			}

			if err := k.DeleteMap(params.Map); err != nil {
				return errorResult(fmt.Sprintf("map_delete %q: %v", params.Map, err)), nil
			}

			_ = k.AppendLog("map_delete: "+params.Map, time.Now())
			result := map[string]interface{}{
				"map":    params.Map,
				"status": "deleted",
			}
			out, _ := json.MarshalIndent(result, "", "  ")
			return textResult(string(out)), nil
		},
	}
}

// --- concept_expand ---

func toolConceptExpand(k *kb.KB) Tool {
	return Tool{
		Name: "concept_expand",
		Description: "Promotes a concept into an expanded concept: turns \"<id>.md\" into a directory " +
			"\"<id>/\" whose index.md holds the same content under the same ID, so it can grow satellite " +
			"concepts (\"<id>/<child>\") without changing its ID or breaking existing links. Requires id " +
			"to have exactly two segments (map/concept). There is no inverse (concept_collapse).",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"required": ["id"],
			"properties": {
				"id": {
					"type": "string",
					"description": "ConceptID to expand (path relative to KB root without .md, exactly 2 segments: map/concept)"
				}
			}
		}`),
		Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
			var params struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return errorResult("invalid params: " + err.Error()), nil
			}
			if params.ID == "" {
				return errorResult("'id' is required"), nil
			}

			if err := k.ExpandConcept(okf.ConceptID(params.ID)); err != nil {
				return errorResult(fmt.Sprintf("concept_expand %q: %v", params.ID, err)), nil
			}

			_ = k.AppendLog("concept_expand: "+params.ID, time.Now())
			result := map[string]interface{}{
				"id":     params.ID,
				"status": "expanded",
			}
			out, _ := json.MarshalIndent(result, "", "  ")
			return textResult(string(out)), nil
		},
	}
}

// --- log_append ---

func toolLogAppend(k *kb.KB) Tool {
	return Tool{
		Name: "log_append",
		Description: "Appends an entry to the root log.md (newest-on-top). If path is given, the entry " +
			"is prefixed '[<path>] ' and still written to the root log — there is no per-directory log.md " +
			"(root-log-with-prefix convention, D78). Use log_tail(path) to read it back: it filters the " +
			"root log by that prefix.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"required": ["entry"],
			"properties": {
				"entry": {
					"type": "string",
					"description": "Log entry text"
				},
				"path": {
					"type": "string",
					"description": "Relative folder (optional, default root). Written to the root log as '[<path>] entry', not to a per-directory log.md (D78)."
				}
			}
		}`),
		Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
			var params struct {
				Entry string `json:"entry"`
				Path  string `json:"path"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return errorResult("invalid params: " + err.Error()), nil
			}
			if params.Entry == "" {
				return errorResult("'entry' is required"), nil
			}

			entry := params.Entry
			if params.Path != "" {
				// Root-log-with-prefix convention (D78): no per-directory log.md,
				// log_tail(path) recovers these by filtering on the prefix.
				entry = "[" + params.Path + "] " + entry
			}

			if err := k.AppendLog(entry, time.Now()); err != nil {
				return errorResult(fmt.Sprintf("log_append: %v", err)), nil
			}
			return textResult(`{"status": "appended"}`), nil
		},
	}
}

// --- snapshot ---

func toolSnapshot(k *kb.KB) Tool {
	return Tool{
		Name: "snapshot",
		Description: "Creates a KB snapshot: records a log entry and, when git auto-commit " +
			"is enabled (CARTOGRAPHER_GIT_AUTOCOMMIT=true), also creates a git commit.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"message": {
					"type": "string",
					"description": "Snapshot message (optional)"
				}
			}
		}`),
		Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
			var params struct {
				Message string `json:"message"`
			}
			json.Unmarshal(args, &params)

			msg := params.Message
			if msg == "" {
				msg = "snapshot"
			}

			if err := k.AppendLog("snapshot: "+msg, time.Now()); err != nil {
				return errorResult(fmt.Sprintf("snapshot: %v", err)), nil
			}

			result := map[string]interface{}{
				"message": msg,
				"status":  "logged",
			}
			out, _ := json.MarshalIndent(result, "", "  ")
			return textResult(string(out)), nil
		},
	}
}

// --- supersede ---

func toolSupersede(k *kb.KB) Tool {
	return Tool{
		Name:        "supersede",
		Description: "Marks a concept as superseded by another. Sets status=superseded and records the successor concept ID.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"required": ["source_id", "target_id"],
			"properties": {
				"source_id": {
					"type": "string",
					"description": "Concept ID of the concept to supersede"
				},
				"target_id": {
					"type": "string",
					"description": "Concept ID of the replacement concept"
				},
				"reason": {
					"type": "string",
					"description": "Optional reason for supersession"
				}
			}
		}`),
		Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
			var params struct {
				SourceID string `json:"source_id"`
				TargetID string `json:"target_id"`
				Reason   string `json:"reason"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return errorResult("invalid params: " + err.Error()), nil
			}
			if params.SourceID == "" {
				return errorResult("'source_id' is required"), nil
			}
			if params.TargetID == "" {
				return errorResult("'target_id' is required"), nil
			}

			data, err := k.ReadConcept(okf.ConceptID(params.SourceID))
			if err != nil {
				return errorResult(fmt.Sprintf("supersede: read source %q: %v", params.SourceID, err)), nil
			}

			fm, err := okf.ParseFrontmatter(data.FrontmatterRaw)
			if err != nil {
				return errorResult(fmt.Sprintf("supersede: parse frontmatter: %v", err)), nil
			}

			fm.Set("status", "superseded")
			fm.Set("superseded_by", params.TargetID)
			if params.Reason != "" {
				fm.Set("supersede_reason", params.Reason)
			}

			if _, err := k.WriteConcept(okf.ConceptID(params.SourceID), fm, data.Body, data.ContentHash); err != nil {
				return errorResult(fmt.Sprintf("supersede: write: %v", err)), nil
			}

			_ = k.AppendLog(fmt.Sprintf("supersede: %s → %s", params.SourceID, params.TargetID), time.Now())
			return textResult(fmt.Sprintf("superseded %s → %s", params.SourceID, params.TargetID)), nil
		},
	}
}

// --- concept_move ---

// conceptMoveEntry is a single source→target pair, used both for the batch
// "moves" array and (as a slice of one) for the single-move top-level form.
type conceptMoveEntry struct {
	SourceID string `json:"source_id"`
	TargetID string `json:"target_id"`
}

// rewrittenConcept reports one concept whose links were rewritten by a
// concept_move backlink-rewrite pass (D72 WP1).
type rewrittenConcept struct {
	ID           string `json:"id"`
	Replacements int    `json:"replacements"`
}

func toolConceptMove(k *kb.KB, live *liveIndex, sqlIdx *sqlindex.Index) Tool {
	return Tool{
		Name: "concept_move",
		Description: "Moves one or more concepts to new paths within the KB, in a single commit. " +
			"Accepts either a single source_id/target_id pair or a 'moves' array of {source_id, " +
			"target_id} objects (the two forms are mutually exclusive). Every entry is fully " +
			"validated (source exists, target free — including against other targets in the same " +
			"batch —, no path traversal, no duplicate source_id) before anything is applied: an " +
			"invalid entry aborts the whole batch, no move is applied. After applying the moves, " +
			"unless rewrite_links=false, the server rewrites in a single pass every inbound wiki-link " +
			"([[old-id]], [[old-id#section]]) and markdown link across the whole KB (including " +
			"services/) to point at the new IDs. Moving an expanded concept moves its whole directory, including assets and satellite concepts; inbound links to assets are intentionally left unchanged.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"source_id": {
					"type": "string",
					"description": "Concept ID of the concept to move (single-move form, mutually exclusive with 'moves')"
				},
				"target_id": {
					"type": "string",
					"description": "Destination concept ID, path relative to KB root without .md (single-move form, mutually exclusive with 'moves')"
				},
				"moves": {
					"type": "array",
					"description": "Batch form: list of {source_id, target_id} pairs. Mutually exclusive with source_id/target_id.",
					"items": {
						"type": "object",
						"required": ["source_id", "target_id"],
						"properties": {
							"source_id": {"type": "string"},
							"target_id": {"type": "string"}
						}
					}
				},
				"rewrite_links": {
					"type": "boolean",
					"description": "Rewrite inbound wiki-links and markdown links across the KB to the new IDs. Default true."
				}
			}
		}`),
		Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
			var params struct {
				SourceID     string             `json:"source_id"`
				TargetID     string             `json:"target_id"`
				Moves        []conceptMoveEntry `json:"moves"`
				RewriteLinks *bool              `json:"rewrite_links"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return errorResult("invalid params: " + err.Error()), nil
			}

			hasSingle := params.SourceID != "" || params.TargetID != ""
			hasBatch := len(params.Moves) > 0
			if hasSingle && hasBatch {
				return errorResult("cannot mix 'moves' batch with top-level source_id/target_id"), nil
			}

			var moves []conceptMoveEntry
			switch {
			case hasBatch:
				moves = params.Moves
			case hasSingle:
				if params.SourceID == "" {
					return errorResult("'source_id' is required"), nil
				}
				if params.TargetID == "" {
					return errorResult("'target_id' is required"), nil
				}
				moves = []conceptMoveEntry{{SourceID: params.SourceID, TargetID: params.TargetID}}
			}
			if len(moves) == 0 {
				return errorResult("'moves' (batch) or 'source_id'+'target_id' (single) is required, and 'moves' cannot be empty"), nil
			}

			rewriteLinks := true
			if params.RewriteLinks != nil {
				rewriteLinks = *params.RewriteLinks
			}

			// --- validation pass: every entry must pass before anything is applied. ---
			type validMove struct {
				sourceID string
				targetID string
				fm       *okf.Frontmatter
				body     string
				expanded bool
				mappings map[string]string
			}
			seenSources := map[string]bool{}
			seenTargets := map[string]bool{}
			valid := make([]validMove, 0, len(moves))

			for _, m := range moves {
				if m.SourceID == "" {
					return errorResult("'source_id' is required for every move"), nil
				}
				if m.TargetID == "" {
					return errorResult("'target_id' is required for every move"), nil
				}
				if seenSources[m.SourceID] {
					return errorResult("duplicate source_id in batch: " + m.SourceID), nil
				}
				seenSources[m.SourceID] = true
				if seenTargets[m.TargetID] {
					return errorResult("duplicate target_id in batch: " + m.TargetID), nil
				}
				seenTargets[m.TargetID] = true
				if _, err := okf.PathToID(m.TargetID + ".md"); err != nil {
					return errorResult("invalid target_id: " + m.TargetID), nil
				}

				// Path traversal check (concept IDs are anchored at the data root).
				targetAbs := filepath.Clean(filepath.Join(k.DataRoot(), m.TargetID+".md"))
				if !strings.HasPrefix(targetAbs, filepath.Clean(k.DataRoot())+string(filepath.Separator)) {
					return errorResult("target_id resolves outside KB root: " + m.TargetID), nil
				}

				data, err := k.ReadConcept(okf.ConceptID(m.SourceID))
				if err != nil {
					return errorResult(fmt.Sprintf("concept_move: read source %q: %v", m.SourceID, err)), nil
				}

				// Check that target does not already exist to prevent silent overwrite.
				if _, terr := k.ReadConcept(okf.ConceptID(m.TargetID)); terr == nil {
					return errorResult("conflict: target already exists: " + m.TargetID), nil
				} else if !errors.Is(terr, okf.ErrNotFound) {
					return errorResult(fmt.Sprintf("concept_move: check target %q: %v", m.TargetID, terr)), nil
				}

				fm, err := okf.ParseFrontmatter(data.FrontmatterRaw)
				if err != nil {
					return errorResult(fmt.Sprintf("concept_move: parse frontmatter %q: %v", m.SourceID, err)), nil
				}

				vm := validMove{sourceID: m.SourceID, targetID: m.TargetID, fm: fm, body: data.Body, mappings: map[string]string{m.SourceID: m.TargetID}}
				if _, indexErr := k.ReadRaw(filepath.Join(m.SourceID, "index.md")); indexErr == nil {
					if len(strings.Split(m.SourceID, "/")) != 2 || len(strings.Split(m.TargetID, "/")) != 2 {
						return errorResult("expanded concept moves require two-segment source_id and target_id"), nil
					}
					if strings.HasPrefix(m.TargetID+"/", m.SourceID+"/") || strings.HasPrefix(m.SourceID+"/", m.TargetID+"/") {
						return errorResult("expanded concept target cannot be inside, above, or equal to its source"), nil
					}
					targetDir := filepath.Join(k.DataRoot(), m.TargetID)
					if _, statErr := os.Lstat(targetDir); statErr == nil {
						return errorResult("conflict: target directory already exists: " + m.TargetID), nil
					} else if !os.IsNotExist(statErr) {
						return errorResult(fmt.Sprintf("concept_move: check target directory %q: %v", m.TargetID, statErr)), nil
					}
					vm.expanded = true
					if err := k.WalkConcepts(func(id okf.ConceptID, _ string) error {
						idStr := string(id)
						if idStr == m.SourceID || strings.HasPrefix(idStr, m.SourceID+"/") {
							vm.mappings[idStr] = m.TargetID + strings.TrimPrefix(idStr, m.SourceID)
						}
						return nil
					}); err != nil {
						return errorResult(fmt.Sprintf("concept_move: list expanded source %q: %v", m.SourceID, err)), nil
					}
				}

				valid = append(valid, vm)
			}
			for i, left := range valid {
				if !left.expanded {
					continue
				}
				for j, right := range valid {
					if i == j {
						continue
					}
					if strings.HasPrefix(right.sourceID+"/", left.sourceID+"/") || strings.HasPrefix(left.sourceID+"/", right.sourceID+"/") ||
						strings.HasPrefix(right.targetID+"/", left.sourceID+"/") || strings.HasPrefix(left.targetID+"/", right.sourceID+"/") ||
						strings.HasPrefix(right.sourceID+"/", left.targetID+"/") || strings.HasPrefix(left.sourceID+"/", right.targetID+"/") {
						return errorResult("expanded concept moves cannot overlap, swap, or use ancestor/descendant paths in one batch"), nil
					}
				}
			}

			// --- apply pass: all entries already validated above. ---
			moveMap := make(map[string]string, len(valid))
			applied := make([]conceptMoveEntry, 0, len(valid))
			logLines := make([]string, 0, len(valid)+1)

			for _, mv := range valid {
				if mv.expanded {
					srcDir := filepath.Join(k.DataRoot(), mv.sourceID)
					targetDir := filepath.Join(k.DataRoot(), mv.targetID)
					if err := os.MkdirAll(filepath.Dir(targetDir), 0o755); err != nil {
						return errorResult(fmt.Sprintf("concept_move: create target parent %q: %v", mv.targetID, err)), nil
					}
					if err := os.Rename(srcDir, targetDir); err != nil {
						return errorResult(fmt.Sprintf("concept_move: move expanded source %q: %v", mv.sourceID, err)), nil
					}
				} else {
					if _, err := k.WriteConcept(okf.ConceptID(mv.targetID), mv.fm, mv.body, ""); err != nil {
						return errorResult(fmt.Sprintf("concept_move: write target %q: %v", mv.targetID, err)), nil
					}

					srcPath := filepath.Join(k.DataRoot(), mv.sourceID+".md")
					if err := os.Remove(srcPath); err != nil && !os.IsNotExist(err) {
						return errorResult(fmt.Sprintf("concept_move: remove source %q: %v", mv.sourceID, err)), nil
					}
				}

				// Keep the keyword and FTS5 indexes in sync: deindex the old ID and
				// index the new one, same pattern as concept_delete/concept_write.
				for oldID, newID := range mv.mappings {
					live.remove(oldID)
					if targetData, readErr := k.ReadConcept(okf.ConceptID(newID)); readErr == nil {
						live.add(newID, targetData.Content)
						if sqlIdx != nil {
							if err := sqlIdx.Delete(oldID); err != nil {
								fmt.Fprintf(os.Stderr, "concept_move: sqlindex delete %q: %v\n", oldID, err)
							}
							if err := sqlIdx.Upsert(newID, targetData.ContentHash, targetData.Content); err != nil {
								fmt.Fprintf(os.Stderr, "concept_move: sqlindex upsert %q: %v\n", newID, err)
							}
						}
					}
					moveMap[oldID] = newID
				}
				applied = append(applied, conceptMoveEntry{SourceID: mv.sourceID, TargetID: mv.targetID})
				logLines = append(logLines, fmt.Sprintf("- %s → %s", mv.sourceID, mv.targetID))
			}

			result := map[string]interface{}{
				"moves": applied,
			}

			if rewriteLinks {
				touched, totalReplacements, err := rewriteBacklinks(k, live, sqlIdx, moveMap)
				if err != nil {
					// Moves are already applied (and will still be committed by
					// gitWrap only on success); surface the rewrite failure so the
					// caller knows some backlinks may be stale.
					return errorResult(fmt.Sprintf("concept_move: applied %d move(s) but rewrite_links failed: %v", len(applied), err)), nil
				}
				result["rewritten"] = touched
				if len(touched) > 0 {
					logLines = append(logLines, fmt.Sprintf("rewrite_links: %d concept(s), %d replacement(s)", len(touched), totalReplacements))
				}
			} else {
				var warnings []string
				for _, mv := range valid {
					warnings = append(warnings, fmt.Sprintf("Warning: inbound links to %s are not updated — run lint to find broken links", mv.sourceID))
				}
				result["warning"] = strings.Join(warnings, "\n")
			}

			_ = k.AppendLog(fmt.Sprintf("concept_move (%d move(s)):\n%s", len(applied), strings.Join(logLines, "\n")), time.Now())

			out, _ := json.MarshalIndent(result, "", "  ")
			return textResult(string(out)), nil
		},
	}
}

// rewriteBacklinks performs a single WalkConcepts pass over the whole KB
// (post-move state, so a moved concept is walked at its new location) and
// rewrites every markdown/wiki-link in every concept whose resolved target
// is a key in moveMap (old ID → new ID, D72 WP1). Concepts with at least one
// replacement are written back through kb.WriteConcept — if_match is the
// content-hash just read from WalkConcepts, guarding against a concurrent
// external write — and re-indexed (live + sqlIdx upsert), same pattern as
// the concept_write handler. Content writes are never best-effort: the first
// write failure aborts the pass and is returned as an error. Index upserts
// are best-effort (logged to stderr, never fail the pass). Returns the list
// of touched concepts and the total number of replacements performed.
func rewriteBacklinks(k *kb.KB, live *liveIndex, sqlIdx *sqlindex.Index, moveMap map[string]string) ([]rewrittenConcept, int, error) {
	var touched []rewrittenConcept
	total := 0

	err := k.WalkConcepts(func(id okf.ConceptID, content string) error {
		fmRaw, body, _ := okf.SplitFrontmatter(content)
		basePath := okf.IDToPath(id)
		if _, err := k.ReadRaw(filepath.Join(string(id), "index.md")); err == nil {
			basePath = filepath.Join(string(id), "index.md")
		}
		newBody, count := kb.RewriteLinks(body, basePath, moveMap)
		if count == 0 {
			return nil
		}

		fm, err := okf.ParseFrontmatter(fmRaw)
		if err != nil {
			return fmt.Errorf("parse frontmatter %q: %w", id, err)
		}

		ifMatch := okf.ContentHash(content)
		if _, err := k.WriteConcept(id, fm, newBody, ifMatch); err != nil {
			return fmt.Errorf("write %q: %w", id, err)
		}

		if data, readErr := k.ReadConcept(id); readErr == nil {
			live.add(string(id), data.Content)
			if sqlIdx != nil {
				if err := sqlIdx.Upsert(string(id), data.ContentHash, data.Content); err != nil {
					fmt.Fprintf(os.Stderr, "concept_move: rewrite_links: sqlindex upsert %q: %v\n", id, err)
				}
			}
		}

		touched = append(touched, rewrittenConcept{ID: string(id), Replacements: count})
		total += count
		return nil
	})
	if err != nil {
		return touched, total, err
	}

	return touched, total, nil
}

// --- concept_batch ---

// conceptBatchMaxOps bounds the number of operations in one concept_batch
// call; conceptBatchMaxTotalBytes bounds the aggregate decoded size (final
// frontmatter + body, summed across every operation) — both conservative,
// named limits so a runaway batch is rejected deterministically during
// preflight rather than after partially touching the filesystem (D125 WP1).
const (
	conceptBatchMaxOps = 50
	// conceptBatchMaxTotalBytes is kept comfortably under the stdio
	// transport's 1 MiB max JSON-RPC line (server.go's scanner buffer): the
	// full request, including this content plus per-operation JSON
	// structure/escaping, must still fit in one line.
	conceptBatchMaxTotalBytes = 512 * 1024 // 512 KiB
)

// batchOperationRequest is one entry of concept_batch's "operations" array:
// "write" mirrors concept_write's frontmatter/body/if_match triple (if_match
// absent means create-only; updating an existing concept requires it);
// "patch" mirrors concept_patch's required if_match, optional frontmatter
// shallow merge, and single/batch old_string/new_string/edits Edit-tool
// semantics (D125 WP1).
type batchOperationRequest struct {
	Op          string                 `json:"op"`
	ID          string                 `json:"id"`
	Frontmatter map[string]interface{} `json:"frontmatter"`
	Body        string                 `json:"body"`
	IfMatch     string                 `json:"if_match"`
	OldString   string                 `json:"old_string"`
	NewString   string                 `json:"new_string"`
	ReplaceAll  bool                   `json:"replace_all"`
	Edits       []patchEditItem        `json:"edits"`
}

// batchOriginal is one target's pre-batch state, captured during preflight —
// used only to reconcile the live/SQLite search indexes back to that exact
// state if a later step in the same call fails (D125 WP2).
type batchOriginal struct {
	existed bool
	content string
	hash    string
}

// batchResultEntry is one applied operation's reported outcome, in request order.
type batchResultEntry struct {
	ID          string `json:"id"`
	ContentHash string `json:"content_hash"`
}

func toolConceptBatch(k *kb.KB, live *liveIndex, sqlIdx *sqlindex.Index) Tool {
	return Tool{
		Name: "concept_batch",
		Description: "Atomically writes or patches several distinct concepts in one logical operation " +
			"(one git commit, one summary log.md entry): either every operation in 'operations' is applied " +
			"or none is — a failure at any point (validation, a stale/missing if_match, a write, or an index " +
			"update) leaves the KB exactly as it was before the call. Intended for large multi-page refactors " +
			"where separate concept_write/concept_patch calls would leave partially-aligned intermediate " +
			"commits if interrupted; for edits confined to one concept use concept_patch's own 'edits' batch " +
			"instead, and for renames use concept_move. Each operation is 'write' (frontmatter, body, optional " +
			"if_match — absent if_match means create-only; updating an existing concept requires it) or " +
			"'patch' (required if_match, optional frontmatter shallow merge, and the same single " +
			"old_string/new_string/replace_all or batch 'edits' semantics as concept_patch). Operations must " +
			"target distinct concept IDs; delete, move, expand, assets, and Map/root curated indexes are out " +
			"of scope for this tool. Every operation is validated — including each Map's strict-ontology " +
			"palette and required-field contract — before anything is written. Returns each operation's id " +
			"and new content_hash in request order.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"required": ["operations"],
			"properties": {
				"operations": {
					"type": "array",
					"description": "Ordered list of operations over distinct concept IDs, applied atomically.",
					"items": {
						"type": "object",
						"required": ["op", "id"],
						"properties": {
							"op": {"type": "string", "description": "\"write\" or \"patch\""},
							"id": {"type": "string", "description": "ConceptID (path relative to KB root without .md)"},
							"frontmatter": {"type": "object", "description": "Full frontmatter (write) or partial shallow-merge (patch, optional)"},
							"body": {"type": "string", "description": "Full markdown body (write only)"},
							"if_match": {"type": "string", "description": "Expected content-hash: optional (create-only) for write, required for patch"},
							"old_string": {"type": "string", "description": "Patch: exact substring to find (single-edit form, mutually exclusive with 'edits')"},
							"new_string": {"type": "string", "description": "Patch: replacement text (single-edit form, mutually exclusive with 'edits')"},
							"replace_all": {"type": "boolean", "description": "Patch: replace all occurrences of old_string (single-edit form)"},
							"edits": {
								"type": "array",
								"description": "Patch: batch form, applied atomically and in order. Mutually exclusive with old_string/new_string/replace_all.",
								"items": {
									"type": "object",
									"required": ["old_string", "new_string"],
									"properties": {
										"old_string": {"type": "string"},
										"new_string": {"type": "string"},
										"replace_all": {"type": "boolean"}
									}
								}
							}
						}
					}
				}
			}
		}`),
		Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
			var params struct {
				Operations []json.RawMessage `json:"operations"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return errorResult("invalid params: " + err.Error()), nil
			}
			if len(params.Operations) == 0 {
				return errorResult("'operations' cannot be empty"), nil
			}
			if len(params.Operations) > conceptBatchMaxOps {
				return errorResult(fmt.Sprintf("'operations' has %d entries, exceeding the max of %d", len(params.Operations), conceptBatchMaxOps)), nil
			}

			ops := make([]kb.BatchWriteOp, 0, len(params.Operations))
			originals := make(map[string]batchOriginal, len(params.Operations))
			seen := make(map[string]bool, len(params.Operations))
			totalBytes := 0

			for i, raw := range params.Operations {
				label := fmt.Sprintf("operation %d of %d", i+1, len(params.Operations))

				var op batchOperationRequest
				if err := json.Unmarshal(raw, &op); err != nil {
					return errorResult(fmt.Sprintf("%s: invalid params: %v", label, err)), nil
				}
				if op.ID == "" {
					return errorResult(fmt.Sprintf("%s: 'id' is required", label)), nil
				}
				if id, err := okf.PathToID(op.ID + ".md"); err != nil || string(id) != op.ID {
					return errorResult(fmt.Sprintf("%s (%s): invalid ConceptID (use kebab-case path segments)", label, op.ID)), nil
				}
				if seen[op.ID] {
					return errorResult(fmt.Sprintf("%s (%s): duplicate id in batch", label, op.ID)), nil
				}
				seen[op.ID] = true
				label = fmt.Sprintf("%s (%s)", label, op.ID)

				var rawFields map[string]json.RawMessage
				_ = json.Unmarshal(raw, &rawFields)
				_, hasEdits := rawFields["edits"]
				hasSingle := op.OldString != "" || op.NewString != "" || op.ReplaceAll

				existing, readErr := k.ReadConcept(okf.ConceptID(op.ID))
				existed := readErr == nil
				if readErr != nil && !errors.Is(readErr, okf.ErrNotFound) {
					return errorResult(fmt.Sprintf("%s: %v", label, readErr)), nil
				}

				var fm *okf.Frontmatter
				var body string

				switch op.Op {
				case "write":
					if hasEdits || hasSingle {
						return errorResult(fmt.Sprintf("%s: 'old_string'/'new_string'/'replace_all'/'edits' are patch-only fields", label)), nil
					}
					if op.Frontmatter == nil {
						return errorResult(fmt.Sprintf("%s: 'frontmatter' is required for a write operation", label)), nil
					}
					if existed && op.IfMatch == "" {
						return errorResult(fmt.Sprintf("%s: if_match is required to update an existing concept", label)), nil
					}
					if op.IfMatch != "" {
						if !existed {
							return errorResult(fmt.Sprintf("stale_write: %s: file not found", label)), nil
						}
						if existing.ContentHash != op.IfMatch {
							return errorResult(fmt.Sprintf("stale_write: %s: content_hash does not match if_match", label)), nil
						}
					}
					var err error
					fm, err = okf.ParseFrontmatter("")
					if err != nil {
						return errorResult(fmt.Sprintf("%s: internal frontmatter error: %v", label, err)), nil
					}
					applyFrontmatterMap(fm, op.Frontmatter)
					body = op.Body

				case "patch":
					if op.IfMatch == "" {
						return errorResult(fmt.Sprintf("%s: 'if_match' is required for a patch operation", label)), nil
					}
					if !existed {
						return errorResult(fmt.Sprintf("%s: not found", label)), nil
					}
					if existing.ContentHash != op.IfMatch {
						return errorResult(fmt.Sprintf("stale_write: %s: content_hash does not match if_match", label)), nil
					}
					if hasEdits && hasSingle {
						return errorResult(fmt.Sprintf("%s: 'edits' is mutually exclusive with top-level 'old_string'/'new_string'/'replace_all'", label)), nil
					}
					if !hasEdits && !hasSingle {
						return errorResult(fmt.Sprintf("%s: 'old_string' is required (or provide 'edits' for a batch of edits)", label)), nil
					}
					if hasEdits && len(op.Edits) == 0 {
						return errorResult(fmt.Sprintf("%s: 'edits' cannot be empty", label)), nil
					}
					if !hasEdits && op.OldString == "" {
						return errorResult(fmt.Sprintf("%s: 'old_string' is required", label)), nil
					}

					body = existing.Body
					if hasEdits {
						for ei, e := range op.Edits {
							if e.OldString == "" {
								return errorResult(fmt.Sprintf("%s, edit %d of %d: 'old_string' is required", label, ei+1, len(op.Edits))), nil
							}
							newBody, _, editErr := applyPatchEdit(body, e.OldString, e.NewString, e.ReplaceAll)
							if editErr != nil {
								return errorResult(fmt.Sprintf("%s, edit %d of %d: %v", label, ei+1, len(op.Edits), editErr)), nil
							}
							body = newBody
						}
					} else {
						newBody, _, editErr := applyPatchEdit(body, op.OldString, op.NewString, op.ReplaceAll)
						if editErr != nil {
							return errorResult(fmt.Sprintf("%s: %v", label, editErr)), nil
						}
						body = newBody
					}

					var err error
					fm, err = okf.ParseFrontmatter(existing.FrontmatterRaw)
					if err != nil {
						return errorResult(fmt.Sprintf("%s: parse frontmatter: %v", label, err)), nil
					}
					if op.Frontmatter != nil {
						applyFrontmatterMap(fm, op.Frontmatter)
					}

				case "":
					return errorResult(fmt.Sprintf("%s: 'op' is required (\"write\" or \"patch\")", label)), nil
				default:
					return errorResult(fmt.Sprintf("%s: invalid 'op' %q (must be \"write\" or \"patch\")", label, op.Op)), nil
				}

				if fm.Type() == "" {
					return errorResult(fmt.Sprintf("%s: type field is required", label)), nil
				}
				if err := mapContractViolation(k, op.ID, fm); err != nil {
					return errorResult(fmt.Sprintf("%s: %v", label, err)), nil
				}

				totalBytes += len(body) + len(fm.Serialize())
				if totalBytes > conceptBatchMaxTotalBytes {
					return errorResult(fmt.Sprintf("%s: aggregate batch content exceeds %d bytes", label, conceptBatchMaxTotalBytes)), nil
				}

				orig := batchOriginal{existed: existed}
				if existed {
					orig.content = existing.Content
					orig.hash = existing.ContentHash
				}
				originals[op.ID] = orig
				ops = append(ops, kb.BatchWriteOp{ID: okf.ConceptID(op.ID), FM: fm, Body: body, IfMatch: op.IfMatch})
			}

			logMessage := fmt.Sprintf("concept_batch (%d operation(s)):\n%s", len(ops), strings.Join(batchLogLines(ops), "\n"))

			// Index updates run only after every file and the log entry are
			// committed (WriteConceptBatch guarantees that ordering). A
			// failure here reconciles every already-applied index entry back
			// to its pre-batch state before returning, so by the time
			// WriteConceptBatch rolls the files back, both indexes already
			// agree with the tree it is restoring (D125 WP2).
			afterFiles := func(results []kb.BatchWriteResult) error {
				applied := make([]string, 0, len(results))
				for _, r := range results {
					data, readErr := k.ReadConcept(okf.ConceptID(r.ID))
					if readErr != nil {
						reconcileBatchIndex(live, sqlIdx, originals, applied)
						return fmt.Errorf("reindex %q: %w", r.ID, readErr)
					}
					live.add(r.ID, data.Content)
					applied = append(applied, r.ID)
					if sqlIdx != nil {
						if err := sqlIdx.Upsert(r.ID, data.ContentHash, data.Content); err != nil {
							reconcileBatchIndex(live, sqlIdx, originals, applied)
							return fmt.Errorf("sqlindex upsert %q: %w", r.ID, err)
						}
					}
				}
				return nil
			}

			results, err := k.WriteConceptBatch(ops, logMessage, afterFiles)
			if err != nil {
				if errors.Is(err, okf.ErrStaleWrite) {
					return errorResult("stale_write: " + err.Error()), nil
				}
				return errorResult("concept_batch: " + err.Error()), nil
			}

			entries := make([]batchResultEntry, len(results))
			for i, r := range results {
				entries[i] = batchResultEntry{ID: r.ID, ContentHash: r.ContentHash}
			}
			out, _ := json.MarshalIndent(map[string]interface{}{"results": entries}, "", "  ")
			return textResult(string(out)), nil
		},
	}
}

// batchLogLines renders one "- <id>" line per applied operation for the
// batch's single summary log.md entry (D125 WP2: one entry for the whole
// call, not one per concept, mirroring concept_move's summary line).
func batchLogLines(ops []kb.BatchWriteOp) []string {
	lines := make([]string, len(ops))
	for i, op := range ops {
		lines[i] = "- " + string(op.ID)
	}
	return lines
}

// reconcileBatchIndex reverts every already-applied live/SQLite index update
// for the given ids back to their pre-batch state (originals) — called when
// a later id in the same afterFiles pass fails, so both indexes end up
// consistent with the pre-call tree that WriteConceptBatch is about to
// restore the files to (D125 WP2). Best-effort on the SQLite side, same as
// every other write path: the persisted index is documented as
// rebuildable/disposable (control-plane.md §Search index) so `reindex` can
// always repair it, but the in-memory live index update itself cannot fail.
func reconcileBatchIndex(live *liveIndex, sqlIdx *sqlindex.Index, originals map[string]batchOriginal, ids []string) {
	for _, id := range ids {
		orig := originals[id]
		if !orig.existed {
			live.remove(id)
			if sqlIdx != nil {
				if err := sqlIdx.Delete(id); err != nil {
					fmt.Fprintf(os.Stderr, "concept_batch: rollback reindex delete %q: %v\n", id, err)
				}
			}
			continue
		}
		live.add(id, orig.content)
		if sqlIdx != nil {
			if err := sqlIdx.Upsert(id, orig.hash, orig.content); err != nil {
				fmt.Fprintf(os.Stderr, "concept_batch: rollback reindex upsert %q: %v\n", id, err)
			}
		}
	}
}

// mapContractViolation checks a proposed concept id/frontmatter against its
// Map's strict-ontology palette and required-field contract — the same
// checks kb.Validate's inline ontology pass and lint's missing_required_field
// finding perform after a write. concept_batch's preflight (D125 WP1) must
// catch them before any file changes: v1 offers no per-operation recovery
// mid-batch beyond an all-or-nothing preflight rejection. A concept outside
// any map (top-level, or a map with no descriptor) has nothing to enforce.
func mapContractViolation(k *kb.KB, id string, fm *okf.Frontmatter) error {
	parts := strings.SplitN(id, "/", 2)
	if len(parts) < 2 {
		return nil
	}
	archiveName := parts[0]
	meta, err := k.ReadArchiveMeta(archiveName)
	if err != nil {
		return nil
	}
	conceptType := fm.Type()
	if modeVal, ok := meta.Get("ontology_mode"); ok {
		if modeStr, _ := modeVal.(string); modeStr == "strict" {
			if ctVal, ok := meta.Get("concept_types"); ok {
				if ctList, ok := ctVal.([]string); ok {
					allowed := make(map[string]bool, len(ctList))
					for _, ct := range ctList {
						allowed[ct] = true
					}
					if !allowed[conceptType] {
						return fmt.Errorf("type %q not allowed in map %s (strict)", conceptType, archiveName)
					}
				}
			}
		}
	}
	contract, err := k.ReadMapContract(archiveName)
	if err != nil {
		return nil
	}
	for _, field := range contract.RequiredFor(conceptType) {
		if _, ok := fm.Get(field); !ok {
			return fmt.Errorf("missing required field %q for map %s", field, archiveName)
		}
	}
	return nil
}

// --- concept_delete ---

func toolConceptDelete(k *kb.KB, live *liveIndex, sqlIdx *sqlindex.Index) Tool {
	return Tool{
		Name: "concept_delete",
		Description: "Permanently removes a concept from the KB (git commit). Inbound links " +
			"to the removed concept are NOT updated — run lint to find broken links. Deleting an expanded concept that owns assets requires force=true; satellite concepts are preserved.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"required": ["id"],
			"properties": {
				"id": {
					"type": "string",
					"description": "ConceptID (path relative to KB root without .md)"
				},
				"if_match": {
					"type": "string",
					"description": "Expected content-hash (optional, for optimistic concurrency)"
				},
				"force": {
					"type": "boolean",
					"description": "Required to delete the non-Markdown assets owned by an expanded concept"
				}
			}
		}`),
		Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
			var params struct {
				ID      string `json:"id"`
				IfMatch string `json:"if_match"`
				Force   bool   `json:"force"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return errorResult("invalid params: " + err.Error()), nil
			}
			if params.ID == "" {
				return errorResult("'id' is required"), nil
			}

			if params.IfMatch != "" {
				data, err := k.ReadConcept(okf.ConceptID(params.ID))
				if err != nil {
					if errors.Is(err, okf.ErrNotFound) {
						return errorResult(fmt.Sprintf("concept_delete %q: not found", params.ID)), nil
					}
					return errorResult(fmt.Sprintf("concept_delete %q: %v", params.ID, err)), nil
				}
				if data.ContentHash != params.IfMatch {
					return errorResult("stale_write: content_hash does not match if_match"), nil
				}
			}

			if _, err := k.DeleteConceptWithAssets(okf.ConceptID(params.ID), params.Force); err != nil {
				if errors.Is(err, okf.ErrNotFound) {
					return errorResult(fmt.Sprintf("concept_delete %q: not found", params.ID)), nil
				}
				return errorResult(fmt.Sprintf("concept_delete %q: %v", params.ID, err)), nil
			}

			live.remove(params.ID)
			if sqlIdx != nil {
				if err := sqlIdx.Delete(params.ID); err != nil {
					fmt.Fprintf(os.Stderr, "concept_delete: sqlindex delete %q: %v\n", params.ID, err)
				}
			}

			_ = k.AppendLog("concept_delete: "+params.ID, time.Now())
			msg := fmt.Sprintf("deleted %s", params.ID)
			msg += fmt.Sprintf("\nWarning: inbound links to %s are not updated — run lint to find broken links", params.ID)
			return textResult(msg), nil
		},
	}
}
