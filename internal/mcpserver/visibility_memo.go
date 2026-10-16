package mcpserver

import (
	"context"
	"sync"

	"github.com/BeppeTemp/cartographer/internal/kb"
)

// visibilityMemo remembers what Visible answered for one request on one KB.
// Deciding a narrowed principal's visibility reads a map descriptor and a
// concept's facets for every concept asked about, so a request that needs the
// visible set twice — search's ranking, then its related block (D781) — asks
// once. It lives in the request's context and dies with it: nothing is carried
// from one request to the next, so a policy or a file that changes is seen by
// the next call.
type visibilityMemo struct {
	k     *kb.KB
	check func(id string) bool
	mu    sync.Mutex
	seen  map[string]bool
}

type visibilityMemoKey struct{}

func newVisibilityMemo(k *kb.KB, check func(id string) bool) *visibilityMemo {
	return &visibilityMemo{k: k, check: check, seen: map[string]bool{}}
}

func (m *visibilityMemo) visible(id string) bool {
	m.mu.Lock()
	v, ok := m.seen[id]
	m.mu.Unlock()
	if ok {
		return v
	}
	v = m.check(id)
	m.mu.Lock()
	m.seen[id] = v
	m.mu.Unlock()
	return v
}

// withVisibilityMemo returns ctx carrying a memo of Visible for k, or ctx
// itself when it already carries one for k.
func withVisibilityMemo(ctx requestContext, k *kb.KB) requestContext {
	if memoOf(ctx, k) != nil {
		return ctx
	}
	return contextWithMemo(ctx, newVisibilityMemo(k, func(id string) bool { return Visible(ctx, k, id) }))
}

func contextWithMemo(ctx requestContext, m *visibilityMemo) requestContext {
	return context.WithValue(ctx, visibilityMemoKey{}, m)
}

func memoOf(ctx requestContext, k *kb.KB) *visibilityMemo {
	if m, _ := ctx.Value(visibilityMemoKey{}).(*visibilityMemo); m != nil && m.k == k {
		return m
	}
	return nil
}

// visiblePredicate is Visible(ctx, k, ·) as a predicate, answered from the
// request's memo when one is installed for k.
func visiblePredicate(ctx requestContext, k *kb.KB) func(id string) bool {
	if m := memoOf(ctx, k); m != nil {
		return m.visible
	}
	return func(id string) bool { return Visible(ctx, k, id) }
}
