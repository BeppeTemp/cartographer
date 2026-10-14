# The live MCP demo (D751, D800): the released server image plus the
# demo/astronomy KB, baked in with its replayed history (read-only), and the
# guestbook KB, cloned from its private remote at start (entrypoint.sh).
#
# Build context: the repository root.
#   docker build -f deploy/demo/Dockerfile --build-arg BASE=<server image> .
ARG BASE=ghcr.io/beppetemp/cartographer:latest

FROM alpine:3.21 AS kb
RUN apk add --no-cache bash git
COPY demo/astronomy /src/demo/astronomy
COPY demo/replay.sh /src/demo/replay.sh
RUN /src/demo/replay.sh /kb/astronomy && mkdir /kb/data

FROM ${BASE}
COPY --from=kb --chown=cartographer:cartographer /kb/astronomy /home/cartographer/astronomy
# The guestbook clone lands in the (empty) data directory, where the server
# discovers it beside the explicit astronomy KB.
COPY --from=kb --chown=cartographer:cartographer /kb/data /home/cartographer/data
COPY deploy/demo/entrypoint.sh /usr/local/bin/demo-entrypoint.sh
# 8080 is Cloud Run's default port, so the server needs no PORT handling. The
# token is not baked in: CARTOGRAPHER_TOKENS comes from the Cloud Run service,
# so rotating it is a deploy, not a rebuild. Auth stays on: `off` would give admin.
# The push after a guestbook write is synchronous (SYNC_OUT_DEBOUNCE=0): Cloud
# Run throttles the CPU between requests, which would strand a pending push.
# GUESTBOOK_REMOTE and the git token file come from the Cloud Run service.
ENV CARTOGRAPHER_KB=/home/cartographer/astronomy \
    CARTOGRAPHER_DATA=/home/cartographer/data \
    CARTOGRAPHER_HTTP=:8080 \
    CARTOGRAPHER_AUTH=on \
    CARTOGRAPHER_UPDATE_CHECK=false \
    CARTOGRAPHER_SYNC_OUT_DEBOUNCE=0 \
    CARTOGRAPHER_GIT_TOKEN_DIR=/etc/guestbook-git \
    GIT_AUTHOR_NAME="Cartographer guestbook" \
    GIT_AUTHOR_EMAIL=noreply@example.com \
    GIT_COMMITTER_NAME="Cartographer guestbook" \
    GIT_COMMITTER_EMAIL=noreply@example.com
ENTRYPOINT ["demo-entrypoint.sh"]
CMD ["serve"]
