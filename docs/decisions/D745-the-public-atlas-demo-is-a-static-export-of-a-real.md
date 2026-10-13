---
topic: deployment-release
---

# D745 — The public Atlas demo is a static export of a real server, published with the docs

**Decision.** The public demo of the Atlas is the UI's own bundle, built with `VITE_ATLAS_STATIC=1`,
plus the JSON answers of a real `cartographer serve` over `demo/astronomy`, exported by
`demo/export.mjs`. It is published on GitHub Pages under `/demo/` by the `docs` workflow, which
rebuilds it on every release. The search goes through an exported index and runs in the browser.

**Why.** A visitor should be able to see the Atlas without installing anything. With no server
there is nothing to run, pay for, rate-limit or protect, and the Pages site already exists. The
answers come from the real server and not from a mock, so the demo cannot drift from what the
binary does. The UI change stays small: one branch in the API client, removed from the embedded
build by the compile-time flag. The cost: the demo is a snapshot. It does not follow live edits,
and its search is a simple word match, not the server's ranking with glossary expansion.

**Alternatives rejected.**
- A hosted instance (container, VPS): live, but a service to keep running and protect, for a
  read-only page. It stays the way to a public MCP endpoint, which a static site cannot answer
  (#745).
- Mock data written by hand for the UI: it drifts from the server's real answers at the first
  API change, and nobody notices.
- The existing generator `web/scripts/demo-kb.mjs`: its pages hold placeholder text, made for
  screenshots and the 3D benchmark, so there is nothing to read once a concept is opened. It
  stays for those uses.
- Absolute commit dates in the replayed history: Activity's windows are relative to today, so the
  demo would show no recent activity a few weeks after each release.

**Consequences.** A request the UI starts making needs a matching line in `demo/export.mjs`, or
the demo answers it with a 404. `staticKey` exists twice, in `web/src/api/static.ts` and in
`demo/export.mjs`, and `static.test.ts` pins both to the same names. `demo/astronomy` is public
content: anything added to it follows the repository's privacy rule. Its lint findings are part
of the demo, so a fix to one is a choice about what the demo shows. `demo/build.sh` fails on a KB
file that is in none of its commit groups, so a new file cannot be left out of the history
silently.
