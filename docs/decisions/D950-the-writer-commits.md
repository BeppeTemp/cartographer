---
topic: concurrency-git
---

# D950 — The writer commits: author and committer are the same identity, and the server file names it per KB

**Decision.** A commit Cartographer makes for a known writer has that writer as author **and** committer. The
instance that recorded a write moves to a `Cartographer-Host: <host>` trailer, appended before
`Cartographer-Token`. The server file gets two keys, `git.identity` and `git.identities` (a map by KB name, with
`default`), and the identity they give a KB is its default: the author and committer of every commit with no writer
of its own (the unattended repair, the external-changes commit, a sync merge, conflict resolution) and of a write
that sends no valid `Cartographer-Author`. The server identity `Cartographer <cartographer@<host>>` stays the last
fallback, for a KB with no identity. A sync that rebases local commits gives each one its author back as committer,
and a commit authored by exactly the server identity (made before the identity was configured) is re-authored and
re-committed as the KB's configured identity. `git.author_name`/`git.author_email`, the 0.24 keys, are moved to
`git.identity` by the server's realignment.

**Why.** D904 made the committer the server, on the premise that it "needs no account on the forge". A forge with a
membership push rule (GitLab `member_check`, "the commit author must be a member") checks the **committer** as well as
the author: measured on eight KBs whose remote is such a GitLab, every push of a 0.25.0 commit was refused, first for
the author, then — after the author was fixed by hand — for the committer. The author could be a real person (the
header), but the committer never could, and neither could the author of what has no client behind it (the repair, the
merges) or of a client that cannot send the header (Kiro's MCP config has no headers). So 0.25.0 could not publish to
such a remote at all. The identity is the **server's** setting, per KB, because it is the machine's: a KB's
`settings.yaml` is committed and shared by every person who writes to the KB, and would put one person's identity on
everyone's commits.

The cost: an author is a claim, not a proof, and now the committer is that claim too, and a write without a header is
attributed to the configured identity whichever token made it. What the server vouches for is unchanged: the
`Cartographer-Token` trailer (unforgeable, last) names the token that wrote, and `Cartographer-Host` the instance.
`concept_history` reads both from trailers, never from the author line.

**Alternatives rejected.**
- The client names the identity per KB and registers it with the server (`identities` in `.cartographer.yaml`, posted
  by `sync` to a `/api/writer` route): it follows the person, but the server then keeps state any whole-KB writer can
  overwrite for the others, a new route to protect, and a registration that is lost when the client does not sync; and
  the maintainer's rule is that a KB's identity is a server setting. The client's `identities` still decides the
  header it sends, and the header still wins.
- The identity in the KB's `settings.yaml` (D906): committed, so shared by all writers of the KB.
- Keep the server as committer and ask the forge to relax the rule: the rule is a group policy that belongs to the
  forge's owner, and "turn off the check" is not an answer a product can give.
- The committer taken from the process or git config of the machine: it is what 0.24 did and D904 removed it, because
  it made history depend on whichever git config the service happened to run under.
- `kbs[].identity`: `kbs[]` has no entry for a KB discovered under `data`, which is most of them, and an entry with no
  remote is ignored by design; a map by name reaches every KB.

**Consequences.** Breaking for anyone who matched on the committer being `Cartographer <cartographer@host>`: it is
the writer now; the host is in the trailer. A KB with no configured identity behaves as 0.25.0 until the operator sets
one. A server shared by several people gives every header-less write of a KB the same configured identity: the token
trailer is what tells them apart. A forge rule that checks the committer's *identity* ("reject unverified users")
no longer stops a header that claims someone else, since the committer is now that claim too: the signed audit log,
which ties every write to its token and commit SHA, is the attribution to trust, the author and committer lines are not.
The realign rewrites only a linear range above the upstream, never a published
commit, and returns `ErrNotLinear`, logged, for a range with a merge or a root commit; it never re-authors a commit
authored `<token id> <cartographer@host>`, which names the token that wrote it.

A future change here must preserve all of these:
- the committer of a commit with a known writer is that writer, whatever the server's environment says;
- the configured identity is attribution, never authorization;
- `Cartographer-Token` stays the last trailer and `Cartographer-Host` the one before it; a caller's copy of either is
  dropped;
- the realign never touches a commit that is already on the upstream, and never takes over a token's authorship.

Amends D904 (the committer is no longer always the server), D265 (the committer fallback now has a writer to fall
back to), D46 and D906 (a server-side identity returns, as one key and one map).
