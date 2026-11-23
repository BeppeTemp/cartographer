---
name: cartographer-acceptance
description: "Use when a Cartographer release candidate must be tried, a nightly finding must be reproduced, or a change needs a usage test before it is queued — builds the test matrix from the commits and the closed bugs and runs the lanes that need no private machine (static readiness, an all-in-one instance in an isolated HOME, a usage test with a headless agent) with `cartographer-acceptance-sandbox`, `cartographer-docs-auditor` and `cartographer-investigator`. Taking the candidate through the release (`rc-ok`, the release action) stays in `CONTRIBUTING.md` §Releases."
---

# cartographer-acceptance — try a candidate on the lanes that need no private machine

The release pipeline is `CONTRIBUTING.md` §Releases; this skill is the procedure of the trial
and the only thing that decides whether a candidate earns `rc-ok` on these lanes. It is run by a
maintainer's coordinator in its own agent session: no workflow calls a model.

## When

- a release candidate (`action: rc`): all three lanes;
- a nightly finding to reproduce: the lane that found it, on the binary that failed;
- a usage test before a change is queued: lane F, with the entries of the change.

## Rules

- **The binary under test is the published asset of the candidate**, its checksum verified: install it
  with `CARTOGRAPHER_INSTALL_TAG=v<next>-rc.<date>[.<n>]` and `CARTOGRAPHER_INSTALL_DIR=<scratch>/bin`
  (the default directory is the real one, and the installer repairs a running service), run under the
  tester's isolation: `HOME` and every `XDG_*_HOME` variable, cache included, under the scratch
  directory. Or fetch the asset and `sha256sums.txt` directly and verify the checksum
  (`CONTRIBUTING.md` §Releases, "Trying the candidate as native service and client"). A local build only when no asset exists, and the report says so.
- **Every finding becomes a `bug` issue** with placeholder names (`kb-a`, `example.com`, `user`), filed by
  the coordinator and never by the tester. The issue carries a `Found-by:` line (a value of
  `scripts/found-by.tsv`) and an `Introduced-by:` line (`CONTRIBUTING.md` §Issues).
- **A lane that is repeatable and isolated goes to a subagent**; two never share a machine, a scratch
  directory or a port.
- The repository is public: no machine, host, KB or user of a real setup in the matrix, the mandates or
  the issues (D259).

## The matrix

Built from `git log v<prev>..<candidate sha>`, the `bug` issues closed in the period and the breaking
changes. One row per entry: id, entry, type, lanes, how it is tried, verdict, issue. The type decides the
trial:

| Type | Trial |
|---|---|
| feature | beyond the happy path: limits, errors, repetition, the neighbouring features |
| fix | the scenario of the issue it closes, then the neighbouring ones |
| breaking change or upgrade note | in the upgrade from the stable release, with the message the user sees |
| process, CI, docs only | lane A only |

`scripts/ac-report.sh` lists the acceptance criteria landed since the last stable release, `manual` rows
first: they are rows of the matrix. Where the matrix is kept is the maintainer's choice and is outside the
repository.

## Lanes

- **Lane A, static readiness.** `cartographer-docs-auditor` over `v<prev>..<candidate sha>`, plus the
  checks of `docs/testing.md` §Before a release.
- **Lane B, all-in-one in an isolated HOME.** `cartographer-acceptance-sandbox` with the battery of `references/battery.md` (its rows with a path are run by CI: the train gate, and the nightly from the `nightly-acceptance` change on, until then `make e2e-nightly` by hand; the tester takes the `tester` rows and the matrix entries): the base battery on
  the first round of a version; on later rounds the smoke battery plus the entries the fixes touched. An
  optional second instance runs the adversarial battery, on other ports.
- **Lane F, usage test with a headless agent.** `make usage-test BIN=<binary>` for the seven fixed tasks
  (`docs/testing.md` §Usage test: the verdict comes from assertions on the transcript and the KB; exit 75
  is `NOT RUN`, not a failure of the binary); the tester with a usage mandate for the entries of the
  matrix. Both run in this session, never in a workflow.

## The mandate to the tester

Name: the tag, the sha and the `version` output the binary must print; the binary's absolute path; the
entries, each with id, what changed, issue and how to try it; the known findings not to report; the
scratch directory and the ports; the state to leave behind. Start it in the background and read only its
report.

## Verdict

Every entry has a verdict and every FAIL an issue. A cause that is uncertain goes to
`cartographer-investigator` before the issue is filed. A new round is a new candidate: lane A, the smoke
battery and the lanes of the entries the fixes touched.

Lanes on a maintainer's own machines (a real deployment, a managed laptop, real Windows) are outside this
repository.
