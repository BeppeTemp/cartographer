# Review evals — scores

One line per run (`cartographer-review-pr` §Changing the reviewer): date, reviewer definition
commit, model pin, caught/cases, noise/clean. Recall first, noise second.

| date | reviewer definition commit | model pin | caught/cases | noise/clean |
|---|---|---|---|---|
| 2026-10-17 | babfd90 (cartographer-contract-reviewer), cartographer-security-reviewer at main after #859 | claude-sonnet-5-5 | 0/5 (c1 and c4 found at low severity only; c2, c3, c5 missed) | 0/5 (k5 medium verified real: okf fence close rule) |
