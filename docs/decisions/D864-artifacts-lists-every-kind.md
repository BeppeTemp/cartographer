---
topic: sync-provisioning
---

# D864 — The Artifacts page lists every kind a KB can hold, from one list in provisioning

**Decision.** `provisioning.Kinds()` is the one ordered list of the artifact
kinds a KB can hold: each with its title, the path its files go in the KB
(`skills/<name>/SKILL.md`), the repository doc and anchor that explains it, and
whether it is KB-only (templates and secrets, which no client receives).
`GET …/artifacts` returns it as `kinds`, adding per kind the count and the
clients from `Destinations(kind, "")`; `GET …/secrets` returns the secrets
descriptor as `kind`. The Atlas Artifacts page draws a chip and a section for
every kind, empty ones included: an empty kind says what it is, where its files
go, which clients receive it and links the docs. Only the presentation (the
blurb, the glyph, the colour) stays in the UI, keyed by kind.

**Why.** The page listed what a KB ships today, so a reader could not learn from
the Atlas what a KB is able to give to its agent clients: a hook or an MCP
server appeared only after someone had written one. The facts the empty section
needs already existed, but scattered: the clients in the destination matrix, the
layout in `BuildManifest`, `listTemplateSlugs` and `uiSecretsList`, the docs in
five pages. Gathering them next to `artifactKinds` makes provisioning the owner
of the layout it scans, which is where a new kind is added anyway.

**Alternatives rejected.**
- *Keep the kinds in the UI and ship only the clients.* The UI's list would
  drift from the matrix the first time a kind was added, with nothing to fail.
- *Derive the layout from `BuildManifest`.* It scans five folders by hand, with
  per-kind rules (a skill needs a valid `SKILL.md`, an MCP descriptor an
  allowlist entry); there is no layout value to read, so the list is data and a
  test holds it to the scanners.
- *A page-level help text instead of empty sections.* It would say the same six
  things once and could not show which clients receive which kind.
- *Docs links resolved by the server.* The server does not know where its docs
  are published; the UI resolves a repository path against the GitHub blob URL
  of `main`, as the top bar's demo link already does, and links only a path of
  the shape `docs/….md#anchor`.

**Consequences.** A kind added to the destination matrix without a descriptor
fails `TestKindsMatchTheMatrix`; a descriptor whose `KBPath` the scanner does not
find fails `TestKinds_PathsAreWhatTheScannerFinds` (synced kinds) or
`TestKinds_KBOnlyPathsAreWhatTheScannersFind` (templates, secrets); a `Doc` that
names a page or heading that does not exist fails
`TestKinds_DocsExistAndHaveTheHeading`, so a renamed heading breaks the build
instead of a link. A server that sends no `kinds` (an older binary, a static
snapshot) leaves the page as it was: only the kinds that have an artifact and the
page-level empty state. The response fields are additive, so no release is
affected. The route detail (`uiArtifactKinds`, the kinds `GET …/artifact`
accepts) is still its own map; the list test checks every listed kind is in it.
