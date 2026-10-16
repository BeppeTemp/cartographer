Closes #

<!--
Before opening: run `.agents/skills/review-pr/scripts/triage.sh` on your branch
and fix every BLOCK line (CONTRIBUTING.md §Pull requests). A change over 150
production lines closes an issue labelled `plan` + `approved`; over 500, it is
split by work package, or this body starts a line with `Size exception:` and why.
-->

## What

<!-- One bullet per behaviour that changes: before → after. -->

## Plan

<!-- The plan's work packages this PR implements, and every deviation from the
plan with its reason. "No plan: trivial fix" when there is none. -->

## Invariants touched

<!-- Tick what the diff touches; for each, one line on how it is preserved. -->
- [ ] Scopes, locking or commits in an MCP handler (`internal/mcpserver/AGENTS.md`)
- [ ] Writes into a client's home directory (`internal/provisioning/AGENTS.md`)
- [ ] A public contract: MCP tool schema, CLI flag, config key, on-disk format
- [ ] None of the above

## Tests

<!-- Which tests pin the change, and whether they fail on the parent commit. -->

## Docs

<!-- The pages updated per docs/index.md §Documentation maintenance rules, and the
decision file if the change makes an architectural or contract choice. -->

## Self-review

<!-- The independent review you ran before opening (the `pr-reviewer` subagent of
the review-pr skill, or your client's equivalent) and what you did with each
finding. `make gate` green: yes/no. -->
