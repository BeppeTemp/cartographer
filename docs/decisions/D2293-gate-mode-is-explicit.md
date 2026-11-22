---
topic: project-governance
---

# D2293 — The gate mode is a per-invocation choice with a hosted default, and every train says which gate it had

**Decision.** Where a maintainer's gate runs is decided in one place, `scripts/signoff.sh`, by `SIGNOFF_GATE`: `hosted` (the default, also when unset; `SIGNOFF_HOSTED` stays a synonym) dispatches `ci.yml` on the branch, `host` runs the targets on the build host through `SIGNOFF_MAKE`; any other value, or `host` with `SIGNOFF_HOSTED`, stops before anything runs. The lander chooses its mode only by its option, `--gate hosted|host` (`make land GATE=host`), never from its environment: it exports `SIGNOFF_GATE` itself, so `make ready` always starts a hosted lander. The mode travels out of band: it is the first line of every signoff, the first word of the `signoff` status description (`gate=hosted run=<id> legs=<n> ...`, `gate=host <platform> windows=<n> web=<yes|no> ...`), the lander's start and train log lines, and the third line of the train PR body, `Gate: <description up to " - make">`, read from the status of the gated head. A landed commit keeps the `Review:` trailer it was queued with.

**Why.** D2040 made the hosted gate the lander's, but the code selected it only when `SIGNOFF_HOSTED` was set in the environment of the shell that started the lander, and the lander inherits the environment of whatever session queued the first change. On one maintainer machine every gate of five consecutive days ran on the build host, with the Windows leg cut to the affected packages and no macOS leg, and `main` was red in about a fifth of its push runs, almost only on `test-windows`, while nothing printed or recorded which gate a train had had. A default in code, an explicit choice per invocation and a recorded mode make the divergence impossible to have by accident and visible when it is chosen. It costs a hand-pushed `make signoff` about ten minutes of `ci.yml` instead of a local run, and a maintainer who wants the build host for a day starts the lander by hand first.

**Alternatives rejected.**
- The environment variable alone (`SIGNOFF_GATE=host` exported also selects it for the lander): one mechanism, but a variable in a shell profile puts every lander back on the build host without anyone having chosen it that day, which is the trap measured.
- A local, unversioned settings file the lander reads: explicit, but a second machine or a stale file reproduces the divergence, and it is one more state that can be wrong without anyone seeing it.
- Only the lander defaults to hosted, a hand-run `make signoff` keeps the local default: a smaller change, but the script's header and its code keep disagreeing.
- Reading the mode from the gate log: the log holds the change's own output, so a test could print any mode.

**Consequences.**
- The status description is a parsed fact: it starts with the `gate=` token, holds no job names (`legs=<n>` counts `HOSTED_JOBS`), fits the 140-character limit whole, and its one word from the tree under test, the platform, must be `<os>/<arch>`. The lander accepts it only in that shape; anything else is `Gate: unknown`, which never stops a train.
- A hosted gate reached by the build host's exit 75 says `fallback=host-unavailable`, so "the mode asked for" and "the mode used" stay distinguishable.
- `SIGNOFF_MAKE` and `SIGNOFF_WINDOWS` mean something only with `SIGNOFF_GATE=host`; set in hosted mode they produce one notice line per gate, never their value.
- A red `main` reports the gate's coverage: `main-status` appends to the `ci-red` issue what `scripts/main-red.sh` reads of the range, each train PR with its `Gate:` line and, for each red leg, the trains whose gate did not run it whole (a fixed table per mode, naming exactly the legs `main-status` watches). A train is a merged PR from the repository's own `train/*` branch; it reads the API and the PR body, never a log, and fails open, so a coverage it cannot compute never keeps the issue from opening. The job gains `contents: read` and `pull-requests: read`, and a checkout of that one script without credentials.
- Amends D2040 (how the mode is selected: the default is in code), D1610 (the local gate is no longer a default anywhere) and D1706 (the train PR body carries the `Gate:` line; the landed commit keeps `Review:` beside `Lander-Slug` and `Lander-Tip`).
