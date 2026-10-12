---
topic: sync-provisioning
---

# D719 — An unreadable OpenCode version writes the newest plugin shape, with a warning

**Decision.** When `opencode --version` cannot be read, the generated hook plugin takes the
newest shape the generator knows (the highest `HookPluginMajors` entry of the OpenCode
descriptor, today 2) instead of 1.x, and `sync` warns for each such plugin. `doctor` warns
once per provider when OpenCode is configured with a managed hook plugin on disk and the
version is unreadable. The probe reads stderr when stdout carries no version. Amends D359's
unknown-version rule and its "unreadable version is silent" doctor row.

**Why.** OpenCode 2.x rejects the 1.x shape at load and the file looks right on disk, so the
old fallback reproduced the #623 failure with no signal, for any client missing from the
`PATH` of the sync process (desktop app, service `PATH`, probe timeout). 2.x is the current
line, so an unreadable client is most likely a recent one. Cost: a 1.x user whose probe
fails gets a 2.x plugin until `opencode` is on `PATH` and `sync` re-runs.

**Alternatives rejected.** Skipping the plugin on an unknown version: a hook never written
fails as silently as a rejected one, and it drops the hook for every 1.x user whose probe
merely failed. Keeping 1.x with only a warning: leaves the 2.x user without hooks.

**Consequences.** The write-findings hook stays gated on a version actually read as >= 2
(`SupportsWriteFindingsHook`, D362): the fallback chooses a shape for a plugin that must
exist, it does not enable a hook verified only on 2.x. A readable 1.x or 2.x output is
unchanged. Live check on 2.0.25: `opencode --version` prints `opencode v2.0.25` (on both
streams) and the parser reads it; the sync-without-`PATH` run and whether the generic 2.x
plugin's `ctx.event.subscribe` delivers `session.created` remain unverified (see D359).
