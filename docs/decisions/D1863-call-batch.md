---
topic: client-configurator
---

# D1863 — `cartographer call` runs many requests through one bridge, and asks for the guide with `Bridge.Guide`

**Decision.** `cartographer call --batch -` reads strict JSON lines (`tool`, optional `arguments` and `id`) from stdin and runs them through one `mcpbridge.Bridge`, one request at a time, writing one JSON result line per input line in input order as each is ready. A failing or malformed line yields `ok:false` with `error.code` (1 the tool or its arguments, 2 anything else) and the batch continues; the exit code is the worst class among the lines. `--instructions` prints `initialize.instructions`, and `--describe a,b` several schemas from one `tools/list`. `--instructions` builds the bridge with `Bridge.Guide`, which leaves the `provisioned=1` marker out of the routed URL so the server sends its operating guide.

**Why.** An agent turn of N calls paid process start, config, scope and a bridge N times (#1805). One bridge removes that without a second scoping model: scope, token, author rule (a read names no author) and the one-shot hint stay in the bridge. Requests are sequential because a batch is a script: a write must be seen by the read after it, and ordered output needs no reordering buffer. D1097 withholds the guide from a session declared provisioned, and the bridge always declares it, so the guide could not be read through `call` without a switch on the bridge.

**Alternatives rejected.** Parallel execution: reorders results and breaks write-then-read; a later `--jobs` can add it without changing the line shape. Stopping the batch at the first failure: an agent loses the rest of its turn for one bad line. Typing `key=value` words in a batch line: arguments are verbatim, so a batch costs no `tools/list`. Dropping the marker for every `call`: a normal call has a managed instructions block and must keep D1097.

**Consequences.** Extends D1658: "one stateless request" becomes one or more requests through one bridge, with its invariants kept: the pipe stays open until the last response, the command writes no file anywhere and works with a read-only `$HOME`, no client-side validation beyond the line shape. The result line shape is a contract for agents.
