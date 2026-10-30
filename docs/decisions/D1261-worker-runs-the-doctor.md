---
topic: control-plane
---

# D1261 — The worker runs the doctor, delegated, and only a closed session writes `kb-doctor`

> **Partly superseded by [D1436](D1436-the-worker-doctor-works-one-item-at-a-time.md)**: the worker no longer runs the skill or writes the marker, and it acts on its queue, not on `doctor_suggested`, as far as the deployment's `doctor_allow` lets it.

**Decision.** The `doctor` task kind of `cartographer worker` runs a `cartographer-kb-doctor` session on each KB of its perimeter whose `kb_status.conformance.doctor_suggested` is true. It is the D704 exception of D18 (the operator deploying the worker is the asking) extended to a delegated session (D305) and nothing more. The session's mode is stated by the worker's own prompt (preamble plus the skill embedded in the binary), never by the server: `doctor_mode` (D358) and the `assisted` capability claim of D704 are gone since D907. Artifact steps of the skill become gaps in a worker session. A session that did not reach its closing step makes the worker write one entry without `kb-doctor`, so `last_doctor` does not move and the debt stays visible.

**Why.** The skill already defines delegated (D305); a headless session needs only to say "delegated" and who asked. `last_doctor` is the newest log entry containing `kb-doctor`: a marker written for a half session would silence the proposal for 14 days with the debt still there.

**Alternatives rejected.** A server-side autonomy switch (D14, D305: who chooses is a property of the session). The worker writing the marker on every run (hides unfinished work). Adding `artifact_write` to the worker for the doctor (D9, D17). A threshold of the worker's own (the server's 14-day interval is the only one).

**Consequences.** The doctor allowlist is the default one plus `kb_review` and `kb_repair`; the deny set still wins. Every text the worker writes (interruption entry, run summaries) goes through `noMarker`. The operator undoes a bad pass per commit with `cartographer kb repair <kb> --revert <sha>`. The doctor is a task kind placed after the ingest kinds in `tasks`, so it only gets what they leave; one session per KB per run.
