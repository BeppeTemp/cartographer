---
topic: architecture
---

# D820 — The Atlas shows what was retired, read from the root log, as a tab of Activity

**Decision.** `GET /api/ui/v1/kbs/{kb}/retired` lists every `deprecated`,
`superseded` or `archived` concept with where it went, when, why and what
replaced it, and the Atlas draws it on a Retired tab of Activity
(`?panel=retired`). The story of a retirement is read from the root `log.md` by
`kb.Retirements`, which parses the entries `concept_archive` and `supersede`
already write; no frontmatter field is added. A retired concept with no
matching entry (a status set by hand) carries its status and `timestamp` and
no story, and the tab says no retirement is recorded. The rule that marks a map as an archive, "at least half of its
top-level concepts are retired, and it is not a journal", moves from the
`index_lists_retired` lint check into `kb.IsArchiveMap`, which the lint
exemption and the overview's `archive` flag both call; the rail marks such a
map with an "Archive" badge and lists it after the live ones. Search keeps hiding
archived pages by default; the UI search route forwards `include_archived=1`
and the command palette gets an "Include archived" toggle that badges them.

**Why.** The server tracks retirement, but only as a status in the page: the
from-path, the date and the reason exist in the log entry and the commit, so a
reader of the Atlas could not tell what a KB had retired, or find a page it had
moved. Reading the log covers pages archived before this change, where a new
frontmatter field would have covered only the future and would have been one
more key for `concept_archive` to keep in step. One function for the archive
rule means a map the rail badges as an archive is one whose index the lint
check leaves alone.

**Alternatives rejected.** A rail entry of its own: retirement happened to the
KB, and most KBs retire little, so every reader would carry a button for a
short list; a tab of Activity, the way Secrets is a tab of Artifacts, costs
nothing until it is opened. A `retired_from` and `retired_at` frontmatter pair
written by `concept_archive`: it leaves every earlier retirement without a
story and duplicates what the log and the commit trailer hold. A contract key
declaring an archive map: a second place to be wrong about a fact the page
statuses already determine. Building the list from the graph snapshot: it is
cut at 5,000 nodes, so a large KB would silently drop retired concepts; the
route walks the permission-filtered concepts like the overview does and never
truncates. Showing archived hits in search by default: an agent does not see
them either, and the reader who wants them says so.

**Consequences.** The log is the record, so a concept moved again after its
archive, or a log entry edited by hand, loses its story and falls back to "no
retirement recorded"; `kb.Retirements` keys the story on the id the concept
holds now (a supersede on its source, an archive on its target; a page
archived and then superseded keeps both) and parses only the entry shapes the
two tools write, so a change to either log line must change the parser and its
test. The reason is read from the entry's title line, so `concept_archive`
writes it on one line (line breaks collapse to a space); the parser also joins
the title lines of an older entry that was written with breaks. Every field of
the route is derived from what the caller sees: `superseded_by` is left out
when that id is hidden, `from` and `reason` when the source is hidden (the
reason is free text that may name it), and the journal and map counts come from
the visible set, so a narrowed token learns no hidden id or count. The Atlas
stays read-only.
