---
topic: client-configurator
---

# D1797 — A wildcard is refused only where it can still lead into Cartographer's state

**Decision.** This amends D1723 for the own-state rule of file globs and provider paths. A literal `cartographer` or `.cartographer*` segment is still refused at any depth. A wildcard segment (`*`, `**`, `c*`) is refused only while the segments before it match the start of a known location of Cartographer's state for the glob's anchor: under `{home}` `cartographer-data`, `.config`, `.cache`, `.local/share`, `Library/Application Support`, `Library/Caches`, `AppData/Roaming` and `AppData/Local` (each followed by `cartographer`), `.cartographer` and `.cartographer.yaml`; under `{xdg_config}`, `{app_data}` and `{local_app_data}` `cartographer`; under `{documents}` none. `reachesOwnState` takes the anchor. `{xdg_config}/vendor-cli/**` and `{home}/.vendor-cli/*/y` pass; `{home}/*`, `{home}/.config/c*` and `{xdg_config}/*` stay refused.

**Why.** The old check matched every segment against `cartographer` whatever preceded it, so a client's own folder could not be globbed below its first level. The lexical rule is an early answer for the approval preview: the control is the runtime enforcer, which refuses every `.cartographer*` component, the resolved state directories and the hard deny list, and never walks into a denied directory. Refusing what the enforcer already denies, at the price of rejecting ordinary globs, bought nothing.

**Alternatives rejected.** Dropping the lexical check (the preview would stop warning about `{home}/*`). Refusing every wildcard that could match `cartographer` at any depth (the status quo). Resolving `XDG_CONFIG_HOME` in the validator (the manifest is checked without the user's environment).

**Consequences.** A custom `XDG_CONFIG_HOME` under an arbitrary folder is not seen lexically; the enforcer's state directories cover it. A new location of Cartographer's state is added to `ownStateLocations`. `docs/plugins.md` states the rule.
