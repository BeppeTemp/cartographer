---
topic: project-governance
---

# D207 — A claim about a client comes from the provider matrix, and a skill is parsed the way a client parses it

**Decision.** Two things, one subject: what this repository asserts about an agent
client has to come from something that was checked.

1. Every cell of `CONTRIBUTING.md` §Working with an agent client is derived from
   the audited provider matrix in `internal/provisioning/workspacescope.go` and
   `internal/configurator/registry.go` (D193), and
   `TestClientSkillSurfacesMatchTheProviderRegistry` fails when the two disagree.
   A client with no project-local scope says so and names its real, global
   location instead of leaving a blank the reader fills in.
2. A `SKILL.md`'s frontmatter is validated with `gopkg.in/yaml.v3` — the
   spec-compliant parser a client uses — not with a reader of our own. And
   `MaxRootChars` keeps its value of 12.000 but loses its attribution: it is this
   repository's own ceiling, not a vendor's.

**Why.** Both halves were found the same way, by asking what a client actually
does rather than what the documentation said.

**The skill nobody could load.** `implement-issue`'s `description` contained
`` `plan-issue` skill: `plan-issue` writes… `` — a colon followed by a space inside
an unquoted plain scalar, which is a YAML syntax error. Kiro listed thirty-one
skills for that workspace and not that one: no error, no log line, no degraded
mode. Three of this repository's own readers said the file was fine — the gate's
hand-rolled "split on the first colon" frontmatter reader, `okf.ParseFrontmatter`,
and `skill.Validate` — while `yaml.v3` rejected it outright. A gate that reads
frontmatter more permissively than its consumer cannot see the only defect that
loses a skill.

**The client column that was asserted.** The same commit that introduced the
per-client table also introduced `internal/provisioning/AGENTS.md`, which says: *a
provider's capabilities are declared, not inferred — do not make a destination up
for a provider that does not document one*. The table then claimed, for
Antigravity, `AGENTS.md` read natively, `.agents/skills/` read natively, a project
MCP config at `.agents/mcp_config.json`, and a `.agents/rules/` directory to stay
out of. The D193 audit recorded in this repository says the opposite in every
cell: Antigravity's configuration root is global — `~/.gemini/GEMINI.md`,
`~/.gemini/config/{skills,agents,hooks,mcp_config.json}` — with no project-local
cell of any kind. Two of those four paths do not exist anywhere. The claim had
also propagated into `.gitignore`'s comment, into `internal/repodocs`, and into
D203's rejected alternatives, so no single file could be read to notice it.

`MaxRootChars` is part of the same failure. 12.000 characters was justified as
"Antigravity documents a 12.000-character ceiling per rules file… the one that
binds". If Antigravity does not read a project rules file, nothing binds there.
The number is a good one and stays; the *reason* is what a future agent reasons
from, so the reason is what had to be corrected.

**Alternatives rejected.**

- *Validate skills with `okf.ParseFrontmatter` or `skill.Validate`.* Both accept
  the exact file a client rejects. OKF's reader is deliberately lenient for KB
  concepts, which is a defensible choice for concepts and the wrong instrument
  for asking "will a client load this". (That leniency is a product-level defect
  in its own right — a KB skill with this frontmatter is synchronized and then
  silently dropped by the client — and it is filed as an issue rather than fixed
  here: tightening it changes behaviour for every existing KB and needs its own
  plan.)
- *Quote every `description` by convention.* A convention is not a gate, and it
  brings its own trap: `plan-issue`'s description already contains double quotes,
  so "just quote it" is "quote it and get the escaping right".
- *Drop Antigravity from the supported clients.* Its own documentation, and third
  parties, describe it reading `AGENTS.md`; the honest state is "documented by the
  vendor, not audited by us", not "unsupported". The table now says exactly that,
  and the audited part — no project-local scope, so the two repository skills are
  unreachable from a clone — is stated as the limitation it is.
- *Fix the table and keep the 12.000 attribution.* The table was the visible
  symptom; the attribution is the load-bearing part, because it is what the next
  person raising or lowering the limit will read.
- *Compare the prose table to the matrix by eye, in review.* That is what happened
  the first time. The matrix is exported (`provisioning.ProjectDestination`), so
  the comparison is four lines of test.

**Consequences.** Adding or re-auditing a client is now two edits that fail
without each other: a row in `repodocs.ClientSurfaces` and a cell in the provider
matrix, with `CONTRIBUTING.md` checked against both. `SupportedClients` — the list
of clients needing a symlink bridge — is derived from that data rather than
maintained beside it, so the symlink gate covers exactly the clients that need one.
The Antigravity row now states that `plan-issue` and `implement-issue` are not
reachable from a clone for that client; that is a real limitation of the
one-copy-plus-bridges layout (D203), not a defect waiting to be fixed, and
inventing a path would hide it rather than remove it.
