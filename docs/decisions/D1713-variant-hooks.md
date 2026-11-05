---
topic: client-configurator
---

# D1713 — A client variant contributes a hook directory to its base's shared dispatcher and never writes one

The number is provisional: it is the next unused one, to be renumbered after the PR (D672).

**Decision.** A variant whose descriptor declares `paths.global.hook.dir` with `scan: base` gets a `hook` destination cell only if its base's mechanism has `sharedDispatcher` (Cline's per-event dispatcher in `Documents/Cline/Hooks`, D1663). Its hooks are materialized in its own directory; the base's registrar (`mechanismFor`, the one read of a hook mechanism) reconciles the shared dispatchers from the hooks on disk, whose roots (`clineHookRoots()`) are Cline's own plus the hook directory of every registered variant of Cline. Event reach, `SupportsSessionHook` and the write-findings flag follow the base; `hookProviderFromPath` maps a variant's directory to the base so a prune goes through the base's registrar. A base without a shared dispatcher (Pi's per-hook extension, the settings-file mechanisms) loses only the cell, with a note.

**Why.** The Cline extension runs one script per event from one folder for every client of the family, so a second dispatcher per fork would collide on the same path. The generator takes no directory, so the fork cannot have a dispatcher of its own, only a directory the shared one also reads. Deriving the roots from the variant registry, filled only by `RegisterCachedVariants` (hash plus resolved-path fingerprint) before any command, means `hook dispatch`, a process the client starts on its own, sees a variant's hooks only while the variant is approved: a revoked variant's files remain, nothing runs them.

**Alternatives rejected.**
- A mutex-guarded root list with Add/Remove calls (the plan's first design): a second piece of state to keep in step with the registry; the registry already is the truth and `ResetVariants` clears it.
- A dispatcher generated with the variant's directory: needs a directory argument in the shim and a second owner of the event files.
- A per-hook registrar of the variant: nothing to register, the dispatcher is the registration.

**Consequences.** A fork-owned hook runs under every Cline-family client installed, as the shared dispatcher already did for Cline's own hooks: the approval preview says so. A fork that needs a directory of its own the shared generator cannot read stays unsupported until the generator takes a directory. The variant's capability rows for hook events stay `absent`: no probe of a fork's hooks exists.
