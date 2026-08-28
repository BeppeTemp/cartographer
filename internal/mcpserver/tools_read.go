package mcpserver

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/BeppeTemp/cartographer/internal/gitx"
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
		Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
			archives, err := k.ListArchives()
			if err != nil {
				return errorResult(fmt.Sprintf("list maps: %v", err)), nil
			}

			var sb strings.Builder
			if WholeVisible(ctx, k, false) {
				indexContent, err := k.ReadIndex("")
				if err != nil {
					return errorResult(fmt.Sprintf("read index.md: %v", err)), nil
				}
				sb.WriteString(indexContent)
			}
			sb.WriteString("\n\n---\n\n## Maps & Journals\n\n")
			if len(archives) == 0 {
				sb.WriteString("No maps found.\n")
			} else {
				for _, a := range archives {
					kind := "map"
					if meta, err := k.ReadArchiveMeta(a); err == nil {
						if v, ok := meta.Get("kind"); ok {
							kind, _ = v.(string)
						}
					}
					if !VisibleCollection(ctx, k, a, kind) {
						continue
					}
					conceptCount, expandedCount := visibleCounts(ctx, k, a)
					if WholeVisible(ctx, k, false) {
						expandedCount, _ = k.ExpandedCount(a)
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
		Name: "index_get",
		Description: "Reads the index.md of the given folder (root if path is empty). By default returns " +
			"the raw Markdown verbatim (byte-for-byte, for backward compatibility). Pass 'with_hash: true' " +
			"to get a structured {path, content, content_hash} response instead — the content_hash is the " +
			"'if_match' expected by index_patch when curating the root or a Map/Journal's index.md.",
		ReadOnly: true,
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"path": {
					"type": "string",
					"description": "Path relative to KB root (e.g. 'maintenance'). Empty = root."
				},
				"with_hash": {
					"type": "boolean",
					"description": "If true, returns structured {path, content, content_hash} instead of raw Markdown. Optional, default false."
				}
			}
		}`),
		Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
			var params struct {
				Path     string `json:"path"`
				WithHash bool   `json:"with_hash"`
			}
			json.Unmarshal(args, &params)

			if !params.WithHash {
				content, err := k.ReadIndex(params.Path)
				if err != nil {
					return errorResult(fmt.Sprintf("index_get %q: %v", params.Path, err)), nil
				}
				return textResult(content), nil
			}

			content, err := k.ReadIndex(params.Path)
			if err != nil {
				return errorResult(fmt.Sprintf("index_get %q: %v", params.Path, err)), nil
			}
			result := map[string]interface{}{
				"path":         params.Path,
				"content":      content,
				"content_hash": okf.ContentHash(content),
			}
			out, _ := json.MarshalIndent(result, "", "  ")
			return textResult(string(out)), nil
		},
	}
}

// --- concept_read ---

// conceptReadSizeGuard is the body size (bytes) above which concept_read
// returns an outline instead of the full content when neither 'section' nor
// 'outline' was requested — a 92 KB concept blew past a client's token
// budget and forced a workaround outside the MCP surface (D78). 'full: true'
// overrides the guard.
const conceptReadSizeGuard = okf.ConceptReadSizeGuard

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
		Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
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
		Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
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

// --- changes_since ---

const (
	defaultChangesSinceWindow = 7 * 24 * time.Hour
	defaultChangesSinceLimit  = 100
	maxChangesSinceLimit      = 500
)

type changesSinceConcept struct {
	ID      string   `json:"id"`
	Change  string   `json:"change"`
	LastAt  string   `json:"last_at"`
	Authors []string `json:"authors"`
	Ops     []string `json:"ops"`
}

type changesSinceResult struct {
	Since        string                `json:"since"`
	Head         string                `json:"head"`
	CommitCount  int                   `json:"commit_count"`
	Concepts     []changesSinceConcept `json:"concepts"`
	OtherChanges int                   `json:"other_changes"`
	Truncated    bool                  `json:"truncated"`
	Note         string                `json:"note,omitempty"`
}

func parseChangesSince(value string, now time.Time) (time.Time, error) {
	if value == "" {
		return now.UTC().Add(-defaultChangesSinceWindow), nil
	}
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed, nil
	}
	if len(value) >= 2 {
		unit := value[len(value)-1]
		n, err := strconv.ParseInt(value[:len(value)-1], 10, 64)
		if err == nil && n >= 0 && (unit == 'd' || unit == 'h') {
			unitDuration := time.Hour
			if unit == 'd' {
				unitDuration *= 24
			}
			if n <= int64((time.Duration(1<<63-1))/unitDuration) {
				return now.UTC().Add(-time.Duration(n) * unitDuration), nil
			}
		}
	}
	return time.Time{}, fmt.Errorf("invalid since %q; use an RFC3339 timestamp or duration shorthand <N>d/<N>h (for example 2d or 48h)", value)
}

func changeSinceStatus(status string) string {
	switch status {
	case "A":
		return "added"
	case "D":
		return "deleted"
	case "R":
		return "moved"
	default:
		return "modified"
	}
}

func isCartographerPath(path string) bool {
	return path == ".cartographer" || strings.HasPrefix(path, ".cartographer/")
}

func toolChangesSince(k *kb.KB) Tool {
	return Tool{
		Name:        "changes_since",
		Description: "Summarizes concept changes from git history since an RFC3339 timestamp or a duration such as 2d or 48h. Aggregates each concept's newest change, latest timestamp, authors, and recent operations.",
		ReadOnly:    true,
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"since": {
					"type": "string",
					"description": "RFC3339 timestamp or duration shorthand <N>d/<N>h, such as '2d' or '48h'. Default 7d."
				},
				"limit": {
					"type": "integer",
					"description": "Maximum concepts to return. Default 100; maximum 500."
				}
			}
		}`),
		Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
			var params struct {
				Since string `json:"since"`
				Limit int    `json:"limit"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return errorResult("invalid params: " + err.Error()), nil
			}

			since, err := parseChangesSince(params.Since, time.Now())
			if err != nil {
				return errorResult(err.Error()), nil
			}
			commits, err := gitx.LogNameStatus(k.Root, since)
			if err != nil {
				return errorResult("changes_since git log: " + err.Error()), nil
			}
			head, _ := gitx.HeadSHA(k.Root)

			limit := params.Limit
			if limit <= 0 {
				limit = defaultChangesSinceLimit
			}
			if limit > maxChangesSinceLimit {
				limit = maxChangesSinceLimit
			}

			conceptByID := map[string]*changesSinceConcept{}
			authorSeen := map[string]map[string]bool{}
			opSeen := map[string]map[string]bool{}
			movedTo := map[string]string{}
			resolveMovedID := func(id string) string {
				for movedTo[id] != "" {
					id = movedTo[id]
				}
				return id
			}
			otherChanges := 0
			for _, commit := range commits {
				for _, file := range commit.Files {
					if isCartographerPath(file.Path) {
						continue
					}
					id, ok := kb.GitPathToConceptID(file.Path)
					if !ok {
						otherChanges++
						continue
					}
					if file.Status == "R" {
						if oldID, oldOK := kb.GitPathToConceptID(file.OldPath); oldOK {
							movedTo[oldID] = id
						}
					}
					id = resolveMovedID(id)
					if !Visible(ctx, k, id) {
						continue
					}
					info, exists := conceptByID[id]
					if !exists {
						info = &changesSinceConcept{
							ID:      id,
							Change:  changeSinceStatus(file.Status),
							LastAt:  commit.At.UTC().Format(time.RFC3339),
							Authors: []string{},
							Ops:     []string{},
						}
						conceptByID[id] = info
						authorSeen[id] = map[string]bool{}
						opSeen[id] = map[string]bool{}
					}
					if !authorSeen[id][commit.Author] {
						authorSeen[id][commit.Author] = true
						info.Authors = append(info.Authors, commit.Author)
					}
					if !opSeen[id][commit.Subject] && len(info.Ops) < 5 {
						opSeen[id][commit.Subject] = true
						info.Ops = append(info.Ops, commit.Subject)
					}
				}
			}

			concepts := make([]changesSinceConcept, 0, len(conceptByID))
			for _, info := range conceptByID {
				concepts = append(concepts, *info)
			}
			sort.Slice(concepts, func(i, j int) bool {
				if concepts[i].LastAt == concepts[j].LastAt {
					return concepts[i].ID < concepts[j].ID
				}
				return concepts[i].LastAt > concepts[j].LastAt
			})
			truncated := len(concepts) > limit
			if truncated {
				concepts = concepts[:limit]
			}

			result := changesSinceResult{
				Since:        since.UTC().Format(time.RFC3339),
				Head:         head,
				CommitCount:  0,
				Concepts:     concepts,
				OtherChanges: 0,
				Truncated:    truncated,
			}
			if WholeVisible(ctx, k, false) {
				result.CommitCount, result.OtherChanges = len(commits), otherChanges
			}
			if len(commits) == 0 {
				result.Note = fmt.Sprintf("no commits since %s", result.Since)
			}
			out, _ := json.MarshalIndent(result, "", "  ")
			return textResult(string(out)), nil
		},
	}
}

// visibleCounts computes overview counters from the caller's view, before any
// aggregation. ExpandedCount is intentionally limited to visible concepts so
// a type-restricted principal cannot infer hidden satellites.
func visibleCounts(ctx requestContext, k *kb.KB, archive string) (concepts, expanded int) {
	_ = k.WalkConcepts(func(id okf.ConceptID, _ string) error {
		idString := string(id)
		if !strings.HasPrefix(idString, archive+"/") || !Visible(ctx, k, idString) {
			return nil
		}
		concepts++
		return nil
	})
	return concepts, expanded
}

// --- map_list ---

func toolMapList(k *kb.KB) Tool {
	return Tool{
		Name:        "map_list",
		Description: "Lists all Maps and Journals in the Atlas with their metadata (kind, ontology_mode, concept_types, expanded-concept count).",
		ReadOnly:    true,
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
		Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
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
				kind := "map"
				if meta, err := k.ReadArchiveMeta(name); err == nil {
					if v, ok := meta.Get("kind"); ok {
						kind, _ = v.(string)
					}
				}
				if !VisibleCollection(ctx, k, name, kind) {
					continue
				}
				info := mapInfo{Name: name}
				if WholeVisible(ctx, k, false) {
					info.Expanded, _ = k.ExpandedCount(name)
				}
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

type conceptListFilter struct {
	key   string
	value string
	not   bool
}

func parseConceptListFilter(entry string) (conceptListFilter, error) {
	// Check != first: in "key!=a=b", the later = belongs to the value.
	if i := strings.Index(entry, "!="); i >= 0 {
		key := strings.TrimSpace(entry[:i])
		if key == "" {
			return conceptListFilter{}, fmt.Errorf("malformed where entry %q: key must not be empty (expected key=value or key!=value)", entry)
		}
		return conceptListFilter{key: key, value: strings.TrimSpace(entry[i+2:]), not: true}, nil
	}
	if i := strings.Index(entry, "="); i >= 0 {
		key := strings.TrimSpace(entry[:i])
		if key == "" {
			return conceptListFilter{}, fmt.Errorf("malformed where entry %q: key must not be empty (expected key=value or key!=value)", entry)
		}
		return conceptListFilter{key: key, value: strings.TrimSpace(entry[i+1:])}, nil
	}
	return conceptListFilter{}, fmt.Errorf("malformed where entry %q: expected key=value or key!=value", entry)
}

func frontmatterValueMatches(value interface{}, want string) bool {
	switch v := value.(type) {
	case string:
		return v == want
	case []string:
		for _, item := range v {
			if item == want {
				return true
			}
		}
		return false
	case nil:
		// An explicit YAML `key:` is the parser's empty scalar value.
		return want == ""
	default:
		return false
	}
}

func matchesConceptListFilters(fm *okf.Frontmatter, filters []conceptListFilter) bool {
	for _, filter := range filters {
		value, exists := fm.Get(filter.key)
		matches := exists && frontmatterValueMatches(value, filter.value)
		if filter.not == matches {
			return false
		}
	}
	return true
}

func parseConceptListTimestamp(value string) (time.Time, error) {
	if len(value) == len("2006-01-02") {
		return time.Parse("2006-01-02", value)
	}
	return time.Parse(time.RFC3339, value)
}

func toolConceptList(k *kb.KB) Tool {
	return Tool{
		Name:        "concept_list",
		Description: "Exhaustive inventory of concepts (id, title, type from frontmatter) under a scope prefix, sorted by id. Empty scope lists the whole KB. where predicates and timestamp ranges are applied before limit. The bounded equivalent of 'ls -R'; use index_get for curated, progressive-disclosure navigation instead.",
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
				},
				"where": {
					"type": "array",
					"items": {"type": "string"},
					"description": "ANDed frontmatter predicates key=value or key!=value. Scalar fields match exactly and list fields match any element; matching is case-sensitive. A missing key matches != but not =."
				},
				"timestamp_before": {
					"type": "string",
					"description": "Strict upper bound for frontmatter timestamp, as RFC3339 or YYYY-MM-DD (midnight UTC)."
				},
				"timestamp_after": {
					"type": "string",
					"description": "Strict lower bound for frontmatter timestamp, as RFC3339 or YYYY-MM-DD (midnight UTC)."
				}
			}
		}`),
		Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
			var params struct {
				Scope           string   `json:"scope"`
				Limit           int      `json:"limit"`
				Where           []string `json:"where"`
				TimestampBefore *string  `json:"timestamp_before"`
				TimestampAfter  *string  `json:"timestamp_after"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return errorResult("invalid params: " + err.Error()), nil
			}

			limit := params.Limit
			if limit <= 0 {
				limit = defaultConceptListLimit
			}

			scope := strings.TrimSuffix(strings.ReplaceAll(params.Scope, "\\", "/"), "/")
			filters := make([]conceptListFilter, 0, len(params.Where))
			for _, entry := range params.Where {
				filter, err := parseConceptListFilter(entry)
				if err != nil {
					return errorResult(err.Error()), nil
				}
				filters = append(filters, filter)
			}

			var before, after *time.Time
			if params.TimestampBefore != nil {
				parsed, err := parseConceptListTimestamp(*params.TimestampBefore)
				if err != nil {
					return errorResult("invalid timestamp_before: expected RFC3339 or YYYY-MM-DD"), nil
				}
				before = &parsed
			}
			if params.TimestampAfter != nil {
				parsed, err := parseConceptListTimestamp(*params.TimestampAfter)
				if err != nil {
					return errorResult("invalid timestamp_after: expected RFC3339 or YYYY-MM-DD"), nil
				}
				after = &parsed
			}
			filtersApplied := len(filters) > 0 || before != nil || after != nil
			timestampFilter := before != nil || after != nil

			type conceptEntry struct {
				ID    string `json:"id"`
				Title string `json:"title,omitempty"`
				Type  string `json:"type,omitempty"`
			}
			entries := []conceptEntry{}
			examined := 0
			skippedTimestamp := 0
			if err := k.WalkConcepts(func(id okf.ConceptID, content string) error {
				idStr := string(id)
				if scope != "" && idStr != scope && !strings.HasPrefix(idStr, scope+"/") {
					return nil
				}
				if !Visible(ctx, k, idStr) {
					return nil
				}
				examined++
				fmRaw, _, _ := okf.SplitFrontmatter(content)
				var title, typ string
				fm, parseErr := okf.ParseFrontmatter(fmRaw)
				if parseErr == nil {
					if v, ok := fm.Get("title"); ok {
						if s, ok := v.(string); ok {
							title = s
						}
					}
					typ = fm.Type()
				}
				if filtersApplied {
					// Malformed frontmatter is examined but cannot match a filter.
					if parseErr != nil || !matchesConceptListFilters(fm, filters) {
						return nil
					}
					if timestampFilter {
						value, exists := fm.Get("timestamp")
						timestamp, timestampOK := value.(string)
						parsed, timestampErr := parseConceptListTimestamp(timestamp)
						if !exists || !timestampOK || timestampErr != nil {
							skippedTimestamp++
							return nil
						}
						if (before != nil && !parsed.Before(*before)) || (after != nil && !parsed.After(*after)) {
							return nil
						}
					}
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
			if filtersApplied {
				result["examined"] = examined
			}
			if timestampFilter {
				result["skipped_timestamp"] = skippedTimestamp
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
		Description: "Returns graph neighbors up to depth hops (default 1): direction out means concepts linked from this one, in means concepts that link to it, and both follows either edge. Useful for scoping lint and understanding relationships.",
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
				},
				"direction": {
					"type": "string",
					"enum": ["out", "in", "both"],
					"description": "Edge direction: out (default) finds linked concepts, in finds backlinks, both follows either."
				}
			}
		}`),
		Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
			var params struct {
				ID        string `json:"id"`
				Depth     int    `json:"depth"`
				Direction string `json:"direction"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return errorResult("invalid params: " + err.Error()), nil
			}
			if params.ID == "" {
				return errorResult("'id' is required"), nil
			}

			direction := params.Direction
			if direction == "" {
				direction = "out"
			}
			if direction != "out" && direction != "in" && direction != "both" {
				return errorResult(fmt.Sprintf("invalid direction %q: expected out, in, or both", direction)), nil
			}
			depth := params.Depth
			if depth <= 0 {
				depth = 1
			}

			neighbors, err := k.GraphNeighbors(okf.ConceptID(params.ID), depth, direction)
			if err != nil {
				return errorResult(fmt.Sprintf("graph_neighbors %q: %v", params.ID, err)), nil
			}

			type neighbor struct {
				ID       string `json:"id"`
				Distance int    `json:"distance"`
			}
			var list []neighbor
			for id, dist := range neighbors {
				if !Visible(ctx, k, id) {
					continue
				}
				list = append(list, neighbor{ID: id, Distance: dist})
			}
			sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })

			result := map[string]interface{}{
				"id":        params.ID,
				"depth":     depth,
				"direction": direction,
				"neighbors": list,
				"count":     len(list),
			}
			out, _ := json.MarshalIndent(result, "", "  ")
			return textResult(string(out)), nil
		},
	}
}
