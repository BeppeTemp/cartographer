---
topic: deployment-release
---

# D197 — The evaluate-then-install path: steer the agent that is actually reading

**Status: implemented.** Closes #252.

**Context.** People do not reach Cartographer by reading the repository. They paste the repository
link into an agent they already have open — *"explain this project, I'm evaluating installing
it"* — read the agent's answer, and then say *"install it"*. At that second prompt the agent has
only what it read at the first: `README.md`. Nobody hands it `docs/agent-install.md`.

The runbook is good — command by command, expected output after each, a failure table, and the
right refusals (never accept a host key on the user's behalf, never clone into the service data dir
by hand, never create a KB without a remote unless the user accepts a local-only one). It was
simply unreachable from the path people take, and the README steered the agent the wrong way in
five specific ways: its agent section was addressed to a *human* copying a prompt; §Quick start
followed immediately, called itself "the primary path", and is four commands that omit the platform
check, the `brew`-absent branch, asking for the KB remote, `cartographer agents`, the verification
triple, the failure table and the session restart; the runbook's own opening framed a *supplied* KB
remote as a precondition, which on this path is always false; its step 5 told the executing agent to
restart the session it is running in; and the README had no prerequisites, no statement of what
lands on the machine, and no uninstall, so the evaluation answer was either incomplete or inferred.

**Decisions.**

- **Steer, do not restructure.** The fix is one paragraph in §Install addressed to the agent in the
  second person: if you are asked to install Cartographer, fetch the runbook's raw URL and follow
  it, and do not improvise from Quick start. It states *why* in the same breath — the runbook asks
  for the remote, identifies the executing client, verifies, and carries the failure table — so an
  agent weighing two visible options has the basis to choose rather than an unexplained
  prohibition. The section heading was renamed from "Agent-driven install", which described only
  the human's recipe, to "Installing Cartographer with an agent", which covers both readers.
- **Quick start stays and names its audience.** It is genuinely useful to a human skimming the
  page; deleting it to protect agents would fix the wrong reader's experience. It gains one line
  saying it assumes an interactive operator, and routes agents to the runbook.
- **The footprint statement lives in the README, not in `deployment.md`.** The whole premise is
  that the evaluating agent has only the README, so the facts it needs must be there: prerequisites
  (git, an empty remote for the first KB, `sops` only for encrypted values, Go only for the source
  path); what gets installed (the binary and where each method puts it, the optional per-user
  service on `127.0.0.1:39273` with its two unit paths, `~/cartographer-data`, and the writes into
  each client's configuration that happen **only** on `connect`); and removal. Every path was
  derived from `install.sh`, `internal/service/paths.go`, `internal/defaults` and
  `cmd/cartographer/service.go` rather than paraphrased. The destinations themselves are not
  repeated: the "One KB, every agent" matrix already lists every one.
- **Uninstall is described as it is.** D192 item 11 had already changed `install.sh uninstall` to
  refuse while native units remain and to name the teardown; the README states that behaviour,
  including that removing `~/cartographer-data` is a separate deliberate act, because the KBs are
  the user's git repositories.
- **The runbook's entry condition becomes the prompt, not the inputs.** "The user asked you to
  install Cartographer" is what is actually true. The KB remote stays required and is promoted from
  a parenthetical to the first thing the runbook establishes, since on this path it is always the
  missing input.
- **The session restart becomes its own closing step, addressed to the user.** The agent executing
  the runbook *is* the session that must restart; it cannot do it. Step 6 now exists for no other
  purpose, with the sentence to say and the reason. Two rows were added to the failure table for
  the symptoms this path produces: no MCP tools after a successful `connect` (the session was not
  restarted — not a failed install), and `status` exiting non-zero immediately after install (the
  service is still starting; retry once).
- **No new install mechanism and no new command.** This is text and one runbook's framing.

**Invariants kept.** The runbook's refusals are untouched. The raw URL in the README's prompt block
still resolves — it is what people paste. Every command in both files stays runnable as printed
(the D192 standard). `getting-started.md` was reconciled on the two points where it could contradict
the runbook, prerequisites and the restart, and now hands off to the bundled `kb-create`/`kb-import`
skills where the runbook ends.

**Consequences.** Documentation only; no behaviour change and no release impact.
