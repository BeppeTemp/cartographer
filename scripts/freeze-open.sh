#!/usr/bin/env bash
# freeze-open.sh — is the freeze open? (D1384, superseding the calendar of D956, D1180, D1192)
#
# Exit 0 while it is open, 1 when it is closed. The freeze is opened by a release
# candidate, not by the calendar: it is open while some candidate tag
# vX.Y.Z-rc.<YYYYMMDD> exists whose X.Y.Z is greater than the latest stable release,
# until that version is released (release.yml deletes its candidates) or the
# candidate is discarded (release-train.yml, action discard).
#
# The latest stable release is the highest vX.Y.Z (no suffix, not a draft) of the
# releases listing, by sort -V. TRAP: never `releases/latest`: a 0.x release is
# flagged pre-release and that endpoint hides it. Drafts are skipped because
# release-please creates the release as a draft and publish un-drafts it last, so
# the freeze lasts until the release is installable.
#
# A release list that cannot be read counts as open (needs GH_TOKEN, GH_REPO,
# contents: read).
#
# `--candidate` prints the oldest candidate tag of the pending version and exits
# as above; nothing is printed when closed. `--candidates` prints every candidate
# of the pending version, one per line. One place that knows the rule.
#
# Tests inject the releases: FREEZE_RELEASES="<tag>:<draft> ..." (draft = true|false).
set -euo pipefail

mode=${1:-}

if [ -n "${FREEZE_RELEASES+x}" ]; then
  releases=$(printf '%s\n' $FREEZE_RELEASES | tr ':' ' ')
else
  releases=$(gh api --paginate "repos/$GH_REPO/releases?per_page=100" \
    --jq '.[] | "\(.tag_name) \(.draft)"') || {
    echo "freeze-open: cannot list the releases (needs GH_TOKEN, GH_REPO, contents: read): freeze counted as open" >&2
    exit 0
  }
fi

stable=$(awk '$1 ~ /^v[0-9]+\.[0-9]+\.[0-9]+$/ && $2 != "true" {print $1}' <<<"$releases" | sort -V | tail -1)
# Candidates are not filtered on draft: preview.yml publishes them as pre-releases.
cands=$(awk '$1 ~ /^v[0-9]+\.[0-9]+\.[0-9]+-rc\.[0-9]{8}$/ {print $1}' <<<"$releases")

newer() { # newer <a> <b>: a is strictly greater than b (b may be empty)
  [ -z "$2" ] || { [ "$1" != "$2" ] && [ "$(printf '%s\n%s\n' "$1" "$2" | sort -V | tail -1)" = "$1" ]; }
}

# Candidates newer than the stable, then only those of the highest such version.
newer_cands=""
top=""
while read -r tag; do
  [ -n "$tag" ] || continue
  v=${tag%%-rc.*}
  if newer "$v" "$stable"; then
    newer_cands="$newer_cands$tag"$'\n'
    if newer "$v" "$top" || [ -z "$top" ]; then top=$v; fi
  fi
done <<<"$cands"

[ -n "$top" ] || exit 1
# Oldest first: the date suffix orders the candidates of one version.
pending=$(grep -F "$top-rc." <<<"$newer_cands" | sort -t. -k4,4n)

case "$mode" in
  --candidate) echo "$pending" | head -1 ;;
  --candidates) echo "$pending" ;;
esac
exit 0
