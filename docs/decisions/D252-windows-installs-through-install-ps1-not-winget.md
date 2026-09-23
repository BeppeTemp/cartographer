---
topic: deployment-release
---

# D252 — Windows installs through install.ps1, not winget

**Decision.** Cartographer is installed, upgraded and removed on Windows with
`install.ps1` at the repository root — `irm …/install.ps1 | iex` — the
counterpart of `install.sh` with the same contract: newest release
(pre-releases included), SHA-256 verified against `sha256sums.txt`, per-user
destination (`%LOCALAPPDATA%\Cartographer\bin` on the **user** `PATH`, no
administrator rights), `upgrade-repair` after an update, and an `uninstall` that
refuses while the Scheduled Tasks are registered. The winget channel is
withdrawn: no `winget:` block in `.goreleaser.yaml`, no `WINGET_PKGS_TOKEN`, no
fork of `microsoft/winget-pkgs`, and the submissions still open there were closed.
The release keeps shipping the Windows assets as zips, which is now what
`install.ps1` reads. Supersedes
[D218](D218-winget-is-the-only-windows-channel-and-the-archive-is-a-zip.md)
(except for the zip format) and
[D223](D223-the-published-windows-zip-is-a-documented-fallback.md).

**Why.** winget never delivered a single install. Every tag opened a pull request
into `microsoft/winget-pkgs` that sat in review for days — 0.13 through 0.16.1
never reached users through it — so the "only channel" was in practice the
hand-driven zip fallback of D223: six PowerShell steps with a checksum to compare
by eye, a `PATH` edit, a service stop before every upgrade, and a new window to
open before anything worked. That is the worst onboarding of the three platforms,
on the platform where it was supposed to be the simplest. A script in this
repository ships the moment a release does, is tested by our own CI, and does
what winget's portable install could not: repair a running service in place. The
cost is a second installer script to keep correct, which is exactly what D218
rejected; the Windows CI job running it end to end under both PowerShell editions
is what makes that cost acceptable now.

**Alternatives rejected.**

- *Keep winget and wait for the review.* Each release restarts the wait, and a
  0.x project releases often; the channel would be permanently one or more
  versions behind, and the D223 fallback would stay the real path.
- *Keep only the zip procedure (D223) as the one Windows path.* Nothing new to
  maintain, but it is the slow, error-prone install this decision exists to
  remove: the checksum comparison and the service stop are exactly the steps a
  hand procedure skips.
- *Scoop or Chocolatey.* The same third-party review problem as winget
  (Chocolatey moderates every version), or a bucket of our own that users must
  add first — a second step for no capability the script lacks.
- *`cartographer self-update` in the binary.* Would cover upgrades only, still
  needs a first install, and would put a self-replacing executable on a platform
  that locks running binaries; the installer already solves both.

**Consequences.**

- **Under `iex` the script runs in the user's own session**, so it never calls
  `exit` (that closes the window) — failures `throw` — and sets
  `$ErrorActionPreference`/`$ProgressPreference` only inside functions. It also
  patches the current process's `PATH`, so `cartographer` works on the next line.
- **An update renames the running executable aside** (`cartographer.exe.old`)
  instead of stopping the service: Windows forbids overwriting or deleting a
  running image but allows renaming it. Leftover `.old` files are deleted on the
  next run once nothing holds them. `docs/getting-started.md` no longer tells
  anyone to stop the service before an upgrade.
- **The service task records the plain install path.** `resolveStableBinPath`
  no longer prefers a winget shim: `install.ps1` replaces the same file on every
  upgrade, so the path `service install` ran from is already stable.
- **A missing `sha256sums.txt` is an error on Windows**, stricter than
  `install.sh`: every release carrying Windows zips carries the file.
- **The Windows asset names are an interface.** `cartographer-windows-{amd64,arm64}.zip`
  containing `cartographer.exe`: renaming either breaks every existing
  `install.ps1 update`. `test/install/goreleaser_guard.sh` pins the zip shape and
  the absence of a `winget:` block; `test/install/windows/run.ps1` runs the
  script against a real build on the `test-windows` job, under PowerShell 7 and
  Windows PowerShell 5.1.
- **Nobody is left on winget**: no submission was ever merged, so there is no
  installed base to migrate and no shim path in any registered task.
