---
topic: architecture
---

# D1892 — a Surveyor can own the repair of its KBs: `NoModel` task kinds and the declared `tasks`

**Decision.** A task kind may be `NoModel`: its entry sets no model profile (a `model:` key is a load error), the runner builds no model client for it and applies no grant gate, and a config whose every task is `NoModel` may omit `models:`. The `repair` kind is the first: once per `repair_interval` per KB (default 24h, at least 1h, in memory) it calls `kb_repair` with `check: auto`, the same staged repair as the server's timer. The heartbeat gains an optional `tasks` list (`{kind, interval_seconds?}`, at most 16, interval clamped to 3600..691200) saying which task kinds a Surveyor runs.

**Why.** Repair is mechanical and needs no tokens; faking a model profile for it would put a meaningless budget and a model client in the config. The declaration is what a later change reads to let the server's own timer step aside for a KB a live Surveyor repairs; it is additive on both sides.

**Alternatives rejected.** A dummy profile with a zero budget: the grant gate would skip the task forever. A new route for the declaration: the heartbeat already carries what the Surveyor is. Deriving the declaration on the server from the settings report: that report is constant per process and not tied to liveness.

**Consequences.** Any new `NoModel` kind must never reach a model (`Runtime.Profile` and `Model` are zero).

**Handover.** The server's background repair of a KB pauses while a live Surveyor owns it. This amends D907 ("no setting turns it off or moves it": `maintenance.auto_repair` switches the timer, and a Surveyor can take it over) and the "never acts on it" of D369 and D1259 for this one case. A declaration covers a KB when all hold: a beat within three intervals (`stopped` removes it), the KB among those accepted for a principal that can write it, `repair` in `tasks`, and a successful non-dry `kb_repair check: auto` that Meridian itself served to that principal on that KB within the server's own timer period plus an hour (not a window the declaration sets). The observation is the server's, recorded where the call is served from the authenticated principal; a declaration alone, a refused call or a read-only token pauses nothing, and a restart forgets it (the timer runs, never the other way round). The timer asks before every run, skips it and re-arms at 15 minutes, so it resumes within one recheck after silence, a clean stop or a lapsed observation. The Atlas summary shows `delegated_to`; `MultiKBServer.RepairDelegates` is the seam. Repair on write is untouched.

**Why not trust the declaration.** A hostile or mistaken writer could otherwise leave KBs unrepaired by claiming `repair`; requiring a success the server saw bounds it to a principal that can write and does repair, and it could already commit to those KBs. The window is the timer's own period plus an hour, not a multiple of the declared interval, so one call buys at most a day and a half of pause and keeping it needs a successful call every period. The residual, accepted: a writer token that calls auto and beats with `repair` declared does pause the timer for that KB, and a Surveyor whose `repair_interval` exceeds the timer period never takes a KB over.

**Trade-off.** `kb_repair check: auto` is stamped `auto-repair (mcp)` for every caller: a token carries no Surveyor identity on this tree, and a reason string proves nothing, so `autoRepairSurveyorReason` stays unused until one exists.
