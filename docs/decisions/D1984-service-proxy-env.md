---
topic: deployment-release
---

# D1984 — The service records the shell proxy and CA variables in a 0600 env file

**Decision.** `service install` records the shell's `HTTPS_PROXY`, `HTTP_PROXY`, `NO_PROXY` and `SSL_CERT_FILE` (and their lowercase forms), a closed list and no other variable, in `service.env` beside the server config: `NAME=value` lines, mode 0600, written atomically, removed when none is set. `serve --env-file <path>` reads it before anything else and honours only the listed names; any other line is ignored and logged by its key, never its value. The launchd plist and the systemd unit also carry the values inline and are written 0600 when they do; the Windows task and the cron backend carry only `--env-file <path>`. `upgrade-repair` and a binary switch keep what is recorded; only an explicit `service install` takes the variables from its own shell, and so removes the ones it no longer has. `service status` prints the recorded names, never the values; `doctor` is not extended.

**Why.** A launchd job, a systemd user unit and a Windows task start from a minimal environment, so a proxy or an internal CA that works in a terminal fails in the service. A proxy URL can hold credentials, so where the values go matters as much as that they go.

**Alternatives rejected.** `serve --setenv NAME=value` in the task arguments: the value sits on a command line any process can read and in the task XML, and the flag package echoes it in an error. Copying the whole shell environment: it would bake tokens into a file on disk. Systemd `EnvironmentFile=`: no smaller than the inline `Environment=` lines the unit already renders, and it adds a quoting dialect. Re-reading the shell on every repair: an upgrade run from a different shell would silently change, or drop, a working proxy.

**Consequences.** The list of names is the only place a new variable is added, with a test that nothing else is written. Anything that rewrites a definition without being an explicit install passes `KeepEnv`. The env file is removed with the service.

On Windows the 0600 mode is a no-op: `service.env` inherits the ACL of the config directory, which is per-user under the default `%APPDATA%`; a config in a shared directory exposes it. No ACL code is written for it. A line the file parser ignores is logged by its key only when that is a plain identifier, else by its line number, since a malformed line may itself be part of a secret.
