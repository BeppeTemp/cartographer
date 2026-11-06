package settingschema

import (
	"slices"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Reorder rewrites the keys of doc in the order of the table: the root keys,
// and the keys of every mapping the table describes key by key (server.*,
// placeholders.*). reg.Order gives the same for the entries of a map whose own
// keys the table leaves to the file ("providers": the keys of each provider).
// It reports whether any key moved.
//
// A key travels with the comment above it and the one on its line, except the
// comment above the file's first key and the one below its last, which stay at
// the top and at the end; a key the
// table does not list (an unknown one, or a hidden one) stays right after the
// key that preceded it in the file, so a hand-curated file keeps its extras
// beside what they annotate. The order of the entries of a map the file names
// (providers, knowledge bases, paths) and of every list is the file's. Call it
// after homeBlocks and before Render: the block of unset settings is rebuilt
// at the new end of each mapping.
func (s Schema) Reorder(doc *yaml.Node, reg Registry) bool {
	root := docMapping(doc)
	if root == nil {
		return false
	}
	return s.reorderMapping(root, "", reg)
}

func (s Schema) reorderMapping(m *yaml.Node, path string, reg Registry) bool {
	moved := false
	if names := s.childNames(path); len(names) > 0 {
		moved = reorderPairs(m, names, path == "")
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		v := m.Content[i+1]
		if v.Kind != yaml.MappingNode {
			continue
		}
		sub := m.Content[i].Value
		if path != "" {
			sub = path + "." + sub
		}
		if len(s.childNames(sub)) > 0 {
			if s.reorderMapping(v, sub, reg) {
				moved = true
			}
			continue
		}
		// A map of entries (one per provider, per KB): each entry has the keys
		// reg.Order names for the map.
		if order := reg.Order[sub]; len(order) > 0 {
			for j := 0; j+1 < len(v.Content); j += 2 {
				if e := v.Content[j+1]; e.Kind == yaml.MappingNode && reorderPairs(e, order, false) {
					moved = true
				}
			}
		}
	}
	return moved
}

// childNames lists, in table order, the keys directly under path ("" for the
// root), hidden ones included.
func (s Schema) childNames(path string) []string {
	var out []string
	for _, k := range s.Keys {
		rest := k.Path
		if path != "" {
			var ok bool
			if rest, ok = strings.CutPrefix(k.Path, path+"."); !ok {
				continue
			}
		}
		name, _, _ := strings.Cut(rest, ".")
		name = strings.TrimSuffix(name, "[]")
		if !slices.Contains(out, name) {
			out = append(out, name)
		}
	}
	return out
}

// reorderPairs sorts the pairs of m by the position of their key in names,
// each unlisted key staying behind the listed one it followed (or first, when
// it led the mapping). It reports whether the order changed.
//
// A mapping where one pair aliases (or merges) an anchor another pair defines
// keeps its order: moving an alias before its anchor writes a file that does not
// parse; Realign also parses the result back. For the root
// of the file (file), the comment above its first key and the one below its
// last stay at the top and at the end of the file, whatever key ends up there.
func reorderPairs(m *yaml.Node, names []string, file bool) bool {
	if refersAcrossPairs(m) {
		return false
	}
	type unit struct {
		rank  int
		pairs []*yaml.Node
	}
	var units []*unit
	for i := 0; i+1 < len(m.Content); i += 2 {
		rank := slices.Index(names, m.Content[i].Value)
		if rank >= 0 || len(units) == 0 {
			units = append(units, &unit{rank: rank})
		}
		u := units[len(units)-1]
		u.pairs = append(u.pairs, m.Content[i], m.Content[i+1])
	}
	if sort.SliceIsSorted(units, func(a, b int) bool { return units[a].rank < units[b].rank }) {
		return false
	}
	sort.SliceStable(units, func(a, b int) bool { return units[a].rank < units[b].rank })
	oldFirst, oldLast := m.Content[0], m.Content[len(m.Content)-2]
	head, foot := oldFirst.HeadComment, oldLast.FootComment
	m.Content = m.Content[:0:0]
	for _, u := range units {
		m.Content = append(m.Content, u.pairs...)
	}
	if file {
		// The new first key keeps the comment it has above it, under the file's
		// header; the new last key keeps the one below it, above the file's foot.
		oldFirst.HeadComment, oldLast.FootComment = "", ""
		first, last := m.Content[0], m.Content[len(m.Content)-2]
		first.HeadComment = stackComments(head, first.HeadComment)
		last.FootComment = stackComments(last.FootComment, foot)
	}
	return true
}

// refersAcrossPairs reports whether one pair of m holds an alias to an anchor
// defined in another pair of m (a merge key is such an alias): moving either
// pair could put the alias before its anchor. An anchor and its aliases inside
// the same pair travel together, and an anchor elsewhere in the file keeps its
// place relative to the whole mapping, so neither blocks the reorder.
func refersAcrossPairs(m *yaml.Node) bool {
	n := len(m.Content) / 2
	defs := make([]map[string]bool, n)
	refs := make([]map[string]bool, n)
	for i := range n {
		defs[i], refs[i] = map[string]bool{}, map[string]bool{}
		collectAnchors(m.Content[2*i], defs[i], refs[i])
		collectAnchors(m.Content[2*i+1], defs[i], refs[i])
	}
	for i := range n {
		for j := range n {
			if i == j {
				continue
			}
			for name := range refs[j] {
				if defs[i][name] {
					return true
				}
			}
		}
	}
	return false
}

// collectAnchors adds the anchors defined under n to defs and the anchors its
// aliases name to refs.
func collectAnchors(n *yaml.Node, defs, refs map[string]bool) {
	if n.Anchor != "" {
		defs[n.Anchor] = true
	}
	if n.Kind == yaml.AliasNode {
		refs[n.Value] = true
	}
	for _, c := range n.Content {
		collectAnchors(c, defs, refs)
	}
}

// stackComments puts a above b, one line break between them, dropping an empty
// one.
func stackComments(a, b string) string {
	if a == "" {
		return b
	}
	if b == "" {
		return a
	}
	return a + "\n" + b
}
