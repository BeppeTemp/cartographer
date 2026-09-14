---
topic: sync-provisioning
---

# D61 — Instructions: auto-generated agents section + curated `instructions.md` (WP5)

**Context.** D56 generates the `instructions` block with archives only + generic operational
instructions. It says nothing about the **agents provisioned by the same KB** (D48/D55): the main
agent does not know local subagents exist nor when to delegate. There is also no place where
the operator can write domain-specific orchestration directives.

**Decision.** `generateKBInstructions` (signature and materialization mechanism unchanged)
adds two optional trailing sections, each omitted if empty: (1) an auto-generated agents
section (name + `description` from the frontmatter of `agents/<nome>.md`, sorted by name); (2)
an optional curated file `<kbRoot>/instructions.md`, free text included verbatim. The hash remains
`sha256(generateKBInstructions(...))`: no change to `BuildManifest`, drift is still detected
because the function now also reads these files. `instructions.md` is not a concept: it lives
outside `data/`, never indexed nor materialized as a standalone file.
**Discarded alternatives.** `instructions.md` as an additional skill/agent (it would require a new
kind and its own destination, instead of enriching the already injected block); keeping it outside the
KB in server config (it would lose the git versioning shared with the rest of the KB).
Details: `docs/sync.md` §Instructions.
