package mcpserver

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// crossRevisionSuffix is the part of a KB's Atlas revision that follows the
// KBs it is cross-linked with (links out of it and links into it), so an open
// page of A refreshes when a linked KB B changes. Only a KB the caller reads
// whole counts (crossReadable): /kbs/{kb}/revision is whole-KB only, so a token
// that moved for a KB the caller cannot poll would tell them it changed. The
// suffix is a digest of generations, never a name or a state; it is empty when
// A has no such KB, so the token of a KB with no cross link is unchanged. A
// caller who reads B only in part is not refreshed by B (stated limit).
//
// The reads trust a validation younger than uiRevisionMaxAge, like the generation: a poll costs no extra walk. One KB's snapshot is read at a time, never under another KB's lock.
func crossRevisionSuffix(ctx context.Context, k *kb.KB) string {
	m := k.Mounts()
	if m == nil {
		return ""
	}
	set := map[string]bool{}
	if out, err := k.CrossLinkedKBsWithin(uiRevisionMaxAge); err == nil {
		for _, n := range out {
			set[n] = true
		}
	}
	for _, n := range k.CrossLinkedFromWithin(uiRevisionMaxAge) {
		set[n] = true
	}
	names := make([]string, 0, len(set))
	for n := range set {
		if crossReadable(ctx, k, n) {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	h := sha256.New()
	used := 0
	for _, n := range names {
		x, ok := m.Lookup(n)
		if !ok {
			continue
		}
		gen, err := x.GraphGenerationWithin(uiRevisionMaxAge)
		if err != nil {
			continue
		}
		h.Write([]byte(n + "\x00" + strconv.FormatUint(gen, 10) + "\x00"))
		used++
	}
	if used == 0 {
		return ""
	}
	return "-x" + hex.EncodeToString(h.Sum(nil))[:12]
}

// kbMapEdgeBudget bounds the links one GET /kb-map examines; a var so a test
// can lower it.
var kbMapEdgeBudget = 50000

// kbMapMaxDepth caps the layer computation; a cycle cannot occur in Resolved,
// the cap and the visited set are belt and braces.
const kbMapMaxDepth = 64

type uiKBMapNode struct {
	Name          string `json:"name"`
	Ready         bool   `json:"ready"`
	ConceptsTotal *int   `json:"concepts_total,omitempty"`
	// Layer is a layout hint over the visible relations alone: 0 for a node
	// with no resolved dependency among the nodes, else 1 + the largest layer
	// of its dependencies.
	Layer                int  `json:"layer"`
	RelationsUnavailable bool `json:"relations_unavailable,omitempty"`
}

// uiKBMapRelation says From (a client) depends on To (a dependency).
type uiKBMapRelation struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// uiKBMapLink counts the cross-KB links of From that resolve to To for the
// caller: only links whose both ends it can read.
type uiKBMapLink struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Count int    `json:"count"`
}

// uiKBMap is the body of GET /kb-map.
type uiKBMap struct {
	Nodes     []uiKBMapNode     `json:"nodes"`
	Relations []uiKBMapRelation `json:"relations"`
	Links     []uiKBMapLink     `json:"links"`
	// Truncated is true when the per-request link budget ran out: the counts
	// are then a lower bound.
	Truncated bool `json:"truncated"`
}

// uiKBMap answers GET /kb-map: the KBs the caller sees (the rows of /kbs), the
// resolved declared relations between them and the weighted cross-KB links. It
// is computed per request and never goes through serveUICached: the answer
// depends on the caller's rights on every KB, which a per-KB cache key cannot
// express. The ETag only saves the transfer.
func (m *MultiKBServer) uiKBMap(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	visible := m.visibleKBs(ctx)
	body := uiKBMap{Nodes: []uiKBMapNode{}, Relations: []uiKBMapRelation{}, Links: []uiKBMapLink{}}
	isNode := map[string]bool{}
	byName := map[string]*kb.KB{}
	for _, v := range visible {
		isNode[v.Info.Name] = true
		byName[v.Info.Name] = v.Srv.kbRef
	}

	deps := map[string][]string{}
	for _, v := range visible {
		name, k := v.Info.Name, v.Srv.kbRef
		node := uiKBMapNode{Name: name, Ready: v.Info.Status == "normal"}
		if node.Ready {
			// A partial reader gets no relation edge and no flag: the KB looks
			// like one that declares none (settings.yaml is a whole-KB file).
			if WholeVisible(ctx, k, false) {
				if rv, err := relationViewFor(ctx, k); err != nil {
					node.RelationsUnavailable = true
				} else {
					for _, to := range rv.Resolved {
						if isNode[to] && to != name {
							deps[name] = append(deps[name], to)
						}
					}
				}
			}
			if n, ok := uiVisibleTotal(ctx, k); ok {
				node.ConceptsTotal = &n
			}
		}
		body.Nodes = append(body.Nodes, node)
	}
	for from, tos := range deps {
		sort.Strings(tos)
		for _, to := range tos {
			body.Relations = append(body.Relations, uiKBMapRelation{From: from, To: to})
		}
	}
	sort.Slice(body.Relations, func(i, j int) bool {
		a, b := body.Relations[i], body.Relations[j]
		return a.From < b.From || (a.From == b.From && a.To < b.To)
	})
	layers := map[string]int{}
	for i := range body.Nodes {
		body.Nodes[i].Layer = kbMapLayer(body.Nodes[i].Name, deps, layers, map[string]bool{}, 0)
	}

	body.Links, body.Truncated = kbMapLinks(ctx, visible, isNode)

	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(body); err != nil {
		writeUIInternal(w, "kb-map", err)
		return
	}
	sum := sha256.Sum256(buf.Bytes())
	etag := `W/"` + hex.EncodeToString(sum[:12]) + `"`
	writeUIHeaders(w)
	w.Header().Set("Cache-Control", "private, no-cache")
	w.Header().Set("ETag", etag)
	if etagMatches(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(buf.Bytes())
}

// kbMapLayer is 0 for a node without dependencies, else 1 + the largest layer
// of its dependencies.
func kbMapLayer(name string, deps map[string][]string, memo map[string]int, onPath map[string]bool, depth int) int {
	if n, ok := memo[name]; ok {
		return n
	}
	if onPath[name] || depth >= kbMapMaxDepth {
		return 0
	}
	onPath[name] = true
	layer := 0
	for _, d := range deps[name] {
		if l := kbMapLayer(d, deps, memo, onPath, depth+1) + 1; l > layer {
			layer = l
		}
	}
	delete(onPath, name)
	memo[name] = layer
	return layer
}

// kbMapLinks counts, per ordered pair of nodes, the links crossStateFor calls
// resolved for the caller. crossStateFor is the one judge of "may be counted":
// the route adds only the node-set check. A source page the caller cannot see
// is skipped before it counts against the budget.
func kbMapLinks(ctx context.Context, visible []uiVisibleKB, isNode map[string]bool) ([]uiKBMapLink, bool) {
	type pair struct{ from, to string }
	counts := map[pair]int{}
	examined, truncated := 0, false
scan:
	for _, v := range visible {
		a := v.Srv.kbRef
		if v.Info.Status != "normal" || a.Mounts() == nil {
			continue
		}
		// A poll trusts a recent validation, as the revision does.
		cross, err := a.CrossLinksWithin(uiRevisionMaxAge)
		if err != nil {
			continue // unknown: this KB contributes no link
		}
		ids := make([]string, 0, len(cross))
		for id := range cross {
			ids = append(ids, string(id))
		}
		sort.Strings(ids)
		whole := WholeVisible(ctx, a, false)
		self := a.MountName()
		for _, id := range ids {
			if !whole && !Visible(ctx, a, id) {
				continue
			}
			for _, l := range cross[okf.ConceptID(id)] {
				if examined >= kbMapEdgeBudget {
					truncated = true
					break scan
				}
				examined++
				if l.Malformed || l.Server != "" {
					continue
				}
				to := a.Mounts().Canonical(l.KB)
				if !isNode[to] || to == v.Info.Name || to == self {
					continue
				}
				if crossStateFor(ctx, a, l) == crossResolved {
					counts[pair{v.Info.Name, to}]++
				}
			}
		}
	}
	out := make([]uiKBMapLink, 0, len(counts))
	for p, n := range counts {
		out = append(out, uiKBMapLink{From: p.from, To: p.to, Count: n})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].From < out[j].From || (out[i].From == out[j].From && out[i].To < out[j].To)
	})
	return out, truncated
}
