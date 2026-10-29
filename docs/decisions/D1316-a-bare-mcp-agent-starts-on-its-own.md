---
topic: control-plane
---

# D1316 — A bare MCP agent starts on its own

**Decision.** The text a server-only agent receives teaches it the first steps: the
`initialize` guide of an empty KB carries a cold-start section (`map_create`, the reserved
name, services under `services/<id>`), the guide lists the served procedures and says it
is the whole guide, "Archives" is "Maps" in agent-facing text, and `map_create` names its
reserved set before and after the mistake.

**Why.** A usability test (headless agents, only the MCP server) spent 7-8 orientation calls
on an empty KB, then called `map_create` with `services` and got an error that named no
alternative; `kb_guide {}` was called for text `initialize` had already sent.

**Alternatives rejected.**
- Put the cold-start section and the procedures list in the managed block: the block is paid on every model call (D1182).
- Empty `kb_guide {}` on a connection that got `initialize`: a host that drops `initialize.instructions` (D1100) would lose the guide. The redundant call is removed by telling the agent, not by emptying the fallback; the cost for such a host is the one-line "already sent" opening, which it reads past.
- Refuse `skills`, `agents`, `hooks` too: out of scope; they are filtered from the maps list but not refused today.

**Consequences.**
- One constant, `kb.ReservedMapNames` (next to `ServicesNamespace`), feeds both refusals (`CreateMap`, `ScaffoldMap`), the `map_create` description and the cold-start section.
- "Maps" is shared with the managed block: every provisioned client rewrites its block once at the next `sync` (the hash moved); `TestShared_MapsWordConvergesAndGuideTextsStayOut` pins the single rewrite and that guide-only text never reaches the block. Go identifiers (`Archives`, `KBArchiveNames`) keep their names.
- The procedures list has one source, `procedureLines` in `internal/mcpserver`, used by `initialize` and `kb_guide`; no bundle means no list. `kb_guide {}` opens with an "already sent" line on a non-provisioned connection, nothing else changes.
- Budgets raised (D285): the guide's operational section 1782 to 2332 bytes (hard cap 2048 to 2400), measured 2120 with the three procedures listed (was 1620 without); new cold-start budget 302 bytes (measured 274). `tools/list` is unchanged in budget: the `map_create` clause was paid by shortening the `kb_guide` description.
