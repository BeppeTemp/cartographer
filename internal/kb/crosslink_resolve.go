package kb

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/BeppeTemp/cartographer/internal/okf"
)

// crossMaxAge is how old a validation of another KB's graph may be when a
// cross-KB read trusts it: the Atlas poller's value (a write through the
// server forgets it, an out-of-band edit is seen within it). A variable so a
// test can shorten it.
var crossMaxAge = 3 * time.Second

// crossDigestBuilds counts CrossInboundDigest recomputes, for tests.
var crossDigestBuilds atomic.Int64

// CrossDigestBuilds is that counter, for tests of the packages above.
func CrossDigestBuilds() int64 { return crossDigestBuilds.Load() }

// CrossTarget is what is true of a link's target, whoever asks: it says
// nothing about what a caller may see. Err is set when the target KB's graph
// could not be validated: the answer is then unknown, neither Exists nor not.
type CrossTarget struct {
	Mounted bool
	Exists  bool
	Err     error
}

// viewWithin returns the graph view, validated within maxAge: when some reader
// validated the cache less than maxAge ago it is reused without another walk of
// the concept files (the rule of GraphGenerationWithin). It takes the graph
// cache mutex only, a leaf: never a git lock, so it is safe under another KB's
// git lock (internal/mcpserver/AGENTS.md).
func (kb *KB) viewWithin(maxAge time.Duration) (*graphView, error) {
	kb.graphMu.Lock()
	defer kb.graphMu.Unlock()
	if c := kb.graph; c != nil && c.view != nil && !c.stale && time.Since(c.validatedAt) < maxAge {
		return c.view, nil
	}
	return kb.graphViewLocked(nil)
}

// CrossTarget resolves one link against the mounts.
func (kb *KB) CrossTarget(l CrossLink) CrossTarget { return kb.CrossTargets([]CrossLink{l})[0] }

// CrossTargets resolves links in order, reading each distinct target KB once.
// A server-qualified or malformed link, no mounts, a name that is not
// mounted (an alias is followed) or a link naming this KB itself is not Mounted. This KB's own graph is never
// held while another's is read.
func (kb *KB) CrossTargets(links []CrossLink) []CrossTarget {
	out := make([]CrossTarget, len(links))
	m := kb.Mounts()
	type guard struct {
		followed bool
		err      error
	}
	guards := map[string]guard{}
	has := map[string]map[okf.ConceptID]struct{}{}
	errs := map[string]error{}
	for i, l := range links {
		if m == nil || l.Server != "" || l.Malformed {
			continue
		}
		target, ok := m.Lookup(l.KB)
		if !ok || target == kb {
			// A link naming this KB (by its name or an alias) is never an edge:
			// lint reports it, write [[<id>]] instead.
			continue
		}
		// An alias is followed only to the remote the KB's own relation of that
		// name declares (relations guard). A link by the current name pays nothing.
		if m.Canonical(l.KB) != l.KB {
			g, seen := guards[l.KB]
			if !seen {
				g.followed, g.err = kb.aliasFollowed(l.KB, target)
				guards[l.KB] = g
			}
			if g.err != nil {
				out[i].Err = g.err
				continue
			}
			if !g.followed {
				continue
			}
		}
		out[i].Mounted = true
		name := m.Canonical(l.KB)
		if _, done := has[name]; !done && errs[name] == nil {
			v, err := target.viewWithin(crossMaxAge)
			if err != nil {
				errs[name] = err
			} else {
				has[name] = v.exists
			}
		}
		if errs[name] != nil {
			out[i].Err = errs[name]
		} else {
			_, out[i].Exists = has[name][l.ID]
		}
	}
	return out
}

func indexCrossByTarget(cross map[okf.ConceptID][]CrossLink) map[string]map[okf.ConceptID][]okf.ConceptID {
	idx := map[string]map[okf.ConceptID][]okf.ConceptID{}
	for src, ls := range cross {
		for _, l := range ls {
			if l.Malformed || l.Server != "" {
				continue
			}
			if idx[l.KB] == nil {
				idx[l.KB] = map[okf.ConceptID][]okf.ConceptID{}
			}
			idx[l.KB][l.ID] = append(idx[l.KB][l.ID], src)
		}
	}
	return idx
}

// CrossRef is a page of another KB that links here.
type CrossRef struct {
	KB string
	ID okf.ConceptID
}

// inbound is one link of another mounted KB toward this one.
type inbound struct {
	kb          string
	target, src okf.ConceptID
}

