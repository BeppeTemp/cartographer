package mcpserver

// readMinTools is the tools/list of an ?access=read-min connection (D1184), in
// listing order. The listing is narrowed, calling is not: every other read tool
// stays callable by name, as the advanced tools are (D65). Every name must be in
// readOnlyToolNames, so the profile can never list a write tool
// (TestReadMinToolsAreReadOnly).
var readMinTools = []string{
	"search",
	"concept_read",
	"index_get",
	"graph_context",
	"graph_neighbors",
	"concept_list",
	"atlas_overview",
	"artifact_read",
	"kb_guide",
}

// inReadMinProfile reports whether name is listed by the read-min profile.
func inReadMinProfile(name string) bool {
	for _, n := range readMinTools {
		if n == name {
			return true
		}
	}
	return false
}
