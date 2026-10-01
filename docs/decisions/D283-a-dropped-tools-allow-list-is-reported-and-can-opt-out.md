---
topic: sync-provisioning
---

# D283 — A dropped `tools` allow-list is reported, and an agent can opt out of being widened

*(Partly superseded by [D291](D291-an-agent-can-carry-each-clients-own-restriction-verbatim.md): an agent's `providers:` frontmatter block now writes a client's own native fields verbatim; dropping `tools` and `model` is unchanged.)*

**Decision.** When a KB agent declares `tools` and the target client is not
Claude Code, `sync` warns on every run, naming the agent, the client and the
list. An agent may add `strict_tools: true` to its frontmatter: it is then not
installed on those clients (reported `unsupported`, excluded from
`FilterForProvider`, pruned if an earlier sync installed it). `artifact_write`
rejects an agent whose frontmatter `name` differs from its file name. The
restriction is derived from the source content (`Artifact.Restriction`, never on
the wire), by `BuildManifest` on the server side and by the client's `sync_pull`
decoding.

**Why.** D55/D58/D195 dropped `tools` so as not to invent a mapping that hands
an agent capabilities its author never granted. Dropping it does that too: in
Claude Code an omitted `tools` is every tool, so a read-only explorer became a
general agent elsewhere while its description still promised read-only, and
`status` showed it as fully synced. The mapping stays uninvented; what changes
is that the loss is visible and the author can choose absence over widening.

**Alternatives rejected.**
- Heuristic `tools` to native mapping: still rejected (D55), names and
  semantics differ per client.
- A per-client `providers:` frontmatter block copied verbatim into native files
  (proposed in #447): left out. The repository has no verified record of which
  restriction keys each client's agent file honours (D195 verified only
  `name`/`description`/`prompt` for Kiro), and writing keys we cannot check
  would put unverified config into users' agent files. It needs a per-client
  check first and is tracked in the issue.
- Warn only when the agent is written: an unchanged widened agent would be
  reported once and then forgotten, so the warning is computed from the manifest
  on every run, like `unsupportedKindWarnings`.
- Warn for a dropped `model` too: a different model is not a wider capability,
  and it would warn on nearly every agent.
- Make strictness the default: would silently stop delivering agents that work
  today; opt-in keeps existing installs unchanged apart from the warning.

**Consequences.** `strict_tools` is the only client-facing switch and lives in
the agent's frontmatter, which Claude Code ignores. The subagent sentence in the
instructions block (D154) omits skipped agents. Agents already mismatched between
`name` and file name are not rewritten; only new writes are checked. Any future
per-client restriction mapping supersedes only the "no way to write a native
restriction" part of this decision, and must record each client's verified key
next to its matrix cell.
