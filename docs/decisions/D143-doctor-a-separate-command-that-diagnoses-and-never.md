---
topic: client-configurator
---

# D143 — `doctor`: a separate command that diagnoses and never repairs

`cartographer doctor` is a new read-only command that runs eight checks over this machine's client
configuration and reports what is left over or missing, each finding naming a real path and the
command that fixes it.

**Why not `status --strict`.** These checks read every provider's native config file, enumerate its
MCP entries, count marker pairs in instruction files and scan Codex's `config.toml` for orphaned
tables. That is an order of magnitude more work than `status`' revision comparison — and `status` is
on the path the bootstrap hook's success message prints. Conflating them would slow down the fast
command to serve the rare one. Two commands, two costs.

**Diagnosis only.** No `--fix` flag, and no writing of any kind: not even the lockfile migration
`ReadLockFile` performs in memory anyway, not a created directory, not a refreshed cache. The repair
paths already exist — `sync` restores managed files and removes Codex's double registrations,
`reconnect` (D142) rebuilds, `service sync-timer install` adds the trigger — and each is individually
reviewable. A doctor that silently fixes things is a doctor nobody can predict, and the operator
would lose the one thing this command is for: knowing what was wrong.

**Severity model.** `error` means something is broken now (a managed file missing, a hook firing
twice, an MCP entry pointing at a KB that is gone); `warning` means stale or suboptimal (a v1
lockfile, no trigger for a hook-less provider, a version difference). Both exit 1 — the operator
should look at either — but the severity orders the output so the actionable one is read first. A
third, `info`, carries what cannot be acted on by itself and never changes the exit code: managed
entries recorded before materialized hashes existed cannot be verified at all, and reporting that
per artifact would bury the real findings. Exit codes stay `status`' convention (0 clean, 1 findings,
2 command error) so scripts treat them identically.

**Every finding names a path.** "Something is wrong with claude" is not a diagnosis. The path is
absolute and may legitimately be gone — that is exactly what a `missing` finding says — but it is
always a place the operator can go and look.

**Useful offline.** The only network access is the `/health` probe `sync` already makes, and failing
it produces one `warning` finding rather than aborting: a client whose server is down is precisely
when someone runs `doctor`.

**Never on the session path.** Neither the bootstrap hook nor the scheduled timer invokes it. Eight
checks on every session start is the background cost D60 avoided by keeping `bootstrap.sh` silent,
deterministic and always `exit 0`.

**One byproduct.** The bootstrap hook's lockfile entries carried no materialized hash, so D139's
verification could not check them and `doctor` would have reported "unverifiable" on every healthy
machine forever. `EnsureBootstrapHook` now records the hash of the two files it writes — computed
from the same constants, never read back — which makes the client-generated hook verifiable like any
other managed artifact.
