---
topic: architecture
---

# D1374 — Prompt tasks run the operator's instructions, guarded by the worker

**Decision.** The worker has a task kind `prompt` (D23): a `tasks` entry with `name`, `model`,
`instructions_file`, `tools`, `every` and an optional `kbs`. The instructions are a file inside the
directory of the worker config, read once at load (at most 16 KiB, through `os.Root`, so nothing
outside the directory is ever read), never taken from a KB (D15). `tools` can only narrow the
default allowlist; the deny set always wins and nothing is implicit. The cadence is stateless (D13):
after each run that reached the model the worker files a marker, `task=<name> at=<RFC 3339> day=
stopped= writes=`, on its own log path `worker/<perimeter>/task/<name>`, and the task is due when
`every` has elapsed since the newest marker. A call guard around the MCP caller sets the `Reason`
trailer (`prompt task <name>`, worker-set, only on tools whose schema declares `reason`) and fences
the task's `log_append`: its path is forced to `task/<name>` and an entry with a `## ` line or the
text `kb-doctor` is refused. A task starts only with a grant of at least 8 000 tokens; under it,
nothing is written and the task stays due. There is no lock between replicas.

**Why.** An operator's recurring chores (a weekly digest, an owner check) have no shipped kind, and
typing them into a client is the manual step the worker exists to remove. The marker is on its own
path because the budget ledger's path prefix `worker/<perimeter>` is matched on the bracketed
prefix, so a marker can neither count as spend nor hide any, and because the runner's closing entry
has day granularity and `log_tail` has no time filter: a week-long `every` would fall out of any
fixed read. The log is one file where any `## ` line forges an entry, so a model must not write
freely into it.

**Alternatives rejected.** Instructions from a KB page or asset: a KB would select what the
worker executes (D15/D6). A kind that runs a command: the model has MCP tools only, code the
worker runs is an `exec` recipe (D19). A cron field instead of `every`: a duration needs no
parser and no calendar. A server-side schedule: the server stays model-less (D14). Re-reading the
file per run: a half-updated ConfigMap would become a live prompt. A token inside the runner's
closing entry as the marker: day granularity, and it would mix with the budget read. A lock between
replicas: not stateless (D13); the overshoot is bounded by `replicas x grant` and the documented
instructions are idempotent.

**Consequences.** A task that lists `concept_write` can overwrite any page its token can write;
the client-side guard cannot tell a harmless call from a harmful one. The fences are the operator's
`tools` list, the token's scopes, `if_match` where the instructions use it, and D11: every commit
carries `Reason: prompt task <name>`, so a pass is findable and revertible commit by commit.
Renaming a task or the perimeter restarts the cadence (one extra run). The marker on shutdown goes
through the runner's normal caller, not the 5 s close caller, because a kind does not receive that
one. The kind lives in package `worker` itself (`prompt*.go`), not a subpackage, because the
registry `DefaultKinds` is in `worker` and a subpackage importing `worker` could not be linked from
it. Nothing under `worker/` in the log is written by a model.
