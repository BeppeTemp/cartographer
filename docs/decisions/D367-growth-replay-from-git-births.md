---
topic: control-plane
---

# D367 — The growth replay reads each concept's birth from git

**Decision.** `GET /api/ui/v1/kbs/{kb}/births` returns, for each visible
concept, the time of the commit that first added its file, walking the whole
history oldest first: an add sets the birth, a rename carries it to the new
id, a delete forgets it. The Atlas replays the KB's growth in that order;
concepts born in the same commit come in along their links (first those
touching what is already shown, then outward), so an import of dozens of
pages still grows as a network. The replay lasts 8 to 20 seconds whatever the
size of the KB.

**Why.** Git is the only record of when a page appeared that every KB has; a
frontmatter date (`timestamp`, `created`) is optional, edited by hand and
often the last update, not the first. Following renames keeps a concept moved
by `concept_move` or a map reorganisation at its real age instead of the day
of the move.

**Alternatives rejected.**
- Order by frontmatter dates: missing on many pages and not a birth.
- Replay commit by commit: a 5,000-commit KB would last minutes, and most
  commits change no concept set.
- Compute the births in the browser from `changes_since`: it is windowed and
  capped at 500 concepts.
- Cache the births server-side: it is asked once per replay, a click away from
  anyone, and the git log walk is the same one `changes_since` does.

**Consequences.** A concept not committed yet has no birth and comes last. A
history rewritten by a squash shows everything born at once, along its links.
Visibility is filtered per caller like every UI route.
