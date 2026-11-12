---
topic: deployment-release
---

# D1972 — a KB that cannot be mounted at start is skipped and reported, and a failed clone restarts the server once it works

**Decision.** Over HTTP, a KB declared in `kbs[]` or `--kb` whose path is missing or whose remote cannot be cloned is skipped at start with one ERROR log line; the server serves the others. The skip is reported: `/health` `skipped_kbs` (name, reason, `retrying`) to a caller who reads that KB, `kb_status` and `/mcp/<name>` saying it is not mounted and why, `status` and `doctor`. The reason names no server path; the log has it. A failed clone is retried with back-off (30 s doubling to 15 min); when one succeeds the server, if it knows it is supervised (`CARTOGRAPHER_SUPERVISED=1`, set by the generated systemd unit and launchd plist and passed as `--supervised` by the Windows task, with the old heuristics for services installed before), shuts down gracefully with exit status 75 for its supervisor to start it with the KB mounted, and a server run by hand logs that a restart is needed and goes on. Stdio still stops, and so does an HTTP server left with nothing mounted, no clone to retry and no `data` directory; with `data` it stays up empty.

**Why.** One moved folder or one remote down at boot took every other KB with it, in a restart loop, against the rule that a KB that failed to mount must not become an outage. Meridian builds its mount tables once and has no lock around them, so a KB cannot be added to a running server.

**Alternatives rejected.** Mounting the KB live when the retry succeeds: needs locking through the routed views, aliases and rosters, a much larger change for a rare event. Not retrying (skip until the next restart): a temporary network error would disable the KB for as long as nobody restarts. Showing the raw error as the reason: it names the data directory and the clone destination to clients of a shared server. Exiting a hand-run server: nobody restarts it, so it would only turn a served set into none.

**Consequences.** The restart on a successful clone is a brief outage of every KB, taken only when a skipped KB can be served. Exit 75 is non-zero on purpose: launchd and systemd restart a failed process only. The Windows task's `RestartOnFailure` count of 3 is a lifetime budget, not per start. A bare container is not assumed supervised; one with a restart policy sets the marker. A reason added to `skipped_kbs` stays path-free and is shown only through `kbRosterVisible`. A live mount, if ever built, replaces the restart and not the skip.
