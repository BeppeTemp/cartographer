FROM golang:1.26-alpine AS builder
ARG VERSION=dev
WORKDIR /src
# Copy go.mod and go.sum first for cached dependency download.
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w -X main.version=${VERSION}" -o /cartographer ./cmd/cartographer

# Test stage: run locally with `docker build --target test .` for a clean-room
# vet+test (the build context is the only source channel inside the container).
FROM builder AS test
RUN apk add --no-cache git
# The git-backed tests (gitx, kb, bootstrap) need an identity and the "master"
# initial branch (bare container environment, no ~/.gitconfig).
RUN git config --global user.name ci && git config --global user.email ci@local \
    && git config --global init.defaultBranch master
RUN go vet ./... && go test ./...

# Dist stage: cross-compiles the client for the supported platforms.
FROM builder AS dist
ARG VERSION=dev
RUN set -e; \
    for target in darwin/arm64 darwin/amd64 linux/amd64 linux/arm64; do \
        GOOS=${target%/*} GOARCH=${target#*/} CGO_ENABLED=0 \
        go build -ldflags="-s -w -X main.version=${VERSION}" \
        -o /dist/cartographer-${target%/*}-${target#*/} ./cmd/cartographer; \
    done

# Artifacts stage: binaries only, exportable with `docker build --target artifacts -o dist .`
FROM scratch AS artifacts
COPY --from=dist /dist /

# Final stage (default): server runtime image.
FROM alpine:3.21
RUN apk add --no-cache git sops age openssh-client
COPY --from=builder /cartographer /usr/local/bin/cartographer
RUN adduser -D -h /home/cartographer cartographer
USER cartographer
WORKDIR /home/cartographer
ENTRYPOINT ["cartographer"]
