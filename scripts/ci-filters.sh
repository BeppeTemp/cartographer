#!/usr/bin/env bash
# Which of the slower CI jobs a pull request needs (D994). Reads the changed
# paths on stdin, one per line, takes the PR's head ref as $1 and prints
# `name=true|false` lines, ready for `>> "$GITHUB_OUTPUT"`.
#
# It is a script, not inline workflow shell, so internal/repodocs/ci_filters_test.go
# can pin the table without GitHub. Narrowing a filter never removes a check from
# `main`, where every job always runs: a miss shows right after the merge.
#
#   web      the frontend and the Go that ends up in bin/cartographer, the only
#            thing the browser suite drives. NOT a *_test.go file, and NOT the
#            packages the binary does not import (ci_filters_test.go asserts that
#            list against `go list -deps`). Non-Go files under cmd/ and
#            internal/ stay in: skills and templates are embedded.
#   windows  packages with platform-specific files or behaviour.
#   demo     the demo Worker or its Terraform (job demo-edge).
#   tf       the Terraform alone (a step of demo-edge).
#   docker   what the container is built from (job docker, D995).
#   macos    the launchd service, install.sh and the Cask (job test-macos, D997):
#            a macOS runner queues slowly, so only what can break them, and the
#            release PR.
#   forge    the real-forge tests and the hook they share (job forge, D996): not
#            an ordinary PR, which would pay minutes of GitLab CE boot for a
#            change that cannot touch it. The release PR runs it like everything.
set -euo pipefail

head_ref=${1:-}
files=$(cat)

# Not the rebuilt UI bundle: its provenance test is platform-independent and
# runs on `test`.
paths=$(printf '%s\n' "$files" | grep -v '^internal/webui/dist/' || true)

# The release PR runs everything: it is the one full pass over what the merged
# PRs add up to before a tag.
release=false
[[ "$head_ref" == release-please--* ]] && release=true

any() { # any <ere> [<ere to drop>]
  [ "$release" = true ] && { echo true; return; }
  local hit
  hit=$(printf '%s\n' "$paths" | grep -E "$1" || true)
  if [ -n "${2:-}" ]; then hit=$(printf '%s\n' "$hit" | grep -vE "$2" || true); fi
  [ -n "$hit" ] && echo true || echo false
}

# Go packages the binary cannot import (kept equal to `go list` by the test).
not_in_binary='^internal/(changelog|gitx/gitxtest|kbtest|repodocs|testkbs|testtree|webui/gen)/'

web_other=$(any '^(web/|test/lib/|go\.(mod|sum)$|Makefile$|\.github/workflows/ci\.yml$|scripts/ci-filters\.sh$)')
web_go=$(any '^(cmd|internal)/' '_test\.go$|'"$not_in_binary")
if [ "$web_other" = true ] || [ "$web_go" = true ]; then web=true; else web=false; fi

echo "web=$web"
echo "windows=$(any '^(cmd/|internal/(service|provisioning|configurator|clientconfig|mcpbridge|agents|execbit|gitx|kb|repoindex|defaults)/|install\.(sh|ps1)$|test/install/|test/forge/pre-receive\.sh$|go\.(mod|sum)$|Makefile$|\.github/workflows/ci\.yml$|scripts/ci-filters\.sh$)|_windows(_test)?\.go$')"
echo "docker=$(any '^(Dockerfile|\.dockerignore|go\.(mod|sum)|test/smoke/image\.sh|test/lib/mcp\.sh|deploy/demo/|demo/guestbook/)|^(\.github/workflows/ci\.yml|scripts/ci-filters\.sh)$')"
echo "tf=$(any '^deploy/demo/.*\.(tf|tfvars)$')"
echo "demo=$(any '^deploy/demo/(worker\.js|worker\.test\.mjs)$|^deploy/demo/.*\.(tf|tfvars)$|^(\.github/workflows/ci\.yml|scripts/ci-filters\.sh)$')"
echo "forge=$(any '^test/forge/|^(\.github/workflows/ci\.yml|scripts/ci-filters\.sh)$')"
echo "macos=$(any '^(internal/service/|install\.sh$|\.goreleaser\.yaml$|test/(install|macos)/|cmd/cartographer/(service|upgraderepair)[^/]*$|\.github/workflows/ci\.yml$|scripts/ci-filters\.sh$)')"
