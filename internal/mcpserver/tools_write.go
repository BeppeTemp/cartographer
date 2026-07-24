package mcpserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
		Handler: func(args json.RawMessage) (ToolResult, error) {
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
		Handler: func(args json.RawMessage) (ToolResult, error) {
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
				}
			}
		}`),
		Handler: func(args json.RawMessage) (ToolResult, error) {
			var params struct {
				Name         string   `json:"name"`
				Title        string   `json:"title"`
				Kind         string   `json:"kind"`
				ConceptTypes []string `json:"concept_types"`
				OntologyMode string   `json:"ontology_mode"`
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

			if err := k.CreateMap(params.Name, params.Title, params.Kind, params.ConceptTypes, params.OntologyMode); err != nil {
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
		Handler: func(args json.RawMessage) (ToolResult, error) {
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
		Handler: func(args json.RawMessage) (ToolResult, error) {
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
		Handler: func(args json.RawMessage) (ToolResult, error) {
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
		Handler: func(args json.RawMessage) (ToolResult, error) {
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
		Handler: func(args json.RawMessage) (ToolResult, error) {
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
			"services/) to point at the new IDs.",
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
		Handler: func(args json.RawMessage) (ToolResult, error) {
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

				valid = append(valid, validMove{sourceID: m.SourceID, targetID: m.TargetID, fm: fm, body: data.Body})
			}

			// --- apply pass: all entries already validated above. ---
			moveMap := make(map[string]string, len(valid))
			applied := make([]conceptMoveEntry, 0, len(valid))
			logLines := make([]string, 0, len(valid)+1)

			for _, mv := range valid {
				if _, err := k.WriteConcept(okf.ConceptID(mv.targetID), mv.fm, mv.body, ""); err != nil {
					return errorResult(fmt.Sprintf("concept_move: write target %q: %v", mv.targetID, err)), nil
				}

				srcPath := filepath.Join(k.DataRoot(), mv.sourceID+".md")
				if err := os.Remove(srcPath); err != nil && !os.IsNotExist(err) {
					return errorResult(fmt.Sprintf("concept_move: remove source %q: %v", mv.sourceID, err)), nil
				}

				// Keep the keyword and FTS5 indexes in sync: deindex the old ID and
				// index the new one, same pattern as concept_delete/concept_write.
				live.remove(mv.sourceID)
				if targetData, readErr := k.ReadConcept(okf.ConceptID(mv.targetID)); readErr == nil {
					live.add(mv.targetID, targetData.Content)
					if sqlIdx != nil {
						if err := sqlIdx.Delete(mv.sourceID); err != nil {
							fmt.Fprintf(os.Stderr, "concept_move: sqlindex delete %q: %v\n", mv.sourceID, err)
						}
						if err := sqlIdx.Upsert(mv.targetID, targetData.ContentHash, targetData.Content); err != nil {
							fmt.Fprintf(os.Stderr, "concept_move: sqlindex upsert %q: %v\n", mv.targetID, err)
						}
					}
				}

				moveMap[mv.sourceID] = mv.targetID
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
		newBody, count := kb.RewriteLinks(body, okf.IDToPath(id), moveMap)
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

// --- concept_delete ---

func toolConceptDelete(k *kb.KB, live *liveIndex, sqlIdx *sqlindex.Index) Tool {
	return Tool{
		Name: "concept_delete",
		Description: "Permanently removes a concept from the KB (git commit). Inbound links " +
			"to the removed concept are NOT updated — run lint to find broken links.",
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
				}
			}
		}`),
		Handler: func(args json.RawMessage) (ToolResult, error) {
			var params struct {
				ID      string `json:"id"`
				IfMatch string `json:"if_match"`
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

			if err := k.DeleteConcept(okf.ConceptID(params.ID)); err != nil {
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
