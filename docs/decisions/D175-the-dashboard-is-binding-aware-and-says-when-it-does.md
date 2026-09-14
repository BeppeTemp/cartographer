---
topic: client-configurator
---

# D175 — The dashboard is binding-aware, and says when it does not know

**Decision.** The dashboard stays one list screen and gains four things: a `kbs`
line per provider carrying its binding and the binding's origin, a `kinds` line
carrying the per-kind breakdown `cartographer status` already printed, a server
panel broken into a labelled block whose KB inventory counts bindings per KB,
and a footer, a sync-all progress report and a disconnect confirmation that
name what they act on. Every provider detail line goes through one grid helper.

**Rationale.**

- **The screen could not answer the question it exists to ask.** With four KBs
  mounted every provider rendered identically — three lines, all `in-sync` —
  and the KB list appeared once, as a property of the server. Which provider
  receives which KB was unanswerable, and D170 made the answer differ per
  provider. The binding belongs on the provider, next to what it explains.
- **The three binding states get three sentences.** `BoundKBs` distinguishes
  absent, empty and populated; rendering the first two as an empty list would
  make "receives every known KB" and "receives none" look the same, which is
  the exact confusion D169 introduced the resolver to prevent. So: the names
  with `(explicit)`, `all known (default)`, `none (explicit)`.
- **The TUI showed strictly less than the CLI, and the comment claimed
  otherwise.** `formatKindStatus`'s doc comment named the dashboard as a caller;
  it never was one. The breakdown was already on the snapshot both renderers
  read, so this is a rendering omission, not a new computation — and the fix is
  reading the field, which is also why the two cannot now drift.
- **"Not measured" is not "fine".** A breakdown is only shown for a provider
  whose manifest was actually read: `remoteStatusMsg.kinds` omits a provider
  whose state came back `unknown`, and the row renders `unknown` rather than
  keeping the previous poll's counts. A dashboard reporting `in-sync` computed
  against a manifest it could not fetch is worse than one that admits it does
  not know. The mirror case is kept apart: a manifest read and found empty is
  `no artifacts`.
- **The count in the server panel is a count of bindings.** Not sessions, not
  proof a sync completed — the label says `bound` for exactly that reason. A KB
  the server mounts and nobody consumes (`kb-tre (0)`) is the most useful fact
  the panel can carry, and it can only carry it if the number means one thing.
- **No new colours, no second pane.** The dashboard is a quick-inspection tool;
  depth belongs to `status` and `doctor`. Another badge in another colour makes
  the screen less readable, not more, so the existing styles are reused and the
  work went into the grid instead.
- **Progress reporting must never be able to stall a sync.** `S` was already
  sequential under one lock (D172); it now announces each provider on a channel
  buffered for the whole run, so a send cannot block while the lock is held. A
  progress message arriving after the outcome is dropped rather than allowed to
  overwrite the result the user needs to read.
- **The confirmation names the KBs.** "Removes managed artifacts" does not say
  what leaves the machine, which is the only thing a destructive confirmation
  is for.

**Deviation from the plan.** The plan folded the breakdown under `artifacts` as
a second, unlabelled line. It became a labelled `kinds` row instead: an
unlabelled `unknown` floating under a status is ambiguous about *what* is
unknown, and the two-column grid the plan calls the reason the screen is
scannable is what makes the label cheap.

**Invariant preserved.** The first frame is still built from local data only —
the binding is resolved in `buildRows` through `Config.BoundKBs`, alongside
agent detection and mcp-config presence. Nothing added here waits on the
network.

**Consequences.** Presentation only; no behaviour outside the dashboard
changes. The server panel's single line became four, and `ready=ready` became
`· ready` — cosmetic, but visible to anyone reading a screenshot. The `S`
behaviour change is the one inherited from D172 and should be described as such
in the release notes.
