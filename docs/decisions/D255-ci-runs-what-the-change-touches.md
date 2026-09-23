---
topic: deployment-release
---

# D255 — CI runs what the change touches

**Decision.** On a PR, a `changes` job reads the PR's file list and runs
`test-windows` only for Go code, what it embeds, the Windows installer and the
CI itself, and `web` only for the frontend and the Go server the browser suite
drives. The release-please PR runs both whatever it touches. A push to `main`
runs `test` only. A new push to a PR cancels the run in flight. The Windows job
turns off Defender's real-time scanning before anything else.

**Why.** Every PR waited on `test-windows`, about six minutes against two for
`test`, and every merge ran all three jobs again on `main`. On Windows the
git-backed packages (`internal/mcpserver`, `internal/kb`) ran 15-20x slower
than on Linux, mostly from git process creation; turning off Defender's
scanning of every temp file took about a fifth off the Windows gate. Since the
rest cannot be tuned away, the remaining lever is not running it.
The cost: a PR is no longer tested on Windows unless it touches a Windows-relevant
path, and `main` is no longer re-tested on Windows or in the browser after each
merge. The release PR is the one full pass over what the merged PRs add up to.

**Alternatives rejected.**
- `paths:` on the workflow: a required check that never reports blocks the PR.
  A job skipped by `if:` reports as passed.
- A third-party path-filter action: one more dependency for two regexes over
  `gh api` output.
- Skipping when `changes` fails: a skipped required check passes, so a broken
  filter would silently drop Windows coverage. The jobs fail open instead.
- Running only `go test` on Windows instead of `make gate`: small gain, and the
  gate has to stay one command on both legs.

**Consequences.** A new path that Windows or the browser suite depends on has
to be added to the regexes in `ci.yml`'s `changes` job. The branch-name test
depends on release-please's `release-please--` head-ref prefix.
