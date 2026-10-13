---
topic: control-plane
---

# D759 — `concept_expand` leaves a dangling asset link as written

**Decision.** When `concept_expand` rebases the concept's relative links (D295), a
link to a non-Markdown file (an extension other than `.md`) whose target does not
exist is left exactly as written. After the move it resolves inside the concept's
own directory. Links to existing files and to concepts (`.md` or no extension,
dangling or not) are rebased as before.

**Why.** The natural order for a dossier with scripts or images is: write the
body, expand, then `asset_write` the files it links. Rebasing kept the old target
(`map/build.py`), so every link broke once the assets landed next to `index.md`
(#759). A non-Markdown file can only legitimately live inside an expanded concept
— in a map directory it is a `stray_file` — so for a dangling one the concept's
own directory is the only target that can ever be valid. Rebasing preserves what a
link resolves to; a dangling link resolves to nothing, so there is nothing to
preserve.

**Alternatives rejected.**
- *`expanded: true` on `concept_write` / `concept_new`*: one more parameter on two
  tools of the default profile for a case the rebase rule now covers, and it would
  not fix a concept that was written plain and expanded later.
- *Leave every dangling link untouched, `.md` included*: a dangling concept link
  most likely names a sibling concept still to be written in the same map;
  pointing it inside the dossier would break it differently.

**Consequences.** The exemption is decided against the working tree at expand
time. `concept_move` and `concept_merge` keep the plain D295 rebase: their target
directories are not the concept's own asset home.
