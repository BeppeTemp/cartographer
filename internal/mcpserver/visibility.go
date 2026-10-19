package mcpserver

import (
	"fmt"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/config"
)

// legacyToolMessage answers a call to a pre-D288 prefixed tool name
// ("<prefix>__<tool>", D102) whose bare tool this server registers: a session
// opened before the topology change still holds the old schema, and a bare
// "tool not found" does not tell its agent what replaced the name (D320).
// The KB is named when the prefix is one a served KB's name derives
// (config.SanitizeToolPrefix, the D102 kb-name mode); an explicit
// kbs[].tool_prefix no longer exists in configuration, so it cannot be mapped
// back and the message names the argument instead. It is consulted only on
// the unknown-tool path, so a prefixed name is never registered and never
// appears in tools/list. Returns "" for any other unknown name.
func (s *Server) legacyToolMessage(name string) string {
	i := strings.LastIndex(name, "__")
	if i <= 0 || i+2 >= len(name) {
		return ""
	}
	prefix, base := name[:i], name[i+2:]
	s.mu.Lock()
	_, known := s.tools[base]
	kbs := append([]string(nil), s.connKBs...)
	if len(kbs) == 0 && s.policyKB != "" {
		kbs = []string{s.policyKB}
	}
	s.mu.Unlock()
	if !known {
		return ""
	}
	kbName := ""
	for _, k := range kbs {
		if config.SanitizeToolPrefix(k) == prefix {
			kbName = k
			break
		}
	}
	call := fmt.Sprintf("`%s`", base)
	if len(kbs) > 1 {
		// Only a connection serving several KBs has a `kb` argument (D288).
		if kbName != "" {
			call += fmt.Sprintf(" with kb: %q", kbName)
		} else {
			call += " with the `kb` argument naming the KB"
		}
	}
	return fmt.Sprintf("tool not found: %s — prefixed tool names were removed in the D288 topology change; call %s instead. Restart your agent session to load the new tool list; `cartographer doctor` flags the old names left in your own steering files.", name, call)
}

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
//     conflict_resolve, reindex): not part of a normal agent
//     session;
//   - provisioning plumbing (sync_*, skill_*, service_*): consumed by the
//     client CLI / hooks, or operator-level (skill_install, service_get).
//
// NOT advanced since D318: concept_batch (the bundled kb-doctor skill
// prescribes it, and an agent cannot call by name what tools/list omits),
// asset_delete and artifact_delete.
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
// same tier as its counterpart map_create, normal Atlas upkeep; so is
// map_update (#320), which an agent tidying a map's index needs. Also NOT
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
	// concept_merge/concept_collapse (D160): large-refactor tooling, same tier as
	// concept_batch — consolidating a dossier or undoing an expansion is not part
	// of a normal agent session, and the default working set stays small. Both
	// stay callable by name.
	"concept_merge":    true,
	"concept_collapse": true,
	// concept_archive (D344): retiring a page is rare, deliberate and whole-KB.
	"concept_archive": true,
	// concept_transfer (D760): moving pages between KBs is rare and whole-KB.
	"concept_transfer": true,
	// artifact_move (D782): the same rare, whole-KB, two-KB move for artifacts.
	"artifact_move":        true,
	"commit_gate":          true,
	"contradiction_report": true,
	"conflict_resolve":     true,
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
	// repair_revert (D323): undoing an automatic repair is an operator
	// decision, taken from the Atlas's revert command or the CLI, not a step
	// of the normal agent loop; it stays callable by name.
	"repair_revert": true,
	// graph_path (D242): "how is X connected to Y" is an investigation, not
	// a step of the normal loop; graph_context and link_suggest are.
	"graph_path": true,
}

// ToolAdvanced reports whether the named tool is hidden from tools/list under
// the "agent" tools profile. Unknown names are not advanced (fail-visible):
// a brand-new tool shows up in tools/list and the golden test forces the
// author to classify it.
func ToolAdvanced(name string) bool {
	return advancedToolNames[name]
}
