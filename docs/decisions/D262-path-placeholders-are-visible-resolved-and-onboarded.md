---
topic: sync-provisioning
---

# D262 — Path placeholders are visible, fully resolved and onboarded

**Decision.** The server lists the `{{repo:…}}`/`{{path:…}}` keys a KB cites in the
`sync_pull` response (`placeholders`), and the client resolves every one of them —
not only the ones inside the artifacts it happens to rewrite — once per sync,
through a single resolver that walks the search roots at most once. The
instructions block always carries a fixed paragraph explaining placeholders,
`cartographer resolve` and `cartographer paths set`, with the "Local paths" table
below it; its hash is recorded in the lock so a table change rewrites the block.
Unresolved keys are reported in one aggregated warning, recorded in the lock
(`unresolved_placeholders`), surfaced by `status`, and fixed through the new
`cartographer paths` command or the placeholder step of an interactive `connect`,
both of which write only `paths:`.

**Why.** Path portability (D75) worked mechanically and was invisible in practice:
the table and its pointer appeared only when an *artifact* placeholder resolved,
concept bodies never reached expansion, and a failed resolution was a per-sync
warning nobody could act on — the client config had no `paths:` section and no
step ever asked for one. On a real client a KB with dozens of placeholder-citing
concepts produced no table at all and agents guessed paths. The cost is an
extra, unsigned field in `sync_pull`, four additive lock fields and one more
rewrite trigger for the instructions block.

**Alternatives rejected.**
- *Expand placeholders on the server, or in `concept_read`.* Breaks the D75
  invariant: the content hash is over the raw text, and the server does not know
  any client's filesystem.
- *Put the key list inside the signed manifest or the revision.* A concept
  starting to cite a key would look like a catalogue change and re-sign every
  artifact; it is derived data, treated like `Manifest.Issues`.
- *Walk the KB on every pull to find the keys.* The graph cache (D241) already
  parses every concept; the keys are one more cached facet, projected with the
  caller's visibility so a key only a hidden concept cites is never listed.
- *Keep one repoindex scan per miss.* With twenty unresolved keys a sync walked
  the roots twenty times, and a key a fresh index lacks does not appear on the
  second walk of the same tree.
- *A new config key for repo mappings.* `paths:` is already consulted first for
  `repo:` keys, so one map serves both kinds; keys are stored without the prefix
  because that is how resolution looks them up.
- *A warning per unresolved occurrence.* That is how warnings get ignored (D162);
  one line per sync names each key once, with its fix.

**Consequences.** `internal/mcpserver` still never sets `ExpandPlaceholders`; the
placeholder syntax lives in `internal/okf` and is shared by the server's lister
and the client's expander, so the two cannot disagree on what a key is. The
client also scans the artifacts it holds for keys, so against an older server
that lists nothing the table is still complete for artifacts. An unresolved key
never blocks a sync or a connect (D75 WP3 stands) and never changes `status`'s
state or exit code. `paths:` is written only on the operator's explicit answer,
never by `sync`. A lockfile written before this has no placeholder fields, which
means "nothing recorded"; its missing section hash rewrites the block once, which
is what puts the paragraph into existing installations.
