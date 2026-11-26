---
topic: client-configurator
---

# D2404 — `connect --kb all` records a marker that follows the server

This supersedes one bullet of D190, "`--kb all` is a real, recordable value ... persisted as an explicit binding to the currently mounted names". The rest of D190 stands: the selection is made before the first write, a new multi-KB connect without `--kb` still fails, names are validated against the mounted ones, `--kb all` cannot be combined with a name, and `--dry-run` applies the binding in memory.

**Decision.** `--kb all` (and the picker's choice "all of them, and the ones mounted later", and `client bind <provider> --all`) saves the scalar `kbs: all` under the provider's entry, in the flat and in the grouped layout, instead of the names mounted at that moment. A list stays a list: `kbs: [all]` is the KB named `all`, and any other scalar is a load error naming the key. The marker resolves exactly like a provider with no binding: `BoundKBs` returns the known KBs with `explicit == false`, so every consumer follows the server for it, and `FollowsServer` says which of the two it is (shown as the origin `all`). It is written only by `connect` and `client bind --all`, never by `sync`; `sync` follows the server through `known_kbs`, which the state file already holds. Each sync adds a KB newly mounted and removes one the server no longer lists; `status` says what is pending and what changed (`kbs_pending`, `kb_change` in the state file).

An explicit list stays explicit: nothing converts it, on upgrade or at a sync. The way to switch is `connect --kb all` again, `client bind <provider> --all`, or editing the file; the way back is `client bind <provider> <kb>[,<kb>]`; `client reset` removes the marker and leaves the implicit default, which resolves the same. A workspace binding never becomes `all` (it is fail-closed, D193): `connect --kb all --workspace` binds the folder to the names mounted now and says so, and leaves the provider's binding as it was.

Lineage lives in the effective sets, not in the binding: a marker has no lineage of its own, since every readable mounted KB is already received, and `no_lineage` and `exclude` go with a named list (`BindAll` clears them, `Bind` on a marker starts a list with none, `connect --kb all --exclude` is a usage error). A KB newly mounted under the marker derives bindings from its `paths.yaml` and gets its closure in the same sync.

Only an answer about the KBs the token reads changes `known_kbs`. Of the four shapes of `/health`, a failed call (a network error, an HTTP error, a 401, a bootstrapping server) changes nothing and persists nothing; a reply with no `kbs` key on a server with auth (the token is unset or not accepted as an identity) keeps what the client follows and warns; a list adds and removes; a listed, empty one removes every followed KB, on a line of its own that `--dry-run` shows first, and a Cartographer server that lists no KB is not pulled from. The server needs no change: it builds the list from the principal already attached to the request, and has no path that turns an error into an empty list.

A KB the server was told to serve and could not mount now (`skipped_kbs`, only those the caller reads) is **held**, not removed, for every provider that follows the server: its lock entries are carried forward unchanged, its region of the instructions block is carried from the bytes already in the block (the block is rebuilt from the manifest, which a held KB is not in, so without this the region would leave at the next rewrite while its lock entry stayed), its per-KB MCP entry stays, and it stays in `known_kbs`. A held KB whose region is not on disk carries nothing and its instructions entry is dropped. One function (`heldKBs`) decides it, and `status` applies the same hold, so a held KB is not drift. This goes beyond "an unmounted one is removed": it keeps the word true for a KB that is gone and refuses to act on one that is down.

Approvals are untouched (D1141, D906) and a new KB is not put behind a gate: what runs on the machine (a hook, an executable file, a skill or agent declaring hooks or MCP servers, an MCP descriptor) still waits for `cartographer approve`, and the sync that adds the KB says how many wait; skills, agents' text and instructions arrive unattended at the next sync, which a session-start hook runs.

**Why.** The behaviour the maintainer wants for a client that asked for all is the one the default already has (resolve to `known_kbs`, refreshed from `/health` on every sync); what the default lacks is intent, because a first multi-KB connect without `--kb` is refused on purpose. A recorded marker that resolves like the default adds the intent without a second resolver, so every consumer of `BoundKBs` is right at once. The cost is that a KB mounted tomorrow does widen a client that asked for all, which D190 avoided for the list it recorded; an operator who wants to approve each KB first keeps a list. A downgraded client cannot read `kbs: all` and refuses the file.

**Alternatives rejected.**

- *Keep recording the names and refresh them at each sync.* It rewrites the user-owned file from `sync`, and cannot tell a list the person wrote from one the sync wrote.
- *A gate on a new KB.* Decided against: connect all follows the server; the sum of what a sync widened is announced instead.
- *Treat any answer with no list as "no KB".* A restart, an unset token or a 401 would then remove everything a client holds.
- *Remove a KB the server could not mount.* A moved path or an unreachable remote is transient, and the removal is a data loss on the client.

**Consequences.** `known_kbs` is the one list a follower resolves to, and `BoundKBs` stays the only resolver: no caller tests the marker to decide a KB set. A new per-KB input of the instructions block needs the same answer for a held KB (its bytes are carried, never rebuilt). A temporary error never prunes or disables for good.
