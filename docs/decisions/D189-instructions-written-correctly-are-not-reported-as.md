---
topic: client-configurator
---

# D189 — Instructions written correctly are not reported as installed until the provider reads them

**Status: implemented.** Closes #243.

**Context.** Cartographer reported instructions as installed when it had written the file it
intended to write. It never checked whether the provider actually reads that file. On a real
machine the two diverge, and the reporting is a false positive of the worst kind: the operator is
told the KB directives are in force while the agent has never seen them.

Verified locally against Codex 0.153.4:

- Cartographer writes the Codex instructions block to `~/.codex/AGENTS.md`.
- The machine also had a user-owned `~/.codex/AGENTS.override.md`.
- Codex's documented rule for the global scope is that `AGENTS.override.md` wins **and the two are
  not concatenated** (<https://developers.openai.com/codex/guides/agents-md>).
- `codex debug prompt-input` found the user's own heading and **no** `cartographer:kb:*` section.
- Meanwhile `cartographer status` printed `instructions 4/4` and `cartographer doctor` reported
  0 errors and 0 warnings.

Both surfaces verified presence and hash of the file they wrote, and neither modelled the
provider's precedence chain.

**Decision.**

- **Detect and report; never write into the user's file.** `AGENTS.override.md` is user-owned.
  Materializing the block there is a separate, explicit choice, not a repair performed on the
  operator's behalf. Silently editing a file the operator owns is worse than a clear "not active"
  finding.
- **A shadowed instructions artifact is not "installed".** `status` excludes it from the
  `instructions n/n` figure and reports it on its own line; `doctor` emits a finding at severity
  **error**, not warning — the operator's stated intent is not met, and the exit code should say so.
- **The chain is declared per provider, as data.** `Descriptor.InstructionsPrecedence` lists the
  provider's global instructions files in the order the provider resolves them, and only where a
  file earlier in the list **replaces** the managed one rather than being merged with it. Providers
  with no documented shadowing rule declare none and behave exactly as before: an invented
  precedence produces a false "not active" finding, which is the same class of defect this check
  exists to remove. Each declaration cites its vendor source in a comment — these are external
  contracts that change outside this project's release cycle.
- **Antigravity declares no chain, deliberately.** It reads both `~/.gemini/GEMINI.md` and the
  cross-tool `~/.gemini/AGENTS.md`, and GEMINI.md — the file Cartographer manages — wins where they
  conflict (<https://antigravity.google/docs/rules-workflows/>). Nothing shadows it. This was the
  open question [D194](D194-support-google-antigravity-in-multi-provider-client.md) recorded when Antigravity landed; it is now answered.
- **Remediation is instructions, not automation.** The finding names the shadowing file, the
  shadowed one, and the two ways out: move the personal content into a section of the override and
  let Cartographer own the managed file, or keep the override and scope Cartographer's instructions
  to a project. Which one is the operator's call.
- **The chain ends at the managed file**, by construction, and a test asserts that its last entry
  equals `provisioning.InstructionsFile(provider)`. Drift between the two declarations would make
  the check compare a file against itself, or against one nobody writes.

**Invariants kept.** Cartographer writes only the files it owns. The hash/drift verification of the
written file is unchanged and the new check is **additional**, not a replacement — a file can be
both correct and inactive. The managed file absent *and* shadowed yields one finding, not two: the
absence wins, because that is what the operator must fix first. `doctor --only <provider>` scoping
applies to the new finding like any other.

**Consequences.** `doctor` can now fail on installations that previously reported clean — that is
the point. `docs/agent-install.md` gains a verification step (`codex debug prompt-input`) so the
runbook does not rely on `status` alone for this, and an instruction to report a case where the two
disagree: that would be a provider precedence rule not yet modelled here.
