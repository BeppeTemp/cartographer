---
topic: control-plane
---

# D1101 — Operator-directed text follows the connection kind

**Decision.** A message that names a `cartographer` command, a bundled skill as the
only way, or the operator's tools is chosen by connection kind: a connection that
declared `?provisioned=1` (D1097) and the Atlas get today's text byte for byte; every
other connection gets the same facts in words. The doctor nudge (amends D299, and the
D358/D704 trigger, "every non-CLI session with write access") reaches only the former.
Static texts every connection sees (tool descriptions, lint suggested actions, the
conflict guidance) are replaced for everyone: `kb_guide` (D1100) is the route, the skill
is cited only as "where the client has it". A test asserts that a generic connection
never receives a `cartographer <subcommand>` string.

**Why.** A host that gives the model only tools has no skill, no CLI, and an end user who
is not the KB's operator; a message that sends the agent there is a dead end. The server
cannot tell the audiences apart except by the marker, which selects wording, never access.

**Alternatives rejected.** A field on the cached routed view: it is shared by every URL
resolving to the same set, and the texts are read in the target KB's server. Branching the
static texts too: `kb_guide` works for a provisioned client as well, so a second wording
would only drift. Rewriting `GitStatus.LastError`: it is compared by prefix, so the
neutral identity-rejection text sits beside it.

**Consequences.** The nudge window is claimed only by a connection that may receive it (the
server is stateless over HTTP: one shared bucket). A new message with a CLI command fails
`TestGenericConnectionIsNeverToldToRunACLICommand`, or is written in two renderings. A
client not yet re-synced after D1097 is generic until its next sync. `?access=read` from a
generic client still reads the bridge's sentence (accepted).
