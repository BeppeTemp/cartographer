---
topic: control-plane
---

# D1110 — `instructions.md` is capped at 64 KiB where it is read, and `artifact_write` refuses more

**Decision.** The curated `instructions.md` is read through one bounded reader in `internal/provisioning`
(`KBCuratedInstructions`, at most `MaxInstructionsBytes` = 64 KiB plus one byte of look-ahead). Past the cap the
text is cut at the last line boundary within it and a marker line says the file was cut and how many bytes were
left out. The `initialize` guide and the managed block share that reader, so they never disagree on what a KB's
instructions are. `artifact_write` refuses an `instructions.md` body over the cap, naming the cap and the size.

**Why.** After D1097 every handshake read the whole file from disk and sent it to every generic client, so anyone
able to write the file could make each session read and carry an arbitrarily large text. The marker tells the
agent it is reading a partial text and the operator what to fix.

**Alternatives rejected.** Dropping the curated section past the cap: the agent loses rules it could still use.
A cap on the guide only: a file one path accepts and the other refuses. Refusing only in `artifact_write`: a push
into the KB's remote bypasses it, so the readers must bound the read anyway.

**Consequences.** A push can still bring in a larger file; it is served truncated. The raw listing of
`instructions.md` in the `sync_pull` artifact catalog is not part of the guide or the block and is unchanged.
