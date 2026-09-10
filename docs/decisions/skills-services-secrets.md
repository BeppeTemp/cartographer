# Skills, services and secrets decisions

Bundled skills, service secret resolution and operational skill distribution. Current behavior: [`../skills-services-secrets.md`](../skills-services-secrets.md).

These records explain why choices were made and may describe superseded behavior.
For the supported interface, follow the current-state page linked above.

<a id="d26"></a>
## D26 — Bundled skills embedded in the binary

**Decision.** Recovery and operator skills are embedded through `internal/skillbundle`
using `//go:embed all:bundled`; `skill_list` and `skill_install` can therefore
serve a known bundle without relying on files beside the executable.

**Rationale.** The skills needed to bootstrap or recover a KB must remain available in
single-binary installations and after an incomplete client provisioning cycle.

---

<a id="d47"></a>
## D47 — Per-KB SOPS: `AgeKeyEnv` (env wins), flat `service_get(resolve_secrets)`, resolve requires rw

**Decision.** Connects `KBSpec.SopsAgeKeyFile`/`SopsConfig.AgeKeyFile` (D44) to the first real
invocation of `internal/sops`. `Decrypt`/`DecryptAll`/`ResolveRefs` gain a variadic
`env ...string` (same pattern as `gitx.runGitEnv`, D46: the caller's env wins). `service_get`
gains the `resolve_secrets` parameter (default `false`): if `true`, it reads `secrets_source` from
the `Service`'s frontmatter and calls `sops.Decrypt` with the per-KB age key. OKF frontmatter
supports only `string`/`[]string`, so structured per-ref `secret_refs` remain out of scope:
`resolve_secrets` always decrypts the entire `secrets_source`.
*(Superseded in part by [D166](transport-auth.md#d166): `DecryptAll` was deleted. It never gained a
caller — `service_get` resolves one `secrets_source` at a time through `Decrypt` — so the variadic
`env` this entry gave it was only ever exercised on the other two.)*
`service_get` stays classified `ReadOnly` (D45) for the default path; with `resolve_secrets:
true` the HTTP guard (`mcpAccessGuard`) forces `needWrite=true` as a special case, without touching the
per-tool-name classification. Defense in depth: `filepath.IsLocal(secretsSource)` rejects
path traversal on `secrets_source` before decryption.
**Rationale.** Propagating the `env` instead of mutating `os.Environ()` prevents one KB's age key
from contaminating requests on another in a multi-KB server (same reason as D46). The special case
in the guard keeps all r/rw enforcement in one place, consistent with D45.
Details: `docs/skills-services-secrets.md` §SOPS secrets, `docs/transport-auth.md`.

---

<a id="d96"></a>
## D96 — Operations knowledge ships as a bundled skill

**Status: implemented (2026-07-24).**

**Decision.** `cartographer-ops` is a single bundled skill containing the operational server/client
playbook: CLI surface, configuration precedence and load-bearing environment variables,
health-based diagnosis, drift recovery, conflict routing, and native/k8s upgrades. It travels in
the embedded bundle and is provisioned with the other bundled skills; the local test suite loads
and validates every bundled skill and asserts the manifest inventory.

**Rationale.** A bundled skill stays aligned with the installed binary, whereas documentation on
`main` can describe a newer pre-1.0 CLI and tool surface. One concise operational reference keeps
the end-to-end runbook available to agents on client machines that do not have the repository
checked out.

---

<a id="d104"></a>
## D104 — Structured SOPS pointers, scoped refs and encrypted-only writes

**Decision.** Decrypted YAML is flattened to RFC 6901 JSON Pointers and
`secret_refs` use scalar `NAME=path#pointer` entries. `secret_set` updates an
existing encrypted secret with `sops set --value-stdin`; it never creates the
root `.sops.yaml` creation-rules file.

**Rationale.** Repeated nested leaves otherwise collide in a flat map. Pointer
references provide least privilege without widening the intentionally small
frontmatter grammar. Creation rules and recipient selection remain operator
work, while encrypted `*.sops.yaml` files are safely writable by the server.

## D158 — `Service` is matched case-insensitively, and `secret_resolve` redacts by default

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


## D191 — One skill validator, for the MCP channel and the git one

**Status: implemented.** Closes #245.

**Context.** A skill written through MCP and the same skill arriving through git were validated by
two different rules, and the git channel was the weaker one. A malformed artifact it accepted broke
the sync of the **whole KB** later, far from its cause.

Two gaps.

**The shared validator was too thin.** `skill.Validate` checked only a non-empty name, a non-empty
description, and a body over 500 lines as a warning. It did not check kebab-case, a maximum name
length, or that the frontmatter `name` matched the directory. The clients do impose those rules —
Kiro and OpenCode document lowercase kebab-case, 64 characters maximum, equal to the directory, and
OpenCode additionally rejects consecutive `--`. A skill that passed our validation and was refused
by the client was a silent no-op in the agent's catalogue, invisible from here.

**The two channels did not run the same check.** `artifact_write` called `validateSkillArtifact`,
which required `name` to equal the directory. `BuildManifest` — which serves the git channel and is
declared equivalent — called `skill.LoadAllSkills` and **discarded the error slice**
(`kbSkills, _ :=`), never invoking `Validate` at all. With directory `skills/foo/` and frontmatter
`name: bar`, the manifest registered the artifact as `bar` but hashed the directory `foo`;
`ReadArtifactFiles` then looked for `skills/bar`, the read failed, and it could take `sync_pull`
down for the entire KB. A hand-edited or git-imported skill broke every client of that KB, while
identical content would have been refused outright over MCP.

**Decision.**

- **One validator, called from both channels.** `skill.Validate` is the single authority.
  `validateSkillArtifact` delegates to it and keeps only the MCP-specific part (the path shape of
  the incoming write); `BuildManifest` calls it too.
- **The rule set is the intersection of what the clients accept**, not the most permissive union:
  lowercase `[a-z0-9]` segments separated by single `-`, no leading/trailing `-`, no `--`, at most
  64 characters, and frontmatter `name` equal to the directory. Adopting the strictest client rule
  is the point — the failure being fixed is a client silently ignoring a skill.
- **Severity split.** A name/directory mismatch, an invalid or over-long name and an empty
  description are **errors**: they break a channel or a client. Body length and description length
  stay **warnings** — they degrade quality without breaking anything, and turning an existing KB's
  long descriptions into hard failures would block syncs that work today.
- **`BuildManifest` reports, it does not silently skip.** An invalid skill is excluded from the
  manifest and the reason is surfaced with the KB, the skill and the violated rule named — through
  a `SkillDiagnostic` callback (the same shape `MCPDiagnostic` already had, wired to the server's
  startup log) and on `Manifest.Issues` for a programmatic caller. The errors `LoadAllSkills`
  returns are surfaced the same way instead of being discarded.
- **`Issue` carries the rule it violated**, so a caller attributes a refusal without parsing a
  message.
- **The `<namespace>--<skill-name>` convention is dropped.** It was unreachable from production
  code and incompatible with the `--` rule above. The comment, the `TestNamespaceExtraction` test
  exercising a local `strings.SplitN` with no production caller, and the E2E fixture
  `kbinfra--query-rete` (now `query-rete`) are all retired. Namespacing, if it is ever needed again,
  comes back as a separate decision rather than as a dormant string convention.

**Invariants kept.** A KB with one invalid skill still syncs its valid artifacts — the failure is
scoped to the offending skill, the opposite of the whole-KB `sync_pull` breakage it replaces.
`LoadSkill` keeps its signature and parsing. `Manifest.Issues` is deliberately **not** part of the
revision: excluding an artifact already changes it, and hashing the message list would make an
edit to the wording look like a catalogue change.

**Consequences.** Skills that pass today can be refused after this change — breaking for KBs whose
skill names are non-conforming, and the release notes must name the rules. `docs/sync.md` and this
page also stop contradicting each other on `signed`: the signature and the trust policy are now
described as the two distinct things they are.
