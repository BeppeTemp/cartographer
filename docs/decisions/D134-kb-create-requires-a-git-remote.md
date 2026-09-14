---
topic: deployment-release
---

# D134 — `kb create` requires a git remote

**Status: implemented (2026-08-26); amends [D85](D85-kb-create-and-first-kb-onboarding-a-cli-command-not.md).**

**Context.** A KB is a git repository, and its `origin` is what makes it durable
and reconstructible: the `kb-create` skill opens with a data-loss warning for
exactly that reason, and every sync path degrades silently without one
(`hasRemote` in `internal/kb/gitsync.go:27`, the `no_remote` git status, and
`CARTOGRAPHER_GIT_SYNC` documented as inert for a remote-less KB). The local
onboarding path did not enforce it: `cmdKBCreate` called `kb.Init` and nothing
else, and `docs/agent-install.md` presented the remote as optional, falling back
to a plain `kb create` whenever the user had not supplied one. Observed in the
field: a from-scratch installation completed successfully and left a local-only
KB, never prompting for a repository and never surfacing that the KB was neither
backed up nor syncable.

**Decision.** `cartographer kb create <name>` requires an explicit remote
decision: `--remote <url>` or `--no-remote`, with neither (or both) a usage
error (exit 2) that lists the three ways to get a KB — `--remote` for an empty
repository, `kb clone` for a repository that already holds a KB, `--no-remote`
for a local-only one. `--remote` attaches the URL as `origin` and pushes the
initial commit with upstream tracking (`gitx.PushSetUpstream`, added next to
`Push`, which must keep leaving tracking configuration alone for the sync
paths). A failure to attach or push removes the scaffold entirely and exits 1,
with hints separating an authentication failure from a non-empty remote — the
same cleanup guarantee `kb clone` already gives, so auto-discovery never mounts
a half-provisioned KB. `--no-remote` keeps the previous behavior and prints a
stderr warning naming the cost and the recovery commands. The agent runbook
(`docs/agent-install.md`) instructs the agent to ask for the repository before
creating anything and to fall back to `--no-remote` only on the user's explicit
acceptance.

**Rationale.** D85 correctly separated the local CLI path from the GitOps skill,
but made the remote optional to keep the path short, which made a non-durable KB
the outcome of the shortest sequence of commands a first-time user runs. The
product has no other moment where that state becomes visible: nothing prompts
later, and the sync machinery treats it as normal. Requiring the choice at the
only point where it is cheap to fix costs one flag and removes the silent
failure mode; keeping `--no-remote` preserves the throwaway/dev case without
making it the default.

**Compatibility.** Breaking for scripted `cartographer kb create <name>`
invocations: adding `--no-remote` restores the previous behavior exactly.
