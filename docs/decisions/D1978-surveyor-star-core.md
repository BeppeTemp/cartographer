---
topic: architecture
---

# D1978 — The Starlark runtime is a server-safe package under the client's kinds and modules

**Decision.** The Starlark runtime of plugin scripts (loader and runner, registrar mechanism, `Script.Invoke` with its limits, the settings, log, env and http builtins) lives in `internal/plugin/starcore`, which imports only `internal/plugin/core` and the interpreter. `internal/plugin/star` keeps what only a client has (the command, tool and event kinds, the files and kb modules) and builds on it by handing the runtime its registrars, its extra modules and a `CallSpec`.

**Why.** The Surveyor needs to run Starlark recipes, but `internal/boundaries` forbids `internal/surveyor` from reaching the client apply half, and `star` reaches it through `internal/plugin`, `internal/service` and `internal/configurator`. Splitting the runtime out follows the earlier `internal/plugin/core` split. It costs a seam: the runtime knows no kind, so a host supplies how an impl is called and its result read.

**Alternatives rejected.** Taking `star` off the exempt list by cutting its imports: files.* needs the home-directory rules of the apply half. Re-implementing a second interpreter wrapper for the Surveyor: two copies of the limits and of the late-binding trap.

**Consequences.** `starcore` must never import the apply half, `internal/service` or `internal/config` (`TestServerNeverLinksTheApply` holds it). A new capability module that touches the filesystem or the server's state belongs in the host, handed over through `Options.Extra` and `CallCtx.Ext`.

The test seams (a replaced resolver, proxy or TLS configuration, a builtin override, a replaced source) are not part of the runtime's API: they take a token from an `internal/` package, `internal/plugin/starcore/startest` is the only door, and `TestStarcoreSeamsAreTestOnly` fails if a non-test file imports it. `Options` refuses a module or registrar that would replace a builtin (`json`, `math`, `time`, `settings`, `log`, `http`, `env`).
