---
topic: control-plane
---

# D2039 — secrets_init writes the creation rule, admin only

**Decision.** The MCP tool `secrets_init(recipient, [force])` writes the KB's root `.sops.yaml` with one creation rule (`secrets/.*\.sops\.yaml$`, one age recipient) and lets the normal write flow commit it. It is advanced-tier, in `secretTools`, and refuses any principal that is not an admin (or a local stdio caller). An identical rule is a no-op; a different one is refused unless `force`. This overrides "Cartographer never writes `.sops.yaml`" of D1191 for this one operator-initiated, admin-only tool. The key stays out of the tool: the operator's CLI installs it into `credentials_dir`, since the key file lives on the operator's machine and a tool that wrote into the server's credentials would hand them to any admin token.

**Why.** No write path accepts a root hidden file, and the server's clone takes no git from a client, so enabling secrets meant a separate clone and a manual commit. The recipients are still the operator's decision: that is why the tool takes them as an argument only from an admin, never from an agent token.

**The command.** `cartographer kb secrets init <kb> --recipient <age-pub> [--key-file <path>] [--force]` is local (D177): it calls `secrets_init` through the running server, then installs the key with `config.WriteCredential` (or `ReplaceCredential` under `--force`), mode 0600, never following a symlink. All no-write checks come first, so a refusal leaves nothing half-done; a failure after the rule is committed is fixed by running it again (idempotent).

**Alternatives rejected.** Letting `artifact_write` accept `.sops.yaml` (opens every hidden root file). A tool that also writes the key (credentials from an MCP call).

**Consequences.** A forced recipient change does not re-encrypt files already under `secrets/`; the result says to run `sops updatekeys`. D1191's rule that a tool writing under `secrets/` takes recipients from the rule holds: the file is not under `secrets/`.
