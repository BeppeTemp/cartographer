---
topic: client-configurator
---

# D169 — Per-provider KB binding: a user-owned preference beside a server-owned cache

**Decision.** `.cartographer.yaml` gains two independent keys. `known_kbs` is the
cache of the KB names the server advertised at the last successful
`connect`/`sync`, overwritten wholesale on every run. `clients.<provider>.kbs` is
the per-provider binding, declared by the user with the new `cartographer
client` subcommand (`list`/`show`/`bind`/`unbind`/`reset`) and never written by
`connect` or `sync`. `Config.BoundKBs(provider)` is the single resolver, and it
also reports whether the answer was explicit.

**Rationale.**

- **The two meanings needed two keys.** The pre-existing `kbs` key looked like a
  preference and behaved like a cache: `runSync` and `doConnect` assigned it the
  full advertised list on every run, so anything a user wrote there survived
  exactly until the next sync. Renaming it to `known_kbs` makes its ownership
  legible, and the binding gets a key nothing else writes.
- **Three states, one resolver.** An absent entry, an entry holding an empty
  list, and an entry holding names are distinct: every known KB, no KB, and
  those. A nil slice is never a wildcard. `BoundKBs` is the only place the rule
  lives, so a caller cannot re-derive it by testing a list for emptiness — the
  mistake that would silently give an intentionally empty binding full access.
- **Default is compatible, not deny.** A provider with no entry keeps receiving
  every known KB. Global default-deny would have stripped artifacts from every
  already-connected client on upgrade, turning a new feature into an outage;
  declaring an entry is what buys default-deny, one provider at a time.
- **`unbind` of the last KB leaves the entry.** Deleting it would restore "every
  known KB" — the opposite of what removing the last KB asks for. `reset` is the
  explicit way back, and the command says so.
- **Configuration is offline.** `client` never contacts the server: a KB absent
  from `known_kbs` is a warning, because it may be mounted later and configuring
  a machine must not require the network. For the same reason the commands save
  and stop rather than triggering a sync — a configuration command that writes
  into provider directories is the side effect this work exists to remove.
- **Provider validation lives in the command, not the package.**
  `internal/clientconfig` stays a data package with no knowledge of providers,
  matching how `Agents` is already handled; `cmd/cartographer/client.go` owns
  the `configurator.Lookup` check and the "valid providers" message.
- **The binding is a projection, not an authorization boundary**, and is
  documented as such. Per-KB scoped tokens exist server-side, but a per-provider
  token env var would not isolate a process that shares the user, the filesystem
  and the environment: it can read the other client's configuration. The binding
  controls what a client is *given*, not what it *could* request.

**Deviation from the plan.** The plan kept `Config.KBs` as a read-only Go alias
of `KnownKBs`. It was removed instead: two Go fields that can diverge are the
bug class being fixed, the field is internal, and every caller was migrated in
the same change. The *on-disk* `kbs` key — the alias that matters for existing
files — is still read, and is replaced by `known_kbs` on the first write.

**Consequences.** Additive and backward compatible on disk: a pre-D169 file
loads unchanged and is migrated on its next write. No projection behaviour
changes here — bindings are recorded and not yet enforced, which the mutating
commands state in their own output so the feature is not mistaken for active
isolation. Enforcement is the filtered projection that follows.
