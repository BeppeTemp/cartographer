---
topic: client-configurator
---

# D690 — The scheduled doctor is installed by `connect`, by default

**Decision.** `connect` (and `setup`, which runs it) schedules the headless `kb-doctor` job the way it installs the
sync timer (D325): when the timer is missing, no `doctor_timer_opt_out` is recorded, the machine knows exactly one KB,
and a connected client has a default grant (first in detection order). Time is `06:00`. The grant is the narrowest that
lets an unattended run use the KB, only the Cartographer server's tools: Claude Code `--allowedTools mcp__<server>__*`,
Copilot CLI `--allow-tool <server>`. `doctor unschedule` records the opt-out, `doctor schedule` clears it; a client
config that cannot be read counts as opted out. `sync` never installs it. This supersedes D369's "never done by
`connect`, `setup` or `sync`".

**Why.** The operator's choice: a KB in `unattended` mode with a daily interval is meant to be tended, and on a real
install the last session was four days old because nobody ran `doctor schedule`. A doctor that never runs leaves the
KB's debt growing. The cost is model quota spent unattended, which the install line states together with the way out.

**Alternatives rejected.** Keeping it opt-in: the doctor does not run where it matters. A permission-bypassing grant
(`--dangerously-skip-permissions`, `--allow-all-tools`): an unattended session must reach the KB's tools and nothing
else. Installing for every KB: one machine holds one doctor job (a single launchd label), so several KBs only get a
hint. A default grant for codex, opencode, kiro, antigravity and crush: no per-server flag is documented, so they keep
the explicit `--client-flag` path and a hint.

**Consequences.** Tests must never reach a real scheduler: `TestMain` stubs the doctor timer functions, as it does for
the sync timer, because the first run of the new code registered a real launchd job from a test binary. On upgrade the
next `connect`/`reconnect` schedules the doctor where eligible.
