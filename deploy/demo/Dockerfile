# The live read-only MCP demo (D751): the released server image plus the
# demo/astronomy KB, baked in with its replayed history.
#
# Build context: the repository root.
#   docker build -f deploy/demo/Dockerfile --build-arg BASE=<server image> .
ARG BASE=ghcr.io/beppetemp/cartographer:latest

FROM alpine:3.21 AS kb
RUN apk add --no-cache bash git
COPY demo/astronomy /src/demo/astronomy
COPY demo/replay.sh /src/demo/replay.sh
RUN /src/demo/replay.sh /kb/astronomy

FROM ${BASE}
COPY --from=kb --chown=cartographer:cartographer /kb/astronomy /home/cartographer/astronomy
# 8080 is Cloud Run's default port, so the server needs no PORT handling. The
# token is not baked in: CARTOGRAPHER_TOKENS comes from the Cloud Run service,
# so rotating it is a deploy, not a rebuild. Auth stays on: `off` would give admin.
ENV CARTOGRAPHER_KB=/home/cartographer/astronomy \
    CARTOGRAPHER_HTTP=:8080 \
    CARTOGRAPHER_AUTH=on \
    CARTOGRAPHER_UPDATE_CHECK=false
CMD ["serve"]
