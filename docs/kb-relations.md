# KB relations: `depends_on`

A KB can declare, in its own `settings.yaml`, the KBs it depends on (the key is listed in `docs/data-plane.md` §The KB's own settings).

`depends_on`: the KBs this KB needs, a list of `{name, remote}` items. `name` is the mount name
of the other KB on the server; `remote` is its git remote, in the canonical `host/owner/name`
form or as a clone URL (ssh, https, `git://`; a `file://` URL is refused, a relation key needs a host), and is the **guard**: a mount name is not
identity (a rename, D772, or another server may map one name to another KB), so a KB mounted
under `name` whose origin differs is not the one meant. Remotes are compared through
`repoindex.NormalizeRemote`, the same normalization `paths.yaml` uses (`kb.NormalizeDeclaredRemote`):
case in owner and name is significant. Like `branch`, it is read **only from the `settings.yaml`
committed on the remote's default branch** (`KB.Relations`), so
a working branch cannot widen a binding. The default branch is the one the clone records in
`refs/remotes/origin/HEAD`, not the branch writes go to (which `branch` or `kbs[].branch` can set to a
working branch); a KB with a remote whose default branch is not known yet (before the first sync) has
no readable relations and the read is an error (`kb.ErrRelationsRefUnknown`; the findings stay out without a warning in the server log), never `HEAD`, which can be a working branch. A KB with
no remote reads `HEAD`, having no other branch; the working copy's value is parsed and shown only. The
read is memoized on the ref's commit and answered from memory for a few seconds, so a change on
the default branch is seen within that bound. A git failure while reading is an error, never
"no relations". The parser keeps the key with its well-formed items: a non-mapping item, a
missing or unknown field, a name that is not a KB name, a remote that is not a git remote or a
name declared twice leaves out that item alone, with one finding `depends_on[<i>]`; a value that
is not a list leaves out the key. `KB.NormalizedOriginErr` gives the KB's own origin in the same form (memoized like `Relations`); a git failure reading it is an error and is not kept, never "no remote".

## Resolution

`kb.ResolveRelations` is the one function every surface uses. A declared `{name, remote}`
resolves when a KB is mounted under `name` or under an old name `kb_aliases` still maps (a
mounted name beats an alias, D772), the caller may read it, and its origin
(`KB.NormalizedOriginErr`) equals the declared remote; the remote guard applies to a target found
through an alias as to any other. The view has `declared` and `unresolved` as written, and
`resolved` (direct edges) and `closure` (transitive, depth-first discovery order, the KB itself
excluded) by **current** mount name. An unresolved item has one reason: `not_mounted`,
`remote_mismatch` (the target's actual remote is never shown), `no_remote` (the mounted target has no usable origin: a folder mount, a `file://` URL or a host-less scp address),
`self` (the declaration names the KB itself by mount name or by its own origin), `cycle` or
`too_deep` (a walk would pass 64 KBs). A direct edge to `T` is a `cycle` when the KB is reachable
from `T`: in `a` to `b` to `a` the edge is a cycle in the view of each, and both closures are
empty; a cycle that does not pass through the KB marks none of its edges. A KB the caller cannot
read is never entered and answers exactly like one that is not mounted (`not_mounted`), so its own
relations never reach the closure. A git failure reading any entered KB's `settings.yaml`, or its
origin, is an error and no view, never a reason.
