#!/bin/sh
# Entrypoint of the live demo image (D800): clones the guestbook KB when it can,
# then runs the server with the image's arguments.
#
# Why the clone happens here and not in the server (`kbs[].remote`,
# CARTOGRAPHER_KB_REMOTES): there a remote that fails to clone is fatal at
# startup (cmd/cartographer/serve.go), and a guestbook outage must never take
# the astronomy demo down. Here a failure is one log line and the server starts
# with astronomy alone. The server then finds the clone by itself, because it
# lives under CARTOGRAPHER_DATA.
#
# Trap: the KB name is the remote's basename without ".git" (internal/config/
# origin.go), so the repository behind GUESTBOOK_REMOTE must be named
# "guestbook" - the clone is placed at $CARTOGRAPHER_DATA/guestbook to match.
set -eu

data=${CARTOGRAPHER_DATA:-}
remote=${GUESTBOOK_REMOTE:-}
token=${CARTOGRAPHER_GIT_TOKEN_DIR:-/nonexistent}/guestbook.token
dest=$data/guestbook

if [ -n "$remote" ] && [ -n "$data" ] && [ -f "$token" ] && [ ! -d "$dest/.git" ]; then
  # The same credential helper the server builds (cmd/cartographer/bootstrap.go
  # gitTokenCredentialEnv): the token is read from its file when git asks for
  # it, so it is never in argv, in the URL or in .git/config. The server keeps
  # using it for pull and push through CARTOGRAPHER_GIT_TOKEN_DIR.
  helper="!f() { echo username=token; echo \"password=\$(cat '$token')\"; }; f"
  if GIT_TERMINAL_PROMPT=0 GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=credential.helper \
    GIT_CONFIG_VALUE_0=$helper timeout 20 git clone --quiet -- "$remote" "$dest" >/dev/null 2>&1; then
    echo "entrypoint: guestbook cloned from $remote" >&2
  else
    # git's own stderr is dropped: it can echo the URL a helper was asked for.
    echo "entrypoint: guestbook not cloned from $remote, starting without it" >&2
    rm -rf "$dest"
  fi
fi

exec cartographer "$@"
