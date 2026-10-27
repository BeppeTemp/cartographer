---
topic: control-plane
---

# D1191 — An agent with rw scope can add a secret to a KB

**Decision.** `secret_set`, `secret_resolve` and `service_get` are in the agent `tools/list`.
`secret_set` creates a missing `secrets/<name>.sops.yaml` when the KB's root `.sops.yaml` has a
creation rule matching the path and the server's age key is one of that rule's recipients; the
recipients are the rule's, sops applies them, the caller chooses none. Without a `.sops.yaml`, or
without a matching rule, or with a key that is not a recipient, it refuses. `kb_status` on
`capabilities.secrets: missing key` carries a `hint`: the recipient the KB's files name, whether a
key file of `credentials_dir` holds it, and, for a key file added after the start, "restart the
service to load it".

**Why.** An agent could not follow the secrets procedure of `cartographer-kb-create`: a client that
builds its tool list from `tools/list` cannot call what the list omits (the reason `concept_batch`
left the advanced set in D318). A KB whose operator already wrote the creation rule has made the
recipient decision; creating a second file under it is not bootstrapping recipients, and refusing
it only sent the agent to ask the operator for `git` in the KB clone. The status said only
`missing key`, though the fix was often a copy of a key already in the directory, or a restart,
since keys are read once at start. The three tools add about 1.4 KiB to the `tools/list` every
client pays, so the budget (D285) goes from 29 to 31 KiB.

**Alternatives rejected.**
- Expose only `secret_set`: `secret_resolve` is how the declared `secret_refs` are verified, and
  `service_get` how a Service is read; the two have the same scope rules.
- Let the caller pass recipients: the agent would choose who can decrypt, which is the operator's
  decision; the creation rule is where the operator wrote it.
- Reload the key file when it appears: a larger change to how a mount is built, for a case a
  restart settles; the hint says so instead.
- Name the matching key file in the hint: it is another KB's name in a KB-scoped transcript.

**Consequences.** `Create` in `internal/sops` is the only path that writes a new encrypted file; it
checks the rule, the key and the result (`ENC[` at the pointer) before the file exists, and the value
is on stdin only. A new file starts as mappings: a list is built by the operator. The write is
committed by the normal write flow. `/health` capabilities are computed at start, so only
`kb_status` shows the live hint. A future tool that writes under `secrets/` has to take its
recipients from the rule, not from its arguments.
