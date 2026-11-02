---
topic: deployment-release
---

# D1459 — What a release shipped stays on main, whatever branch it was cut from

**Decision.** Ten features released in 0.27.1 from `release-0.27` return to `main`. Anything a maintenance release ships counts as released on `main` too: taking it off goes through a retirement, never through a split like D1455's.

**Why.** D1455 took the unreleased work off `main`, but the commits it reverted included those backported to 0.27.1, so 0.27.2 removed released keys. A config with `artifacts: r` no longer started, and `kb_options.<kb>.access: read` was ignored without a word, which widens access.

**Alternatives rejected.** Telling users to stay on 0.27.1: the latest release would remain one that fails on a valid config. Releasing them as 0.28: it would wait for the rest of the features.

**Consequences.** Amends D1455 and D1443. Before a revert of work on `main`, compare with the latest tag of every maintenance branch: `git diff <tag> HEAD -- cmd internal web` must show no released code going away.
