package mcpserver

// readOnlyToolNames is the source-of-truth list of tool names that never mutate
// KB content and are therefore safe to call with a read-only ("kb:<name>:r")
// scope token. It must be kept in sync with the ReadOnly field set on each
// tool's Tool{} literal in tools_*.go — TestReadOnlyToolsGolden (server_test.go)
// builds a real registry via RegisterKBTools and fails if the two diverge.
//
// index_rebuild is read-only: it only regenerates the derived, gitignored
// search index (in-memory and/or SQLite) from the concepts already on disk;
// it never writes KB content.
var readOnlyToolNames = map[string]bool{
	"atlas_overview":  true,
	"index_get":       true,
	"concept_read":    true,
	"log_tail":        true,
	"changes_since":   true,
	"validate":        true,
	"map_list":        true,
	"concept_list":    true,
	"graph_neighbors": true,
	"search":          true,
	"lint":            true,
	"gate_check":      true,
	"kb_status":       true,
	"conflicts_list":  true,
	"service_get":     true,
	"service_list":    true,
	"skill_list":      true,
	"sync_check":      true,
	"sync_pull":       true,
	"index_rebuild":   true,
	"artifact_read":   true,
	"artifact_list":   true,
}

// ToolRequiresWrite reports whether calling the named tool requires write
// access to the KB. Fail-closed: unknown tool names require write.
func ToolRequiresWrite(name string) bool {
	return !readOnlyToolNames[name]
}
