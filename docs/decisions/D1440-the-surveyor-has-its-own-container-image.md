---
topic: deployment-release
---

# D1440 — The Surveyor has its own container image

**Decision.** Two images are published from one Dockerfile and one builder stage. `ghcr.io/beppetemp/cartographer` stays the server image: name, content and default build unchanged. `ghcr.io/beppetemp/cartographer-surveyor` is the new `surveyor` stage, placed before the final stage: same binary, `git` and the CA bundle Alpine already ships, no `sops`, `age` or `openssh-client`, the non-root user `cartographer`, no MCP Registry label, entrypoint `cartographer surveyor`. It is the documented base of exec-recipe derived images. The release and the candidate publish both, multi-arch, each attested on its own digest (the candidate pushes `:<tag>` only, never `latest`, never attested). The server image still runs a Surveyor with `surveyor` as its first argument.

**Why.** The Surveyor shells out to `git` only, anonymous and https-only through `internal/gitx` (so `GIT_ALLOW_PROTOCOL=https` and `httpsRemote` keep ssh unreachable), and talks to models and the Server over Go `net/http`, which reads the system roots. It never imports `internal/sops`. A process that reads sources and calls a model has no reason to carry the server's secret and ssh tooling. The operator's own commands (exec recipes, stdio connectors) are what derived images are for.

**Alternatives rejected.**
- A second job per workflow: the builder stages are shared, and inside one buildx builder the second build reuses the first's Go layers (the arm64 compile runs under QEMU); `publish` needs `docker` and `binaries` of `preview.yml` must need exactly the job that smokes the image, so one job holding both builds satisfies "the chain waits for both" with no `needs:` change.
- A second Dockerfile: it forks the builder and the pinned base.
- An image of the Surveyor's own binary: it reverses D1439 (one binary).
- Naming or reordering the server stage: the last stage is the default target of `docker build .`, the demo's `BASE` and the CI; it must stay the server.
- An Atlas or client image: the Atlas is embedded in the Server, the client is a binary in the agent's image (D1439).

**Consequences.** With this entrypoint `docker run <image> version` hands `version` to the Surveyor: a shell or the version needs `--entrypoint`. A derived image runs `apk` as `root` and switches back to `cartographer`. `internal/repodocs/dockerfile_test.go` pins the stage order, the absence of the label and of the secret tooling, the entrypoint and the shared pinned base; `release_publish_test.go` pins that each image is attested with the digest of the step that pushed it; `release_train_test.go` pins that both smokes precede the first candidate push and that no `latest` Surveyor candidate exists. The first release candidate creates the GHCR package `cartographer-surveyor`: a maintainer sets it Public and checks that Actions has Write access (private by default; the push is denied otherwise).
