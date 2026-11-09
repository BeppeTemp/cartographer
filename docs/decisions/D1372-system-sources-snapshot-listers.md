---
topic: architecture
---

# D1372 — System sources: snapshot listers, worker-side diff

**Decision.** A live read-only system (a cluster, a cloud account, a metrics instance) is a Source with
`source_shape: system`, served by an exec recipe of `shape: system` that prints the current state grouped. The worker,
not the lister, hashes each group (first 16 hex of sha256 over the content, CRLF folded), diffs it against the state it
kept as the asset `system-state.jsonl` of the Source (whose digest is `feed_cursor`) and creates one `IngestUnit` per
changed group, with the snapshot and a line diff in the unit body. Before every listing a mandatory preflight makes the
lister prove its identity is read-only; the recipe's shape, not the Source's label, decides whether it applies.

**Why.** Nothing in a live system has a revision and its raw output is far too large to read (one measured cluster:
about 1.1M tokens raw, about 52k normalized, about 8 KB per namespace), so the only cheap signal is a per-group hash:
an unchanged system costs no tokens (design D19, D20, D21 of #1201; D1262). The lister is operator code and cannot be
trusted to report change, so the worker computes it. The preflight guards against a misconfigured identity (the
measured kubeconfig was cluster-admin); it is not a security boundary, the script can lie, and the boundary is a
dedicated read-only identity.

**Alternatives rejected.**
- *Trust the lister's revision and change*: a buggy or hostile script would hide or invent changes (design D20).
- *State in frontmatter*: a cluster's per-group state does not fit a Source's frontmatter and would put it in every
  `concept_list`; an asset keeps it out of search (the extension is outside the indexed list, D277) and `feed_cursor`
  holds its digest so the CAS lease of D1262 still guards it.
- *One Source per group*: `source_register` dedups by locator and would turn a cluster into hundreds of ledger entries.
- *Snapshot as an asset of the unit (D1262 WP7)*: four commits per group and a staging state to resume, and the write
  gate scans a body for credentials but not an asset (D1202): the body is the only net behind a lister that failed its
  "never a secret" duty.
- *Cursor advances only when every group is handled (D1262 decision 8)*: a feed cursor is opaque, so partial progress is
  not expressible; a system's cursor is the digest of a per-group state, where it is. With all-or-nothing, one
  permanently refused group would stop the baseline from ever being saved. The state is the old one with the *handled*
  groups applied.
- *Unit ID from the revision alone, or from the revision and the day*: a system revisits earlier states (A, B, A),
  even within one day, and the second A must create a unit, so the ID hashes how many units the group already has;
  a lookup of the group's existing units, not an ID collision, decides whether the group is handled (a crash re-run or
  a rebuilt state creates only what is missing), and the create-only batch settles two pollers that see the same count.

**Consequences.**
- Write order is units, then the state file, then (first time) the body link, then the cursor patch; every crash
  converges, and a state whose digest differs from `feed_cursor` is untrusted and rebuilt, not repaired (lost diffs of a
  crash window are accepted; snapshots are not).
- An empty listing is `empty_listing`, never "all removed". A vanished group yields an "observed absent since <date>"
  mark and a gap, never a delete; a statement of intent that disagrees with an observation is never rewritten, a
  Contradiction involving the claim and the system Source is opened instead (D273; gap fallback of #1318).
- The poll's model never reads a snapshot; the executor of a system unit has no tool on the observed system and no
  destructive or structural tool (`source_fetch`, archive, merge, move, supersede, assets, maps, `source_register`).
- A new unit marks the group's still-`open` units `skipped` with `superseded_by`, so an old snapshot is never executed
  after a newer one; an `in-progress` unit is left alone.
- Four optional unit fields (`observed_group`, `observed_revision`, `observed_change`, `observed_at`) and four
  `feed_error` codes (`identity_not_read_only`, `empty_listing`, `state_error`, `groups_failed`) are contracts.
- Reference listers live in `examples/`, not in the binary: no new Go dependency, and the Kubernetes one is tested with
  fixtures, no cluster (D1262 is the exec protocol they speak; D1072 for the unit template pattern).
