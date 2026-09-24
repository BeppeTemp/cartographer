---
topic: sync-provisioning
---

# D267 — Hook commands bound for a POSIX shell use forward slashes on Windows, and one that cannot run is drift

**Decision.** On Windows, the executable of a hook command that a POSIX shell
runs — every Claude Code hook in `settings.json`, and the `sh -c` of a
generated OpenCode plugin — is written with forward slashes, still quoted when
it contains a space; its arguments are left verbatim. A Claude Code hook whose
registered command cannot run (a backslash path on Windows, or an absolute path
to a file that is not there) is an on-disk finding, `unrunnable`, that `status`
and `doctor` report with the command and the reason and that `sync` heals by
re-registering the hook.

**Why.** Claude Code on Windows runs hook commands through Git Bash, where a
backslash is an escape: `C:\Users\user\.claude\hooks\cartographer-bootstrap\bootstrap.cmd`
arrived as `C:Usersuser.claudehookscartographer-bootstrapbootstrap.cmd`, so the
bootstrap hook — the session-start sync and the D254 update notice — never ran
on any Windows machine, while `status` reported in-sync (#412). Windows accepts
forward slashes in a path, and the shell leaves them alone. The existing
verification only looked at the hook's files, which were correct: the defect
lived entirely in the registration, so nothing on Cartographer's side could see
it.

**Alternatives rejected.**
- *Escape the backslashes (`C:\\Users\\…`)*: correct for bash, but the command
  is then wrong for any reader that is not a shell, and it doubles on every
  pass unless unescaped first — a second encoding to keep idempotent for no gain.
- *Convert the whole command line*: a backslash in an argument may be a shell
  escape the hook author meant; only the executable is Cartographer's.
- *Convert for Codex and Antigravity too*: neither documents running hooks
  through a POSIX shell, and a provider's behaviour is declared, not inferred
  (D50). They keep the host's spelling until one is audited.
- *A `runtime.GOOS` branch*: the conventions call for a build-tagged pair
  (`hookhost_unix.go`/`hookhost_windows.go`); the value then sits behind a
  package variable, as `execBitSupported` does, so the Windows spelling is
  pinned by tests on a unix host.
- *A new `status` section for hook health*: the D139 finding list already
  carries every "on disk but not what we wrote" state to `status`, `doctor` and
  `sync`'s heal pass; a new reason reuses all three.

**Consequences.** Ownership matching (`commandOwnedBy`) must keep reading a
backslash as a separator on Windows: it is what makes the next sync *replace*
a pre-D267 entry rather than append its slash rewrite beside it, and a test
pins that migration. The bootstrap hook is healed by `EnsureBootstrapHook`
re-registering it on every `sync`; a KB hook is healed through the manifest like
any other diverged artifact. The missing-file check applies only to an absolute
executable: a bare name is PATH's business and a `$VAR` path the shell's.
