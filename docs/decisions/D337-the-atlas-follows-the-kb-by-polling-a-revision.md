---
topic: control-plane
---

# D337 — The Atlas follows the KB by polling a revision

**Decision.** `GET /api/ui/v1/kbs/{kb}/revision` returns an opaque token built
from the link-graph cache's generation (`kb.GraphGeneration`, D294) and a
per-process boot prefix. The Atlas polls it every ten seconds while the tab is
visible and, when it changes, refetches the open view in place, keeping what is
on screen until the fresh answer arrives. The route answers only a caller that
sees the whole KB, like `/status`.

**Why.** A reader leaving the Atlas open while an agent writes saw a stale page
until a reload. The generation already detects every add, remove and content
change, including ones made behind the server's back (a pull, an editor), and
checking it is a stat walk, so a short poll costs little. The boot prefix
exists because the generation restarts at 1 with the process: without it a
restart across a change could return the value the page already holds. The
token counts changes anywhere in the KB, so a narrowed principal would learn
when a hidden collection moves; it gets `404` and no live refresh instead.

**Alternatives rejected.**
- Server-sent events or a WebSocket: a long-lived connection per tab, another
  path through the auth chain and SSO proxies (D308), and a notifier the KB
  does not have, for out-of-band edits it would still have to poll for.
- The git `HEAD` sha: it misses uncommitted out-of-band edits and needs a git
  call per poll, while the generation is already maintained.
- A per-principal revision over only the visible concepts: a full projection
  per poll, to serve the one reader that today has no live view.
- Covering artifacts too: they change through their own tools and are not in
  the graph cache; the Artifacts list still reloads with the rest on any concept
  change, and a change to an artifact alone waits for the next one.
