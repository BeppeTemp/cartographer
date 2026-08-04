package mcpserver

// advancedToolNames is the source-of-truth list of tool names hidden from
// tools/list under the default "agent" tools profile (D65). They stay fully
// registered and callable via tools/call — the CLI client (sync_pull, D57) and
// the SessionStart hook (sync_check, D48) invoke them by name without going
// through tools/list — but they are not advertised to the LLM agent, whose
// working set stays small (search/read/write/log plus content structure,
// git-conflict self-recovery, and the read-only governance loop below).
//
// Classification rationale:
//   - operator-level mutation/maintenance (commit_gate, contradiction_report,
//     conflict_resolve, index_rebuild, reindex): not part of a normal agent
//     session;
//   - provisioning plumbing (sync_*, skill_*, service_*): consumed by the
//     client CLI / hooks, or operator-level (skill_install, service_get).
//
// Also advanced: concept_batch (D125) — a bounded atomic multi-concept
// write, deliberately kept operator/large-refactor tooling rather than part
// of the default agent working set: normal agent sessions reach for
// concept_write/concept_patch (single concept) or concept_move (renames),
// and the default profile stays small.
//
// NOT advanced (agent-visible) despite being niche: conflicts_list and
// git_conflict_resolve — an agent whose write fails on a degraded concept must
// self-recover (kb-conflict-resolve skill, concurrency.md Step 4). Also NOT
// advanced: artifact_read/artifact_write (D71) — the point of the pair is
// self-maintenance of the agent's own skills/agents, so it must be part of
// the default working set, unlike the enumeration/removal tools below. Also
// NOT advanced: concept_expand (D77 WP2) — growing a concept into an
// expanded concept is a normal, frequent agent action, same tier as
// concept_move/concept_delete. Also NOT advanced: map_delete (D88 WP2) —
// same tier as its counterpart map_create, normal Atlas upkeep. Also NOT
// advanced: validate, lint, gate_check, kb_status (D123) — read-only
// governance diagnostics that the documented agent loop (docs/loop.md,
// docs/use-cases.md) requires, and that descriptor-bound MCP hosts (e.g.
// Codex) can only ever invoke if tools/list advertises them: unlike a
// stdio/TUI agent, they cannot call a tool by name that tools/list omits.
//
// TestServer_ToolsProfile (server_test.go) is the golden test: it builds a
// real registry and asserts the exact agent-visible set, so adding a tool
// without classifying it here fails the build.
var advancedToolNames = map[string]bool{
	"concept_batch":        true,
	"commit_gate":          true,
	"contradiction_report": true,
	"conflict_resolve":     true,
	"index_rebuild":        true,
	"reindex":              true,
	"sync_check":           true,
	"sync_apply":           true,
	"sync_pull":            true,
	"sync_status":          true,
	"skill_list":           true,
	"skill_install":        true,
	"service_get":          true,
	"service_list":         true,
	"secret_resolve":       true,
	"secret_set":           true,
	"artifact_list":        true,
	"artifact_delete":      true,
	"asset_delete":         true,
	"pr_status":            true,
	"pr_finalize":          true,
}

// ToolAdvanced reports whether the named tool is hidden from tools/list under
// the "agent" tools profile. Unknown names are not advanced (fail-visible):
// a brand-new tool shows up in tools/list and the golden test forces the
// author to classify it.
func ToolAdvanced(name string) bool {
	return advancedToolNames[name]
}
