---
topic: project-governance
---

# D2320 — A change to the CLI, a client or a message is tried on its binary before it is queued

**Decision.** `scripts/pr-triage.sh` prints `Usage-test: required` for a maintainer's change whose production
paths are on the usage list (`cmd/cartographer/`, `internal/configurator/`, `internal/provisioning/`,
`internal/skillbundle/bundled/`, `install.sh`, `install.ps1`), and `make ready` refuses it without
`USAGE="pass, <n> entries"` or `USAGE="waived: <reason>"`, which becomes the `Usage-test:` trailer next to
`Review:`. The trial is run by the coordinator's own agent session on the worktree's built binary; the scripted
harness `make usage-test` is a headless agent whose verdict comes from assertions, and no workflow runs it.

**Why.** A release candidate's acceptance run found, all at once and with the freeze open, the defects that no
earlier phase looks for: commands that disagree, incoherent messages, a client left half written. Running the
change where such defects occur, before it is queued, moves them to the day they are introduced. The record is in
the commit, so the escape report can compare tested, waived and exempt changes. It costs a model session per
change on the list, which is why the list is by path and short.

**Alternatives rejected.** Advisory only: a line nobody must answer is skipped when the queue is busy. Blocking
with no waiver: a change whose effect cannot be tried (an installer text, a dependency bump in a listed path) would
be stuck, or its author would invent a pass. A tester agent driving another agent for the fixed tasks: the verdict
would depend on a second model and a failure could not be reproduced, so the harness asserts on the transcript and
the KB. The list as a `profiles.tsv` column: the requirement is by path and cuts across the profiles (a `fix` and a
`feat` under `cmd/cartographer/` both owe it), and a column would change the seven-field format five scripts read.
`internal/mcpserver/` on the list: most changes touch that package and few change what an agent reads.

**Consequences.** `USAGE_PATHS` in `scripts/pr-triage.sh` is the one list; `scripts/ready.sh` reads the triage it
already runs and keeps no copy. The lander does not check the trailer: the refusal is at `make ready`, and a
waiver is as visible as a pass. No workflow holds a model key or starts an agent, and `TestNoWorkflowCallsTheUsageTest`
fails when one names the harness. A defect found later in a change that carries a waiver is the escape data of
`Found-by:`.
