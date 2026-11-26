package mcpserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/lint"
)

// crossState is what a caller may be told about a cross-KB link (D2398).
// `unresolved` is the one neutral value for a KB that is absent, not mounted,
// a server-qualified link, a KB the caller cannot read, a page the caller
// cannot read and a page missing in a KB the caller does not read whole: the
// answer is byte-identical, so existence is never revealed.
type crossState string

const (
	crossResolved   crossState = "resolved"
	crossMissing    crossState = "missing"
	crossUnresolved crossState = "unresolved"
	crossOutOfScope crossState = "out_of_scope"
)

// crossMounted finds the mounted KB a name points at (an alias is followed) and
// its current name. ok is false when k has no mounts or nothing is mounted
// under name.
func crossMounted(k *kb.KB, name string) (target *kb.KB, canonical string, ok bool) {
	m := k.Mounts()
	if m == nil {
		return nil, name, false
	}
	target, ok = m.Lookup(name)
	if !ok {
		return nil, name, false
	}
	return target, m.Canonical(name), true
}

// crossReadable is the one "readable" predicate for a finding that names
// another KB: a read grant on it (kbRosterVisible, by the current name, D772)
// and, when it is mounted, a whole-KB read of it. A relation view uses
// kbRosterVisible alone, on purpose; this one is never wider than that.
func crossReadable(ctx context.Context, k *kb.KB, name string) bool {
	target, canonical, mounted := crossMounted(k, name)
	if !kbRosterVisible(ctx, canonical) {
		return false
	}
	return !mounted || WholeVisible(ctx, target, false)
}

// crossFindingVisible is the threshold for a finding about other KBs: visible
// only when the caller reads every KB it names. A finding that names none
// follows the page, decided by the caller of this function.
//
// An orphan carries ClearedBy instead: the shared lint always reports it, and
// a caller who reads whole one of the KBs whose resolved edges would clear it
// is not shown it. A caller who reads none sees it whether or not the page it
// links, or that links it, exists there: the verdict is no existence oracle.
func crossFindingVisible(ctx context.Context, k *kb.KB, f lint.Finding) bool {
	for _, n := range f.OtherKBs {
		if !crossReadable(ctx, k, n) {
			return false
		}
	}
	for _, n := range f.ClearedBy {
		if crossReadable(ctx, k, n) {
			return false
		}
	}
	return true
}

// crossNamed says whether a finding's visibility depends on other KBs.
func crossNamed(f lint.Finding) bool { return len(f.OtherKBs) > 0 || len(f.ClearedBy) > 0 }

// lintFilteredFor drops the findings the caller may not be told of because they
// name another KB, before anything is counted. The unfiltered call sites of a
// whole-KB tool (lint, gate_check, the doctor nudge) go through it.
func lintFilteredFor(ctx context.Context, k *kb.KB, findings []lint.Finding) []lint.Finding {
	for i, f := range findings {
		if !crossNamed(f) || crossFindingVisible(ctx, k, f) {
			continue
		}
		out := append([]lint.Finding(nil), findings[:i]...)
		for _, g := range findings[i+1:] {
			if !crossNamed(g) || crossFindingVisible(ctx, k, g) {
				out = append(out, g)
			}
		}
		return out
	}
	return findings
}

// crossStateFor is the state of l for the caller of ctx. It looks at the
// target page only after the caller's right on the target KB is settled, and
// answers out_of_scope before it looks at the page at all.
func crossStateFor(ctx context.Context, k *kb.KB, l kb.CrossLink) crossState {
	if l.Server != "" || l.Malformed {
		return crossUnresolved
	}
	target, canonical, mounted := crossMounted(k, l.KB)
	if !mounted || !kbRosterVisible(ctx, canonical) {
		return crossUnresolved
	}
	if set := routedSetFrom(ctx); set != nil && !containsString(set, canonical) {
		return crossOutOfScope
	}
	t := k.CrossTarget(l)
	if !t.Mounted || t.Err != nil {
		return crossUnresolved // unknown reads as unresolved, never as missing
	}
	if t.Exists {
		if Visible(ctx, target, string(l.ID)) {
			return crossResolved
		}
		return crossUnresolved
	}
	if WholeVisible(ctx, target, false) {
		return crossMissing
	}
	return crossUnresolved
}

