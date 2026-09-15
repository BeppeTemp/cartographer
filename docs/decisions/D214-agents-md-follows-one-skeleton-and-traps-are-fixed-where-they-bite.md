---
topic: project-governance
---

# D214 — AGENTS.md follows one skeleton, and a trap is fixed where it bites

**Decision.**
- The root `AGENTS.md` has exactly these sections, in this order: `Commands`,
  `Rules that apply everywhere`, `Areas`, the generated `Code map`,
  `Where things are`, `Working rules`.
- `Working rules` defines **Done**. Every trap you hit (a mistake already paid
  for that the code does not flag by itself) is fixed where it bites, in this
  order:
  1. a test, if it can be checked;
  2. a comment next to the code, if it is about that code;
  3. otherwise one line in the area `AGENTS.md` or in the skill of the procedure.
- The plan template's Closing section asks for the traps, and `implement-issue`
  repeats the rule to its subagents.

**Why.**
- This is the maintainer's cross-repository skeleton. An agent that has worked
  in one of these repositories finds the same headings in the same places here.
  Before this change, the section map lived in a bold paragraph above
  `Commands`, and the "where to look" table was ahead of everything else.
- The trap rule existed nowhere in this repository. A trap was either
  remembered by whoever hit it or lost.
- Each of the three places is read at the moment the trap matters:
  - a test fails when the trap is repeated;
  - a comment is read by whoever edits that line;
  - an area file or a skill loads when that area or procedure is in play.
- Writing traps in prose elsewhere (a journal, a "lessons" page) does none of
  those things. No client loads such a file on its own, and prose fails nothing.

**Alternatives rejected.**
- *A journal of traps, bounded to a dozen entries.* Another repository tried
  it. No client loads it, the bound throws away traps that are still true, and
  it needed a hook forcing entries even when there was nothing to say.
- *Keep this repository's headings.* They were fine on their own, but they
  cost every agent a re-orientation that the shared skeleton removes, for no
  gain.

**Consequences.**
- A new section in `AGENTS.md` is a change to the skeleton, not a local edit:
  content that does not fit goes to the page that owns it, with a pointer in
  `Where things are`.
- A plan's Closing section lists its traps, or says `none`.
