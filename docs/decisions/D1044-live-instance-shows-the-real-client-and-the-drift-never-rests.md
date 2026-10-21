---
topic: architecture
---

# D1044 — A live instance shows this machine's real client, and the graph's drift slows instead of resting

**Decision.** `make dev DATA=<dir>` has this machine's installed client report to
the live instance: `cartographer sync --report-to <url>` sends the usage and
client-settings reports of D326 and D818 to that endpoint, and does nothing else.
It reads the config without saving it, provisions nothing and does not contact
the configured server. In the Atlas, the live drift no longer rests when a tick
costs more than `DRIFT_TICK_BUDGET_MS` (D889). It runs at half rate instead
(`DRIFT_SLOW_EVERY`), and rests only past `DRIFT_REST_MS`, 12 ms, a tick that
is a visible hitch even at half rate (about 5,000 concepts on an Apple M5).
While the panorama turns with nobody at the controls, the camera eases very
slowly onto the fit, so hours of drift never carry the network off screen.

**Why.** A live session on copies of real KBs showed the test fleet's invented
client on the Settings page, and no usage on the Artifacts cards: the bugs that
only real data shows (long paths, many workspaces, listed but never invoked
skills) could only be seen on the production instance. On those KBs (800 to 900
concepts and 4,000 links) a tick sits right on the 4 ms line, so the network
breathed or froze depending on the moment. That read as a bug, and the reader
asked for the motion always on. A half-rate drift costs half the CPU of a
full-rate one and still breathes. The 12 ms cap keeps D889's reason for the
largest graphs.

**Alternatives rejected.**
- Point the installed client at the live instance (`connect` to its URL). It
  rewrites the real configuration and provisions from the copies, and a
  forgotten switch back leaves the machine on a throwaway server.
- Have `dev.sh` build the report itself from `.cartographer.yaml`. It would
  duplicate the client's report code, and the usage scan needs the lockfile and
  the transcripts, which only the client reads.
- Keep the 4 ms rest (D889 unchanged). It is the behaviour the reader asked to
  remove.
- Run the drift at full rate on every graph. At 2,000 concepts a 7.5 ms tick
  every frame takes half a 60 Hz frame from the rest of the page.

**Consequences.** `--report-to` sends the configured token to the URL it is
given. The URL goes through the same check as `server_url`: plain `http://` is
refused unless it is loopback or insecure HTTP is allowed. `make dev` without
`DATA` still posts the test fleet's invented client. The idle refit moves the
camera only while the panorama turns, so a reader's own framing holds until
they leave the controls for `IDLE_RESUME_MS`.
