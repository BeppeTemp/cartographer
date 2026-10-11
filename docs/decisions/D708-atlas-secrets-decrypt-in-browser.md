---
topic: control-plane
---

# D708 — The Atlas opens sops secrets in the browser, never on the server

**Decision.** The Atlas has a read-only **Secrets** panel for the KB's
`secrets/*.sops.yaml` files. The server sends each file's structure (key names,
which sops leaves in the clear), the ciphertext of every value with the
additional data it was sealed under, the age stanzas of the data key and the
concepts that reference it (`GET /api/ui/v1/kbs/{kb}/secrets`). The reader pastes
an age identity into the page; the browser unwraps the data key with
`age-encryption` and opens each value with WebCrypto AES-256-GCM. The identity is
used once and dropped; only the opened values stay in component state until
*Lock*, until the panel closes or the KB changes. Nothing is stored, logged or
sent. The route and the panel exist only for a principal that can see the whole
KB, like the artifacts (D238).

**Why.** A human reading a KB needs to see what a secret holds without opening a
terminal, and the server must not become a place that holds or uses decryption
keys: the per-KB keys of D47 and the hermetic sops runs of D260 are for agents'
`secrets_resolve` flows, not for a browser session. Everything the route returns
is already in the KB's git history, so serving it discloses nothing a reader of
the repository does not have. The cost is a second implementation of the sops
value format (`ENC[AES256_GCM,…]` and its additional data) in TypeScript, which
must follow sops if it ever changes the format, and a new npm dependency.

**Alternatives rejected.**
- *Decrypt on the server with its configured key*: the Atlas would show secrets
  to anyone holding a read-only UI token, and the key would be used for a purpose
  beyond the agents' flows.
- *The reader posts the key to the server, which decrypts*: the key would cross
  the network and live in server memory and possibly logs; nothing gained over
  doing it in the page.
- *Run `sops` in WebAssembly*: a large bundle for a format small enough to read
  directly.
- *Verify the sops file MAC*: each value is already authenticated by its own GCM
  tag, and the MAC covers values the panel does not need to trust as a whole.

**Consequences.** The server never decrypts, never sees a key and never runs sops
for the Atlas; a change that would make it do so reverses this decision. Key
names and the shape of a secrets file are visible to whole-KB readers without a
key, as they are in git. A `secrets/` that is a symlink, or a file that is not a
regular `.sops.yaml`, is not served. A future sops format change shows up as
values that fail to open, not as wrong values.
