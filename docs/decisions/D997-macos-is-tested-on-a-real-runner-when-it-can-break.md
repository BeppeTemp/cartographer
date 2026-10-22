---
topic: project-governance
---

# D997 — macOS is tested on a real runner when it can break

**Decision.** A `test-macos` job on `macos-latest` runs three scripts under
`test/macos/`: the launchd service installed, restarted, stopped and removed for
real; `install.sh install` and `update` over a real running service (the fake curl
of `test/install/` serves the release, nothing touches the network); and the Cask
GoReleaser renders in snapshot mode, installed by Homebrew from a local tap where
only the `url` is rewritten. The job runs on the release PR, on a PR touching its
inputs (the `macos` output of `scripts/ci-filters.sh`), on `main` and on
`workflow_dispatch`; it is not a required check and `main-status` watches it. The
scripts run with the runner's real `$HOME`, never a sandbox one, and refuse to run
outside CI unless `CARTOGRAPHER_MACOS_E2E=1`.

**Why.** The primary desktop target was never run on macOS by CI: launchd was
asserted through a stub runner, `install.sh` against a fake binary, the Cask by a
grep of `.goreleaser.yaml`. A break in any of them reached users on a release day.

**Alternatives rejected.** Running on every PR: macOS runners queue slowly, and the
Windows leg already shows the cost of an always-on slow leg. A sandbox `HOME`: the
service code registers a job only when `$HOME` is the account's own home (#683), so
a sandbox would test nothing. A second harness: the existing shell harness already
serves the release list and the asset.

**Consequences.** `make gate` stays side-effect-free: nothing registers a launchd
job outside this job. Running the scripts on a Mac that has its own Cartographer
service would replace it (same label), hence the CI guard.
