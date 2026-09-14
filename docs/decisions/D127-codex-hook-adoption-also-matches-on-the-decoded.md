---
topic: client-configurator
---

# D127 — Codex hook adoption also matches on the decoded command value

**Status: implemented (2026-08-04).**

**Decision.** `codexHookTableOwner` (`internal/provisioning/hooksettings.go`) accepts a
marker-less `[[hooks.<event>]]` registration as hookName's own by **either** identity: D99's
original path-fragment marker (`codexHookOwnershipMarker`), **or** a `command` that decodes to
exactly the command `registerHookConfigTOML` is about to write for that hook. The decode is
`configurator.CodexTableStringValue(body, "command")`, a new exported helper next to
`codextoml.go`'s existing line/key parsing that reads the value of a `key = <string>` assignment
regardless of which of the four TOML string forms it is spelled in — basic `"…"`, literal `'…'`,
multi-line literal `'''…'''`, multi-line basic `"""…"""` — decoding each one's own escaping rules
(or none, for literal forms) rather than comparing raw TOML text. The comparison is byte-exact on
the decoded value, no trimming or normalization.

**Rationale.** D99's path-fragment identity only holds for a hook whose command invokes a script
inside its own materialized directory (`resolveHookCommand` only ever prefixes such a command with
the hook's directory). A hook whose `hook.json` command is a self-contained one-liner — the `jq`
one-liners shipped as `env-block`/`sops-warn` in a real KB — is passed through verbatim and
contains no path fragment at all, so it never matched. After a Codex rewrite dropped the block's
comment markers, `AdoptCodexOrphanTables` could not recognize that class of hook's orphaned
registration; `blocktext.Write` appended the block again, registering — and firing — the hook
twice, the exact failure D99 set out to prevent. The command Cartographer writes for a hook is
otherwise deterministic (`resolveHookCommand` computed once per registration), so its decoded value
is a sound second identity; a new emitted key (e.g. `cartographer_hook = "<name>"`) was rejected —
Codex's config schema is not ours to extend, and an unknown key risks a hard parse failure on the
user's only Codex config file. The decode is necessary, not optional: Codex re-serializes a command
Cartographer wrote as a basic string into a multi-line literal string, and the two spellings share
no useful substring to match on directly.

**Consequences.** `env-block`/`sops-warn`-style hooks stop being duplicated (and firing twice)
after a Codex `config.toml` rewrite. The legacy path-fragment identity is kept, so a registration
written by an older client version is still adopted. A user-authored hook on the same event with a
genuinely different command matches neither identity and is left untouched, as before. **Residual
limit, accepted rather than engineered around:** a hook whose command changes in the narrow window
between a Codex rewrite and the next `connect`/`sync` matches neither identity — the orphan and the
freshly-written registration disagree — and survives as a duplicate; this requires two faults
inside the same window, and the legacy path-fragment match still covers the script-based hooks
where a command change is the most likely of the two. `internal/provisioning/hooksettings.go` (D57,
Claude Code's `settings.json`) has the same shape and the same blind spot, but no duplication was
reproducible there — its JSON path never goes through orphan adoption — so it is left unchanged
pending an actual reproduction.
