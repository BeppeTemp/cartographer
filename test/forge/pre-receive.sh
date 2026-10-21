#!/bin/sh
# A forge's server-side push rules in one portable pre-receive hook (D996), shared
# by the Go tests and the e2e scenarios so neither carries its own caricature.
# Install it as <bare>/hooks/pre-receive (mode 0755); it is configured by files
# next to it, in <bare>/hooks/:
#
#   allowed-identities  one email glob per line, `#` comments. Every new commit's
#                       author AND committer email must match one, or the push is
#                       refused with the wording of GitLab's member check. No file:
#                       every identity is allowed.
#   protected-branch    the branch name (a glob; `*` for every branch) to refuse
#                       with GitHub's GH006 wording. An optional second line
#                       replaces that wording.
#
# POSIX sh and LF only (.gitattributes forces LF): git runs hooks with its bundled
# sh on Windows too, so no bashism and no external tool beyond git.
hooks=$(dirname "$0")
zero=0000000000000000000000000000000000000000

while read -r old new ref; do
  [ "$new" = "$zero" ] && continue # a branch deletion carries no commit

  if [ -f "$hooks/protected-branch" ]; then
    protected=$(sed -n 1p "$hooks/protected-branch")
    case "${ref#refs/heads/}" in
      $protected)
        message=$(sed -n 2p "$hooks/protected-branch")
        [ -n "$message" ] || message="remote: error: GH006: Protected branch update failed for $ref."
        echo "$message" >&2
        exit 1
        ;;
    esac
  fi

  [ -f "$hooks/allowed-identities" ] || continue
  if [ "$old" = "$zero" ]; then range="$new --not --all"; else range="$old..$new"; fi
  for c in $(git rev-list $range); do
    for email in $(git log -1 --format='%ae %ce' "$c"); do
      ok=
      while IFS= read -r glob; do
        case "$glob" in '' | '#'*) continue ;; esac
        # shellcheck disable=SC2254
        case "$email" in $glob) ok=1 ;; esac
      done <"$hooks/allowed-identities"
      if [ -z "$ok" ]; then
        echo "GitLab: $email is not a member of team" >&2
        exit 1
      fi
    done
  done
done
exit 0
