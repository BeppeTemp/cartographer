---
topic: deployment-release
---

# D223 — the published Windows zip is a documented fallback, not a second channel

**Status: superseded by [D245](D245-windows-installs-through-install-ps1-not-winget.md)** — winget was withdrawn and Windows installs through `install.ps1`.

**Decision.** `docs/getting-started.md` documents how to install, upgrade and
remove Cartographer on Windows from `cartographer-windows-{amd64,arm64}.zip`
and `sha256sums.txt`, the assets every release already carries: verify the
checksum, extract into `%LOCALAPPDATA%\Cartographer\bin`, add that directory to
the **user** `PATH`. winget remains the only *supported channel*
([D218](D218-winget-is-the-only-windows-channel-and-the-archive-is-a-zip.md));
every page still leads with `winget install BeppeTemp.Cartographer` and points
at the manual procedure only as the fallback for a machine that cannot use it.
`README.md`, `install.sh`'s Windows refusal and the bundled `cartographer-ops`
skill each carry one line pointing there, and none of them restates the steps.

**Why.** Without this, a Windows machine that winget cannot serve has no
documented way in — and, worse, a machine already installed from the zip has no
documented way to ever move off that version: `install.sh update` dispatches to
the same Windows refusal, and the lazy self-repair both pages rely on assumes
something already replaced the binary on disk. Nothing does. The gap is the
normal state for the hours or days after every tag, since each release only
*opens* a pull request into `microsoft/winget-pkgs` (D218), and it is permanent
where winget is blocked by policy. The cost is prose that has to stay true:
a per-user directory name, a `PATH` idiom and a stop-the-service step now live
in the docs rather than in code that would fail a test when it drifts.

**Alternatives rejected.**

- *An `install.ps1`, a Scoop bucket or a Chocolatey package* — exactly what D218
  rejected, and this does not reopen it: documenting an artifact the pipeline
  already publishes adds nothing that has to be built, signed or kept working at
  release time.
- *Leaving the procedure to be reconstructed from the repository each time* —
  that is how it was done the first time, and it produced a correct install only
  because the reader could read `.goreleaser.yaml`; it also silently drops the
  checksum step, which `install.sh` treats as an error rather than a skip.
- *Documenting only the install* — the upgrade hole is the worse one: a missing
  install path blocks you once and visibly, a missing upgrade path leaves a
  working installation quietly pinned.
- *A machine-wide destination (`Program Files`)* — needs administrator rights and
  writes outside the user profile, unlike winget's own portable install.

**Consequences.** The manual path must never be presented as an alternative of
equal standing: pages lead with winget, and mixing the two is called out as
something to undo rather than to layer. Two invariants travel with the
procedure wherever it is restated. Checksum verification is a stop, not a
warning — a hand procedure must not be weaker than the scripted one. And
Windows locks a running executable, so `cartographer service stop` precedes the
extract when the native service is installed: `internal/skillbundle/bundle_test.go`
asserts that the ops skill keeps both that clause and the pointer, because the
skill is the only thing an agent doing this on a user's machine reads. A future
change to the installed layout (the directory name, the `PATH` scope) has to
land in `getting-started.md` in the same session, since nothing else describes
it.
