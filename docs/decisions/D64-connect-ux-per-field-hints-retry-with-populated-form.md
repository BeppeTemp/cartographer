---
topic: client-configurator
---

# D64 — Connect UX: per-field hints, retry with populated form, persistent default, pre-connect probe (WP8)

**Context.** Four frictions in the `connect` flow: the "Token env" field mistaken for the token
itself; on error the typed values were lost (CLI exited with exit 2); `doDisconnect`
deleted `.cartographer.yaml`, so the next connect restarted from localhost even on
machines pointed at a real server; no validation at submit (errors discovered only at a
"deferred" sync).

**Decision.**
1. **Per-field hints** (`fieldHint`): "Token env var" label with a contextual hint clarifying it is
   the env var's *name*, not the token; ignored if Auth is off.
2. **Error → form re-presented with the entered values**: `errMsg`/`forceRetry` in `connectFormModel`,
   inline error, connect stays idempotent (no `disconnect` needed to retry).
3. **Persistent default server**: `doDisconnect` clears only `agents` in `.cartographer.yaml`,
   preserving `server_url`/`trust`/`kbs` as prefill; new env `CARTOGRAPHER_SERVER_URL`.
4. **Pre-connect probe with force-override**: new `client.Ping(timeout)` (JSON-RPC `ping`, 5s) at
   submit, before writing files; on failure the form returns with the error, but a second consecutive
   submit with no changes skips the probe and proceeds (server temporarily down with a valid
   config) — CLI equivalent via `y/N` prompt.
**Discarded alternatives.** Skipping the Token env field from the tab order with Auth off (complicates the
focus cycle for little gain); `tools/list`/`initialize` as probe (`ping` is cheaper);
blocking probe without override (it would prevent saving a correct config with the server down).
Details: `docs/configurator.md` §`cartographer connect [provider|all]`.
**Follow-up (July 2026).** Point 3 preserved `server_url` in `.cartographer.yaml`, but only the
*interactive form* re-read it as prefill: in the non-interactive path (`--no-input`, non-TTY)
`cmdConnect` built `opts` from the flags' defaults, so a bare `connect <agent>` on a machine
already pointed at a remote server rewrote the shared config to `http://localhost:8080` / `auth:false`.
Now the **flag > config > default** precedence also applies there: `resolveConnectSettings` (a pure,
tested function) inherits `server_url`/`auth`/`token_env` from the existing `.cartographer.yaml` for each flag not
passed explicitly.
