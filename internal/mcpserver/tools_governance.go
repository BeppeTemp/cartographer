package mcpserver

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/lint"
	"github.com/BeppeTemp/cartographer/internal/okf"
	"github.com/BeppeTemp/cartographer/internal/sqlindex"
)

// toolReindex reconciles the derived search indexes with out-of-band KB
// changes. It is deliberately a write-scoped administrative action: although
// it never changes KB content, it writes the server-owned SQLite database.
func toolReindex(k *kb.KB, live *liveIndex, sqlIdx *sqlindex.Index) Tool {
	return Tool{
		Name:        "reindex",
		Description: "Reconciles the derived search index with KB files changed outside MCP, including imports, manual edits, and git pulls. Returns indexed, updated, and removed counts.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
		Handler: func(json.RawMessage) (ToolResult, error) {
			if sqlIdx == nil {
				return errorResult("reindex: SQLite index is unavailable"), nil
			}
			stats, err := ReconcileIndex(k, live, sqlIdx)
			if err != nil {
				return errorResult("reindex: " + err.Error()), nil
			}
			out, _ := json.MarshalIndent(map[string]int{
				"indexed": stats.Indexed,
				"updated": stats.Updated,
				"removed": stats.Removed,
			}, "", "  ")
			return textResult(string(out)), nil
		},
	}
}

// --- validate ---

func toolValidate(k *kb.KB) Tool {
	return Tool{
		Name:     "validate",
		ReadOnly: true,
		Description: "Validates KB files: parseable frontmatter, non-empty type, well-formed reserved files, " +
			"strict ontology where applicable. Returns the list of errors.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"scope": {
					"type": "string",
					"description": "Path relative to KB root. Empty = entire KB."
				}
			}
		}`),
		Handler: func(args json.RawMessage) (ToolResult, error) {
			var params struct {
				Scope string `json:"scope"`
			}
			json.Unmarshal(args, &params)

			errs, err := k.Validate(params.Scope)
			if err != nil {
				return errorResult(fmt.Sprintf("validate: %v", err)), nil
			}

			if len(errs) == 0 {
				return textResult("Validation OK: no errors found."), nil
			}

			// Validation errors are application results: use textResult, not errorResult.
			var sb strings.Builder
			for _, e := range errs {
				sb.WriteString(e.Path + ": " + e.Message + "\n")
			}
			return textResult(strings.TrimRight(sb.String(), "\n")), nil
		},
	}
}

// --- lint ---

func toolLint(k *kb.KB) Tool {
	return Tool{
		Name:     "lint",
		ReadOnly: true,
		Description: "Runs deterministic lint checks: broken links, stale claims (review_after in the past), " +
			"orphan concepts (no incoming links). Returns findings with severity.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"scope": {
					"type": "string",
					"description": "Path prefix to lint (e.g. 'maintenance'). Empty = entire KB."
				},
				"scope_neighbors": {
					"type": "boolean",
					"description": "Also lint 1-hop graph neighbors of concepts in scope (default false)."
				}
			}
		}`),
		Handler: func(args json.RawMessage) (ToolResult, error) {
			var params struct {
				Scope          string `json:"scope"`
				ScopeNeighbors bool   `json:"scope_neighbors"`
			}
			json.Unmarshal(args, &params)

			findings, err := lint.Run(k, params.Scope, params.ScopeNeighbors)
			if err != nil {
				return errorResult(fmt.Sprintf("lint: %v", err)), nil
			}

			if len(findings) == 0 {
				return textResult("Lint OK: no findings."), nil
			}

			type findingJSON struct {
				Path     string `json:"path"`
				Check    string `json:"check"`
				Severity string `json:"severity"`
				Message  string `json:"message"`
			}
			var results []findingJSON
			for _, f := range findings {
				results = append(results, findingJSON{
					Path:     f.Path,
					Check:    f.Check,
					Severity: f.Severity,
					Message:  f.Message,
				})
			}

			result := map[string]interface{}{
				"count":    len(results),
				"findings": results,
			}
			out, _ := json.MarshalIndent(result, "", "  ")
			return textResult(string(out)), nil
		},
	}
}

// --- commit_gate ---

