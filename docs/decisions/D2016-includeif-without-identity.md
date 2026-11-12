---
topic: concurrency-git
---

# D2016 — An includeIf counts as a per-folder identity only when an included file may set one

**Status: implemented.** Closes #1910.
> **Amends [D904](D904-the-writer-is-the-author.md)**: its rejected alternative "reading the global git identity even
> when it has an `includeIf`" and the "no identity when the config holds an `includeIf`" rule are narrowed here;
> D904's text is unchanged.

**Decision.** The global git identity written once into a shared client entry is withheld, with the per-folder
warning, only when the global config has a conditional include (`includeIf`) whose file may set an identity: a
`user`, `author` or `committer` `name` or `email`. An `includeIf` whose files set none of those (a per-organisation
`core.sshCommand`, say) changes no identity and is ignored, so the global identity is sent as for a config with no
`includeIf`. Conditions are not evaluated. Every doubt counts as an identity and keeps the per-folder rule: a file
that is missing, unreadable or not a regular file, an `includeIf` or a key that is not a path inside the included
file, a path that cannot be resolved (a rooted path with no drive on Windows), a non-file origin, a git command that
fails or times out. Each git command has its own time budget, so a slow host never turns a harmless setup into the
warning.

**Why.** D904 refused every `includeIf`, because a folder whose `includeIf` names a work identity would silently
write as the personal one. That is right for an identity and wrong for the common setup that only switches the SSH
key by organisation: git resolves the same author in every folder there, yet `sync` warned that the identity is
picked by folder, and an entry without a workspace bridge got no author. The reasoning of D904 holds unchanged; only
its trigger was wider than its cause. The price is that the global config is now read file by file, which is more
code to keep honest than one `includeif` key test.

**Alternatives rejected.**
- Keep D904's unconditional rule: warns on setups where no folder can differ, and sends no author to the entries that
  most need one.
- Evaluate the conditions (`gitdir:`, `onbranch:`, `hasconfig:`): they depend on the repository a folder is in, which
  a client entry shared by every folder does not have; the file that decides may be included in some folder.
- Treat an unreadable or missing included file as harmless: git skips it silently, but it may be mounted, restored or
  fixed later, and then the folder gets another identity. Failing closed costs one warning.

**Consequences.** What D904 says about the stdio bridge, `kb repair` and the per-KB `identity` is unchanged: they
ask git in the folder, inside a work tree. Any new way the included files are read must keep the rule that what
cannot be shown to be harmless counts as an identity. The behaviour is described in [sync](../sync.md).
