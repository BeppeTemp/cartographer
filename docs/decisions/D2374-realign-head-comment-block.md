---
topic: client-configurator
---

# D2374 — A realign lifts a block read as a head comment

**Decision.** Before it homes the available-settings blocks, `Render` runs a pre-pass that moves the part of any key's
`HeadComment` from its first divider on to the `FootComment` of the previous sibling, or to the document foot when the
key has no previous sibling; the text before the divider stays as the key's head note. A block above the first key is
read as the head comment of the document, and is moved the same way. The placement logic then runs unchanged. It extends
D747.

**Why.** yaml.v3 attaches a comment written directly above a key to that key as its head comment. `homeBlocks` reads blocks
only from the foot comments of the block holders, so a key a person appended below the block made the block invisible:
`applyBlock` appended a fresh one, the old one stayed as the key's head comment, and the new copy listed the hand-written
key as unset (#2367).

**Alternatives rejected.** Teaching `homeBlocks` to read head comments as holders: it would give every holder two places
to look and a second ordering to keep. Dropping the old block when a second one is written: it loses lines a person edited.

**Consequences.** A file that already carries two blocks, one read as a key's head comment and one as a foot comment,
has them merged into one by the realign: one block per owner is the invariant, so no duplicate is kept. The block is cut at its
divider: text above it, glued or not, stays as the key's note; a note glued below the block's last line goes with it;
a note separated by a blank line below stays above its key. Output changes once for a file that had the defect before the fix (the block is lifted,
one realign reports it, then the file is stable). A future change to block placement has to keep the pre-pass before
`homeBlocks`.
