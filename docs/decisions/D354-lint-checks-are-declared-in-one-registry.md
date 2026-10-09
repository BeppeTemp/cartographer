---
topic: data-plane
---

# D354 — Lint checks are declared in one registry, and every table about them is derived

**Decision.** `internal/lint/registry.go` holds one `CheckSpec` per check: name, default severity, level (`concept`, `map`, `graph`, `artifact`, `kb`), who may accept it, the fix kinds it carries, and the flags `AutoRepairSafe`, `CrossConcept`, `OnWrite`, `Judgement` (plus `AlsoArtifact`, `NeverSuppress`, `WholeGraph`, `Panel`, which keep the old tables expressible). `perConceptChecks`, `mapOnlyIgnorable`, `artifactChecks`, `artifactAcceptable`, `WholeGraphChecks`, `FixableChecks`, `lintJudgementChecks`, the non-suppressible set in `suppressed`, the artifact-repair set and the Artifacts panel list are computed from it. Every finding is built by `newFinding(check, Finding{…})`, which fills `Severity` from the spec (an unregistered name panics under test, and is `info` with one stderr line in production). The page-level checks of `runChecks` moved into `conceptFindings`, which `Run` and `CheckConcept` both call; `CheckConcept` keeps only the `OnWrite` ones. The catalogue in `docs/data-plane.md` is generated from the registry.

**Why.** A new check meant editing up to seven tables that had to agree by hand, and the sibling plans add about twenty. Nothing asserted that an emitted name was known to every table, or that the write path and a whole-KB run agree about a page. One struct makes each table a projection, and the constructor makes severity a property of the check, not of the emission site.

**Alternatives rejected.**
- A plugin interface per check (`Evaluate(ctx)`): graph, map and artifact checks share inputs computed once per run (graph, walk, artifact scan), so each plugin would recompute them or take the same shared context anyway. Only the page-level checks, which read nothing but one page, are evaluated through the shared function.
- Deriving `DefaultAutoRepair` from the registry: it is a deliberate, reviewed list (D323), so it stays a literal in `config` and a test asserts each entry is `AutoRepairSafe`.
- Keeping `island` in `mapOnlyIgnorable`: it is already `Accept: concept` (D306), where `CheckAcceptability` found it first, so the duplicate entry changed nothing and is dropped.

**Consequences.** Adding a check is three edits: the spec, a `newFinding` call, a regenerated catalogue (`CONTRIBUTING.md` §Adding a lint check). `OnWrite` on a `graph` or `map` check means `ScopedCheck` evaluates it; on a `concept` check, `conceptFindings` does, and `TestCheckConceptAgreesWithRun` pins that the write path equals the `OnWrite` subset of `Run`. A new `LevelConcept` check that needs whole-KB inputs must not be `OnWrite`. `TestNoFindingLiteralOutsideRegistry` fails any `Finding{…}` literal that sets `Check`. No output changes: `TestRegistry_DerivedTablesMatchLegacy` holds the old tables as a golden. The planned unattended repair stages build on `AutoRepairSafe` and `CrossConcept`.
