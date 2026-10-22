---
topic: sync-provisioning
---

# D1018 — A folder under no workspace binding is served read-only

**Decision.** In workspace scope, a session started in a folder that no binding covers (`$HOME`
included, with no special rule for it) gets the provider's KB set with every read tool and no write
tool, and no KB artifact. The bridge asks for it with `?access=read` on the routed endpoint; the
server enforces it: a read-only session is one more `Server` in the routed cache, its `tools/list`
omits the write tools and a call to one is refused with the `unbound_folder` denial, which names
`cartographer workspace bind` and the repository declaration in a KB's `paths.yaml` as the fixes.
Every response of such a session carries `Cartographer-Access: read`. A bridge that asked for
read-only and does not see that header on the first successful response serves no tools.

**Why.** The same folder receives no KB artifact (fail-closed, D193), so a read-write session there
had write tools and none of the skills, agents or instructions that say how to use them. Writing
needs a binding.

**Alternatives rejected.** Filtering in the bridge only: a tool call would still reach the server, and
the bridge is a convenience, not an enforcement point. Leaving the folder read-write with a warning:
agents do not read warnings before a write. A special rule for `$HOME`: the same defect applies to
every unbound folder. A separate endpoint path: the mode combines with `?kbs=` and the cache key
already carries the set.

**Consequences.** Read-only is a scope filter, not a security boundary (D737): the token in the
environment can still write through any other route. The client and the server must both carry this
release: an older server ignores `access` and returns no confirmation, so the bridge serves no
Cartographer tool until the server is upgraded (fail closed across versions). Until the first
confirmation the bridge forwards messages one at a time instead of concurrently. The read-only tool
list stays `readOnlyToolNames`: one fact with each tool's `ReadOnly` field.
