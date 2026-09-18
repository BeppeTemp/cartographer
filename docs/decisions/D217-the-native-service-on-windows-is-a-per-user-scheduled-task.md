---
topic: deployment-release
---

# D217 — On Windows the native service is a per-user Scheduled Task, driven by PowerShell

**Decision.** `internal/service` gains a third platform: `cartographer service
install|uninstall|start|stop|restart|status` and `service sync-timer` work on
Windows through a per-user **Scheduled Task** in the `\Cartographer\` folder
(`Serve` and `Sync`), registered with the PowerShell `ScheduledTasks` cmdlets from
a task XML written to `%LOCALAPPDATA%\cartographer\tasks\`. `serve` and `sync`
gain `--log-file` so the task's argv stays flat, and `serve` additionally listens
on a named event, which is the only graceful stop this platform can deliver. This
**amends D73**, which named launchd and systemd as the two implementations of the
native local service.

**Why.** Everything that reaches the server on this machine goes through this
package: `connect` offers to install the service, `status` and `doctor` report it,
`upgrade-repair` restarts it, `kb rename` restarts it. All of it answered
`service: unsupported platform "windows"`, so a Windows client could talk to a
remote server and nothing else. The cost is a third set of platform commands to
keep in step with two others, and one genuinely new mechanism — the named event —
that has no counterpart to copy from.

**Alternatives rejected.**

- *A real Windows service (SCM), the closest analogue of launchd/systemd.* It has
  actual supervision (`sc failure`), and it is **per-machine and needs
  administrator rights**. That contradicts the premise of the package, whose paths
  all derive from one user's home and whose install must not prompt for
  elevation. A task registered for the current user needs none.
- *`schtasks.exe` instead of the cmdlets.* Its `/Query` output prints a
  **localised** state — an Italian Windows says `In esecuzione` — and this package
  already decided, in the comment above `launchdJobLoaded`, that liveness is
  judged on exit codes because the text is localised. `Get-ScheduledTask` returns
  a state whose string form is locale-invariant, and even that is compared inside
  PowerShell so only an exit code crosses back.
- *`pwsh` instead of `powershell.exe`.* PowerShell 7 is an optional install. A
  service manager that works only where someone installed a newer shell is not a
  service manager.
- *Wrapping the action in `cmd.exe /c "… >> log 2>&1"` instead of adding
  `--log-file`.* It works, and it turns `<Arguments>` into a nested quoted command
  line that `EffectiveConfigPath` then has to parse to recover `--config`. That
  function exists to fail with a contextual error **before** anything is signalled
  (D121); a flat argv is the version of it that can be trusted.
- *Deriving "installed" from a scheduler query.* `Status` and `SyncTimerStatus`
  define installed as `os.Stat` of the definition on disk, and
  `EffectiveConfigPath` *reads that file*. Asking the scheduler instead would fork
  the meaning of the field across platforms and leave `EffectiveConfigPath` with
  nothing to read.
- *Reporting `Running` from the task's `State`.* A task distinguishes `Ready` from
  `Running`, which is more than launchd reports — and `Running` means "the
  scheduler knows the job" on every platform. Whether it is serving is what
  `/health` answers, and `Healthy`/`HealthSkipReason` already carry that (D174).
- *`Stop-ScheduledTask` as the graceful stop for `Replace`.* It is a kill. The
  in-flight requests and the debounced pushes that D76 WP4's drain flushes would
  be lost on every upgrade, silently.
- *A PATH in the task definition, mirroring D156.* The format cannot express one:
  an `Exec` action has `Command`, `Arguments` and `WorkingDirectory`, and no
  environment block. It also does not need one — D156 exists because a launchd job
  inherits a *minimal* PATH that excludes Homebrew, while a task registered with
  an interactive token inherits the user's environment, where the machine and user
  PATH from the registry already contains the winget, Scoop and Chocolatey shims.
  This is a deliberate deviation from the plan's WP3, recorded here rather than
  worked around in a wrapper.

**Consequences.**

- **`--log-file` is additive and means two slightly different things, on purpose.**
  For `serve` it redirects the standard logger only: stdout carries the MCP
  protocol in stdio transport, and redirecting it would move the protocol into the
  log. For `sync` it redirects stdout and stderr as well, because a sync reports
  itself with `fmt.Print` and has no protocol on stdout. Both append and neither
  rotates — launchd does not rotate either, and a policy invented here would be a
  second source of truth for something no other platform has.
- **The graceful stop is a named event in the `Local\` namespace**
  (`defaults.WindowsShutdownEventName`), created by `serve` and set by the
  Manager. Reachable by another process of the same user in the same session and
  by nobody else, which is the same reachability as sending SIGTERM on unix — not
  a new exposure. If the event cannot be created, `serve` logs and starts anyway;
  if it cannot be opened, `Replace` fails loudly rather than claiming a drain it
  did not perform.
- **`signalGraceful` restarts the task explicitly, and that line must not be
  removed.** A Scheduled Task is not a supervisor: `RestartOnFailure` reacts to a
  failure and a logon trigger fires at logon, so nothing brings back a process
  that drained and exited cleanly. Without the relaunch, `Replace` would poll
  `/health` against a port nobody is listening on for its whole timeout.
- **Four Task Scheduler defaults are overridden and each is pinned by a test**:
  the 3-day `ExecutionTimeLimit`, the two battery settings, and
  `StopOnIdleEnd` — every one of them would kill a long-running server, and the
  battery pair only on a laptop, which is the machine least likely to be the one
  it was tested on.
- **The task XML is UTF-8 and declares UTF-8.** `schtasks /Create /XML` demands
  UTF-16LE with a BOM; `Register-ScheduledTask -Xml` takes the document as a
  string, so the file can stay a plain UTF-8 document that
  `EffectiveConfigPath` reads with an ordinary read.
- **The `<Arguments>` splitter is quote-aware, unlike the systemd one.**
  `extractUnitConfigPath` can use `strings.Fields` because a unit's `ExecStart` is
  a unix command line; here `C:\Program Files\cartographer\server.yaml` is an
  ordinary path, and `strings.Fields` would report `"C:\Program` as the configured
  config path.