// inbounds lists, sorted, the links of the other mounted KBs that name this
// KB (by its name or an alias) and, when target is non-empty, that id only. It
// also returns the generation of each view it read. Each KB's view is read in
// turn within crossMaxAge, never with another's mutex held: one lookup per
// mounted KB, not a walk. A KB whose graph cannot be validated is unknown,
// never absent: the error names it and no partial list is returned.
func (kb *KB) inbounds(target okf.ConceptID) ([]inbound, map[string]uint64, error) {
	m, self := kb.Mounts(), kb.MountName()
	if m == nil || self == "" {
		return nil, nil, nil
	}
	names := m.NamesFor(self)
	gens := map[string]uint64{}
	var out []inbound
	for _, x := range m.Names() {
		other, ok := m.Lookup(x)
		if !ok || other == kb {
			continue
		}
		v, err := other.viewWithin(crossMaxAge)
		if err != nil {
			return nil, nil, fmt.Errorf("cross-KB links of %q are unknown: %w", x, err)
		}
		gens[x] = v.generation
		for _, n := range names {
			if len(v.crossByTarget[n]) == 0 {
				continue
			}
			if n != self {
				// An alias of this KB, as x's page wrote it: followed only to the
				// remote x's relation of that name declares.
				followed, err := other.aliasFollowed(n, kb)
				if err != nil {
					return nil, nil, fmt.Errorf("cross-KB links of %q are unknown: %w", x, err)
				}
				if !followed {
					continue
				}
			}
			for tid, srcs := range v.crossByTarget[n] {
				if target != "" && tid != target {
					continue
				}
				for _, s := range srcs {
					out = append(out, inbound{x, tid, s})
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.kb != b.kb {
			return a.kb < b.kb
		}
		if a.target != b.target {
			return a.target < b.target
		}
		return a.src < b.src
	})
	return out, gens, nil
}

// CrossIncoming lists the pages of the other mounted KBs whose links name id
// of this KB, by this KB's name or one of its aliases. The error says another
// KB could not be read: the list is then unknown, not empty.
func (kb *KB) CrossIncoming(id okf.ConceptID) ([]CrossRef, error) {
	ins, _, err := kb.inbounds(id)
	if err != nil {
		return nil, err
	}
	var out []CrossRef
	for _, in := range ins {
		out = append(out, CrossRef{KB: in.kb, ID: in.src})
	}
	return out, nil
}

// CrossInboundRef is one link of a page of another KB toward Target of this one.
type CrossInboundRef struct {
	Target okf.ConceptID
	KB     string
	ID     okf.ConceptID
}

// CrossIncomingAll lists, sorted, every page of the other mounted KBs that
// links a page of this one (CrossIncoming for all targets at once). The error
// says another KB could not be read: the list is then unknown, not empty.
func (kb *KB) CrossIncomingAll() ([]CrossInboundRef, error) {
	ins, _, err := kb.inbounds("")
	if err != nil {
		return nil, err
	}
	out := make([]CrossInboundRef, 0, len(ins))
	for _, in := range ins {
		out = append(out, CrossInboundRef{Target: in.target, KB: in.kb, ID: in.src})
	}
	return out, nil
}

// CrossInboundKBs lists, sorted, the current names of the other mounted KBs
// with a page that links to this one (by its name or an alias). The error says
// a KB could not be read: the list is then unknown.
func (kb *KB) CrossInboundKBs() ([]string, error) {
	ins, _, err := kb.inbounds("")
	if err != nil {
		return nil, err
	}
	var out []string
	for _, in := range ins {
		if len(out) == 0 || out[len(out)-1] != in.kb {
			out = append(out, in.kb)
		}
	}
	return out, nil
}

// crossDigestMemo remembers the digest for the vector of the other KBs'
// graph generations it was computed on.
type crossDigestMemo struct {
	mu     sync.Mutex
	gens   map[string]uint64
	digest string
	ok     bool
}

// CrossInboundDigest is a sha of the links the other mounted KBs hold toward
// this one, empty when there are none: a link from another KB appearing or
// disappearing changes it. It sits in the lint-cache key, so it is memoized on
// the other KBs' graph generations, each read within crossMaxAge: while none
// moved it costs a clock read per KB, no git call and no walk. When another
// KB's graph cannot be read the links are unknown: it returns "unknown:" and
// that KB's name, which is never memoized and matches no real digest, so a
// stamp built on it changes again when the KB recovers.
func (kb *KB) CrossInboundDigest() string {
	m, self := kb.Mounts(), kb.MountName()
	if m == nil || self == "" {
		return ""
	}
	// The memo's lock covers the whole computation, so an older vector can
	// never overwrite a newer one.
	memo := &kb.crossDigest
	memo.mu.Lock()
	defer memo.mu.Unlock()
	if memo.ok {
		now := map[string]uint64{}
		for _, x := range m.Names() {
			if other, ok := m.Lookup(x); ok && other != kb {
				g, err := other.GraphGenerationWithin(crossMaxAge)
				if err != nil {
					break
				}
				now[x] = g
			}
		}
		if sameGens(memo.gens, now) {
			return memo.digest
		}
	}
	crossDigestBuilds.Add(1)
	ins, gens, err := kb.inbounds("")
	if err != nil {
		memo.ok = false
		return "unknown:" + err.Error()
	}
	digest := ""
	if len(ins) > 0 {
		h := sha256.New()
		for _, in := range ins {
			fmt.Fprintf(h, "%s\x00%s\x00%s\n", in.kb, in.target, in.src)
		}
		digest = hex.EncodeToString(h.Sum(nil))
	}
	// gens are the generations of the views the digest was built from.
	memo.gens, memo.digest, memo.ok = gens, digest, true
	return digest
}

func sameGens(a, b map[string]uint64) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}

// aliasFollowed says whether a link of this KB that names a KB as name reaches
// target. A name that is target's current mount name is always followed (a
// link alone carries no remote; a mounted name beats an alias). An alias is
// followed unless this KB declares a relation of that name (Relations) and
// target's normalized origin is not the relation's remote, or target has none:
// the same comparison as a relation's own resolution. An error is an unknown:
// reading Relations or the origin failed. Hot path: the first check returns
// before any memo is read, and both reads after it are in-memory within
// relationsMaxAge.
func (kb *KB) aliasFollowed(name string, target *KB) (bool, error) {
	m := kb.Mounts()
	if m == nil || m.Canonical(name) == name {
		return true, nil
	}
	rels, err := kb.Relations()
	if err != nil {
		return false, err
	}
	for _, r := range rels {
		if r.Name != name {
			continue
		}
		origin, ok, err := target.NormalizedOriginErr()
		if err != nil {
			return false, err
		}
		return ok && origin == r.Remote, nil
	}
	return true, nil
}
