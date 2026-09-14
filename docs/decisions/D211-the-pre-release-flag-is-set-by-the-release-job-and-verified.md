---
topic: deployment-release
---

# D211 — The pre-release flag is set by the release job and verified there

**Decision.** A dedicated `prerelease-flag` job in `.github/workflows/release.yml`
marks a `v0.*` release as a GitHub pre-release and then reads the flag back,
failing the release if it is not set. A tag outside `v0.*` is asserted the other
way round: a stable release must not carry the flag. `"prerelease": true` stays in
`release-please-config.json` untouched, and is not the mechanism.

**And `install.sh` stops asking for `releases/latest`.** That endpoint is
documented to skip pre-releases, so making the flag work would have broken the
documented installation path — see *Why the flag could not simply be switched on*
below. It now reads `/releases?per_page=1`, which is ordered newest-first and
includes pre-releases.

[D82](D82-beta-marking-via-github-pre-release-flag-not-beta.md) chose this marker
and is still the right choice; this records that its implementation never worked
and what replaced it.

**Why.** D82 says every GitHub release is marked *Pre-release* until 1.0, and
`CONTRIBUTING.md` and the README badge both build on that. Measured:

| tag | `isPrerelease` |
| --- | --- |
| `v0.1.0`, `v0.1.1` | `true` |
| `v0.2.0` … `v0.12.3` | `false` |

The two that carry it are exactly the two D82 says were marked **by hand**
("operator action"). Every release produced by the pipeline since — twenty-one of
them — is a plain release. So the configuration key was never the mechanism; the
operator was, once, and then stopped being.

The failure is the shape this repository keeps finding: a claim that reads as
implemented, a config key that looks like it does the job, and nothing anywhere
comparing the two. The README badge is downstream of it — `?include_prereleases`
is justified in D82 by "with every release a pre-release, `releases/latest` would
freeze on the last stable one", which has been false for a year, so the badge has
been working for a different reason than the one written down.

**Why the flag could not simply be switched on.** `install.sh` resolved the
version to install through `GET /repos/{owner}/{repo}/releases/latest`, and that
endpoint excludes pre-releases by definition. Two failures follow from setting the
flag without touching it:

- **retro-marking everything** leaves no unflagged release at all, so the endpoint
  answers 404, `latest_tag()` comes back empty and the installer dies with
  "cannot determine latest release (rate-limited? set GITHUB_TOKEN)" — a message
  pointing at the wrong cause;
- **marking only future releases** is worse, because it does not fail: the endpoint
  keeps answering with the last unflagged release, so `curl … | sh` silently
  installs `v0.12.3` forever while releases keep coming.

This is also the likely reason the broken flag went unnoticed for a year: the
installer works *because* nothing is flagged. The two decisions are coupled, so
they land together — the fixture in `test/install/lib/fake-curl.sh` answers only
the new path, which means a regression back to `releases/latest` fails
`make test-install` rather than surfacing at a user's shell.

**So the flag is verified, not just set.** Setting it in the workflow would fix
today and leave the same hole: a token scope change, a `gh` behaviour change, or a
future `release: mode:` in `.goreleaser.yaml` that recreates the release could
quietly undo it. Reading the flag back in the same job costs one API call and turns
the next regression into a red release instead of a year of wrong metadata.

**Alternatives rejected.**

- *Delete `"prerelease": true` from `release-please-config.json`.* It is inert for
  the GitHub flag, so removing it is tidier — and it is an input to the tool that
  computes the next version, changed immediately after cutting a release, with no
  way to test the effect short of cutting another one. Not worth it for tidiness.
  The key is documented as not being the mechanism instead, in
  `docs/deployment.md` §CI/CD, where a reader looking for the flag will be.
- *Abandon the marker and correct the prose to match reality.* The cheapest option,
  and it throws away a decision that is sound: 0.x plus a visible pre-release badge
  is exactly what a pre-1.0 project should show, and D82's reasoning for preferring
  the flag over a `-beta` tag suffix (which would re-break `go install @latest`)
  still holds.
- *Make goreleaser own the flag* with `release: prerelease: true`. `mode:
  keep-existing` is deliberate — the release and its notes belong to release-please
  (D-note in `docs/deployment.md`) — and giving goreleaser a say over release
  metadata invites it to recreate what it is supposed to leave alone.
- *A manual operator step at each release.* That is what has been failing since
  v0.2.0.
- *Leave the twenty-one published releases as they are.* Retro-marking them is what
  D82 already did for `v0.1.x`, and a release history where the flag appears twice
  at the beginning and then never again is worse than either consistent answer.

**Consequences.** The twenty-one releases from `v0.2.0` to `v0.12.3` were
retro-marked as pre-releases (operator action, as in D82), so the whole published
history is now consistent. `releases/latest` consequently resolves to **nothing**
until 1.0.0: the README badge's `?include_prereleases` becomes load-bearing rather
than incidental, and any new link or script that reaches for `releases/latest`
will fail — reach for `/releases`, or for `/releases?per_page=1` when a script
needs the newest tag. At 1.0.0 the job's condition stops matching on its own,
which is one fewer manual step than D82's "at 1.0.0 the flag is removed"; the
stable-tag assertion then starts guarding the opposite direction, and
`releases/latest` starts working again on its own.
