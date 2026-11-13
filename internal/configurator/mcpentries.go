package configurator

import "sort"

// OpenCode is the one provider that reads two shapes of the same MCP section at
// once: the native V2 "mcp"."servers" — what OpenCode 2.x writes itself
// (`opencode mcp add`) and what its 2.3 documentation describes — and the V1
// flat "mcp"."<name>" that 1.x reads and 2.x still normalizes on load (D2056).
// Every reader and writer of a client's MCP section goes through the path list
// below, so none can disagree with another about where an entry lives.

// mcpServerPaths returns the JSON paths at which provider keeps its map of MCP
// server entries, native shape first. The descriptor's MCPServerKey is the
// single-shape case; OpenCode is the only provider with two.
func mcpServerPaths(p Provider) [][]string {
	if p == ProviderOpenCode {
		return [][]string{{"mcp", "servers"}, {"mcp"}}
	}
	if d, ok := Lookup(p); ok && d.MCPServerKey != "" {
		return [][]string{{d.MCPServerKey}}
	}
	return nil
}

// openCodeMCPMeta are the members of OpenCode's "mcp" object that are not
// servers: the native servers map and the V2 timeout defaults. A V1 file has
// only servers there, but a file OpenCode 2.x has written has both.
var openCodeMCPMeta = map[string]bool{"servers": true, "timeout": true}

// isMCPServerName reports whether name, found at path in provider's config, is a
// server entry rather than a member of the section itself.
func isMCPServerName(p Provider, path []string, name string) bool {
	if p == ProviderOpenCode && len(path) == 1 {
		return !openCodeMCPMeta[name]
	}
	return true
}

// mapAt returns the object at path under root, or nil.
func mapAt(root map[string]any, path []string) map[string]any {
	cur := root
	for _, seg := range path {
		next, ok := cur[seg].(map[string]any)
		if !ok {
			return nil
		}
		cur = next
	}
	return cur
}

// ensureMap returns the object at path under root, creating the missing levels.
func ensureMap(root map[string]any, path []string) map[string]any {
	cur := root
	for _, seg := range path {
		next, ok := cur[seg].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[seg] = next
		}
		cur = next
	}
	return cur
}

// pruneEmptyMaps removes every level of path a deletion left empty, from the
// leaf up, so a native section that lost its last server leaves no
// "mcp": {"servers": {}} shell behind.
func pruneEmptyMaps(root map[string]any, path []string) {
	for i := len(path); i >= 1; i-- {
		parent := mapAt(root, path[:i-1])
		if parent == nil {
			return
		}
		child, ok := parent[path[i-1]].(map[string]any)
		if !ok || len(child) > 0 {
			return
		}
		delete(parent, path[i-1])
	}
}

func samePath(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// MCPServerNames lists the MCP server names provider's config object declares,
// across every shape it reads, sorted. A meta member (OpenCode's "servers" and
// "timeout") is never reported as a name.
func MCPServerNames(root map[string]any, p Provider) []string {
	seen := map[string]bool{}
	for _, path := range mcpServerPaths(p) {
		for name := range mapAt(root, path) {
			if isMCPServerName(p, path, name) {
				seen[name] = true
			}
		}
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// MCPServerEntry returns the entry for name in the first shape provider reads
// that holds it (the native one first).
func MCPServerEntry(root map[string]any, p Provider, name string) (any, bool) {
	for _, path := range mcpServerPaths(p) {
		if !isMCPServerName(p, path, name) {
			continue
		}
		if entry, ok := mapAt(root, path)[name]; ok {
			return entry, true
		}
	}
	return nil, false
}

// MergeMCPEmission merges one emitted provider config into root and makes the
// emitted shape the one that wins: every server entry replaces the same-named
// one whole, and the name is removed from every other shape provider accepts.
// That last half is what keeps a file converged: a client still normalizing the
// V1 flat map (OpenCode 2.x) would otherwise keep serving a stale entry that
// shadows the one just written, and Cartographer would never see the difference.
func MergeMCPEmission(root, emitted map[string]any, p Provider) {
	paths := mcpServerPaths(p)
	if len(paths) == 0 {
		return
	}
	// The emitter writes one shape: the native one when present, the legacy one
	// otherwise.
	at, src := paths[0], mapAt(emitted, paths[0])
	if src == nil && len(paths) > 1 {
		at, src = paths[1], mapAt(emitted, paths[1])
	}
	if src == nil {
		return
	}
	dst := ensureMap(root, at)
	for name, entry := range src {
		if !isMCPServerName(p, at, name) {
			continue
		}
		dst[name] = entry
		for _, other := range paths {
			if samePath(other, at) {
				continue
			}
			deleteMCPEntryAt(root, other, name)
		}
	}
}

// deleteMCPEntryAt removes name from the server map at path and prunes the maps
// it empties.
func deleteMCPEntryAt(root map[string]any, path []string, name string) bool {
	parent := mapAt(root, path[:len(path)-1])
	if parent == nil {
		return false
	}
	servers, ok := parent[path[len(path)-1]].(map[string]any)
	if !ok {
		return false
	}
	if _, ok := servers[name]; !ok {
		return false
	}
	delete(servers, name)
	pruneEmptyMaps(root, path)
	return true
}

// RemoveMCPServer deletes name from every shape provider accepts and reports
// whether any held it.
func RemoveMCPServer(root map[string]any, p Provider, name string) bool {
	removed := false
	for _, path := range mcpServerPaths(p) {
		if deleteMCPEntryAt(root, path, name) {
			removed = true
		}
	}
	return removed
}
