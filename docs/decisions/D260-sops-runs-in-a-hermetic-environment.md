---
topic: skills-services-secrets
---

# D260 — sops runs in a hermetic environment: allowlisted variables, an empty home, the per-KB key file

**Decision.** Every `sops` child (`Decrypt`, `Set` in `internal/sops`) gets an
environment built from scratch: `PATH`, `TMPDIR`, `TMP`, `TEMP`, `SYSTEMROOT`,
`LANG`, `LC_ALL` copied from the server when set; `HOME` and `XDG_CONFIG_HOME`
(on Windows also `USERPROFILE` and `APPDATA`) pointing at a fresh empty
temporary directory removed after the call; then the caller's entries, which
carry the per-KB `SOPS_AGE_KEY_FILE` and win. This applies unconditionally, also
when the caller passes no env. `Decrypt` parses `sops` stdout only.

**Why.** `sops` does not use only the key file it is given: it collects every
identity it can find — `SOPS_AGE_KEY`, `SOPS_AGE_KEY_CMD`,
`SOPS_AGE_SSH_PRIVATE_KEY_FILE`, the default `sops/age/keys.txt` under the user
config dir, default SSH keys under `$HOME/.ssh`, cloud KMS / Vault credentials.
With `cmd.Env = append(os.Environ(), env...)` any KB on a multi-KB server, or on
a workstation whose shell profile exports `SOPS_AGE_KEY`, was decryptable with
the ambient key whatever `sops_age_key_file` said. D47's claim that passing the
key via `env` "prevents one KB's age key from contaminating requests on another"
was therefore partial: it held for the key Cartographer passes, not for the
ambient ones. Separately, `CombinedOutput` fed any stderr warning of a
successful decrypt into the YAML parse. The cost: an operator who relied on an
ambient key *instead of* a matching `sops_age_key_file` now gets a decrypt
failure until the key file is configured.

**Alternatives rejected.**
- *Denylist `SOPS_*` and cloud variables* — misses the next identity source sops
  adds, and does nothing about default files under the real home.
- *Keep the real `HOME`* — the default `keys.txt` and `~/.ssh` keys stay
  reachable. Decrypting an existing file and `sops set` on one never need it:
  creation rules are read from the KB root, which stays `cmd.Dir`.
- *Fall back to the real home when the temp dir cannot be created* — silently
  reopens every ambient key; the call fails instead.
- *Keep cloud KMS / Vault variables* — they are not a supported key source (the
  config knows only `sops_age_key_file`, and resolution refuses without it), so
  dropping them breaks no supported setup.

**Consequences.** The configured age key file is the only identity sops can
use; supporting another key source (KMS, Vault) means adding it to config *and*
to the child environment explicitly, never re-inheriting the server's. Test
fakes of `sops` cannot receive knobs through the test process environment —
bake them into the script. `secret_set` still never surfaces sops diagnostics;
`Decrypt` errors keep `sops decrypt <path>: <err>: <stderr>`.
