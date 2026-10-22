---
topic: data-plane
---

# D1025 — Concept paths refuse symlinks, and lint reports them

**Decision.** A symlink on a concept's path (a map directory, the `.md` file, an expanded `index.md`, a dangling link) is `okf.ErrInvalidPath` for read, write, delete and locate; the graph walk does not list a symlinked `.md`; the new lint check `symlinked_path` reports every symlink under `data/` and `services/`.

**Why.** A KB's `data/` is a git working tree, and git stores symlinks: a poisoned remote or a shared tree could make `concept_read` disclose, or `concept_write` modify, a file outside the KB. Assets (`asset.go`), provisioning artifacts and `paths.yaml` already `Lstat` every component and refuse links (D148); concepts were the gap. No MCP token can create a link, so this is hardening. It costs a `Lstat` per path component on each by-ID concept access, and a KB that relied on a symlinked map or concept stops working for it.

**Alternatives rejected.**
- Resolve the link and check the target stays inside the KB: Cartographer would decide what is on the other side, and a target can change after the check. D148 chose refuse-never-resolve.
- Put the walk in `ResolvePath` or `ReadRaw`: they serve non-concept paths (map directories for `MkdirAll`, KB-root artifacts, every graph read), so every graph build would pay a walk for no extra coverage. The one choke point is `resolveConceptRelPath`, plus the forced-expanded branch of `WriteConcept`.
- Silently skip symlinks without a lint check: the operator would see a concept that "does not exist" with no reason.

**Consequences.** Every ID-to-path caller must go through `resolveConceptRelPath` (a comment says so). `conceptFiles` drops symlinked files, so the graph, search and the walks agree with the readers. `symlinked_path` is not avoidable at write (nothing in Cartographer creates a link) and has no fix kind: the remedy is `git rm`. `data/` and the KB root may themselves be links (a mount) and are not inspected, only components below them.
