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
	"github.com/BeppeTemp/cartographer/internal/lint"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// --- concept_write ---

func toolConceptWrite(k *kb.KB, sim *similarFinder, facts *factFinder) Tool {
	return Tool{
		Name:        "concept_write",
		Description: "Creates or updates a concept from frontmatter (YAML map, type required) and a markdown body. if_match (content hash) gives optimistic concurrency: stale_write if changed. Returns content_hash and structural findings too (links, orphan, index): fix them.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"required": ["id", "frontmatter", "body"],
			"properties": {
				"id": {
					"type": "string"
				},
				"frontmatter": {
					"type": "object",
					"description": "Map; type required"
				},
				"body": {
					"type": "string"
				},
				"if_match": {
					"type": "string",
					"description": "Optional content hash"
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
			if err := rejectToolParamKeys(params.Frontmatter, false); err != nil {
				return errorResult("concept_write: " + err.Error()), nil
			}

			// Build a structured Frontmatter from a JSON map.
			fm, err := okf.ParseFrontmatter("")
			if err != nil {
				return errorResult("internal frontmatter error: " + err.Error()), nil
			}
			applyFrontmatterMap(fm, params.Frontmatter)

			isNew := conceptIsNew(k, params.ID)
			prev := priorBody(k, params.ID)
			newHash, err := writeConceptAndLog(k, "concept_write", params.ID, fm, params.Body, params.IfMatch)
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
			findings, repaired, hashes := repairWritten(k, []string{params.ID}, nil)
			findings = withFacts(findings, facts, ctx, params.ID, prev, newFactBudget())
			result["findings"] = findingsOrEmpty(findings)
			applyRepairResult(result, params.ID, repaired, hashes)
			if isNew {
				if similar := sim.find(ctx, params.ID, frontmatterTitle(k, params.ID)); len(similar) > 0 {
					result["similar"] = similar
				}
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
func toolConceptNew(k *kb.KB, sim *similarFinder, facts *factFinder) Tool {
	return Tool{
		Name:        "concept_new",
		Description: "Creates a concept from a KB template (template_list) and records it as its shape. Refuses an existing id. Returns findings.",
		InputSchema: json.RawMessage(`{
			"type":"object", "required":["template", "id"],
			"properties": {
				"template":{"type":"string"},
				"id":{"type":"string"},
				"vars":{"type":"object","additionalProperties":{"type":"string"}}
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
			// The template's schema describes the template, not the page (D352);
			// the page records which template it follows instead.
			for _, key := range fm.Keys() {
				if strings.HasPrefix(key, kb.TemplateMetaPrefix) {
					fm.Delete(key)
				}
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
			for _, key := range fm.Keys() {
				if lint.IsToolParamField(key) {
					return errorResult(fmt.Sprintf("concept_new: template %q: frontmatter key %q is a tool parameter, not a field — pass it as a top-level argument", params.Template, key)), nil
				}
			}
			// Stamped after rendering, over any shape the template carries: the
			// slug is the one the caller chose. A template outside the map's
			// templates is not refused; the write response carries
			// template_not_allowed (D352).
			fm.Set(kb.ShapeField, params.Template)
			newHash, err := writeConceptAndLog(k, "concept_new", params.ID, fm, body, "")
			if err != nil {
				return errorResult(fmt.Sprintf("concept_new %q: %v", params.ID, err)), nil
			}
			result := map[string]interface{}{"id": params.ID, "template": params.Template, "content_hash": newHash}
			findings, repaired, hashes := repairWritten(k, []string{params.ID}, nil)
			findings = withFacts(findings, facts, ctx, params.ID, "", newFactBudget())
			result["findings"] = findingsOrEmpty(findings)
			applyRepairResult(result, params.ID, repaired, hashes)
			if similar := sim.find(ctx, params.ID, frontmatterTitle(k, params.ID)); len(similar) > 0 {
				result["similar"] = similar
			}
			out, _ := json.MarshalIndent(result, "", "  ")
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

// writeConceptAndLog writes a concept via k.WriteConcept and appends its
// log.md line. Shared write-path for concept_write, concept_new and
// concept_patch (D70). It does no index work: the next search reconciles the
// indexes with the files (D245). logPrefix labels the log.md entry.
func writeConceptAndLog(k *kb.KB, logPrefix string, id string, fm *okf.Frontmatter, body string, ifMatch string) (string, error) {
	newHash, err := k.WriteConcept(okf.ConceptID(id), fm, body, ifMatch)
	if err != nil {
		return "", err
	}

	_ = k.AppendLog(logPrefix+": "+id, time.Now())
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
		return "", 0, errors.New("old_string_not_found: no match for old_string" + closestLineHint(body, oldString))
	}
	if count > 1 && !replaceAll {
		return "", 0, fmt.Errorf(
			"old_string_ambiguous: old_string matches %d times (lines %s); pass replace_all=true or provide more surrounding context",
			count, matchLines(body, oldString),
		)
	}
	if replaceAll {
		return strings.ReplaceAll(body, oldString, newString), count, nil
	}
	return strings.Replace(body, oldString, newString, 1), 1, nil
}

func toolConceptPatch(k *kb.KB, facts *factFinder) Tool {
	return Tool{
		Name: "concept_patch",
		Description: "Edit-style patch of a concept body: old_string/new_string/replace_all, or an edits array applied atomically in order. if_match required (stale_write). frontmatter is shallow-merged and may be the only change; frontmatter_append/frontmatter_remove add or drop list items (idempotent). Returns content_hash, findings. " +
			fmt.Sprintf("Many concepts: concept_batch, up to %d operations in one commit.", conceptBatchMaxOps),
		InputSchema: json.RawMessage(`{
			"type": "object",
			"required": ["id", "if_match"],
			"properties": {
				"id": {
					"type": "string"
				},
				"old_string": {
					"type": "string",
					"description": "Exact text to find (single form)"
				},
				"new_string": {
					"type": "string"
				},
				"replace_all": {
					"type": "boolean",
					"description": "Replace every match; default: must match once"
				},
				"edits": {
					"type": "array",
					"description": "Batch form; exclusive with the single form",
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
					"type": "string"
				},
				"frontmatter": {
					"type": "object",
					"description": "Keys to shallow-merge; null removes a key"
				},
				"frontmatter_append": {
					"type": "object"
				},
				"frontmatter_remove": {
					"type": "object"
				},
				"unset": {
					"type": "array",
					"items": {"type": "string"},
					"description": "Frontmatter keys to remove (same as a null value)"
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
				FMAppend    map[string]interface{} `json:"frontmatter_append"`
				FMRemove    map[string]interface{} `json:"frontmatter_remove"`
				Unset       []string               `json:"unset"`
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
			// A frontmatter-only patch (no body edit) is legitimate: setting a
			// missing title or fixing a type should not need a fake no-op edit
			// or a full concept_write of a body the caller did not touch (#321).
			pfm := patchFrontmatter{Merge: params.Frontmatter, Append: params.FMAppend, Remove: params.FMRemove, Unset: params.Unset}
			hasFM := pfm.any()
			if !hasEdits && !hasSingle && !hasFM {
				return errorResult("'old_string' is required (or provide 'edits' for a batch of edits, or 'frontmatter'/'frontmatter_append'/'frontmatter_remove'/'unset' alone)"), nil
			}
			if hasEdits && len(params.Edits) == 0 && !hasFM {
				return errorResult("'edits' cannot be empty"), nil
			}
			if hasSingle && params.OldString == "" {
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
			var editMatches []int
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
					editMatches = append(editMatches, n)
				}
			} else if hasSingle {
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
			// Merge, append, remove, then unset (which wins) — one helper shared
			// with concept_batch (D342).
			dropped, err := applyPatchFrontmatter(fm, pfm)
			if err != nil {
				return errorResult("concept_patch: " + err.Error()), nil
			}

			newHash, err := writeConceptAndLog(k, "concept_patch", params.ID, fm, body, params.IfMatch)
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
			if hasEdits && len(editMatches) > 0 {
				result["edit_matches"] = editMatches
			}
			findings, repaired, hashes := repairWritten(k, []string{params.ID}, nil)
			findings = append(findings, droppedFinding(params.ID, dropped)...)
			findings = withFacts(findings, facts, ctx, params.ID, data.Body, newFactBudget())
			result["findings"] = findingsOrEmpty(findings)
			applyRepairResult(result, params.ID, repaired, hashes)
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
		Name:        "index_patch",
		Description: "Like concept_patch, for the root or a Map/Journal curated index.md (same edit forms; if_match required, from index_get with_hash). An expanded concept's own index.md is refused (expanded_index): use concept_patch. Returns path, content_hash, replacements, findings.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"required": ["if_match"],
			"properties": {
				"path": {
					"type": "string",
					"description": "Empty (root) or a Map/Journal name"
				},
				"old_string": {
					"type": "string"
				},
				"new_string": {
					"type": "string"
				},
				"replace_all": {
					"type": "boolean"
				},
				"edits": {
					"type": "array",
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
					"type": "string"
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
			original := content

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

			if msg := generatedBlockEdited(k, normalizeIndexPath(params.Path), original, content); msg != "" {
				return errorResult(msg), nil
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
			// D312: the concepts this patch took out of the index may now be
			// missing from it (index_incomplete).
			if dropped := indexEntriesDropped(k, normalizedPath, original, content); len(dropped) > 0 {
				if f := scopedFindings(k, dropped, "index_incomplete"); f != nil {
					result["findings"] = f
				}
			}
			out, _ := json.MarshalIndent(result, "", "  ")
			return textResult(string(out)), nil
		},
	}
}

// indexEntriesDropped lists the existing concepts a curated index linked
// before an index_patch and no longer links after it (D312). The root index
// and an index whose map keeps it generated have no entries to lose.
func indexEntriesDropped(k *kb.KB, indexPath, before, after string) []string {
	if indexPath == "" {
		return nil
	}
	linked := func(content string) map[okf.ConceptID]bool {
		_, body, _ := okf.SplitFrontmatter(content)
		set := map[okf.ConceptID]bool{}
		for _, id := range kb.ExtractLinks(body, indexPath+"/index.md", k.AssetExists) {
			set[id] = true
		}
		return set
	}
	now := linked(after)
	var dropped []string
	for id := range linked(before) {
		if now[id] || !conceptExists(k, string(id)) {
			continue
		}
		dropped = append(dropped, string(id))
	}
	sort.Strings(dropped)
	return dropped
}

// conceptMoveRemove and conceptMoveRename are the two filesystem calls that
// complete a move after its target exists. They are variables only so a test
// can make them fail deterministically (a read-only directory does not stop
// root, nor Windows): production never reassigns them.
var (
	conceptMoveRemove = os.Remove
	conceptMoveRename = os.Rename
)

// --- map_create ---

func toolMapCreate(k *kb.KB) Tool {
	return Tool{
		Name:        "map_create",
		Description: "Creates a Map or Journal (kind: journal) with _map.md, index.md, log.md. ontology_mode: strict (enforces concept_types) or flexible. templates (slugs) make every page follow one. Contract options are lint only, not a write gate.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"required": ["name", "title"],
			"properties": {
				"name": {
					"type": "string"
				},
				"title": {
					"type": "string"
				},
				"kind": {
					"type": "string"
				},
				"concept_types": {
					"type": "array",
					"items": {"type": "string"}
				},
				"ontology_mode": {
					"type": "string"
				},
				"required_fields": {
					"type": "array",
					"items": {"type": "string"}
					},
				"required_fields_by_type": {
					"type": "object",
					"additionalProperties": {"type": "array", "items": {"type": "string"}}
					},
				"field_values": {
					"type": "object",
					"additionalProperties": {"type": "array", "items": {"type": "string"}}
					},
				"field_values_by_type": {
					"type": "object",
					"additionalProperties": {"type": "object", "additionalProperties": {"type": "array", "items": {"type": "string"}}}
					},
				"forbidden_fields": {
					"type": "array",
					"items": {"type": "string"}
					},
				"require_index_entry": {
					"type": "boolean"
					},
				"machine_path_allow_prefixes": {
					"type": "array",
					"items": {"type": "string"}
					},
				"templates": {"type": "array", "items": {"type": "string"}},
				"default_template": {"type": "string"}
			}
		}`),
		Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
			var params struct {
				Name                     string                         `json:"name"`
				Title                    string                         `json:"title"`
				Kind                     string                         `json:"kind"`
				ConceptTypes             []string                       `json:"concept_types"`
				OntologyMode             string                         `json:"ontology_mode"`
				RequiredFields           []string                       `json:"required_fields"`
				RequiredFieldsByType     map[string][]string            `json:"required_fields_by_type"`
				FieldValues              map[string][]string            `json:"field_values"`
				FieldValuesByType        map[string]map[string][]string `json:"field_values_by_type"`
				ForbiddenFields          []string                       `json:"forbidden_fields"`
				RequireIndexEntry        *bool                          `json:"require_index_entry"`
				MachinePathAllowPrefixes []string                       `json:"machine_path_allow_prefixes"`
				Templates                []string                       `json:"templates"`
				DefaultTemplate          string                         `json:"default_template"`
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
			if msg := validateContractParams(params.RequiredFields, params.RequiredFieldsByType, params.MachinePathAllowPrefixes); msg != "" {
				return errorResult(msg), nil
			}
			if msg := validateFieldValueParams(params.FieldValues, params.FieldValuesByType, params.ForbiddenFields); msg != "" {
				return errorResult(msg), nil
			}

			if msg := validateTemplateParams(params.Templates, params.DefaultTemplate); msg != "" {
				return errorResult(msg), nil
			}
			contract := kb.MapContract{
				Templates:            params.Templates,
				DefaultTemplate:      params.DefaultTemplate,
				RequireTemplate:      len(params.Templates) > 0, // a new map starts strict (D352)
				RequiredFields:       params.RequiredFields,
				RequiredFieldsByType: params.RequiredFieldsByType,
				FieldValues:          params.FieldValues,
				FieldValuesByType:    params.FieldValuesByType,
				ForbiddenFields:      params.ForbiddenFields,
				// Absent means on (D325): a concept nobody links from its map's
				// index is an orphan within one lint run. Only an explicit
				// false opts out.
				RequireIndexEntry:        params.RequireIndexEntry == nil || *params.RequireIndexEntry,
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

// validateTemplateParams rejects a templates / default_template pair a map
// contract cannot hold (D352): slugs only, and the default one of the list.
// Shared by map_create and map_update.
func validateTemplateParams(templates []string, def string) string {
	for _, slug := range templates {
		if !kb.ValidTemplateSlug(slug) {
			return fmt.Sprintf("'templates': %q is not a template slug (lowercase words and hyphens)", slug)
		}
	}
	if def == "" {
		return ""
	}
	if !kb.ValidTemplateSlug(def) {
		return fmt.Sprintf("'default_template': %q is not a template slug (lowercase words and hyphens)", def)
	}
	for _, slug := range templates {
		if slug == def {
			return ""
		}
	}
	return "'default_template' must be one of 'templates'"
}

// validateContractParams rejects the empty entries a map contract must not
// carry. Shared by map_create and map_update so the two cannot drift; returns
// "" when the input is acceptable.
func validateContractParams(requiredFields []string, byType map[string][]string, allowPrefixes []string) string {
	for _, field := range requiredFields {
		if strings.TrimSpace(field) == "" {
			return "'required_fields' must not contain empty field names"
		}
	}
	for typ, fields := range byType {
		if strings.TrimSpace(typ) == "" {
			return "'required_fields_by_type' must not contain an empty type name"
		}
		for _, field := range fields {
			if strings.TrimSpace(field) == "" {
				return "'required_fields_by_type' must not contain empty field names"
			}
		}
	}
	for _, prefix := range allowPrefixes {
		if strings.TrimSpace(prefix) == "" {
			return "'machine_path_allow_prefixes' must not contain empty entries"
		}
	}
	return ""
}

// validateFieldValueParams rejects the malformed entries of the D275 contract
// keys (empty field or type names, empty or blank allowed values, empty
// forbidden names). An empty list inside an update is a removal only at the
// whole-key level, so a per-field empty list is refused here as it is when read.
func validateFieldValueParams(values map[string][]string, byType map[string]map[string][]string, forbidden []string) string {
	check := func(label, field string, vals []string) string {
		if strings.TrimSpace(field) == "" || strings.Contains(field, ".") {
			return "'" + label + "' field names must be non-empty and contain no '.'"
		}
		if len(vals) == 0 {
			return "'" + label + "' needs at least one allowed value for " + fmt.Sprintf("%q", field)
		}
		for _, v := range vals {
			if strings.TrimSpace(v) == "" {
				return "'" + label + "' must not contain empty values"
			}
		}
		return ""
	}
	for field, vals := range values {
		if msg := check("field_values", field, vals); msg != "" {
			return msg
		}
	}
	for typ, fields := range byType {
		if strings.TrimSpace(typ) == "" || strings.Contains(typ, ".") {
			return "'field_values_by_type' type names must be non-empty and contain no '.'"
		}
		for field, vals := range fields {
			if msg := check("field_values_by_type", field, vals); msg != "" {
				return msg
			}
		}
	}
	for _, f := range forbidden {
		if strings.TrimSpace(f) == "" {
			return "'forbidden_fields' must not contain empty field names"
		}
	}
	return ""
}

// generatedBlockEdited refuses an index_patch that changes the block the
// server maintains in an `index: generated` map (D301): the edit would be
// overwritten by the next write anyway. Text outside the block is free.
func generatedBlockEdited(k *kb.KB, mapName, before, after string) string {
	if mapName == "" {
		return ""
	}
	contract, err := k.ReadMapContract(mapName)
	if err != nil || contract.Index != kb.IndexGenerated {
		return ""
	}
	was, ok := kb.IndexBlock(before)
	if !ok {
		return ""
	}
	if now, ok := kb.IndexBlock(after); ok && now == was {
		return ""
	}
	return fmt.Sprintf("generated_index: map %q has index: generated; its concept list between the cartographer:index markers is maintained by the server, edit only outside the block", mapName)
}

// --- map_update ---

func toolMapUpdate(k *kb.KB) Tool {
	return Tool{
		Name:        "map_update",
		Description: "Changes a map's title or lint contract: given keys are replaced whole, an empty list, \"\", false or {} removes one. stale_after 0 = off, -1 = default.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"required": ["map"],
			"properties": {
				"map": {
					"type": "string"
				},
				"required_fields": {
					"type": "array",
					"items": {"type": "string"}
				},
				"required_fields_by_type": {
					"type": "object",
					"additionalProperties": {"type": "array", "items": {"type": "string"}}
				},
				"field_values": {
					"type": "object",
					"additionalProperties": {"type": "array", "items": {"type": "string"}}
				},
				"field_values_by_type": {
					"type": "object",
					"additionalProperties": {"type": "object", "additionalProperties": {"type": "array", "items": {"type": "string"}}}
				},
				"forbidden_fields": {
					"type": "array",
					"items": {"type": "string"}
				},
				"require_index_entry": {
					"type": "boolean"
				},
				"machine_path_allow_prefixes": {
					"type": "array",
					"items": {"type": "string"}
				},
				"value_synonyms": {
					"type": "object",
					"additionalProperties": {"type": "array", "items": {"type": "string"}}
				},
				"open_statuses": {"type": "array", "items": {"type": "string"}},
				"open_field": {"type": "string"},
				"open_markers": {"type": "array", "items": {"type": "string"}},
				"stale_after": {"type": "integer"},
				"harvest_after": {"type": "integer"},
				"concept_types": {"type": "array", "items": {"type": "string"}},
				"ontology_mode": {"type": "string", "enum": ["strict", "flexible", ""]},
				"template_sections": {"type": "boolean"},
				"templates": {"type": "array", "items": {"type": "string"}},
				"default_template": {"type": "string"},
				"require_template": {"type": "boolean"},
				"promote_to": {"type": "string"},
				"procedure_headings": {"type": "array", "items": {"type": "string"}},
				"glossary": {"type": "boolean"},
				"index": {"type": "string"},
				"repeated_fact_min": {"type": "integer"},
				"hotspot_in_degree": {"type": "integer"},
				"hotspot_bytes": {"type": "integer"},
				"oversize_bytes": {"type": "integer"},
				"oversize_concepts": {"type": "integer"},
				"work_map": {"type": "string"},
				"title_max_length": {"type": "integer"},
				"forbidden_title_terms": {"type": "array", "items": {"type": "string"}},
				"title": {"type": "string"},
				"lint_ignore": {"type": "array", "items": {"type": "string"}}
			}
		}`),
		Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
			// Pointers distinguish "not given" (leave it) from an empty value
			// (remove it): the two must never collapse into one.
			var params struct {
				Map                      string                         `json:"map"`
				RequiredFields           *[]string                      `json:"required_fields"`
				RequiredFieldsByType     map[string][]string            `json:"required_fields_by_type"`
				FieldValues              map[string][]string            `json:"field_values"`
				FieldValuesByType        map[string]map[string][]string `json:"field_values_by_type"`
				ForbiddenFields          *[]string                      `json:"forbidden_fields"`
				RequireIndexEntry        *bool                          `json:"require_index_entry"`
				MachinePathAllowPrefixes *[]string                      `json:"machine_path_allow_prefixes"`
				ValueSynonyms            map[string][]string            `json:"value_synonyms"`
				OpenStatuses             *[]string                      `json:"open_statuses"`
				OpenField                *string                        `json:"open_field"`
				OpenMarkers              *[]string                      `json:"open_markers"`
				StaleAfter               *int                           `json:"stale_after"`
				HarvestAfter             *int                           `json:"harvest_after"`
				ConceptTypes             *[]string                      `json:"concept_types"`
				OntologyMode             *string                        `json:"ontology_mode"`
				TemplateSections         *bool                          `json:"template_sections"`
				Templates                *[]string                      `json:"templates"`
				DefaultTemplate          *string                        `json:"default_template"`
				RequireTemplate          *bool                          `json:"require_template"`
				PromoteTo                *string                        `json:"promote_to"`
				ProcedureHeadings        *[]string                      `json:"procedure_headings"`
				Glossary                 *bool                          `json:"glossary"`
				Index                    *string                        `json:"index"`
				RepeatedFactMin          *int                           `json:"repeated_fact_min"`
				HotspotInDegree          *int                           `json:"hotspot_in_degree"`
				HotspotBytes             *int                           `json:"hotspot_bytes"`
				OversizeBytes            *int                           `json:"oversize_bytes"`
				OversizeConcepts         *int                           `json:"oversize_concepts"`
				WorkMap                  *string                        `json:"work_map"`
				TitleMaxLength           *int                           `json:"title_max_length"`
				ForbiddenTitleTerms      *[]string                      `json:"forbidden_title_terms"`
				Title                    *string                        `json:"title"`
				LintIgnore               *[]string                      `json:"lint_ignore"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return errorResult("invalid params: " + err.Error()), nil
			}
			if params.Map == "" {
				return errorResult("'map' is required"), nil
			}
			if params.RequiredFields == nil && params.RequiredFieldsByType == nil &&
				params.FieldValues == nil && params.FieldValuesByType == nil && params.ForbiddenFields == nil &&
				params.RequireIndexEntry == nil && params.MachinePathAllowPrefixes == nil && params.ValueSynonyms == nil &&
				params.OpenStatuses == nil && params.OpenField == nil && params.OpenMarkers == nil && params.StaleAfter == nil && params.HarvestAfter == nil && params.ConceptTypes == nil && params.OntologyMode == nil && params.TemplateSections == nil && params.Templates == nil && params.DefaultTemplate == nil && params.RequireTemplate == nil &&
				params.PromoteTo == nil && params.ProcedureHeadings == nil && params.Glossary == nil &&
				params.Index == nil && params.RepeatedFactMin == nil && params.HotspotInDegree == nil && params.HotspotBytes == nil && params.OversizeBytes == nil && params.OversizeConcepts == nil && params.WorkMap == nil && params.TitleMaxLength == nil && params.ForbiddenTitleTerms == nil && params.Title == nil && params.LintIgnore == nil {
				return errorResult("nothing to change: pass at least one of title, lint_ignore, require_index_entry, required_fields, required_fields_by_type, field_values, field_values_by_type, forbidden_fields, machine_path_allow_prefixes, value_synonyms, open_statuses, open_field, open_markers, stale_after, harvest_after, concept_types, ontology_mode, template_sections, templates, default_template, require_template, promote_to, procedure_headings, glossary, index, repeated_fact_min, hotspot_in_degree, hotspot_bytes, oversize_bytes, oversize_concepts, work_map, title_max_length, forbidden_title_terms"), nil
			}
			var fields, prefixes, forbidden []string
			if params.ForbiddenFields != nil {
				forbidden = *params.ForbiddenFields
			}
			if params.RequiredFields != nil {
				fields = *params.RequiredFields
			}
			if params.MachinePathAllowPrefixes != nil {
				prefixes = *params.MachinePathAllowPrefixes
			}
			if msg := validateContractParams(fields, params.RequiredFieldsByType, prefixes); msg != "" {
				return errorResult(msg), nil
			}
			if msg := validateFieldValueParams(params.FieldValues, params.FieldValuesByType, forbidden); msg != "" {
				return errorResult(msg), nil
			}
			if _, err := k.ReadArchiveMeta(params.Map); err != nil {
				return errorResult(fmt.Sprintf("map_update %q: not found", params.Map)), nil
			}
			if params.Templates != nil {
				def := ""
				if params.DefaultTemplate != nil {
					def = *params.DefaultTemplate
				}
				if msg := validateTemplateParams(*params.Templates, def); msg != "" {
					return errorResult(msg), nil
				}
			}

			contract, err := k.UpdateMapContract(params.Map, kb.MapContractUpdate{
				RequiredFields:           params.RequiredFields,
				RequiredFieldsByType:     params.RequiredFieldsByType,
				FieldValues:              params.FieldValues,
				FieldValuesByType:        params.FieldValuesByType,
				ForbiddenFields:          params.ForbiddenFields,
				RequireIndexEntry:        params.RequireIndexEntry,
				MachinePathAllowPrefixes: params.MachinePathAllowPrefixes,
				ValueSynonyms:            params.ValueSynonyms,
				OpenStatuses:             params.OpenStatuses,
				OpenField:                params.OpenField,
				OpenMarkers:              params.OpenMarkers,
				StaleAfterDays:           params.StaleAfter,
				HarvestAfterDays:         params.HarvestAfter,
				ConceptTypes:             params.ConceptTypes,
				OntologyMode:             params.OntologyMode,
				TemplateSections:         params.TemplateSections,
				Templates:                params.Templates,
				DefaultTemplate:          params.DefaultTemplate,
				RequireTemplate:          params.RequireTemplate,
				PromoteTo:                params.PromoteTo,
				ProcedureHeadings:        params.ProcedureHeadings,
				Glossary:                 params.Glossary,
				Index:                    params.Index,
				RepeatedFactMin:          params.RepeatedFactMin,
				HotspotInDegree:          params.HotspotInDegree,
				HotspotBytes:             params.HotspotBytes,
				OversizeBytes:            params.OversizeBytes,
				OversizeConcepts:         params.OversizeConcepts,
				WorkMap:                  params.WorkMap,
				TitleMaxLength:           params.TitleMaxLength,
				ForbiddenTitleTerms:      params.ForbiddenTitleTerms,
				Title:                    params.Title,
				LintIgnore:               params.LintIgnore,
			})
			if err != nil {
				return errorResult(fmt.Sprintf("map_update %q: %v", params.Map, err)), nil
			}

			_ = k.AppendLog("map_update: "+params.Map, time.Now())
			byType := contract.RequiredFieldsByType
			if byType == nil {
				byType = map[string][]string{}
			}
			fieldValues := contract.FieldValues
			if fieldValues == nil {
				fieldValues = map[string][]string{}
			}
			fieldValuesByType := contract.FieldValuesByType
			if fieldValuesByType == nil {
				fieldValuesByType = map[string]map[string][]string{}
			}
			title := ""
			lintIgnore := []string{}
			if meta, err := k.ReadArchiveMeta(params.Map); err == nil {
				if v, ok := meta.Get("title"); ok {
					title, _ = v.(string)
				}
				if v, ok := meta.Get("lint_ignore"); ok {
					if l, ok := v.([]string); ok {
						lintIgnore = l
					}
				}
			}
			result := map[string]interface{}{
				"map":         params.Map,
				"title":       title,
				"lint_ignore": lintIgnore,
				"status":      "updated",
				"contract": map[string]interface{}{
					"require_index_entry":         contract.RequireIndexEntry,
					"required_fields":             nonNilStrings(contract.RequiredFields),
					"required_fields_by_type":     byType,
					"field_values":                fieldValues,
					"field_values_by_type":        fieldValuesByType,
					"forbidden_fields":            nonNilStrings(contract.ForbiddenFields),
					"machine_path_allow_prefixes": nonNilStrings(contract.MachinePathAllowPrefixes),
				},
			}
			echo := result["contract"].(map[string]interface{})
			if contract.Index != "" {
				echo["index"] = contract.Index
			}
			// D346: the same view as map_list, so a caller sees from the
			// response that a key was written. Lists as [] when empty,
			// scalars omitted when unset.
			echo["open_statuses"] = nonNilStrings(contract.OpenStatuses)
			echo["open_markers"] = nonNilStrings(contract.OpenMarkers)
			echo["forbidden_title_terms"] = nonNilStrings(contract.ForbiddenTitleTerms)
			echo["concept_types"] = nonNilStrings(contract.ConceptTypes)
			echo["procedure_headings"] = nonNilStrings(contract.ProcedureHeadings)
			valueSyn := contract.ValueSynonyms
			if valueSyn == nil {
				valueSyn = map[string][]string{}
			}
			echo["value_synonyms"] = valueSyn
			echo["template_sections"] = contract.TemplateSections
			echo["templates"] = nonNilStrings(contract.Templates)
			echo["require_template"] = contract.StrictTemplates()
			if contract.DefaultTemplate != "" {
				echo["default_template"] = contract.DefaultTemplate
			}
			echo["glossary"] = contract.Glossary
			for k, v := range map[string]string{"kind": contract.Kind, "ontology_mode": contract.OntologyMode, "promote_to": contract.PromoteTo, "open_field": contract.OpenField, "work_map": contract.WorkMap} {
				if v != "" {
					echo[k] = v
				}
			}
			for k, v := range map[string]int{"harvest_after": contract.HarvestAfterDays, "repeated_fact_min": contract.RepeatedFactMin, "hotspot_in_degree": contract.HotspotInDegree, "hotspot_bytes": contract.HotspotBytes, "oversize_bytes": contract.OversizeBytes, "oversize_concepts": contract.OversizeConcepts} {
				if v > 0 {
					echo[k] = v
				}
			}
			if contract.HarvestAfterDays < 0 {
				echo["harvest_after"] = 0 // D347: explicit off
			}
			if contract.TitleMaxLength != nil {
				echo["title_max_length"] = *contract.TitleMaxLength
			}
			if days, defaulted := lint.EffectiveStaleAfter(&contract); days > 0 {
				echo["stale_after"] = days
				if defaulted {
					echo["stale_after_defaulted"] = true
				}
			}
			out, _ := json.MarshalIndent(result, "", "  ")
			return textResult(string(out)), nil
		},
	}
}

// nonNilStrings renders a nil slice as [] rather than null in JSON output.
func nonNilStrings(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

// --- map_delete ---

func toolMapDelete(k *kb.KB) Tool {
	return Tool{
		Name:        "map_delete",
		Description: "Deletes a Map or Journal only if empty (just the scaffold _map.md, index.md, log.md). Otherwise nothing changes and the error lists the concepts: concept_move them first.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"required": ["map"],
			"properties": {
				"map": {
					"type": "string"
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
		Name:        "concept_expand",
		Description: "Turns <id>.md into a directory <id>/ whose index.md keeps the same ID, so the concept can hold satellites <id>/<child>; links keep working. id needs exactly two segments (map/concept). No inverse.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"required": ["id"],
			"properties": {
				"id": {
					"type": "string"
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
		Name:        "log_append",
		Description: "Appends an entry to the root log.md (newest first). With path, the entry is prefixed '[<path>] ' in the root log (no per-directory log); read back with log_tail(path).",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"required": ["entry"],
			"properties": {
				"entry": {
					"type": "string"
				},
				"path": {
					"type": "string",
					"description": "Optional folder prefix"
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
		Name:        "snapshot",
		Description: "Logs a KB snapshot entry and, with git auto-commit enabled, a git commit.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"message": {
					"type": "string"
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
		Description: "Marks a concept as superseded by another: sets status=superseded and records the successor. Returns findings on it.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"required": ["source_id", "target_id"],
			"properties": {
				"source_id": {
					"type": "string",
					"description": "Concept to supersede"
				},
				"target_id": {
					"type": "string",
					"description": "Replacement concept"
				},
				"reason": {
					"type": "string",
					"description": "Why"
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
			if params.TargetID == params.SourceID {
				return errorResult("supersede: a concept cannot supersede itself"), nil
			}
			// One text for a missing and a hidden successor: no existence
			// oracle (D243).
			if !conceptExists(k, params.TargetID) || !Visible(ctx, k, params.TargetID) {
				return errorResult("supersede: target not found: " + params.TargetID), nil
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
			text := fmt.Sprintf("superseded %s → %s", params.SourceID, params.TargetID)
			f, repaired, _ := repairWritten(k, []string{params.SourceID}, nil)
			if f != nil {
				enc, _ := json.MarshalIndent(f, "", "  ")
				text += "\nfindings:\n" + string(enc)
			}
			if len(repaired) > 0 {
				enc, _ := json.MarshalIndent(repaired, "", "  ")
				text += "\nrepaired:\n" + string(enc)
			}
			return textResult(text), nil
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

func toolConceptMove(k *kb.KB) Tool {
	return Tool{
		Name:        "concept_move",
		Description: "Moves concepts to new IDs in one commit: one source_id/target_id pair or a moves array (exclusive). All entries are validated first (source exists, target free, no traversal or duplicate source); an invalid one aborts the batch. Unless rewrite_links=false, inbound links KB-wide are rewritten. Works across maps and services/. An expanded concept moves with assets and satellites. A mid-batch filesystem failure names both IDs, no rollback. Returns findings on moved concepts and their linkers.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"source_id": {
					"type": "string"
				},
				"target_id": {
					"type": "string"
				},
				"moves": {
					"type": "array",
					"description": "Batch of {source_id, target_id} pairs; exclusive with the single pair",
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
					"type": "boolean"
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

			result, errRes := applyConceptMoves(k, moves, rewriteLinks, moveOptions{})
			if errRes != nil {
				return *errRes, nil
			}
			out, _ := json.MarshalIndent(result, "", "  ")
			return textResult(string(out)), nil
		},
	}
}

// moveOptions are the two things concept_archive changes about the shared move
// core (D344).
type moveOptions struct {
	// ForceSourceIndexRemoval removes the moved concept's entry from the source
	// map's curated index even when that map never declared require_index_entry.
	ForceSourceIndexRemoval bool
	// LogTitle replaces the "concept_move (n move(s))" header of the log entry.
	LogTitle string
}

// applyConceptMoves validates every move, applies them, rebases the moved
// concepts' own links, maintains curated indexes, rewrites inbound links, logs
// and computes the findings. Shared by concept_move and concept_archive; an
// application error comes back as the non-nil ToolResult, nothing else is
// returned in that case.
func applyConceptMoves(k *kb.KB, moves []conceptMoveEntry, rewriteLinks bool, opts moveOptions) (map[string]interface{}, *ToolResult) {
	// --- validation pass: every entry must pass before anything is applied. ---
	type validMove struct {
		sourceID string
		targetID string
		fm       *okf.Frontmatter
		body     string
		expanded bool
		source   kb.ConceptLocation
		target   kb.ConceptLocation
		mappings map[string]string
	}
	seenSources := map[string]bool{}
	seenTargets := map[string]bool{}
	valid := make([]validMove, 0, len(moves))

	for _, m := range moves {
		if m.SourceID == "" {
			return nil, errRes(errorResult("'source_id' is required for every move"))
		}
		if m.TargetID == "" {
			return nil, errRes(errorResult("'target_id' is required for every move"))
		}
		if seenSources[m.SourceID] {
			return nil, errRes(errorResult("duplicate source_id in batch: " + m.SourceID))
		}
		seenSources[m.SourceID] = true
		if seenTargets[m.TargetID] {
			return nil, errRes(errorResult("duplicate target_id in batch: " + m.TargetID))
		}
		seenTargets[m.TargetID] = true
		if _, err := okf.PathToID(m.TargetID + ".md"); err != nil {
			return nil, errRes(errorResult("invalid target_id: " + m.TargetID))
		}

		// Both ends are resolved by the KB, never joined onto
		// DataRoot() here: services/ is rooted at the KB root, and a
		// hand-built path removed data/services/<x>.md while the real
		// services/<x>.md survived, leaving two copies (D269). The
		// resolver also carries the path-confinement check.
		targetLoc, err := k.LocateConcept(okf.ConceptID(m.TargetID))
		if err != nil {
			if errors.Is(err, okf.ErrInvalidPath) {
				return nil, errRes(errorResult("target_id resolves outside KB root: " + m.TargetID))
			}
			return nil, errRes(errorResult(fmt.Sprintf("concept_move: resolve target %q: %v", m.TargetID, err)))
		}

		data, err := k.ReadConcept(okf.ConceptID(m.SourceID))
		if err != nil {
			return nil, errRes(errorResult(fmt.Sprintf("concept_move: read source %q: %v", m.SourceID, err)))
		}
		sourceLoc, err := k.LocateConcept(okf.ConceptID(m.SourceID))
		if err != nil {
			return nil, errRes(errorResult(fmt.Sprintf("concept_move: resolve source %q: %v", m.SourceID, err)))
		}

		if msg := targetOccupied(k, m.TargetID, targetLoc); msg != "" {
			return nil, errRes(errorResult(msg))
		}

		fm, err := okf.ParseFrontmatter(data.FrontmatterRaw)
		if err != nil {
			return nil, errRes(errorResult(fmt.Sprintf("concept_move: parse frontmatter %q: %v", m.SourceID, err)))
		}

		vm := validMove{sourceID: m.SourceID, targetID: m.TargetID, fm: fm, body: data.Body, source: sourceLoc, target: targetLoc, mappings: map[string]string{m.SourceID: m.TargetID}}
		if sourceLoc.Expanded {
			if len(strings.Split(m.SourceID, "/")) != 2 || len(strings.Split(m.TargetID, "/")) != 2 {
				return nil, errRes(errorResult("expanded concept moves require two-segment source_id and target_id"))
			}
			if strings.HasPrefix(m.TargetID+"/", m.SourceID+"/") || strings.HasPrefix(m.SourceID+"/", m.TargetID+"/") {
				return nil, errRes(errorResult("expanded concept target cannot be inside, above, or equal to its source"))
			}
			vm.expanded = true
			if err := k.WalkConcepts(func(id okf.ConceptID, _ string) error {
				idStr := string(id)
				if idStr == m.SourceID || strings.HasPrefix(idStr, m.SourceID+"/") {
					vm.mappings[idStr] = m.TargetID + strings.TrimPrefix(idStr, m.SourceID)
				}
				return nil
			}); err != nil {
				return nil, errRes(errorResult(fmt.Sprintf("concept_move: list expanded source %q: %v", m.SourceID, err)))
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
				return nil, errRes(errorResult("expanded concept moves cannot overlap, swap, or use ancestor/descendant paths in one batch"))
			}
		}
	}

	// --- apply pass: all entries already validated above. ---
	moveMap := make(map[string]string, len(valid))
	applied := make([]conceptMoveEntry, 0, len(valid))
	logLines := make([]string, 0, len(valid)+1)

	// A failure from here on is late: earlier entries (and, for a flat
	// move, this entry's target) are already on disk and are not rolled
	// back. It must still be an explicit application error — gitWrap
	// then neither commits nor logs success — and say what is where.
	appliedNote := func() string {
		if len(applied) == 0 {
			return ""
		}
		return fmt.Sprintf("; %d earlier move(s) in this batch were already applied and are not rolled back", len(applied))
	}
	for _, mv := range valid {
		if mv.expanded {
			if err := os.MkdirAll(filepath.Dir(mv.target.Dir), 0o755); err != nil {
				return nil, errRes(errorResult(fmt.Sprintf("concept_move: create target parent %q: %v%s", mv.targetID, err, appliedNote())))
			}
			if err := conceptMoveRename(mv.source.Dir, mv.target.Dir); err != nil {
				return nil, errRes(errorResult(fmt.Sprintf("concept_move: could not move expanded concept %q to %q: %v; its directory was not moved%s", mv.sourceID, mv.targetID, err, appliedNote())))
			}
		} else {
			if _, err := k.WriteConcept(okf.ConceptID(mv.targetID), mv.fm, mv.body, ""); err != nil {
				return nil, errRes(errorResult(fmt.Sprintf("concept_move: write target %q: %v%s", mv.targetID, err, appliedNote())))
			}

			// The source was resolved and read in preflight, so even
			// not-found here is a failure: ignoring it is how the move
			// used to report success with the source still in place.
			if err := conceptMoveRemove(mv.source.File); err != nil {
				return nil, errRes(errorResult(fmt.Sprintf("concept_move: target %q was already written but source %q could not be removed: %v; both now exist and nothing was rolled back — delete one of them%s", mv.targetID, mv.sourceID, err, appliedNote())))
			}
		}

		for oldID, newID := range mv.mappings {
			moveMap[oldID] = newID
		}
		applied = append(applied, conceptMoveEntry{SourceID: mv.sourceID, TargetID: mv.targetID})
		logLines = append(logLines, fmt.Sprintf("- %s → %s", mv.sourceID, mv.targetID))
	}

	result := map[string]interface{}{
		"moves": applied,
	}

	// The moved concept's OWN relative links break when the directory
	// depth changes: concept_move rewrote inbound links only, which is a
	// half-move (D160). No flag: a move that leaves the moved body's links
	// broken is simply incomplete, and a flag would preserve that as a
	// supported mode.
	outboundFixed := 0
	for _, mv := range applied {
		oldBase, newBase := okf.IDToPath(okf.ConceptID(mv.SourceID)), okf.IDToPath(okf.ConceptID(mv.TargetID))
		if relPath, expanded := k.ConceptRelPath(okf.ConceptID(mv.TargetID)); expanded {
			newBase = relPath
			oldBase = filepath.ToSlash(filepath.Join(mv.SourceID, "index.md"))
		}
		data, readErr := k.ReadConcept(okf.ConceptID(mv.TargetID))
		if readErr != nil {
			continue
		}
		newBody, n := kb.RewriteOutboundLinks(data.Body, oldBase, newBase, moveMap)
		if n == 0 {
			continue
		}
		fm, parseErr := okf.ParseFrontmatter(data.FrontmatterRaw)
		if parseErr != nil {
			continue
		}
		if _, err := k.WriteConcept(okf.ConceptID(mv.TargetID), fm, newBody, data.ContentHash); err != nil {
			return nil, errRes(errorResult(fmt.Sprintf("concept_move: applied the move but could not rewrite %q's own links: %v", mv.TargetID, err)))
		}
		outboundFixed += n
	}
	if outboundFixed > 0 {
		result["outbound_rewritten"] = outboundFixed
		logLines = append(logLines, fmt.Sprintf("outbound_links: %d replacement(s) in the moved concept(s)", outboundFixed))
	}

	if rewriteLinks {
		// Curated indexes are not links between concepts, so the backlink
		// pass below never touched them: the source map's index kept
		// listing the moved concept (broken_link) and the destination's did
		// not mention it (index_incomplete). Only maps that asked for a
		// curated index are edited — rewriting prose nobody declared as
		// curated would be an assumption, not a fix (D160).
		if notes := maintainCuratedIndexes(k, applied, opts.ForceSourceIndexRemoval); len(notes) > 0 {
			result["curated_indexes"] = notes
			logLines = append(logLines, notes...)
		}
	}

	if rewriteLinks {
		touched, totalReplacements, err := rewriteBacklinks(k, moveMap)
		if err != nil {
			// Moves are already applied (and will still be committed by
			// gitWrap only on success); surface the rewrite failure so the
			// caller knows some backlinks may be stale.
			return nil, errRes(errorResult(fmt.Sprintf("concept_move: applied %d move(s) but rewrite_links failed: %v", len(applied), err)))
		}
		result["rewritten"] = touched
		if len(touched) > 0 {
			logLines = append(logLines, fmt.Sprintf("rewrite_links: %d concept(s), %d replacement(s)", len(touched), totalReplacements))
		}
		indexes, err := rewriteIndexLinks(k, moveMap)
		if err != nil {
			return nil, errRes(errorResult(fmt.Sprintf("concept_move: applied %d move(s) but rewriting index links failed: %v", len(applied), err)))
		}
		if len(indexes) > 0 {
			result["rewritten_indexes"] = indexes
			logLines = append(logLines, "rewrite_links: index(es) "+strings.Join(indexes, ", "))
		}
	} else {
		var warnings []string
		for _, mv := range valid {
			warnings = append(warnings, fmt.Sprintf("Warning: inbound links to %s are not updated — run lint to find broken links", mv.sourceID))
		}
		result["warning"] = strings.Join(warnings, "\n")
	}

	title := opts.LogTitle
	if title == "" {
		title = fmt.Sprintf("concept_move (%d move(s))", len(applied))
	}
	_ = k.AppendLog(title+":\n"+strings.Join(logLines, "\n"), time.Now())

	// D312: what the move left behind. Every moved ID is checked, and
	// every old ID as a gone one: a page still linking it (rewrite_links
	// false, or a link the rewrite could not follow) is broken now.
	written := make([]string, 0, len(moveMap))
	gone := make([]string, 0, len(moveMap))
	for oldID, newID := range moveMap {
		written = append(written, newID)
		gone = append(gone, oldID)
	}
	sort.Strings(written)
	sort.Strings(gone)
	f, repaired, _ := repairWritten(k, written, gone)
	if f != nil {
		result["findings"] = f
	}
	if len(repaired) > 0 {
		result["repaired"] = repaired
	}
	return result, nil
}

// targetOccupied explains why a move cannot land on targetID, or "" when the
// slot is free. Occupied also means a file ReadConcept cannot parse, or an
// "<id>/" directory holding only assets: moving onto either would silently
// adopt or shadow what is there.
func targetOccupied(k *kb.KB, targetID string, loc kb.ConceptLocation) string {
	if _, err := k.ReadConcept(okf.ConceptID(targetID)); err == nil {
		return "conflict: target already exists: " + targetID
	} else if !errors.Is(err, okf.ErrNotFound) {
		return fmt.Sprintf("concept_move: check target %q: %v", targetID, err)
	}
	if _, err := os.Lstat(loc.File); err == nil {
		return "conflict: target already exists: " + targetID
	} else if !os.IsNotExist(err) {
		return fmt.Sprintf("concept_move: check target %q: %v", targetID, err)
	}
	if _, err := os.Lstat(loc.Dir); err == nil {
		return "conflict: target directory already exists: " + targetID
	} else if !os.IsNotExist(err) {
		return fmt.Sprintf("concept_move: check target directory %q: %v", targetID, err)
	}
	return ""
}

func errRes(r ToolResult) *ToolResult { return &r }

// toolConceptMerge folds a satellite into its expanded parent (D160). Consolidating
// a dossier is a routine refactor with no primitive: done by hand it breaks links
// three ways — the merged body's own relative links were relative to the
// satellite's directory, links TO the deleted satellite remain, and so do links
// from sibling satellites. All three are mechanical once the link rule is settled
// (D149), and all three are what rewriteBacklinks already does for a move.
func toolConceptMerge(k *kb.KB) Tool {
	return Tool{
		Name: "concept_merge",
		Description: "Folds a satellite concept into its own expanded parent, in one commit: the satellite's " +
			"body is appended to the parent's index.md under a heading, its own relative links are rebased " +
			"to the parent's directory, every inbound link (markdown and wiki-link, including from sibling " +
			"satellites) is redirected to the parent, and the satellite is deleted. Only a satellite into " +
			"its own parent: arbitrary concept-into-concept merging is out of scope.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"required": ["satellite_id", "if_match"],
			"properties": {
				"satellite_id": {"type": "string", "description": "ConceptID of the satellite (3 segments: map/concept/child)"},
				"if_match": {"type": "string", "description": "Expected content-hash of the satellite"},
				"heading": {"type": "string", "description": "Heading the merged body is appended under; defaults to the satellite's title"}
			}
		}`),
		Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
			var params struct {
				SatelliteID string `json:"satellite_id"`
				IfMatch     string `json:"if_match"`
				Heading     string `json:"heading"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return errorResult("invalid params: " + err.Error()), nil
			}
			parts := strings.Split(params.SatelliteID, "/")
			if len(parts) != 3 {
				return errorResult(fmt.Sprintf("concept_merge %q: not a satellite — expected 3 segments (map/concept/child)", params.SatelliteID)), nil
			}
			parentID := okf.ConceptID(strings.Join(parts[:2], "/"))
			parentRel, expanded := k.ConceptRelPath(parentID)
			if !expanded {
				return errorResult(fmt.Sprintf("concept_merge %q: %q is not an expanded concept", params.SatelliteID, parentID)), nil
			}

			satellite, err := k.ReadConcept(okf.ConceptID(params.SatelliteID))
			if err != nil {
				return errorResult(fmt.Sprintf("concept_merge %q: %v", params.SatelliteID, err)), nil
			}
			if satellite.ContentHash != params.IfMatch {
				return errorResult(fmt.Sprintf("stale_write: concept_merge %q: if_match %q does not match %q", params.SatelliteID, params.IfMatch, satellite.ContentHash)), nil
			}
			parent, err := k.ReadConcept(parentID)
			if err != nil {
				return errorResult(fmt.Sprintf("concept_merge: read parent %q: %v", parentID, err)), nil
			}

			heading := params.Heading
			if heading == "" {
				heading = string(parentID)
				if parsed, parseErr := okf.ParseFrontmatter(satellite.FrontmatterRaw); parseErr == nil && parsed != nil {
					if t, ok := parsed.Get("title"); ok {
						if ts, ok := t.(string); ok && strings.TrimSpace(ts) != "" {
							heading = ts
						}
					}
				}
			}

			// The satellite's own relative links move up one level.
			mergedBody, _ := kb.RewriteOutboundLinks(satellite.Body, okf.IDToPath(okf.ConceptID(params.SatelliteID)), parentRel, nil)
			parentFM, err := okf.ParseFrontmatter(parent.FrontmatterRaw)
			if err != nil {
				return errorResult(fmt.Sprintf("concept_merge: parse parent frontmatter: %v", err)), nil
			}
			newParentBody := strings.TrimRight(parent.Body, "\n") + "\n\n## " + heading + "\n\n" + strings.TrimSpace(mergedBody) + "\n"
			if _, err := k.WriteConcept(parentID, parentFM, newParentBody, parent.ContentHash); err != nil {
				return errorResult(fmt.Sprintf("concept_merge: write parent %q: %v", parentID, err)), nil
			}
			if err := k.DeleteConcept(okf.ConceptID(params.SatelliteID)); err != nil {
				return errorResult(fmt.Sprintf("concept_merge: delete satellite %q: %v", params.SatelliteID, err)), nil
			}

			// Inbound links — including from sibling satellites, which the
			// whole-KB pass covers with no special case.
			moveMap := map[string]string{params.SatelliteID: string(parentID)}
			touched, replacements, err := rewriteBacklinks(k, moveMap)
			if err != nil {
				return errorResult(fmt.Sprintf("concept_merge: merged %q but redirecting inbound links failed: %v", params.SatelliteID, err)), nil
			}
			if _, err := rewriteIndexLinks(k, moveMap); err != nil {
				return errorResult(fmt.Sprintf("concept_merge: merged %q but redirecting index links failed: %v", params.SatelliteID, err)), nil
			}
			_ = k.AppendLog(fmt.Sprintf("concept_merge: %s → %s", params.SatelliteID, parentID), time.Now())

			out, _ := json.MarshalIndent(map[string]interface{}{
				"merged_into":  string(parentID),
				"heading":      heading,
				"rewritten":    touched,
				"replacements": replacements,
			}, "", "  ")
			return textResult(string(out)), nil
		},
	}
}

// toolConceptCollapse is the inverse of concept_expand (D160). concept_expand's
// description said there was none, while expanded_as_category actively advises
// going back above 8 children — so the only route was a batch concept_move by
// hand.
//
// Expansion never changes an ID, so neither does this: "<id>/index.md" becomes
// "<id>.md" under the same ID, and no inbound link needs rewriting.
func toolConceptCollapse(k *kb.KB) Tool {
	return Tool{
		Name: "concept_collapse",
		Description: "Turns an expanded concept back into a plain one: \"<id>/index.md\" becomes \"<id>.md\" " +
			"under the SAME ConceptID, so no inbound link changes. The inverse of concept_expand. Refuses " +
			"when the directory still holds satellites, assets or any other file (a hidden one) — none would have a home after the " +
			"collapse — and names them.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"required": ["id", "if_match"],
			"properties": {
				"id": {"type": "string", "description": "ConceptID to collapse (2 segments: map/concept)"},
				"if_match": {"type": "string", "description": "Expected content-hash of the expanded concept"}
			}
		}`),
		Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
			var params struct {
				ID      string `json:"id"`
				IfMatch string `json:"if_match"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return errorResult("invalid params: " + err.Error()), nil
			}
			id := okf.ConceptID(params.ID)
			if len(strings.Split(params.ID, "/")) != 2 {
				return errorResult(fmt.Sprintf("concept_collapse %q: expected exactly 2 segments (map/concept)", params.ID)), nil
			}
			if _, expanded := k.ConceptRelPath(id); !expanded {
				return errorResult(fmt.Sprintf("concept_collapse %q: not an expanded concept", params.ID)), nil
			}
			data, err := k.ReadConcept(id)
			if err != nil {
				return errorResult(fmt.Sprintf("concept_collapse %q: %v", params.ID, err)), nil
			}
			if data.ContentHash != params.IfMatch {
				return errorResult(fmt.Sprintf("stale_write: concept_collapse %q: if_match %q does not match %q", params.ID, params.IfMatch, data.ContentHash)), nil
			}

			// Satellites and assets live in the directory and would have no home.
			var satellites []string
			if err := k.WalkConcepts(func(other okf.ConceptID, _ string) error {
				if strings.HasPrefix(string(other), params.ID+"/") {
					satellites = append(satellites, string(other))
				}
				return nil
			}); err != nil {
				return errorResult(fmt.Sprintf("concept_collapse: walk: %v", err)), nil
			}
			if len(satellites) > 0 {
				sort.Strings(satellites)
				return errorResult(fmt.Sprintf("concept_collapse %q: still holds %d satellite(s) — merge or move them first: %s",
					params.ID, len(satellites), strings.Join(satellites, ", "))), nil
			}
			assets, assetErr := k.ListAssets(id)
			if assetErr != nil {
				return errorResult(fmt.Sprintf("concept_collapse %q: %v", params.ID, assetErr)), nil
			}
			if len(assets) > 0 {
				names := make([]string, 0, len(assets))
				for _, a := range assets {
					names = append(names, a.Path)
				}
				sort.Strings(names)
				return errorResult(fmt.Sprintf("concept_collapse %q: still owns %d asset(s), which only an expanded concept can hold — remove them with asset_delete first: %s",
					params.ID, len(assets), strings.Join(names, ", "))), nil
			}

			dirAbs, err := k.ResolvePath(params.ID, true)
			if err != nil {
				return errorResult(fmt.Sprintf("concept_collapse %q: %v", params.ID, err)), nil
			}
			flatAbs, err := k.ResolvePath(okf.IDToPath(id), true)
			if err != nil {
				return errorResult(fmt.Sprintf("concept_collapse %q: %v", params.ID, err)), nil
			}
			if _, statErr := os.Stat(flatAbs); statErr == nil {
				return errorResult(fmt.Sprintf("concept_collapse %q: %s.md already exists (expanded_ambiguous) — remove one form first", params.ID, params.ID)), nil
			}
			// Anything ListAssets does not count (a hidden file, an empty
			// subdirectory) would make the final Remove fail after index.md
			// already moved: refuse before touching anything (D270).
			entries, err := os.ReadDir(dirAbs)
			if err != nil {
				return errorResult(fmt.Sprintf("concept_collapse %q: %v", params.ID, err)), nil
			}
			var leftover []string
			for _, e := range entries {
				if e.Name() != "index.md" {
					leftover = append(leftover, e.Name())
				}
			}
			if len(leftover) > 0 {
				return errorResult(fmt.Sprintf("concept_collapse %q: the directory still holds %s, which would have no home after the collapse — remove it first",
					params.ID, strings.Join(leftover, ", "))), nil
			}
			if err := os.Rename(filepath.Join(dirAbs, "index.md"), flatAbs); err != nil {
				return errorResult(fmt.Sprintf("concept_collapse %q: move index.md: %v", params.ID, err)), nil
			}
			if err := os.Remove(dirAbs); err != nil {
				return errorResult(fmt.Sprintf("concept_collapse %q: remove the now-empty directory: %v", params.ID, err)), nil
			}
			_ = k.AppendLog("concept_collapse: "+params.ID, time.Now())

			out, _ := json.MarshalIndent(map[string]interface{}{
				"id":   params.ID,
				"path": okf.IDToPath(id),
			}, "", "  ")
			return textResult(string(out)), nil
		},
	}
}

// maintainCuratedIndexes removes the moved concept's entry from the source map's
// curated index and appends one to the destination's, for maps that opted in with
// require_index_entry (D160). Returns one human-readable note per action.
//
// Both edits are conservative. A source line is removed only when the moved
// concept is its ONLY link: a line citing two concepts is prose the operator
// wrote, so it is kept and reported instead. The destination entry is appended
// under a "## Moved here" heading created if absent — Cartographer cannot know the
// right thematic section, and a wrong placement in a curated document is worse
// than an obvious one at the end. Only maps that declared require_index_entry are
// touched: editing an index nobody called curated would be an assumption.
func maintainCuratedIndexes(k *kb.KB, applied []conceptMoveEntry, forceSourceRemoval bool) []string {
	var notes []string
	// A generated index (D301) is the server's: gitWrap rewrites it.
	curated := func(mapName string) (contract kb.MapContract, ok bool) {
		contract, err := k.ReadMapContract(mapName)
		return contract, err == nil && contract.Index != kb.IndexGenerated
	}
	requiresIndex := func(mapName string) bool {
		contract, ok := curated(mapName)
		return ok && contract.RequireIndexEntry
	}
	// TRAP (D344): the two sides are gated differently on purpose. A generic
	// rename (concept_move) edits the SOURCE index only when the map opted in
	// with require_index_entry; a retirement (concept_archive) is explicit
	// intent, so it removes the entry from any non-generated index. The
	// destination side stays opt-in for both.
	srcEdited := func(mapName string) bool {
		if forceSourceRemoval {
			_, ok := curated(mapName)
			return ok
		}
		return requiresIndex(mapName)
	}
	for _, mv := range applied {
		srcMap, srcOK := conceptMapName(mv.SourceID)
		dstMap, dstOK := conceptMapName(mv.TargetID)
		if !srcOK || !dstOK || srcMap == dstMap {
			continue
		}
		if srcEdited(srcMap) {
			removed, kept, err := removeCuratedEntry(k, srcMap, okf.ConceptID(mv.SourceID))
			switch {
			case err != nil:
				notes = append(notes, fmt.Sprintf("curated index %s/index.md: %v", srcMap, err))
			case removed:
				notes = append(notes, fmt.Sprintf("curated index %s/index.md: removed the entry for %s", srcMap, mv.SourceID))
			case kept != "":
				notes = append(notes, fmt.Sprintf("curated index %s/index.md: kept a line citing %s alongside other concepts, edit it by hand: %q", srcMap, mv.SourceID, kept))
			}
		}
		if requiresIndex(dstMap) {
			if err := appendCuratedEntry(k, dstMap, okf.ConceptID(mv.TargetID), mv.SourceID); err != nil {
				notes = append(notes, fmt.Sprintf("curated index %s/index.md: %v", dstMap, err))
			} else {
				notes = append(notes, fmt.Sprintf("curated index %s/index.md: added an entry for %s", dstMap, mv.TargetID))
			}
		}
	}
	return notes
}

// conceptMapName returns a concept's map name, and false when the ID has none.
func conceptMapName(id string) (string, bool) {
	parts := strings.Split(id, "/")
	if len(parts) < 2 {
		return "", false
	}
	return parts[0], true
}

// removeCuratedEntry drops the line whose only link is id, reporting kept when a
// line cites id alongside other concepts.
func removeCuratedEntry(k *kb.KB, mapName string, id okf.ConceptID) (removed bool, kept string, err error) {
	content, hash, err := k.IndexHash(mapName)
	if err != nil {
		return false, "", fmt.Errorf("unreadable: %w", err)
	}
	fmRaw, body, hasFM := okf.SplitFrontmatter(content)
	indexPath := mapName + "/index.md"
	var out []string
	for _, line := range strings.Split(body, "\n") {
		targets := kb.ExtractLinks(line, indexPath, k.AssetExists)
		hit := false
		for _, t := range targets {
			if t == id || strings.TrimSuffix(string(t), "/index") == string(id) {
				hit = true
			}
		}
		if !hit {
			out = append(out, line)
			continue
		}
		if len(targets) > 1 {
			kept = strings.TrimSpace(line)
			out = append(out, line)
			continue
		}
		removed = true
	}
	if !removed {
		return false, kept, nil
	}
	_, err = k.PatchIndex(mapName, hash, joinIndex(fmRaw, hasFM, strings.Join(out, "\n")))
	return err == nil, kept, err
}

// appendCuratedEntry adds an entry for id under a "## Moved here" heading.
func appendCuratedEntry(k *kb.KB, mapName string, id okf.ConceptID, from string) error {
	content, hash, err := k.IndexHash(mapName)
	if err != nil {
		return fmt.Errorf("unreadable: %w", err)
	}
	fmRaw, body, hasFM := okf.SplitFrontmatter(content)
	title := string(id)
	if data, readErr := k.ReadConcept(id); readErr == nil {
		if parsed, parseErr := okf.ParseFrontmatter(data.FrontmatterRaw); parseErr == nil && parsed != nil {
			if t, ok := parsed.Get("title"); ok {
				if ts, ok := t.(string); ok && strings.TrimSpace(ts) != "" {
					title = ts
				}
			}
		}
	}
	// From a map index an expanded concept is "<concept>.md" — the form D149 WP3
	// settled, and the one require_index_entry accepts.
	rel := strings.TrimPrefix(string(id), mapName+"/") + ".md"
	entry := fmt.Sprintf("- [%s](%s): moved from %s", title, rel, from)

	const heading = "## Moved here"
	if strings.Contains(body, heading) {
		body = strings.TrimRight(body, "\n") + "\n" + entry + "\n"
	} else {
		body = strings.TrimRight(body, "\n") + "\n\n" + heading + "\n\n" + entry + "\n"
	}
	_, err = k.PatchIndex(mapName, hash, joinIndex(fmRaw, hasFM, body))
	return err
}

// joinIndex reassembles an index file from its frontmatter and body.
func joinIndex(fmRaw string, hasFM bool, body string) string {
	if !hasFM {
		return body
	}
	return "---\n" + strings.TrimSuffix(fmRaw, "\n") + "\n---\n" + body
}

// rewriteBacklinks rewrites every markdown/wiki-link whose resolved target is
// a key in moveMap (old ID → new ID, D72 WP1), in the post-move state, so a
// moved concept is visited at its new location. Only the concepts that can
// need it are read (D248): those the link graph says link to an old id, those
// whose superseded_by names one, and the moved concepts themselves (their own
// links to co-moved targets, D160) — in walk order, so the rewritten list is
// the one a whole-KB pass would produce. Concepts with at least one
// replacement are written back through kb.WriteConcept — if_match is the
// content-hash just read, guarding against a concurrent external write.
// Content writes are never best-effort: the first write failure aborts the
// pass and is returned as an error. Returns the list of touched concepts and
// the total number of replacements performed.
func rewriteBacklinks(k *kb.KB, moveMap map[string]string) ([]rewrittenConcept, int, error) {
	var touched []rewrittenConcept
	total := 0

	oldIDs := make([]okf.ConceptID, 0, len(moveMap))
	newIDs := make([]okf.ConceptID, 0, len(moveMap))
	for old, moved := range moveMap {
		oldIDs = append(oldIDs, okf.ConceptID(old))
		newIDs = append(newIDs, okf.ConceptID(moved))
	}

	err := k.WalkConceptsLinkingTo(oldIDs, newIDs, func(id okf.ConceptID, basePath, content string) error {
		fmRaw, body, _ := okf.SplitFrontmatter(content)
		newBody, count := kb.RewriteLinks(body, basePath, moveMap, k.AssetExists)
		// superseded_by is a relation, not a link (D243), but it names a
		// concept all the same: a moved successor must not leave it dangling.
		// The frontmatter is parsed only when there is something to rewrite.
		if count == 0 && !strings.Contains(fmRaw, "superseded_by") {
			return nil
		}

		fm, err := okf.ParseFrontmatter(fmRaw)
		if err != nil {
			if count == 0 {
				return nil // nothing in the body to fix, and no relation to read
			}
			return fmt.Errorf("parse frontmatter %q: %w", id, err)
		}
		if v, ok := fm.Get("superseded_by"); ok {
			if old, ok := v.(string); ok {
				if moved, ok := moveMap[old]; ok {
					fm.Set("superseded_by", moved)
					count++
				}
			}
		}
		if count == 0 {
			return nil
		}

		ifMatch := okf.ContentHash(content)
		if _, err := k.WriteConcept(id, fm, newBody, ifMatch); err != nil {
			return fmt.Errorf("write %q: %w", id, err)
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

// rewriteIndexLinks redirects the links to a moved concept in the root and
// every map's index.md. Indexes are not concepts, so rewriteBacklinks never
// reads them, and maintainCuratedIndexes only edits the source and target maps
// that opted in: an index of a third map citing the concept kept the old path,
// a broken_link with no mechanical fix. Pointing an existing link at where its
// target now lives adds or removes no entry, so it needs no opt-in (D160 is
// about entries). Returns the index paths written ("" is the root).
func rewriteIndexLinks(k *kb.KB, moveMap map[string]string) ([]string, error) {
	maps, err := k.ListArchives()
	if err != nil {
		return nil, err
	}
	var touched []string
	for _, name := range append([]string{""}, maps...) {
		content, hash, err := k.IndexHash(name)
		if err != nil {
			continue // no index.md: nothing to redirect
		}
		fmRaw, body, hasFM := okf.SplitFrontmatter(content)
		basePath := "index.md"
		if name != "" {
			basePath = name + "/index.md"
		}
		newBody, n := kb.RewriteLinks(body, basePath, moveMap, k.AssetExists)
		if n == 0 {
			continue
		}
		if _, err := k.PatchIndex(name, hash, joinIndex(fmRaw, hasFM, newBody)); err != nil {
			return touched, fmt.Errorf("%s: %w", basePath, err)
		}
		if name == "" {
			touched = append(touched, "index.md")
		} else {
			touched = append(touched, basePath)
		}
	}
	return touched, nil
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
	FMAppend    map[string]interface{} `json:"frontmatter_append"`
	FMRemove    map[string]interface{} `json:"frontmatter_remove"`
	Unset       []string               `json:"unset"`
	Body        string                 `json:"body"`
	IfMatch     string                 `json:"if_match"`
	OldString   string                 `json:"old_string"`
	NewString   string                 `json:"new_string"`
	ReplaceAll  bool                   `json:"replace_all"`
	Edits       []patchEditItem        `json:"edits"`
}

// batchOpBytes is what one operation adds to the batch's aggregate size. A
// write counts its final body and frontmatter; a patch counts what the caller
// sent (the edits, plus the frontmatter when it changes it), not the full body
// after the patch, so fifteen one-line patches on large concepts are not
// rejected for bytes they never transmitted (D318). The limit bounds the
// request line, not the result.
func batchOpBytes(op batchOperationRequest, hasEdits bool, body string, fm *okf.Frontmatter) int {
	if op.Op != "patch" {
		return len(body) + len(fm.Serialize())
	}
	n := len(op.OldString) + len(op.NewString)
	if hasEdits {
		for _, e := range op.Edits {
			n += len(e.OldString) + len(e.NewString)
		}
	}
	if len(op.Frontmatter) > 0 || len(op.FMAppend) > 0 || len(op.FMRemove) > 0 || len(op.Unset) > 0 {
		n += len(fm.Serialize())
	}
	return max(n, 1)
}

// batchResultEntry is one applied operation's reported outcome, in request order.
type batchResultEntry struct {
	ID          string        `json:"id"`
	ContentHash string        `json:"content_hash"`
	Findings    []findingOut  `json:"findings,omitempty"`
	Repaired    []repairedOut `json:"repaired,omitempty"`
}

func toolConceptBatch(k *kb.KB, facts *factFinder) Tool {
	return Tool{
		Name: "concept_batch",
		Description: "All-or-nothing write/patch of distinct concepts: one commit and log entry; a failure leaves the KB untouched. " +
			fmt.Sprintf("At most %d operations and %s of decoded content per call (a patch counts its edits). ", conceptBatchMaxOps, byteBudget(conceptBatchMaxTotalBytes)) +
			"Each operation is 'write' (frontmatter, body, if_match optional = create-only) or 'patch' (if_match required; the concept_patch fields). " +
			"Not covered: delete, move, expand, assets, indexes. Returns id, content_hash and findings per operation.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"required": ["operations"],
			"properties": {
				"operations": {
					"type": "array",
					"description": "Distinct concept IDs, applied atomically.",
					"items": {
						"type": "object",
						"required": ["op", "id"],
						"properties": {
							"op": {"type": "string"},
							"id": {"type": "string"},
							"frontmatter": {"type": "object"},
							"frontmatter_append": {"type": "object"},
							"frontmatter_remove": {"type": "object"},
							"unset": {"type": "array", "items": {"type": "string"}},
							"body": {"type": "string"},
							"if_match": {"type": "string"},
							"old_string": {"type": "string"},
							"new_string": {"type": "string"},
							"replace_all": {"type": "boolean"},
							"edits": {
								"type": "array",
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
			seen := make(map[string]bool, len(params.Operations))
			totalBytes := 0
			droppedByID := map[string][]findingOut{}
			prevByID := map[string]string{} // body before the batch: read before the write (D351)

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
				if existed {
					prevByID[op.ID] = existing.Body
				}
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
					if err := rejectToolParamKeys(op.Frontmatter, false); err != nil {
						return errorResult(fmt.Sprintf("%s: %v", label, err)), nil
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
					// Frontmatter-only is legitimate, as in concept_patch (#321).
					pfm := patchFrontmatter{Merge: op.Frontmatter, Append: op.FMAppend, Remove: op.FMRemove, Unset: op.Unset}
					hasFM := pfm.any()
					if !hasEdits && !hasSingle && !hasFM {
						return errorResult(fmt.Sprintf("%s: 'old_string' is required (or provide 'edits' for a batch of edits, or 'frontmatter'/'frontmatter_append'/'frontmatter_remove'/'unset' alone)", label)), nil
					}
					if hasEdits && len(op.Edits) == 0 && !hasFM {
						return errorResult(fmt.Sprintf("%s: 'edits' cannot be empty", label)), nil
					}
					if hasSingle && op.OldString == "" {
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
					} else if hasSingle {
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
					dropped, err := applyPatchFrontmatter(fm, pfm)
					if err != nil {
						return errorResult(fmt.Sprintf("%s: %v", label, err)), nil
					}
					if msg := droppedFinding(op.ID, dropped); msg != nil {
						droppedByID[op.ID] = msg
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

				totalBytes += batchOpBytes(op, hasEdits, body, fm)
				if totalBytes > conceptBatchMaxTotalBytes {
					return errorResult(fmt.Sprintf("%s: aggregate batch content exceeds %d bytes", label, conceptBatchMaxTotalBytes)), nil
				}

				ops = append(ops, kb.BatchWriteOp{ID: okf.ConceptID(op.ID), FM: fm, Body: body, IfMatch: op.IfMatch})
			}

			logMessage := fmt.Sprintf("concept_batch (%d operation(s)):\n%s", len(ops), strings.Join(batchLogLines(ops), "\n"))

			results, err := k.WriteConceptBatch(ops, logMessage, nil)
			if err != nil {
				if errors.Is(err, okf.ErrStaleWrite) {
					return errorResult("stale_write: " + err.Error()), nil
				}
				return errorResult("concept_batch: " + err.Error()), nil
			}

			entries := make([]batchResultEntry, len(results))
			factBudget := newFactBudget() // one lookup budget for the whole batch
			for i, r := range results {
				findings, repaired, hashes := repairWritten(k, []string{r.ID}, nil)
				findings = append(findings, droppedByID[r.ID]...)
				findings = withFacts(findings, facts, ctx, r.ID, prevByID[r.ID], factBudget)
				hash := r.ContentHash
				if h, ok := hashes[r.ID]; ok {
					hash = h
				}
				entries[i] = batchResultEntry{ID: r.ID, ContentHash: hash, Findings: findings, Repaired: repaired}
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

func toolConceptDelete(k *kb.KB) Tool {
	return Tool{
		Name:        "concept_delete",
		Description: "Permanently removes a concept (git commit). Refuses with inbound_links (listing the linkers) unless force=true; links are never rewritten. To keep the page use supersede; to rename, concept_move. An expanded concept that owns assets also needs force=true.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"required": ["id"],
			"properties": {
				"id": {
					"type": "string"
				},
				"if_match": {
					"type": "string",
					"description": "Optional content hash"
				},
				"force": {
					"type": "boolean",
					"description": "Accept broken inbound links and deletion of an expanded concept's assets"
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

			linkers, err := deleteBlockers(ctx, k, params.ID)
			if err != nil {
				return errorResult(fmt.Sprintf("concept_delete %q: read links: %v", params.ID, err)), nil
			}
			if len(linkers) > 0 && !params.Force {
				// Dangling links to a missing concept are not a reason to refuse:
				// report not found, as the delete itself would.
				if _, err := k.ReadConcept(okf.ConceptID(params.ID)); errors.Is(err, okf.ErrNotFound) {
					return errorResult(fmt.Sprintf("concept_delete %q: not found", params.ID)), nil
				}
				return errorResult(fmt.Sprintf("inbound_links: %d concept(s) still link to %s: %s. "+
					"Relink them, use supersede (retire the page but keep it) or concept_move (rename with backlink rewrite), "+
					"or pass force: true to delete anyway and leave these links broken",
					len(linkers), params.ID, formatIDList(linkers))), nil
			}

			if _, err := k.DeleteConceptWithAssets(okf.ConceptID(params.ID), params.Force); err != nil {
				if errors.Is(err, okf.ErrNotFound) {
					return errorResult(fmt.Sprintf("concept_delete %q: not found", params.ID)), nil
				}
				return errorResult(fmt.Sprintf("concept_delete %q: %v", params.ID, err)), nil
			}

			_ = k.AppendLog("concept_delete: "+params.ID, time.Now())
			msg := fmt.Sprintf("deleted %s", params.ID)
			if len(linkers) > 0 {
				msg += fmt.Sprintf("\nWarning: %d concept(s) still link to %s: %s", len(linkers), params.ID, formatIDList(linkers))
			}
			return textResult(msg), nil
		},
	}
}

// maxListedLinkers caps how many linking concepts a concept_delete message names.
const maxListedLinkers = 20

// visibleInboundLinkers returns, sorted, the concepts the caller can see that
// link to id. The concept itself and, for an expanded concept, everything under
// id+"/" (its satellites and index) are not inbound links: the same call removes
// or preserves the files that carry them. A linker hidden from the caller is
// dropped so a narrowed token learns nothing about it (D271).
func visibleInboundLinkers(ctx requestContext, k *kb.KB, id string) ([]string, error) {
	in, err := k.IncomingLinks()
	if err != nil {
		return nil, err
	}
	var out []string
	for src := range in[okf.ConceptID(id)] {
		s := string(src)
		if s == id || strings.HasPrefix(s, id+"/") || !Visible(ctx, k, s) {
			continue
		}
		out = append(out, s)
	}
	sort.Strings(out)
	return out, nil
}

// formatIDList joins ids, naming at most maxListedLinkers of them.
func formatIDList(ids []string) string {
	if len(ids) <= maxListedLinkers {
		return strings.Join(ids, ", ")
	}
	return strings.Join(ids[:maxListedLinkers], ", ") + fmt.Sprintf(", and %d more", len(ids)-maxListedLinkers)
}
