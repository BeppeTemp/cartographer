---
topic: control-plane
---

# D2028 — The age key of a KB is resolved on every read

**Decision.** A mounted KB holds a resolver, not a path: `KB.SopsAgeKey()` looks for `<kb>.age`,
then `default.age`, in `credentials_dir` on each call. A key file added or removed there is seen by
the next `kb_status`, `secret_set`, `secret_resolve` or `service_get`, with no restart. This
supersedes the rejected alternative of D1191 ("reload the key file when it appears").

**Why.** Enabling secrets on a KB ended with a restart of the service only to make it notice a file.
The cost D1191 feared is small: the lookup is two `Stat`s, cheaper than the `sops` process every
caller is about to run, and the loose-permission warning stays once per file.

**Alternatives rejected.** A cache with a reload signal: one more state to keep right, for a lookup
that costs less than the call it serves. Reloading the whole mount: it restarts git sync and the
other credentials for one file.

**Consequences.** A handler reads the key once into a local, so one call sees one key. Nothing
reads a key path at mount time; the hint of `kb_status` no longer mentions a restart.
