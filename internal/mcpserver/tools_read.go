package mcpserver

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// --- atlas_overview ---

func toolAtlasOverview(k *kb.KB) Tool {
	return Tool{
		Name:        "atlas_overview",
		Description: "Returns the Atlas's root index.md plus the list of Maps and Journals, each with its concept (and expanded-concept) count.",
		ReadOnly:    true,
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
		Handler: func(args json.RawMessage) (ToolResult, error) {
			indexContent, err := k.ReadIndex("")
			if err != nil {
				return errorResult(fmt.Sprintf("read index.md: %v", err)), nil
			}

			archives, err := k.ListArchives()
			if err != nil {
				return errorResult(fmt.Sprintf("list maps: %v", err)), nil
			}

			var sb strings.Builder
			sb.WriteString(indexContent)
			sb.WriteString("\n\n---\n\n## Maps & Journals\n\n")
			if len(archives) == 0 {
				sb.WriteString("No maps found.\n")
			} else {
				for _, a := range archives {
					conceptCount, err := k.ConceptCount(a)
					if err != nil {
						conceptCount = 0
					}
					expandedCount, err := k.ExpandedCount(a)
					if err != nil {
						expandedCount = 0
					}
					if expandedCount > 0 {
						sb.WriteString(fmt.Sprintf("- **%s** (%d concepts, %d expanded)\n", a, conceptCount, expandedCount))
					} else {
						sb.WriteString(fmt.Sprintf("- **%s** (%d concepts)\n", a, conceptCount))
					}
				}
			}
			return textResult(sb.String()), nil
		},
	}
}

// --- index_get ---

func toolIndexGet(k *kb.KB) Tool {
	return Tool{
		Name:        "index_get",
		Description: "Reads the index.md of the given folder (root if path is empty).",
		ReadOnly:    true,
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"path": {
					"type": "string",
					"description": "Path relative to KB root (e.g. 'maintenance'). Empty = root."
				}
			}
		}`),
		Handler: func(args json.RawMessage) (ToolResult, error) {
			var params struct {
				Path string `json:"path"`
			}
			json.Unmarshal(args, &params)

			content, err := k.ReadIndex(params.Path)
			if err != nil {
				return errorResult(fmt.Sprintf("index_get %q: %v", params.Path, err)), nil
			}
			return textResult(content), nil
		},
	}
}

// --- concept_read ---

// conceptReadSizeGuard is the body size (bytes) above which concept_read
// returns an outline instead of the full content when neither 'section' nor
// 'outline' was requested — a 92 KB concept blew past a client's token
// budget and forced a workaround outside the MCP surface (D78). 'full: true'
// overrides the guard.
const conceptReadSizeGuard = 60000

// maxSectionNotFoundHeadings caps the heading list surfaced in a "section
// not found" error, so the agent sees the real outline instead of guessing.
const maxSectionNotFoundHeadings = 50

// headingsToOutline renders okf.Heading values into the JSON outline shape
// shared by the 'outline' param and the size-guard response.
func headingsToOutline(headings []okf.Heading) []map[string]interface{} {
	outline := make([]map[string]interface{}, 0, len(headings))
	for _, h := range headings {
		outline = append(outline, map[string]interface{}{
			"level": h.Level,
			"title": h.Title,
			"bytes": h.Bytes,
		})
	}
	return outline
}

func toolConceptRead(k *kb.KB) Tool {
	return Tool{
		Name: "concept_read",
		Description: "Reads a concept by ID. Returns content, content_hash, frontmatter_raw, body. " +
			"If 'section' is specified, returns only that section (error lists the available headings " +
			"if the section is not found). If 'outline' is true, returns the heading structure " +
			"({level, title, bytes} per heading) without content. Bodies over 60 KB are returned as an " +
			"outline by default (note explains why); pass 'full: true' to force the full content.",
		ReadOnly: true,
		InputSchema: json.RawMessage(`{
			"type": "object",
			"required": ["id"],
			"properties": {
				"id": {
					"type": "string",
					"description": "Concept ID (path relative to KB root without .md, e.g. 'maintenance/cert-rotation/runbook')"
				},
				"section": {
					"type": "string",
					"description": "Heading of the section to extract (e.g. '# Schema'). Optional."
				},
				"outline": {
					"type": "boolean",
					"description": "If true, returns only the heading outline ({level, title, bytes}), no content. Optional."
				},
				"full": {
					"type": "boolean",
					"description": "If true, forces the full content even if the body exceeds the 60 KB size guard. Optional."
				}
			}
		}`),
		Handler: func(args json.RawMessage) (ToolResult, error) {
			var params struct {
				ID      string `json:"id"`
				Section string `json:"section"`
				Outline bool   `json:"outline"`
				Full    bool   `json:"full"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return errorResult("invalid params: " + err.Error()), nil
			}
			if params.ID == "" {
				return errorResult("'id' is required"), nil
			}

			data, err := k.ReadConcept(okf.ConceptID(params.ID))
			if err != nil {
				return errorResult(fmt.Sprintf("concept_read %q: %v", params.ID, err)), nil
			}

			if params.Outline {
				result := map[string]interface{}{
					"id":           params.ID,
					"content_hash": data.ContentHash,
					"outline":      headingsToOutline(okf.ListHeadings(data.Body)),
					"body_bytes":   len(data.Body),
				}
				out, _ := json.MarshalIndent(result, "", "  ")
				return textResult(string(out)), nil
			}

			if params.Section != "" {
				section, found := okf.ExtractSection(data.Body, params.Section)
				if !found {
					headings := okf.ListHeadings(data.Body)
					if len(headings) > maxSectionNotFoundHeadings {
						headings = headings[:maxSectionNotFoundHeadings]
					}
					available := make([]string, 0, len(headings))
					for _, h := range headings {
						available = append(available, strings.Repeat("#", h.Level)+" "+h.Title)
					}
					msg := fmt.Sprintf("section %q not found in %q", params.Section, params.ID)
					if len(available) > 0 {
						msg += "; available headings: " + strings.Join(available, " | ")
					} else {
						msg += "; concept has no headings"
					}
					return errorResult(msg), nil
				}
				result := map[string]interface{}{
					"id":           params.ID,
					"section":      params.Section,
					"content":      section,
					"content_hash": data.ContentHash,
				}
				out, _ := json.MarshalIndent(result, "", "  ")
				return textResult(string(out)), nil
			}

			if !params.Full && len(data.Body) > conceptReadSizeGuard {
				result := map[string]interface{}{
					"id":           params.ID,
					"content_hash": data.ContentHash,
					"outline":      headingsToOutline(okf.ListHeadings(data.Body)),
					"body_bytes":   len(data.Body),
					"note": fmt.Sprintf("body is %d bytes (over the %d byte guard) — use 'section' to read "+
						"a specific part, or 'full: true' to force the full content", len(data.Body), conceptReadSizeGuard),
				}
				out, _ := json.MarshalIndent(result, "", "  ")
				return textResult(string(out)), nil
			}

			result := map[string]interface{}{
				"id":              params.ID,
				"content":         data.Content,
				"content_hash":    data.ContentHash,
				"frontmatter_raw": data.FrontmatterRaw,
				"body":            data.Body,
			}
			out, _ := json.MarshalIndent(result, "", "  ")
			return textResult(string(out)), nil
		},
	}
}

