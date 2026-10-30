# Source types: where to look, what to ask for

For each family: where to find a way in, what a stable **key** and a **revision** usually are, the read-only
identity to ask for, and the traps. Prefer an existing MCP server or a documented API; never script a login page.

## Wiki space or document library

- Look for: a git-backed export, a REST API with a "changed since" query, an MCP server, a sitemap.
- Key: the page or file ID (never the title, which is renamed). Revision: the page version number or modified time.
- Identity: a dedicated viewer role on that space only.
- Traps: pagination; deleted pages (emit `removed` when the API tells you, otherwise they are not seen); attachments
  and binary files (skip or list their locator); a title in the key.

## Tracker project

- Look for: a search API with an `updated >= cursor` filter, or an MCP server.
- Key: the issue key. Revision: the `updated` timestamp or version. Content: the issue text and comments the unit
  needs, within `max_content_bytes`.
- Identity: a viewer or reporter-read role on one project.
- Traps: comments edited without bumping `updated`; private fields; rate limits (the cursor carries the position).

## Git repository of documents

Use the built-in `git-docs` (the host must be in `git.allow_hosts`); no script is needed.

## Site with an index

Use the built-in `http-pages` (the host must be in `fetch.allow_hosts`); no script is needed.

## Live systems (cluster, cloud account, metrics endpoint)

A live system is the **current state** of something, not a stream of changed documents: use `shape: system` (protocol
in `references/lister-protocol.md`). If the installed worker does not accept it, export the state to a git repository
or a file the feed recipes can read.

- A group is a namespace, a service or an account region. For a metrics endpoint list the **definitions** (alert
  rules, dashboards, targets) or **verdicts** computed at the source, words of a fixed scale, never samples: a line
  holding a value changes at every poll and costs a unit (`worker-recipes.md` §Verdicts, not metrics).
- The lister emits a normalized, deterministic snapshot: volatile fields dropped (timestamps, resource versions,
  counters), no secret value ever. Endpoint and identity come from the recipe's `env`, never from the `locator`.
- Identity: a Kubernetes `view` ClusterRole (it excludes Secrets); a cloud read-only or view-only policy (check what
  it can still read); for a metrics endpoint a read-only API token. The preflight answers true only if the
  write-class and secret-read questions answer no and a read question answers yes: an identity that can read nothing
  is a misconfiguration.
- Worked example in the Cartographer repository:
  [`examples/system-recipes/kubernetes/`](https://github.com/BeppeTemp/cartographer/blob/main/examples/system-recipes/kubernetes/README.md),
  and for verdicts [`examples/system-recipes/prometheus-verdicts/`](https://github.com/BeppeTemp/cartographer/blob/main/examples/system-recipes/prometheus-verdicts/README.md).
