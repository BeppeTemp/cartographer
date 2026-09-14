---
topic: client-configurator
---

# D37 — Single binary with subcommands; client always HTTP (no stdio/`--check`/`--base-dir`)

**Decision.** `cartographer-configure` is eliminated: `cmd/cartographer` becomes a binary with
subcommands (`serve`, `version`, `help`, plus the client subcommands `agents`/`connect`/`status`/
`sync`); with no arguments it opens a TUI dashboard in a TTY. The client always talks to the server via
HTTP: the client-side stdio transport, `--check` (replaced by `status`), and `--base-dir`
(replaced by cwd/`--global`) are removed.
**Rationale.** Real deploy topologies (local Docker, multi-client k8s) have the client
on a different machine from the server, or in any case connected over the network: a "direct on the
filesystem" stdio transport was dead code duplicating the materialization logic. A single binary with
subcommands (`git`/`kubectl` style) is more discoverable than two binaries with different flags.
Details: `docs/configurator.md`, `docs/deployment.md` §Topologies.
