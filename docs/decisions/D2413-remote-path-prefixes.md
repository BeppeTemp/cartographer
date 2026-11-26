---
topic: data-plane
amends: D907, D124
---

# D2413 — A KB declares the path prefixes of the remote hosts it documents

**Decision.** A KB's `settings.yaml` may carry `remote_path_prefixes`, a list of absolute paths; the `machine_path` check does not flag a path under one, in prose, a list or a table. The match is by path segment. Without the key nothing changes, and the D124 `ssh <host>` exemptions stay.

**Why.** A KB that documents a remote host names its canonical paths (`/home/user/services/app`) in tables and bullets, where there is no `ssh <host>` line to anchor the D124 exemption, and the only remedy D907 left was rewriting a path that is correct for its readers. The prefix is declared by the KB, so it travels with it and means the same on every server. The key is flat, like every other KB key.

**Alternatives rejected.** A per-map allow list: D907 removed it, a map-wide list hides the check on pages nobody inspected. A per-concept ignore: D907 removed it. A `lint:` section: every KB key is flat and the parser reads top-level keys only.

**Consequences.** This is a declared narrowing of the check, not a switch: a path outside every declared prefix is still a finding, and a prefix is checked (absolute, never the bare root, never a home root or above: `/home`, `/home/<user>`, `/Users`, `/Users/<user>`, `/root`, `C:\Users`, `C:\Users\<user>`, which would switch the check off for client-local paths). A client-local path under a declared prefix is not flagged, so the author owns what the prefix covers. D907's statement that nothing loosens `machine_path` now has this one exception; a future loosening of a check needs the same shape, a declared fact about the KB, never an off switch.
