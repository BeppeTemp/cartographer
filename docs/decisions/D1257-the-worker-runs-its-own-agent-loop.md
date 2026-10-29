---
topic: architecture
---

# D1257 — The worker runs its own agent loop over an allowlisted MCP client

**Decision.** A worker is a headless MCP client of the public HTTP contract plus
its own loop in Go (`internal/worker`) that calls the model API directly: the
Anthropic Messages API and OpenAI-compatible chat completions, both over
`net/http` and `encoding/json`, non-streaming. The model only sees an allowlist
(`allow.go`): an explicit default list of read tools and content writes, minus a
hard deny set that no caller-supplied list overrides. The deny set is every
`artifact_*` tool (by prefix, so a future one is denied unchanged),
`concept_delete`, `repair_revert`, `concept_transfer` (D9) and four names added
here for the same intent: `secret_resolve`, `secret_set`, `skill_install`,
`git_conflict_resolve`. A tool outside the set that was offered for the token is
never sent to the model, and a call naming one is refused locally with no
request to the server. The loop is bounded by mandatory step and token caps (a
zero value takes a default, never "unlimited"); tool-level errors and
invalid-params refusals go back to the model, transport errors abort the run.
System instructions are a parameter: the loop executes no command, script or
shell, and reads nothing from the KB (D6, D15).

**Why.** Instructions and code a worker acts on must come from a trusted source,
and everything the model returns is untrusted, so the model-facing half of the
defence (D17 adds the server-side half) has to hold on its own and be pinned by
a test that fails when the server renames a tool. A registry test checks every
name of the default list and of the deny set against `mcpserver`'s tools.

**Alternatives rejected.**
- Wrapping a headless client CLI (`claude -p`, `codex exec`): needs the CLI in the
  image, subscription auth, and gives weaker control of the tool set.
- A vendor SDK: one SDK covers one of the two wire formats, the OpenAI-compatible
  one is what local endpoints implement, and the subset used is about 150 lines
  per provider, so D1's bar for a dependency is not met; no `go.mod` change.
- A denylist-only policy: a tool the server adds later would reach workers
  silently; the explicit list keeps it invisible until someone names it.

**Consequences.** Token usage is reported per call (estimated as bytes/4 when an
endpoint omits it) so the daily budget of #1258 cannot be bypassed by a local
server; the per-task cap stops a run without executing the calls of the turn
that crossed it. `Outcome.Refused` and `Outcome.Writes` exist for the worker's
audit line. The four added deny names and the default list are the one place to
change when a worker legitimately needs one of them. The package is a library
until #1258: `internal/worker` stays in `scripts/ci-filters.sh` `not_in_binary`
until the binary imports it.

Deviations while implementing: the plan's `feat(worker)` title scope is not in the
`pr-title` list, so the PRs carry no scope; the work shipped in three PRs (#1382,
#1385, then the allowlist and loop in this one) because the plan exceeded the size cap.
