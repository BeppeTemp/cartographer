---
topic: architecture
---

# D818 — The Atlas surfaces the server's features in six places, and the client reports its own settings

**Decision.** The Atlas shows what the server knows (Map contracts, settings,
placeholders, sources, services, secrets, contradictions, conflicts, history,
the journal and the audit log), but through six entries, not one panel per
feature:

- **Atlas**, **Activity**, **Work**, **Health**, **Artifacts** and **Settings**
  make up the rail. Each feature lives where the reader already looks for it.
  Secrets are a tab of Artifacts, grouped by the Service concept that uses each
  value; Services have no page of their own. A pending source is open work, a
  "to ingest" column of Work. Placeholders are configuration: the KB's
  declarations sit under Settings → Knowledge Base, and how they resolve on a
  machine sits under Settings → Client. An old link to a removed panel still
  lands on its new home.
- `map_list` returns the whole `_map.md` contract. `GET /settings` returns, beside
  the entries, the configuration in force as commented YAML: the server's file
  and each KB's `settings.yaml`.
- **The client reports its own settings to the server** after every
  `cartographer sync` (`POST /api/client-settings`). The report holds its keys,
  its `.cartographer.yaml`, its agent clients with their workspace bindings and
  an isolation label (issue 737), and where each placeholder resolves on the
  machine. The server keeps the report in memory, per principal and machine,
  and returns it only to the principal that sent it, as `clients` in
  `GET /settings`.
- `make dev` without `DATA` serves ten generated test KBs (`internal/testkbs`,
  5,000 concepts down to 30). They are built to put every Atlas view in a state
  worth judging, and they are the scale the Atlas has to handle (issue 861).

**Why.** One panel per server feature had grown the rail to ten entries, and
several of them answered a question another page already asked: a pending
source is work, a service is the reason a secret exists, a placeholder is a
setting. A reader judging the KB needs fewer places, each answering one
question.

The client's settings live on the workstation, which a server, possibly a
shared one, cannot read. A client report reuses what `sync` already computes:
the resolved placeholders come from the lockfile, the isolation label from the
provider registry. It also works with today's single-server Atlas. The cost is
that the machine's local paths travel to the server. They are the sender's own,
and only the sender sees them back: nobody else, and no list of clients.

A live session judges the Atlas by looking at it. The two demo KBs are too
small to show scale, and they lack most of the states: decisions to take,
conflicts, malformed contracts, a lint trend, KBs with no history.

**Alternatives rejected.**

- **The client serves the Atlas locally and proxies each server's UI API.** This
  covers several servers and keeps every path on the machine, but it adds a
  second way to serve the UI, a local process to keep running, and a proxy for
  auth. It waits until a client talks to more than one server.
- **Only a local service reads `~/.cartographer.yaml` itself**, when it runs as
  the same user. That is correct only on a workstation's own service; on a
  shared server the tab would never exist.
- **Persist client reports on the server.** A report describes a workstation,
  which the server does not own. The sync timer sends a new one every half hour,
  so a restart only leaves the tab empty until then.
- **Keep the panels and add a "more" menu.** Fewer entries, but the same
  scattering: the reader still has to know which of ten pages answers the
  question.
- **The demo KBs for `make dev`.** They stay what they are, a product demo and
  the export video's source, and do not grow test defects.

**Consequences.**

- A new server feature that the Atlas shows gets a section in one of the six
  places, not a rail entry. A removed panel keeps its URL redirect
  (`web/src/lib/viewstate.ts`).
- The client report is best-effort, like the usage report (D326). An older
  server answers 404 and the client ignores it; an older client sends no
  `providers` or `placeholders`, and the Atlas leaves those sections out. The
  server passes both through without reading them, so the client owns their
  shape.
- A change to a lint check or to a Map contract key can stop a planted state
  from showing in the test KBs: `go test ./internal/testkbs` names the check
  that no longer fires. Their dates are relative to the day they are generated,
  and `scripts/dev.sh` caches them per day and per generator source.
