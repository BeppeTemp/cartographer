---
topic: deployment-release
---

# D956 — Releases leave on a weekly train, gated on a tested release candidate

**Decision.** One release a week, merged by the `release-train` workflow on
Monday at 08:30 Europe/Rome. From Friday 00:00 to that merge nothing that adds
or breaks behaviour lands (`feat`, `perf`, `refactor`, `!:`, `BREAKING CHANGE`).
On Friday at 00:30 the workflow dispatches `preview.yml` for `v<next>-rc.<date>`;
a maintainer runs it on a real deployment and adds `rc-ok`. The train merges the
release PR only if the newest successful candidate build is of the head of `main`
and the label was added after that build, no blocked
commit landed since Friday, and the required checks passed; otherwise it
comments why and waits a week.

**Why.** A release used to be whatever release-please had accumulated: v0.25.0
carried 92 commits and 10 breaking changes and needed v0.25.1 the same day. A
fixed cadence with a freeze gives each release a scope someone decided and a
candidate someone ran. It costs latency: a fix merged on Friday ships ten days later
unless it is an out-of-train patch (a regression of the latest release or a
security fix), which the maintainer merges by hand and nothing here prevents.

**Alternatives rejected.**

- A manual rule only: it is what failed. The freeze and the label are checked by a workflow.
- A single UTC cron: GitHub cron knows no time zone, so the train would drift an
  hour twice a year. Two entries per slot; a run continues only when the entry that
  fired is the one for today's Europe/Rome offset.
- A guard on the clock: scheduled runs can start an hour late, which a minute or hour
  guard drops, and an hour guard can let both entries of a slot run.
- Accepting a label newer than the last commit on `main`: a fix merged after the
  Friday build and before the label would ship without having been run.
- Merging with `GITHUB_TOKEN`: the merge would not trigger the `push` workflows
  and release-please would never tag. The merge step uses `RELEASE_PLEASE_TOKEN`.

**Consequences.** Schedules run only from the default branch, so the train starts after this merges. The
workflow checks out nothing and runs only `gh`, so no third-party code holds the
PAT. `internal/repodocs/release_train_test.go` pins the crons, the offset guard and the
merge token. The skills do not merge a blocked PR during the freeze.
`plan-gate` joins the required checks of `main-protection`.
