---
topic: control-plane
---

# D1557 — `last_doctor` reads the first line of a log entry, nothing else

**Decision.** A `log.md` entry is a doctor session when the first non-blank line of its body starts with `kb-doctor:`, the line the `cartographer-kb-doctor` skill closes with. A block that only contains the word elsewhere is not a session. This amends the note of D958 that the marker "still matches by substring".

**Why.** Every `concept_write` logs `concept_write: <id>`, and a note or a `log_append` may mention the word: with a substring match, a write to `tools/kb-doctor-checklist` moved `last_doctor` to today and silenced `doctor_suggested` and the nudge for the whole interval, hiding real debt.

**Alternatives rejected.** Keeping the substring match for legacy entries: that is the defect. A dedicated log kind or a frontmatter field: log.md is plain text appended by `log_append`, and a new shape would need a migration for every KB. Matching the skill name: it has been renamed once (D958) and the marker must survive that.

**Consequences.** The skill's close step must keep writing `kb-doctor:` first; a KB whose past doctor entries began otherwise reads `last_doctor` as absent until the next session. Only the first line counts, so an agent that prefixes its entry with other text is not a session.
