# Review evals — scores

One line per run (`cartographer-review-pr` §Changing the reviewer): date, reviewer definition
commit, model pin, caught/cases, noise/clean. Recall first, noise second.

| date | reviewer definition commit | model pin | caught/cases | noise/clean |
|---|---|---|---|---|
| 2026-10-17 | babfd90 (cartographer-contract-reviewer), cartographer-security-reviewer at main after #859 | claude-sonnet-5-5 | 0/5 (c1 and c4 found at low severity only; c2, c3, c5 missed) | 0/5 (k5 medium verified real: okf fence close rule) |
| 2026-11-01 | 2ae259a1 (both reviewers, mandate wording only) | claude-sonnet-5-5 | 0/6 (none named; c4 security found a target id traversal, c6 a stale derived read in sync, both real but not the recorded defects) | 0/5 (k3 two mediums verified real: VHS files still on the old port and tests the plan asked for) |
