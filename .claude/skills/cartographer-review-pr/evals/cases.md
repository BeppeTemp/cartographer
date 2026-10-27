# Review evals — cases

Past PRs the reviewers are scored against (`cartographer-review-pr` §Changing the reviewer).
`.claude/skills/cartographer-review-pr/scripts/eval-setup.sh` reads the two tables below and nothing else: one row per
case, cells separated by `|`, the first cell is the id. PR numbers and code only
(D259).

A case is *caught* when a high or medium finding names the defect's file and its
class. A fix PR's `Regression-of: #<pr>` line (or, for history, `git blame`)
names the introducing PR.

## Defects that escaped review

`defect` is `file:line` at the **introducing commit** (`git show <commit>:<file>`).
`plan` is the issue the introducing PR closed, `none` when it closed none.

| id | introducing PR | squash commit | fixing PR | plan | defect | expected finding | class | not in front of the reviewer |
|---|---|---|---|---|---|---|---|---|
| c1 | #556 | bc754234fdae74f7f1f9a9efe39b146700e401d2 | #846 | 524 | internal/lint/decay.go:72 | `reviewSuspended` truncates `Now()` to the day and treats `review_after` of today as suspended, while `stale_claim` already reports today as past: two readings of one date | edge case | `stale_claim` in `internal/lint/conformance.go`, the other reader of `review_after` |
| c2 | #631 | 1eb59d99e08201c05dd1b75efce18efa4a33069f | #848 | 608 | internal/mcpserver/tools_write.go:1720 | `applyConceptMoves` rebases the outbound links of the moved concept's `index.md` only; the satellites of an expanded concept keep links relative to the old directory | edge case | the satellites an expanded concept carries along, listed by `k.ConceptRelPath` and the directory walk |
| c3 | #631 | 1eb59d99e08201c05dd1b75efce18efa4a33069f | #852 | 608 | internal/lint/lint.go:1296 | `index_lists_retired` removes retired entries from the curated index, but `checkCuratedIndex` still asks for every candidate, retired ones included: add, drop, ask again | invariant | the caller `checkCuratedIndex` (`lint.go:720`), unchanged by the diff |
| c4 | #776 | c84662ee7de3225794ba3782d175dbfc2e29589b | #814 | 760 | internal/mcpserver/tools_transfer.go:150 | `authorizeTransferTarget` checks the destination slot of the top concept only; the satellites of an expanded concept move too and are never authorized, with their own type | security | `allowedMove` in `policy.go:455`, which has the same gap, and the move plan the transfer applies |
| c5 | #506 | 4136235b1743fe339ca0023f5b047f389c61dfe5 | #851 | none | internal/lint/lint.go:249 | a map's `lint_ignore` is applied by `applyMapIgnores` for `Run` only; `CheckConcept` (`conformance.go:268`), the write path's check, still returns the findings the map accepts | caller | `CheckConcept` and its single caller in `internal/mcpserver`, outside the diff |
| c6 | #1052 | b5eabfbf68f33b54c596973fad50d375ba0d4d66 | #1186 | 1019 | cmd/cartographer/workspace.go:128 | a derived projection, applied in a repository nobody bound, gets a tracked-file answer for its instructions only (`derivedInstructions`): its hygiene pass (`prepareWorkspace`, :688) still owns whole directories through `ProjectOwnedPaths`, shared with the repository's own skills (`.agents/skills/<own>/`), and the MCP and hook registration files, which the repository may track, so `sync` refuses (or edits a tracked file) in a repository it was never asked to touch | invariant | `ProjectOwnedPaths` and `CheckWorkspaceHygiene` (`internal/provisioning/workspacescope.go`), unchanged by the diff and written for an explicit binding, where the user chose the repository; the provider's tracked `.kiro/settings/mcp.json` and registration files |

`c4` is the only `security` case: it also scores `cartographer-security-reviewer`.

## Clean PRs

Merged 60 days or more before the baseline, no `Regression-of:` pointing at
them, no `fix:` commit touching their lines within 30 days (checked with
`git blame` on each later fix). Findings a reviewer makes on these that the
coordinator's verification drops are noise.

| id | PR | squash commit | plan |
|---|---|---|---|
| k1 | #110 | 330fd270dbf00ec409e297f8791705e58fda2dc1 | 107 |
| k2 | #92 | 30bf7d9eee0763b05dd927114cb009e4e672b972 | 91 |
| k3 | #85 | e80b4204be4ba46273216b148cfba49ef607b873 | 82 |
| k4 | #49 | 1b7f702b4578b37726512aaa19f542638713f8cf | 34 |
| k5 | #28 | e3f4a54d2a871d7e29e7f245c86a114014a6c64c | 19 |
