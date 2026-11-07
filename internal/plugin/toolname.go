package plugin

import "strings"

// ToolName is the name a plugin's tool is served and called by:
// <plugin>_<tool> with hyphens turned into underscores (a client's tool names
// are identifiers). The bridge, the approval-time `tools` hint, `plugin
// validate` and the approval preview all use this one function, so the name a
// user reads is the name `cartographer call` takes.
func ToolName(plugin, tool string) string {
	return strings.ReplaceAll(plugin+"_"+tool, "-", "_")
}
