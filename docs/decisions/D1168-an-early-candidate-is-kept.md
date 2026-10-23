---
topic: deployment-release
---

# D1168 — A candidate built from the current main is kept by the Saturday run

**Decision.** The scheduled Saturday run of `release-train.yml` (D956) does not build a release candidate
when the newest successful candidate was built from the current `main` head: it comments on the release PR
that the candidate already carries `main`, so an `rc-ok` added after that build stays valid. A manual
`action=rc` always builds. Pull requests that touch the README film try its render on 30 frames
(`film-check.yml`), with the tools of `film.yml`.

**Why.** A maintainer who tries the candidate on Friday, before the freeze, had to try and label it again
after Saturday 00:30 for an identical image, because the rebuild made `rc-ok` predate "the current
candidate". And the film's encode chain first met the runner's tool versions inside the candidate (#1082),
where a failure blocks the train: the first one did (#1165).

**Alternatives rejected.**
- Let the train accept an `rc-ok` older than the newest build of the same `main`: the label would then mean
  "some candidate was tried", not "this one", and a rebuild that pulls newer base images would go untried.
- Render the whole film on every film pull request: a full render is up to two hours of a runner for a
  check whose failures so far were in the encode step, which 30 frames already exercise.

**Consequences.** A manual candidate built on Friday is what the train ships if `main` does not move; one
that moves `main` afterwards is rebuilt on Saturday as before. The skip reads the candidate exactly as the
train does (newest successful `preview.yml` run named `-rc.<YYYYMMDD>`), and the two must stay the same
query. A newer failed rebuild of the same `main` is not looked at, as the train does not look at it either.
