package mcpserver

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/lint"
	"github.com/BeppeTemp/cartographer/internal/okf"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// toolReindex reconciles the derived search indexes with out-of-band KB
// changes, or rebuilds them from scratch with full=true (D136: the former
// index_rebuild tool). It is deliberately a write-scoped administrative
// action: although it never changes KB content, it writes the server-owned
// SQLite database — a read-only client has no business rewriting it.
func toolReindex(k *kb.KB, rec *searchReconciler, deps Deps) Tool {
	return Tool{
		Name: "reindex",
		Description: "Reconciles the derived search index with KB files changed outside MCP, including imports, manual edits, and git pulls. Returns indexed, updated, and removed counts. " +
			"Search already reconciles before every query, so this is an explicit check. Set full=true to rebuild the whole index from every concept instead.",
		InputSchema: json.RawMessage(`{
	"type": "object",
	"properties": {
		"full": {
			"type": "boolean",
			"description": "Rebuild the whole index from every concept instead of reconciling only what changed (default false)"
		}
	}
}`),
		Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
			var params struct {
				Full bool `json:"full"`
			}
			if len(args) > 0 {
				if err := json.Unmarshal(args, &params); err != nil {
					return errorResult("invalid params: " + err.Error()), nil
				}
			}

			if params.Full {
				indexed, sqlUpserted, err := rec.rebuild()
				if err != nil {
					return errorResult("reindex: " + err.Error()), nil
				}
				result := map[string]interface{}{
					"status":           "rebuilt",
					"concepts_indexed": indexed,
				}
				if deps.SQLIndex != nil {
					result["sql_upserted"] = sqlUpserted
				}
				out, _ := json.MarshalIndent(result, "", "  ")
				return textResult(string(out)), nil
			}

			// Incremental: the same reconciliation every search runs (D245),
			// with or without SQLite.
			stats, err := rec.reconcile()
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
		Name:        "validate",
		ReadOnly:    true,
		Description: "Validates frontmatter, reserved files and strict ontology; returns the errors.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"scope": {
					"type": "string"
				}
			}
		}`),
		Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
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
		Name:        "lint",
		ReadOnly:    true,
		Description: "Deterministic checks: broken links, stale claims (review_after past), orphans. Returns findings with severity. severity_min sets the floor (default info). counts_by_check and counts_by_severity are computed before filtering; count is the unfiltered total.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"scope": {
					"type": "string"
				},
				"scope_neighbors": {
					"type": "boolean",
					"description": "Also lint 1-hop neighbors"
				},
				"severity_min": {
					"type": "string",
					"enum": ["info", "warning", "error"],
					"description": "info (default), warning or error"
				}
			}
		}`),
		Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
			var params struct {
				Scope          string `json:"scope"`
				ScopeNeighbors bool   `json:"scope_neighbors"`
				SeverityMin    string `json:"severity_min"`
			}
			json.Unmarshal(args, &params)

			if params.SeverityMin == "" {
				params.SeverityMin = lint.SevInfo
			}
			if !lint.ValidSeverity(params.SeverityMin) {
				return errorResult(fmt.Sprintf("lint: 'severity_min' must be one of %s, got %q",
					strings.Join(lint.Severities, ", "), params.SeverityMin)), nil
			}

			findings, err := lint.Run(k, params.Scope, params.ScopeNeighbors)
			if err != nil {
				return errorResult(fmt.Sprintf("lint: %v", err)), nil
			}

			// No early return for a clean KB: the empty result keeps the shape
			// of a non-empty one, so a caller that parses JSON never meets a
			// bare string (D318).
			total := len(findings)
			findings, countsByCheck, countsBySeverity := lint.Filter(findings, params.SeverityMin)

			results := findingsOut(findings)

			// 'count' keeps meaning the unfiltered total, which is what it has
			// always meant; findings_omitted says how much of it is not below.
			result := map[string]interface{}{
				"count":              total,
				"findings":           results,
				"findings_omitted":   total - len(results),
				"counts_by_check":    countsByCheck,
				"counts_by_severity": countsBySeverity,
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
		Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
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
		Name:        "gate_check",
		ReadOnly:    true,
		Description: "Local gate: validate + lint + commit_gate. Use at session end over the IDs you wrote, or before fast-forwarding to main. changed_ids alone lints just those (no whole-KB structure checks); empty changed_ids: whole KB, no commit gate; scope: a path prefix. severity_min sets the lint floor (default warning); pass ignores it.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"required": ["changed_ids"],
			"properties": {
				"changed_ids": {
					"type": "array",
					"items": {"type": "string"}
				},
				"severity_min": {
					"type": "string",
					"enum": ["info", "warning", "error"],
					"description": "info, warning (default) or error"
				},
				"scope": {
					"type": "string"
				},
				"scope_neighbors": {
					"type": "boolean",
					"description": "Also lint 1-hop neighbors"
				}
			}
		}`),
		Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
			var params struct {
				ChangedIDs     []string `json:"changed_ids"`
				SeverityMin    string   `json:"severity_min"`
				Scope          string   `json:"scope"`
				ScopeNeighbors bool     `json:"scope_neighbors"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return errorResult("invalid params: " + err.Error()), nil
			}
			if params.SeverityMin == "" {
				params.SeverityMin = lint.SevWarning
			}
			if !lint.ValidSeverity(params.SeverityMin) {
				return errorResult(fmt.Sprintf("gate_check: 'severity_min' must be one of %s, got %q",
					strings.Join(lint.Severities, ", "), params.SeverityMin)), nil
			}

			ids := make([]okf.ConceptID, len(params.ChangedIDs))
			for i, s := range params.ChangedIDs {
				ids[i] = okf.ConceptID(s)
			}

			pass := true

			// changed_ids with no scope is the cheap session-end gate (D312):
			// validate, the frontmatter checks and the write-time structural
			// checks over exactly the concepts the session touched, not a
			// whole-KB lint. With a scope the gate is the scope's lint as
			// before, and with no changed_ids it is the whole-KB gate (D318).
			scopedToIDs := len(ids) > 0 && params.Scope == ""
			lintScope := "kb"
			switch {
			case scopedToIDs:
				lintScope = "changed_ids"
			case params.Scope != "":
				lintScope = "scope"
			}

			// 1. Validate
			var valErrs []kb.ValidationError
			if scopedToIDs {
				for _, id := range ids {
					rel, _ := k.ConceptRelPath(id)
					errs, err := k.Validate(rel)
					if err != nil {
						// A changed id that no longer exists has no file to
						// validate: lint's gone-id check covers it.
						continue
					}
					valErrs = append(valErrs, errs...)
				}
			} else {
				var err error
				valErrs, err = k.Validate(params.Scope)
				if err != nil {
					return errorResult(fmt.Sprintf("gate_check: validate: %v", err)), nil
				}
			}
			if len(valErrs) > 0 {
				pass = false
			}

			// 2. Lint
			var lintFindings []lint.Finding
			if scopedToIDs {
				lintFindings = writeLintFindings(k, params.ChangedIDs, nil)
			} else {
				var err error
				lintFindings, err = lint.Run(k, params.Scope, params.ScopeNeighbors)
				if err != nil {
					return errorResult(fmt.Sprintf("gate_check: lint: %v", err)), nil
				}
			}
			// pass is decided on the unfiltered findings, before severity_min is
			// applied below: a response budget must never be able to change a
			// verdict.
			for _, f := range lintFindings {
				if f.Severity == lint.SevError {
					pass = false
					break
				}
			}
			lintTotal := len(lintFindings)
			lintFindings, countsByCheck, countsBySeverity := lint.Filter(lintFindings, params.SeverityMin)

			// 3. Commit gate. It checks the contradictions that involve the
			// changed concepts, so with none there is nothing to check: an empty
			// changed_ids is the whole-KB session-end gate (D318).
			gate := &kb.GateResult{Pass: true}
			if len(ids) > 0 {
				var err error
				gate, err = k.CommitGate(ids)
				if err != nil {
					return errorResult(fmt.Sprintf("gate_check: commit_gate: %v", err)), nil
				}
				if !gate.Pass {
					pass = false
				}
			}

			type valErrJSON struct {
				Path    string `json:"path"`
				Message string `json:"message"`
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
			lintJSON2 := findingsOut(lintFindings)
			var blockers []blockerJSON
			for _, b := range gate.Blockers {
				blockers = append(blockers, blockerJSON{
					Path: b.ConceptPath, Involves: b.Involves, Kind: b.Kind, Reason: b.Reason,
				})
			}

			// validation_errors and gate_blockers are never filtered: they are
			// already error-level and small.
			result := map[string]interface{}{
				"pass":               pass,
				"lint_scope":         lintScope,
				"validation_errors":  valErrsJSON,
				"lint_findings":      lintJSON2,
				"gate_blockers":      blockers,
				"lint_count":         lintTotal,
				"findings_omitted":   lintTotal - len(lintJSON2),
				"counts_by_check":    countsByCheck,
				"counts_by_severity": countsBySeverity,
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
		Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
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

// KBCapabilitiesFor exposes the capability map in the /health shape, so the MCP
// tool and the HTTP endpoint answer the identical question from one derivation
// (D151). kbCapabilities already builds KBCapability values, so this is a plain
// alias rather than a re-copy: the two used to be distinct structs with
// identical fields, and the copy loop between them could only ever drift.
func KBCapabilitiesFor(k *kb.KB) map[string]KBCapability {
	return kbCapabilities(k)
}

// kbCapabilities reports what this KB is allowed to do, from in-process state
// only — kb_status promises never to hit the network and that must keep holding
// (D151). No path and no secret material appears: the SOPS key is reported as
// configured or not, plus the name of the key, so the host's layout does not leak
// into an agent transcript.
func kbCapabilities(k *kb.KB) map[string]KBCapability {
	onOff := func(enabled bool) string {
		if enabled {
			return "enabled"
		}
		return "disabled"
	}
	prefix := k.ToolPrefix
	if prefix == "" {
		prefix = "none"
	}
	mount := "configured"
	if k.Discovered {
		mount = "discovered"
	}
	interval := "disabled"
	if k.DoctorIntervalDays > 0 {
		interval = fmt.Sprintf("%d days", k.DoctorIntervalDays)
	}
	workflow := "local"
	if k.ServerGit != nil {
		workflow = "pr"
	}
	return map[string]KBCapability{
		"artifact_write": {State: onOff(k.AllowArtifactWrite), Setting: "kbs[].allow_artifact_write"},
		"secrets":        {State: onOff(k.SopsAgeKeyFile != ""), Setting: "kbs[].sops_age_key_file or sops.age_key_file"},
		"git_sync":       {State: onOff(k.GitSync), Setting: "git.sync"},
		"git_workflow":   {State: workflow, Setting: "kbs[].server_git"},
		"tool_prefix":    {State: prefix, Setting: "kbs[].tool_prefix or mcp.tool_prefix_mode"},
		// A discovered KB cannot carry any of the settings above, which is the
		// single invisible cause behind several of them being off at once.
		"mount": {State: mount, Setting: "kbs[]"},
		// D299: what `cartographer kb repair --apply` may apply unattended,
		// and when the server proposes the next kb-doctor session.
		"auto_repair":     {State: onOff(len(k.AutoRepair) > 0), Setting: "kbs[].auto_repair", Checks: k.AutoRepair},
		"doctor_interval": {State: interval, Setting: "kbs[].doctor_interval"},
	}
}

func toolKBStatus(k *kb.KB, misses *searchMissLog, serverVersion string, latestVersion func() string, cc *conformanceCache) Tool {
	return Tool{
		Name:        "kb_status",
		Description: "KB health in one call: concept counts (by type, status), stale concepts, open contradictions and gaps, git replication state, capabilities (each per-KB gate, its state and controlling key), source-ledger counts, frequent search misses, versions, and conformance: {findings by severity, fixable, last_doctor, doctor_suggested}, the drift from the current standard. When doctor_suggested is true, follow the kb-doctor skill. usage: which skills and agents the clients actually load (never used, stale, active), from their reports. Read-only.",
		ReadOnly:    true,
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
		Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
			today := time.Now().UTC().Format("2006-01-02")
			typeCounts := map[string]int{}
			statusCounts := map[string]int{}
			total := 0
			staleCount := 0
			openContradictions := 0
			type gapEntry struct {
				ID       string   `json:"id"`
				Title    string   `json:"title,omitempty"`
				Kind     string   `json:"kind"`
				Involves []string `json:"involves,omitempty"`
				ts       string
			}
			var gaps []gapEntry
			gapsByKind := map[string]int{}

			err := k.WalkConcepts(func(id okf.ConceptID, content string) error {
				total++
				fmRaw, _, _ := okf.SplitFrontmatter(content)
				fm, err := okf.ParseFrontmatter(fmRaw)
				if err != nil {
					return nil
				}

				t := fm.Type()
				typeCounts[t]++

				if s, ok := fm.Get("status"); ok {
					if sStr, ok := s.(string); ok && sStr != "" {
						statusCounts[sStr]++
					}
				}

				if ra, ok := fm.Get("review_after"); ok {
					if raStr, ok := ra.(string); ok && raStr != "" && raStr < today {
						staleCount++
					}
				}

				if t == "Contradiction" {
					isOpen := false
					rs, ok := fm.Get("resolution_status")
					if !ok {
						isOpen = true
					} else if rsStr, ok := rs.(string); ok && (rsStr == "open" || rsStr == "") {
						isOpen = true
					}
					if isOpen {
						kind := ""
						if v, ok := fm.Get("contradiction_kind"); ok {
							kind, _ = v.(string)
						}
						if kb.IsGapKind(kind) {
							// D273: a gap is not a disagreement; counted apart.
							g := gapEntry{ID: string(id), Kind: kind}
							if v, ok := fm.Get("title"); ok {
								g.Title, _ = v.(string)
							}
							if v, ok := fm.Get("involves"); ok {
								g.Involves, _ = v.([]string)
							}
							if v, ok := fm.Get("timestamp"); ok {
								g.ts, _ = v.(string)
							}
							gaps = append(gaps, g)
							gapsByKind[kind]++
						} else {
							openContradictions++
						}
					}
				}
				return nil
			})
			if err != nil {
				return errorResult(fmt.Sprintf("kb_status: walk: %v", err)), nil
			}

			server := k.ServerGitStatus()
			// Replication facts (D145): kb_status is the agent-visible tool, so it
			// must answer "does this KB have a remote, and is it pushing" without
			// the agent reaching for the advanced sync_status. Local state plus one
			// git remote get-url — no fetch, no network.
			remoteURL, hasRemote := k.RemoteInfo()
			git := k.GitStatusSnapshot()
			result := map[string]interface{}{
				// The KB that served this call (D144): on a client with a flat MCP
				// tool namespace it is the only protocol-level way to tell which KB
				// answered. Same derivation the provisioning manifest uses.
				"kb":                  filepath.Base(k.Root),
				"total":               total,
				"by_type":             typeCounts,
				"by_status":           statusCounts,
				"stale_count":         staleCount,
				"open_contradictions": openContradictions,
				// git_workflow describes the write workflow (commit-and-push vs PR
				// boundary, D117), never remote presence. git_profile is its
				// deprecated alias, kept for one minor release.
				"git_workflow":     server.Profile,
				"git_profile":      server.Profile,
				"has_remote":       hasRemote,
				"remote_url":       remoteURL,
				"git_sync":         k.GitSync,
				"push_state":       git.State,
				"push_last_error":  git.LastError,
				"unpushed_commits": git.UnpushedCommits,
				"base_branch":      server.BaseBranch,
				"working_branch":   server.WorkingBranch,
				"pr_number":        server.PRNumber,
				"pr_url":           server.PRURL,
				"pr_head_sha":      server.PRHeadSHA,
				"last_forge_error": server.LastForgeError,
				// capabilities (D151): what this KB is allowed to do, and the
				// YAML key that controls each gate. kb_status is where an agent
				// already looks to learn the state of a KB, and it was
				// exhaustive about git and silent about permissions — so a
				// documented capability could stay unfound for a whole
				// migration. A state without the name of the switch is only half
				// an answer, hence the setting alongside it.
				"capabilities": kbCapabilities(k),
			}
			if len(gaps) > 0 {
				// Newest first by frontmatter timestamp, then ID descending.
				sort.Slice(gaps, func(i, j int) bool {
					if gaps[i].ts != gaps[j].ts {
						return gaps[i].ts > gaps[j].ts
					}
					return gaps[i].ID > gaps[j].ID
				})
				recent := gaps
				if len(recent) > 10 {
					recent = recent[:10]
				}
				result["open_gaps"] = map[string]interface{}{
					"total":   len(gaps),
					"by_kind": gapsByKind,
					"recent":  recent,
				}
			}
			// D298: the size of the doctor's work list this caller may see.
			reviewTotal := 0
			if items, rerr := visibleReview(ctx, k, cc); rerr == nil {
				result["review"] = reviewSummary(items)
				reviewTotal = len(items)
			}
			// D302: the open work this caller can see.
			if work, werr := visibleWork(ctx, k, cc); werr == nil {
				result["work"] = workSummary(work)
			}
			// D301: what reading this KB costs, over what this caller sees.
			if rc, rerr := cc.readCostFor(k, func(id string) bool { return Visible(ctx, k, id) }, WholeVisible(ctx, k, false)); rerr == nil {
				result["read_cost"] = rc
			}
			// D290: conformance debt, from the findings this caller may see.
			// D294: lint.Run is cached on the graph generation; the visibility
			// filter stays outside the cache so a restricted caller never sees
			// a hidden concept's findings from a warm cache.
			if allFindings, lerr := cc.lintFindings(k); lerr == nil {
				vis, _ := uiVisibleFindingsFrom(ctx, k, "", allFindings)
				result["conformance"] = summarizeConformance(vis, reviewTotal, cc.cachedDoctorDate(k), k.DoctorIntervalDays, time.Now().UTC())
				// D297: open questions the KB marks in its own words.
				concepts, markers := 0, 0
				for _, f := range vis {
					if f.Check == "open_marker" {
						concepts++
						markers += f.Count
					}
				}
				if concepts > 0 {
					result["open_markers"] = map[string]int{"concepts": concepts, "markers": markers}
				}
			}
			// D326: which skills and agents the clients actually load. Only
			// whole-KB callers see it: artifacts are whole-KB resources.
			if WholeVisible(ctx, k, false) {
				result["usage"] = usageSummary(k, time.Now())
			}
			if src := sourceCounts(k); src != nil {
				result["sources"] = src
			}
			if top := misses.top(); len(top) > 0 {
				result["search_misses"] = top
			}
			// D254: what the server knows about its own staleness, from the
			// serve-owned check — reading it is not a network call.
			result["server_version"] = serverVersion
			if latestVersion != nil {
				if latest := latestVersion(); latest != "" {
					result["latest_version"] = latest
				}
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
		Description: "Lists contradiction concepts, optionally filtered by scope prefix, resolution status (default: open) and kind. Knowledge gaps (contradiction_kind missing_context or open_question, D273) are listed here too; they never block commit_gate.",
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
				},
				"kind": {
					"type": "string",
					"description": "Filter by contradiction_kind: an exact kind, 'gap' (missing_context and open_question), 'contradiction' (every non-gap kind, including none). Empty = all."
				}
			}
		}`),
		Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
			var params struct {
				Scope  string `json:"scope"`
				Status string `json:"status"`
				Kind   string `json:"kind"`
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
				if !Visible(ctx, k, sid) {
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

				kindVal := ""
				if v, ok := fm.Get("contradiction_kind"); ok {
					kindVal, _ = v.(string)
				}
				switch params.Kind {
				case "":
				case "gap":
					if !kb.IsGapKind(kindVal) {
						return nil
					}
				case "contradiction":
					if kb.IsGapKind(kindVal) {
						return nil
					}
				default:
					if kindVal != params.Kind {
						return nil
					}
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
		Name:        "conflicts_list",
		ReadOnly:    true,
		Description: "Lists open git rebase conflicts: concept, local/remote SHAs, branch, files. Resolve with git_conflict_resolve (kb-conflict-resolve skill).",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
		Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
			conflicts, err := k.ListConflicts()
			if err != nil {
				return errorResult(fmt.Sprintf("conflicts_list: %v", err)), nil
			}
			if len(conflicts) == 0 {
				return textResult("No open conflicts."), nil
			}

			type conflictJSON struct {
				ConceptID     string   `json:"concept_id"`
				Path          string   `json:"path"`
				LocalSHA      string   `json:"local_sha"`
				RemoteSHA     string   `json:"remote_sha"`
				Branch        string   `json:"branch"`
				BaseBranch    string   `json:"base_branch,omitempty"`
				WorkingBranch string   `json:"working_branch,omitempty"`
				PRNumber      int      `json:"pr_number,omitempty"`
				PRURL         string   `json:"pr_url,omitempty"`
				Files         []string `json:"files"`
				DetectedAt    string   `json:"detected_at"`
				Kind          string   `json:"kind,omitempty"`
				Guidance      string   `json:"guidance"`
			}
			results := make([]conflictJSON, len(conflicts))
			for i, c := range conflicts {
				results[i] = conflictJSON{
					ConceptID:     c.ConceptID,
					Path:          c.Path,
					LocalSHA:      c.LocalSHA,
					RemoteSHA:     c.RemoteSHA,
					Branch:        c.Branch,
					BaseBranch:    c.BaseBranch,
					WorkingBranch: c.WorkingBranch,
					PRNumber:      c.PRNumber,
					PRURL:         c.PRURL,
					Files:         c.Files,
					DetectedAt:    c.DetectedAt,
					Kind:          c.Kind,
					Guidance: "Read the concept with concept_read, reconcile the content, " +
						"then rewrite it with concept_write removing status:degraded. " +
						"See the kb-conflict-resolve skill for the full procedure.",
				}
				if c.Kind == kb.ConflictKindReserved {
					results[i].Guidance = reservedConflictGuidance(c.Path)
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
		Name:        "git_conflict_resolve",
		Description: "Resolves a git conflict. strategy: ours (local), theirs (remote), edit (reconciled body), union (log.md only). Once every open conflict (conflicts_list) is resolved, Cartographer merges, commits, pushes and clears the degraded markers.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"required": ["concept_id", "strategy"],
			"properties": {
				"concept_id": {"type": "string"},
				"strategy": {"type": "string", "enum": ["ours", "theirs", "edit", "union"]},
				"body": {"type": "string", "description": "Full file content; for edit"}
			}
		}`),
		Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
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
			case "ours", "theirs", "union":
			case "edit":
				if strings.TrimSpace(p.Body) == "" {
					return errorResult("strategy 'edit' requires a non-empty 'body'"), nil
				}
			default:
				return errorResult("unknown strategy: " + p.Strategy + " (use ours|theirs|edit|union)"), nil
			}
			if msg := reservedStrategyError(k, p.ConceptID, p.Strategy); msg != "" {
				return errorResult(msg), nil
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

// reservedConflictGuidance is the conflicts_list guidance for a reserved file
// the server could not reconcile by itself (D311).
func reservedConflictGuidance(path string) string {
	strategies := "theirs (remote) or ours (local)"
	if filepath.Base(path) == "log.md" {
		strategies = "union (keeps both sides' entries), theirs (remote) or ours (local)"
	}
	return "Reserved file, not a concept: resolve it with git_conflict_resolve, concept_id " + path +
		", strategy " + strategies + "."
}

// reservedStrategyError rejects a strategy that does not fit the registered
// conflict: union is only for a reserved log.md, and a reserved file takes no
// edit (it is generated or append-only). Empty when the strategy fits, or when
// no conflict is registered (RecordResolution reports that).
func reservedStrategyError(k *kb.KB, conceptID, strategy string) string {
	conflicts, err := k.ListConflicts()
	if err != nil {
		return ""
	}
	for _, c := range conflicts {
		if c.ConceptID != conceptID {
			continue
		}
		reserved := c.Kind == kb.ConflictKindReserved
		switch {
		case strategy == "union" && (!reserved || filepath.Base(c.Path) != "log.md"):
			return "strategy 'union' applies only to a reserved log.md conflict (use ours|theirs|edit)"
		case strategy == "edit" && reserved:
			return "strategy 'edit' does not apply to a reserved file (use union|ours|theirs for log.md, ours|theirs otherwise)"
		}
		return ""
	}
	return ""
}

// usageSummary is kb_status's `usage` section (D326). scanner_enabled is true
// once any client has reported: the scan is client-side and the server cannot
// see whether one is switched off, only that nothing has arrived — in which
// case no_data is true and every count would be a guess, so none is given.
func usageSummary(k *kb.KB, now time.Time) map[string]interface{} {
	entries, err := k.LoadUsage()
	if err != nil || len(entries) == 0 {
		return map[string]interface{}{"scanner_enabled": false, "no_data": true}
	}
	supported, partial, unsupported := provisioning.UsageProviders()
	usage := kb.SummarizeUsage(entries)
	counts := map[string]int{}
	staleDays := k.UsageStaleDays
	if staleDays <= 0 { // threshold disabled: nothing is stale, only never or active
		staleDays = 1 << 30
	}
	for _, a := range lint.UsageArtifacts(k) {
		u, seen := usage[kb.UsageKey(a.Kind, a.Name)]
		counts[lint.UsageState(u, seen, staleDays, now)]++
	}
	return map[string]interface{}{
		"scanner_enabled":       true,
		"supported_providers":   supported,
		"partial_providers":     partial,
		"unsupported_providers": unsupported,
		// catalogue-only artifacts count as never used: a Codex load proves
		// availability, not use.
		"artifacts_never_used": counts[lint.UsageNever] + counts[lint.UsageCatalog],
		"artifacts_stale":      counts[lint.UsageStale],
		"artifacts_active":     counts[lint.UsageActive],
		"stale_threshold_days": k.UsageStaleDays,
	}
}
