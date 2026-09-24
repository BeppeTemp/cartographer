---
topic: data-plane
---

# D269 — Reserve the services namespace and make concept moves namespace-aware

Closes #426.

**Decision.** `services/` is the KB-root service-descriptor namespace (a sibling of `data/`), and
`kb.ResolvePath` stays the only place that picks a concept's root. Every operation that turns a
concept ID into a physical path goes through it, and nothing joins an ID onto `DataRoot()` by hand:

- `CreateMapWithContract` refuses the exact map/journal name `services` with `okf.ErrInvalidPath`,
  before any filesystem mutation, for every `kind`.
- `concept_move` resolves both ends of every entry with `kb.LocateConcept` during preflight — the
  file actually removed, the expanded directory actually renamed, the destination file and
  directory checked for occupancy — and removes or renames exactly those paths.
- A filesystem failure after a destination was written (source removal, including a not-found the
  preflight did not predict, or an expanded directory rename) is an application error naming both
  IDs. There is no rollback, no success log entry and, because it is an error result, no commit.

**Why.** Two defects shared one cause, a second set of root-selection rules outside the resolver.
`map_create services` scaffolded `data/services/` and reported success, but every later read resolved
`services/…` to the KB root, so the map was listed without metadata and failed `index_get` and
`validate`. `concept_move` read and wrote through the concept API (namespace-aware) but removed the
source at `data/services/<x>.md`, ignored the not-found, and left the real `services/<x>.md` in
place: two copies, reported as a successful move. Ignoring not-found on removal is what turned the
path bug into a silent one, so it is now an error too.

**Alternatives rejected.**
- *Special-case `services/` in the `concept_move` handler.* Rejected: it is a third copy of the rule
  `ResolvePath` already holds, and the next namespace would be missed the same way.
- *Make `map_create services` create the map at the KB root.* Rejected: `services/` is not a map
  (no `_map.md`, no depth guard, no dossier stubbing) and mixing the two shapes in one folder makes
  every service tool and lint rule ambiguous.
- *Roll a move back on a late failure.* Rejected: undoing a
  rename or a delete can itself fail, and a claimed rollback that did not happen is worse than an
  explicit "target written, source still present" the operator can act on. `concept_batch` (D125)
  keeps its own rollback because it only ever writes files.
- *Broaden the reservation to `skills`, `agents`, `hooks`, `templates`.* Rejected: those are not
  concept namespaces, so a map with that name under `data/` is unambiguous. Only a name that
  `ResolvePath` reroutes collides.

**Consequences.**
- The reservation is the folder name only; the case-insensitive `type: Service` match (D158) and
  names such as `application-services` are unaffected.
- An existing `data/services/` left by the bug, or a duplicate left by a pre-D269 move, is neither
  read, repaired nor removed automatically: the operator cleans it up by hand. `map_delete services`
  stays available for that (only `map_create` reserves the name) and removes an empty scaffold.
- A move destination is also occupied when it is an unparsable file or an `<id>/` directory holding
  only assets: moving onto either would adopt or shadow what is there.
- An expanded concept moved into `services/` keeps its asset files, but the asset tools serve only
  `data/` concepts, so those assets are carried, not managed, until the concept moves back.
- The late-failure steps go through two package variables (`conceptMoveRemove`,
  `conceptMoveRename`) so tests can make them fail deterministically; production never reassigns them.
