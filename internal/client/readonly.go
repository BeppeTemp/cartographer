package client

// ReadOnlyToolNames is the source-of-truth list of tool names that never mutate
// KB content and are therefore safe to call with a read-only ("kb:<name>:r")
// scope token. It lives here, not in mcpserver, so the local bridge can ask
// whether a call writes without linking the server.
// mcpserver.ToolRequiresWrite answers from it; TestReadOnlyToolsGolden
// (httpserver_auth_test.go) builds a real registry via RegisterKBTools and
// fails if the list and the ReadOnly field of the tools diverge.
var ReadOnlyToolNames = map[string]bool{
	"atlas_overview":  true,
	"index_get":       true,
	"concept_read":    true,
	"concept_history": true,
	"log_tail":        true,
	"changes_since":   true,
	"validate":        true,
	"map_list":        true,
	"concept_list":    true,
	"graph_neighbors": true,
	"graph_context":   true,
	"link_suggest":    true,
	"graph_path":      true,
	"search":          true,
	"lint":            true,
	"gate_check":      true,
	"kb_status":       true,
	"kb_review":       true,
	"work_list":       true,
	"kb_guide":        true,
	"conflicts_list":  true,
	"service_get":     true,
	"service_list":    true,
	"skill_list":      true,
	"sync_check":      true,
	"sync_pull":       true,
	"sync_status":     true,
	"artifact_read":   true,
	"artifact_list":   true,
	"template_list":   true,
	"asset_read":      true,
	"asset_list":      true,
	"source_list":     true,
}

// ToolRequiresWrite reports whether calling the named tool requires write
// access to the KB. Fail-closed: unknown tool names (and plugin tools) require
// write.
func ToolRequiresWrite(name string) bool {
	return !ReadOnlyToolNames[name]
}
