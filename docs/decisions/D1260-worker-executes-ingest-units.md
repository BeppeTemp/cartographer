---
topic: control-plane
---

# D1260 — The worker executes ingest units: tier from the model profile, skip-not-fail, fetch limited to the unit's Source

**Decision.** The `ingest` task kind takes the next claimable `IngestUnit` the task's model profile may execute: the
profile's `tier` decides (`small` takes `executor: any`, `large` also `large`, `human` never), there is no `model.tier`
key. A unit whose Source needs a connector the worker lacks is skipped, never failed. The built-in `source_fetch`
takes no argument and fetches only the locator of the claimed unit's Source, over http(s) only in this step.

**Why.** `executor` is the planner's statement of what a unit needs; the config says what this worker's model can do, and a
missing key must not let a small model take a unit marked `large`. A skipped unit stays untouched for a worker that has the
connector, whereas failing it would block or burn it. A page the model reads is untrusted text: a tool that fetched any
URL could be told by a page to send something to one. The worker, not the model, decides done or released, with
`gate_check` over the pages the loop wrote.

**Alternatives rejected.** A `model.tier` key on the task: a missing key would default to the wrong side. A `url`
argument on the fetch: an injection channel. Git clone in the same step: it needs a checkout lifecycle and a
file-reading tool, so it comes with the MCP connectors. Trusting the model's own `done`: the worker re-checks.

**Consequences.** A new `Binder` hook gives an `Instance` the config and the profile of its entry. The skip set is
in memory and expires by time (the runner has no run boundary), so a restart re-evaluates it. A blocking
Contradiction written by the loop is the unit's output and does not fail its check. The lease (`review_after`, a day
granular date) stays the crash net only. `fetch.allow_private` is the one config key that widens the fetch.
