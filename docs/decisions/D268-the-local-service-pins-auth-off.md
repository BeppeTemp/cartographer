---
topic: deployment-release
---

# D268 — The generated config of a loopback service pins `auth.mode: "off"`, and `setup` stops on a 401 before `connect`

**Decision.** `service install` writes `auth: {mode: "off"}` into the `server.yaml` it generates
when the listen address is loopback; a non-loopback address keeps the default `auto`. The
precedence of `CARTOGRAPHER_TOKENS` over the YAML is unchanged. `setup` probes the service with an
unauthenticated `GET /mcp` before running `connect` and, on a 401, stops naming
`CARTOGRAPHER_TOKENS`, the config file and the one-line fix. `serve` logs the tokens it ignores
when the mode is `off`.

**Why.** A `CARTOGRAPHER_TOKENS` exported for a remote server can reach the local service's
environment (a systemd user manager that imported the session environment, a Windows user
variable), and under `auto` it turned authentication on, while the clients `setup` configures for a
local server send no token: every agent call failed with a bare 401. The mode is a setting separate
from the tokens, so pinning it fixes the local service without touching how env tokens merge —
which operators who run `serve` with `CARTOGRAPHER_TOKENS` rely on. `CARTOGRAPHER_AUTH=on` still
beats the YAML, so the deliberate case keeps a switch that needs no file edit. The cost: a local
service whose owner *wants* auth has to say so (`mode: "on"` or `CARTOGRAPHER_AUTH`), and `serve`
now logs the ignored tokens so that surprise is visible.

**Alternatives rejected.**
- *Make an explicit YAML value beat the environment for tokens.* Reverses the documented
  flag > env > YAML order for one key and breaks operators who keep the secret in the env with
  non-secret settings in the YAML — the arrangement `deployment.md` recommends.
- *Clear `CARTOGRAPHER_TOKENS` in the service definition.* Only systemd can (`UnsetEnvironment=`);
  a Scheduled Task has no environment block and launchd's cannot unset, so it would fix one
  platform of three, and it would also silently drop a token an operator put in a unit drop-in.
- *Have `setup` configure the client to send the token.* The token was meant for another server;
  copying it into the local client's config would bind the local agents to a credential they
  should not hold.
- *Pin `off` for every address.* A service bound to `:39273` or a LAN address protected by env
  tokens would become open to the network without a word.

**Consequences.** A config generated before this decision has no `auth.mode`, and `service
install` never rewrites an existing config: those machines are covered only by the `setup`
detection, whose message tells the operator which line to add. The probe relies on the auth
middleware answering before routing and on `/mcp` not being a public path; a change that exempts
`/mcp` from authentication must replace the probe.
