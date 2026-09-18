---
topic: client-configurator
---

# D222 — sync checks the environment once, and a 401 names the variable it read

**Decision.** `cartographer sync` runs a single read-only preflight before it
takes the client-state lock: it collects every environment variable the selected
providers need — each provider's `BaseDirEnv` and, when `auth: true` with a
`token_env`, that variable — and fails with **one** error naming all of them.
The preflight never contacts the server: it checks that a token exists, not that
it is accepted. Separately, a 401 from the server now names the variable the
client read the token from and says whether anything was sent at all.

**Why.** The prerequisites surfaced at three different depths of `runSync`, so a
shell missing two of them cost two full runs: `/health` failing is downgraded to
a warning, a missing base directory only breaks in `allProjections`, and a
missing token only as a 401 from `sync_pull`. Every input needed to diagnose
both is available before the first of the three. The cost is a second place that
knows about base directories and tokens; it is paid by reusing
`ResolveBaseDir` and `resolveToken`'s own guard instead of restating their
rules, and by leaving both downstream checks in place as the last line of
defence. `ErrUnauthorized`'s "check the bearer token/env var" named the one
thing the user could not know, while the base-dir error already named its
variable; the two now match.

**Alternatives rejected.**
- *Fail on the first missing variable.* Moves the problem instead of solving
  it: two prerequisites still cost two runs.
- *Let the preflight ping the server to validate the token.* Makes a read-only
  local check depend on the network, and duplicates a judgement only the server
  can make — a token that is set but wrong is correctly a 401.
- *Treat a set-but-empty variable as present.* `resolveToken` returns `""` for
  both unset and empty, and both send no credential; distinguishing them would
  report a prerequisite as satisfied when it is not.
- *Change `ErrUnauthorized`'s own text.* `errors.Is(err, ErrUnauthorized)` is
  matched in four places; the sentinel stays, and the 401's `Cause` is a wrapper
  that unwraps to it.
- *Add the variable name to `client.New`'s signature.* Twenty-odd call sites
  would change to pass an empty string; `WithTokenEnv` is opt-in and leaves the
  legacy message exactly as it was.

**Consequences.** A sync that cannot succeed now fails before the lock and
before the network, `--dry-run` included, so an unattended run (session-start
hook, sync timer, CI) reports its whole misconfiguration at once. Every new
error keeps the `(no configuration was modified)` suffix, and the preflight
stays silent when nothing is missing — a fully configured client's output is
unchanged. A future change here must keep the sentinel reachable through
`errors.Is`, must not let the preflight write or dial anything, and must not
remove the downstream `ResolveBaseDir` and 401 checks: the environment can still
change between the check and the call.
