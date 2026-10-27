#!/usr/bin/env bash
# dependabot-eligible.sh <ecosystem> [update-type...] — may a bot arm auto-merge
# on this Dependabot PR? Exit 0 if so (D329, D1020, D1180). The one rule for
# dependabot-automerge.yml and dependabot-arm.yml.
#
# A major bump stays open for a human, and so does a base-image bump: `docker` is
# not a required check, so auto-merge would wait for `test` alone, which never
# builds the image.
set -euo pipefail
eco=${1:?ecosystem}
shift
[ "$eco" = docker ] && exit 1
for t in "$@"; do
  [ "$t" = version-update:semver-major ] && exit 1
done
exit 0
