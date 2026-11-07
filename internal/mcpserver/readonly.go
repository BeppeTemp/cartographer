package mcpserver

import "github.com/BeppeTemp/cartographer/internal/client"

// readOnlyToolNames is the list of tool names that never mutate KB content and
// are therefore safe to call with a read-only ("kb:<name>:r") scope token. The
// source of truth is client.ReadOnlyToolNames (internal/client/readonly.go: a
// tool author adds the name there); this is an alias of it. It must be kept in sync with the ReadOnly field set on each
// tool's Tool{} literal in tools_*.go — TestReadOnlyToolsGolden (httpserver_auth_test.go)
// builds a real registry via RegisterKBTools and fails if the two diverge.

var readOnlyToolNames = client.ReadOnlyToolNames

// ToolRequiresWrite reports whether calling the named tool requires write
// access to the KB. Fail-closed: unknown tool names require write.
func ToolRequiresWrite(name string) bool {
	return client.ToolRequiresWrite(name)
}
