---
topic: client-configurator
---

# D1663 — Cline hooks are wired through an owned dispatcher in the Cline hooks folder

**Decision.** A connected Cline gets one Cartographer-owned file per needed event in `Documents/Cline/Hooks`
(`<Event>` on POSIX, `<Event>.ps1` on Windows), a shim for `cartographer hook dispatch` (D1659). The hook's own
files live in `~/.cline/cartographer-hooks/<name>/`. Ownership is the token `cartographer:cline-hook-dispatcher
<Event>` in the first 512 bytes, never a lock entry; the set of dispatchers is derived from the hooks on disk
(`reconcileClineDispatchers`), so the bootstrap, write-findings and KB hooks share one path in any order and a
prune removes a dispatcher only with the last hook of its event, and only while it still carries the marker; no folder is ever removed, `Cline/Hooks` belongs to the extension. A file
at the path without the marker is the user's: it is never rewritten, merged or read past its head, the hooks are
installed but not wired, and the sync warns (a symlink there is reported in `safewrite`'s refusal wording). For the
bootstrap hook that warning is fatal (`warningBlocksBootstrap`): `TaskStart` would never run it, so it must not be
recorded as installed. An owned file is rewritten with its mode kept (`chmod 644` is the
user's per-hook toggle in Cline's UI). Claimed events: `SessionStart` (as `TaskStart`), `UserPromptSubmit`,
`PreToolUse`, `PostToolUse`.

**Why.** The VS Code extension reads one script per event name from that folder only, and runs it for every KB and
extension, so a generated file per hook would collide. Nothing in the payload or the environment names the client,
so the dispatcher is a machine-wide, client-agnostic file; the design therefore refuses to touch anything it does
not own and fails open (a missing binary prints `{}`, the script exits 0).

**Alternatives rejected.**
- A lock entry per dispatcher: it is shared by every hook of its event, so one hook's prune would delete what another uses.
- Overwriting or merging into a user's own file of that name: it is not ours to change; skip and report instead.
- Home-relative `Documents` everywhere: a OneDrive-redirected Windows folder would leave the hooks dead. On Windows,
  for the real home, the known folder is used; every mkdir, write, remove, prune and the registration-target check go through `safewrite` rooted at the
  Documents folder, not the base dir: the folder itself may be a link of the user's (exempt as a root, also when
  the base dir is not its parent) while a link at `Cline` or `Cline/Hooks` is refused before anything is written: on sync a warning (the event is skipped, the remove path
  included), never an error.

**Consequences.** The binary path is quoted for PowerShell by doubling the ASCII quote and U+2018, U+2019, U+201A and
U+201B, which PowerShell reads as quotes too. `clineDocumentsDir` is the one place a Cline destination leaves the base dir (a relocated Linux
`xdg-user-dir` is not replicated: documented). A `PreToolUse` block stops the whole Cline task (it has no per-call
skip), so a KB guard hook is fail-closed at that price. The write-findings hook is not claimed for Cline until probe
P2 shows what `postToolUse.result` carries for an MCP response; probes P1-P3 are not run. The Cline `hook.*`
capability cells are `unverified`, in the table and the ledger together. A dispatcher already written outlives a
revert until removed by hand.
