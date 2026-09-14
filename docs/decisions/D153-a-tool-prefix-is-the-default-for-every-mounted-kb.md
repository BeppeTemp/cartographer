---
topic: transport-auth
---

# D153 — A tool prefix is the default for every mounted KB

**Status: implemented (2026-08-28).** Supersedes [D102](D102-opt-in-per-kb-mcp-tool-name-prefix.md)'s default; requires
[D152](D152-tool-prefix-uniqueness-is-enforced-and-the-client.md). Closes #174.

**Context.** Prefixing was opt-in and its absence was silent. `mcp.tool_prefix_mode` defaulted to
`off`, so with two or more KBs mounted every KB registered the same tool names. On a client with a
flat tool namespace one of them wins and the others become unreachable — a question about KB A
answered from KB B, with **no error at call time and nothing in the logs**, only a startup warning
the operator may never read. Silent wrong answers from a plausible-looking source are the worst
failure class a knowledge base can have, and that was the default configuration's behaviour.

The portability argument is the one that bit in daily use. Because the prefix was an optional,
server-side choice, **a tool name was not a stable identifier for a KB's capability**: the same KB
is `concept_read` on one deployment and `<x>__concept_read` on another. That contradicts the rest of
the model, where a concept ID is stable and portable by construction. The product already knew it —
`generateKBInstructions` takes a `toolPrefix` and qualifies every tool name it imprints, precisely
"so a prefixed KB does not imprint tools the agent cannot call" — but skill **bodies** get no such
treatment: turning on a prefix for one KB forced rewriting **25 tool citations inside skill bodies
across two KBs**, because every naked `concept_write` in a skill had silently become wrong.

D102's objection was that keeping the mode off lets deployments on per-server-namespacing clients
work untouched. That optimises for not renaming existing tools, at the price of making correctness
depend on which client happens to connect. **A KB's identity in the tool namespace is a property of
the KB, not of the consumer** — and the consumer that gets it wrong fails silently.

**Decision.**

- `Default()` returns `ToolPrefixMode: "kb-name"`. An explicit `kbs[].tool_prefix` still wins,
  unchanged: nothing an operator wrote down is overridden.
- **`off` is retained**, explicitly and documented, as the opt-out for single-KB deployments and
  for anyone who cannot absorb the rename in this release. It is not deprecated here.
- **The single-KB case is reserved.** With exactly one KB mounted a prefix is pure noise, so no
  prefix is derived and tools keep bare names. Implemented by overriding the *mode* for that case
  rather than by changing `ResolveToolPrefix`, which stays a pure function of (spec, mode, name) —
  the reservation is a deployment-shape decision, not a resolution rule, and routing it through the
  mode is what keeps an explicit `tool_prefix` working on a single-KB mount for free.
- **A derived prefix is announced at startup**, once per KB, only when it was derived rather than
  configured: an operator who wrote the prefix does not need telling, but adding a second KB to a
  previously-single-KB deployment renames the first one's tools and that must be loud.
- **Not in scope: making skill bodies prefix-independent.** A placeholder expanded at
  materialization time (the way `{{repo:...}}` already is) would be the fix, and coupling a config
  default to the provisioning expander in one change is how both get broken. Recorded here as
  follow-up work rather than attempted.

**Consequences.** Breaking for multi-KB deployments: tool names change on first start, so the PR
title is a `feat:` and release-please computes a **minor** bump. The generated steering block
follows automatically; hand-written tool citations in skill bodies do not, which the release note
must say.

A name that sanitises to empty or starts with a digit now fails fast for a KB that previously
mounted fine, so the message names the derivation and `mcp.tool_prefix_mode: off` as the escape.

Three e2e scenarios asserted on bare tool names with two KBs mounted: `10_scoped_tokens` and
`14_rbac_visibility` pin `off` (they assert on RBAC, not on naming) and `16_prefixed_multikb` pins it
for its mixed prefixed/unprefixed phases, then gains a fourth phase that exercises the new default
end to end — derived prefixes on both KBs, the announcement in the log, `/health` advertising them,
and the bare name no longer resolving. Pinning the old mode in a test is also exactly the migration
path a real deployment takes, so the suite documents it.
