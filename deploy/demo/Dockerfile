# The live MCP demo (D751, D2053): the released server image plus two
# read-only KBs baked in. astronomy is demo/astronomy with its replayed history;
# cartographer-docs is the documentation of the very binary in the image, which
# `cartographer docs export` writes at build time and a single commit seals. Both
# are in the image, so nothing external (a remote, a token) can expire.
#
# Build context: the repository root.
#   docker build -f deploy/demo/Dockerfile --build-arg BASE=<server image> .
# BASE must carry `cartographer docs export` (a release from 0.29 on).
ARG BASE=ghcr.io/beppetemp/cartographer:latest

# COPY --from takes no variable, so the base gets a stage name.
FROM ${BASE} AS base

FROM alpine:3.24@sha256:71e9b7bae160ba167fa17414401b94cbcdaf3c32feeab9298bd794a2a0614c35 AS kb
RUN apk add --no-cache bash git
COPY demo/astronomy /src/demo/astronomy
COPY demo/replay.sh /src/demo/replay.sh
# The binary is static (CGO_ENABLED=0), so it runs on this stage's libc.
COPY --from=base /usr/local/bin/cartographer /usr/local/bin/cartographer
RUN /src/demo/replay.sh /kb/astronomy && mkdir /kb/data
RUN cartographer docs export /kb/cartographer-docs \
    && cd /kb/cartographer-docs \
    && git init -q -b main \
    && git add -A \
    && git -c user.name=demo -c user.email=demo@example.com commit -qm "The documentation of this release"

FROM ${BASE}
COPY --from=kb --chown=cartographer:cartographer /kb/astronomy /home/cartographer/astronomy
COPY --from=kb --chown=cartographer:cartographer /kb/cartographer-docs /home/cartographer/cartographer-docs
# An empty, writable data directory: the server keeps its credentials directory
# in it (<data>/credentials) and generates there the key that signs its audit log.
COPY --from=kb --chown=cartographer:cartographer /kb/data /home/cartographer/data
# 8080 is Cloud Run's default port, so the server needs no PORT handling. The
# token is not baked in: CARTOGRAPHER_TOKENS comes from the Cloud Run service,
# so rotating it is a deploy, not a rebuild. Auth stays on: `off` would give admin.
ENV CARTOGRAPHER_KB=/home/cartographer/astronomy,/home/cartographer/cartographer-docs \
    CARTOGRAPHER_DATA=/home/cartographer/data \
    CARTOGRAPHER_HTTP=:8080 \
    CARTOGRAPHER_AUTH=on \
    CARTOGRAPHER_UPDATE_CHECK=false
CMD ["serve"]
