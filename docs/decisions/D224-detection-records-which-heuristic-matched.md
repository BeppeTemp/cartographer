---
topic: client-configurator
---

# D224 — Detection records which heuristic matched, and the evidence is labelled with it

**Decision.** `agents.Agent` carries `DetectedBy`, one of five named heuristics
(`binary`, `config-dir`, `env-config-dir`, `provider-root`, `app-dir`), set by
the same probe that fills `Evidence`. `cartographer agents` prints it as a
DETECTION column next to EVIDENCE, `--output json` as `detected_by`, and the TUI
uses it as the label of the evidence line instead of the hardcoded `binary`.
`Installed` keeps its current meaning, and every caller that reads it — the
`connect` target set above all — is untouched.

**Why.** All five heuristics collapse into one boolean, so a live client and a
directory a removed client left behind are indistinguishable downstream (#305:
a Windows machine with no `claude` binary anywhere, `%USERPROFILE%\.claude`
still on disk, reported installed and provisioned with the full artifact set).
The directory probes are not the defect — a client installed as an application
with nothing on `PATH` is found only that way — so the fix is to stop discarding
which probe answered. The cost is a field that every future heuristic must
name, and one more column in a table that already carries four.

**Alternatives rejected.**

- *Trust only the binary probe.* Deletes the application-install case the config
  and app-dir probes exist for (D216), turning a reporting defect into a
  detection regression.
- *A confidence score (strong/weak).* Ranks the heuristics inside the package,
  where the ranking is not knowable: how much a config directory is worth
  depends on the caller. A name lets each caller decide; a score decides for it.
- *Encode it in `Evidence` as free text.* Keeps the string the only output,
  which is what made it unparseable in the first place.
- *Narrow `connect`/`connect all` to binary-backed providers in the same change.*
  That is a behaviour change to what gets written into people's home
  directories, with its own failure mode (a provider that was being configured
  silently stops). It needs its own decision, and this field is the evidence it
  will be argued from.

**Consequences.** A positive detection is now made in one place,
`Agent.found`, so the three fields cannot drift apart; a heuristic added without
a value of its own fails `TestHeuristicsAreDistinct`. `providerStatus` gained an
optional `detected_by` — additive to `cartographer.status/v1`, absent when
nothing was detected. The DETECTION column is sized on the widest value it
prints, like PROVIDER (#303), so the table stays narrow on a machine where every
answer is `binary`. Anything that later consumes the field to decide *what to
write* must state that in its own decision: reading it changes nothing, acting
on it does.
