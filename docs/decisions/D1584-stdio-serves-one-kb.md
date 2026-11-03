---
topic: deployment-release
---

# D1584 — stdio `serve` refuses more than one KB

**Decision.** `serve` over stdio mounts exactly one KB: with several distinct mounts (`--kb a,b`, or `--data` holding several KBs) it stops with an error that names them and points to `--kb <path>` or `--http`. One directory spelled two ways (a relative path, a symlink) counts as one mount, in both transports.

**Why.** Stdio carries one session for one KB. The code mounted and indexed every KB and then served only the first, so the rest were opened, locked and committed to for nothing and an operator who passed two paths believed both were served. The same-directory comparison used only `filepath.Clean`, which let one root be mounted twice under different names and the second `index.db` replace the first in the index map, leaking its handle.

**Alternatives rejected.** Serving the first and warning (keeps the surprise for anyone who does not read stderr of a spawned child). Routing several KBs over stdio like the HTTP routed mount (a different product surface, not a bug fix).

**Consequences.** A stdio launch that relied on the silent first-KB behaviour now fails at start and must name one KB. Mount identity is the symlink-resolved absolute path.
