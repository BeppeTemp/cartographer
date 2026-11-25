package mcpserver

import (
	"context"
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
func crossFindingVisible(ctx context.Context, k *kb.KB, f lint.Finding) bool {
	for _, n := range f.OtherKBs {
		if !crossReadable(ctx, k, n) {
			return false
		}
	}
	return true
}

// lintFilteredFor drops the findings the caller may not be told of because they
// name another KB, before anything is counted. The unfiltered call sites of a
// whole-KB tool (lint, gate_check, the doctor nudge) go through it.
func lintFilteredFor(ctx context.Context, k *kb.KB, findings []lint.Finding) []lint.Finding {
	for i, f := range findings {
		if len(f.OtherKBs) == 0 || crossFindingVisible(ctx, k, f) {
			continue
		}
		out := append([]lint.Finding(nil), findings[:i]...)
		for _, g := range findings[i+1:] {
			if len(g.OtherKBs) == 0 || crossFindingVisible(ctx, k, g) {
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
// caller's rights: here the cross-linked ones. An error gives the empty list
// (A's own graph failing fails the request that follows); it never has a
// fingerprint value of its own.
func crossNamedKBs(k *kb.KB) []string {
	names, err := k.CrossLinkedKBs()
	if err != nil {
		return nil
	}
	return names
}

// crossFingerprint extends the whole-KB cache key with the linked KBs the
// caller reads, so two callers with different rights never share a body whose
// counts depend on them. A KB with no cross link keeps the plain key.
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
