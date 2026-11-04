#!/usr/bin/env bash
# ci-gate.sh — the verdict of ci.yml's `gate` job, the one required CI check.
#
# A script, not inline workflow shell, so internal/repodocs/ci_gate_test.go pins
# the decision table without GitHub (as ci-filters.sh does, D994). Everything
# arrives through the environment, never interpolated into shell:
#
#   EVENT          github.event_name
#   TEST_RESULT    needs.test.result
#   WEB_RESULT     needs.web-leg.result
#   CHANGES_RESULT needs.changes.result
#   CHANGES_WEB    needs.changes.outputs.web
#   HEAD_SHA       github.event.pull_request.head.sha (pull_request only)
#   REPO           github.repository
#   GH_TOKEN       a token that reads statuses and collaborators' permission
#   GATE_POLL_TRIES, GATE_POLL_INTERVAL   how long to wait for a signoff (default 13 x 15 s)
#
# On a pull_request where `test` was skipped (the only reason is that the PR is a
# maintainer's) the proof is a `signoff` commit status in state success on the head
# sha, created by someone with admin, maintain or write access (make signoff posts
# it). Anywhere else the proof is `test` and the web legs having succeeded, a skipped
# web leg counting only when `changes` said the PR does not need it.
set -euo pipefail

fail() { echo "gate: $*" >&2; exit 1; }

if [ "${EVENT:-}" = pull_request ] && [ "${TEST_RESULT:-}" = skipped ]; then
  tries=${GATE_POLL_TRIES:-13}
  interval=${GATE_POLL_INTERVAL:-15}
  i=0
  while :; do
    i=$((i + 1))
    # An unreadable answer is no signoff, and no permission.
    logins=$(gh api "repos/$REPO/commits/$HEAD_SHA/statuses?per_page=100" 2>/dev/null |
      jq -r '.[] | select(.context == "signoff" and .state == "success") | .creator.login' 2>/dev/null |
      sort -u || true)
    for login in $logins; do
      perm=$(gh api "repos/$REPO/collaborators/$login/permission" 2>/dev/null | jq -r .permission 2>/dev/null || true)
      case "$perm" in
        admin|maintain|write)
          echo "gate: signoff by $login ($perm) on $HEAD_SHA"
          exit 0 ;;
      esac
    done
    [ "$i" -lt "$tries" ] || break
    sleep "$interval"
  done
  fail "no maintainer signoff on $HEAD_SHA: run make signoff on this branch (it also re-runs this job)"
fi

[ "${TEST_RESULT:-}" = success ] || fail "test is ${TEST_RESULT:-unknown}"
case "${WEB_RESULT:-}" in
  success) ;;
  skipped)
    # Not needed is exactly: a PR whose `changes` job ran and said no frontend change.
    if [ "${EVENT:-}" = pull_request ] && [ "${CHANGES_RESULT:-}" = success ] && [ "${CHANGES_WEB:-}" = false ]; then
      :
    else
      fail "the web legs were skipped but this ${EVENT:-event} needs them"
    fi ;;
  *) fail "the web legs are ${WEB_RESULT:-unknown}" ;;
esac
echo "gate: test and the web legs are green"
