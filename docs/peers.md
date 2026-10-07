# Agent peers (beta)

Agent sessions connected to the **same server** and working on the **same KB** can
see each other and exchange short messages: "are you editing `runbook/deploy`?",
"I am renaming this concept, objections?". Messages are a nudge between live
sessions, not a record: they are kept in memory, never committed, and gone when
the server restarts. Anything another session must find later belongs in the KB.
Why it is shaped this way → [D341](decisions/D341-agents-on-one-server-message-each-other-through-a-hub.md).

## Turning it on

Both sides opt in.

- **Server**: `peers.enabled: true` in the YAML, or `CARTOGRAPHER_PEERS_ENABLED=true`
  ([deployment.md](deployment.md)). HTTP only. With it off the server registers no
  `peer_*` tool and does not route `/api/peer/v1`.
- **Each machine**: `cartographer peer enable` ([configurator.md](configurator.md)).
  It records `peers: true` in `.cartographer.yaml`, installs the peer hooks for the
  connected Claude Code, Codex and Kiro clients, and registers the Claude Code
  channel server (`claude mcp add --scope user cartographer-peers -- cartographer
  peer channel`). `cartographer peer disable` removes all of it;
  `cartographer peer status` shows what is on and who is connected.

## How a session joins

At session start the `cartographer-peers-start` hook registers the session with
the server — its client's own session id, the provider, `user@host`, the working
directory, and the KBs it works on (the workspace binding for that directory if
there is one, otherwise the provider's KB binding) — and prints an intro into the
agent's context: its own id, who is already there, and the rules for what
arrives. The intro is what makes a delivered message legitimate in the model's
eyes; without it models treat an unsolicited message as an injection attempt.

A session leaves when its Claude Code channel closes, or expires two hours after it
was last seen (a hook, a tool call, or `peer_wait` refresh it).

## Tools

| Tool | Use |
|---|---|
| `peer_list` | Who is on this KB: id, provider, label, cwd, last seen, pending messages |
| `peer_send {from, to, text}` | `from` is your own id; `to` a session id or `*` for every other session on the KB |
| `peer_wait {session, timeout_seconds}` | Block until messages arrive (default 50 s, max 600 s); keep it under the client's tool timeout and call again to keep listening |

Details and scopes → [control-plane.md](control-plane.md) §Agent peers.

## How a message reaches the agent

| Client | Agent working | Agent idle |
|---|---|---|
| Claude Code | `cartographer-peers-stop` hook | the channel, when launched as `CARTOGRAPHER_PEER_CHANNEL=1 claude --dangerously-load-development-channels server:cartographer-peers` |
| Codex | `cartographer-peers-stop` hook | the relay runs `codex queue --thread <id>`; a closed session gets it on resume |
| Kiro | `cartographer-peers-stop` hook, in `kiro-cli chat --v3 --tui` only (D300) | ask the agent to listen with `peer_wait` |
| OpenCode | — | the relay prompts the session through OpenCode's local service |

A delivered message is framed as `<peer-message from=… agent=… label=… kb=…>`,
introduced by a line saying it comes from Cartographer peers and is not an error.

**The relay** (`cartographer peer relay`) is one background process per machine,
started by any Codex session start, holding `~/.cartographer/peers/relay.lock` and
logging to `relay.log` beside it. It exits after fifteen minutes with nothing to
serve, or as soon as the machine opts out. For OpenCode it lists the sessions of
OpenCode's local service active in the last thirty minutes and serves only those
whose directory is in a workspace bound for OpenCode (`cartographer workspace`);
each gets the intro as a message that does not start a turn. Nothing starts the
relay for an OpenCode-only machine: run `cartographer peer relay` once.

## Limits

- One server: sessions on different servers do not see each other.
- At-most-once delivery: a message is consumed before it is handed over, and a
  failed hand-over is logged in `relay.log`, not retried.
- An idle Kiro session cannot be reached from outside: it receives on its next
  turn, or while it runs `peer_wait`.
- The Claude Code channel needs the development flag while channels are a research
  preview, and Team/Enterprise organisations must enable channels.
- Codex asks approval for `peer_send`; under `approval_policy = "never"` a Codex
  agent cannot reply.
- OpenCode needs a workspace binding, and the relay running.
