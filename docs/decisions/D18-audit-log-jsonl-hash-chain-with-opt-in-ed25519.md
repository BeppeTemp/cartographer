---
topic: transport-auth
---

# D18 — Audit log: JSONL hash-chain with opt-in Ed25519 signature (compliance-grade)

Each entry: `prev_hash` + `hash` = sha256 of `Timestamp|Tool|Args|AgentID|Outcome|PrevHash`. Genesis: `prev_hash = "genesis"`. `Verify` checks the whole chain. Args truncated to 1024 chars. Opt-in Ed25519 signature: if `CARTOGRAPHER_AUDIT_KEY` is set, each entry is signed (`sig` = hex of the signature of `hash`); `Verify` also verifies the signature if the key is available. Entries without `sig` (pre-signature logs) remain valid. `VerifyFull` distinguishes signed, unsigned, and invalid-signature entries.

**Wiring in `main.go`** (added): `CARTOGRAPHER_AUDIT_LOG` enables opening the log (`audit.Open`, or `audit.OpenWithKey` if `CARTOGRAPHER_AUDIT_KEY` is also set). The `*audit.Log` is passed to `serveHTTP`/`serveStdio`; wiring into the individual tool handlers is a future step.
