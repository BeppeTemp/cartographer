---
topic: skills-services-secrets
---

# D158 — `Service` is matched case-insensitively, and `secret_resolve` redacts by default

**Status: implemented (2026-08-28).** Closes #179.

**Context.** Two defects on the secrets path, one making the feature silently inert and one
putting credentials in a transcript.

`service_list` and `service_get` compared `fm.Type() != "Service"` exactly — the only two
occurrences of the literal in non-test code. A KB whose type vocabulary is lowercase (the
likely outcome when types come from an imported corpus, or from a non-English domain
vocabulary) declared `type: service`, both tools returned **zero services**, secret
resolution was unusable, and nothing reported why. In the field this cost a source-code read
*after* 17 SOPS bundles had already been converted. Nothing in the docs said the type string
was reserved or that its capitalisation was load-bearing.

`secret_resolve` had no redacted mode: the handler already built a sorted key list and then
interpolated the value. Verifying that resolution works therefore meant printing a
credential into the agent transcript, and from there into whatever logs it. It happened. The
audit layer was already careful about exactly this distinction — `auditResourceFields`
records `secret_resolve`'s `concept_id` and deliberately excludes its `names` as "secret
field names, not resource identifiers" — so the tool's own output was the one place that
leaked.

**Decision.**

- The comparison becomes `strings.EqualFold`, **not** "any type accepted": `Service` stays
  the reserved role, only the spelling stops mattering. That is the smallest change that
  removes a silent failure without widening what counts as a service descriptor — a
  near-miss like `services` is still not one.
- The reserved type is documented, and lint reports `secrets_on_non_service` (warning) when a
  concept declares `secrets_source` or `secret_refs` under another type, so the mistake
  surfaces from the KB rather than from reading Go.
- **`secret_resolve` redacts by default**, rather than offering an opt-in `keys_only`. The
  safe behaviour has to be the default one: the unsafe default already leaked a credential
  once, and an agent choosing the safe flag requires the agent to know the flag exists.
  `reveal: true` returns the values.
- `reveal` **is** recorded in the audit trail (added to `auditResourceFields`): the argument
  name is not secret, and the decision to print a credential is exactly what an audit trail
  is for.
- `service_get(resolve_secrets: true)` is **unchanged**. It returns a descriptor with
  resolved values by design and its callers are the skill-execution path, not an agent
  transcript; changing it is a separate decision with different consequences.

**Consequences.** `secret_resolve`'s default output changes, so any caller parsing
`key=value` from it must pass `reveal: true` — the one breaking edge of this entry. The
case-insensitive match is strictly additive: KBs that worked keep working, KBs that silently
returned nothing start working. The regression test asserts the **absence** of the plaintext
rather than only the presence of the placeholder, so a future change that reintroduces the
leak fails the build.