func toolCommitGate(k *kb.KB) Tool {
	return Tool{
		Name: "commit_gate",
		Description: "Checks for open contradictions blocking a commit. Pass the list of changed concept IDs; " +
			"returns pass/fail and any blocking Contradiction concepts with resolution_status=open.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"required": ["changed_ids"],
			"properties": {
				"changed_ids": {
					"type": "array",
					"items": {"type": "string"},
					"description": "List of concept IDs that were modified"
				}
			}
		}`),
		Handler: func(args json.RawMessage) (ToolResult, error) {
			var params struct {
				ChangedIDs []string `json:"changed_ids"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return errorResult("invalid params: " + err.Error()), nil
			}
			if len(params.ChangedIDs) == 0 {
				return errorResult("'changed_ids' is required and must not be empty"), nil
			}

			ids := make([]okf.ConceptID, len(params.ChangedIDs))
			for i, s := range params.ChangedIDs {
				ids[i] = okf.ConceptID(s)
			}

			gate, err := k.CommitGate(ids)
			if err != nil {
				return errorResult(fmt.Sprintf("commit_gate: %v", err)), nil
			}

			type blockerJSON struct {
				Path     string   `json:"path"`
				Involves []string `json:"involves"`
				Kind     string   `json:"kind"`
				Reason   string   `json:"reason"`
			}

			var blockers []blockerJSON
			for _, b := range gate.Blockers {
				blockers = append(blockers, blockerJSON{
					Path:     b.ConceptPath,
					Involves: b.Involves,
					Kind:     b.Kind,
					Reason:   b.Reason,
				})
			}

			result := map[string]interface{}{
				"pass":     gate.Pass,
				"blockers": blockers,
			}
			out, _ := json.MarshalIndent(result, "", "  ")
			return textResult(string(out)), nil
		},
	}
}

// --- gate_check ---

func toolGateCheck(k *kb.KB) Tool {
	return Tool{
		Name:     "gate_check",
		ReadOnly: true,
		Description: "Lightweight local gate: runs validate + lint + commit_gate in one call. " +
			"Returns pass/fail with details. Use before fast-forwarding to main.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"required": ["changed_ids"],
			"properties": {
				"changed_ids": {
					"type": "array",
					"items": {"type": "string"},
					"description": "List of concept IDs that were modified"
				}
			}
		}`),
		Handler: func(args json.RawMessage) (ToolResult, error) {
			var params struct {
				ChangedIDs []string `json:"changed_ids"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return errorResult("invalid params: " + err.Error()), nil
			}
			if len(params.ChangedIDs) == 0 {
				return errorResult("'changed_ids' is required and must not be empty"), nil
			}

			ids := make([]okf.ConceptID, len(params.ChangedIDs))
			for i, s := range params.ChangedIDs {
				ids[i] = okf.ConceptID(s)
			}

			pass := true

			// 1. Validate
			valErrs, err := k.Validate("")
			if err != nil {
				return errorResult(fmt.Sprintf("gate_check: validate: %v", err)), nil
			}
			if len(valErrs) > 0 {
				pass = false
			}

			// 2. Lint
			lintFindings, err := lint.Run(k, "", false)
			if err != nil {
				return errorResult(fmt.Sprintf("gate_check: lint: %v", err)), nil
			}
			for _, f := range lintFindings {
				if f.Severity == lint.SevError {
					pass = false
					break
				}
			}

			// 3. Commit gate
			gate, err := k.CommitGate(ids)
			if err != nil {
				return errorResult(fmt.Sprintf("gate_check: commit_gate: %v", err)), nil
			}
			if !gate.Pass {
				pass = false
			}

			type valErrJSON struct {
				Path    string `json:"path"`
				Message string `json:"message"`
			}
			type lintJSON struct {
				Path     string `json:"path"`
				Check    string `json:"check"`
				Severity string `json:"severity"`
				Message  string `json:"message"`
			}
			type blockerJSON struct {
				Path     string   `json:"path"`
				Involves []string `json:"involves"`
				Kind     string   `json:"kind"`
				Reason   string   `json:"reason"`
			}

			var valErrsJSON []valErrJSON
			for _, e := range valErrs {
				valErrsJSON = append(valErrsJSON, valErrJSON{Path: e.Path, Message: e.Message})
			}
			var lintJSON2 []lintJSON
			for _, f := range lintFindings {
				lintJSON2 = append(lintJSON2, lintJSON{
					Path: f.Path, Check: f.Check, Severity: f.Severity, Message: f.Message,
				})
			}
			var blockers []blockerJSON
			for _, b := range gate.Blockers {
				blockers = append(blockers, blockerJSON{
					Path: b.ConceptPath, Involves: b.Involves, Kind: b.Kind, Reason: b.Reason,
				})
			}

			result := map[string]interface{}{
				"pass":              pass,
				"validation_errors": valErrsJSON,
				"lint_findings":     lintJSON2,
				"gate_blockers":     blockers,
			}
			out, _ := json.MarshalIndent(result, "", "  ")
			return textResult(string(out)), nil
		},
	}
}

