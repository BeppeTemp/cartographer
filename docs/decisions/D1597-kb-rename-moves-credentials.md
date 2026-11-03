---
topic: deployment-release
---

# D1597 — `kb rename` moves the KB's credential files and both rename and unmount hold the KB lock

**Decision.** `kb rename` renames `<old>.<kind>` to `<new>.<kind>` for every per-KB credential kind (ssh, token, age, signing) in the same transactional step as the directory, undone with it on a failure, and refuses when a `<new>.<kind>` already exists. `kb unmount` leaves the credential files and lists them. Both commands take the KB's advisory process lock without waiting, before the first change, and release it just before the directory moves or the clone is deleted; unmount repeats its "pushed" check under the lock.

**Why.** Credentials are looked up by KB name (D905). A rename that moved only the directory left the KB with no push key, undecryptable secrets and no manifest signature, and said nothing. Both commands are documented as usable while the service runs, yet worked on the clone without the lock the server and `import` honour (D155): a server commit between the "pushed" check and the delete was lost.

**Alternatives rejected.** Only listing the files in the "NOT migrated" section (leaves the operator to move secrets by hand, and the KB is broken until they do). Refusing when the service lists the KB (the lock is the existing, kernel-enforced answer to "is someone writing"). Holding the lock across the move (Windows refuses to move a directory holding an open lock file).

**Consequences.** The window between the release and the move or delete is short but not zero. `kb unmount` keeps credentials on purpose: a KB mounted again under the name inherits them, and the listing says so. A rename onto a name with stale credentials needs the operator to move them first.
