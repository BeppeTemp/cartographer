---
topic: architecture
---

# D219 — Windows has no execute bit, and a KB path is a slash path

**Decision.** Three rules, applied wherever the `test-windows` gate of D215 found
them broken.

1. `internal/execbit` is the only place that answers whether the filesystem
   carries a POSIX execute bit. The `Executable` flag keeps travelling through
   the protocol and keeps being stored in the KB; what it does not do on Windows
   is round-trip through the disk. A file read back there never reports the bit,
   a mode difference there is not drift, and the MCP stdio preflight stops asking
   a file's permissions for what `PATHEXT` decides.
2. Every path that is an *identifier* — a KB path, an artifact's destination, a
   `ManagedFile.Path`, a lint finding, a skill's `DirPath` — is slash-separated
   on every host, built with `path`, not `filepath`. `filepath` is for paths on
   this machine and nothing else.
3. A path is absolute if it is absolute on *either* platform, and an MCP stdio
   command is valid if it is valid on either. `filepath.IsAbs` answers for the
   host, and both of these are read by clients that are not the writer.

**Why.** D215 made Windows compile and put `make gate` on `windows-latest`; 65
tests then failed, in eight packages. Almost none of them were about the lock it
had ported. They were about POSIX assumptions that had never been named, and each
one was a defect on Windows rather than a test being fussy: a hook's ownership
marker (`.claude/hooks/notify/`) never matched the command Cartographer had just
written (`...\.claude\hooks\notify\notify.sh`), so registration stopped being
idempotent and prune stopped finding what to remove; `safeJoin` let `/etc/passwd`
through, because a leading slash is not absolute on Windows, and joined it onto
the KB root; the audit log's rollback could not truncate through an `O_APPEND`
handle and, with its error dropped, left exactly the malformed tail it exists to
prevent; a `C:\Users\RUNNER~1\...` path was rejected as containing "shell
metacharacters", because of the tilde in an 8.3 short name.

A second pass sharpened rule 2. An expanded concept's body path was built with
`filepath.Join` while its reader takes `path.Dir` of it: the two disagreed by one
directory level on Windows, and lint called every relative link inside an
expanded concept broken. `Validate` split a KB path on `filepath.Separator`, so
its strict-ontology check silently examined nothing there. `--repair-hashes`
read the execute bit off the disk while verification recomputes it through the
hook floor, so a repaired hash could never match and the finding could not be
cleared. None of these is a difference in spelling; each is a check that stops
working.

The execute bit is the one of the three that is a real loss and not a bug: NTFS
cannot represent it. Pretending otherwise — storing the intended mode and
reporting it back — would have made `WriteAsset` claim a bit that the next read
denies. Reporting it honestly makes a Windows client a place where a KB's scripts
lose their bit if it round-trips through disk, which is a limitation to state
rather than to hide.

**Alternatives rejected.**

- *Skip the failing tests on Windows.* The plan allows a skip whose reason is
  specific to what is asserted; it does not allow one whose reason is "Windows".
  Only two skips are left: a file that is readable but not executable cannot
  exist where there is no execute bit.
- *Emulate the execute bit out of band* (an attribute, a sidecar file). A second
  source of truth for a permission, to be kept in sync with the KB, with nothing
  on Windows that would consume it.
- *Normalise separators at the edges instead of at the source.* Every new
  producer of a path would have to remember; the bug reappears at the first one
  that does not.
- *Keep `ValidateMCPStdioCommand`'s original character set and let Windows
  descriptors fail.* The set was defence in depth against a shell Cartographer
  never starts, and it was rejecting legal file names.

**Consequences.** `internal/execbit` is a dependency of `kb`, `provisioning` and
their tests, and is the thing to reach for rather than a fresh `mode&0o111`. An
assertion about the bit is written `execbit.Supported && ...`. The client-state
lock of D172 has a real Windows implementation now instead of the `!unix` no-op,
so `internal/provisioning` has a platform triple to keep in surface-sync. What
still has no Windows answer is the native service and the distribution of a
Windows artifact, each an open plan issue of its own.
