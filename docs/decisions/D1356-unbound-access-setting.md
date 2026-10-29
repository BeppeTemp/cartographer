---
topic: sync-provisioning
---

# D1356 — The access mode of an unbound folder is a client setting

**Decision.** The client setting `unbound_access` chooses the mode of a folder under no workspace
binding: a map with a `default:` entry for every provider and optional `<provider>:` entries, values
`read` or `write`. A provider entry beats `default:`, which beats the built-in `read`. `write` gives the
folder the provider's KB set read-write, still with no KB artifact; the bridge then sends no
`access=read`, says in the `initialize` instructions it relays that the session is unbound, and
`workspace resolve` prints the mode and its origin.

**Why.** D1018 fixed unbound folders to read-only, which stops an operator who works in a non-project
folder from writing back what they just fixed; the only workaround, binding `$HOME`, turns write on for
everything under it. The mode is a choice the operator can make, the artifact rule is not: D193
fail-closed is unchanged, so artifacts stay tied to a binding and the handshake sentence tells the
agent to pass `kb` explicitly and read the KB guide, since no skill does.

**Alternatives rejected.**
- A scalar form (`unbound_access: write`): one shape like `scopes` is simpler to read, merge and
  document; the maintainer chose the map only.
- A server-side change: the server's `?access=read` and `Cartographer-Access` confirmation work as they
  are; with no `access` param the session is an ordinary read-write routed one, and an older server works.
- An invalid value falling back to write: never. Anything but `write` is `read`, as `WorkspaceScope`
  does for `scopes`.

**Consequences.** The default and every bound folder are unchanged. The setting only matters for a
provider whose MCP entry is the bridge (it has a binding); on the HTTP entry it is inert. It is not a
security boundary (D737). D1018's server enforcement is untouched.
