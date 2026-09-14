---
topic: sync-provisioning
---

# D54 — Per-server trust persisted at `connect` (replaces the recurring `--auto-trust` gate)

**Context.** Artifacts from the KB arrive `signed:false` (D40): without `--auto-trust`, every
`sync`/`connect` leaves them in `needs_approval`. The TUI did not expose `--auto-trust` at all, so
from there they stayed pending forever — in a single-user context, "re-approving" at every sync is pure
friction that verifies nothing.

**Decision.** Trust becomes an explicit choice made once at `connect`, persisted:
`clientconfig.Config.Trust bool` (default `true`), toggle in the connect form shared CLI/TUI.
`sync`/`status`/TUI use `cfg.Trust` as default; `--auto-trust` remains a one-off override
(`cfg.Trust || --auto-trust`) at materialization time. `status`/TUI never mutate `Signed`: they
report a separate `trust` state per artifact (`snapshotArtifacts`, D115) that reads `cfg.Trust`
read-only to report `trusted` instead of `needs_approval`, so as not to show a false pending
approval without pretending the artifact was cryptographically verified. No symmetric
`--no-trust`: revoking is rare, manually setting `trust: false` in `.cartographer.yaml` is enough.
**Rationale.** The signature placeholder (D40) stays identical server-side; this decision concerns
only who/when decides to trust (the user, at connect, persisted — not relitigated at every sync).
**Discarded alternatives.** A symmetric CLI `--no-trust`: no real use case, it would only have
doubled the flag surface to maintain.
Details: `docs/configurator.md` §`.cartographer.yaml`, §`cartographer connect`.
