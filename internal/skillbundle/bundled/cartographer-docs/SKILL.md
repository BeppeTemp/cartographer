---
name: cartographer-docs
description: Use on any question about how Cartographer works or why - a command, flag, config key, error message, behaviour or design choice - before answering and before opening the repository; reads the documentation of the installed version with `cartographer docs`.
version: "1.0"
---
# Cartographer documentation

The documentation of the version you run is built into the binary, as a read-only KB. `cartographer docs`
runs the read tools (`search`, `graph_context`, `concept_read`, ...) over it: no server, network or
client configuration. Never answer a Cartographer question from memory, and do not browse the
repository first: the docs here match the installed version.

## Procedure

1. Search with the **content words** of the question (nouns, command, flag and config key names), not
   a whole sentence: the index matches whole words, and filler words ("how", "does") make a sentence
   query miss.
   `cartographer docs graph_context query="workspace bind scope"` (or `cartographer docs search query="..."`).
2. Read each cited section that looks relevant, whole, never from an excerpt, at most five:
   `cartographer docs concept_read id=<id>`. For several at once, one batch:
   `cartographer docs --batch - <<'JSONL'` with a line `{"tool":"concept_read","arguments":{"id":"<id>"}}` per read.
3. Answer from what you read, for the installed version, and cite the concept ids
   (`using-cartographer/sync/workspace-scope-d193`). Say which version the answer is for when it matters
   (`cartographer version`).
4. A "why" question: follow the `Decisions cited:` line of the section to the decision concept and read it.
5. The docs do not cover it: say so. Offer the `cartographer-feedback` skill to report a docs gap; it
   files nothing itself.

`cartographer docs` with no arguments prints the usage; `--list` lists the tools. An unfound topic
is often a wrong keyword: retry with a synonym or the exact command name before concluding.
