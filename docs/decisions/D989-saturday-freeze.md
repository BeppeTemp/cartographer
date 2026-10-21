---
topic: deployment-release
---

# D989 — The freeze and the release candidate move to Saturday

**Decision.** The freeze starts on Saturday at 00:00 Europe/Rome and the release
candidate is built at 00:30 the same night; the train still leaves on Monday at
08:30. It amends D956, which started both on Friday.

**Why.** The maintainer works on the project on Fridays too: with a Friday freeze,
a fix finished on Friday waited ten days, and the RC had a third day of testing
that nobody used. Moving the cut by a day gives Friday back to merging. It costs a
day of RC testing: Saturday and Sunday, not Friday to Sunday.

**Alternatives rejected.**

- Keep Friday 00:00 (D956): a working day lost to the freeze every week.
- A later cut, Sunday: one day of RC testing is too little to find a regression.

**Consequences.** The RC cron entries are Friday 22:30 and 23:30 UTC (Saturday
00:30 in Rome, summer and winter); the train's freeze check counts commits since
the last Saturday 00:00. Neither slot falls on a daylight-saving change, which is
always on a Sunday. `internal/repodocs/release_train_test.go` pins the new entries.
