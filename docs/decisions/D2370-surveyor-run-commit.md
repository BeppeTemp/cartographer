---
topic: concurrency-git
---

# D2370 — A Surveyor run is written as a commit trailer

**Decision.** A Surveyor sends the id of the run a write belongs to in the request header `Cartographer-Surveyor-Run` on `tools/call`; the server, when the header holds exactly one value that satisfies `componentproto.ValidRunID`, writes it as a `Surveyor-Run: <id>` trailer, right before `Cartographer-Host`/`Cartographer-Token`, and `changes_since` returns the distinct ids per concept as `surveyor_runs`. The id is a label: the server never uses it to allow, deny or attribute anything.

**Why.** The Atlas matched a run to the pages it changed by time and author, which a later edit of a person, a skewed clock or the newest-change-only listing of the changes route all blur. The trailer makes the link exact and rides on the read the Atlas already makes (a bounded window, no new history walk). It is a header and not a tool argument so that no tool schema or cost-budget golden changes; it is additive, so `componentproto.Protocol` stays 1 and a mixed pair (old Surveyor or old server) simply carries no trailer and the Atlas falls back to the heuristic.

**Alternatives rejected.** A tool argument or a `_meta` key: it changes every tool's schema. Gating the trailer on the token kind: with authentication off the Surveyor cannot be told from a person, and the field is only a hint. Rejecting a write with an invalid header: the author header decides a commit's identity and so warns; this one decides nothing, so an invalid or repeated value is dropped silently. Extending `FileHistory` and `concept_history`: nothing reads it there.

**Consequences.** Any client with a token can send the header, so a trailer proves nothing about who wrote the commit: a reader must not trust it for authorization (`TestSurveyorRunNeverAuthorizes` pins the identifiers to the files that write and read it), and the Atlas also requires the commit's author to be a Surveyor author of the run's replica. A valid id holds no whitespace, so it cannot inject a newline or a second trailer; a caller copy of `Surveyor-Run:` in a subject, reason or extra trailer is stripped with the token trailers, and gitx reads the last value. The `logNameStatus` format and its field count change together.
