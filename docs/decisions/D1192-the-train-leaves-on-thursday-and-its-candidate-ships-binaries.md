---
topic: deployment-release
---

# D1192 — The train leaves on Thursday, and its release candidate ships binaries

**Decision.** The weekly train runs on working days (amends D956, supersedes
D989's Saturday freeze), Europe/Rome: the freeze runs from Wednesday 00:00 to
Thursday 18:00, the automatic release candidate is built on Wednesday at 00:30, and
the train leaves on Thursday at 18:00; development is open from Thursday after the
train through Tuesday 23:59, so a PR merged by Tuesday ships that Thursday. D1168's
rule (a candidate that already carries `main` is kept) now applies to the Wednesday
run, and D1180's Dependabot disarm moves to Wednesday 00:05. The candidate is the
image **and** a GitHub pre-release `v<next>-rc.<YYYYMMDD>` with the GoReleaser
archives and `sha256sums.txt`, built by `preview.yml` after the image smoke passed
and the image was pushed, with nothing stable touched: no cask, no MCP Registry
entry, no `latest` image, GitHub's latest release unchanged. The update channel
(`update check`, `kb_status` `latest_version`, the Atlas history) and both
installers skip every tag with a semver pre-release suffix, judged by the tag and
never by GitHub's `prerelease` flag; `CARTOGRAPHER_INSTALL_TAG` installs one tag
explicitly. `release.yml` ignores candidate tags (`!v*-rc.*`) and, after shipping
`vX.Y.Z`, deletes that version's candidates.

**Why.** The maintainer's heaviest deployments run on a machine used only on
weekdays: a candidate tried on Saturday and Sunday never met them, so `rc-ok`
vouched for something that had not run where it matters most. And an image-only
candidate could not exercise the native service or the client (`install.sh`, the
service replacement on `sync`, D199). It costs one working day of freeze a week in
the middle of the week, and a feature merged on Wednesday waits a week.

**Alternatives rejected.**
- Keep the weekend freeze and try the candidate on Monday morning before the
  train: half a day is not enough to run the client and the service, and a skip
  costs a whole week.
- Judge a candidate by GitHub's `prerelease` flag: every stable 0.x release carries
  it (D82, D211), so the channel would go dark.
- Publish the candidate binaries from `release.yml` on a pushed `-rc` tag: that
  workflow holds the tap token and publishes `latest` and the registry entry; one
  wrong condition and a candidate is stable. The candidate stays in `preview.yml`,
  which holds no stable secret.
- Delete older candidates when a new one is built: two candidates of one week can
  both be under test (a manual rebuild on another day); the stable release is the
  moment none is needed any more.

**Consequences.** The cron guard still matches `github.event.schedule` against
today's offset (both slots avoid the Sunday of a daylight-saving change); the
freeze look-back of the train is `last wednesday 00:00`, pinned with the crons in
`internal/repodocs/release_train_test.go`, and `scripts/freeze-open.sh` is the one
place that answers whether the freeze is open. A candidate's tag must keep a
hyphen, and a stable tag must never have one, or the channel and the tag filter
disagree. A candidate binary reports its tag: up to date while the newest stable is
older, a minor update once its version ships. Going back past a schema migration is
not supported. Cutover: v0.27.0 is the first train on this calendar, run by hand
because this change lands after it; v0.28.0 is the first automatic one.
