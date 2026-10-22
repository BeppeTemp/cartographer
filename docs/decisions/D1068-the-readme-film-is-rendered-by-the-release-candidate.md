---
topic: deployment-release
---

# D1068 — The README film is rendered by the release candidate, and a failed render blocks the train

**Decision.** This amends D807, which deferred "recording the film in CI". The `film` workflow (`film.yml`) renders
the launch film headless on `ubuntu-latest` (`film/render.sh`) and force-pushes it as the single commit of the orphan
branch `readme-film` (`film/publish.sh`), with `GITHUB_TOKEN` only. It is called by a `film` job of `preview.yml`, so it
runs in the Saturday release candidate, and it renders only when the hash of the film's input set (`film/inputs.sh`)
differs from the `Film-Tree:` trailer of the branch's commit (`film/film-needed.sh`). The job has no `continue-on-error`:
a render failure turns the candidate run red, and `release-train.yml`, which merges only on a successful candidate that
is the head of `main`, skips the Monday train.

**Why.** The maintainer wants the README film always present and current: the project must always be presentable. The
candidate is built from the head of `main` and the Monday gate requires that head, so a film that gets released was
rendered from the released sources, with no change to `release-train.yml`. The film imports the Atlas engine, so a
camera refactor in `web/src/lib/graph3d/scene.ts` breaks its render without failing a test; a blocking job is the test.
This differs from D790, whose hero runs after the tag and "never blocks a release". The cost is a longer candidate run
and a train that can now wait on a film render.

**Alternatives rejected.**
- *A required status check on the release PR through the `main-protection` ruleset.* A repository setting outside the
  code, and the release PR changes only `CHANGELOG` and version files, so a path-based check there says nothing about
  the film.
- *Rendering in `release.yml` after the tag, like the hero.* The failure would come after the release, when it can no
  longer stop it.
- *Hashing `web/` or `film/` and `graph3d/` only.* The film also imports `palette.ts`, `theme.ts` (through the engine),
  two brand files, the tokens and `three` (via the lockfile); a directory hash would re-render on every UI change.
  `internal/repodocs` checks the input list against the film's imports.
- *A GPU runner.* The film renders on the same SwiftShader path as the hero and the browser suite.

**Consequences.** A new import of the film outside `film/inputs.sh` fails `go test ./internal/repodocs`. Only `film.yml`
exercises the render. A manual `preview.yml` run also runs the job (not needed means green). The job holds
`contents: write` at job level in `preview.yml` only.
