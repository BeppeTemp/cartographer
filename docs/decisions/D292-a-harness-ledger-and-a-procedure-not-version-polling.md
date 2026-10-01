---
topic: project-governance
---

# D292 — A harness ledger and a re-alignment procedure, not automated version polling

**Decision.** What Cartographer believes about each agent client lives in one
ledger page, `docs/harnesses.md` (one section per provider: version last aligned
with, sources, dependent matrix cells, watch items, probe notes), and is
refreshed by a repo-local skill, `harness-watch`, that reads the changelog since
the ledger version, classifies what touches a Cartographer surface, verifies
every candidate cell change with a real-client probe, and reports. A test
(`TestHarnessLedgerCoversEveryProvider`) fails when a provider has no ledger
section, or a section names no provider.

**Why.** The clients ship weekly and none announces anything to Cartographer, so
"has anything changed?" was re-derived from scratch each time, from scattered
decision files and code comments. Three things make a mechanical check the wrong
tool: changelogs are prose, so a poller would only report "a new version exists";
a cell flips on behaviour, not on a version string (Kiro 2.21.3 with and without
`--v3`, hooks documented but not shipped in D140 and D195, firing only in the V3
interactive UI on 2.26.1); and the probes need an installed, logged-in client,
which CI does not have.

**Alternatives rejected.**

- *A CI job polling release feeds*: reports a version delta, which is the least
  useful part, and needs network and per-vendor parsing that rots.
- *Probes in CI against real clients*: needs installs and logins on a public
  repository's runners.
- *Recording the verification only in code comments and decisions (status quo)*:
  accurate, but unqueryable per client and never prompts anyone to look.
- *A ledger without a test*: a client added to the registry would silently have no
  entry, which is the failure the ledger exists to prevent.

**Consequences.** Adding a client requires a ledger section (the gate says so).
A re-audit is one docs-only PR that bumps **Aligned with**; a confirmed cell
change goes through a plan issue, never from inside the skill. The skill is
read-only on the machine apart from probe sentinels in a temp directory, and
never installs or upgrades a client without the operator's say-so. The ledger and
the skill's outputs carry placeholder names only, because the repository is
public (D259). Nothing ships in the binary.
