package lint

import (
	"fmt"

	"github.com/BeppeTemp/cartographer/internal/kb"
)

// RelationFindings reports the declared dependencies of k that do not resolve
// against the KBs this server mounts, and the ones on a cycle. It resolves with
// the admin's eye (every mounted KB readable): who may be told of a finding is
// decided by Finding.OtherKBs, never here. The intrinsic malformed items already
// ride contract_malformed (settings.yaml). An error is the caller's to log: a
// lint response never claims the relations are fine because they could not be
// read.
//
// Not part of Run: the findings depend on the server's mounts and on other
// KBs, so they stay out of the cached run, of the write gate and of the history.
func RelationFindings(k *kb.KB) ([]Finding, error) {
	v, err := kb.ResolveRelations(kb.RelationEnv{Mounts: k.Mounts()}, k)
	if err != nil {
		return nil, err
	}
	current := func(name string) string {
		if m := k.Mounts(); m != nil {
			return m.Canonical(name)
		}
		return name
	}
	var out []Finding
	for _, u := range v.Unresolved {
		f := Finding{Path: kb.SettingsFile}
		switch u.Reason {
		case kb.RelationSelf:
			f.Check = "kb_relation_unresolved"
			f.Message = fmt.Sprintf("depends_on %q names this KB itself", u.Name)
		case kb.RelationTooDeep:
			f.Check = "kb_relation_unresolved"
			f.Message = fmt.Sprintf("depends_on %q leads to a dependency chain too long to follow", u.Name)
			f.OtherKBs = viaOr(u, current(u.Name))
		case kb.RelationCycle:
			f.Check = "kb_relation_cycle"
			f.Message = fmt.Sprintf("depends_on %q depends back on this KB: a dependency cycle", u.Name)
			f.OtherKBs = viaOr(u, current(u.Name))
		default:
			// not_mounted, remote_mismatch and no_remote read the same: a caller
			// learns only that no mounted KB matches the pair.
			f.Check = "kb_relation_unresolved"
			f.Message = fmt.Sprintf("depends_on %q (remote %s) matches no KB mounted on this server", u.Name, u.Remote)
			f.OtherKBs = []string{current(u.Name)}
		}
		out = append(out, newFinding(f.Check, f))
	}
	return out, nil
}

// viaOr is every KB the unresolved relation rests on (the cycle's path, the
// too-deep chain), the target at least. The finding says a path exists, so it
// is shown only to a caller who reads all of them: the target alone would
// reveal, to a caller who reads it, that an unreadable KB sits behind it.
func viaOr(u kb.RelationUnresolved, target string) []string {
	if len(u.Via) == 0 {
		return []string{target}
	}
	return append([]string(nil), u.Via...)
}