- **`os.Getuid` returns -1 on Windows** and no Windows branch may reference it —
  the tests never stub it, so a branch that built `gui/-1/com.cartographer.serve`
  would pass every one of them. `noGUIDomain` asserts no recorded command carries
  a `gui/` target.
- **`TestUnsupportedPlatform` now uses `plan9`.** It was the canary for the
  `default` case and it named `windows`, which this decision makes supported; the
  guarantee it protects — an unhandled GOOS refuses rather than half-works — is
  unchanged.
- **The `getenv` seam is now part of the test contract of this package.** A test
  that stubs `userHomeDir` and not `getenv` resolves the Windows paths through the
  *real* `%LOCALAPPDATA%`, which on the `test-windows` runner means writing task
  definitions into the runner's actual profile. `withTestHome` neutralises it;
  every test that does its own setup must too.
- **The schema validity of the two XML documents is not verified by CI.** No test
  registers a task — the runner would need one, and `make gate` must stay
  side-effect-free — so the element order follows what Task Scheduler's own export
  writes, which it re-imports. A future edit that reorders `Settings` on aesthetic
  grounds can therefore break registration without failing a single test; the
  first real `service install` on Windows is the check.
- **A new config is written under `%APPDATA%`, and an existing `<home>\.config` one
  is still read.** A machine that ran a pre-Windows build, or that has a
  hand-written config in the unix location, keeps working: `ConfigPath` returns
  that file while the roaming one is absent, `install` then installs a task
  pointing at it, and nothing is moved or copied — a migration that silently
  relocated an operator's config would be a worse answer than reading it where it
  is.
