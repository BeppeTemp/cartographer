---
topic: architecture
---

# D286 — The Atlas answers the reader's questions with the agents' own tools, and draws artifacts beside concepts

**Decision.** The Atlas stays graph-first, and the questions a reader of an
agent-written KB brings to it are answered by the read-only tools the agents
use, through three new UI routes: `search` (full text in the Ctrl/Cmd+K
palette, after an instant title match), `changes` (an Activity panel, filterable
by author) and `status` (open knowledge gaps, search misses and stale pages,
shown in the Observatory under the lint findings). Skills, agents and hooks are
related to concepts only by the explicit references in their own files — a
`[[wiki-link]]` or a written-out concept id — and that relation is shown both
ways: *Used by* in a concept's Links tab, *Concepts it reads* in an artifact's
detail, and diamonds linked to their concepts in the graph. The concept list
beside the canvas is gone; it survives as the whole view when there is no WebGL.

**Why.** Reviewed live against real KBs, the global graph was scenery: a
~700-node network answers no question on its own. What a reader comes for —
find something, see what the agents changed and why, see what the KB is
missing — already existed server-side and never reached the page. Answering
through the tools' handlers keeps one visibility filter and one definition of
each answer. The search route skips the miss log because the palette queries
as the reader types: every prefix that matches nothing would be recorded as a
knowledge gap. On a real work KB, 34 of 37 skills write concept ids (112
pairs), while concepts only name skills in prose, and a name such as a short
common word is also a word: only the side that spells out an id is evidence.

**Alternatives rejected.** A home page with search, changes and a to-fix
queue as the landing view — rejected in review: the graph must stay the first
thing. Activity as an overlay on the graph — rejected: it is a page to read,
like the Observatory. Relating artifacts by matching their names in concept
bodies — false positives with no way to tell them apart. A switch to show the
diamonds — rejected: they are part of the picture, not an option. Keeping the
concept list toggle — the search reaches every concept faster, and the list
remains where it is the only path (no WebGL).

**Consequences.** `/status` is a whole-KB resource like the artifact routes
(it describes remotes and capabilities); `used_by` is omitted for a narrowed
principal. The relation is recomputed per request from the artifact files;
a KB with many large artifacts will want it cached per revision. The keyboard
path to a concept is Ctrl/Cmd+K; the browser suite reads the drawn concept set
through it. An Escape already handled by a dialog no longer also clears the
selection.
