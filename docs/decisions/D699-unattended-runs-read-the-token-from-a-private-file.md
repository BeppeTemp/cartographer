---
topic: client-configurator
---

# D699 — Unattended runs read the client token from a private file

**Decision.** When auth is on and `$token_env` is set, `connect` (hence `reconnect` and `setup`) copies the token to a
`.cartographer-token` file next to `.cartographer.yaml`: mode `0600`, written atomically, never through a symlink. `resolveToken`
returns `$token_env` when set, otherwise that file. `sync`'s environment check counts the file as present, and
`doctor run` puts the token in the headless client's environment (never argv) when the variable is not already set.
`disconnect` removes the file with the last provider; `reconnect` keeps it.

**Why.** launchd, systemd user units and the Task Scheduler do not inherit a login shell's exports, so the sync timer
(D325) and the scheduled doctor (D690) could never authenticate on an `auth: true` install: the sync failed its
environment check on every run, the doctor's client got a 401. The token already sits in plain text in the user's
shell profile; a `0600` file in their home adds no exposure, and one code path works on all three platforms. The cost
is a second copy to keep fresh: rotate the variable, then `reconnect`.

**Alternatives rejected.** Keychain / secret-service / Credential Manager: three platform APIs for the same exposure
the user already accepts. Putting the token on the client's command line: argv is visible to other processes. Reading
the shell profile: it is arbitrary shell, not data.

**Consequences.** A token file readable by group or others is refused, with a message naming `chmod 600`: a silent
fallback would hide a leak. The env var always wins, so an interactive session behaves as before. Existing installs get
the file on their next `reconnect` (or `connect`) with the variable set.
