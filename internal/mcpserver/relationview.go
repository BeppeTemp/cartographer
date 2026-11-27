package mcpserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/lint"
	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
)

// relationViewFor is the one function every surface calls to show a KB's
// dependencies to a caller. A target the caller may not read at all
// (kbRosterVisible, D1096: any read grant) answers exactly like one that is not
// mounted, so the view cannot be used to probe for a KB. It is not
// uiKBVisible, which decides whether a KB row is listed, not whether a name may
// be spoken. An error is returned, never an empty view.
func relationViewFor(ctx context.Context, k *kb.KB) (kb.RelationView, error) {
	return kb.ResolveRelations(kb.RelationEnv{
		Mounts:   k.Mounts(),
		Readable: func(n string) bool { return kbRosterVisible(ctx, n) },
	}, k)
}

// Reasons a relations read failed, as the Atlas routes say them next to
// relations_unavailable. Fixed words: the cause itself stays in the server log.
const (
	// relationsReasonNoOriginHead: a remote's default branch is not known yet
	// (a KB created with a remote learns it at its first sync, or one of the
	// KBs it depends on does not know it yet): sync first.
	relationsReasonNoOriginHead = "no_origin_head"
	// relationsReasonReadFailed: git could not answer, any other cause.
	relationsReasonReadFailed = "read_failed"
)

// relationsReasonFor names why a relations read failed (err is never nil).
func relationsReasonFor(err error) string {
	if errors.Is(err, kb.ErrRelationsRefUnknown) {
		return relationsReasonNoOriginHead
	}
	return relationsReasonReadFailed
}

// relationsUnavailableIssue is the one fixed line of sync_pull's issues when
// the relations could not be read; the cause stays in the server log.
const relationsUnavailableIssue = "relations unavailable: the KB's dependencies could not be read (details in the server log)"

// pulledRelations is sync_pull's `relations` field: the view in its wire shape.
func pulledRelations(ctx context.Context, k *kb.KB) (contract.Relations, error) {
	v, err := relationViewFor(ctx, k)
	if err != nil {
		return contract.Relations{}, err
	}
	out := contract.Relations{
		Declared:   make([]contract.Relation, 0, len(v.Declared)),
		Resolved:   append([]string{}, v.Resolved...),
		Closure:    append([]string{}, v.Closure...),
		Unresolved: make([]contract.RelationUnresolved, 0, len(v.Unresolved)),
	}
	for _, d := range v.Declared {
		out.Declared = append(out.Declared, contract.Relation{Name: d.Name, Remote: d.Remote})
	}
	for _, u := range v.Unresolved {
		out.Unresolved = append(out.Unresolved, contract.RelationUnresolved{Name: u.Name, Remote: u.Remote, Reason: u.Reason})
	}
	return out, nil
}

// relationStampUnavailable is the stamp of a KB whose relations could not be
// read. It is never "", which means "declares none", so the cached body of a
// failed read is recomputed when the read works again and is never taken for a
// "no relations" body.
const relationStampUnavailable = "rel-unavailable"

// relationNames are the KBs the relations of k make a cached whole-KB body
// depend on: every KB the admin-eye resolution looked at, plus the current
// name of each declared target (mounted or not). Sorted, without duplicates.
func relationNames(k *kb.KB) ([]string, error) {
	v, err := kb.ResolveRelations(kb.RelationEnv{Mounts: k.Mounts()}, k)
	if err != nil {
		return nil, err
	}
	if len(v.Declared) == 0 {
		return nil, nil
	}
	set := map[string]bool{}
	for _, n := range v.Visited() {
		set[n] = true
	}
	for _, d := range v.Declared {
		n := d.Name
		if m := k.Mounts(); m != nil {
			n = m.Canonical(n)
		}
		set[n] = true
	}
	names := make([]string, 0, len(set))
	for n := range set {
		names = append(names, n)
	}
	sort.Strings(names)
	return names, nil
}

// relationStamp is the part of the whole-KB stamp that the relations of k
// move: a digest of the admin-eye view and the names it looked at. It is empty
// for a KB that declares none, so such a KB keeps its stamp, and
// relationStampUnavailable when the resolution fails. It reads memory only
// within the relations age (kb.Relations, kb.NormalizedOrigin): no git call
// per request.
func relationStamp(k *kb.KB) string {
	v, err := kb.ResolveRelations(kb.RelationEnv{Mounts: k.Mounts()}, k)
	if err != nil {
		return relationStampUnavailable
	}
	if len(v.Declared) == 0 {
		return ""
	}
	b, err := json.Marshal(struct {
		View    kb.RelationView
		Visited []string
	}{v, v.Visited()})
	if err != nil {
		return relationStampUnavailable
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// appendRelationFindings adds the relation findings (kb_relation_unresolved,
// kb_relation_cycle) to a whole-KB lint result, after the cached lint.Run and
// before the caller filter: they depend on the server's mounts and on other
// KBs, so they are never part of what lint.Run caches. It is called by the
// whole-KB read surfaces only (lint, kb_status, the Atlas overview and lint
// route), never by gate_check, commit_gate, the doctor nudge, the lint history
// or the repair, so a relation finding gates nothing and enters no history.
// Who may be told of one is Finding.OtherKBs and crossFindingVisible. The
// settings.yaml finding is a whole-KB one: a caller who does not read the KB
// whole gets none. A read failure leaves them out and is logged, never read
// as "all resolved". The input slice is never modified (it may be the cache's).
func appendRelationFindings(ctx context.Context, k *kb.KB, findings []lint.Finding) []lint.Finding {
	if !WholeVisible(ctx, k, false) {
		return findings
	}
	rel, err := lint.RelationFindings(k)
	if err != nil {
		if errors.Is(err, kb.ErrRelationsRefUnknown) {
			// A KB just created with a remote, before its first sync: nothing is
			// wrong, so no warning; the findings appear once the ref is known.
			return findings
		}
		fmt.Fprintf(os.Stderr, "cartographer: warn: lint: relation findings left out: %v\n", err)
		return findings
	}
	if len(rel) == 0 {
		return findings
	}
	return append(append(make([]lint.Finding, 0, len(findings)+len(rel)), findings...), rel...)
}
