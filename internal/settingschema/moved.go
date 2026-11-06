package settingschema

import (
	"fmt"
	"reflect"
	"strings"

	"gopkg.in/yaml.v3"
)

// Moved is a key that a release moved to another path of the same file, its value
// unchanged (D1725). Unlike Removed it loses nothing: a
// realignment cuts the key out of From and puts it under To, with the comment a
// person wrote above it; the comment the schema wrote for it goes (Description).
//
// A file that holds both paths with different values is refused, never resolved
// by a silent choice (Registry.MovedConflicts, Realign): the operator keeps one.
// With equal values the old path is simply dropped. A loader keeps reading From
// for at least one minor, so a file on a read-only mount keeps working.
//
// Paths are dotted mapping keys; a "[]" segment (a list of mappings) is not
// supported, a move across list entries has no single destination.
type Moved struct {
	From string
	To   string
	// Plan is the decision that moved it.
	Plan int
	// Description is the description From had in its last release, and Previous
	// the ones before it, as in Removed: how the comment the schema wrote above
	// the key is told from a person's.
	Description string
	Previous    []string
}

// movedPair is one Moved found in a document.
type movedPair struct {
	m          Moved
	parent     *yaml.Node // the mapping that holds From
	idx        int        // index of its key in parent
	value      *yaml.Node // From's value, an alias resolved
	to         *yaml.Node // To's value when the file already has it, else nil
	toParent   *yaml.Node // the mapping that holds To, nil when it does not exist yet
	nullParent *yaml.Node // a bare key on the way to To, to become a mapping
	rest       []string   // the segments of To that do not exist yet, the last one the key
	conflicts  bool       // To exists with a different value
}

// findMoved locates every Moved of r that the document holds. A pair reached
// through an alias or a merge key is shared with other places and is left alone,
// as Realign does for a removed key.
func (r Registry) findMoved(doc *yaml.Node) ([]movedPair, error) {
	root := rootMapping(doc)
	if root == nil {
		return nil, nil
	}
	var out []movedPair
	for _, m := range r.Moved {
		if strings.Contains(m.From, "[]") || strings.Contains(m.To, "[]") {
			return nil, fmt.Errorf("moved %s -> %s: a list segment cannot be moved", m.From, m.To)
		}
		var found *movedPair
		walkPath(root, strings.Split(m.From, "."), "", false, map[walkStep]bool{}, func(parent *yaml.Node, idx int, concrete string, shared bool) {
			if shared || found != nil {
				return
			}
			v, _ := deref(parent.Content[idx+1])
			found = &movedPair{m: m, parent: parent, idx: idx, value: v}
		})
		if found == nil {
			continue
		}
		segs := strings.Split(m.To, ".")
		cur := root
		shared := false // the destination is reached through an alias: other places use it too
		for i, seg := range segs {
			var via bool
			cur, via = deref(cur)
			shared = shared || via
			if cur == nil || cur.Kind != yaml.MappingNode {
				return nil, fmt.Errorf("moved %s -> %s: %s is not a mapping", m.From, m.To, strings.Join(segs[:i], "."))
			}
			j := pairIdx(cur, seg)
			if j < 0 {
				found.toParent, found.rest = cur, segs[i:]
				break
			}
			child, via := deref(cur.Content[j+1])
			if i == len(segs)-1 {
				found.toParent = cur
				found.to = child
				shared = shared || via
				break
			}
			if child != nil && child.Kind == yaml.ScalarNode && !holdsValue(child) {
				// A bare key (git.identities:) is an empty mapping that was not written out.
				found.toParent, found.rest, found.nullParent = child, segs[i+1:], child
				shared = shared || via
				break
			}
			cur = cur.Content[j+1]
		}
		if shared {
			continue // an anchor's mapping is not edited: the old key stays, still read
		}
		if found.to != nil && holdsValue(found.value) && (!holdsValue(found.to) || !sameNodeValue(found.value, found.to)) {
			// A value at the old path and a different one, or an empty entry (which
			// means "none"), at the new: never a silent choice.
			found.conflicts = true
		}
		out = append(out, *found)
	}
	return out, nil
}

func sameNodeValue(a, b *yaml.Node) bool {
	var x, y any
	if a.Decode(&x) != nil || b.Decode(&y) != nil {
		return false
	}
	return reflect.DeepEqual(x, y)
}

func (p movedPair) conflictMessage() string {
	return fmt.Sprintf("%s and %s are both set with different values; %s replaces %s, keep one of the two and remove the other", p.m.From, p.m.To, p.m.To, p.m.From)
}

// MovedConflicts lists, for the settings file data, the moved keys that are set
// at both their old and their new path with different values (or an empty one): the refusal a
// loader and a realignment share. Unparseable data holds none.
func (r Registry) MovedConflicts(data []byte) []string {
	var doc yaml.Node
	if yaml.Unmarshal(data, &doc) != nil {
		return nil
	}
	pairs, err := r.findMoved(&doc)
	if err != nil {
		return nil
	}
	var out []string
	for _, p := range pairs {
		if p.conflicts {
			out = append(out, p.conflictMessage())
		}
	}
	return out
}

// relocate applies the moves of reg to doc and reports them, with the mappings
// that lost a key (for pruneEmpty). Idempotent: once From is gone there is
// nothing to do. An error leaves doc unchanged.
func (s Schema) relocate(doc *yaml.Node, reg Registry) (changes []Change, touched []*yaml.Node, err error) {
	pairs, err := reg.findMoved(doc)
	if err != nil {
		return nil, nil, err
	}
	for _, p := range pairs {
		if p.conflicts {
			return nil, nil, fmt.Errorf("%s", p.conflictMessage())
		}
	}
	// One move at a time, each found again: an earlier one may have created the
	// mapping a later one lands in.
	for _, m := range reg.Moved {
		one, err := Registry{Moved: []Moved{m}}.findMoved(doc)
		if err != nil {
			return nil, nil, err
		}
		for _, p := range one {
			idx := pairIdx(p.parent, p.m.From[strings.LastIndex(p.m.From, ".")+1:])
			if idx < 0 {
				continue
			}
			touched = append(touched, p.parent)
			switch {
			case !holdsValue(p.value):
				// Nothing to carry: an empty value is dropped, not planted at the
				// new path. A note a person wrote above it moves on (removePair).
				s.removePair(doc, p.parent, idx)
				changes = append(changes, Change{Kind: ChangeMoved, Path: m.From, Detail: "dropped, it was empty"})
			case p.to != nil:
				s.removePair(doc, p.parent, idx)
				changes = append(changes, Change{Kind: ChangeMoved, Path: m.From, Detail: "dropped, " + m.To + " already holds the same value"})
			default:
				key, val := p.parent.Content[idx], p.parent.Content[idx+1]
				p.parent.Content = append(p.parent.Content[:idx], p.parent.Content[idx+2:]...)
				key.Value = p.rest[len(p.rest)-1]
				if p.nullParent != nil {
					// Keep the comments the bare key carried.
					head, foot := p.nullParent.HeadComment, p.nullParent.FootComment
					*p.nullParent = yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", HeadComment: head, FootComment: foot}
				}
				attach, attachVal := key, val
				// Build the missing mappings from the inside out.
				for i := len(p.rest) - 2; i >= 0; i-- {
					attachVal = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{attach, attachVal}}
					attach = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: p.rest[i]}
				}
				p.toParent.Content = append(p.toParent.Content, attach, attachVal)
				changes = append(changes, Change{Kind: ChangeMoved, Path: m.From, Detail: "now " + m.To})
			}
		}
	}
	return changes, touched, nil
}
