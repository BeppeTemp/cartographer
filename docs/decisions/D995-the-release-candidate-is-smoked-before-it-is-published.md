---
topic: deployment-release
---

# D995 — The release candidate is smoked before it is published

**Decision.** `preview.yml` builds the candidate for the runner's own architecture
(amd64), runs `test/smoke/image.sh` on it, and only then logs in and pushes the
multi-arch image; a red smoke fails the run before anything reaches ghcr. `ci.yml`
has a `docker` job that runs the Dockerfile's `test` stage and the same smoke (plus
`make demo-smoke` on top of that image) on a PR that touches the container's inputs,
on the release PR and on `main`. `release-train.yml` names a failed candidate
build in its skip comment.

**Why.** Nothing started the image before a maintainer tried it: `preview.yml` only
pushed, the `test` stage was never built by a workflow, and the final stage's tools
(`git`, `sops`, `age`, `ssh`) and its non-root user were checked by nobody. A broken
candidate was found by hand on Saturday or, worse, as a skipped train whose comment
said the candidate was "not the head of main". The cost is one more amd64 build in
the candidate run (the push reuses its layers from the GitHub cache) and a
`docker` job on PRs that touch the Dockerfile, `go.mod`/`go.sum`, `deploy/demo/`,
`demo/guestbook/` or the smoke itself.

**Alternatives rejected.** Smoking the image after the push: a red image would
already be published under the candidate tag. Running the smoke on arm64 under QEMU:
minutes of emulation for a check that the runtime stage's contents (identical across
architectures) already pass. A Go test instead of a shell script: the thing under
test is `docker run`, and `deploy/demo/smoke.sh` is already the precedent. Adding
`docker` to the required checks: a ruleset change only the maintainer makes; the
release PR and the candidate run it anyway, and `main-status` watches it.

**Consequences.** The `test` stage now runs in CI, so a test that depends on the host
breaks that job, not only `make gate`. `test/smoke/image.sh` and `test/lib/mcp.sh`
are inputs of the `docker` filter in `scripts/ci-filters.sh`. The Makefile targets
`docker` and `docker-smoke` are local conveniences: the workflows use buildx for its
layer cache and call the script themselves. A trap for local runs: a git worktree's
`.git` is a file, `COPY . .` carries it into the image, and `git config --global`
then dies with "not a git repository"; run `docker build --target test .` from a
clone, as CI does.

`make demo-smoke` had rotted unseen since the write gate (D907): the Worker sends
`concept_new` without `index_entry`, and the seed's `messages` journal kept a curated
index, so the guestbook write was refused. The seed's `messages/_map.md` now says
`index: generated` (D301); the live guestbook reaches it only through its private
remote, like every change to `demo/guestbook/`.
