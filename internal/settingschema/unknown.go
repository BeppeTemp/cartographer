package settingschema

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// Unknown is a key a settings file holds that neither the schema nor the
// retired registry knows: most often a typo, which silently does nothing.
type Unknown struct {
	// Path is the concrete location, with the list index ("kbs[1].remtoe").
	Path         string
	Line, Column int
	// Hint is the path of the nearest known key at that level, "" when none is close.
	Hint string
}

// String is the line a warning prints.
func (u Unknown) String() string {
	s := fmt.Sprintf("%s: unknown key, ignored (line %d)", u.Path, u.Line)
	if u.Hint != "" {
		s += "; did you mean " + u.Hint + "?"
	}
	return s
}

// maxAliasDepth stops a merge key that leads back to itself.
const maxAliasDepth = 16

// Unknown lists, in file order, the keys of data (the contents of a settings
// file) that the table does not describe and reg does not retire: it is the
// table, not a second list, that says what is known. A key of a retired
// registry has its own message and is not reported here. Below a key of the
// table that has no keys of its own (providers, plugins' settings, a map of
// names) the names are the operator's and are not checked, and an unparseable
// file holds nothing.
func (s Schema) Unknown(data []byte, reg Registry) []Unknown {
	var doc yaml.Node
	if yaml.Unmarshal(data, &doc) != nil {
		return nil
	}
	root := rootMapping(&doc)
	if root == nil {
		return nil
	}
	retired := map[string]bool{}
	for _, h := range reg.Find(&doc) {
		retired[h.Path] = true
	}
	if moved, err := reg.findMoved(&doc); err == nil {
		for _, p := range moved {
			retired[p.m.From] = true
		}
	}
	w := unknownWalk{retired: retired}
	w.walk(s.treeOf(true), root, "", 0)
	return w.out
}

type unknownWalk struct {
	retired map[string]bool
	out     []Unknown
}

// pairs is the key and value nodes of a mapping, merge keys resolved.
func pairs(m *yaml.Node, depth int) (keys, vals []*yaml.Node) {
	m, _ = deref(m)
	if m == nil || m.Kind != yaml.MappingNode || depth > maxAliasDepth {
		return nil, nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		k, v := m.Content[i], m.Content[i+1]
		if k.Kind != yaml.ScalarNode || k.ShortTag() != "!!merge" {
			keys, vals = append(keys, k), append(vals, v)
			continue
		}
		src, _ := deref(v)
		from := []*yaml.Node{src}
		if src != nil && src.Kind == yaml.SequenceNode {
			from = src.Content
		}
		for _, f := range from {
			mk, mv := pairs(f, depth+1)
			keys, vals = append(keys, mk...), append(vals, mv...)
		}
	}
	return keys, vals
}

// isRetired: the key is retired, or is the parent of a retired one (the
// scopes of scopes.<provider>, whose entries moved elsewhere).
func (w *unknownWalk) isRetired(path string) bool {
	for p := range w.retired {
		if p == path || strings.HasPrefix(p, path+".") || strings.HasPrefix(p, path+"[") {
			return true
		}
	}
	return false
}

func (w *unknownWalk) walk(n *node, m *yaml.Node, path string, depth int) {
	if depth > maxAliasDepth {
		return
	}
	keys, vals := pairs(m, 0)
	for i, k := range keys {
		concrete := k.Value
		if path != "" {
			concrete = path + "." + k.Value
		}
		child, ok := n.byName[k.Value]
		if !ok {
			child, ok = wildcardChild(n)
		}
		if !ok {
			if !w.isRetired(concrete) {
				w.out = append(w.out, Unknown{Path: concrete, Line: k.Line, Column: k.Column, Hint: nearest(n, k.Value, path)})
			}
			continue
		}
		v, _ := deref(vals[i])
		switch {
		case v == nil || len(child.kids) == 0:
		case child.list && v.Kind == yaml.SequenceNode:
			for j, item := range v.Content {
				w.walk(child, item, fmt.Sprintf("%s[%d]", concrete, j), depth+1)
			}
		case !child.list && v.Kind == yaml.MappingNode:
			w.walk(child, v, concrete, depth+1)
		}
	}
}

// wildcardChild is the {name} child of a node: a map whose entries are named by
// the operator (plugins.{name}.digest).
func wildcardChild(n *node) (*node, bool) {
	for _, c := range n.kids {
		if strings.HasPrefix(c.name, "{") {
			return c, true
		}
	}
	return nil, false
}

// nearest is the path of the known key closest to name among the children of n,
// "" when none is within a third of its length (two letters at least).
func nearest(n *node, name, path string) string {
	best, bestD := "", len(name)/3
	bestD = max(bestD, 2)
	for _, c := range n.kids {
		if strings.HasPrefix(c.name, "{") {
			continue
		}
		if d := EditDistance(strings.ToLower(name), strings.ToLower(c.name)); d <= bestD && d < len([]rune(name)) && (best == "" || d < bestD) {
			best, bestD = c.name, d
		}
	}
	if best != "" && path != "" {
		best = path + "." + best
	}
	return best
}

// EditDistance is the Levenshtein distance of two strings.
func EditDistance(x, y string) int {
	a, b := []rune(x), []rune(y)
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}
