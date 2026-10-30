---
topic: control-plane
---

# D1259 — A worker declares itself by heartbeat, reports typed settings on their own route, and a token can be denied artifact writes

**Decision.** A `cartographer worker` is known to the server by `POST /api/workers`, an HTTP route in the auth
chain and never an MCP tool, and its configuration by `POST /api/worker-settings`, a separate route. The server
keeps both in memory and shows them read-only in the Atlas; it never acts on them. The heartbeat answer carries
the idle signal, `last_activity_at` per KB: the last authorized tool call by a principal with no live worker
declaration, other than `sync_pull`. The settings report is typed scalar `entries` (schema keys, JSON scalars)
and the `kbs`, rebuilt and scrubbed by the leaf package `internal/workerconfig`, never a YAML file. A role rule
gains `artifacts: r`, an allow that grants whole-KB access except artifact writes. This implements design D16,
D17, D21 and D22, keeps D285 (no tool budget spent) and D369 (a declaration is a hint, validated, bounded in
time and ignored when stale), and **amends D948**: Health carries one read-only line saying which worker
maintains the KB and when its doctor last ran, which is upkeep and not a finding.

**Why.** The worker is a separate process and the server stays model-less. A tool would spend `tools/list`
budget on every client for something no agent calls. The settings are constant for the life of the process, so
riding a beat would re-send them thousands of times a day, and a report the server refuses must never take a
worker off the Atlas and out of the idle exclusion. `sync_pull` is not activity because the 30-minute sync timer
calls it on every connected KB, so an idle trigger of 30 minutes or more could never fire. A YAML file can carry
a secret under any key; a typed value cannot, so the server keeps only values that match their key's type.
The existing roles cannot deny artifact writes to a token the worker can use (they sit in the same whole-KB
list as `log_append` and `source_register`), and there are intentionally no deny rules, so the minimal change
is a narrower allow. `internal/mcpserver` must not import `internal/worker`, hence the leaf package.

**Alternatives rejected.** An MCP tool for the heartbeat: spends the tool budget. The settings in the beat:
resent for nothing, and one failure domain. `kb_status` for the idle signal: needs whole-KB read and a
lint-backed aggregate every minute. Counting `sync_pull`: defeats the idle trigger. A deny rule for artifacts:
breaks the order-independence of unions. A rendered, commented YAML in the report (the plan's first shape):
cannot be proved secret-free by shape, so the server accepts typed scalars only and the worker sends no list
elements (`models[]`, `tasks[]`), where API key names and task options live.

**Consequences.** With authentication off the server cannot tell the worker from a person: the answer says
`activity_known: false` and the worker idles on its window only; so it does against a server too old for the
routes (`404`). The worker beats from its own goroutine on a mutex-guarded snapshot, once at start (before the
triggers are read, `--once` included), every 60 s, and once with `state: stopped` on exit; a failed beat or
report is a log line and never blocks a run. It computes idleness from the server's clock (`now`) advanced by
local elapsed time, and counts an answer older than three intervals as unknown. Replicas share a token, hence a
principal, and all are excluded from the signal. What a report may show is decided by `workerconfig.Scrub` and
the key table: a new worker key is shown only if its type and shape say it is safe.
