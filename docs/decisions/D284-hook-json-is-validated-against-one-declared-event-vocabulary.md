---
topic: sync-provisioning
---

# D284 — `hook.json` is validated against one declared event vocabulary, on write, at sync and in lint

**Decision.** `hookEventReach` (`internal/provisioning/hookspec.go`) is the single table of the canonical hook events, each with the providers that fire it. `ValidateHookJSON` reads it: `artifact_write` refuses a `hook.json` that is not valid JSON, lacks `event` or `command`, or has a relative `command` climbing out of the hook's directory, and accepts an event outside the table with a warning; lint reports the same hooks already in a KB (`hook_invalid`, warning). At sync, every registrar turns a `hook.json` it cannot use into an `AppliedResult.Warnings` entry naming the hook and the reason, and the claude and codex registrars consult the table too: an event the client does not fire (`PreInvocation`) is not registered, an event outside the table is registered as declared but warned about.

**Why.** A broken hook was invisible from authoring to sync: the files were materialized and counted (`hook 1/1`) and nothing ever fired. Reject-at-write is the only point where the author can still fix it cheaply; the warning at sync and the lint check cover what arrives by git or was written before. Sync stays tolerant (it never fails `Apply` and still registers an unknown event) because a KB may be newer than the client, but it is no longer silent.

**Alternatives rejected.**
- Reject unknown events only at write and keep sync silent: leaves a hook that arrives by git broken with no signal.
- Reject unknown events at write: the table holds only the events this repository encodes, so a real client event it lacks would become unwritable; a warning catches the typo without that cost.
- Make sync refuse an unknown event: a KB carrying an event this client's table predates would lose a hook that works on the real client.
- Fold `openCodeHookEvents` and `antigravityHookEvents` into the table: they carry the client mapping, not a yes/no; a test pins that the two agree with the table instead.
- Reject an event that some connected client will not fire: a hook may deliberately target one client. The write result lists the clients that will not fire it instead.
- Guess the events of a client that the repo does not already encode (e.g. Claude Code events beyond the ones here): out of scope; adding one is a line in the table.

**Consequences.** The vocabulary is only as wide as the events the repository already encodes for a client (`SessionStart`, `UserPromptSubmit`, `PreToolUse`, `PostToolUse`, `Stop`, `SubagentStop`, `PreCompact`, `PreInvocation`, `PostInvocation`); a legitimate client event missing from it (Claude Code's `Notification`, for one) is accepted with a warning on write, and by lint, until it is added to the table (and to the registrar's mapping if that client needs one). Adding a provider's mapping means updating both the table and that provider's map. A `hook.json` missing from a hook directory is now also a sync warning.
