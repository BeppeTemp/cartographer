---
topic: client-configurator
---

# D1659 — Cline hooks run through one internal command: `cartographer hook dispatch`

**Decision.** The logic of Cline's hooks is the internal subcommand `cartographer hook dispatch --provider cline
--event <Event>`, in `internal/provisioning` (`DispatchClineHooks`). It reads Cline's payload on stdin, runs the
Cartographer hooks of the event as Claude-style `sh -c` commands, and answers in the three fields Cline reads
(`cancel`, `contextModification`, `errorMessage`) as one line of ASCII JSON. It always exits 0: an unknown
provider, event or flag, a bad payload and a panic print `{}`. The files that call it (one dispatcher per event
in `Documents/Cline/Hooks`) and the hook cell are the next change; this one leaves the cell unsupported.

**Why.** Cline's VS Code extension loads one script per event name from `Documents/Cline/Hooks` and runs it for
every extension and KB alike, so the generated-file-per-hook model of Pi cannot be used: the file stays a thin
shim and the logic lives in a command that can change without rewriting the user's folder. Mapping follows what
the bundle does with the result (4.1.26, read, not run): `cancel` stops the whole task, so only a `PreToolUse`
exit 2 produces it and a `PostToolUse` exit 2 is context (a stopped task would lose the agent's write); `TaskStart`
stands for `SessionStart`; a tool `<server>__<tool>` is presented as `mcp__<server>__<tool>` because that is the
form KB matchers expect. The total run stays under 25 s, kill grace included, because Cline kills a hook at 30 s; each hook's stdout and stderr are capped at 1 MiB. The output is ASCII
only because on Windows the `.ps1` re-encodes the native command's output.

**Alternatives rejected.**
- One generated script per hook (Pi's model): two hooks of one event would write the same path.
- Putting the logic in the generated file: it would be rewritten in a folder shared with the user at every change.
- A non-zero exit or a structured error for a bad payload: a hook must never break a session
  (`cmd/cartographer/hook.go`).
- Dropping the `TaskStart` dedupe: `TaskStart` may fire per turn (probe P1 is open) and the bootstrap hook runs a
  sync; a marker file per task, 12 h, is created with `O_EXCL` and removed when stale. It lives in a per-user
  directory of the temp dir (`cartographer-cline-<uid>`, mode 0700, never a link, owned by the user), because a
  predictable name in the shared temp dir could be pre-created by another local user to silence the sync; an
  untrusted directory fails open (the sync runs). The marker is taken only after `sh` is found.

**Consequences.** Probes P1-P3 (the `TaskStart` cadence, the shape of `postToolUse.result`, the `PreToolUse`
cancel) were not run, so the docs say so and the write-findings hook is not claimed for Cline until P2. Registration,
ownership of the dispatcher files and the Documents-folder resolver belong to the next PR and its decision.
