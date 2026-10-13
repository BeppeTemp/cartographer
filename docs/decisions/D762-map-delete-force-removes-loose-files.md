---
topic: control-plane
---

# D762 — `map_delete` takes `force` to remove loose non-Markdown files, not a deleting lint repair

**Decision.** `map_delete` accepts `force: true`, which deletes the regular
non-`.md` files directly under the map directory together with the map and
returns them as `removed_files`. Concepts, subdirectories (expanded concepts,
asset-only directories, sub-maps), non-scaffold `.md` files and symlinks still
refuse, with or without `force`. The commit subject lists the removed files.

**Why.** A loose file such as `x/schema.png` is reachable by no tool: `asset_*`
resolve only files owned by an expanded concept and `artifact_delete` only root
artifacts, so the only way out was a manual delete on disk. The invariant of
D88 (`map_delete` never removes concepts or directories) is kept: `force` widens
it only to files no concept owns.

**Alternatives rejected.**
- A lint with a deleting `kb_repair` fix: every repair so far rewrites or
  scaffolds, and doctor auto-repair assumes repairs never delete user files.
- Letting `force` remove subdirectories: it would delete expanded concepts and
  assets silently, which D88 forbids.

**Consequences.** `DeleteMap` classifies a loose file by `d.Type().IsRegular()`
directly under the map, so a symlink is never followed and never counted as
loose. The error offers `force` only when every remaining entry is loose.
