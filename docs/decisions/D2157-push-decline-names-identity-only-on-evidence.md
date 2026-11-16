---
topic: concurrency-git
---

# D2157 — A declined push names identity only on the remote's wording or on bare-decline evidence

**Decision.** A declined push is reported as an identity refusal when the remote's own output names authorship
or identity, or, for a bare decline (no reason line besides the generic `pre-receive hook declined`), when the
unpushed commits carry the server's fallback identity and none is configured. The inferred case is worded as
inferred. A decline with a reason of its own (`rejected by policy: protected`) stays a protected branch and
quotes that reason. `sync_status` and the doctor's `Remedy` classify one output one way.

**Why.** D1063 inferred identity from a bare hook decline because GitLab's membership rule often prints nothing
else; but the inference also fired on a hook that said why it declined (#2151), sending the operator to the
wrong fix while the doctor said "protected or a server-side hook". The evidence for an inference is the
absence of any other reason, so a reason line removes it. The cost: a bare decline from a real protected branch
with fallback identities ahead is still called identity, which is why the message says it is inferred and names
that alternative.

**Alternatives rejected.** Dropping the inference: regresses #1075, where the generic line is all GitLab prints.
Keeping it unconditionally: contradicts the remote's own stated reason. Having the doctor recompute it: it sees
only the error text, not the local commits, so it echoes the inferred wording instead.

**Consequences.** `gitx.IsBareHookDecline` accepts only the exact generic lines; any other parenthetical is a
reason. `Remedy` recognises the inferred lead ("the remote gave no reason for declining"), so that sentence is
a contract between `ErrIdentityRejected` and the doctor. `kb init`'s `writePushRefusalHints` does not apply the
inference.
