package mcpserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strconv"

	"github.com/BeppeTemp/cartographer/internal/kb"
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
