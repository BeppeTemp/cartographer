package configurator

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// OpenCode is the one provider that reads two shapes of the same MCP section at
// once: the native V2 "mcp"."servers" — what OpenCode 2.x writes itself
// (`opencode mcp add`) and what its 2.3 documentation describes — and the V1
// flat "mcp"."<name>" that 1.x reads and 2.x still normalizes on load (D2056).
// Every reader and writer of a client's MCP section goes through the path list
// below, so none can disagree with another about where an entry lives.

// MCPShapeProvider returns the built-in provider whose MCP section layout p
// follows: p itself for a built-in, its base for a plugin provider — whose
// emitter is the base's, so it writes the base's format (D2056).
func MCPShapeProvider(p Provider) Provider {
	if d, ok := Lookup(p); ok && d.Base != "" {
		return d.Base
	}
	return p
}

// mcpServerPaths returns the JSON paths at which provider keeps its map of MCP
// server entries, native shape first. The descriptor's MCPServerKey is the
// single-shape case; OpenCode — and a plugin provider based on it — is the only
// one with two.
func mcpServerPaths(p Provider) [][]string {
	if MCPShapeProvider(p) == ProviderOpenCode {
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
	if MCPShapeProvider(p) == ProviderOpenCode && len(path) == 1 {
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

// ErrMCPSectionUnusable marks a merge refused because the client's MCP section
// holds something that is not a server map: a value that is not an object at
// "mcp" or "mcp"."servers", or a V1 server entry named "servers" where the
// native shape would write its section. Replacing it would discard what the
// user wrote, and the merge is non-destructive (D23); a caller reports the
// reason, and a KB descriptor is skipped with a warning like an unrepresentable
// name.
var ErrMCPSectionUnusable = errors.New("the client's MCP section cannot take this entry without discarding what is in it")

// isV1ServerEntry reports whether m is a V1 server entry (an object whose
// "type" is "local" or "remote") rather than a map of servers.
func isV1ServerEntry(m map[string]any) bool {
	t, _ := m["type"].(string)
	return t == "local" || t == "remote"
}

// ensureMap returns the object at path under root, creating the missing levels.
// A level that exists and is not an object, or the last one when it is a V1
// server entry, is refused (ErrMCPSectionUnusable), never replaced.
func ensureMap(root map[string]any, path []string) (map[string]any, error) {
	cur := root
	for i, seg := range path {
		v, present := cur[seg]
		next, ok := v.(map[string]any)
		switch {
		case !present || (v == nil):
			next = map[string]any{}
			cur[seg] = next
		case !ok:
			return nil, fmt.Errorf("%w: %s is not an object", ErrMCPSectionUnusable, strings.Join(path[:i+1], "."))
		case i == len(path)-1 && len(path) > 1 && isV1ServerEntry(next):
			return nil, fmt.Errorf("%w: %s is a V1 server entry (a server named %q), not a map of servers; rename or move it", ErrMCPSectionUnusable, strings.Join(path[:i+1], "."), seg)
		}
		cur = next
	}
	return cur, nil
}

// AdaptUnknownShape keeps the shape a server already has on disk when the
// installed major could not be read (the emitter wrote the native default): a
// flat mcp.<name> entry without a native one of the same name is rewritten in
// the flat shape (enabled, no oauth), so a 1.x install is never stripped of its
// server by a sync that could not run `opencode --version`. Neither shape
// present, or a native entry present: unchanged. It mutates emitted and returns
// the path to merge at.
func AdaptUnknownShape(existing, emitted map[string]any, p Provider, at []string) []string {
	if MCPShapeProvider(p) != ProviderOpenCode || !samePath(at, []string{"mcp", "servers"}) {
		return at
	}
	src := mapAt(emitted, at)
	flat := []string{"mcp"}
	flatMap := mapAt(existing, flat)
	native := mapAt(existing, at)
	moved := map[string]any{}
	for name, entry := range src {
		e, isMap := entry.(map[string]any)
		if _, hasNative := native[name]; hasNative || !isMap || !isMCPServerName(p, flat, name) {
			continue
		}
		cur, hasFlat := flatMap[name].(map[string]any)
		if !hasFlat || !isV1ServerEntry(cur) {
			continue
		}
		conv := make(map[string]any, len(e)+1)
		for k, v := range e {
			if k != "oauth" {
				conv[k] = v
			}
		}
		conv["enabled"] = true
		moved[name] = conv
	}
	if len(moved) == 0 {
		return at
	}
	for name := range moved {
		delete(src, name)
	}
	if len(src) > 0 {
		return at // mixed batch: the emitter writes one server per file
	}
	emitted["mcp"] = moved
	return flat
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
//
// at is the path the emitter wrote under (EmitResult.MCPServerPath), so a shape
// is never inferred from the content: a V1 entry legitimately named "servers"
// is a server map member, not the native section. An empty at is the
// single-shape provider's only path; a provider with two shapes must pass its
// own, and writes nothing when it does not — guessing a shape is how the merge
// once wrote an entry's fields as server names.
func MergeMCPEmission(root, emitted map[string]any, p Provider, at []string) error {
	return MergeMCPEmissionShaped(root, emitted, p, at, false)
}

// MergeMCPEmissionShaped is MergeMCPEmission for an emission whose shape was a
// default because the client's version could not be read (EmitResult.
// MCPShapeUnknown): the shape already on disk is kept (AdaptUnknownShape).
// It returns ErrMCPSectionUnusable, writing nothing, when the section holds
// something that is not a server map.
func MergeMCPEmissionShaped(root, emitted map[string]any, p Provider, at []string, shapeUnknown bool) error {
	paths := mcpServerPaths(p)
	if len(paths) == 0 {
		return nil
	}
	if shapeUnknown && len(at) > 0 {
		at = AdaptUnknownShape(root, emitted, p, at)
	}
	if len(at) == 0 {
		if len(paths) > 1 {
			// A provider with two shapes must say which one it wrote: guessing
			// from the content once wrote an entry's fields as server names.
			return nil
		}
		at = paths[0]
	}
	src := mapAt(emitted, at)
	if src == nil {
		return nil
	}
	dst, err := ensureMap(root, at)
	if err != nil {
		return err
	}
	for name, entry := range src {
		dst[name] = entry
		for _, other := range paths {
			if samePath(other, at) {
				continue
			}
			deleteMCPEntryAt(root, p, other, name)
		}
	}
	return nil
}

// deleteMCPEntryAt removes name from the server map at path and prunes the maps
// it empties. A name that is section metadata at that path (OpenCode's
// "servers") is never deleted there: it belongs to the section, not to a server.
func deleteMCPEntryAt(root map[string]any, p Provider, path []string, name string) bool {
	if !isMCPServerName(p, path, name) {
		return false
	}
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
		if deleteMCPEntryAt(root, p, path, name) {
			removed = true
		}
	}
	return removed
}
