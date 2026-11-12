---
topic: architecture
---

# D1995 — a private-network host needs a manifest declaration and the same host in the Surveyor config

**Decision.** The surveyor side of a manifest may list `permissions.net_private`: up to 16 exact host names, each
matched by a pattern of `net`. The Surveyor config lists `plugins.<name>.allow_private_hosts`, each of which must be in
that list or the start is refused. Only a host on both lists may resolve to a private or loopback address, and only in the Starlark `http` module (`starcore`, `CallSpec.PrivateHosts`). Every other side, schema 1
included, refuses `net_private`, and the client never sets `PrivateHosts`.

**Why.** An internal wiki is reached by a name that resolves inside the network, and the dial check exists to stop a
script from reaching exactly that kind of address. A grant the plugin author alone could write would let any approved
plugin aim at the Surveyor's network; one the operator alone could write would not show in the approval what the code
intends to reach. Two keys make both consent: the preview shows the declared hosts and the config key that opens them,
and the start log says which ones this Surveyor opens. It costs one more key to keep in step, and a refusal names the
missing one.

**Alternatives rejected.**
- A wildcard or an address in `net_private`: a grant is for one machine the operator can name; a suffix would open every
  internal name under it, and an address says nothing about who is reached.
- Opening every forbidden range for a granted host, or link-local as the plan first had it: link-local is where cloud
  metadata lives (`169.254.169.254`), and neither it nor multicast, unspecified, carrier-grade NAT or the reserved
  ranges is where an internal service lives. The grant opens private and loopback only, minus the metadata address
  inside a private range (`fd00:ec2::254`).
- Dropping a config host the manifest does not declare: the operator would believe a grant is in force; it stops the start.
- Enforcing it for exec recipes and MCP connector processes: they are operator-pinned code outside the host's network
  control. The grant neither applies to them nor limits them, and the preview says so.

**Consequences.** The grant is per exact host and per hop: a redirect is judged by its target's own grant, so a granted
host cannot hand a script on to another private host. The `net` allow-list still runs first. A future runtime that
reaches the network for a plugin must take `PrivateHosts` from the host, never from the plugin.
