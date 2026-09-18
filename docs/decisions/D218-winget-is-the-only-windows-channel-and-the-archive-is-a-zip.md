---
topic: deployment-release
---

# D218 — winget is the only Windows channel, and its archive is a zip

**Decision.** The release builds `windows/{amd64,arm64}` and ships those two assets
as **zips** containing `cartographer.exe`, while darwin and linux keep their raw
per-platform binaries. GoReleaser generates the three winget manifests for
`BeppeTemp.Cartographer`, pushes them to the fork `BeppeTemp/winget-pkgs` on a
branch per release, and opens a pull request against `microsoft/winget-pkgs`.
`winget install BeppeTemp.Cartographer` is the **only** Windows channel: no
`install.ps1`, no Scoop bucket, no Chocolatey package. `install.sh` keeps refusing
on Windows, now naming winget instead of the platform.

**Why.** D215–D217 made the binary compile, the client reach parity and the
service installable on Windows; none of that is reachable without a way to get the
binary onto the machine. One channel is the whole point of the decision: a second
one doubles the surface that has to be correct at every release, for a platform
where the first one already puts `cartographer` on `PATH`. The cost is real and
worth naming — a winget submission is a pull request into someone else's
repository, reviewed by Microsoft's automation and by humans, so a release is no
longer entirely in our hands, and every 0.x tag opens another one.

**Alternatives rejected.**

- *A raw-binary `portable` installer instead of a zip.* It is what the existing
  single `archives` entry already produces, so it would have been the zero-change
  option — and the winget pipe reads the archive to decide the installer: a binary
  takes the uploadable-binary branch, giving `InstallerType: portable`, no
  `PortableCommandAlias`, and the command registered as **`cartographer.exe`**. The
  zip gives `NestedInstallerType: portable` with the alias trimmed to
  `cartographer`. The two cannot be mixed for one platform either — the pipe
  refuses an archive set containing both `.exe` and `.zip`.
- *Converting the whole `archives` entry to zip.* It would rename the darwin and
  linux assets, and `install.sh` reconstructs those names from `uname` and verifies
  them against their line in `sha256sums.txt`, where a missing entry is an **error**
  and not a skip. Every existing `install.sh update` would break at once, silently
  for anyone not reading the output.
- *A manifest repository of our own plus `winget source add`.* No cross-repo pull
  request to wait on, and no `winget install BeppeTemp.Cartographer` either, which
  is the entire reason to have the channel.
- *An `install.ps1`, mirroring `install.sh`.* A second Windows channel to keep
  correct — checksum verification, install directory, upgrade path, uninstall
  refusal — for no capability winget does not already provide.
- *Scoop or Chocolatey.* Same reasoning, and each adds its own review and manifest
  conventions.
- *Deriving the `Dockerfile`'s platform list from `.goreleaser.yaml`.* The machinery
  to share one list across a YAML config and a shell loop inside a build stage costs
  more than the comment that names `.goreleaser.yaml` as the authority. The comment
  is the decision; if the lists diverge, the symptom is an artifacts export missing
  a platform, not a broken release.

**Consequences.**

- **`goreleaser check` and a `--snapshot` run are the only local verification**, and
  both were run: six artifacts, the two Windows ones as
  `cartographer-windows-{amd64,arm64}.zip`, the four unix names byte-identical to
  the previous release's, and manifests carrying `InstallerType: zip`,
  `NestedInstallerType: portable`, `PortableCommandAlias: cartographer`. The
  publishing half — fork push and cross-repo pull request — cannot be rehearsed
  without the token and is first exercised by a real tag.
- **`WINGET_PKGS_TOKEN` is a soft prerequisite.** The winget pipe continues on
  error, so a missing token logs and publishes nothing rather than failing the
  release. That is deliberate: a packaging channel must not be able to sink a
  release whose binaries are already correct. It also means its absence is **silent**
  — the check is that a manifest pull request appeared, not that the release
  succeeded.
- **The `.github/PULL_REQUEST_TEMPLATE.md` side effect is now a constraint on this
  repository.** GoReleaser uses that file, when present, as the body of the pull
  request it opens. There is none today; adding one for our own pull requests would
  start prefixing every winget-pkgs submission with it. The comment above the
  `winget:` block is where that is recorded, because the file it warns about does
  not exist to carry the warning itself.
- **Uninstalling on Windows has no hook of ours, and the documentation carries what
  the code cannot.** `winget uninstall` runs no Cartographer code, so a registered
  Scheduled Task (D217) survives the removal and is left pointing at a missing
  executable. `install.sh`'s refusal to uninstall a binary while the native units
  are installed has no Windows counterpart and cannot have one; README,
  `docs/deployment.md` and `install.sh`'s own Windows message all state the order
  instead.
- **`install.sh` decides the OS before the architecture.** Reversing that order put
  `unsupported architecture` in front of the winget hint for a Windows machine
  whose `uname -m` is unusual. The test scenario pins the message, not the exit code
  alone.
- **The packaging guard is now the only thing standing between a refactor and a
  broken Windows install**, since nothing in CI runs winget or Task Scheduler. Each
  of its new assertions was verified to fail when its target line is removed —
  including the negative ones, which are the easy ones to write wrong.
- **Two platform lists exist on purpose** (`.goreleaser.yaml` and the `Dockerfile`'s
  dist stage), with the authority named in a comment. The Windows entries there
  carry `.exe`: without it the artifacts stage exports two extensionless Windows
  binaries, which is neither runnable by name nor what the zip should contain.
