---
topic: architecture
---

# D1442 — The background role is the Surveyor, the command `cartographer surveyor`

> **Partly superseded by [D1856](D1856-surveyor-rename.md)**: the packages, files, routes, wire fields and ScopeWorker are renamed; the command, the no-alias rule and the gray-area rule stand.

**Decision.** The role D1258 introduced (a process for one perimeter of KBs) is named the Surveyor; its
command is `cartographer surveyor` and its sub-command `cartographer surveyor source test`. Atlas (the UI) and
the Surveyor are the only component nicknames that become artifact names; `server` and `client` stay plain words,
and no other nickname is introduced. `cartographer worker` is an unknown command (exit 2, no "did you mean"):
a hard rename, no alias, no deprecation hint.

**Why.** Nothing released carries the name: the command was added after v0.27.0, and at that tag every `worker`
in code and docs means something else (the hosted demo's Cloudflare Worker, the KB's async push goroutine, the
writer subagents of a doctor session). Renaming before the container image and the Helm chart fix the name in
image names and values costs one change; afterwards it costs a deprecation path.

**What stays (a decision about cost, not about compatibility).** The Go packages `internal/worker/...` and
`internal/workerconfig`, the files `cmd/cartographer/worker*.go`, every Go and TypeScript identifier, the routes
`POST /api/workers`, `POST /api/worker-settings` and `GET /api/ui/v1/kbs/{kb}/workers`, the wire fields `worker`,
`workers` and `worker_template` (and the `field: "worker"` of the two 400 answers), Go comments outside the
command, the docs path and the package doc's first sentence, developer-facing panic and registry messages, and
`ScopeWorker`. `internal/worker/AGENTS.md` and the package doc say once that the package is the Surveyor's. The
files under `docs/decisions/` keep "worker" as written (D1257-D1263, D1373, D1436); only a moved path in D1262
was updated, forced by the cited-path gate.

**The gray-area rule.** A stable contract already released stays; an unreleased, user-visible name is renamed.
Renamed: the KB log paths `surveyor/<name>`, `surveyor/<name>/doctor`, `surveyor/<name>/task/<task>` and the
ledger prefix `[surveyor/<name>] run `, the metric `surveyor_task`, the unit note `> Surveyor note (`, the MCP
`clientInfo` name `cartographer-surveyor`, the 413 texts of the heartbeat and settings report, the
`source_register` description, the text a model reads, the example values (`SURVEYOR_TOKEN`, `surveyor.yaml`,
`cartographer-surveyor`, `surveyor-team-a`) and the Atlas labels. Kept: the settings tab id (component state, never
in a URL) and the CSS classes.

**Alternatives rejected.** An alias for `cartographer worker`: nothing released uses it, so it would only be debt.
Renaming packages, routes and wire fields: large churn and a server/Surveyor compatibility break for a name no user
reads. Reading both log prefixes: that would be an alias in the data.

**Consequences.** A ledger or cadence entry a dev build wrote under the old `worker/` prefix is ignored: that
day's budget restarts once and a prompt task runs once more. A running dev-build process still beating
`/api/workers` is accepted. Released users are unaffected.
