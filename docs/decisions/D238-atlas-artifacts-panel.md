---
topic: architecture
---

# D238 — The Atlas UI shows a KB's artifacts in their own panel

**Decision.** The Atlas UI has a third rail panel, Artifacts, that lists what
the KB ships to agent clients and shows each artifact's files. It lists exactly
what `artifact_list` lists (bundled skills excluded) through one shared
enumerator, served by two read-only UI API routes. Only a principal that can
see the whole KB gets the panel: `GET /kbs` carries an `artifacts` flag and the
routes answer 404 otherwise. Files are shown read-only, bounded to 256 KiB of
UTF-8 each, with Markdown rendered by the existing sanitising renderer and
everything else as plain text. The selection is URL state.

**Why.** A human could not see which skills, subagents and hooks a KB provides,
where they go or whether they are signed, short of asking an agent. Artifacts
belong to the KB, not to a concept, so they get a panel rather than an
Inspector tab. They are whole-KB resources in the policy, so a partial reader
must not learn they exist. The cost is a second consumer of the artifact
listing, which is why the tool and the routes share one function rather than
two copies.

**Alternatives rejected.**
- An Inspector tab: artifacts are not attached to a concept.
- Listing bundled skills too: they come with the binary, not with the wiki.
- A syntax highlighting library: a runtime dependency and budget cost for a
  read-only view; preformatted text is enough.
- Attaching validation issues to each artifact: an excluded skill has no entry
  to attach them to, so the list carries them once, above the groups.
- Showing the MCP allowlist diagnostics as issues: they name the target of a
  descriptor `artifact_read` hides from the same reader.
- No clients for the curated `instructions.md`: sync folds it into the
  generated instructions block (D61), so it does reach the matrix's clients.

**Consequences.** `listKBArtifacts` in `internal/mcpserver` is the one listing;
`artifact_list`'s output must stay byte-identical when it changes.
`provisioning.Destinations` reads the destination matrix, so the clients the UI
names cannot drift from what sync writes. Signing state reflects the server's
artifact signer. Nothing in these routes decrypts: a descriptor's secret
references are shown as written, as `artifact_read` returns them.
