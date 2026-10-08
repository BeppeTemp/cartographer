---
topic: transport-auth
---

# D341 — A token may name the git author of its writes

**Decision.** `auth.tokens[]` accepts an optional `author_name`/`author_email`
pair. A write made through that token is committed with it as the author; the
committer stays the KB's identity. A token without the pair commits as the KB.

**Why.** One KB is often written by a person and by unattended agents through
the same server. With a single per-KB identity every commit looked alike, and
the history could not answer "which agent wrote this". The principal already
reaches `gitWrap`, which makes the commit, so the identity rides on it at no
extra cost. Keeping the KB as committer preserves the server's signature on
every write. The cost: the identity is only as trustworthy as the token, and
it is attribution, not authentication.

**Alternatives rejected.**
- *Derive the author from the token `id`*: an `id` is a log key, not an email,
  and git hosts link commits to accounts by email.
- *One KB entry per agent with its own `author_*`*: the same repository would
  be cloned twice and the two clones would race on push.
- *A `Co-authored-by` trailer only*: hosts show the KB as the author, which
  is the problem this solves.

**Consequences.** `author_name` and `author_email` are validated together at
startup, like the per-KB identity. The flat `CARTOGRAPHER_TOKENS` env form has
no room for an identity, so a token that needs one is declared in YAML. Writes
that do not go through `gitWrap` (KB init, import, the reserved
`.gitattributes` commit) keep the KB's identity.
