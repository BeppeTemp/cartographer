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
Each verb call has 5 minutes and 100 million steps.

## The adapter

`internal/surveyor/recipe/starlark.go` is an exec recipe whose child is the function: `runScript` hands the request an
exec script would get on stdin to the verb and reads the lines it returns with the exec parsers, so the caps, key rules
and codes are the exec ones, and the one-at-a-time slot and the redaction of the mapped variables are shared. `env.get`
reaches only the names the manifest declares and the config maps, and a mapped variable that is unset or empty fails
like an exec recipe's. A recipe folder with `instructions.md` alone is a Starlark recipe; the tree reader runs the script
check (`recipes.Check`), and the config refuses a registration with no folder. A Starlark recipe has no `connector`:
its units read the Source's own locator, and content comes inline or from `fetch`. The private-network grant is a later
change; `http` keeps refusing private addresses.
