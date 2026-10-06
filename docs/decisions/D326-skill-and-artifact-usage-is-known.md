---
topic: control-plane
---

# D326 — Skill and artifact usage is measured by the client from its own transcripts

**Decision.** `cartographer sync` scans the local session transcripts of the clients it
manages, read-only, for evidence that a materialised skill or agent was loaded, and posts only
the aggregate (`name, kind, provider, source, last_used, count`) to `POST /api/usage` of the
KB the artifact came from. The server keeps it in `.cartographer/usage.json` (local, never
committed) and shows it as the `artifact_unused` lint (info, default 42 days, `usage_stale_days`
per KB, `0` disables), the `usage` section of `kb_status`, and a "Last used" column in the Atlas
Artifacts panel. `usage_scan: false` in `.cartographer.yaml` switches the scan off.

**Why.** Cartographer had no signal for whether a skill is used: twelve of fifty had never been
read and two were broken, and it took a manual sweep of transcripts to find out. The server's
audit trail cannot answer it: a skill reaches a client as a file `sync` materialised, and the
client reads it from its own disk, so the server never sees the load. Only the client knows the
path it wrote and holds the transcript that shows it being read. Reading the operator's own
transcripts is a local, deterministic, read-only operation on their own data, unlike the
release-feed polling D292 rejects.

**Alternatives rejected.**
- Counting `artifact_read` calls in the audit trail: `artifact_read` is how agents read KB-root
  files on demand, not how skills reach them. It would report every skill as unused.
- Sending transcripts, or message-level evidence, to the server: the aggregate is all the
  question needs, and a prompt must never leave the machine for it.
- Matching a skill's name in transcript text: a mention in prose is not a use. Only a tool call
  that names the materialised path, or an explicit activation event, counts.
- Incremental scanning with a `last_scanned` mark: `count` is replaced, never summed, so a
  partial scan would report a falsely low count; a cold scan of a 90-day window, with a cheap
  reject before any JSON decoding, costs less than the state to keep correct.
- Counting a Codex catalogue load as use: Codex lists every skill in every session, so every
  skill would look alive forever. It is recorded with `count` 0 and reported as "catalogue
  loaded, never seen activated".
- Failing or blocking `sync` on a scan or report error: the signal is a convenience; a server
  too old for the route, an unreachable one or a malformed transcript ends in a debug line.
- Putting `usage_stale_days` in a `_cartographer.yaml` inside the KB: no such file exists. The
  other per-KB operator settings (`doctor_interval`, `auto_repair`) live in the server's
  `kbs[]` entry, which is where the operator who runs the server already looks.

**Consequences.** The scanner reads only tool-call records and skips everything else without
keeping it; a test asserts that user messages, assistant prose and Codex message items never
count. A transcript format is undocumented and may change: an unknown shape is skipped, never
fatal, and `docs/harnesses.md` lists each format as a dependency with a watch item, since a
renamed field makes a client's skills look unused without any error. Providers with no readable
transcript (opencode, hermes, antigravity, crush) contribute nothing and are explicit stubs, so
adding one is a one-function change; `artifact_unused` is silent until some client has reported,
so absence of a signal is never reported as disuse. Usage is per server, never synchronised
between machines. The route is the one HTTP write that is not an MCP tool; it needs the whole KB
in write scope and answers `404` otherwise.
