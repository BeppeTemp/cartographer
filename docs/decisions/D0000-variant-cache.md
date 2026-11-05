---
topic: client-configurator
---

# D0 — Accepted client variants live in a cache registered before the config loads

**Decision.** `.cartographer-variants.json` in the client directory maps a variant name to `{source_kb, content_hash, descriptor_b64, accepted_at}`, written atomically and without following a link (`clientvariant.SaveCache`, under the client lock). `main` calls `clientvariant.RegisterCached` before any command: every entry is hashed, parsed (`Parse`), resolved for this OS (`Resolve`) and registered with `configurator.ReplaceVariants`-style semantics; an entry that fails any step, or resolves with a dropped path, is skipped with a named warning. `clientconfig.Load` and the lock cleanup (`dropUnsupportedLockSections`) skip names present in the cache, so a variant that failed to register this run is kept, not deleted (D898).

**Why.** The registry is empty at process start and a variant is declared by a KB that may be unreachable. D898 deletes from the config any provider the registry does not know, so registration must precede the load. The cache is a file an operator can edit: its bytes are never trusted, containment is re-proved at every start.

**Alternatives rejected.** Registering from `clientconfig.Load`: pulls provisioning and the registry into the config package. Trusting the cached resolved paths: a hand edit would write outside the home.

**Consequences.** `Cache.Retired(declared, complete)` is the one place that says which names retire: nothing, unless every bound KB was reached and pulled completely. Discovery from a manifest, approval by hash and the retirement through disconnect are wired by the steps that follow (approval, sync).
