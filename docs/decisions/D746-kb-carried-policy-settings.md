---
topic: data-plane
---

# D746 — A KB carries its own policy in `settings.yaml`, and it wins over the server

> **Amended by [D906](D906-settings-cleanup.md)**: the KB's `settings.yaml` is the only home of a KB's policy (the `kbs[]` twins and the "ignored server value" report are gone), `mcp_allowlist` and `allow_artifact_write` are removed, and `secrets` is derived from the sops files under `secrets/`.

**Decision.** The per-KB keys that describe how a KB behaves (the schema's `kb` scope: artifact and
template writes, write gate, auto-repair, doctor cadence, MCP allow-list, how changes land, and the
new `secrets` declaration) can be set in `settings.yaml` at the KB root, next to `paths.yaml`. The
KB is authoritative: effective value = the KB file if it sets the key, else the server's
`kbs[]`/env/flag value, else the default. A differing server value is ignored and reported, with no
local override. A KB without the file behaves exactly as before. No MCP tool writes the file.

**Why.** A KB is shared. Five people mounting it must get the same behaviour, and with the policy in
each server's `kbs[]` a mount could have artifacts enabled while another did not, and a discovered KB
could carry none at all. The file travels with the KB through git, reviewed like a Map contract.
Moving `allow_artifact_write` and `mcp_allowlist` into the KB does not weaken anything: anyone who
can push to the KB can already commit artifacts and `mcp/*.json` with git, so the server flag was
never the trust boundary; signature verification and the hash-bound local approval of MCP
descriptors (D115) stay per machine. The cost: an operator can no longer tighten a KB's policy from
the server side, and a bad commit changes every mount at once. `github_api_url` is the exception
among the forge keys and stays server-only: the server sends its own GitHub token to that URL, so a
KB able to set it would let anyone who can push to the KB send the token to a host of theirs.

**Alternatives rejected.**
- The server wins and the KB only suggests: the failure this removes, a shared KB behaving
  differently per mount.
- A local override on top of the KB: it brings the divergence back through the side door and needs
  a second precedence story. An operator who disagrees changes the KB or does not mount it.
- A hidden `.cartographer/` file: `paths.yaml` already is the KB-root settings file, with the same
  tolerant parsing and lint reporting; a new directory would need explaining.
- Moving machine-bound keys too (`ssh_key`, `author_*`, `sops_age_key_file`, tokens): each server
  needs its own, and `artifact_signing_seed` is a private key. `secrets: true|false` lets the KB
  declare the requirement while the key stays per machine.

**Consequences.** `internal/kb` cannot import `config` (config -> lint -> kb), so `kb` holds a table
of the accepted keys and their value checks, and `TestKBSettingsKeysMatchSchema` pins it to the
schema in both directions; checks that need `lint` or `provisioning` (auto_repair names,
`mcp_allowlist` entries) run in `config.ResolveKBSettings` and in the lint check. Policy fields of
`kb.KB` are read without a lock, so the refresh (`KB.RefreshSettings`, after a pull that moved HEAD
and before a write) assigns them in one place and runs under the git lock; `mcp_allowlist` and the
git workflow keys are read at mount and apply at the next start. The settings view gains origin
`kb`. A future per-KB key goes in the schema first and, if it describes the KB and not the machine,
gets scope `kb`, which makes it settable here.
