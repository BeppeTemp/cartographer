---
topic: skills-services-secrets
---

# D2017 — status shows an agent with unparsable frontmatter as held, with the parse error

**Decision.** `status` lists an agent whose frontmatter is not valid YAML as held for
approval and gives the parse error as the reason (`held_reason` on the artifact in the
JSON, `omitempty`, derived on the client from the file `sync_pull` already carried).
The manifest issue D2009 adds for it is not printed as "not in the catalogue" for an
agent `status` shows as held; for an agent that is in a manifest but not shown held
(its content is approved, or it is installed and in sync) the issue stays on `status`
under `in the catalogue, with a problem:` (JSON `catalogue_warnings`, `omitempty`),
never under "not in the catalogue". This amends the last Consequences sentence of
D2009, which said `status` lists the issue under "not in the catalogue".

**Why.** The agent IS in the catalogue (D2009 rejected excluding it, which would prune
an installed copy), so "not in the catalogue" told the operator something false and hid
the reason it waits. Dropping the issue for every agent in a manifest would have
swallowed the warning of an approved agent whose frontmatter a client may still skip.
The reason is derived on the client with `contract.ValidateFrontmatterYAML`, the parser
the gate fails closed on, so nothing new travels on the wire and an older server is
enough.

**Alternatives rejected.** *A held-reason field in `sync_pull`:* a wire change for
text the client can compute from the file it holds. *Match the agent name inside any
issue text:* an unrelated issue that merely mentions the name would vanish.
*Rewrite D2009:* decisions are not edited; this file amends its Consequences.

**Consequences.** The client recognises the issue by `contract.AgentFrontmatterIssuePrefix`,
a text contract between a server and clients of other versions, pinned by
`TestAgentFrontmatterIssuePrefix_IsStable`. Across versions: an older client against a
newer server still prints the line as "not in the catalogue" (the text is unchanged,
only the wording of the old client is wrong); a newer client against an older server
that predates the D2009 check receives no such issue and shows nothing extra. Changing
the text needs a fallback that still recognises the previous one, or the newer clients
fall back to the false wording for the servers that send it. `sync` and `connect` keep
printing the issue as a `warning:` line.