// bindHint says how to bind a KB the caller reads but the connection does not
// reach, worded by connection kind (D1101): the commands for a connection
// addressed to the operator, the generic wording otherwise.
func bindHint(ctx context.Context, canonical string) string {
	if !addressesOperator(ctx) {
		return fmt.Sprintf("kb %q is mounted on this server but is not part of this connection. %s", canonical, bindTextGeneric)
	}
	return fmt.Sprintf("kb %q is mounted on this server but not bound to this connection: bind it to this client "+
		"(`cartographer client bind <provider> %s`, or `cartographer workspace bind <provider> <path> --kb %s` "+
		"for a session in a bound folder), %s", canonical, canonical, canonical, bindSyncTail)
}

// crossNamedKBs are the KBs a cached whole-KB body of k depends on for the
// caller's rights: the ones k links out to and the ones with a page linking in
// (an orphan's ClearedBy names both). Sorted, without duplicates. An error of
// either part leaves the names the other gave (A's own graph failing fails the
// request that follows); it never has a fingerprint value of its own. The KBs
// the relations of k name or reach (relationNames) are in it too; a failing
// relation read leaves the link names only, and the stamp carries the failure
// (relationStampUnavailable).
func crossNamedKBs(k *kb.KB) []string {
	out, _ := k.CrossLinkedKBs()
	in, _ := k.CrossInboundKBs()
	// Trap (D214): the error of relationNames is swallowed on purpose. It is safe
	// because a failed relation read also makes relationStamp the fixed
	// relationStampUnavailable (the cached body is never reused as "no
	// relations") and appendRelationFindings adds nothing then. A path that
	// tolerates the failure must not reuse this key for a body that carries
	// relation data.
	rel, _ := relationNames(k)
	set := map[string]bool{}
	for _, n := range append(append(out, in...), rel...) {
		set[n] = true
	}
	if len(set) == 0 {
		return nil
	}
	names := make([]string, 0, len(set))
	for n := range set {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// crossFingerprint extends the whole-KB cache key with the linked KBs (out and
// in) the caller reads, so two callers with different rights never share a body whose
// counts depend on them. A KB with no cross link, in or out, keeps the plain key.
func crossFingerprint(ctx context.Context, k *kb.KB) string {
	named := crossNamedKBs(k)
	if len(named) == 0 {
		return ""
	}
	var read []string
	for _, n := range named {
		if crossReadable(ctx, k, n) {
			read = append(read, n)
		}
	}
	sort.Strings(read)
	return "+x:" + strings.Join(read, ",")
}

// crossStamp is the part of the lint-cache key that other KBs move: whether
// each page this KB links across exists there (broken_cross_kb_link), and the
// links other KBs hold toward this one (orphan). Without cross links both are
// empty and so is the stamp: a KB that uses none keeps its key. An unreadable
// KB is left out (its link is no finding), except in the inbound digest, whose
// "unknown:" value changes again when the KB recovers.
func crossStamp(k *kb.KB) string {
	inbound := k.CrossInboundDigest()
	links, err := k.CrossLinks()
	var out []string
	if err == nil {
		var uniq []kb.CrossLink
		for _, ls := range links {
			uniq = append(uniq, ls...)
		}
		for i, t := range k.CrossTargets(uniq) {
			if t.Mounted && t.Err == nil {
				out = append(out, fmt.Sprintf("%s\x00%s\x00%t", k.Mounts().Canonical(uniq[i].KB), uniq[i].ID, t.Exists))
			}
		}
	}
	if len(out) == 0 && inbound == "" {
		return ""
	}
	sort.Strings(out)
	h := sha256.New()
	fmt.Fprintf(h, "%s\n%s", inbound, strings.Join(out, "\n"))
	return hex.EncodeToString(h.Sum(nil))
}
