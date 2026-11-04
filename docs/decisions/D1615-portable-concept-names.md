---
topic: data-plane
---

# D1615 — A new concept id must be portable: every segment is checked, and Windows device names are refused

**Decision.** The write path checks every directory segment of a new concept id, not only the last: a segment that differs only in case from an existing entry is refused with the same message as a file-name twin, and a directory that does not exist yet must be a slug (a move or transfer keeps its source's legacy names). The Windows device names (`con`, `prn`, `aux`, `nul`, `com1`-`com9`, `lpt1`-`lpt9`, with or without an extension) are refused for new ids, and `nonslug_file_name` reports an existing one without a fix.

**Why.** On a case-insensitive filesystem `Notes/foo` lands in `notes/` while the answer, the log, the commit and any `index_entry` say `Notes/foo`, so links written with the returned id break on every Linux clone; on Linux the same call creates a second `Notes/` that macOS and Windows cannot check out. A page named `aux` written by a Linux server makes the repository impossible to clone on Windows. The cost is that a few previously accepted ids are now refused.

**Alternatives rejected.** Canonicalising the id to the on-disk spelling and answering with it: the caller asked for one id and silently gets another, which is what the last-segment twin rule already refuses. A lint finding only, with no write-time refusal: the repository would already be unclonable by the time it fired. A fix for a device name in `nonslug_file_name`: the name is a valid slug, so a rename is a judgement the server does not take.

**Consequences.** Existing pages keep working under their names; only new ids are checked. The device-name list is Windows' and must be kept in `kb.IsWindowsDeviceName`, which the write path and the lint finding share.
