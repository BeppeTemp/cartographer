#!/usr/bin/env bash
# issue-new.sh — a maintainer's session opens an accepted bug that says where it was found (make issue).
#
#   make issue TITLE="<title>" BODY=<file> FOUND_BY=<value> [INTRODUCED_BY=<ref>]
#
# Opens a `bug` issue labelled `accepted` (CONTRIBUTING.md §Issues: a maintainer's own bug is
# triaged at creation) whose body is BODY's, then the lines a fix copies (scripts/ready.sh):
# `Found-by: <value>`, a value of scripts/found-by.tsv (`trailers-lint.sh --values`), and
# `Introduced-by: <ref>` when given (a commit, `#<n>`, a landed slug or `unknown`; its shape only
# is checked here, the fix's trailers-lint.sh looks it up). Prints the issue's URL. Exit 2 on a bad
# call, 1 when gh fails. Only a maintainer opens one: the labels need triage access.
# internal/repodocs/landertest/issue_loop_test.go pins it. Plain bash 3.2.
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
bad() { echo "issue: $*" >&2; exit 2; }
[ -n "${TITLE:-}" ] || bad "TITLE=<title> is required"
[ -n "${BODY:-}" ] && [ -s "$BODY" ] || bad "BODY=<file> with the issue's body is required"
values=$("$here/trailers-lint.sh" --values)
[ -n "${FOUND_BY:-}" ] && grep -qxF -- "$FOUND_BY" <<<"$values" ||
  bad "FOUND_BY=<value> is required, one of: $(tr '\n' ' ' <<<"$values")"
[ -z "${INTRODUCED_BY:-}" ] || [[ "$INTRODUCED_BY" =~ ^[A-Za-z0-9#._/-]+$ ]] ||
  bad "INTRODUCED_BY=$INTRODUCED_BY is not a ref (a commit, #<n>, a landed slug or unknown)"
! grep -qiE '^(found-by|introduced-by)[[:space:]]*:' "$BODY" || bad "BODY already has a Found-by or Introduced-by line: pass them as FOUND_BY and INTRODUCED_BY"
repo=$("$here/upstream-repo.sh")
body=$(cat "$BODY")$'\n\n'"Found-by: $FOUND_BY"
[ -z "${INTRODUCED_BY:-}" ] || body+=$'\n'"Introduced-by: $INTRODUCED_BY"
gh issue create -R "$repo" --label bug --label accepted --title "$TITLE" --body "$body"
