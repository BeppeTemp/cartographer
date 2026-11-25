---
topic: control-plane
---

# D2380 — the Atlas reads dashboards through two relay routes, and the revision token folds a stamp of `dashboards/`

**Decision.** `GET /kbs/{kb}/dashboards` and `GET /kbs/{kb}/dashboards/{name}` (preview tier) run the `dashboard_list` and `dashboard_get` handlers with the caller's principal, as `/work` does for `work_list`, so the page can never disagree with the agent and the narrowing of a caller without whole-KB read is the tool's. `{name}` is the first path variable besides `{kb}` of the route table: a `{x}` segment matches one non-empty segment and is declared as a path parameter of the OpenAPI document. The revision token of `/kbs/{kb}/revision` (D337) gains a suffix, the stamp of `dashboards/*.yaml` (name, size and mtime of each file, one `ReadDir`, `KB.DashboardsStamp`), and has no suffix while the folder holds no dashboard, so a KB without one keeps its token.

**Why.** A dashboard file is outside the link graph, whose generation is what the token counted: a dashboard-only edit, by an agent or a pull, would not have refreshed an open page. The stamp is not `lintInputsStamp`, which hashes the date and walks the whole KB root: the poll runs every 10 s per open tab and must stay cheap. The mtime and size are what the graph cache's own validation reads, so the stamp is as exact as it.

**Alternatives rejected.** `/kbs/{kb}/dashboard?name=` as `/concept?id=` is: it needs no new kind of route segment, but the plan's resource shape is the one a client of the public API expects. Folding `lintInputsStamp`: too heavy for a poll. Hashing the file contents: a read per file per poll.

**Consequences.** The token still moves only for a caller that sees the whole KB (the route is a whole-KB resource). A change of mtime without a change of content (a `touch`) moves the token and costs one refetch. A future route with a path variable reuses `isPathVar`; the demo export has one call for the list and one per dashboard.
