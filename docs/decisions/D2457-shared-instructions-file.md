---
topic: sync-provisioning
---

# D2457 — one write per physical instructions file, with merged content

**Decision.** When the instructions destinations of two or more providers of one sync resolve to the same physical file through links the allow set lists (a followed `CLAUDE.md -> AGENTS.md`, or codex and opencode on `AGENTS.md`), the sync writes one block: each provider's body is computed as before, merged (KB sections of all without duplicates, each client's own text once, labelled by client when they differ), and every sharer writes that merged block. Each sharer records its own lock entries.

**Why.** Each provider rendered its own block into the shared file and overwrote the other's, so every sync reported writes and the file flapped between two contents (#2449). Merging keeps both clients' instructions in the one file they both read, which a single owner writing only its own block would lose.

**Alternatives rejected.** The first provider owns the file and the others skip: the other client's KBs and notes vanish from a file it reads. An unlisted link shared anyway: the allow set is the user's consent to write through a link, so an unlisted one keeps today's refusal. Stripping the block of a leaving sharer and rewriting it at once: the next sync already rewrites it from the remaining sharers.

**Consequences.** A new part of the instructions block joins `InstructionsParts` and its merge. The sharers are decided per run from the projections applied, so one disconnecting leaves the block with the others' content after the next sync, not before. The extra dry `Apply` of each sharer costs one more manifest pass only when a file is shared.
