---
topic: control-plane
---

# D341 — Agent sessions on one server message each other through an in-memory hub, delivered the way each client allows

**Decision.** A server with `peers.enabled` keeps an in-memory hub (`internal/peers`)
of the agent sessions connected to it: presence per KB and a bounded mailbox per
session, never persisted and never committed. Agents use three MCP tools
(`peer_list`, `peer_send`, `peer_wait`); the client side registers sessions and
collects mail through `/api/peer/v1`. A session is identified by its client's own
session id, and every tool names the caller's id explicitly, because a stateless
MCP request carries none. Delivery into the agent is per client, with what each one
actually offers (probed on Claude Code 2.1.290, Codex 0.160.1, Kiro CLI 2.27.1 and
OpenCode 2.0.20):

| Client | Agent working | Agent idle |
|---|---|---|
| Claude Code | `Stop` hook answering `{"decision":"block","reason":…}` | the Claude Code channel (`peer channel`, `notifications/claude/channel`), when launched with `--dangerously-load-development-channels server:cartographer-peers` |
| Codex | the same `Stop` hook | the relay runs `codex queue --thread <id>`: an open, idle TUI starts a turn within seconds, a closed one gets it on resume |
| Kiro | the same `Stop` hook (V3 TUI only, D300) | none from outside: the agent listens with `peer_wait` |
| OpenCode | — | the relay prompts the session through OpenCode's local service (`/api/session/<id>/prompt`, `delivery: queue`) |

A machine opts in with `cartographer peer enable`, which installs two reserved
client hooks (`cartographer-peers-start`, `cartographer-peers-stop`) for Claude
Code, Codex and Kiro and registers the channel server with `claude mcp add`.

**Why.** The ask was real-time messaging like Claude Code's own between local
sessions. The network part is easy once every agent already talks to the same
server; the hard part is putting a message into an agent that is not asking for
one. An MCP server cannot start a turn, and the probes showed each client exposes a
different, partial way in: Claude Code has channels; Codex has a persistent
per-thread queue drained by its shared app-server; OpenCode 2 runs a local service
with a durable prompt inbox; Kiro's agent server runs per TUI over stdio, its MCP
notifications only evict caches, and its native `send_message` reaches only sessions
of the same process. A `Stop` hook that blocks with a reason works on Claude Code,
Codex and Kiro alike and covers the busy case. The probes also showed what decides
whether a delivered message is acted on: Kiro refused, as social engineering, a
message that arrived in session-start output with no explanation, and one that
looked like a probe; it answered once the session-start context explained the
protocol and the message said who delivered it and that it was not an error. So
the intro and the message frame are part of the mechanism, not cosmetics.

**Alternatives rejected.**
- *Each machine opens a port and peers connect directly, with discovery across
  networks.* Listening on a non-loopback interface triggers the macOS firewall
  prompt and looks like what endpoint security flags; colleagues on different VPNs
  rarely reach each other anyway. Every agent already reaches its server, so the
  server is the hub. Cross-server messaging is a separate, later question.
- *Git (the KB remote) as the mailbox.* Reachable by everyone, but latency is the
  fetch interval, and messages are not KB content.
- *An MCP session id as identity.* The HTTP transport is stateless (D133): there is
  none. The client's own session id is also what `codex queue` and OpenCode's API
  take, so hooks, relay and tools agree with no mapping.
- *A per-session sidecar MCP server for every client.* Codex spawns MCP servers from
  its shared daemon, so a sidecar cannot learn its thread; hooks get the id on
  stdin. Only Claude Code needs a stdio process, because a channel must be one.
- *OpenCode through Cartographer's generated plugin.* OpenCode 2 refuses that
  plugin format ("must export a default definition with an id"); its service API is
  the supported way in, through the registration file its own clients read.
- *Registering every recent OpenCode session.* The service lists every session on
  the machine; the first version did, and wrote an intro into sessions outside any
  KB's perimeter. Only a session in a workspace bound for OpenCode joins.
- *Persisting mailboxes.* Presence is meaningless after a restart, and a message is
  a nudge between live sessions, not a record: anything durable belongs in the KB.

**Consequences.**
- The hub is per server process: sessions on different servers do not see each
  other. A restart empties it; hooks and the channel re-register on the next 404.
- A session id is not a credential: the hub binds each session to the principal
  that registered it, so another token can neither read its mail nor speak as it.
  With auth off every caller is `local-admin`, which is the single-user case.
- Peer tools are whole-KB tools: `peer_list` and `peer_wait` need read scope,
  `peer_send` needs write. Joining a KB through the API needs read access to it.
- Delivery is at-most-once: the relay and the hooks consume mail before handing it
  over, and a failed `codex queue` or OpenCode prompt is logged, not retried.
- Codex treats `peer_send` as a tool needing approval; with `approval_policy =
  "never"` the agent cannot reply.
- Each client mechanism is a dependency on that client's current behaviour —
  Claude Code channels are a research preview, `codex queue` and OpenCode's v2 API
  are new — so a harness re-audit (`harnesses.md`) has to re-check them.
- Messages stay outside git and the audit trail; the KB remains the place for
  anything another session must find later.
