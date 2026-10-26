---
topic: client-configurator
---

# D1074 — `workspace bind codex --trust` records the folder as trusted, on request

**Decision.** `cartographer workspace bind codex <path> --kb … --trust` appends one table, `[projects."<path>"]` with `trust_level = "trusted"`, to the user's Codex `config.toml` (`provisioning.TrustCodexProject`), so a Codex workspace projection is not born `inactive`. It is the only write of a security setting of another tool that Cartographer makes. The flag is the whole consent: no prompt, valid for `codex` only, never implied by `--kb`, `sync`, a timer, a hook or a derived binding. The edit is textual (the old bytes stay an exact prefix), idempotent, and lands outside any managed block. It is refused, printing the exact line and exiting non-zero with the binding already saved, when the file or a directory above it is a link, when the file already mentions the path or declares `projects` in another form, or when the path cannot be a key.

**Why.** Without it the user opens a Codex session in every folder or hand-edits `config.toml` after each bind. Codex rewrites that file in canonical form and drops comments, markers included (D99), so a table inside a managed block would be lost or evicted (D126), and a table written twice is a duplicate key that stops Codex from starting: hence one table, written once, outside any block, never over an existing declaration. An explicit `untrusted` is the user's refusal and is never overridden. It costs a deliberate break of the "never parse or edit the user's TOML" rule (D58), kept as narrow as a line-prefix test and an append.

**Alternatives rejected.** A prompt: the command is scriptable and the flag is the consent. Trusting by default or on `--kb`: trust is a security decision of the user's. Writing it from `sync` or for derived bindings (D1019): nothing a user ran carries the consent. Parsing and re-serializing the TOML: it would rewrite hand-curated bytes. A marker-wrapped block: Codex strips markers. `unbind` removing the table: trust may predate the binding and is the user's record. Copilot: its project layers are gated on folder trust and unsupported (D676); a follow-up plan if wanted.

**Consequences.** `TrustCodexProject` has exactly one caller and the exception is stated in its header and in `internal/provisioning/AGENTS.md`; another key, caller or provider is a new decision. The refusal cases and the exact-prefix property are pinned by tests. A revert leaves the written table in place, as it is the user's record.
