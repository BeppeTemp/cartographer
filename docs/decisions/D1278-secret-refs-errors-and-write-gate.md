---
topic: control-plane
---

# D1278 — Secret references: names restrict, errors carry no server path, a broken reference is refused at write

**Status: implemented.**

**Decision.** `secret_resolve`'s `names` restricts the resolution to the named references, and a name the concept does not declare is an error (`unknown name`), not an empty result. Errors of the secrets tools carry no server path and no raw sops output: they name the step and the KB-relative file (`age key missing or invalid for <kb>`, `reference X: secrets/missing.sops.yaml not found`, `sops set failed on <file>`), and the full detail goes to the server log. The check `secret_ref_broken` reports a `secret_refs` entry whose file is not in the KB or, reading only the clear key names of the sops file, whose pointer names no single value; it is avoidable at write (D907), so `concept_write` refuses it.

**Why.** A broken reference nobody asked for made every resolution fail, and an unknown name returned someone else's error. The caller of these tools is a remote agent: absolute paths, the identity search list of sops and the temporary home are the server's, and they landed in transcripts. The error surfaced only at resolution, long after the write that introduced it.

**Alternatives rejected.** An empty result for an undeclared name: it reads as "resolved nothing" and hides a typo. Redacting paths by string replacement in the handler: it misses the next path an error learns to carry; the sops package builds the public text itself instead. A lint finding only, not refused at write: the declaration would still be committed broken.

**Consequences.** Any new error on the secrets path is built with `sops.newError` (public text, private detail) and logged with `sops.Detail`; a raw `os` error must not reach a tool result. The finding message names the reference, the file and the pointer, and must keep naming no server path, since the write gate keys findings on it. A file sops cannot be judged on (not YAML, no sops block, over 1 MiB) is not reported.
