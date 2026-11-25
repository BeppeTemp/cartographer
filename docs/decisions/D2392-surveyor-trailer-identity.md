---
topic: concurrency-git
amends: D950, D2281
---

# D2392 — A Surveyor is named by a trailer, not by the commit author

**Decision.** A Surveyor's client sends no `Cartographer-Author`. It sends `Cartographer-Surveyor: <perimeter>/<replica>`, which the server records as a `Cartographer-Surveyor` commit trailer next to `Surveyor-Run`, `Cartographer-Host` and `Cartographer-Token`; the commit's author and committer are the KB's configured identity like any write with no writer of its own (D950). `concept_history` and `changes_since` show a commit with the trailer as the author `surveyor/<perimeter>/<replica>`, so the Atlas, which reads that form, changes nothing, and a commit written under D2281 (author `surveyor/...` at `surveyor.invalid`) is still shown as before. `kb reauthor` moves such an author to the trailer when it replaces it, so a repair does not erase who wrote.

**Why.** D2281 put the Surveyor in the author line, and D950 makes a declared author the committer too. A forge with a membership push rule refuses an author or committer who is not a member, the failure D950 closed, so no Surveyor write could be published to such a remote and the operator had to reauthor repeatedly while the Surveyor kept writing.

**Alternatives rejected.** A configured identity per perimeter: one more key for something the KB's identity already answers. Keeping the author and fixing the committer only: the rule checks the author too. Attributing by the existing `Surveyor-Run` trailer: it names a run, not the perimeter and replica the Atlas matches, and is absent from calls outside a run.

**Consequences.** The trailer is self-declared, like the author header was: any writer token can send the header, so it is a label, never authorization. A trailer that is not a well-formed `<perimeter>/<replica>` (one read from a pushed or pulled commit) is ignored by the readers. A Surveyor whose KB has no configured identity writes as its token id, as any client without the header. A commit already rewritten by an earlier `kb reauthor` stays unattributed; one not yet rewritten gets the trailer.
