---
topic: client-configurator
---

# D743 — One settings view with origin, recorded by the loaders

**Decision.** Every setting is shown with its effective value, the layer that set it (`default`, `file`, `env`, `flag`)
and the schema's description and restart marker, on two read-only surfaces fed by the same code: `cartographer config`
and the Atlas Settings page (`GET /api/ui/v1/settings`). The origin is recorded by the loaders as they apply their
layer (`Config.Origins`, keyed by schema path, per-KB keys by mounted name: `kbs[<name>].write_gate`), never
reconstructed. A secret is shown as `(set)` or `(unset)`, redacted where the view is built, not in each surface.

**Why.** Nobody could tell why a setting had its value: defaults, file, environment and flags are merged and the
merge forgets who won. Diffing the result against `Default()` is wrong in exactly the case that matters, a value
written in the file equal to its default, which would be reported as "default" and look as if the file were ignored.
Keying per-KB origins by name keeps them right when the list is reordered.

**Alternatives rejected.**
- Reconstructing the origin by diffing against the defaults: misreports an explicit default.
- Reading `kb_status` capabilities as the view: hand-maintained, partial, and its shape is an agent contract. It stays
  as is; a test pins every setting it names to a schema key (it named `kbs[].server_git`, which does not exist).
- Editing settings from the Atlas: needs write-back to a file that may be GitOps-managed and a restart story; its own
  plan.
- One merged client and server view in a single file: see D747.

**Consequences.** A new loader layer or env/flag override must call `mark`, or its keys read as `default`. The server
section is visible to any principal that passes the UI API's auth (no new role); per-KB git facts follow the
overview's rule and are for callers that see the whole KB. Absent from `Origins` means default, so `Default()` marks
nothing. The origin `kb` (a KB carrying its own settings) is added by the plan that introduces that layer.
