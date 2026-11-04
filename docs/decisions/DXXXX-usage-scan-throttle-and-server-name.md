---
topic: client-configurator
---

# DXXXX — The usage scan is throttled and runs off the client lock, and `server_name` is a plain identifier

**Decision.** `sync` scans the session transcripts at most once every 6 hours,
with the time of the last scan kept as the mtime of `.cartographer-usage-scan`
in the client state directory, and runs the scan after releasing the client
lock. The emitters refuse a server name that is not letters, digits, `_` or `-`,
and `Apply` replaces a server's whole entry instead of merging into it.

**Why.** The scan rereads every recent transcript and ran at every agent session
start with the lock held: a few seconds each, and the fourth concurrent session
timed out on the lock. The server keeps only "last use", so hours of lag change
nothing it shows. A name with a space, quote or dot made the Codex `config.toml`
unparsable, and the one alphabet makes the JSON and TOML emitters agree.

**Alternatives rejected.** An incremental scan with per-file state (exact, but a
newly managed skill still needs a full rescan, and the state needs invalidation
rules). A `--no-usage` flag on the bootstrap hook's sync (changes the generated
script of every provider and forces a re-materialisation). Quoting the TOML key
and keeping any name (the name stays exotic as a tool prefix and a URL part).

**Consequences.** A skill used within the last 6 hours may reach the server one
window later; `sync --report-to` always scans. An existing `server_name` outside
the alphabet now fails at the next sync instead of producing a broken entry.
Anything else a `sync` reports after the apply belongs after `unlock()` too.