// --- conflict_resolve ---

func toolConflictResolve(k *kb.KB) Tool {
	return Tool{
		Name:        "conflict_resolve",
		Description: "Resolves an open contradiction (type:Contradiction, resolution_status:open). Sets resolution_status=resolved and records the resolution.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"required": ["contradiction_id", "resolution"],
			"properties": {
				"contradiction_id": {
					"type": "string",
					"description": "Concept ID of the Contradiction concept"
				},
				"resolution": {
					"type": "string",
					"description": "The resolution decision text"
				},
				"reason": {
					"type": "string",
					"description": "Optional rationale for the resolution"
				}
			}
		}`),
		Handler: func(args json.RawMessage) (ToolResult, error) {
			var params struct {
				ContradictionID string `json:"contradiction_id"`
				Resolution      string `json:"resolution"`
				Reason          string `json:"reason"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return errorResult("invalid params: " + err.Error()), nil
			}
			if params.ContradictionID == "" {
				return errorResult("'contradiction_id' is required"), nil
			}
			if params.Resolution == "" {
				return errorResult("'resolution' is required"), nil
			}

			data, err := k.ReadConcept(okf.ConceptID(params.ContradictionID))
			if err != nil {
				return errorResult(fmt.Sprintf("conflict_resolve: read %q: %v", params.ContradictionID, err)), nil
			}

			fm, err := okf.ParseFrontmatter(data.FrontmatterRaw)
			if err != nil {
				return errorResult(fmt.Sprintf("conflict_resolve: parse frontmatter: %v", err)), nil
			}

			if fm.Type() != "Contradiction" {
				return errorResult(fmt.Sprintf("conflict_resolve: %q has type %q, expected Contradiction", params.ContradictionID, fm.Type())), nil
			}

			fm.Set("resolution_status", "resolved")
			fm.Set("resolution", params.Resolution)
			fm.Set("resolution_date", time.Now().UTC().Format("2006-01-02"))
			if params.Reason != "" {
				fm.Set("resolution_reason", params.Reason)
			}

			if _, err := k.WriteConcept(okf.ConceptID(params.ContradictionID), fm, data.Body, data.ContentHash); err != nil {
				return errorResult(fmt.Sprintf("conflict_resolve: write: %v", err)), nil
			}

			_ = k.AppendLog("conflict_resolve: "+params.ContradictionID, time.Now())
			return textResult("resolved contradiction " + params.ContradictionID), nil
		},
	}
}

// --- kb_status ---

func toolKBStatus(k *kb.KB) Tool {
	return Tool{
		Name:        "kb_status",
		Description: "Returns aggregate metrics about the KB: total concepts, per-type counts, stale concepts (review_after in the past), open contradictions.",
		ReadOnly:    true,
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
		Handler: func(args json.RawMessage) (ToolResult, error) {
			today := time.Now().UTC().Format("2006-01-02")
			typeCounts := map[string]int{}
			total := 0
			staleCount := 0
			openContradictions := 0

			err := k.WalkConcepts(func(id okf.ConceptID, content string) error {
				total++
				fmRaw, _, _ := okf.SplitFrontmatter(content)
				fm, err := okf.ParseFrontmatter(fmRaw)
				if err != nil {
					return nil
				}

				t := fm.Type()
				typeCounts[t]++

				if ra, ok := fm.Get("review_after"); ok {
					if raStr, ok := ra.(string); ok && raStr != "" && raStr < today {
						staleCount++
					}
				}

				if t == "Contradiction" {
					rs, ok := fm.Get("resolution_status")
					if !ok {
						openContradictions++
					} else if rsStr, ok := rs.(string); ok && (rsStr == "open" || rsStr == "") {
						openContradictions++
					}
				}
				return nil
			})
			if err != nil {
				return errorResult(fmt.Sprintf("kb_status: walk: %v", err)), nil
			}

			result := map[string]interface{}{
				"total":               total,
				"by_type":             typeCounts,
				"stale_count":         staleCount,
				"open_contradictions": openContradictions,
			}
			out, _ := json.MarshalIndent(result, "", "  ")
			return textResult(string(out)), nil
		},
	}
}

