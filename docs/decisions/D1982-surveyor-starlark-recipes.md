---
topic: architecture
---

# D1982 — the Surveyor side of the Starlark runtime lives in `starcore/recipes`

**Decision.** `surveyor.star` at the root of a plugin is the script entry of the surveyor side, stored with the
`recipes` family (`core.SurveyorScript`). `starcore.Options.Entry` names the entry a runtime loads (empty is
`plugin.star`, the client's). `internal/plugin/starcore/recipes` registers `recipe(name, list, shape, spec, check,
fetch)` and calls the verbs, answering the JSON lines an exec recipe prints; the Surveyor links it and never
`internal/plugin/star`. `internal/boundaries` holds that it reaches neither the apply half, `config` nor `service`.

**Why.** One definition of a valid listing, check, spec and fetch: the dict a verb returns is only reshaped into the
exec wire lines, and the parsers of the exec protocol judge it. The package sits under `starcore` so the runtime's test
seams stay reachable only through `startest`. The runtime offers `http`, `env`, `log`, `settings` and the pure modules;
`files` and `kb` are not registered, so a script that names one fails to load.

**Alternatives rejected.**
- Reusing `internal/plugin/star`: it reaches the apply half and `config`, which the Surveyor may not link.
- A Starlark validator of its own: two sets of listing rules that drift.
- A registrar in `starcore` itself: the runtime would know a kind, and the client half would carry the Surveyor's.

**Consequences.** A verb the script does not register answers `ErrNotDeclared`, which the adapter reads as exit 64.
Each verb call has 5 minutes and 100 million steps. The adapter that runs the recipes in the Surveyor is a later change.
