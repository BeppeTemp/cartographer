---
topic: project-governance
---

# D785 — CI splits its slow legs across runners

**Decision.** The Windows tests run in four shards on separate runners
(`scripts/test-shard.sh`), with `install.ps1` on a fifth, and the browser suite
runs in two legs: the `@video`-tagged export test, which also carries the
frontend unit tests and bundle checks, and the rest. A small job named
`test-windows` and one named `web` collect the legs and are the required
checks. On `main` the concurrency group is the commit, so every merge gets its
own run.

**Why.** One Windows job ran `make gate` for 12 minutes on average and up to 19,
almost all of it waiting on `internal/mcpserver` (about 13 minutes, against one
on ubuntu: git process creation is that much slower on Windows), close to the
package's 20-minute test timeout. One browser test, the replay video export, was
half of the five-minute `web` job. Standard runner minutes cost nothing on a
public repository, so wall time is the only cost that matters. The price is more
moving parts: a sharding script and two collector jobs.

**Alternatives rejected.**
- A bigger Windows runner: larger runners are billed even on public repositories.
- Sharding by package: `internal/mcpserver` alone would still be the whole wait.
- One test-name regex for the whole repository: past Windows' command-line limit.
- Playwright `--shard`: with `fullyParallel: false` it splits by file, and the
  slow test's file would still be the long leg; a tag says which test is slow.
- Matrix legs as the required checks: a matrix renames them, which silently
  un-protects `main`.

**Consequences.** The Windows shards run the tests, not `make gate`: `fmt-check`
and `vet` (with `GOOS=windows`) stay on `test`, and `internal/repodocs` runs
only there. A new test lands in a shard by itself; a new slow browser test needs
the `@video` tag or a leg of its own. Branch protection must keep requiring the
collector names, never a leg's.
