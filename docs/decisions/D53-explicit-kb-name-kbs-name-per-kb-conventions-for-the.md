---
topic: deployment-release
---

# D53 — Explicit KB name (`kbs[].name`) + per-KB conventions for the git token (`git.token_dir`) and SOPS key (`sops.age_key_dir`)

**Context.** With multiple remote KBs in `kbs[]`, the KB name was always derived from remote/path (no
way to set it explicitly); git authentication on private HTTPS required the token
in the remote URL (ends up in cleartext in `.git/config`) or out-of-band handling.

**Decision.** `KBSpec.Name` (yaml `name`), if set, wins over derivation wherever the name is
used (endpoint, token scopes, clone dir). `GitConfig.TokenDir`: if `<token_dir>/<name>.token`
exists, it is used as the HTTPS credential injected via `credential.helper` in the per-process
environment — the token never touches argv/URL/`.git/config`. `SopsConfig.AgeKeyDir`: fallback
`KBSpec.SopsAgeKeyFile` > `<age_key_dir>/<name>.age` > global.
**Rationale.** Convention over configuration: with a fixed KB name, the token and age key are
found by convention instead of being listed field by field — a single secret/volume to
mount for all KBs, with an explicit override always available.
**Discarded alternatives.** Token in the remote URL (ends up in cleartext in logs/`.git/config`);
checking the remote's scheme in Go before injecting the helper (complexity for zero gain).
Details: `docs/deployment.md` §Environment variables.
