---
topic: deployment-release
---

# D862 — The Atlas version opens the release history, fetched by the server on demand

**Decision.** The Atlas gets a release history: the version in its top bar and
in the Settings title opens a sheet with every release, newest first, its
changelog, the one this server runs marked, and, when a newer release is known
and the install channel names a command, how to upgrade. The sheet is the UI
half and lands in its own PR (#879); this one ships the data behind it,
`GET /api/ui/v1/releases`. The server reads it from the GitHub release list
(`updatecheck.Releases`) when someone asks, keeps it in memory for an hour
(conditional on the ETag, one fetch at a time) and serves the last good list,
flagged `stale`, when a refresh fails.

The fetch obeys the update-check switch: `update_check: false`,
`CARTOGRAPHER_NO_UPDATE_CHECK` or a `dev` build leave the route without a
source, and it answers `unavailable: "disabled"` without a network call
(`serverUpdateEnabled`, shared with the daily check). It is bounded: at most 3
pages of 100 releases, each response read through a 4 MiB limit, each changelog
cut at 64 KiB (`truncated`), 10 s per page request (so a full 3-page history can
take up to 30 s).

**Why.** The user wants the whole history, including the releases newer than
the binary, and a changelog is the only thing that says what an upgrade brings.
The browser cannot ask GitHub itself (the Atlas CSP is `connect-src 'self'`), so
the server does. An operator who turned off the one outbound call the update
check makes must not get a second one, which is why the history follows the same
switch, and why it is fetched on demand and not on a timer: a server whose
history nobody asks for never makes the call.

**Alternatives rejected.**

- *Embed `CHANGELOG.md` in the binary.* It stops at the binary's own version,
  so the newer releases the user asked for are exactly what it cannot show, and
  embedding a root-level file needs a root-level Go package for it.
- *Let the browser call `api.github.com`.* Blocked by the CSP, and loosening
  `connect-src` for a convenience would give up the Atlas's promise that its
  page talks to its own origin only.
- *Fetch the history in the daily update check and persist it.* A second thing
  to keep on disk and to migrate, for data that is public and cheap to re-fetch.
- *Render the release body as HTML.* A release body is written by anyone with
  push access to the repository; the sheet renders it through the same sanitised
  Markdown as a concept (no raw HTML, no remote images), never
  `dangerouslySetInnerHTML`.

**Consequences.** A server with the update check on now also calls
`api.github.com/repos/…/releases` when the Atlas asks for the history, at most
once an hour (60 unauthenticated requests an hour per IP is GitHub's limit); a
failed refresh is not retried for a minute, so a server that cannot reach GitHub
does not wait out the timeout on every open. The `If-None-Match` is sent on page 1
only: a new release lands there, so a 304 stands for the whole list, and an edit
to a changelog older than the first page shows up only when page 1 changes. The
tag and the release URL come from the network: a release whose tag is not a
version (`updatecheck.ValidTag`) is left out, and the same check keeps such a
tag out of the upgrade command the user copies and runs; the route sends a
release URL only when it is `https`.
The daily check follows the same rule: it reads the 10 newest releases, not
the newest one, and answers with the first that is not a draft and has a
version tag, so a draft at the head of the list (a token with write access
sees them) no longer blinds it.
