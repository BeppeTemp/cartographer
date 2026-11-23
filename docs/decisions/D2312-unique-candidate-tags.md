---
topic: deployment-release
amends: D1192, D1384
---

# D2312 — Every release candidate has its own tag, and a release-only step is declared

**Decision.** A release candidate's tag is `v<next>-rc.<YYYYMMDD>.<n>`, `<n>` counted from 1 in the UTC day: `release-train.yml` makes it one more than the highest `<n>` among the newest 50 `preview.yml` runs, of any status, whose title carries `v<next>-rc.<today>.<k>`; a read of the runs that fails stops the step and dispatches nothing. Every reader of a candidate tag (the release gate, `candidate-cleanup`, `scripts/freeze-open.sh`) accepts the old shape `-rc.<YYYYMMDD>` as well; `freeze-open.sh` orders by date, then by `<n>` numerically, the old shape before `.1`. A test (`TestReleaseOnlyStepsAreDeclared`) maps every job of `release.yml` to its twin in `preview.yml` or to the reason it has none, and every step of a twinned job to the twin or to a declared reason.

**Why.** A second candidate of a day replaced the first under the same tag: assets downloaded again, a deployment pinned to the tag restarted to pull the new image, a round of acceptance able to tell candidates apart only by the sha. A failed build leaves no tag and no release, so its run title is the only record that a number was used. A publishing step that only `release.yml` runs is first executed on release day (0.29.0 stayed a draft for want of a registry login in front of an attestation, #2272); declaring it makes adding one a reviewed choice.

**Alternatives rejected.** The short sha as the suffix: not ordered within a day, and the freeze needs the oldest candidate. A counter per version: loses the date the readers sort on. Reading the releases instead of the runs: a failed build has none, so its number would be reused. Rehearsing the attestations in `preview.yml`: a candidate is not attested (D1022).

**Consequences.** D1168's single query for the oldest candidate is kept. Two `rc` runs started within the same seconds may compute the same `<n>`: the later build replaces the earlier under that tag, as a same-day rebuild did. The same tag is now rebuilt only when a run of it is started again by hand. The next candidate is the first with the new shape; deployments that pin a candidate take the tag from the release PR comment, as before. Pinned by `release_train_test.go` (`TestCandidateDispatchNumbersTheDay`, the readers), `freeze_test.go` and `release_publish_test.go`.
