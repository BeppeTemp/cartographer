---
topic: concurrency-git
---

# D904 — The writer is the author of a commit, the server its committer, the token a trailer

**Decision.** The author of every commit a write makes is whoever wrote it. The client names the writer in the
`Cartographer-Author: Name <email>` HTTP header of the request; without a valid one the author is
`<token id> <cartographer@<host>>`. The committer of every commit is the server, `Cartographer
<cartographer@<host>>` (`<host>` the machine name reduced to what an email domain accepts). Every write ends its
trailer paragraph with `Cartographer-Token: <token id>`, appended last by the server, which drops any such line
the caller put in the message. A commit that is not a write (the `external changes` commit, a sync merge or
rebase, conflict resolution, the first commit `serve --init` makes) is authored by the server too; `kb create`
authors a new KB's first commit as the operator running it.

A valid header is one `Name <email>` of at most 256 bytes, valid UTF-8, with no control character, a non-empty
name and a plain `local@domain` email. A value outside plain ASCII travels in MCP's encoded form for header values
(SEP-2243, `=?base64?…?=`), which the server decodes first. Cartographer's own senders encode every value that
holds anything but letters, digits, inner spaces and `. - _ @ < > + ' ( ) ,`. An invalid header is treated as
absent and the write's response gains one warning line that says why and whom the write was authored as.

The writer's identity is a client setting: `identities` in `.cartographer.yaml`, by the client's KB name or
`default`. A present entry always decides, and an empty one sends no author. Without one:
- the stdio bridge asks git in the session's folder, inside a work tree only, so an `includeIf gitdir:` identity
  applies to KB writes as it does to commits;
- `cartographer kb repair` does the same from its own folder;
- an HTTP entry that `connect` and `sync` write once into a file every folder shares carries a static header with
  the global git identity. It is written only when every KB the entry reaches agrees on one, and the global
  config is not read when it holds an `includeIf`. Either case sends none and warns.

The header goes on `tools/call` only, and it decides nothing about access. The server's settings for an author or
committer are removed: global `git.author_*`/`committer_*`, `kbs[].author_*`/`committer_*`, a token's
`author_name`/`author_email`, and `author_*`/`committer_*` in a KB's `settings.yaml`. Neither the machine's git
config nor `GIT_AUTHOR_*`/`GIT_COMMITTER_*` in the server's environment changes a commit any more.

Supersedes D341 (a token naming the author). Amends D46, whose per-KB author and committer are gone while its SSH
and credential half stays, and D265, whose committer fallback no KB commit reaches any more, since the server
always sets one.

**Why.** History should say who wrote a change and which instance recorded it. A per-server or per-KB identity
(D46) made every commit look alike, which on Kubernetes, where the server is a pod, says nothing. A token's
author pair (D341) named a token, not a person, and needed a server-side entry per writer. The person is known on
the client, where git already knows them, and a header reaches the server on every transport that has one
without changing a tool's schema. It cannot be set by the model, because the client configuration sets it, not
the tool arguments.

The cost: the author is a claim, not a proof. Anyone holding a token can write under any name. The trailer is the
part the server vouches for, so `concept_history` reads the token from it, never from the author line. A forge
with an author push rule refuses the fallback author, which is not an account on it. The committer needs no
configuration and no account.

**Alternatives rejected.**
- Keeping the token's author pair (D341): it names a token, needs the server's config to change for each person,
  and cannot follow a per-folder identity.
- An `author` argument on the write tools, or a `_meta` field: every write schema changes, and the model, which
  writes the arguments, could pick the author.
- The writer as committer as well: the committer records which instance made the commit, and needs no account.
- Signing commits per writer: it needs a key per writer and per client. It is out of scope; the trailer gives
  what the server can vouch for.
- Refusing a write whose header is invalid: an author is attribution, not authorization, so a bad display string
  would break an agent's write for nothing. The warning makes it visible.
- Raw UTF-8 or RFC 8187 header values: header bytes beyond ASCII are not portable, while MCP already has an
  encoded form (`Mcp-Name`).
- Encoding only non-ASCII values: client configs expand `${VAR}`, `$VAR`, `$(cmd)` and `{env:VAR}` in a header
  value, so a plain `$` in a name would be read as a template. Hence the allowlist.
- Writing an HTTP entry's author from the folder `connect` runs in: the entry is shared by every folder, so one
  folder's identity would author the others' writes.
- Reading the global git identity even when it has an `includeIf` (the issue's plain rule): a folder whose
  `includeIf` names a work identity would silently write as the personal one. Sending none, with a warning that
  names `identities.default`, fails closed.

**Consequences.** Breaking: commit authors change from the configured server, KB or token identity to the writer,
and the committer becomes `Cartographer <cartographer@<host>>`. The removed keys load with a warning and are
dropped by the 0.25 realignment: they are entries in `config.Retired` (the server's, `kbs[]` and token keys) and
`kb.SettingsRetired`. A client that sends no header (CI, a timer, a client config that cannot carry one, such as Kiro's
today) writes as its token.

A future change here must preserve all of these:
- the header never authorizes anything;
- the token trailer is appended last by the server, and a forged one is dropped however it is spaced;
- the header goes on `tools/call` only;
- Cartographer's senders encode by the allowlist;
- an HTTP entry names an author only when every KB it reaches agrees on it.
