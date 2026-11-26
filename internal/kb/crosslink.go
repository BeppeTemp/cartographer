package kb

import (
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/BeppeTemp/cartographer/internal/okf"
)

// CrossLink is one [[kb:<name>[@<server>]/<id>]] wiki-link: a reference to a
// concept of another KB mounted on the same server. It carries the target
// and nothing else, never content. Server is reserved for a future server
// qualifier: it is parsed and kept, and resolves nowhere.
type CrossLink struct {
	Server, KB string
	ID         okf.ConceptID
	// Malformed is set when the name or the id does not hold; KB and ID then
	// carry what could be read of them.
	Malformed bool
}

// kbNameRe is config.ValidateKBName's rule. kb must not import config
// (internal/boundaries): keep the two in step.
var kbNameRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// ParseCrossLink reads the target of a wiki-link (group 1 of wikiLinkRe: no
// "#section", no label). ok is false when raw does not start with the kb: scheme, so
// it is an ordinary link.
func ParseCrossLink(raw string) (l CrossLink, ok bool) {
	rest, ok := strings.CutPrefix(raw, kbScheme)
	if !ok {
		return CrossLink{}, false
	}
	head, id, hasID := strings.Cut(rest, "/")
	name, server, hasServer := strings.Cut(head, "@")
	l.KB, l.Server = name, server
	l.Malformed = hasServer && !kbNameRe.MatchString(server)
	id = strings.TrimSuffix(id, ".md")
	l.ID = okf.ConceptID(id)
	l.Malformed = l.Malformed || !hasID || !kbNameRe.MatchString(l.KB) || okf.ValidateID(id) != nil
	return l, true
}

// ExtractCrossLinks lists the cross-KB links of a concept body, in order of
// appearance, without duplicates. Code is masked as ExtractLinks masks it. Only
// the wiki-link form counts: a markdown href "kb:..." is text.
func ExtractCrossLinks(body string) []CrossLink {
	var out []CrossLink
	seen := map[string]bool{}
	for _, m := range wikiLinkRe.FindAllStringSubmatch(MaskCodeSpans(body), -1) {
		l, ok := ParseCrossLink(m[1])
		if !ok {
			continue
		}
		// The key is normalised alike for both: ".md" never makes a second link.
		key := strings.TrimSuffix(m[1], ".md")
		if !l.Malformed {
			key = l.Server + "@" + l.KB + "/" + string(l.ID)
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, l)
	}
	return out
}

// CrossLinks returns, per concept, the cross-KB links of its body, from the
// current view. The map is shared: never mutate it.
func (kb *KB) CrossLinks() (map[okf.ConceptID][]CrossLink, error) {
	v, err := kb.graphView()
	if err != nil {
		return nil, err
	}
	return v.cross, nil
}

// CrossLinkedKBs lists the current mount names of the KBs the current view
// links to: well-formed links with no server qualifier whose name (or alias)
// is mounted, this KB's own name excluded. Sorted, without duplicates.
func (kb *KB) CrossLinkedKBs() ([]string, error) {
	return kb.CrossLinkedKBsWithin(0)
}

// CrossLinkedKBsWithin is CrossLinkedKBs for a poller: a view some reader
// validated less than maxAge ago is trusted, as GraphGenerationWithin does.
func (kb *KB) CrossLinkedKBsWithin(maxAge time.Duration) ([]string, error) {
	m := kb.Mounts()
	if m == nil {
		return nil, nil
	}
	v, err := kb.graphViewWithin(maxAge)
	if err != nil {
		return nil, err
	}
	self, set := kb.MountName(), map[string]bool{}
	for _, ls := range v.cross {
		for _, l := range ls {
			if l.Malformed || l.Server != "" {
				continue
			}
			if _, ok := m.Lookup(l.KB); !ok {
				continue
			}
			if name := m.Canonical(l.KB); name != self {
				set[name] = true
			}
		}
	}
	out := make([]string, 0, len(set))
	for n := range set {
		out = append(out, n)
	}
	sort.Strings(out)
	return out, nil
}

// CrossLinkedFrom lists the current mount names of the other mounted KBs whose
// current view links to this one (CrossLinkedKBs read the other way). Sorted,
// without duplicates; empty when this KB has no mount name. It reads one KB's
// snapshot at a time, each under its own leaf mutex, never two together; a KB
// whose view cannot be read is left out (unknown, never an error).
func (kb *KB) CrossLinkedFrom() []string { return kb.CrossLinkedFromWithin(0) }

// CrossLinkedFromWithin is CrossLinkedFrom for a poller (see CrossLinkedKBsWithin).
func (kb *KB) CrossLinkedFromWithin(maxAge time.Duration) []string {
	m := kb.Mounts()
	self := kb.MountName()
	if m == nil || self == "" {
		return nil
	}
	var out []string
	for _, name := range m.Names() {
		x, ok := m.Lookup(name)
		if !ok || x == kb {
			continue
		}
		linked, err := x.CrossLinkedKBsWithin(maxAge)
		if err != nil {
			continue
		}
		for _, n := range linked {
			if n == self {
				out = append(out, name)
				break
			}
		}
	}
	sort.Strings(out)
	return out
}
