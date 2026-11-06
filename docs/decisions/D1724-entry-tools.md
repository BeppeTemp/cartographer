---
topic: client-configurator
---

# D1724 — Cartographer works with each client's tool hiding and pins only its entry tools

**Decision.** Four tools are the entry tools: `atlas_overview`, `search`, `concept_read` and
`graph_context`, the reading path the managed block names (`configurator.EntryTools`, one list).
(1) `tools/list` carries `_meta: {"anthropic/alwaysLoad": true, "antigravity/alwaysLoad": true}` on
them and on no other tool (`Tool.AlwaysLoad`). (2) A provisioned connection's `initialize.instructions`
(and `server/discover`) is now a fixed, one-line, client-neutral entry paragraph of at most 400 bytes,
followed by the read-only sentence when the session has one; it names the tools, never a call syntax.
A non-provisioned connection keeps the full guide. (3) The input schemas of the entry tools are kept
short and a test caps each. `search` already has no property descriptions (about 58 tokens, capped at 256 bytes), so only `concept_read` was trimmed. (4) Pi's Cartographer entry drops the entry-wide `"exposure": "direct"` and
writes `toolExposure` with the four tools `direct`; the other tools stay in Pi's codemode. A
third-party server of a KB `mcp` artifact keeps `"exposure": "direct"`. Nothing opts out of a client
feature: OpenCode gets no `codemode: false`, Codex and Copilot no whole-server opt-out.

**Why.** Most clients now hide MCP tools behind a code mode or a tool search. An agent whose client
shows it only `execute` and a partial catalog does not consult the KB unless told to (#1714). Switching
the feature off per client is a growing list of vendor keys and loses the client's own saving; the
per-tool pins that exist (`alwaysLoad`, `toolExposure`) put exactly the reading path in view and leave
the other tools hidden as the client chose. Where there is no pin (OpenCode, Codex, Kiro, Copilot) the
entry paragraph is the one hint the server can give, and short schemas keep the entry tools in the
size-ordered inline catalogs (OpenCode 2000 tokens, Pi 3000).

**Alternatives rejected.** `codemode: false` on the OpenCode entry (the first design, withdrawn): it
opts out of a client feature, needs a version gate and an `unsupported` diagnostic on 1.x. Keeping Pi's
entry-wide `direct`: it declares every tool of the server, the opposite of working with codemode. The
paragraph in the managed block only: a provisioned client already has the block; it is the hiding
client that does not show it. Naming `execute` or `tools["<server>"]` in the paragraph: a client syntax
that is wrong in every other client.

**Consequences.** Amends D1097 (a provisioned connection no longer gets nothing) and D1603 (the Pi
entry shape). An existing Pi entry is replaced by the next apply, which drops the old `exposure`. A
change to the entry tools is one list plus the `AlwaysLoad` flag, pinned together by a test. Codex,
Kiro and Copilot have no per-tool pin: upstream requests are filed separately. Pi's `toolExposure` keys are bare
tool names, verified in the installed Pi 1.5.0 source (`getMcpToolExposure` is called with the server's
tool name; the `mcp__` form is built later). Claude Code 2.1.318 honours the per-tool
`_meta` pin in its binary. Not yet observed in a real session on OpenCode 2.2.x, Claude Code and
Pi 1.5.x (#1714).
