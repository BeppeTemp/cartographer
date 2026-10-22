---
topic: control-plane
---

# D1097 — `initialize` carries the operating guide; a provisioned client marks itself

**Decision.** Every connection that is not marked provisioned receives, as the `instructions` of
`initialize` and of `server/discover`, a guide built from one server-side generator: per KB of the
connection's effective set its routing sentence and the archives the caller may see, the client-neutral
operational bullets once, then each KB's `instructions.md` behind its scope sentence. A client that has
the managed instructions block says so with `?provisioned=1` on the routed URL and gets exactly what it
got before. `cartographer connect`/`sync`/`reconnect` write the marker into every routed entry and
`mcpbridge` adds it when it forwards.

**Why.** The server alone must be usable by an MCP client with no Cartographer client side (an SDK loop,
a desktop client): `initialize.instructions` is the only protocol channel for "how to use this server",
and the text existed only client-side. The guide is produced by editing the `initialize` and `discover`
results per handshake, not at build time, because the SDK server is memoised and its routed views are
shared by every URL that resolves to the same set, while the text depends on the caller and on files read
from disk (an `artifact_write` or a pull shows up at the next handshake, no restart). The marker travels
on the request context, not in the view cache key: the guide is per handshake, the doctor nudge of
plan #1101 runs on the target KB's server where only the context reaches, and a flag in the key would
double the cached views.

**Alternatives rejected.** Setting `Instructions` when the SDK server is built: stale and caller-blind.
A marker in the view cache key: doubles views for no gain. A `/health` capability field or a response
header for the marker: the server ignores unknown query parameters, so a client can always send it, and an
older server has no guide to suppress. Sending the guide to everyone: a provisioned client would read the
same text twice. Counting pages per archive: nothing read the count, and it walked every archive on each
`sync_pull`. Two generators: they would drift.

**Consequences.** The managed block is byte-identical (golden tests pin text and `ContentHash`), so no
client re-syncs because of this. The neutral part has a byte budget pinned by a test (paid by every
handshake); curated bodies are not capped. A guide never names a KB the caller cannot read nor an archive
it cannot see; the one whole-KB file it carries is `instructions.md`, a deliberate choice (the KB's own
directions to whoever works in it). `instructions.md` and `data/` that are symlinks are not read. An older
client against this server sends no marker and receives the guide on top of its block until it syncs with
this release's binary (the SessionStart hook's `sync` heals it). The block's own reader still follows a
symlinked `instructions.md`; not changed here because the block must stay byte-identical.
