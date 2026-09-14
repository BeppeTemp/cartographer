---
topic: data-plane
---

# D91 — Import convenience without widening the default write surface

**Status: implemented (2026-07-24).**

**Decision.** `cartographer import` remains uncommitted and flat by default, preserving D74's mechanical-review workflow. `--commit` makes exactly one final commit of only the paths written by the import; `--message` supplies its message and implies `--commit`. It uses the KB git lock and an isolated Git index seeded from `HEAD`, so pre-existing staged or unstaged work cannot enter the import commit. A partial batch still commits its successful writes and reports the errors.

Each absent destination map is created through `CreateMap`, adding the same `_map.md`/`index.md`/`log.md` scaffold as `map_create`; existing maps are not altered. `--dir-as-concept` promotes a source directory with `index.md`, or `README.md` if no index exists, into `<map>/<dirname>/`: its chosen index is written through the expanded-concept path and its sibling markdown files become satellites. The dry-run identifies each promotion. Without the flag, `index.md` retains the established reserved-name rejection and other files remain flat.

**Rationale.** An opt-in single commit supplies the expected convenience without making a mass import silently commit by default, and the isolated index makes that promise meaningful even in a dirty clone. Reusing map creation and expanded-concept resolution keeps the CLI output structurally identical to the MCP write path instead of adding another partial KB shape. Directory promotion is explicit because automatic hierarchy inference would turn D74's deliberately mechanical importer into a semantic mapper.
