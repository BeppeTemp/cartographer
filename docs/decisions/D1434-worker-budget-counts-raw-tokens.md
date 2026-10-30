---
topic: architecture
---

# D1434 — A worker's budget counts raw tokens, its context has its own limit

**Decision.** `daily_tokens` and `run_tokens` count input and output as the API reports them, cache reads in full, on every provider. Each profile also declares `context_tokens` (default 32 000): the loop tells a run once to close and stops it before a prompt that would not fit.

**Why.** The worker is meant to run on small open-weight models: a test on a cloud model must count what the local run will count, or a tuned budget breaks on the machine it was tuned for. The cost is that a budget overstates a cloud bill when the cache hits; dollars are computed from the metric line, outside the worker.

**Alternatives rejected.** Weighting cache reads: the weights differ per model and the OpenAI adapter has none. A provider task budget: beta and Anthropic only. A default window as large as a cloud model's: a small model would fail at runtime, while a large one only has to declare its window.

**Consequences.** Prompt caching stays an optimization, nothing may depend on it. The closing notice is plain user text in the turn that carries the tool results, so it works on both adapters and keeps the conversation append-only. A turn cut by the output limit is dropped, because its tool calls may be incomplete JSON, and retried once. A kind whose prompt cannot fit in 32 000 tokens needs a declared window or a smaller shape.