// --- contradiction_report ---

func toolContradictionReport(k *kb.KB) Tool {
	return Tool{
		Name:        "contradiction_report",
		Description: "Lists contradiction concepts, optionally filtered by scope prefix and resolution status (default: open).",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"scope": {
					"type": "string",
					"description": "Concept ID prefix filter (e.g. 'arch/'). Empty = all."
				},
				"status": {
					"type": "string",
					"description": "Filter by resolution_status (default 'open'). Use '*' for all."
				}
			}
		}`),
		Handler: func(args json.RawMessage) (ToolResult, error) {
			var params struct {
				Scope  string `json:"scope"`
				Status string `json:"status"`
			}
			json.Unmarshal(args, &params)

			statusFilter := params.Status
			if statusFilter == "" {
				statusFilter = "open"
			}

			type entry struct {
				ID                string   `json:"id"`
				Title             string   `json:"title,omitempty"`
				Involves          []string `json:"involves,omitempty"`
				ContradictionKind string   `json:"contradiction_kind,omitempty"`
				ResolutionStatus  string   `json:"resolution_status"`
			}

			var matches []entry

			err := k.WalkConcepts(func(id okf.ConceptID, content string) error {
				fmRaw, _, _ := okf.SplitFrontmatter(content)
				fm, err := okf.ParseFrontmatter(fmRaw)
				if err != nil {
					return nil
				}
				if fm.Type() != "Contradiction" {
					return nil
				}

				sid := string(id)
				if params.Scope != "" && !strings.HasPrefix(sid, params.Scope) {
					return nil
				}

				rs := "open"
				if v, ok := fm.Get("resolution_status"); ok {
					if s, ok := v.(string); ok && s != "" {
						rs = s
					}
				}

				if statusFilter != "*" && rs != statusFilter {
					return nil
				}

				e := entry{
					ID:               sid,
					ResolutionStatus: rs,
				}
				if v, ok := fm.Get("title"); ok {
					e.Title, _ = v.(string)
				}
				if v, ok := fm.Get("involves"); ok {
					e.Involves, _ = v.([]string)
				}
				if v, ok := fm.Get("contradiction_kind"); ok {
					e.ContradictionKind, _ = v.(string)
				}
				matches = append(matches, e)
				return nil
			})
			if err != nil {
				return errorResult(fmt.Sprintf("contradiction_report: walk: %v", err)), nil
			}

			if len(matches) == 0 {
				return textResult("No contradictions found."), nil
			}

			var sb strings.Builder
			for _, e := range matches {
				sb.WriteString(fmt.Sprintf("- %s [%s]", e.ID, e.ResolutionStatus))
				if e.Title != "" {
					sb.WriteString(": " + e.Title)
				}
				if e.ContradictionKind != "" {
					sb.WriteString(" (" + e.ContradictionKind + ")")
				}
				if len(e.Involves) > 0 {
					sb.WriteString(" involves: " + strings.Join(e.Involves, ", "))
				}
				sb.WriteByte('\n')
			}
			return textResult(strings.TrimRight(sb.String(), "\n")), nil
		},
	}
}

// --- conflicts_list ---

// toolConflictsList is read-only: it exposes the KB conflict registry so the
// agent can see which concepts are degraded and need manual reconciliation.
// NOT wrapped with gitWrap (no write, no sync).
func toolConflictsList(k *kb.KB) Tool {
	return Tool{
		Name:     "conflicts_list",
		ReadOnly: true,
		Description: "Lists open git rebase conflicts registered on this KB. " +
			"Each entry includes the affected concept, the local and remote SHAs, " +
			"the branch, and the list of conflicting files. Use the kb-conflict-resolve skill " +
			"for the step-by-step resolution procedure.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
		Handler: func(args json.RawMessage) (ToolResult, error) {
			conflicts, err := k.ListConflicts()
			if err != nil {
				return errorResult(fmt.Sprintf("conflicts_list: %v", err)), nil
			}
			if len(conflicts) == 0 {
				return textResult("No open conflicts."), nil
			}

			type conflictJSON struct {
				ConceptID  string   `json:"concept_id"`
				Path       string   `json:"path"`
				LocalSHA   string   `json:"local_sha"`
				RemoteSHA  string   `json:"remote_sha"`
				Branch     string   `json:"branch"`
				Files      []string `json:"files"`
				DetectedAt string   `json:"detected_at"`
				Guidance   string   `json:"guidance"`
			}
			results := make([]conflictJSON, len(conflicts))
			for i, c := range conflicts {
				results[i] = conflictJSON{
					ConceptID:  c.ConceptID,
					Path:       c.Path,
					LocalSHA:   c.LocalSHA,
					RemoteSHA:  c.RemoteSHA,
					Branch:     c.Branch,
					Files:      c.Files,
					DetectedAt: c.DetectedAt,
					Guidance: "Read the concept with concept_read, reconcile the content, " +
						"then rewrite it with concept_write removing status:degraded. " +
						"See the kb-conflict-resolve skill for the full procedure.",
				}
			}
			out, _ := json.MarshalIndent(results, "", "  ")
			return textResult(string(out)), nil
		},
	}
}

// --- git_conflict_resolve (Step 4) ---

// toolGitConflictResolve closes the conflict-resolution loop: the agent picks a
// strategy per concept ("ours" = local, "theirs" = remote, "edit" = supplied body).
// The decision is recorded in the registry; once every open conflict has a recorded
// resolution, FinalizeConflicts performs a single git merge, commits, pushes, and
// clears the degraded markers. NOT wrapped with gitWrap: it manages its own git lock
// and must not trigger the SyncIn/SyncOut wrapper (which would re-hit the conflict).
func toolGitConflictResolve(k *kb.KB) Tool {
	return Tool{
		Name: "git_conflict_resolve",
		Description: "Resolves a registered git conflict on a concept. strategy: " +
			"'ours' keeps the local version, 'theirs' takes the remote version, 'edit' uses the " +
			"full reconciled file content passed in 'body'. The decision is recorded; once every " +
			"open conflict (see conflicts_list) has a recorded resolution, Cartographer performs a " +
			"single merge, commits, pushes, and removes the degraded markers.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"required": ["concept_id", "strategy"],
			"properties": {
				"concept_id": {"type": "string", "description": "The conflicting concept ID (from conflicts_list)."},
				"strategy": {"type": "string", "enum": ["ours", "theirs", "edit"], "description": "ours=local, theirs=remote, edit=use body."},
				"body": {"type": "string", "description": "Full reconciled file content (frontmatter + body); required when strategy=edit."}
			}
		}`),
		Handler: func(args json.RawMessage) (ToolResult, error) {
			// D76/WP4: flush any pending async push before touching the
			// conflict registry/git state directly, so this handler does
			// not race a push scheduled by a preceding write.
			flushPendingPush(k, "git_conflict_resolve")

			var p struct {
				ConceptID string `json:"concept_id"`
				Strategy  string `json:"strategy"`
				Body      string `json:"body"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return errorResult("invalid params: " + err.Error()), nil
			}
			if p.ConceptID == "" || p.Strategy == "" {
				return errorResult("'concept_id' and 'strategy' are required"), nil
			}
			switch p.Strategy {
			case "ours", "theirs":
			case "edit":
				if strings.TrimSpace(p.Body) == "" {
					return errorResult("strategy 'edit' requires a non-empty 'body'"), nil
				}
			default:
				return errorResult("unknown strategy: " + p.Strategy + " (use ours|theirs|edit)"), nil
			}

			var result ToolResult
			_ = k.WithGitLock(func() error {
				if err := k.RecordResolution(p.ConceptID, p.Strategy, p.Body); err != nil {
					result = errorResult("git_conflict_resolve: " + err.Error())
					return nil
				}
				pending, err := k.PendingConflictCount()
				if err != nil {
					result = errorResult("git_conflict_resolve: " + err.Error())
					return nil
				}
				if pending > 0 {
					result = textResult(fmt.Sprintf(
						"Resolution recorded for %q (strategy=%s). %d conflict(s) still pending; "+
							"resolve them to finalize.", p.ConceptID, p.Strategy, pending))
					return nil
				}
				ids, ferr := k.FinalizeConflicts()
				if ferr != nil {
					result = errorResult("git_conflict_resolve: finalize: " + ferr.Error())
					return nil
				}
				_ = k.AppendLog("git_conflict_resolve: "+strings.Join(ids, ", "), time.Now())
				result = textResult(fmt.Sprintf(
					"Resolved and merged %d concept(s): %s. Registry cleared, degraded markers removed.",
					len(ids), strings.Join(ids, ", ")))
				return nil
			})
			return result, nil
		},
	}
}
