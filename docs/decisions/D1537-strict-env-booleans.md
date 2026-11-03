---
topic: deployment-release
---

# D1537 — Boolean environment variables are strict

**Decision.** `CARTOGRAPHER_WEB_ENABLED` and `CARTOGRAPHER_UPDATE_CHECK` accept the words `CARTOGRAPHER_AUTH` accepts (`true`/`1`/`yes`/`on`, `false`/`0`/`no`/`off`, any case; `t` and `f` as before). `FromEnv` returns an error naming the variable for anything else, and the server does not start.

**Why.** The old parse dropped the `strconv.ParseBool` error and kept the default. `CARTOGRAPHER_WEB_ENABLED=off` left the Atlas UI served and `CARTOGRAPHER_UPDATE_CHECK=no` still called GitHub, while the settings view reported the key as set by the environment. A value the operator typed to close something must never leave it open. The cost is that a script with a sloppy value that used to start now fails at start, with the variable named.

**Alternatives rejected.** A warning and the default: it is the silent fallback again, only noisier. Keeping `strconv.ParseBool` strictness alone: it rejects `yes`/`on`/`no`/`off`, which `CARTOGRAPHER_AUTH` already accepts, so two booleans of one family would disagree.

**Consequences.** A new boolean environment variable uses `parseBool(name, v)` and returns its error; it never falls back to the default on a value it cannot read.
