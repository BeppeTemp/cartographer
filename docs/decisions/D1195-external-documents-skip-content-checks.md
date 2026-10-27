---
topic: data-plane
---

# D1195 — A page marked `origin: external` skips the content checks; remote shell blocks skip `machine_path`

**Status: implemented.**

**Decision.** The frontmatter field `origin: external` marks a verbatim external document. `open_marker`, `machine_path` and `title_quality` skip such a page, on `lint` and on the write path (both go through the same functions); every structural check still applies. Separately, `machine_path` skips a path inside a fenced block that runs on a remote host: the nearest non-blank line before the fence names `ssh <host>`, or the block opens with an `ssh <host>` line or a prompt carrying a hostname (`user@host:…$`, `user@host$`, `[user@host dir]$`, `root@host#`). The finding's message says how to express a remote path.

**Why.** The three checks judge the KB's own writing: a third-party manual, RFC or vendor README kept verbatim has `TBD`, `~/venv` and a long or decorated title that are not the KB's to rewrite, and editing them to silence a finding falsifies the source. D907 closed every per-page escape (no `lint_ignore`, no allow-list), so the page needs a declared fact about itself, not a switch on a check. A frontmatter field is declarative, visible in review and per page; the alternative of a status list in the map contract would put a page-level fact in a map-level file and make the exemption depend on a vocabulary. Structural checks stay because they are about the KB's graph and contract (links, index, required fields, templates, `title_h1_mismatch`), which a verbatim page still takes part in. The remote-block rule covers the other false positive of D124: documentation of a command run on a server, where `/home/user/...` is correct and identical for every reader.

**Alternatives rejected.** A path allow-list for remote hosts: no map can extend one (D907) and it would never be complete. A `status: external` value: it collides with the status vocabularies and the lifecycle checks that read them. Skipping every check on an external page: it would hide broken links and an unindexed page. Detecting remote blocks with a per-line regexp over the whole body: the fence scan runs only when a candidate path exists, and most pages have none.

**Consequences.** `origin` with any other value means nothing, and no check validates it. The remote heuristic is deliberately loose (a preceding line such as "ssh to" counts); it can only hide a finding, never invent one, and a path outside a remote block is still checked. A future check that judges the KB's own writing should call `externalOrigin` (internal/lint/remote.go) and say so in its registry entry's docs.