// --- log_tail ---

func toolLogTail(k *kb.KB) Tool {
	return Tool{
		Name: "log_tail",
		Description: "Returns the last n entries relevant to path. Empty path = root log verbatim. " +
			"A non-empty path never has its own log.md written by log_append (root-log-with-prefix " +
			"convention, D78): it returns any entries in '<path>/log.md' (rare, pre-existing files) " +
			"followed by root-log entries prefixed '[<path>] ', up to n total. Default n = 20.",
		ReadOnly: true,
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"path": {
					"type": "string",
					"description": "Folder relative to KB root. Empty = root."
				},
				"n": {
					"type": "integer",
					"description": "Maximum number of entries. Default 20."
				}
			}
		}`),
		Handler: func(args json.RawMessage) (ToolResult, error) {
			var params struct {
				Path string `json:"path"`
				N    int    `json:"n"`
			}
			json.Unmarshal(args, &params)

			content, err := k.LogTail(params.Path, params.N)
			if err != nil {
				return errorResult(fmt.Sprintf("log_tail: %v", err)), nil
			}
			if content == "" {
				note := map[string]interface{}{
					"entries": 0,
					"note":    fmt.Sprintf("no entries for path %q", params.Path),
				}
				out, _ := json.MarshalIndent(note, "", "  ")
				return textResult(string(out)), nil
			}
			return textResult(content), nil
		},
	}
}

// --- map_list ---

func toolMapList(k *kb.KB) Tool {
	return Tool{
		Name:        "map_list",
		Description: "Lists all Maps and Journals in the Atlas with their metadata (kind, ontology_mode, concept_types, expanded-concept count).",
		ReadOnly:    true,
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
		Handler: func(args json.RawMessage) (ToolResult, error) {
			archives, err := k.ListArchives()
			if err != nil {
				return errorResult(fmt.Sprintf("map_list: %v", err)), nil
			}
			if len(archives) == 0 {
				return textResult("No maps found."), nil
			}

			type mapInfo struct {
				Name         string   `json:"name"`
				Title        string   `json:"title,omitempty"`
				Kind         string   `json:"kind,omitempty"`
				OntologyMode string   `json:"ontology_mode,omitempty"`
				ConceptTypes []string `json:"concept_types,omitempty"`
				Expanded     int      `json:"expanded_concepts"`
			}

			var infos []mapInfo
			for _, name := range archives {
				info := mapInfo{Name: name}
				info.Expanded, _ = k.ExpandedCount(name)
				if meta, err := k.ReadArchiveMeta(name); err == nil {
					if v, ok := meta.Get("title"); ok {
						info.Title, _ = v.(string)
					}
					if v, ok := meta.Get("kind"); ok {
						info.Kind, _ = v.(string)
					}
					if v, ok := meta.Get("ontology_mode"); ok {
						info.OntologyMode, _ = v.(string)
					}
					if v, ok := meta.Get("concept_types"); ok {
						info.ConceptTypes, _ = v.([]string)
					}
				}
				infos = append(infos, info)
			}
			out, _ := json.MarshalIndent(infos, "", "  ")
			return textResult(string(out)), nil
		},
	}
}

// --- concept_list ---

// defaultConceptListLimit caps the number of results returned by
// concept_list when the caller does not pass an explicit limit (D72 WP3).
const defaultConceptListLimit = 500

func toolConceptList(k *kb.KB) Tool {
	return Tool{
		Name:        "concept_list",
		Description: "Exhaustive inventory of concepts (id, title, type from frontmatter) under a scope prefix, sorted by id. Empty scope lists the whole KB. The bounded equivalent of 'ls -R'; use index_get for curated, progressive-disclosure navigation instead.",
		ReadOnly:    true,
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"scope": {
					"type": "string",
					"description": "Path prefix relative to KB root (e.g. 'entities/'). Empty = whole KB."
				},
				"limit": {
					"type": "integer",
					"description": "Maximum number of results. Default 500."
				}
			}
		}`),
		Handler: func(args json.RawMessage) (ToolResult, error) {
			var params struct {
				Scope string `json:"scope"`
				Limit int    `json:"limit"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return errorResult("invalid params: " + err.Error()), nil
			}

			limit := params.Limit
			if limit <= 0 {
				limit = defaultConceptListLimit
			}

			scope := strings.TrimSuffix(strings.ReplaceAll(params.Scope, "\\", "/"), "/")

			type conceptEntry struct {
				ID    string `json:"id"`
				Title string `json:"title,omitempty"`
				Type  string `json:"type,omitempty"`
			}
			entries := []conceptEntry{}
			if err := k.WalkConcepts(func(id okf.ConceptID, content string) error {
				idStr := string(id)
				if scope != "" && idStr != scope && !strings.HasPrefix(idStr, scope+"/") {
					return nil
				}
				fmRaw, _, _ := okf.SplitFrontmatter(content)
				var title, typ string
				if fm, err := okf.ParseFrontmatter(fmRaw); err == nil {
					if v, ok := fm.Get("title"); ok {
						if s, ok := v.(string); ok {
							title = s
						}
					}
					typ = fm.Type()
				}
				entries = append(entries, conceptEntry{ID: idStr, Title: title, Type: typ})
				return nil
			}); err != nil {
				return errorResult(fmt.Sprintf("concept_list: %v", err)), nil
			}

			sort.Slice(entries, func(i, j int) bool { return entries[i].ID < entries[j].ID })

			total := len(entries)
			truncated := total > limit
			if truncated {
				entries = entries[:limit]
			}

			result := map[string]interface{}{
				"count":   len(entries),
				"results": entries,
			}
			if truncated {
				result["truncated"] = true
				result["total"] = total
			}
			out, _ := json.MarshalIndent(result, "", "  ")
			return textResult(string(out)), nil
		},
	}
}

// --- graph_neighbors ---

func toolGraphNeighbors(k *kb.KB) Tool {
	return Tool{
		Name:        "graph_neighbors",
		Description: "Returns the concepts linked from a given concept, up to depth hops (default 1). Useful for scoping lint and understanding relationships.",
		ReadOnly:    true,
		InputSchema: json.RawMessage(`{
			"type": "object",
			"required": ["id"],
			"properties": {
				"id": {
					"type": "string",
					"description": "ConceptID of the starting concept"
				},
				"depth": {
					"type": "integer",
					"description": "Maximum traversal depth (default 1)"
				}
			}
		}`),
		Handler: func(args json.RawMessage) (ToolResult, error) {
			var params struct {
				ID    string `json:"id"`
				Depth int    `json:"depth"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return errorResult("invalid params: " + err.Error()), nil
			}
			if params.ID == "" {
				return errorResult("'id' is required"), nil
			}

			neighbors, err := k.GraphNeighbors(okf.ConceptID(params.ID), params.Depth)
			if err != nil {
				return errorResult(fmt.Sprintf("graph_neighbors %q: %v", params.ID, err)), nil
			}

			type neighbor struct {
				ID       string `json:"id"`
				Distance int    `json:"distance"`
			}
			var list []neighbor
			for id, dist := range neighbors {
				list = append(list, neighbor{ID: id, Distance: dist})
			}

			result := map[string]interface{}{
				"id":        params.ID,
				"depth":     params.Depth,
				"neighbors": list,
				"count":     len(list),
			}
			out, _ := json.MarshalIndent(result, "", "  ")
			return textResult(string(out)), nil
		},
	}
}
