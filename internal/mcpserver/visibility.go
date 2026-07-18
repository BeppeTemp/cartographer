package mcpserver

// advancedToolNames is the source-of-truth list of tool names hidden from
// tools/list under the default "agent" tools profile (D65). They stay fully
// registered and callable via tools/call — the CLI client (sync_pull, D57) and
// the SessionStart hook (sync_check, D48) invoke them by name without going
// through tools/list — but they are not advertised to the LLM agent, whose
// working set stays small (search/read/write/log plus content structure and
// git-conflict self-recovery).
//
// Classification rationale:
//   - governance/diagnostics (validate, lint, gate_check, commit_gate,
//     kb_status, contradiction_report, conflict_resolve, index_rebuild):
//     operator-level maintenance, not part of a normal agent session;
//   - provisioning plumbing (sync_*, skill_*, service_*): consumed by the
//     client CLI / hooks, or operator-level (skill_install, service_get).
//
// NOT advanced (agent-visible) despite being niche: conflicts_list and
// git_conflict_resolve — an agent whose write fails on a degraded concept must
// self-recover (kb-conflict-resolve skill, concurrency.md Step 4). Also NOT
// advanced: artifact_read/artifact_write (D71) — the point of the pair is
// self-maintenance of the agent's own skills/agents, so it must be part of
// the default working set, unlike the enumeration/removal tools below. Also
// NOT advanced: concept_expand (D77 WP2) — growing a concept into an
// expanded concept is a normal, frequent agent action, same tier as
// concept_move/concept_delete.
//
// TestToolsProfile (server_test.go) is the golden test: it builds a real
// registry and asserts the exact agent-visible set, so adding a tool without
// classifying it here fails the build.
var advancedToolNames = map[string]bool{
	"validate":             true,
	"lint":                 true,
	"gate_check":           true,
	"commit_gate":          true,
	"kb_status":            true,
	"contradiction_report": true,
	"conflict_resolve":     true,
	"index_rebuild":        true,
	"sync_check":           true,
	"sync_apply":           true,
	"sync_pull":            true,
	"skill_list":           true,
	"skill_install":        true,
	"service_get":          true,
	"service_list":         true,
	"artifact_list":        true,
	"artifact_delete":      true,
}

// ToolAdvanced reports whether the named tool is hidden from tools/list under
// the "agent" tools profile. Unknown names are not advanced (fail-visible):
// a brand-new tool shows up in tools/list and the golden test forces the
// author to classify it.
func ToolAdvanced(name string) bool {
	return advancedToolNames[name]
}
