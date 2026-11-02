---
topic: client-configurator
---

# D1534 — Settings comments are recognised by their history, not by a marker

**Decision.** A comment the schema wrote above a key is told from a person's by its text, and the table remembers the
texts a key has had: `Key.Previous` lists the descriptions it carried in earlier releases, and the `Removed` entry that
retires a key records its last `Description` (and `Previous`). `isGenerated` accepts the current description in the
current and the first layout, and each earlier one in either layout with whatever notes line (values, default, variable,
flag) came with it, as long as every line under the description is a note the schema writes. `Realign` takes the
comments of retired keys off every head comment before it removes anything, so the comment goes with its key and the
copies an older realignment moved onto the neighbours go too. The client `Save` does the same for the keys it drops. A
test (`internal/settingschema/testdata/descriptions.tsv`) lists every key's description and fails, with the line to add,
when a description changes without the old text in `Previous` or a key leaves the table without its last description in
the registry. A note that ends a file after a mapping gaining a block of unset settings is moved to the end of the
document by the first render, so the second realignment finds nothing to do.

**Why.** Recognition compared a comment with the current description only, so every release that edited one froze the
old text in all existing files, and the comment of a retired key was taken for a person's and moved onto the next key
(a real client file carried four such comments above `auth:` and `token:`). The notes line is not compared with the
key's current one because it changes with the description (a default moves with the text that explains it), and a text
that did not match for that reason was as frozen as before. The cost is a table that only grows, one line per edited
description, and a notes grammar that a person's line directly under an old description could in principle satisfy.

**Alternatives rejected.**
- An invisible marker on the generated lines: noise in a file people edit, and a migration of its own for every file
  already written.
- Digests of the old comments: not readable in a diff review, and the old text is what the fix has to show.
- Comparing the whole old comment, notes line included: it needs the old default, values and variable of every
  release, which the table does not keep and which changed together with the description for `search_roots`.
- A command that cleans the files: the next realignment (automatic after an upgrade) does it once the texts are known.
- Backfilling the 86 keys the server and KB registries retired before v0.25.0, the first release that wrote comments:
  no released file carries them, and the guard makes every later retirement record its text. The eight retired client
  keys are recorded, because a real file proved them.

**Consequences.** Changing a `Description` means appending the old text to `Previous` in the same change; retiring a key
means giving its entry a `Description`. A comment with a line a person added under the generated text, or that matches
nothing, is theirs and moves with its key as before. Keys listed inside a commented group of the available-settings
block (`# audit:` with its keys) are still compared with the current text only. The rule is in
`docs/conventions.md` §Settings.
