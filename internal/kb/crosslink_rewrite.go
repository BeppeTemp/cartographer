package kb

import (
	"slices"
	"sort"

	"github.com/BeppeTemp/cartographer/internal/okf"
)

// CrossRetarget says that the page FromID of the KB FromKB is now the page
// ToID of the KB ToKB: the unit of work of RewriteCrossLinks.
type CrossRetarget struct{ FromKB, FromID, ToKB, ToID string }

// RewriteCrossLinks retargets the cross-KB wiki-links of body, resolving them
// as ExtractCrossLinks does (same code-span masking, same parse): a link
// [[kb:<name>/<id>#s|l]] whose name is one of names(FromKB) and whose id is
// FromID becomes [[kb:<ToKB>/<ToID>#s|l]], the section and the label kept
// verbatim. ownNames are the names (current and aliases) of the KB that holds
// the body: a link naming one of them is a finding, never an edge, and is left
// alone; and a retarget whose ToKB is one of them lands as the internal form
// [[<ToID>#s|l]], never as [[kb:<own>/...]] (a self-named link is a finding).
// A server-qualified or malformed link is left alone. names may be nil: only
// FromKB itself then matches. Duplicate (FromKB, FromID) pairs keep the first.
// It returns the new body and the number of links rewritten.
func RewriteCrossLinks(body string, ownNames []string, moves []CrossRetarget, names func(kb string) []string) (string, int) {
	if len(moves) == 0 {
		return body, 0
	}
	type key struct{ kb, id string }
	idx := map[key]CrossRetarget{}
	for _, mv := range moves {
		ns := []string{mv.FromKB}
		if names != nil {
			if got := names(mv.FromKB); len(got) > 0 {
				ns = got
			}
		}
		for _, n := range ns {
			if _, dup := idx[key{n, mv.FromID}]; !dup {
				idx[key{n, mv.FromID}] = mv
			}
		}
	}
	return rewriteCrossTargets(body, ownNames, func(l CrossLink) (string, bool) {
		mv, ok := idx[key{l.KB, string(l.ID)}]
		if !ok {
			return "", false
		}
		if slices.Contains(ownNames, mv.ToKB) {
			return mv.ToID, true
		}
		return kbScheme + mv.ToKB + "/" + mv.ToID, true
	})
}

// InternalizeCrossLinks rewrites every well-formed [[kb:<own>/<id>#s|l]] whose
// name is in ownNames to the internal form [[<id>#s|l]]: the self-named form
// is a finding, and a page brought into a KB must not carry one.
func InternalizeCrossLinks(body string, ownNames []string) (string, int) {
	return rewriteCrossTargets(body, nil, func(l CrossLink) (string, bool) {
		if !slices.Contains(ownNames, l.KB) {
			return "", false
		}
		return string(l.ID), true
	})
}

// rewriteCrossTargets replaces group 1 of each well-formed, unqualified cross
// link that pick accepts, skipping links naming skip. Only the target group is
// edited: "#section" and "|label" stay verbatim.
func rewriteCrossTargets(body string, skip []string, pick func(CrossLink) (string, bool)) (string, int) {
	masked := MaskCodeSpans(body)
	var edits []spanEdit
	for _, m := range wikiLinkRe.FindAllStringSubmatchIndex(masked, -1) {
		l, ok := ParseCrossLink(body[m[2]:m[3]])
		if !ok || l.Malformed || l.Server != "" || slices.Contains(skip, l.KB) {
			continue
		}
		if text, ok := pick(l); ok {
			edits = append(edits, spanEdit{m[2], m[3], text})
		}
	}
	return applySpanEdits(body, edits)
}

// CrossLinksTo lists, sorted, the pages of this KB holding a well-formed cross
// link to one of ids in the KB named by one of names (its current name and
// aliases). A link counts only when its written name resolves to that KB as
// CrossTarget resolves it, the relation guard of an alias included: a link the
// guard leaves unresolved is never rewritten. The error is that of CrossLinks
// or of the guard.
func (kb *KB) CrossLinksTo(names []string, ids map[string]bool) ([]okf.ConceptID, error) {
	cross, err := kb.CrossLinks()
	if err != nil {
		return nil, err
	}
	m := kb.Mounts()
	if m == nil {
		return nil, nil
	}
	followed := map[string]bool{}
	var out []okf.ConceptID
	for src, ls := range cross {
		for _, l := range ls {
			if l.Malformed || l.Server != "" || !ids[string(l.ID)] || !slices.Contains(names, l.KB) {
				continue
			}
			target, ok := m.Lookup(l.KB)
			if !ok || target == kb {
				continue
			}
			f, seen := followed[l.KB]
			if !seen {
				if f, err = kb.aliasFollowed(l.KB, target); err != nil {
					return nil, err
				}
				followed[l.KB] = f
			}
			if f {
				out = append(out, src)
				break
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}

// FollowedNames lists, sorted, the names under which a link of this KB reaches
// the KB mounted as current: its current name and the aliases this KB follows
// (an alias a relation of this KB guards to another remote is left out). It is
// the `names` of RewriteCrossLinks, so a link the guard leaves unresolved is
// never rewritten. The error is the guard's (Relations or the origin could not
// be read): the answer is then unknown.
func (kb *KB) FollowedNames(current string) ([]string, error) {
	m := kb.Mounts()
	if m == nil {
		return nil, nil
	}
	target, ok := m.Lookup(current)
	if !ok {
		return nil, nil
	}
	var out []string
	for _, n := range m.NamesFor(m.Canonical(current)) {
		followed, err := kb.aliasFollowed(n, target)
		if err != nil {
			return nil, err
		}
		if followed {
			out = append(out, n)
		}
	}
	return out, nil
}
