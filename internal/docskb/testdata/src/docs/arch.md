# Architecture

How the parts fit together.

## Layers

The layers are described below, one per sub-section, and together they are larger than the lowered cap.

### Storage

Storage keeps markdown files in a git repository. Every write is a commit, and every commit is revertible, so history is never lost. Readers never see a half-written file.

### Transport

Transport carries requests over stdio or HTTP. Both share one handler, so a request behaves the same on either. Authorization sits in front of the handler.

### Indexing

Indexing builds an inverted keyword index in memory when the server starts. It is rebuilt after each write, which keeps search consistent with the files.

## Long prose

The first paragraph of a section that has no sub-headings but is longer than the hard cap, so it is cut at blank lines. It goes on for a while to take some room in the unit.

The second paragraph keeps going with plain sentences about nothing in particular, only to take space in the part it lands in, and it ends here. The sentence is padded so that this paragraph is a fair share of the section and the greedy grouping has something to place.

| Column | Value |
|---|---|
| a | one |
| b | two |
| c | three |
| d | four |

The third paragraph follows the table. A table is never cut in the middle, because a break before a table row is not a break.

The fourth paragraph is the last one of this section and it closes the fixture with a few more words so that a third part exists. Another sentence follows only to add bytes to the last part of the section, which has to be long enough to stand next to the table on its own.

## Unsplittable

One single paragraph that no blank line can cut: it is the block the split leaves whole, and the size test names it. It is repeated to be long enough. It is repeated to be long enough. It is repeated to be long enough. It is repeated to be long enough. It is repeated to be long enough. It is repeated to be long enough. It is repeated to be long enough. It is repeated to be long enough. It is repeated to be long enough. It is repeated to be long enough. It is repeated to be long enough. It is repeated to be long enough. It is repeated to be long enough. It is repeated to be long enough. It is repeated to be long enough. It is repeated to be long enough. It is repeated to be long enough. It is repeated to be long enough. It is repeated to be long enough. It is repeated to be long enough. It is repeated to be long enough. It is repeated to be long enough. It is repeated to be long enough. It is repeated to be long enough. It is repeated to be long enough. It is repeated to be long enough. It is repeated to be long enough.
