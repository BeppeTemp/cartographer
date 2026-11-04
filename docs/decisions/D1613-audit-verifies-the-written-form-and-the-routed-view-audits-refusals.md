---
topic: transport-auth
---

# D1613 — audit verification compares the bytes on disk, and the routed view audits the refusals it decides

**Decision.** `audit verify` and `audit export` accept a line only if it is exactly
the line the writer produces for the entry it decodes to (UTC timestamp, bare `\n`
ending, fixed field order), in the layout of the release that wrote it; a legacy
(version 0) line may carry no field of the later eras. A version-2 event with a
non-empty `args` or `agent_id` is malformed. The routed view records, as one denied
attempt+completion pair, each call it refuses before any KB server sees it; a call
it dispatches is audited once, by the target KB.

**Why.** The hash covers a fixed subset of the fields and the decoder matches keys
case-insensitively, so hashing parsed values authenticated bytes the hash never
saw. Comparing the line itself closes unhashed fields, key variants and re-spellings
with one rule. The routed mount is the topology clients use, and its refusals left no
trace although every `tools/call` is documented as audited.

**Alternatives rejected.** A strict decoder (case-sensitive, no unknown fields): leaves
other spellings of the same values. Resolving `kb` first and auditing on the target
server: some refusals happen before any target exists. Auditing the whole routed call
on the view: a dispatched call would be recorded twice.

**Consequences.** A line re-encoded by another tool now fails verification even with an
intact hash. A legacy line's `duration_ms` and `commit_sha` are outside its hash by
construction and remain unauthenticated. The audit log gains denied pairs for routed
refusals (outcome `unauthorized`, `kb` empty unless the call resolved to a KB in the
set). A change to the entry layout must keep older layouts accepted in `canonicalLine`.
