---
topic: sync-provisioning
---

# D291 — An agent can carry each client's own restriction, verbatim

**Decision.** An agent's frontmatter may hold a `providers:` map keyed by client
(`opencode`, `codex`, `kiro`, `antigravity`). Each entry is copied verbatim into
that client's native agent file, after the fields Cartographer writes, in the
file's own syntax (YAML for OpenCode and Antigravity, TOML for Codex, JSON for
Kiro). Nothing is inferred. A client with an entry counts as having its own
restriction: the widened-agent warning (D283) is not printed for it and
`strict_tools` does not skip the agent there. The widened-agent warning now
points at `providers.<client>`. `artifact_write` rejects an unknown client, a
non-map entry, `claude` (which takes its fields at the top level) and any key
Cartographer writes itself; the same check fails the translation at sync.

**Why.** D283 made the widening visible and opt-out but left an author with only
two choices: accept the widened agent or lose it on that client. #447 asked for
a third: say what that client's restriction is. The author knows each client's
syntax; Cartographer does not, and D55/D195 are right that a guessed mapping
could grant a capability the author never gave.

**What is and is not verified.** Believed to restrict an agent, per the clients'
public documentation and not exercised by this repository against the real
clients: OpenCode `permission` (edit/bash/webfetch: allow, ask, deny) and `tools`
in the agent frontmatter; Codex `sandbox_mode` in the agent TOML; Kiro `tools`
and `allowedTools` in the agent JSON; Antigravity has no key known to us. D195
verified only Kiro's `name`/`description`/`prompt`. The mechanism makes no claim
beyond copying what the author wrote: a key a client ignores restricts nothing,
and that is the author's to check. The same statement sits next to the code
(`internal/provisioning/agentnative.go`) and in `docs/sync.md`.

**Alternatives rejected.**
- A built-in table mapping Claude's `tools` to each client's keys: still the D55
  rejection, and it would need the verification above to exist first.
- Whitelisting known native keys per client: it would reject a key a newer client
  version accepts, and turns an unverified belief into a gate. Only the keys
  Cartographer writes itself are reserved, because overriding them changes the
  agent's identity or prompt.
- A new `providers_strict` switch (as worded in #447): D283 already ships
  `strict_tools`; a second name for it would be two spellings of one choice. The
  `providers:` entry is what scopes it per client.
- Warning about a dropped `model`: kept out, as in D283. A different model is not
  a wider capability and would warn on nearly every agent.
- Parsing `providers:` with a general YAML reader in `internal/okf`: the OKF subset
  has no nested maps. The parser now keeps an indented block verbatim as an
  `okf.Block` instead of flattening it into sibling keys (which made a nested
  `tools:` look like the agent's own); `internal/provisioning` interprets it with
  `gopkg.in/yaml.v3`, already a dependency.

**Consequences.** Supersedes the D283 alternative "a per-client `providers:` block
left out" and the "no way to write a native restriction" sentence in D283,
D55, D58 and D195 for fields under `providers:`; their dropping of `tools` and
`model` as such is unchanged. Any concept frontmatter with an indented nested
block now parses as one `okf.Block` value instead of flattened keys. The lockfile
and the content hash are unchanged: the entry is part of the source, so editing it
re-syncs the agent. A future verification of a client's keys belongs next to its
cell in `destinationMatrix` and replaces the "believed" wording here.
