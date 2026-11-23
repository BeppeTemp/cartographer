---
topic: deployment-release
---

# D2313 — The Atlas bundle stays committed

**Decision.** The built Atlas UI, `internal/webui/dist`, stays a tracked directory of the repository. The binary embeds it from the source tree (`go:embed all:dist` in `internal/webui/webui.go`), `TestBundleMatchesSource` fails when it no longer matches `web/`, and the lander builds it again for the commits whose numbering made it stale (D2128).

**Why.** The bundle is part of what `go install …@latest` compiles, and that install path is documented (`README.md`, `docs/getting-started.md`): a module fetched by the Go tooling has no Node step, so an untracked bundle would build a binary with no UI. The gate is Go-only on purpose (D227: the Go checks do not need Node), and no release build (`.goreleaser.yaml`, `Dockerfile`, `release.yml`, `preview.yml`) runs `make web`, `npm` or `node`. The cost is generated files in the diffs and a conflict class on `internal/webui/dist`, which the lander already resolves at pick time and after numbering (D2128).

**Alternatives rejected.**
- Build the bundle in CI and at release only: it removes the conflict class and the generated files from the diffs, but breaks `go install` and the Go-only gate unless a stub bundle and a Node step are added to every build path.
- Commit it only on release branches: the repository has no release branches, so it would be a new branching model for one directory.

**Consequences.**
- Three things would have to change before the bundle can leave the tree: the documented `go install …@latest` path must stop being a supported install (or a published module must carry the bundle), the Go-only gate must learn a Node step or a stub bundle, and the release builds (goreleaser, the Dockerfile, the preview workflow) must run `make web` first.
- The conflict class stays and stays the lander's: a conflict confined to `internal/webui/dist` is rebuilt at the pick, and a citation under `web/` rewritten by the numbering is rebuilt after it.
