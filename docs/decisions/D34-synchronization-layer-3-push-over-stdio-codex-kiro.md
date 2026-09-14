---
topic: sync-provisioning
---

# D34 — Synchronization: Layer 3 push over stdio + codex/kiro materialization

**Decision.** Two pieces of the sync model (D27) completed:
- **codex/kiro providers**: `skillDestDir` (`internal/provisioning`) now also maps `codex` → `.codex/skills/<nome>/` and `kiro` → `.kiro/skills/<nome>/` — they move out of `needs_approval` to direct materialization like claude/opencode. Everything else (manifest, lockfile, diff, prune) was already provider-generic.
- **Layer 3 (push)**: `Server.Notify(method, params)` emits a JSON-RPC notification on the shared stdio encoder (serialized by `writeMu`); the `notifyWrap` helper wraps `skill_install` (outside `gitWrap`, fires after the commit, only on success) and emits `notifications/skills/list_changed`. Capability `skills.listChanged: true` announced in `initialize`.

**Rationale.** On the hand-rolled HTTP transport (D16: request/response, no SSE) the server has no channel for unsolicited pushes → `Server.Notify` is a **no-op** when not inside `Run` (`enc == nil`), and Layer 3 applies only to **stdio**, degrading gracefully to Layers 1–2 over HTTP (consistent with the "additive" design of `sync.md`). The crucial choice is the **lock discipline**: `writeMu` protects each individual `Encode` but is never held during `dispatch` (where `Notify` is called, same goroutine) → no nested locks and no deadlock; verified with `go test -race`. The practical value over stdio is limited (the agent calling `skill_install` is also the consumer), but the `Notify` infrastructure is the hook-in point for future emitters (server-side git pull, bundle hot-reload) with no further changes to the loop.
