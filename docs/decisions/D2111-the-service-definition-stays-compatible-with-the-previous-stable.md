---
topic: control-plane
---

# D2111 — a service definition runs under the previous stable's binary

**Decision.** The command line of a service definition stays the one the previous stable wrote: the Windows task runs `serve --config <config> --log-file <log>` and nothing more, and the cron backend starts `serve` the same way. What a definition cannot carry is read by `serve` from the `service.env` beside its config (`EnvFilePath`): the proxy variables and, on Windows, the marker `CARTOGRAPHER_SUPERVISED=1`, read before the retry logic starts. `serve` keeps accepting `--supervised` and `--env-file` for the definitions release candidates wrote. `upgrade-repair` re-renders a running service's definition when its arguments differ from the current render, not only when its program differs. Amends D217 (the task's command line) and D1321 (what `upgrade-repair` compares).

**Why.** The candidate's task passed `--supervised` and `--env-file`, flags the previous stable does not define. A downgrade through the installer put the old binary under a task that exited 2, and the old `upgrade-repair` only switches a definition whose program path differs, so the task never recovered; only `cartographer service install` with the old binary did. launchd and systemd were not hit: their supervision marker is an environment variable. The previous stable cannot be fixed after the fact, so the new binary writes only what the old one understands.

**Alternatives rejected.** The installer re-rendering the definition on a downgrade: the old binary's `upgrade-repair` is what runs, and it cannot know. Documenting the command only: leaves every downgrade broken until the user reads it. A wrapper script as the task's program: another file to keep in step with the binary, and an argument the old binary may not parse either.

**Consequences.** A flag added to a definition's command line must be one the previous stable accepts; new inputs travel through the env file or the config. A hand-run `serve --config X` beside a Windows `service.env` is supervised too; its implicit read of `service.env` keeps any variable the process already has (an explicit `--env-file` replaces). After a downgrade from a release candidate the one command is `cartographer service install` with the installed binary; that binary's `upgrade-repair` does the same once it carries this change.
