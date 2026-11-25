package mcpserver

import (
	"context"

	"github.com/BeppeTemp/cartographer/internal/kb"
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
