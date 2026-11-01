Closes #

<!--
Before opening: run `scripts/pr-triage.sh` on your branch
and fix every BLOCK line (CONTRIBUTING.md §Pull requests). A change over 500
production lines is split by work package, or this body starts a line with
`Size exception:` and why.
-->

## What

<!-- One bullet per behaviour that changes: before → after. A fix for a regression
also names the PR that introduced it, on a line of its own: `Regression-of: #<n>`.
A line that starts like a conventional commit (`feat(mcp): ...`) becomes a changelog
entry: write it as ONE unwrapped line, release-please keeps only its first line. -->

## Plan

<!-- Why this change is made, as a reader who saw nothing before this PR needs it.
A maintainer's plan: its work packages and every deviation, with no private names.
"No plan" otherwise. -->

## Invariants touched

<!-- Tick what the diff touches; for each, one line on how it is preserved. -->
- [ ] Scopes, locking or commits in an MCP handler (`internal/mcpserver/AGENTS.md`)
- [ ] Writes into a client's home directory (`internal/provisioning/AGENTS.md`)
- [ ] A public contract: MCP tool schema, CLI flag, config key, on-disk format
- [ ] None of the above

## Revert

<!-- What a `git revert` of this PR would not undo once a release has shipped it:
files written into users' home directories, an installed service or scheduled
job, a KB on-disk format, an MCP tool schema an agent relies on. "Plain revert"
when there is nothing; otherwise what a reverting release would have to migrate. -->

## Tests

<!-- Which tests pin the change, and whether they fail on the parent commit. -->

## Docs

<!-- The pages updated per docs/index.md §Documentation maintenance rules, and the
decision file if the change makes an architectural or contract choice; or, when no page
needs to change, a line `Docs: none - <reason>` (the triage prints a WARN for an area whose
page is untouched; this line, with its reason, answers it and reaches the reviewer). -->

## Self-review

<!-- The independent review you ran before opening (the `cartographer-contract-reviewer` subagent of
the cartographer-review-pr skill) and what you did with each
finding. `make gate` green: yes/no. -->
